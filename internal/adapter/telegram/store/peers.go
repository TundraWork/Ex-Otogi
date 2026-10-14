package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/gotd/td/telegram/peers"
)

// Key prefixes used by gotd's peers package, shared with the update manager's
// access hashers so both see the same hashes.
const (
	usersPrefix   = "users_"
	channelPrefix = "channel_"
)

var _ peers.Storage = (*Store)(nil)

// Save stores a peer access hash for gotd's peers.Manager.
func (s *Store) Save(ctx context.Context, key peers.Key, value peers.Value) error {
	return s.putPeer(ctx, key.Prefix, key.ID, value.AccessHash)
}

// Find returns a peer access hash for gotd's peers.Manager.
func (s *Store) Find(ctx context.Context, key peers.Key) (peers.Value, bool, error) {
	hash, found, err := s.getPeer(ctx, key.Prefix, key.ID)
	return peers.Value{AccessHash: hash}, found, err
}

// SavePhone maps a phone number to a peer.
func (s *Store) SavePhone(ctx context.Context, phone string, key peers.Key) error {
	return s.exec(ctx, "save peer phone", `INSERT INTO peer_phones (phone, prefix, id) VALUES (?, ?, ?)
		ON CONFLICT (phone) DO UPDATE SET prefix = excluded.prefix, id = excluded.id`, phone, key.Prefix, key.ID)
}

// FindPhone returns the peer mapped to a phone number.
func (s *Store) FindPhone(ctx context.Context, phone string) (peers.Key, peers.Value, bool, error) {
	var key peers.Key
	var hash int64
	err := s.db.QueryRowContext(ctx, `SELECT p.prefix, p.id, COALESCE(h.access_hash, 0) FROM peer_phones p
		LEFT JOIN peers h ON h.prefix = p.prefix AND h.id = p.id WHERE p.phone = ?`, phone).Scan(&key.Prefix, &key.ID, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return peers.Key{}, peers.Value{}, false, nil
	}
	if err != nil {
		return peers.Key{}, peers.Value{}, false, fmt.Errorf("find peer phone: %w", err)
	}
	return key, peers.Value{AccessHash: hash}, true, nil
}

// GetContactsHash returns the saved contacts hash, or zero.
func (s *Store) GetContactsHash(ctx context.Context) (int64, error) {
	var hash int64
	err := s.db.QueryRowContext(ctx, "SELECT contacts_hash FROM peer_meta").Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get contacts hash: %w", err)
	}
	return hash, nil
}

// SaveContactsHash saves the contacts hash.
func (s *Store) SaveContactsHash(ctx context.Context, hash int64) error {
	return s.exec(ctx, "save contacts hash", `INSERT INTO peer_meta (singleton, contacts_hash) VALUES (1, ?)
		ON CONFLICT (singleton) DO UPDATE SET contacts_hash = excluded.contacts_hash`, hash)
}

func (s *Store) putPeer(ctx context.Context, prefix string, id, hash int64) error {
	return s.exec(ctx, "save peer", `INSERT INTO peers (prefix, id, access_hash) VALUES (?, ?, ?)
		ON CONFLICT (prefix, id) DO UPDATE SET access_hash = excluded.access_hash`, prefix, id, hash)
}

func (s *Store) getPeer(ctx context.Context, prefix string, id int64) (int64, bool, error) {
	var hash int64
	err := s.db.QueryRowContext(ctx, "SELECT access_hash FROM peers WHERE prefix = ? AND id = ?", prefix, id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get peer: %w", err)
	}
	return hash, true, nil
}
