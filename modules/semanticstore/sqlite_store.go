package semanticstore

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"

	"github.com/google/uuid"
	_ "modernc.org/sqlite" // Register the pure-Go SQLite database/sql driver.
)

const semanticStoreSchemaVersion = 1

// EmbeddingFingerprint identifies one compatible vector space.
type EmbeddingFingerprint struct {
	Provider   string
	Model      string
	Dimensions int
}

// Validate checks that the vector-space identity is explicit.
func (f EmbeddingFingerprint) Validate() error {
	if strings.TrimSpace(f.Provider) == "" || strings.TrimSpace(f.Model) == "" || f.Dimensions <= 0 {
		return fmt.Errorf("validate embedding fingerprint: provider, model, and positive dimensions are required")
	}
	return nil
}

func (f EmbeddingFingerprint) canonical() string {
	return fmt.Sprintf("%s/%s/%d", strings.TrimSpace(f.Provider), strings.TrimSpace(f.Model), f.Dimensions)
}

// SQLiteStore is the transactional durable implementation of SemanticStore.
type SQLiteStore struct {
	db          *sql.DB
	fingerprint EmbeddingFingerprint
	clock       func() time.Time
	newID       func() string
}

// OpenSQLiteStore opens a current-schema store and verifies its vector space.
func OpenSQLiteStore(ctx context.Context, path string, fingerprint EmbeddingFingerprint, clock func() time.Time, newID func() string) (*SQLiteStore, error) {
	if err := fingerprint.Validate(); err != nil {
		return nil, err
	}
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "." || cleanPath == "" {
		return nil, fmt.Errorf("open semantic store: database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o755); err != nil {
		return nil, fmt.Errorf("open semantic store create directory: %w", err)
	}
	db, err := sql.Open("sqlite", cleanPath)
	if err != nil {
		return nil, fmt.Errorf("open semantic store database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &SQLiteStore{db: db, fingerprint: fingerprint, clock: clock, newID: newID}
	if store.clock == nil {
		store.clock = time.Now
	}
	if store.newID == nil {
		store.newID = uuid.NewString
	}
	if err := store.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) initialize(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS memory_store_meta (singleton INTEGER PRIMARY KEY CHECK (singleton = 1), schema_version INTEGER NOT NULL, embedding_fingerprint TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS memory_records (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, platform TEXT NOT NULL, conversation_id TEXT NOT NULL, content TEXT NOT NULL, category TEXT NOT NULL, embedding BLOB NOT NULL, profile_json BLOB NOT NULL, keywords_json BLOB NOT NULL, tags_json BLOB NOT NULL, links_json BLOB NOT NULL, created_at_ns INTEGER NOT NULL, updated_at_ns INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS memory_records_scope_created ON memory_records (tenant_id, platform, conversation_id, created_at_ns DESC, id DESC)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize semantic store: %w", err)
		}
	}
	var version int
	var fingerprint string
	err := s.db.QueryRowContext(ctx, `SELECT schema_version, embedding_fingerprint FROM memory_store_meta WHERE singleton = 1`).Scan(&version, &fingerprint)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = s.db.ExecContext(ctx, `INSERT INTO memory_store_meta (singleton, schema_version, embedding_fingerprint) VALUES (1, ?, ?)`, semanticStoreSchemaVersion, s.fingerprint.canonical())
		if err != nil {
			return fmt.Errorf("initialize semantic store metadata: %w", err)
		}
	case err != nil:
		return fmt.Errorf("read semantic store metadata: %w", err)
	case version != semanticStoreSchemaVersion:
		return fmt.Errorf("semantic store schema version %d is incompatible with required version %d", version, semanticStoreSchemaVersion)
	case fingerprint != s.fingerprint.canonical():
		return fmt.Errorf("semantic store embedding fingerprint mismatch: database uses %q, configuration requires %q", fingerprint, s.fingerprint.canonical())
	}
	return nil
}

// Close releases the database.
func (s *SQLiteStore) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("close semantic store: %w", err)
	}
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close semantic store: %w", err)
	}
	return nil
}

