package sleep

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/platform"
)

const (
	wakeCodeDomain            = "platform.sleep.wake.v2"
	wakeCodeVersion      byte = 2
	wakeCodeTagSize           = 10
	minSigningKeySize         = 32
	maxWakeCodeMinute         = uint64(1<<32 - 1)
	maxWakeCodeFieldSize      = 1<<16 - 1
)

// codeScope binds a wake code to one user and carries the original target
// conversation/sink where permissions should be restored.
type codeScope struct {
	UserID           string
	ConversationID   string
	ConversationType platform.ConversationType
	SourcePlatform   platform.Platform
	SourceID         string
}

type wakeTargetScope struct {
	ConversationID   string
	ConversationType platform.ConversationType
	SourcePlatform   platform.Platform
	SourceID         string
}

type wakeCodeClaims struct {
	version      byte
	expiryMinute uint32
	target       wakeTargetScope
}

// codeManager handles generation and validation of stateless wake codes.
//
// Wake codes are valid only for the same user. The target conversation and sink
// are embedded in the code so the user can wake from a different chat.
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

	claims := wakeCodeClaims{
		version:      wakeCodeVersion,
		expiryMinute: expiryMinute,
		target:       scope.target(),
	}
	payload, err := cm.encodeWakeCode(scope.UserID, claims)
	if err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(payload), nil
}

// Validate checks a wake code against the current user and time, then
// returns the original target conversation and sink to restore.
func (cm *codeManager) Validate(
	code string,
	userID string,
	now time.Time,
) (wakeTargetScope, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(code))
	if err != nil {
		return wakeTargetScope{}, fmt.Errorf("decode wake code: %w", err)
	}
	claims, tag, err := decodeWakeCode(decoded)
	if err != nil {
		return wakeTargetScope{}, err
	}

	currentMinute, err := currentUnixMinute(now)
	if err != nil {
		return wakeTargetScope{}, fmt.Errorf("derive current minute: %w", err)
	}
	if currentMinute > claims.expiryMinute {
		return wakeTargetScope{}, fmt.Errorf("expired or invalid wake code")
	}

	expectedMAC := cm.computeMAC(userID, claims)
	if !hmac.Equal(tag, expectedMAC[:wakeCodeTagSize]) {
		return wakeTargetScope{}, fmt.Errorf("expired or invalid wake code")
	}
	if claims.version != wakeCodeVersion {
		return wakeTargetScope{}, fmt.Errorf("malformed wake code")
	}

	return claims.target, nil
}

func (cm *codeManager) encodeWakeCode(userID string, claims wakeCodeClaims) ([]byte, error) {
	payload := make([]byte, 0, 64)
	payload = append(payload, claims.version)
	payload = binary.BigEndian.AppendUint32(payload, claims.expiryMinute)

	var err error
	payload, err = appendLengthPrefixed16(payload, claims.target.ConversationID)
	if err != nil {
		return nil, err
	}
	payload, err = appendLengthPrefixed16(payload, string(claims.target.ConversationType))
	if err != nil {
		return nil, err
	}
	payload, err = appendLengthPrefixed16(payload, string(claims.target.SourcePlatform))
	if err != nil {
		return nil, err
	}
	payload, err = appendLengthPrefixed16(payload, claims.target.SourceID)
	if err != nil {
		return nil, err
	}

	mac := cm.computeMAC(userID, claims)
	payload = append(payload, mac[:wakeCodeTagSize]...)

	return payload, nil
}

func (cm *codeManager) computeMAC(userID string, claims wakeCodeClaims) []byte {
	payload := make([]byte, 0, len(wakeCodeDomain)+1+4+5*8)
	payload = append(payload, wakeCodeDomain...)
	payload = append(payload, claims.version)
	payload = binary.BigEndian.AppendUint32(payload, claims.expiryMinute)
	payload = appendLengthPrefixed(payload, userID)
	payload = appendLengthPrefixed(payload, claims.target.ConversationID)
	payload = appendLengthPrefixed(payload, string(claims.target.ConversationType))
	payload = appendLengthPrefixed(payload, string(claims.target.SourcePlatform))
	payload = appendLengthPrefixed(payload, claims.target.SourceID)

	h := hmac.New(sha256.New, cm.key)
	_, _ = h.Write(payload)

	return h.Sum(nil)
}

func appendLengthPrefixed(payload []byte, value string) []byte {
	payload = binary.BigEndian.AppendUint64(payload, uint64(len(value)))
	return append(payload, value...)
}

func appendLengthPrefixed16(payload []byte, value string) ([]byte, error) {
	if len(value) > maxWakeCodeFieldSize {
		return nil, fmt.Errorf("wake code field too large")
	}

	payload = append(payload, byte(len(value)>>8), byte(len(value)))
	payload = append(payload, value...)

	return payload, nil
}

func decodeWakeCode(decoded []byte) (wakeCodeClaims, []byte, error) {
	if len(decoded) < 1+4+wakeCodeTagSize {
		return wakeCodeClaims{}, nil, fmt.Errorf("malformed wake code")
	}

	version := decoded[0]
	if version != wakeCodeVersion {
		return wakeCodeClaims{}, nil, fmt.Errorf("malformed wake code")
	}

	expiryMinute := binary.BigEndian.Uint32(decoded[1:5])
	offset := 5
	conversationID, nextOffset, err := consumeLengthPrefixed16(decoded, offset)
	if err != nil {
		return wakeCodeClaims{}, nil, err
	}
	offset = nextOffset

	conversationType, nextOffset, err := consumeLengthPrefixed16(decoded, offset)
	if err != nil {
		return wakeCodeClaims{}, nil, err
	}
	offset = nextOffset

	sourcePlatform, nextOffset, err := consumeLengthPrefixed16(decoded, offset)
	if err != nil {
		return wakeCodeClaims{}, nil, err
	}
	offset = nextOffset

	sourceID, nextOffset, err := consumeLengthPrefixed16(decoded, offset)
	if err != nil {
		return wakeCodeClaims{}, nil, err
	}
	offset = nextOffset

	if len(decoded)-offset != wakeCodeTagSize {
		return wakeCodeClaims{}, nil, fmt.Errorf("malformed wake code")
	}

	return wakeCodeClaims{
		version:      version,
		expiryMinute: expiryMinute,
		target: wakeTargetScope{
			ConversationID:   conversationID,
			ConversationType: platform.ConversationType(conversationType),
			SourcePlatform:   platform.Platform(sourcePlatform),
			SourceID:         sourceID,
		},
	}, decoded[offset:], nil
}

func consumeLengthPrefixed16(payload []byte, offset int) (string, int, error) {
	if len(payload)-offset < 2 {
		return "", 0, fmt.Errorf("malformed wake code")
	}

	length := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
	offset += 2
	if len(payload)-offset < length {
		return "", 0, fmt.Errorf("malformed wake code")
	}

	return string(payload[offset : offset+length]), offset + length, nil
}

func (scope codeScope) target() wakeTargetScope {
	return wakeTargetScope{
		ConversationID:   scope.ConversationID,
		ConversationType: scope.ConversationType,
		SourcePlatform:   scope.SourcePlatform,
		SourceID:         scope.SourceID,
	}
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
