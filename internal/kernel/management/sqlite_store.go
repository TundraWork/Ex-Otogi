package management

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"time"

	"ex-otogi/internal/sqlc/management"
	panel "ex-otogi/pkg/otogi/management"

	// Register the pure-Go SQLite driver for database/sql.
	_ "modernc.org/sqlite"

	"github.com/pressly/goose/v3"
)

const (
	sqliteDefaultEventLimit = 100
	managementSchemaVersion = 1
)

// SQLiteStore persists management observability data in a SQLite database. It
// implements both panel.Recorder and panel.Query without retaining data
// in-process beyond what is needed for the current request.
type SQLiteStore struct {
	db      *sql.DB
	queries *managementsql.Queries
}

// NewSQLiteStore opens (or creates) the SQLite database at dbPath, runs pending
// goose migrations, enables WAL mode, and returns a ready-to-use store.
func NewSQLiteStore(ctx context.Context, dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", dbPath, err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", dbPath, err)
	}
	var hasEvents, hasSchemaMarker int
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'events')").Scan(&hasEvents); err != nil {
		db.Close()
		return nil, fmt.Errorf("inspect management schema %s: %w", dbPath, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'management_meta')").Scan(&hasSchemaMarker); err != nil {
		db.Close()
		return nil, fmt.Errorf("inspect management schema marker %s: %w", dbPath, err)
	}
	if hasEvents != 0 && hasSchemaMarker == 0 {
		db.Close()
		return nil, fmt.Errorf("validate management schema: incompatible telemetry database; stop the bot and reset %s", dbPath)
	}

	migrationsDir, err := fs.Sub(MigrationsFS, "migrations")
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("sub migrations fs: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrationsDir)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	var schemaVersion int
	if err := db.QueryRowContext(ctx, "SELECT schema_version FROM management_meta LIMIT 1").Scan(&schemaVersion); err != nil {
		db.Close()
		return nil, fmt.Errorf("validate management schema: incompatible telemetry database; stop the bot and reset %s: %w", dbPath, err)
	}
	if schemaVersion != managementSchemaVersion {
		db.Close()
		return nil, fmt.Errorf("validate management schema: version %d, want %d; stop the bot and reset %s", schemaVersion, managementSchemaVersion, dbPath)
	}

	return &SQLiteStore{
		db:      db,
		queries: managementsql.New(db),
	}, nil
}

// Close releases the database connection held by the store.
func (s *SQLiteStore) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close management sqlite store: %w", err)
	}
	return nil
}

// RecordEvent appends one event to the database.
func (s *SQLiteStore) RecordEvent(ctx context.Context, event panel.Event) (panel.Event, error) {
	if err := ctx.Err(); err != nil {
		return panel.Event{}, fmt.Errorf("record event: %w", err)
	}

	record := event
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

	payloadJSON, err := json.Marshal(record.Payload)
	if err != nil {
		return panel.Event{}, fmt.Errorf("record event: marshal payload: %w", err)
	}

	var parentEventID sql.NullInt64
	if record.ParentEventID != nil {
		parentEventID = sql.NullInt64{Int64: *record.ParentEventID, Valid: true}
	}

	inserted, err := s.queries.InsertEvent(ctx, managementsql.InsertEventParams{
		OccurredAt:     record.OccurredAt,
		TraceID:        record.TraceID,
		ParentEventID:  parentEventID,
		Category:       string(record.Category),
		Kind:           record.Kind,
		Level:          string(record.Level),
		Module:         record.Module,
		Component:      record.Component,
		TenantID:       record.TenantID,
		Platform:       record.Platform,
		ConversationID: record.ConversationID,
		ActorID:        record.ActorID,
		Subject:        record.Subject,
		Description:    record.Description,
		PayloadJson:    string(payloadJSON),
	})
	if err != nil {
		return panel.Event{}, fmt.Errorf("record event: insert: %w", err)
	}

	return s.scanEvent(ctx, inserted)
}

// RecordArtifact stores one artifact associated with a retained event.
func (s *SQLiteStore) RecordArtifact(ctx context.Context, artifact panel.Artifact) (panel.Artifact, error) {
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

	// Verify the referenced event exists.
	if _, err := s.queries.GetEvent(ctx, record.EventID); err != nil {
		return panel.Artifact{}, fmt.Errorf("record artifact %s: event %d: %w", record.ID, record.EventID, panel.ErrEventNotFound)
	}

	_, err := s.queries.InsertArtifact(ctx, managementsql.InsertArtifactParams{
		ID:        record.ID,
		EventID:   record.EventID,
		Kind:      string(record.Kind),
		CreatedAt: record.CreatedAt,
		Content:   record.Content,
	})
	if err != nil {
		return panel.Artifact{}, fmt.Errorf("record artifact %s: insert: %w", record.ID, err)
	}

	return record, nil
}

