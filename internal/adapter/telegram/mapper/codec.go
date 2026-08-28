package mapper

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

const (
	// NamespaceCommon identifies Telegram's account-wide cloud message ID box.
	NamespaceCommon = "common"
	// NamespaceChannel identifies a Telegram channel-specific message ID box.
	NamespaceChannel = "channel"
	// MaxPreservationBytes bounds deterministic Telegram preservation envelopes.
	MaxPreservationBytes = 64 << 10
)

// MessageLocator is the durable Telegram origin needed to refetch a cloud message.
type MessageLocator struct {
	// Namespace identifies the common or channel message ID space.
	Namespace string
	// Peer is the canonical tagged conversation identifier.
	Peer string
	// MessageID is the positive Telegram message ID.
	MessageID int
}

// MediaLocator is the durable Telegram identity and refresh material for lazy media.
type MediaLocator struct {
	// Kind identifies the Telegram media constructor family.
	Kind string
	// ID is the Telegram media identifier.
	ID int64
	// AccessHash authorizes access to the media identity.
	AccessHash int64
	// FileReference is the expiring Telegram refresh token retained byte-for-byte.
	FileReference []byte
	// DCID identifies the Telegram data center holding the media.
	DCID int
	// Variant optionally identifies a thumbnail or alternate quality selector.
	Variant string
}

// QuarantineRecord contains a deterministic unsupported-update preservation envelope.
type QuarantineRecord struct {
	// SourceKey is the deterministic provider deduplication key.
	SourceKey string
	// Kind is the namespaced Telegram constructor name.
	Kind string
	// Payload is the bounded versioned preservation envelope.
	Payload []byte
}

// EncodeMessageLocator encodes a bounded versioned provider locator.
func EncodeMessageLocator(locator MessageLocator) ([]byte, error) {
	if err := validateMessageLocator(locator); err != nil {
		return nil, err
	}
	return encodeFields(locator.Namespace, locator.Peer, strconv.Itoa(locator.MessageID)), nil
}

// DecodeMessageLocator decodes a versioned provider locator.
func DecodeMessageLocator(data []byte) (MessageLocator, error) {
	fields, err := decodeFields(data, 3)
	if err != nil {
		return MessageLocator{}, err
	}
	id, err := strconv.Atoi(fields[2])
	if err != nil {
		return MessageLocator{}, fmt.Errorf("message locator ID: %w", err)
	}
	locator := MessageLocator{Namespace: fields[0], Peer: fields[1], MessageID: id}
	if err := validateMessageLocator(locator); err != nil {
		return MessageLocator{}, err
	}
	return locator, nil
}

// EncodeMediaLocator encodes bounded media identity and refresh bytes.
func EncodeMediaLocator(locator MediaLocator) ([]byte, error) {
	if locator.Kind == "" || locator.ID <= 0 || locator.DCID < 0 || len(locator.FileReference) > 16<<10 {
		return nil, fmt.Errorf("media locator: invalid")
	}
	return encodeFields(locator.Kind, strconv.FormatInt(locator.ID, 10), strconv.FormatInt(locator.AccessHash, 10), strconv.Itoa(locator.DCID), locator.Variant, string(locator.FileReference)), nil
}

// DecodeMediaLocator decodes bounded media identity and refresh bytes.
func DecodeMediaLocator(data []byte) (MediaLocator, error) {
	fields, err := decodeFields(data, 6)
	if err != nil {
		return MediaLocator{}, err
	}
	id, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return MediaLocator{}, err
	}
	hash, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return MediaLocator{}, err
	}
	dc, err := strconv.Atoi(fields[3])
	if err != nil {
		return MediaLocator{}, err
	}
	locator := MediaLocator{Kind: fields[0], ID: id, AccessHash: hash, DCID: dc, Variant: fields[4], FileReference: []byte(fields[5])}
	if _, err := EncodeMediaLocator(locator); err != nil {
		return MediaLocator{}, err
	}
	return locator, nil
}

// RefetchRequest selects the Telegram RPC request shape without performing networking.
func RefetchRequest(locator MessageLocator, channel tg.InputChannelClass) (any, error) {
	if err := validateMessageLocator(locator); err != nil {
		return nil, err
	}
	ids := []tg.InputMessageClass{&tg.InputMessageID{ID: locator.MessageID}}
	switch locator.Namespace {
	case NamespaceCommon:
		return &tg.MessagesGetMessagesRequest{ID: ids}, nil
	case NamespaceChannel:
		if channel == nil {
			return nil, fmt.Errorf("channel locator requires access hash")
		}
		return &tg.ChannelsGetMessagesRequest{Channel: channel, ID: ids}, nil
	default:
		return nil, fmt.Errorf("unsupported refetch namespace %q", locator.Namespace)
	}
}

