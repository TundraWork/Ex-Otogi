package mapper

import (
	"fmt"
	"unicode/utf8"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const extensionNamespace = "https://ex-otogi.dev/protocol/telegram/layer228"

// ConvertEntities converts Telegram UTF-16 entity ranges into UTF-8 byte
// annotations. Unknown entities, and entities whose range does not fall on
// character boundaries, are kept as Telegram extensions instead.
func ConvertEntities(text string, entities []tg.MessageEntityClass) ([]otogi.Annotation, []otogi.Extension, error) {
	var annotations []otogi.Annotation
	var extensions []otogi.Extension
	for i, entity := range entities {
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
			extensions = append(extensions, ext)
			continue
		}
		annotations = append(annotations, otogi.Annotation{ID: fmt.Sprintf("telegram-entity-%d", i), Offset: start, Length: end - start, Kind: kind, Target: target})
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
		if v.Collapsed {
			return otogi.AnnotationExpandableBlockQuote, "", true
		}
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
	boundary := func(i int) bool { return i == len(text) || utf8.RuneStart(text[i]) }
	return start, end, start >= 0 && end >= start && boundary(start) && boundary(end)
}

func telegramExtension(name string, value interface{ Encode(*bin.Buffer) error }) (otogi.Extension, error) {
	var b bin.Buffer
	if err := value.Encode(&b); err != nil {
		return otogi.Extension{}, fmt.Errorf("encode Telegram %s: %w", name, err)
	}
	return otogi.Extension{Namespace: extensionNamespace, Name: name, Version: 1, MediaType: "application/x.telegram-tl", Data: b.Copy()}, nil
}