// UpsertSnapshot replaces one snapshot identified by namespace and key.
func (s *SQLiteStore) UpsertSnapshot(ctx context.Context, snapshot panel.Snapshot) (panel.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return panel.Snapshot{}, fmt.Errorf("upsert snapshot %s/%s: %w", snapshot.Namespace, snapshot.Key, err)
	}
	if snapshot.Namespace == "" {
		return panel.Snapshot{}, fmt.Errorf("upsert snapshot: empty namespace")
	}
	if snapshot.Key == "" {
		return panel.Snapshot{}, fmt.Errorf("upsert snapshot %s: empty key", snapshot.Namespace)
	}

	record := snapshot
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
	}

	payloadJSON, err := json.Marshal(record.Payload)
	if err != nil {
		return panel.Snapshot{}, fmt.Errorf("upsert snapshot %s/%s: marshal payload: %w", record.Namespace, record.Key, err)
	}

	_, err = s.queries.UpsertSnapshot(ctx, managementsql.UpsertSnapshotParams{
		Namespace:   record.Namespace,
		Key:         record.Key,
		Module:      record.Module,
		UpdatedAt:   record.UpdatedAt,
		Summary:     record.Summary,
		PayloadJson: string(payloadJSON),
	})
	if err != nil {
		return panel.Snapshot{}, fmt.Errorf("upsert snapshot %s/%s: %w", record.Namespace, record.Key, err)
	}

	return record, nil
}

// ListEvents returns one newest-first page. With no cursor it selects the
// latest events; AfterID polls newer events and BeforeID pages into history.
func (s *SQLiteStore) ListEvents(ctx context.Context, request panel.EventQuery) (panel.EventPage, error) {
	if err := ctx.Err(); err != nil {
		return panel.EventPage{}, fmt.Errorf("list events: %w", err)
	}
	if request.AfterID < 0 {
		return panel.EventPage{}, fmt.Errorf("list events: after_id %d: %w", request.AfterID, panel.ErrInvalidQuery)
	}
	if request.BeforeID < 0 {
		return panel.EventPage{}, fmt.Errorf("list events: before_id %d: %w", request.BeforeID, panel.ErrInvalidQuery)
	}
	if request.AfterID > 0 && request.BeforeID > 0 {
		return panel.EventPage{}, fmt.Errorf("list events: after_id and before_id are mutually exclusive: %w", panel.ErrInvalidQuery)
	}

	limit := int64(request.Limit)
	if limit <= 0 {
		limit = sqliteDefaultEventLimit
	}
	rows, hasOlder, hasNewer, err := s.listEventRows(ctx, request, limit)
	if err != nil {
		return panel.EventPage{}, fmt.Errorf("list events: %w", err)
	}

	items := make([]panel.Event, len(rows))
	for i, row := range rows {
		items[i], err = s.scanEvent(ctx, row)
		if err != nil {
			return panel.EventPage{}, fmt.Errorf("list events: scan row %d: %w", i, err)
		}
	}

	windowStartID, err := s.minEventID(ctx)
	if err != nil {
		return panel.EventPage{}, fmt.Errorf("list events: min id: %w", err)
	}
	windowEndID, err := s.maxEventID(ctx)
	if err != nil {
		return panel.EventPage{}, fmt.Errorf("list events: max id: %w", err)
	}

	firstID := int64(0)
	lastID := request.AfterID
	if len(items) > 0 {
		lastID = items[0].ID
		firstID = items[len(items)-1].ID
	}

	return panel.EventPage{
		Items:         items,
		OldestID:      firstID,
		NewestID:      lastID,
		WindowStartID: windowStartID,
		WindowEndID:   windowEndID,
		HasOlder:      hasOlder,
		HasNewer:      hasNewer,
	}, nil
}

