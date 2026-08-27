package kernel

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func messagingKey(event otogi.EventID, module otogi.ModuleID, slot string) (otogi.IdempotencyKey, error) {
	h := sha256.New()
	var size [8]byte
	for _, part := range []string{event.String(), module.String(), "core.messaging", slot} {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(part))
	}
	key, err := otogi.NewIdempotencyKey(hex.EncodeToString(h.Sum(nil)))
	if err != nil {
		return otogi.IdempotencyKey{}, fmt.Errorf("messaging key: %w", err)
	}
	return key, nil
}