// Quarantine deterministically preserves an unsupported update and bounded provider context.
func Quarantine(update tg.UpdateClass, account string, position int) (QuarantineRecord, error) {
	if update == nil || account == "" {
		return QuarantineRecord{}, fmt.Errorf("quarantine input: invalid")
	}
	var raw bin.Buffer
	if err := update.Encode(&raw); err != nil {
		return QuarantineRecord{}, err
	}
	payload := encodeFields(account, strconv.Itoa(position), string(raw.Copy()))
	if len(payload) > MaxPreservationBytes {
		return QuarantineRecord{}, fmt.Errorf("quarantine payload exceeds %d bytes", MaxPreservationBytes)
	}
	digest := sha256.Sum256(payload)
	return QuarantineRecord{SourceKey: fmt.Sprintf("telegram:layer228:%x", digest[:]), Kind: "telegram.layer228." + update.TypeName(), Payload: payload}, nil
}

// DecodeQuarantinedUpdate decodes the versioned preservation envelope into its original TL update.
func DecodeQuarantinedUpdate(payload []byte) (tg.UpdateClass, error) {
	fields, err := decodeBinaryFields(payload, 3)
	if err != nil {
		return nil, err
	}
	raw := bin.Buffer{Buf: fields[2]}
	update, err := tg.DecodeUpdate(&raw)
	if err != nil {
		return nil, err
	}
	if raw.Len() != 0 {
		return nil, fmt.Errorf("trailing quarantined update bytes: %d", raw.Len())
	}
	return update, nil
}

func decodeBinaryFields(data []byte, count int) ([][]byte, error) {
	if len(data) < 2 || len(data) > MaxPreservationBytes || binary.LittleEndian.Uint16(data[:2]) != 1 {
		return nil, fmt.Errorf("invalid preservation envelope")
	}
	fields := make([][]byte, 0, count)
	data = data[2:]
	for len(fields) < count {
		if len(data) < 4 {
			return nil, fmt.Errorf("truncated preservation envelope")
		}
		n := int(binary.LittleEndian.Uint32(data[:4]))
		data = data[4:]
		if n > len(data) {
			return nil, fmt.Errorf("truncated preservation field")
		}
		fields = append(fields, data[:n])
		data = data[n:]
	}
	if len(data) != 0 {
		return nil, fmt.Errorf("trailing preservation fields")
	}
	return fields, nil
}

func validateMessageLocator(locator MessageLocator) error {
	if locator.MessageID <= 0 || locator.Peer == "" || locator.Namespace != NamespaceCommon && locator.Namespace != NamespaceChannel {
		return fmt.Errorf("message locator: invalid")
	}
	return nil
}

func encodeFields(fields ...string) []byte {
	size := 2
	for _, field := range fields {
		size += 4 + len(field)
	}
	data := make([]byte, 2, size)
	binary.LittleEndian.PutUint16(data, 1)
	for _, field := range fields {
		var length [4]byte
		binary.LittleEndian.PutUint32(length[:], uint32(len(field)))
		data = append(data, length[:]...)
		data = append(data, field...)
	}
	return data
}

func decodeFields(data []byte, count int) ([]string, error) {
	if len(data) < 2 || len(data) > MaxPreservationBytes || binary.LittleEndian.Uint16(data[:2]) != 1 {
		return nil, fmt.Errorf("invalid locator envelope")
	}
	fields := make([]string, 0, count)
	data = data[2:]
	for len(fields) < count {
		if len(data) < 4 {
			return nil, fmt.Errorf("truncated locator envelope")
		}
		n := int(binary.LittleEndian.Uint32(data[:4]))
		data = data[4:]
		if n < 0 || n > len(data) {
			return nil, fmt.Errorf("truncated locator field")
		}
		fields = append(fields, string(data[:n]))
		data = data[n:]
	}
	if len(data) != 0 || strings.ContainsRune(strings.Join(fields, ""), '\x00') {
		return nil, fmt.Errorf("invalid locator fields")
	}
	return fields, nil
}