func (s *SQLiteStore) listEventRows(
	ctx context.Context,
	request panel.EventQuery,
	limit int64,
) ([]managementsql.Event, bool, bool, error) {
	queryLimit := limit + 1
	switch {
	case request.AfterID > 0:
		rows, err := s.queries.ListNewerEvents(ctx, managementsql.ListNewerEventsParams{
			Category: string(request.Category), Kind: request.Kind, TraceID: request.TraceID,
			ConversationID: request.ConversationID, Module: request.Module, Level: string(request.Level),
			AfterID: request.AfterID, Limit: queryLimit,
		})
		if err != nil {
			return nil, false, false, fmt.Errorf("list newer events after %d: %w", request.AfterID, err)
		}
		hasNewer := len(rows) > int(limit)
		if hasNewer {
			rows = rows[:limit]
		}
		slices.Reverse(rows)
		return rows, false, hasNewer, nil
	case request.BeforeID > 0:
		rows, err := s.queries.ListOlderEvents(ctx, managementsql.ListOlderEventsParams{
			Category: string(request.Category), Kind: request.Kind, TraceID: request.TraceID,
			ConversationID: request.ConversationID, Module: request.Module, Level: string(request.Level),
			BeforeID: request.BeforeID, Limit: queryLimit,
		})
		if err != nil {
			return nil, false, false, fmt.Errorf("list older events before %d: %w", request.BeforeID, err)
		}
		hasOlder := len(rows) > int(limit)
		if hasOlder {
			rows = rows[:limit]
		}
		return rows, hasOlder, false, nil
	default:
		rows, err := s.queries.ListLatestEvents(ctx, managementsql.ListLatestEventsParams{
			Category: string(request.Category), Kind: request.Kind, TraceID: request.TraceID,
			ConversationID: request.ConversationID, Module: request.Module, Level: string(request.Level),
			Limit: queryLimit,
		})
		if err != nil {
			return nil, false, false, fmt.Errorf("list latest events: %w", err)
		}
		hasOlder := len(rows) > int(limit)
		if hasOlder {
			rows = rows[:limit]
		}
		return rows, hasOlder, false, nil
	}
}

// GetEvent returns one retained event by ID.
func (s *SQLiteStore) GetEvent(ctx context.Context, id int64) (panel.Event, error) {
	if err := ctx.Err(); err != nil {
		return panel.Event{}, fmt.Errorf("get event %d: %w", id, err)
	}

	row, err := s.queries.GetEvent(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return panel.Event{}, fmt.Errorf("get event %d: %w", id, panel.ErrEventNotFound)
		}
		return panel.Event{}, fmt.Errorf("get event %d: %w", id, err)
	}

	return s.scanEvent(ctx, row)
}

// GetTrace returns retained events for one trace ID.
func (s *SQLiteStore) GetTrace(ctx context.Context, request panel.TraceQuery) (panel.TraceView, error) {
	if err := ctx.Err(); err != nil {
		return panel.TraceView{}, fmt.Errorf("get trace %s: %w", request.TraceID, err)
	}
	if request.TraceID == "" {
		return panel.TraceView{}, fmt.Errorf("get trace: empty trace id: %w", panel.ErrInvalidQuery)
	}

	rows, err := s.queries.GetTraceEvents(ctx, request.TraceID)
	if err != nil {
		return panel.TraceView{}, fmt.Errorf("get trace %s: %w", request.TraceID, err)
	}

	items := make([]panel.Event, len(rows))
	for i, row := range rows {
		items[i], err = s.scanEvent(ctx, row)
		if err != nil {
			return panel.TraceView{}, fmt.Errorf("get trace %s: scan row %d: %w", request.TraceID, i, err)
		}
	}

	if request.Limit > 0 && len(items) > request.Limit {
		items = items[len(items)-request.Limit:]
	}

	return panel.TraceView{
		TraceID: request.TraceID,
		Items:   items,
	}, nil
}

// GetArtifact returns one retained artifact by ID.
func (s *SQLiteStore) GetArtifact(ctx context.Context, id string) (panel.Artifact, error) {
	if err := ctx.Err(); err != nil {
		return panel.Artifact{}, fmt.Errorf("get artifact %s: %w", id, err)
	}

	row, err := s.queries.GetArtifact(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return panel.Artifact{}, fmt.Errorf("get artifact %s: %w", id, panel.ErrArtifactNotFound)
		}
		return panel.Artifact{}, fmt.Errorf("get artifact %s: %w", id, err)
	}

	return panel.Artifact{
		ID:        row.ID,
		EventID:   row.EventID,
		Kind:      panel.ArtifactKind(row.Kind),
		CreatedAt: row.CreatedAt,
		Content:   row.Content,
	}, nil
}

// ListSnapshots returns snapshots matching the optional filters.
func (s *SQLiteStore) ListSnapshots(ctx context.Context, request panel.SnapshotQuery) (panel.SnapshotList, error) {
	if err := ctx.Err(); err != nil {
		return panel.SnapshotList{}, fmt.Errorf("list snapshots: %w", err)
	}

	rows, err := s.queries.ListSnapshots(ctx, managementsql.ListSnapshotsParams{
		Namespace: request.Namespace,
		Key:       request.Key,
		Module:    request.Module,
	})
	if err != nil {
		return panel.SnapshotList{}, fmt.Errorf("list snapshots: %w", err)
	}

	items := make([]panel.Snapshot, len(rows))
	for i, row := range rows {
		items[i], err = scanSnapshot(row)
		if err != nil {
			return panel.SnapshotList{}, fmt.Errorf("list snapshots: scan row %d: %w", i, err)
		}
	}

	return panel.SnapshotList{Items: items}, nil
}