// Store commits one record.
func (s *SQLiteStore) Store(ctx context.Context, entry ai.SemanticEntry) (ai.SemanticRecord, error) {
	if err := entry.Validate(); err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore store: %w", err)
	}
	if err := s.validateEmbedding(entry.Embedding); err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore store: %w", err)
	}
	now := s.clock().UTC()
	record := ai.SemanticRecord{ID: s.newID(), Scope: entry.Scope, Content: strings.TrimSpace(entry.Content), Category: strings.TrimSpace(entry.Category), Embedding: cloneEmbedding(entry.Embedding), Profile: cloneProfile(entry.Profile), Keywords: cloneStrings(entry.Keywords), Tags: cloneStrings(entry.Tags), Links: cloneLinks(entry.Links), CreatedAt: now, UpdatedAt: now}
	if err := s.insert(ctx, record); err != nil {
		return ai.SemanticRecord{}, err
	}
	return record, nil
}

func (s *SQLiteStore) insert(ctx context.Context, record ai.SemanticRecord) error {
	profile, keywords, tags, links, err := encodeRecordJSON(record)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO memory_records (id, tenant_id, platform, conversation_id, content, category, embedding, profile_json, keywords_json, tags_json, links_json, created_at_ns, updated_at_ns) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, record.ID, record.Scope.TenantID, record.Scope.Platform, record.Scope.ConversationID, record.Content, record.Category, encodeVector(record.Embedding), profile, keywords, tags, links, record.CreatedAt.UnixNano(), record.UpdatedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("semanticstore insert record: %w", err)
	}
	return nil
}

