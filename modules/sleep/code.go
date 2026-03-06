package sleep

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"ex-otogi/pkg/otogi"
)

const (
	wakeCodeDomain         = "otogi.sleep.wake.v1"
	wakeCodeVersion   byte = 1
	wakeCodeTagSize        = 10
	wakeCodeSize           = 1 + 4 + wakeCodeTagSize
	minSigningKeySize      = 32
	maxWakeCodeMinute      = uint64(1<<32 - 1)
)

// codeScope binds a wake code to one user and one inbound event context.
type codeScope struct {
	UserID           string
	ConversationID   string
	ConversationType otogi.ConversationType
	SourcePlatform   otogi.Platform
	SourceID         string
}

// codeManager handles generation and validation of stateless wake codes.
//
// Wake codes are valid only for the same user, conversation, and sink context
// where the code was originally sent.
type codeManager struct {
	key []byte
}

// newCodeManager creates a code manager with a configured signing key.
func newCodeManager(key []byte) (*codeManager, error) {
	if len(key) < minSigningKeySize {
		return nil, fmt.Errorf(
			"sleep code manager: signing key must be at least %d bytes",
			minSigningKeySize,
		)
	}

	return &codeManager{key: append([]byte(nil), key...)}, nil
}

// Generate produces a compact wake code bound to one scope until untilDate.
func (cm *codeManager) Generate(scope codeScope, untilDate time.Time) (string, error) {
	expiryMinute, err := ceilUnixMinute(untilDate)
	if err != nil {
		return "", err
	}

	payload := make([]byte, wakeCodeSize)
	payload[0] = wakeCodeVersion
	binary.BigEndian.PutUint32(payload[1:5], expiryMinute)
	mac := cm.computeMAC(scope, wakeCodeVersion, expiryMinute)
	copy(payload[5:], mac[:wakeCodeTagSize])

	return base64.RawURLEncoding.EncodeToString(payload), nil
}

// Validate checks a wake code against the current scope and time.
func (cm *codeManager) Validate(code string, scope codeScope, now time.Time) error {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(code))
	if err != nil {
		return fmt.Errorf("decode wake code: %w", err)
	}
	if len(decoded) != wakeCodeSize {
		return fmt.Errorf("malformed wake code")
	}
	if decoded[0] != wakeCodeVersion {
		return fmt.Errorf("malformed wake code")
	}

	expiryMinute := binary.BigEndian.Uint32(decoded[1:5])
	currentMinute, err := currentUnixMinute(now)
	if err != nil {
		return fmt.Errorf("derive current minute: %w", err)
	}
	if currentMinute > expiryMinute {
		return fmt.Errorf("expired or invalid wake code")
	}

	expectedMAC := cm.computeMAC(scope, decoded[0], expiryMinute)
	if !hmac.Equal(decoded[5:], expectedMAC[:wakeCodeTagSize]) {
		return fmt.Errorf("expired or invalid wake code")
	}

	return nil
}

func (cm *codeManager) computeMAC(scope codeScope, version byte, expiryMinute uint32) []byte {
	payload := make([]byte, 0, len(wakeCodeDomain)+1+4+5*8)
	payload = append(payload, wakeCodeDomain...)
	payload = append(payload, version)
	payload = binary.BigEndian.AppendUint32(payload, expiryMinute)
	payload = appendLengthPrefixed(payload, scope.UserID)
	payload = appendLengthPrefixed(payload, scope.ConversationID)
	payload = appendLengthPrefixed(payload, string(scope.ConversationType))
	payload = appendLengthPrefixed(payload, string(scope.SourcePlatform))
	payload = appendLengthPrefixed(payload, scope.SourceID)

	h := hmac.New(sha256.New, cm.key)
	_, _ = h.Write(payload)

	return h.Sum(nil)
}

func appendLengthPrefixed(payload []byte, value string) []byte {
	payload = binary.BigEndian.AppendUint64(payload, uint64(len(value)))
	return append(payload, value...)
}

func ceilUnixMinute(t time.Time) (uint32, error) {
	t = t.UTC()
	minute := t.Unix() / 60
	if t.Truncate(time.Minute) != t {
		minute++
	}
	if minute < 0 {
		return 0, fmt.Errorf("wake code minute before unix epoch")
	}
	validatedMinute := uint64(minute)
	if validatedMinute > maxWakeCodeMinute {
		return 0, fmt.Errorf("wake code minute out of range")
	}

	return uint32(validatedMinute), nil
}

func currentUnixMinute(t time.Time) (uint32, error) {
	minute := t.UTC().Unix() / 60
	if minute < 0 {
		return 0, fmt.Errorf("wake code minute before unix epoch")
	}
	validatedMinute := uint64(minute)
	if validatedMinute > maxWakeCodeMinute {
		return 0, fmt.Errorf("wake code minute out of range")
	}

	return uint32(validatedMinute), nil
}
