package management

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	panel "ex-otogi/pkg/otogi/management"
)

const defaultEventLimit = 100

// Limits configures sliding-window retention for the in-memory store.
type Limits struct {
	// MaxEvents caps the number of retained timeline events. Non-positive values
	// disable event retention.
	MaxEvents int
	// MaxArtifacts caps the number of retained artifacts. Non-positive values
	// disable artifact retention.
	MaxArtifacts int
	// MaxArtifactBytes caps the total bytes retained across all artifacts.
	// Non-positive values disable artifact retention.
	MaxArtifactBytes int
	// MaxSnapshots caps the number of replace-in-place snapshots retained.
	// Non-positive values disable snapshot retention.
	MaxSnapshots int
}

// Service is a concurrency-safe in-memory management recorder and query store.
type Service struct {
	nextEventID atomic.Int64

	mu               sync.RWMutex
	limits           Limits
	events           []panel.Event
	artifactsByID    map[string]panel.Artifact
	artifactOrder    []string
	artifactBytes    int
	artifactsByEvent map[int64]map[string]struct{}
	snapshotsByKey   map[string]panel.Snapshot
	snapshotOrder    []string
}

// NewService constructs one in-memory observability service with the provided
// retention limits.
func NewService(limits Limits) *Service {
	return &Service{
		limits:           limits,
		artifactsByID:    make(map[string]panel.Artifact),
		artifactsByEvent: make(map[int64]map[string]struct{}),
		snapshotsByKey:   make(map[string]panel.Snapshot),
	}
}

// RecordEvent appends one event to the retained sliding window.
func (s *Service) RecordEvent(ctx context.Context, event panel.Event) (panel.Event, error) {
	if err := ctx.Err(); err != nil {
		return panel.Event{}, fmt.Errorf("record event: %w", err)
	}

	record := cloneEvent(event)
	record.ID = s.nextEventID.Add(1)
	if record.OccurredAt.IsZero() {
		record.OccurredAt = time.Now().UTC()
	}
	if trace, ok := panel.TraceFromContext(ctx); ok {
		if record.TraceID == "" {
			record.TraceID = trace.TraceID
		}
		if record.ParentEventID == nil && trace.ParentEventID != nil {
			parentID := *trace.ParentEventID
			record.ParentEventID = &parentID
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.limits.MaxEvents > 0 {
		s.events = append(s.events, record)
		s.evictEventsLocked()
	}

	return cloneEvent(record), nil
}

// RecordArtifact stores one artifact subject to artifact retention limits.
func (s *Service) RecordArtifact(ctx context.Context, artifact panel.Artifact) (panel.Artifact, error) {
	if err := ctx.Err(); err != nil {
		return panel.Artifact{}, fmt.Errorf("record artifact %s: %w", artifact.ID, err)
	}
	if artifact.ID == "" {
		return panel.Artifact{}, fmt.Errorf("record artifact: empty artifact id")
	}
	if artifact.EventID <= 0 {
		return panel.Artifact{}, fmt.Errorf("record artifact %s: missing event id", artifact.ID)
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}

	record := artifact

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.limits.MaxArtifacts <= 0 || s.limits.MaxArtifactBytes <= 0 {
		return cloneArtifact(record), nil
	}
	if _, exists := s.artifactsByID[record.ID]; exists {
		return panel.Artifact{}, fmt.Errorf("record artifact %s: duplicate artifact id", record.ID)
	}
	if !s.eventRetainedLocked(record.EventID) {
		return panel.Artifact{}, fmt.Errorf("record artifact %s: event %d: %w", record.ID, record.EventID, panel.ErrEventNotFound)
	}

	s.artifactsByID[record.ID] = cloneArtifact(record)
	s.artifactOrder = append(s.artifactOrder, record.ID)
	s.artifactBytes += len(record.Content)
	if s.artifactsByEvent[record.EventID] == nil {
		s.artifactsByEvent[record.EventID] = make(map[string]struct{})
	}
	s.artifactsByEvent[record.EventID][record.ID] = struct{}{}
	for index := range s.events {
		if s.events[index].ID != record.EventID {
			continue
		}
		s.events[index].ArtifactIDs = append(s.events[index].ArtifactIDs, record.ID)
		break
	}
	s.evictArtifactsLocked()

	if _, retained := s.artifactsByID[record.ID]; !retained {
		return cloneArtifact(record), nil
	}

	return cloneArtifact(record), nil
}

// UpsertSnapshot replaces one snapshot identified by namespace and key.
func (s *Service) UpsertSnapshot(ctx context.Context, snapshot panel.Snapshot) (panel.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return panel.Snapshot{}, fmt.Errorf("upsert snapshot %s/%s: %w", snapshot.Namespace, snapshot.Key, err)
	}
	if snapshot.Namespace == "" {
		return panel.Snapshot{}, fmt.Errorf("upsert snapshot: empty namespace")
	}
	if snapshot.Key == "" {
		return panel.Snapshot{}, fmt.Errorf("upsert snapshot %s: empty key", snapshot.Namespace)
	}

	record := cloneSnapshot(snapshot)
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
	}
	storageKey := snapshotStorageKey(record.Namespace, record.Key)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.limits.MaxSnapshots <= 0 {
		return cloneSnapshot(record), nil
	}

	if _, exists := s.snapshotsByKey[storageKey]; !exists {
		s.snapshotOrder = append(s.snapshotOrder, storageKey)
	}
	s.snapshotsByKey[storageKey] = record
	s.evictSnapshotsLocked()

	return cloneSnapshot(record), nil
}