// GetOverview returns a lightweight summary of the retained management data.
func (s *SQLiteStore) GetOverview(ctx context.Context) (panel.Overview, error) {
	if err := ctx.Err(); err != nil {
		return panel.Overview{}, fmt.Errorf("get overview: %w", err)
	}

	windowStartID, err := s.minEventID(ctx)
	if err != nil {
		return panel.Overview{}, fmt.Errorf("get overview: min id: %w", err)
	}
	windowEndID, err := s.maxEventID(ctx)
	if err != nil {
		return panel.Overview{}, fmt.Errorf("get overview: max id: %w", err)
	}

	totalEvents, err := s.queries.CountEvents(ctx)
	if err != nil {
		return panel.Overview{}, fmt.Errorf("get overview: count events: %w", err)
	}

	categoryRows, err := s.queries.CountEventsByCategory(ctx)
	if err != nil {
		return panel.Overview{}, fmt.Errorf("get overview: count by category: %w", err)
	}
	eventCountsByCategory := make(map[panel.EventCategory]int, len(categoryRows))
	for _, row := range categoryRows {
		eventCountsByCategory[panel.EventCategory(row.Category)] = int(row.Count)
	}

	recentErrorCount, err := s.queries.CountRecentErrors(ctx)
	if err != nil {
		return panel.Overview{}, fmt.Errorf("get overview: count errors: %w", err)
	}

	recentTraceCount, err := s.queries.CountDistinctTraceIDs(ctx)
	if err != nil {
		return panel.Overview{}, fmt.Errorf("get overview: count traces: %w", err)
	}

	return panel.Overview{
		WindowStartID:         windowStartID,
		WindowEndID:           windowEndID,
		TotalEvents:           int(totalEvents),
		EventCountsByCategory: eventCountsByCategory,
		RecentErrorCount:      int(recentErrorCount),
		RecentTraceCount:      int(recentTraceCount),
	}, nil
}

// scanEvent converts one sqlc Event row into a panel.Event, including its raw
// JSON payload and artifact ID reconstruction.
func (s *SQLiteStore) scanEvent(ctx context.Context, row managementsql.Event) (panel.Event, error) {
	var parentEventID *int64
	if row.ParentEventID.Valid {
		parentEventID = &row.ParentEventID.Int64
	}

	// Reconstruct ArtifactIDs from the artifacts table.
	artifactIDs, err := s.queries.ListArtifactIDsByEventID(ctx, row.ID)
	if err != nil {
		artifactIDs = nil
	}

	return panel.Event{
		ID:             row.ID,
		OccurredAt:     row.OccurredAt,
		TraceID:        row.TraceID,
		ParentEventID:  parentEventID,
		Category:       panel.EventCategory(row.Category),
		Kind:           row.Kind,
		Level:          panel.EventLevel(row.Level),
		Module:         row.Module,
		Component:      row.Component,
		TenantID:       row.TenantID,
		Platform:       row.Platform,
		ConversationID: row.ConversationID,
		ActorID:        row.ActorID,
		Subject:        row.Subject,
		Description:    row.Description,
		Payload:        decodeJSONPayload(row.PayloadJson),
		ArtifactIDs:    artifactIDs,
	}, nil
}

// scanSnapshot converts one sqlc Snapshot row into a panel.Snapshot.
func scanSnapshot(row managementsql.Snapshot) (panel.Snapshot, error) {
	return panel.Snapshot{
		Namespace: row.Namespace,
		Key:       row.Key,
		Module:    row.Module,
		UpdatedAt: row.UpdatedAt,
		Summary:   row.Summary,
		Payload:   decodeJSONPayload(row.PayloadJson),
	}, nil
}

// minEventID returns the minimum event ID in the database, or 0 if empty.
func (s *SQLiteStore) minEventID(ctx context.Context) (int64, error) {
	val, err := s.queries.MinEventID(ctx)
	if err != nil {
		return 0, fmt.Errorf("query min event id: %w", err)
	}
	switch v := val.(type) {
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	default:
		return 0, nil
	}
}

// maxEventID returns the maximum event ID in the database, or 0 if empty.
func (s *SQLiteStore) maxEventID(ctx context.Context) (int64, error) {
	val, err := s.queries.MaxEventID(ctx)
	if err != nil {
		return 0, fmt.Errorf("query max event id: %w", err)
	}
	switch v := val.(type) {
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	default:
		return 0, nil
	}
}

func decodeJSONPayload(payloadJSON string) any {
	if payloadJSON == "" || payloadJSON == "{}" {
		return nil
	}
	var payload any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return json.RawMessage(payloadJSON)
	}
	return payload
}
