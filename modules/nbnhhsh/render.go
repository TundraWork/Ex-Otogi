package nbnhhsh

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"ex-otogi/pkg/otogi"
)

const (
	noAbbreviationMessage = "您的查询内容不包含任何英文或数字缩写词。"
	replyNotFoundMessage  = "抱歉，您回复的消息不在我的记忆中~"
	replyNoTextMessage    = "您回复的消息不包含可供翻译的文本。"
	timeoutMessage        = "处理超时，请过一会儿再试试吧~"
	invalidDataMessage    = "处理数据出现问题，请告诉我的主人吧~"
	emptyResultMessage    = "没有找到可解释的缩写结果。"
)

var abbreviationPattern = regexp.MustCompile(`[A-Za-z0-9]+`)

type renderedMessage struct {
	Text     string
	Entities []otogi.TextEntity
}

type richTextBuilder struct {
	text     strings.Builder
	entities []otogi.TextEntity
	offset   int
}

func (b *richTextBuilder) Write(text string) {
	if text == "" {
		return
	}

	b.text.WriteString(text)
	b.offset += utf8.RuneCountInString(text)
}

func (b *richTextBuilder) WriteEntity(text string, entityType otogi.TextEntityType) {
	if text == "" {
		return
	}

	length := utf8.RuneCountInString(text)
	b.entities = append(b.entities, otogi.TextEntity{
		Type:   entityType,
		Offset: b.offset,
		Length: length,
	})
	b.Write(text)
}

func (b *richTextBuilder) Message() renderedMessage {
	message := renderedMessage{
		Text: b.text.String(),
	}
	if len(b.entities) > 0 {
		message.Entities = append([]otogi.TextEntity(nil), b.entities...)
	}

	return message
}

func usageMessage() renderedMessage {
	var builder richTextBuilder
	builder.Write("用法：\n")
	builder.WriteEntity("/nbnhhsh <欲翻译的内容>", otogi.TextEntityTypeCode)
	builder.Write("\n或使用 ")
	builder.WriteEntity("/nbnhhsh", otogi.TextEntityTypeCode)
	builder.Write(" 回复您想翻译的消息。\n注：如果觉得 ")
	builder.WriteEntity("/nbnhhsh", otogi.TextEntityTypeCode)
	builder.Write(" 太长，用 ")
	builder.WriteEntity("/srh", otogi.TextEntityTypeCode)
	builder.Write(" 也可以。")

	return builder.Message()
}

func renderGuessResults(results []guessResult) renderedMessage {
	if len(results) == 0 {
		return plainMessage(emptyResultMessage)
	}

	var builder richTextBuilder
	renderedCount := 0
	for _, result := range results {
		if strings.TrimSpace(result.Name) == "" {
			continue
		}
		if renderedCount > 0 {
			builder.Write("\n\n")
		}
		renderedCount++

		builder.WriteEntity(result.Name, otogi.TextEntityTypeBold)
		builder.Write("\n")

		switch {
		case len(result.Trans) > 1:
			builder.WriteEntity("它可能的含义有：", otogi.TextEntityTypeItalic)
			builder.Write("\n")
			builder.Write(strings.Join(result.Trans, " / "))
		case len(result.Trans) == 1:
			builder.WriteEntity("它应该是：", otogi.TextEntityTypeItalic)
			builder.Write("\n")
			builder.Write(result.Trans[0])
		case len(result.Inputting) > 0:
			builder.WriteEntity("我没听说过它，但我猜它也许是：", otogi.TextEntityTypeItalic)
			builder.Write("\n")
			builder.Write(strings.Join(result.Inputting, " / "))
		default:
			builder.WriteEntity(
				"可能不是抽象话，也可能太抽象了，你可以试试 /translate",
				otogi.TextEntityTypeItalic,
			)
		}
	}

	if renderedCount == 0 {
		return plainMessage(emptyResultMessage)
	}

	return builder.Message()
}

func extractAbbreviations(text string) []string {
	matches := abbreviationPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(matches))
	tokens := make([]string, 0, len(matches))
	for _, match := range matches {
		if _, exists := seen[match]; exists {
			continue
		}

		seen[match] = struct{}{}
		tokens = append(tokens, match)
	}

	return tokens
}

func plainMessage(text string) renderedMessage {
	return renderedMessage{Text: text}
}