// Update commits one complete mutable replacement.
func (s *SQLiteStore) Update(ctx context.Context, update ai.SemanticUpdate) (ai.SemanticRecord, error) {
	if err := update.Validate(); err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore update: %w", err)
	}
	if err := s.validateEmbedding(update.Embedding); err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore update: %w", err)
	}
	record, err := s.recordByID(ctx, strings.TrimSpace(update.ID))
	if err != nil {
		return ai.SemanticRecord{}, err
	}
	record.Content, record.Category, record.Embedding, record.Profile = strings.TrimSpace(update.Content), strings.TrimSpace(update.Category), cloneEmbedding(update.Embedding), cloneProfile(update.Profile)
	record.Keywords, record.Tags, record.Links, record.UpdatedAt = cloneStrings(update.Keywords), cloneStrings(update.Tags), cloneLinks(update.Links), s.clock().UTC()
	profile, keywords, tags, links, err := encodeRecordJSON(record)
	if err != nil {
		return ai.SemanticRecord{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE memory_records SET content = ?, category = ?, embedding = ?, profile_json = ?, keywords_json = ?, tags_json = ?, links_json = ?, updated_at_ns = ? WHERE id = ?`, record.Content, record.Category, encodeVector(record.Embedding), profile, keywords, tags, links, record.UpdatedAt.UnixNano(), record.ID)
	if err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore update record: %w", err)
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore update record %s: not found", record.ID)
	}
	return record, nil
}

// Delete commits one deletion.
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return fmt.Errorf("semanticstore delete: missing id")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM memory_records WHERE id = ?`, trimmed)
	if err != nil {
		return fmt.Errorf("semanticstore delete: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("semanticstore delete rows affected: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("semanticstore delete: record %s not found", trimmed)
	}
	return nil
}

// ListByScope returns newest records in one conversation namespace.
func (s *SQLiteStore) ListByScope(ctx context.Context, scope ai.SemanticScope, limit int) ([]ai.SemanticRecord, error) {
	if err := scope.Validate(); err != nil {
		return nil, fmt.Errorf("semanticstore list by scope: %w", err)
	}
	if limit < 0 {
		return nil, fmt.Errorf("semanticstore list by scope: limit must be >= 0")
	}
	query := `SELECT id, tenant_id, platform, conversation_id, content, category, embedding, profile_json, keywords_json, tags_json, links_json, created_at_ns, updated_at_ns FROM memory_records WHERE tenant_id = ? AND platform = ? AND conversation_id = ? ORDER BY created_at_ns DESC, id DESC`
	args := []any{scope.TenantID, scope.Platform, scope.ConversationID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("semanticstore list by scope: %w", err)
	}
	defer rows.Close()
	var records []ai.SemanticRecord
	for rows.Next() {
		record, scanErr := scanRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("semanticstore list rows: %w", err)
	}
	return records, nil
}

// Search scores same-scope records within the bound embedding space.
func (s *SQLiteStore) Search(ctx context.Context, query ai.SemanticQuery) ([]ai.SemanticMatch, error) {
	if err := query.Validate(); err != nil {
		return nil, fmt.Errorf("semanticstore search: %w", err)
	}
	if err := s.validateEmbedding(query.Embedding); err != nil {
		return nil, fmt.Errorf("semanticstore search: %w", err)
	}
	records, err := s.ListByScope(ctx, query.Scope, 0)
	if err != nil {
		return nil, err
	}
	limit, threshold := query.Limit, query.MinSimilarity
	if limit == 0 {
		limit = defaultSearchLimit
	}
	if threshold == 0 {
		threshold = defaultMinSimilarity
	}
	now := s.clock().UTC()
	var matches []ai.SemanticMatch
	for _, record := range records {
		if record.Profile.ValidUntil != nil && record.Profile.ValidUntil.Before(now) {
			continue
		}
		similarity := dotProduct(query.Embedding, record.Embedding)
		if similarity >= threshold {
			matches = append(matches, ai.SemanticMatch{Record: record, Similarity: similarity})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Similarity == matches[j].Similarity {
			return matches[i].Record.CreatedAt.After(matches[j].Record.CreatedAt)
		}
		return matches[i].Similarity > matches[j].Similarity
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func (s *SQLiteStore) recordByID(ctx context.Context, id string) (ai.SemanticRecord, error) {
	record, err := scanRecord(s.db.QueryRowContext(ctx, `SELECT id, tenant_id, platform, conversation_id, content, category, embedding, profile_json, keywords_json, tags_json, links_json, created_at_ns, updated_at_ns FROM memory_records WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore update: record %s not found", id)
	}
	if err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore read record %s: %w", id, err)
	}
	return record, nil
}

type rowScanner interface{ Scan(...any) error }

func scanRecord(row rowScanner) (ai.SemanticRecord, error) {
	var record ai.SemanticRecord
	var embedding, profile, keywords, tags, links []byte
	var createdNS, updatedNS int64
	if err := row.Scan(&record.ID, &record.Scope.TenantID, &record.Scope.Platform, &record.Scope.ConversationID, &record.Content, &record.Category, &embedding, &profile, &keywords, &tags, &links, &createdNS, &updatedNS); err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("scan semantic record: %w", err)
	}
	record.Embedding = decodeVector(embedding)
	if err := json.Unmarshal(profile, &record.Profile); err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("decode semantic profile: %w", err)
	}
	if err := json.Unmarshal(keywords, &record.Keywords); err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("decode semantic keywords: %w", err)
	}
	if err := json.Unmarshal(tags, &record.Tags); err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("decode semantic tags: %w", err)
	}
	if err := json.Unmarshal(links, &record.Links); err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("decode semantic links: %w", err)
	}
	record.CreatedAt, record.UpdatedAt = time.Unix(0, createdNS).UTC(), time.Unix(0, updatedNS).UTC()
	return record, nil
}

func encodeRecordJSON(record ai.SemanticRecord) ([]byte, []byte, []byte, []byte, error) {
	profile, err := json.Marshal(record.Profile)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("encode semantic profile: %w", err)
	}
	keywords, err := json.Marshal(record.Keywords)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("encode semantic keywords: %w", err)
	}
	tags, err := json.Marshal(record.Tags)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("encode semantic tags: %w", err)
	}
	links, err := json.Marshal(record.Links)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("encode semantic links: %w", err)
	}
	return profile, keywords, tags, links, nil
}

func (s *SQLiteStore) validateEmbedding(vector []float32) error {
	if len(vector) != s.fingerprint.Dimensions {
		return fmt.Errorf("embedding dimensions %d do not match store dimensions %d", len(vector), s.fingerprint.Dimensions)
	}
	return nil
}

func encodeVector(vector []float32) []byte {
	encoded := make([]byte, len(vector)*4)
	for i, value := range vector {
		binary.LittleEndian.PutUint32(encoded[i*4:], math.Float32bits(value))
	}
	return encoded
}

func decodeVector(encoded []byte) []float32 {
	vector := make([]float32, len(encoded)/4)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(encoded[i*4:]))
	}
	return vector
}

var _ ai.SemanticStore = (*SQLiteStore)(nil)
