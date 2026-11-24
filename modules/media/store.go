package media

import (
	"context"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"

	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

//go:embed migrations/*.sql
var migrations embed.FS

// key names a piece of media by a digest of its content.
type key struct {
	Algorithm string
	Digest    []byte
}

func (k key) String() string { return k.Algorithm + ":" + hex.EncodeToString(k.Digest) }

// store is the module's private database: descriptions, sources, recent
// messages' media, and stickers.
type store struct{ db *sql.DB }

func openStore(ctx context.Context, db *sql.DB) (*store, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("media store: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return nil, fmt.Errorf("media store: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return nil, fmt.Errorf("media store: migrate: %w", err)
	}
	return &store{db: db}, nil
}

// get returns the description kept under k.
func (s *store) get(ctx context.Context, k key) (contract.Description, bool, error) {
	var d contract.Description
	var kind string
	err := s.db.QueryRowContext(ctx, `SELECT kind, summary, text FROM descriptions WHERE algorithm = ? AND digest = ?`, k.Algorithm, k.Digest).
		Scan(&kind, &d.Summary, &d.Text)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.Description{}, false, nil
	}
	if err != nil {
		return contract.Description{}, false, fmt.Errorf("media store: read description: %w", err)
	}
	d.Kind = contract.Kind(kind)
	return d, true, nil
}

// put keeps d under k, described by model at now.
func (s *store) put(ctx context.Context, k key, d contract.Description, model string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO descriptions (algorithm, digest, kind, summary, text, model, described_at, seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (algorithm, digest) DO UPDATE SET kind = excluded.kind, summary = excluded.summary, text = excluded.text,
			model = excluded.model, described_at = excluded.described_at, seen_at = excluded.seen_at`,
		k.Algorithm, k.Digest, string(d.Kind), d.Summary, d.Text, model, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return fmt.Errorf("media store: keep description: %w", err)
	}
	return nil
}

// seen marks the description under k as seen at now, so it outlives the
// pruning while the media keeps coming back.
func (s *store) seen(ctx context.Context, k key, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE descriptions SET seen_at = ? WHERE algorithm = ? AND digest = ?`, now.UnixMilli(), k.Algorithm, k.Digest); err != nil {
		return fmt.Errorf("media store: mark seen: %w", err)
	}
	return nil
}

// source returns the key of the media that came from src.
func (s *store) source(ctx context.Context, src string) (key, bool, error) {
	var k key
	err := s.db.QueryRowContext(ctx, `SELECT algorithm, digest FROM sources WHERE source = ?`, src).Scan(&k.Algorithm, &k.Digest)
	if errors.Is(err, sql.ErrNoRows) {
		return key{}, false, nil
	}
	if err != nil {
		return key{}, false, fmt.Errorf("media store: read source: %w", err)
	}
	return k, true, nil
}

// putSource keeps that src is the media under k.
func (s *store) putSource(ctx context.Context, src string, k key, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sources (source, algorithm, digest, seen_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (source) DO UPDATE SET algorithm = excluded.algorithm, digest = excluded.digest, seen_at = excluded.seen_at`,
		src, k.Algorithm, k.Digest, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("media store: keep source: %w", err)
	}
	return nil
}

// prune deletes descriptions and sources last seen before before, and
// recent media from before recentBefore.
func (s *store) prune(ctx context.Context, before, recentBefore time.Time) (int64, error) {
	var n int64
	for _, q := range []struct {
		sql string
		at  time.Time
	}{
		{`DELETE FROM descriptions WHERE seen_at < ?`, before},
		{`DELETE FROM sources WHERE seen_at < ?`, before},
		{`DELETE FROM recent_media WHERE at < ?`, recentBefore},
	} {
		res, err := s.db.ExecContext(ctx, q.sql, q.at.UnixMilli())
		if err != nil {
			return n, fmt.Errorf("media store: prune: %w", err)
		}
		rows, _ := res.RowsAffected()
		n += rows
	}
	return n, nil
}

// putRecent keeps the media of message source in d, seen at now.
func (s *store) putRecent(ctx context.Context, d otogi.Destination, source string, media []otogi.Media, now time.Time) error {
	encoded, err := json.Marshal(media)
	if err != nil {
		return fmt.Errorf("media store: keep recent media: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO recent_media (platform, account, conversation, source_id, media, at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (platform, account, conversation, source_id) DO UPDATE SET media = excluded.media, at = excluded.at`,
		d.Platform, d.Account, d.Conversation, source, string(encoded), now.UnixMilli())
	if err != nil {
		return fmt.Errorf("media store: keep recent media: %w", err)
	}
	return nil
}

