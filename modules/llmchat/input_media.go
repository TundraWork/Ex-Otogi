package llmchat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"ex-otogi/pkg/otogi"
)

const maxImageInputCaptionRunes = 240

var errImageInputTooLarge = errors.New("llmchat image input exceeds size limit")

type currentImageInput struct {
	ArticleID    string
	AttachmentID string
	MIMEType     string
	FileName     string
	Caption      string
	Data         []byte
}

type imageInputBudget struct {
	remainingImages int
	remainingBytes  int64
}

type limitedWriteBuffer struct {
	limit int64
	size  int64
	buf   bytes.Buffer
}

func newImageInputBudget(policy ImageInputPolicy) *imageInputBudget {
	return &imageInputBudget{
		remainingImages: policy.MaxImages,
		remainingBytes:  policy.MaxTotalBytes,
	}
}

func (m *Module) buildMessageWithImageInputs(
	role otogi.LLMMessageRole,
	text string,
	detail otogi.LLMInputImageDetail,
	imageInputs []currentImageInput,
) otogi.LLMMessage {
	if strings.TrimSpace(text) == "" {
		return otogi.LLMMessage{Role: role, Content: text}
	}
	if role != otogi.LLMMessageRoleUser || len(imageInputs) == 0 {
		return otogi.LLMMessage{Role: role, Content: text}
	}

	parts := make([]otogi.LLMMessagePart, 0, len(imageInputs)+1)
	parts = append(parts, otogi.LLMMessagePart{
		Type: otogi.LLMMessagePartTypeText,
		Text: text + "\n" + serializeMessageImageInputs(imageInputs),
	})
	for _, input := range imageInputs {
		image := otogi.LLMInputImage{
			MIMEType: input.MIMEType,
			Data:     append([]byte(nil), input.Data...),
			Detail:   detail,
		}
		parts = append(parts, otogi.LLMMessagePart{
			Type:  otogi.LLMMessagePartTypeImage,
			Image: &image,
		})
	}

	return otogi.LLMMessage{
		Role:  role,
		Parts: parts,
	}
}

func (m *Module) collectArticleImageInputs(
	ctx context.Context,
	event *otogi.Event,
	article otogi.Article,
	policy ImageInputPolicy,
	budget *imageInputBudget,
) []currentImageInput {
	if !policy.Enabled || event == nil || len(article.Media) == 0 || budget == nil {
		return nil
	}

	candidates := filterImageInputCandidates(article.Media)
	if len(candidates) == 0 {
		return nil
	}
	if m == nil || m.mediaDownloader == nil {
		m.logImageInputSkip(ctx, event, article.ID, "", "media_downloader_unavailable", nil)
		return nil
	}

	requestedCount := minInt(len(candidates), budget.remainingImages)
	results := make([]currentImageInput, 0, requestedCount)
	for _, attachment := range candidates {
		if budget.remainingImages <= 0 {
			m.logImageInputSkip(ctx, event, article.ID, attachment.ID, "max_images_reached", nil)
			break
		}
		if budget.remainingBytes <= 0 {
			m.logImageInputSkip(ctx, event, article.ID, attachment.ID, "max_total_bytes_reached", nil)
			break
		}

		perImageLimit := minInt64(policy.MaxImageBytes, budget.remainingBytes)
		if attachment.SizeBytes > 0 && attachment.SizeBytes > perImageLimit {
			m.logImageInputSkip(
				ctx,
				event,
				article.ID,
				attachment.ID,
				fmt.Sprintf("attachment_too_large limit=%d size=%d", perImageLimit, attachment.SizeBytes),
				nil,
			)
			continue
		}
		if strings.TrimSpace(attachment.ID) == "" {
			m.logImageInputSkip(ctx, event, article.ID, "", "missing_attachment_id", nil)
			continue
		}

		request := otogi.MediaDownloadRequest{
			Source:       event.Source,
			Conversation: event.Conversation,
			ArticleID:    article.ID,
			AttachmentID: attachment.ID,
		}
		if err := request.Validate(); err != nil {
			m.logImageInputSkip(ctx, event, article.ID, attachment.ID, "build_media_request_failed", err)
			continue
		}

		buffer := limitedWriteBuffer{limit: perImageLimit}
		downloaded, err := m.mediaDownloader.Download(ctx, request, &buffer)
		if err != nil {
			reason := "download_failed"
			if errors.Is(err, errImageInputTooLarge) {
				reason = "download_limit_exceeded"
			}
			m.logImageInputSkip(ctx, event, article.ID, attachment.ID, reason, err)
			continue
		}

		data := buffer.Bytes()
		if len(data) == 0 {
			m.logImageInputSkip(ctx, event, article.ID, attachment.ID, "empty_download", nil)
			continue
		}
		mimeType := resolveImageInputMIMEType(attachment, downloaded, data)
		if !isSupportedImageInputMIMEType(mimeType) {
			m.logImageInputSkip(ctx, event, article.ID, attachment.ID, "unsupported_image_mime_type", nil)
			continue
		}

		metadata := mergeImageAttachmentMetadata(attachment, downloaded)
		results = append(results, currentImageInput{
			ArticleID:    article.ID,
			AttachmentID: metadata.ID,
			MIMEType:     mimeType,
			FileName:     metadata.FileName,
			Caption:      metadata.Caption,
			Data:         data,
		})
		budget.remainingImages--
		budget.remainingBytes -= int64(len(data))
	}

	return results
}