// ListEvents returns retained events newer than the requested cursor.
func (s *Service) ListEvents(ctx context.Context, request panel.EventQuery) (panel.EventPage, error) {
	if err := ctx.Err(); err != nil {
		return panel.EventPage{}, fmt.Errorf("list events: %w", err)
	}
	if request.AfterID < 0 {
		return panel.EventPage{}, fmt.Errorf("list events: after_id %d: %w", request.AfterID, panel.ErrInvalidQuery)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	page := panel.EventPage{
		WindowStartID: currentWindowStartID(s.events),
		WindowEndID:   currentWindowEndID(s.events),
		LastID:        currentWindowEndID(s.events),
	}
	if len(s.events) == 0 {
		return page, nil
	}

	if request.AfterID > 0 && request.AfterID < page.WindowStartID {
		page.CursorResetRequired = true
	}

	matches := make([]panel.Event, 0, len(s.events))
	for _, event := range s.events {
		if event.ID <= request.AfterID {
			continue
		}
		if !matchesEventQuery(event, request) {
			continue
		}
		matches = append(matches, cloneEvent(event))
	}

	limit := request.Limit
	if limit <= 0 {
		limit = defaultEventLimit
	}
	if limit < len(matches) {
		page.Items = matches[:limit]
		page.HasMore = true
		return page, nil
	}

	page.Items = matches
	return page, nil
}

// GetEvent returns one retained event by ID.
func (s *Service) GetEvent(ctx context.Context, id int64) (panel.Event, error) {
	if err := ctx.Err(); err != nil {
		return panel.Event{}, fmt.Errorf("get event %d: %w", id, err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, event := range s.events {
		if event.ID == id {
			return cloneEvent(event), nil
		}
	}

	return panel.Event{}, fmt.Errorf("get event %d: %w", id, panel.ErrEventNotFound)
}

// GetTrace returns retained events for one trace ID.
func (s *Service) GetTrace(ctx context.Context, request panel.TraceQuery) (panel.TraceView, error) {
	if err := ctx.Err(); err != nil {
		return panel.TraceView{}, fmt.Errorf("get trace %s: %w", request.TraceID, err)
	}
	if request.TraceID == "" {
		return panel.TraceView{}, fmt.Errorf("get trace: empty trace id: %w", panel.ErrInvalidQuery)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	view := panel.TraceView{
		TraceID: request.TraceID,
	}
	for _, event := range s.events {
		if event.TraceID != request.TraceID {
			continue
		}
		view.Items = append(view.Items, cloneEvent(event))
	}
	if request.Limit > 0 && len(view.Items) > request.Limit {
		view.Items = view.Items[len(view.Items)-request.Limit:]
	}

	return view, nil
}

// GetArtifact returns one retained artifact by ID.
func (s *Service) GetArtifact(ctx context.Context, id string) (panel.Artifact, error) {
	if err := ctx.Err(); err != nil {
		return panel.Artifact{}, fmt.Errorf("get artifact %s: %w", id, err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	artifact, ok := s.artifactsByID[id]
	if !ok {
		return panel.Artifact{}, fmt.Errorf("get artifact %s: %w", id, panel.ErrArtifactNotFound)
	}

	return cloneArtifact(artifact), nil
}

// ListSnapshots returns snapshots matching the optional filters.
func (s *Service) ListSnapshots(ctx context.Context, request panel.SnapshotQuery) (panel.SnapshotList, error) {
	if err := ctx.Err(); err != nil {
		return panel.SnapshotList{}, fmt.Errorf("list snapshots: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := panel.SnapshotList{
		Items: make([]panel.Snapshot, 0, len(s.snapshotsByKey)),
	}
	for _, storageKey := range s.snapshotOrder {
		snapshot, ok := s.snapshotsByKey[storageKey]
		if !ok {
			continue
		}
		if request.Namespace != "" && snapshot.Namespace != request.Namespace {
			continue
		}
		if request.Key != "" && snapshot.Key != request.Key {
			continue
		}
		if request.Module != "" && snapshot.Module != request.Module {
			continue
		}
		result.Items = append(result.Items, cloneSnapshot(snapshot))
	}

	return result, nil
}

// GetOverview returns a lightweight summary of the retained management data.
func (s *Service) GetOverview(ctx context.Context) (panel.Overview, error) {
	if err := ctx.Err(); err != nil {
		return panel.Overview{}, fmt.Errorf("get overview: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	overview := panel.Overview{
		WindowStartID:             currentWindowStartID(s.events),
		WindowEndID:               currentWindowEndID(s.events),
		TotalEvents:               len(s.events),
		EventCountsByCategory:     make(map[panel.EventCategory]int),
		ActiveInflightByComponent: make(map[string]int),
	}
	traceIDs := make(map[string]struct{})
	for _, event := range s.events {
		overview.EventCountsByCategory[event.Category]++
		if event.Level == panel.EventLevelError {
			overview.RecentErrorCount++
		}
		if event.TraceID != "" {
			traceIDs[event.TraceID] = struct{}{}
		}
	}
	overview.RecentTraceCount = len(traceIDs)

	return overview, nil
}

func (s *Service) evictEventsLocked() {
	if s.limits.MaxEvents <= 0 {
		s.events = nil
		return
	}
	for len(s.events) > s.limits.MaxEvents {
		evicted := s.events[0]
		s.events = s.events[1:]
		s.evictArtifactsForEventLocked(evicted.ID)
	}
}

func (s *Service) evictArtifactsLocked() {
	for len(s.artifactOrder) > 0 && (len(s.artifactOrder) > s.limits.MaxArtifacts || s.artifactBytes > s.limits.MaxArtifactBytes) {
		s.removeArtifactLocked(s.artifactOrder[0])
	}
}

func (s *Service) evictArtifactsForEventLocked(eventID int64) {
	artifactIDs := s.artifactsByEvent[eventID]
	if len(artifactIDs) == 0 {
		delete(s.artifactsByEvent, eventID)
		return
	}
	for artifactID := range artifactIDs {
		s.removeArtifactLocked(artifactID)
	}
	delete(s.artifactsByEvent, eventID)
}

func (s *Service) removeArtifactLocked(id string) {
	artifact, ok := s.artifactsByID[id]
	if !ok {
		s.artifactOrder = slices.DeleteFunc(s.artifactOrder, func(candidate string) bool {
			return candidate == id
		})
		return
	}

	delete(s.artifactsByID, id)
	s.artifactOrder = slices.DeleteFunc(s.artifactOrder, func(candidate string) bool {
		return candidate == id
	})
	s.artifactBytes -= len(artifact.Content)
	if s.artifactBytes < 0 {
		s.artifactBytes = 0
	}
	if linked := s.artifactsByEvent[artifact.EventID]; linked != nil {
		delete(linked, id)
		if len(linked) == 0 {
			delete(s.artifactsByEvent, artifact.EventID)
		}
	}
}

func (s *Service) evictSnapshotsLocked() {
	if s.limits.MaxSnapshots <= 0 {
		s.snapshotOrder = nil
		s.snapshotsByKey = make(map[string]panel.Snapshot)
		return
	}
	for len(s.snapshotOrder) > s.limits.MaxSnapshots {
		evictedKey := s.snapshotOrder[0]
		s.snapshotOrder = s.snapshotOrder[1:]
		delete(s.snapshotsByKey, evictedKey)
	}
}

func (s *Service) eventRetainedLocked(eventID int64) bool {
	for _, event := range s.events {
		if event.ID == eventID {
			return true
		}
	}
	return false
}

func snapshotStorageKey(namespace string, key string) string {
	return namespace + "\x00" + key
}

func matchesEventQuery(event panel.Event, request panel.EventQuery) bool {
	if request.Category != "" && event.Category != request.Category {
		return false
	}
	if request.Kind != "" && event.Kind != request.Kind {
		return false
	}
	if request.TraceID != "" && event.TraceID != request.TraceID {
		return false
	}
	if request.ConversationID != "" && event.ConversationID != request.ConversationID {
		return false
	}
	if request.Module != "" && event.Module != request.Module {
		return false
	}
	if request.Level != "" && event.Level != request.Level {
		return false
	}

	return true
}

func currentWindowStartID(events []panel.Event) int64 {
	if len(events) == 0 {
		return 0
	}
	return events[0].ID
}

func currentWindowEndID(events []panel.Event) int64 {
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].ID
}

func cloneEvent(event panel.Event) panel.Event {
	cloned := event
	if event.ParentEventID != nil {
		parentID := *event.ParentEventID
		cloned.ParentEventID = &parentID
	}
	cloned.ArtifactIDs = append([]string(nil), event.ArtifactIDs...)
	return cloned
}

func cloneArtifact(artifact panel.Artifact) panel.Artifact {
	return artifact
}

func cloneSnapshot(snapshot panel.Snapshot) panel.Snapshot {
	return snapshot
}