// recent returns the media of message source in d, kept since since.
func (s *store) recent(ctx context.Context, d otogi.Destination, source string, since time.Time) ([]otogi.Media, bool, error) {
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT media FROM recent_media WHERE platform = ? AND account = ? AND conversation = ? AND source_id = ? AND at >= ?`,
		d.Platform, d.Account, d.Conversation, source, since.UnixMilli()).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("media store: read recent media: %w", err)
	}
	var media []otogi.Media
	if err := json.Unmarshal([]byte(encoded), &media); err != nil {
		return nil, false, fmt.Errorf("media store: read recent media: %w", err)
	}
	return media, true, nil
}

// sticker is a kept sticker. Bytes is only read for its picture.
type sticker struct {
	ID        int
	SHA256    []byte
	MediaType string
	Bytes     []byte
	Size      int64
	Kind      contract.Kind
	Summary   string
	Note      string
}

// text is what the sticker shows in a list: its note, else its summary.
func (st sticker) text() string {
	if st.Note != "" {
		return st.Note
	}
	return st.Summary
}

var (
	// errStickersFull reports that as many stickers as allowed are kept.
	errStickersFull = errors.New("media: too many stickers")
	// errStickersTooLarge reports that keeping one more would pass the
	// limit on their bytes in all.
	errStickersTooLarge = errors.New("media: stickers too large in all")
)

// addSticker keeps st, added by by at now, and returns its number. When a
// sticker with st's digest is kept already, it returns that one's number
// and existed. It fails with errStickersFull or errStickersTooLarge when
// keeping st would pass one of lim. One statement checks and inserts, so
// two operators adding at once cannot pass the limits together.
func (s *store) addSticker(ctx context.Context, st sticker, by string, now time.Time, lim stickerLimits) (id int, existed bool, err error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO stickers (sha256, media_type, bytes, kind, summary, note, added_by, added_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?
		WHERE (SELECT count(*) FROM stickers) < ? AND (SELECT coalesce(sum(length(bytes)), 0) FROM stickers) + ? <= ?
		ON CONFLICT (sha256) DO NOTHING`,
		st.SHA256, st.MediaType, st.Bytes, string(st.Kind), st.Summary, st.Note, by, now.UnixMilli(),
		lim.count, len(st.Bytes), lim.total)
	if err != nil {
		return 0, false, fmt.Errorf("media store: keep sticker: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		last, err := res.LastInsertId()
		if err != nil {
			return 0, false, fmt.Errorf("media store: keep sticker: %w", err)
		}
		return int(last), false, nil
	}
	if id, ok, err := s.stickerByDigest(ctx, st.SHA256); err != nil || ok {
		return id, ok, err
	}
	count, total, err := s.stickerUsage(ctx)
	if err != nil {
		return 0, false, err
	}
	if count >= lim.count {
		return 0, false, errStickersFull
	}
	if total+int64(len(st.Bytes)) > lim.total {
		return 0, false, errStickersTooLarge
	}
	// A sticker kept or removed in between; the caller can try again.
	return 0, false, fmt.Errorf("media store: keep sticker: the stickers changed meanwhile: %w", otogi.ErrUnavailable)
}

// stickerByDigest returns the number of the sticker with digest sum.
func (s *store) stickerByDigest(ctx context.Context, sum []byte) (int, bool, error) {
	var id int
	err := s.db.QueryRowContext(ctx, `SELECT id FROM stickers WHERE sha256 = ?`, sum).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("media store: read sticker: %w", err)
	}
	return id, true, nil
}

// stickerUsage returns how many stickers are kept and their bytes in all.
func (s *store) stickerUsage(ctx context.Context) (count int, total int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(length(bytes)), 0) FROM stickers`).Scan(&count, &total)
	if err != nil {
		return 0, 0, fmt.Errorf("media store: count stickers: %w", err)
	}
	return count, total, nil
}

// stickers returns the kept stickers by number, without their bytes.
func (s *store) stickers(ctx context.Context) ([]sticker, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, sha256, media_type, length(bytes), kind, summary, note FROM stickers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("media store: list stickers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []sticker
	for rows.Next() {
		var st sticker
		var kind string
		if err := rows.Scan(&st.ID, &st.SHA256, &st.MediaType, &st.Size, &kind, &st.Summary, &st.Note); err != nil {
			return nil, fmt.Errorf("media store: list stickers: %w", err)
		}
		st.Kind = contract.Kind(kind)
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media store: list stickers: %w", err)
	}
	return out, nil
}

// stickerPicture returns sticker id with its bytes.
func (s *store) stickerPicture(ctx context.Context, id int) (sticker, bool, error) {
	st := sticker{ID: id}
	var kind string
	err := s.db.QueryRowContext(ctx, `SELECT sha256, media_type, bytes, kind, summary, note FROM stickers WHERE id = ?`, id).
		Scan(&st.SHA256, &st.MediaType, &st.Bytes, &kind, &st.Summary, &st.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return sticker{}, false, nil
	}
	if err != nil {
		return sticker{}, false, fmt.Errorf("media store: read sticker: %w", err)
	}
	st.Kind, st.Size = contract.Kind(kind), int64(len(st.Bytes))
	return st, true, nil
}

// setStickerNote sets sticker id's note and returns the sticker.
func (s *store) setStickerNote(ctx context.Context, id int, note string) (sticker, bool, error) {
	st := sticker{ID: id}
	var kind string
	err := s.db.QueryRowContext(ctx, `UPDATE stickers SET note = ? WHERE id = ? RETURNING sha256, media_type, length(bytes), kind, summary, note`, note, id).
		Scan(&st.SHA256, &st.MediaType, &st.Size, &kind, &st.Summary, &st.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return sticker{}, false, nil
	}
	if err != nil {
		return sticker{}, false, fmt.Errorf("media store: note sticker: %w", err)
	}
	st.Kind = contract.Kind(kind)
	return st, true, nil
}

// removeSticker deletes sticker id and returns what it was.
func (s *store) removeSticker(ctx context.Context, id int) (sticker, bool, error) {
	st := sticker{ID: id}
	var kind string
	err := s.db.QueryRowContext(ctx, `DELETE FROM stickers WHERE id = ? RETURNING sha256, media_type, length(bytes), kind, summary, note`, id).
		Scan(&st.SHA256, &st.MediaType, &st.Size, &kind, &st.Summary, &st.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return sticker{}, false, nil
	}
	if err != nil {
		return sticker{}, false, fmt.Errorf("media store: remove sticker: %w", err)
	}
	st.Kind = contract.Kind(kind)
	return st, true, nil
}