func filterImageInputCandidates(media []otogi.MediaAttachment) []otogi.MediaAttachment {
	if len(media) == 0 {
		return nil
	}

	candidates := make([]otogi.MediaAttachment, 0, len(media))
	for _, attachment := range media {
		if isImageInputCandidate(attachment) {
			candidates = append(candidates, attachment)
		}
	}

	return candidates
}

func hasImageInputCandidates(article otogi.Article) bool {
	return len(filterImageInputCandidates(article.Media)) > 0
}

func isImageInputCandidate(attachment otogi.MediaAttachment) bool {
	switch attachment.Type {
	case otogi.MediaTypePhoto:
		return true
	case otogi.MediaTypeDocument:
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(attachment.MIMEType)), "image/")
	default:
		return false
	}
}

func mergeImageAttachmentMetadata(
	original otogi.MediaAttachment,
	downloaded otogi.MediaAttachment,
) otogi.MediaAttachment {
	merged := original
	if strings.TrimSpace(merged.ID) == "" {
		merged.ID = strings.TrimSpace(downloaded.ID)
	}
	if strings.TrimSpace(downloaded.MIMEType) != "" {
		merged.MIMEType = downloaded.MIMEType
	}
	if strings.TrimSpace(merged.FileName) == "" {
		merged.FileName = downloaded.FileName
	}
	if strings.TrimSpace(merged.Caption) == "" {
		merged.Caption = downloaded.Caption
	}
	if downloaded.SizeBytes > 0 {
		merged.SizeBytes = downloaded.SizeBytes
	}

	return merged
}

func resolveImageInputMIMEType(
	original otogi.MediaAttachment,
	downloaded otogi.MediaAttachment,
	data []byte,
) string {
	for _, candidate := range []string{
		strings.ToLower(strings.TrimSpace(downloaded.MIMEType)),
		strings.ToLower(strings.TrimSpace(original.MIMEType)),
	} {
		if strings.HasPrefix(candidate, "image/") {
			return candidate
		}
	}

	detected := strings.ToLower(strings.TrimSpace(http.DetectContentType(data)))
	if strings.HasPrefix(detected, "image/") {
		return detected
	}

	return ""
}

func isSupportedImageInputMIMEType(mimeType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "image/")
}

func serializeMessageImageInputs(inputs []currentImageInput) string {
	var builder strings.Builder
	builder.WriteString("<image_inputs")
	writeIntAttr(&builder, "count", len(inputs))
	builder.WriteString(">\n")
	for index, input := range inputs {
		builder.WriteString("<image_input")
		writeIntAttr(&builder, "index", index+1)
		writeStringAttr(&builder, "article_id", input.ArticleID)
		writeStringAttr(&builder, "attachment_id", input.AttachmentID)
		writeStringAttr(&builder, "mime_type", input.MIMEType)
		writeStringAttr(&builder, "file_name", input.FileName)
		builder.WriteString(">\n")
		if strings.TrimSpace(input.Caption) != "" {
			caption, truncated := trimContextText(input.Caption, maxImageInputCaptionRunes)
			builder.WriteString("<caption")
			writeBoolAttr(&builder, "truncated", truncated)
			builder.WriteString(">")
			builder.WriteString(escapeStructuredContent(caption))
			builder.WriteString("</caption>\n")
		}
		builder.WriteString("</image_input>\n")
	}
	builder.WriteString("</image_inputs>")

	return builder.String()
}

func (m *Module) logImageInputSkip(
	ctx context.Context,
	event *otogi.Event,
	articleID string,
	attachmentID string,
	reason string,
	err error,
) {
	if m == nil || m.logger == nil || event == nil {
		return
	}

	args := []any{
		"source_platform", event.Source.Platform,
		"source_id", event.Source.ID,
		"conversation_id", event.Conversation.ID,
		"article_id", articleID,
		"attachment_id", attachmentID,
		"reason", reason,
		"deadline_remaining", describeContextDeadlineRemaining(ctx),
	}
	if err != nil {
		args = append(args, "error", err)
	}

	m.logger.WarnContext(ctx, "llmchat skipped image input", args...)
}

func (b *limitedWriteBuffer) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if b.limit <= 0 {
		return 0, errImageInputTooLarge
	}

	remaining := b.limit - b.size
	if remaining <= 0 {
		return 0, errImageInputTooLarge
	}
	if int64(len(data)) > remaining {
		written, err := b.buf.Write(data[:remaining])
		b.size += int64(written)
		if err != nil {
			return written, fmt.Errorf("write limited buffer partial chunk: %w", err)
		}
		return written, errImageInputTooLarge
	}

	written, err := b.buf.Write(data)
	b.size += int64(written)
	if err != nil {
		return written, fmt.Errorf("write limited buffer chunk: %w", err)
	}

	return written, nil
}

func (b *limitedWriteBuffer) Bytes() []byte {
	return append([]byte(nil), b.buf.Bytes()...)
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}

	return right
}

func minInt64(left int64, right int64) int64 {
	if left < right {
		return left
	}

	return right
}
