package llmmemory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

type persistedState struct {
	Records []persistedRecord `json:"records"`
}

type persistedRecord struct {
	ID             string              `json:"id"`
	TenantID       string              `json:"tenant_id"`
	Platform       string              `json:"platform"`
	ConversationID string              `json:"conversation_id"`
	Content        string              `json:"content"`
	Category       string              `json:"category"`
	Kind           ai.LLMMemoryKind    `json:"kind,omitempty"`
	Profile        ai.LLMMemoryProfile `json:"profile,omitempty"`
	Embedding      []float32           `json:"embedding"`
	Metadata       map[string]string   `json:"metadata,omitempty"`
	Keywords       []string            `json:"keywords,omitempty"`
	Tags           []string            `json:"tags,omitempty"`
	Links          []ai.LLMMemoryLink  `json:"links,omitempty"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
}

// LoadFromFile restores store contents from one persisted JSON file.
func (s *Store) LoadFromFile(path string) error {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return fmt.Errorf("llmmemory load from file: empty path")
	}

	data, err := os.ReadFile(trimmedPath)
	if err != nil {
		return fmt.Errorf("llmmemory load from file %s: %w", trimmedPath, err)
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("llmmemory load from file %s: unmarshal: %w", trimmedPath, err)
	}

	records := make(map[string]*ai.LLMMemoryRecord, len(state.Records))
	scopes := make(map[ai.LLMMemoryScope][]string)
	for index, item := range state.Records {
		record, err := item.toRecord()
		if err != nil {
			return fmt.Errorf("llmmemory load from file %s records[%d]: %w", trimmedPath, index, err)
		}
		records[record.ID] = &record
		scopes[record.Scope] = append(scopes[record.Scope], record.ID)
	}

	for scope, ids := range scopes {
		sort.Slice(ids, func(i, j int) bool {
			left := records[ids[i]]
			right := records[ids[j]]
			if left == nil || right == nil {
				return ids[i] < ids[j]
			}
			if left.CreatedAt.Equal(right.CreatedAt) {
				return left.ID < right.ID
			}
			return left.CreatedAt.Before(right.CreatedAt)
		})
		if s.maxEntries > 0 && len(ids) > s.maxEntries {
			for _, id := range ids[:len(ids)-s.maxEntries] {
				delete(records, id)
			}
			ids = ids[len(ids)-s.maxEntries:]
		}
		scopes[scope] = ids
	}

	s.mu.Lock()
	s.records = records
	s.scopes = scopes
	s.mu.Unlock()

	return nil
}

// SaveToFile persists store contents to one JSON file using atomic replace.
func (s *Store) SaveToFile(path string) error {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return fmt.Errorf("llmmemory save to file: empty path")
	}

	state := s.snapshotState()
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("llmmemory save to file %s: marshal: %w", trimmedPath, err)
	}

	dir := filepath.Dir(trimmedPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("llmmemory save to file %s: mkdir %s: %w", trimmedPath, dir, err)
	}

	tempFile, err := os.CreateTemp(dir, filepath.Base(trimmedPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("llmmemory save to file %s: create temp file: %w", trimmedPath, err)
	}
	tempPath := tempFile.Name()
	if _, err := tempFile.Write(payload); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("llmmemory save to file %s: write temp file: %w", trimmedPath, err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("llmmemory save to file %s: sync temp file: %w", trimmedPath, err)
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("llmmemory save to file %s: close temp file: %w", trimmedPath, err)
	}
	if err := os.Rename(tempPath, trimmedPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("llmmemory save to file %s: rename temp file: %w", trimmedPath, err)
	}

	return nil
}

func (s *Store) snapshotState() persistedState {
	s.mu.RLock()
	records := make([]ai.LLMMemoryRecord, 0, len(s.records))
	for _, record := range s.records {
		if record == nil {
			continue
		}
		records = append(records, cloneRecord(*record))
	}
	s.mu.RUnlock()

	sort.Slice(records, func(i, j int) bool {
		left := records[i]
		right := records[j]
		switch {
		case left.Scope.TenantID != right.Scope.TenantID:
			return left.Scope.TenantID < right.Scope.TenantID
		case left.Scope.Platform != right.Scope.Platform:
			return left.Scope.Platform < right.Scope.Platform
		case left.Scope.ConversationID != right.Scope.ConversationID:
			return left.Scope.ConversationID < right.Scope.ConversationID
		case !left.CreatedAt.Equal(right.CreatedAt):
			return left.CreatedAt.Before(right.CreatedAt)
		default:
			return left.ID < right.ID
		}
	})

	state := persistedState{Records: make([]persistedRecord, 0, len(records))}
	for _, record := range records {
		state.Records = append(state.Records, persistedRecord{
			ID:             record.ID,
			TenantID:       record.Scope.TenantID,
			Platform:       record.Scope.Platform,
			ConversationID: record.Scope.ConversationID,
			Content:        record.Content,
			Category:       record.Category,
			Kind:           record.Profile.Kind,
			Profile:        cloneProfile(record.Profile),
			Embedding:      cloneEmbedding(record.Embedding),
			Metadata:       cloneMetadata(record.Metadata),
			Keywords:       cloneStrings(record.Keywords),
			Tags:           cloneStrings(record.Tags),
			Links:          cloneLinks(record.Links),
			CreatedAt:      record.CreatedAt.UTC().Format(time.RFC3339Nano),
			UpdatedAt:      record.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}

	return state
}

func (r persistedRecord) toRecord() (ai.LLMMemoryRecord, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(r.CreatedAt))
	if err != nil {
		return ai.LLMMemoryRecord{}, fmt.Errorf("parse created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(r.UpdatedAt))
	if err != nil {
		return ai.LLMMemoryRecord{}, fmt.Errorf("parse updated_at: %w", err)
	}

	record := ai.LLMMemoryRecord{
		ID: strings.TrimSpace(r.ID),
		Scope: ai.LLMMemoryScope{
			TenantID:       r.TenantID,
			Platform:       strings.TrimSpace(r.Platform),
			ConversationID: strings.TrimSpace(r.ConversationID),
		},
		Content:   strings.TrimSpace(r.Content),
		Category:  strings.TrimSpace(r.Category),
		Embedding: cloneEmbedding(r.Embedding),
		Profile:   cloneProfile(r.Profile),
		Metadata:  cloneMetadata(r.Metadata),
		Keywords:  cloneStrings(r.Keywords),
		Tags:      cloneStrings(r.Tags),
		Links:     cloneLinks(r.Links),
		CreatedAt: createdAt.UTC(),
		UpdatedAt: updatedAt.UTC(),
	}
	if record.Profile.Kind == "" && r.Kind != "" {
		record.Profile.Kind = r.Kind
	}
	record.Profile = normalizeProfile(record.Profile, record.Metadata, record.CreatedAt)
	if err := validatePersistedRecord(record); err != nil {
		return ai.LLMMemoryRecord{}, err
	}

	return record, nil
}

func validatePersistedRecord(record ai.LLMMemoryRecord) error {
	if strings.TrimSpace(record.ID) == "" {
		return fmt.Errorf("missing id")
	}
	if err := record.Scope.Validate(); err != nil {
		return fmt.Errorf("validate scope: %w", err)
	}
	if strings.TrimSpace(record.Content) == "" {
		return fmt.Errorf("missing content")
	}
	if strings.TrimSpace(record.Category) == "" {
		return fmt.Errorf("missing category")
	}
	if err := validateMemoryEmbedding(record.Embedding); err != nil {
		return fmt.Errorf("validate embedding: %w", err)
	}
	if err := record.Profile.Validate(); err != nil {
		return fmt.Errorf("validate profile: %w", err)
	}
	if record.CreatedAt.IsZero() {
		return fmt.Errorf("missing created_at")
	}
	if record.UpdatedAt.IsZero() {
		return fmt.Errorf("missing updated_at")
	}

	return nil
}
