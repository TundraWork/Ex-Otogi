package mapper

import (
	"fmt"
	"unicode/utf8"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const extensionNamespace = "https://ex-otogi.dev/protocol/telegram/layer228"

// MaxEntities bounds one Telegram text entity vector before allocation.
const MaxEntities = 128

// ConvertEntities converts Telegram UTF-16 entity ranges into UTF-8 byte annotations.
// Unsupported and malformed entities are retained as deterministic Telegram extensions.
func ConvertEntities(text string, entities []tg.MessageEntityClass) ([]otogi.Annotation, []otogi.Extension, error) {
	if !utf8.ValidString(text) {
		return nil, nil, fmt.Errorf("telegram entity text is not UTF-8: %w", otogi.ErrInvalid)
	}
	if len(entities) > MaxEntities {
		return nil, nil, fmt.Errorf("telegram entity count exceeds %d: %w", MaxEntities, otogi.ErrCapacity)
	}
	annotations := make([]otogi.Annotation, 0, len(entities))
	extensions := make([]otogi.Extension, 0)
	for i, entity := range entities {
		if entity == nil {
			return nil, nil, fmt.Errorf("telegram entity %d is nil: %w", i, otogi.ErrInvalid)
		}
		kind, target, known := entityProjection(entity)
		start, end, valid := utf16Range(text, entity.GetOffset(), entity.GetLength())
		if !known || !valid {
			name := "unknown-entity"
			if known {
				name = "malformed-entity"
			}
			ext, err := telegramExtension(name, entity)
			if err != nil {
				return nil, nil, err
			}
			if len(ext.Data()) > MaxPreservationBytes {
				return nil, nil, fmt.Errorf("telegram entity preservation exceeds %d bytes: %w", MaxPreservationBytes, otogi.ErrCapacity)
			}
			if len(extensions) == 64 {
				return nil, nil, fmt.Errorf("telegram entity preservation count exceeds 64: %w", otogi.ErrCapacity)
			}
			extensions = append(extensions, ext)
			continue
		}
		annotation, err := otogi.NewAnnotation(fmt.Sprintf("telegram-entity-%d", i), start, end-start, kind, target, nil)
		if err != nil {
			return nil, nil, err
		}
		annotations = append(annotations, annotation)
	}
	return annotations, extensions, nil
}

func entityProjection(entity tg.MessageEntityClass) (otogi.AnnotationKind, string, bool) {
	switch v := entity.(type) {
	case *tg.MessageEntityMention:
		return otogi.AnnotationMention, "", true
	case *tg.MessageEntityHashtag, *tg.MessageEntityCashtag:
		return otogi.AnnotationHashtag, "", true
	case *tg.MessageEntityBotCommand:
		return otogi.AnnotationCommand, "", true
	case *tg.MessageEntityURL:
		return otogi.AnnotationLink, "", true
	case *tg.MessageEntityEmail:
		return otogi.AnnotationEmail, "", true
	case *tg.MessageEntityBold:
		return otogi.AnnotationBold, "", true
	case *tg.MessageEntityItalic:
		return otogi.AnnotationItalic, "", true
	case *tg.MessageEntityCode:
		return otogi.AnnotationCode, "", true
	case *tg.MessageEntityPre:
		return otogi.AnnotationPreformatted, v.Language, true
	case *tg.MessageEntityTextURL:
		return otogi.AnnotationLink, v.URL, true
	case *tg.MessageEntityMentionName:
		return otogi.AnnotationMention, fmt.Sprintf("telegram:user:%d", v.UserID), true
	case *tg.MessageEntityUnderline:
		return otogi.AnnotationUnderline, "", true
	case *tg.MessageEntityStrike:
		return otogi.AnnotationStrike, "", true
	case *tg.MessageEntitySpoiler:
		return otogi.AnnotationSpoiler, "", true
	case *tg.MessageEntityCustomEmoji:
		return otogi.AnnotationEmoji, fmt.Sprintf("telegram:document:%d", v.DocumentID), true
	case *tg.MessageEntityBlockquote:
		return otogi.AnnotationBlockQuote, "", true
	case *tg.MessageEntityFormattedDate:
		return otogi.AnnotationTimestamp, "", true
	default:
		return "", "", false
	}
}

func utf16Range(text string, offset, length int) (int, int, bool) {
	if offset < 0 || length < 0 || offset > int(^uint(0)>>1)-length {
		return 0, 0, false
	}
	wantStart, wantEnd := offset, offset+length
	units, start, end := 0, -1, -1
	if wantStart == 0 {
		start = 0
	}
	if wantEnd == 0 {
		end = 0
	}
	for byteIndex, r := range text {
		if units == wantStart {
			start = byteIndex
		}
		if units == wantEnd {
			end = byteIndex
		}
		step := 1
		if r > 0xffff {
			step = 2
		}
		if wantStart > units && wantStart < units+step || wantEnd > units && wantEnd < units+step {
			return 0, 0, false
		}
		units += step
	}
	if units == wantStart {
		start = len(text)
	}
	if units == wantEnd {
		end = len(text)
	}
	return start, end, start >= 0 && end >= start
}

func telegramExtension(name string, value interface{ Encode(*bin.Buffer) error }) (otogi.Extension, error) {
	var b bin.Buffer
	if err := value.Encode(&b); err != nil {
		return otogi.Extension{}, fmt.Errorf("encode Telegram %s: %w", name, err)
	}
	return otogi.NewExtension(extensionNamespace, name, 1, "application/x.telegram-tl", b.Copy())
}

func utf8Boundary(s string, offset int) bool {
	return offset >= 0 && offset <= len(s) && (offset == len(s) || utf8.RuneStart(s[offset]))
}
