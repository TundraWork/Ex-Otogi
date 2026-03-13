package llmchat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"ex-otogi/pkg/otogi"
	"golang.org/x/sync/errgroup"
)

const (
	maxImageInputCaptionRunes        = 240
	maxConcurrentImageInputDownloads = 4
)

var errImageInputTooLarge = errors.New("llmchat image input exceeds size limit")

type currentImageInput struct {
	ArticleID    string
	AttachmentID string
	MIMEType     string
	FileName     string
	Caption      string
	Data         []byte
}

type imageInputDownloadJob struct {
	order      int
	articleID  string
	attachment otogi.MediaAttachment
}

type imageInputDownloadResult struct {
	job   imageInputDownloadJob
	input currentImageInput
	ok    bool
}

type limitedWriteBuffer struct {
	limit int64
	size  int64
	buf   bytes.Buffer
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

func (m *Module) collectContextImageInputs(
	ctx context.Context,
	event *otogi.Event,
	articles []otogi.Article,
	policy ImageInputPolicy,
) []currentImageInput {
	if !policy.Enabled || event == nil || len(articles) == 0 {
		return nil
	}
	jobs := buildImageInputDownloadJobs(articles)
	if len(jobs) == 0 {
		return nil
	}
	if m == nil || m.mediaDownloader == nil {
		for _, article := range articles {
			if hasImageInputCandidates(article) {
				m.logImageInputSkip(ctx, event, article.ID, "", "media_downloader_unavailable", nil)
			}
		}
		return nil
	}

	results := make([]imageInputDownloadResult, len(jobs))
	workerCount := minInt(maxConcurrentImageInputDownloads, len(jobs))
	g, downloadCtx := errgroup.WithContext(ctx)
	semaphore := make(chan struct{}, workerCount)
	for index, job := range jobs {
		index := index
		job := job
		g.Go(func() error {
			defer func() {
				if panicValue := recover(); panicValue != nil {
					m.logImageInputSkip(
						downloadCtx,
						event,
						job.articleID,
						job.attachment.ID,
						"download_worker_panic",
						fmt.Errorf("%v", panicValue),
					)
				}
			}()

			select {
			case semaphore <- struct{}{}:
			case <-downloadCtx.Done():
				return nil
			}
			defer func() { <-semaphore }()

			input, ok := m.downloadImageInput(downloadCtx, event, job.articleID, job.attachment, policy)
			if ok {
				results[index] = imageInputDownloadResult{
					job:   job,
					input: input,
					ok:    true,
				}
			}

			return nil
		})
	}
	if err := g.Wait(); err != nil {
		m.logImageInputSkip(ctx, event, "", "", "download_collection_interrupted", err)
	}

	return m.selectImageInputsWithinBudget(ctx, event, results, policy)
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

func buildImageInputDownloadJobs(articles []otogi.Article) []imageInputDownloadJob {
	jobs := make([]imageInputDownloadJob, 0)
	seenAttachmentIDs := make(map[string]struct{})
	for _, article := range articles {
		candidates := filterImageInputCandidates(article.Media)
		for _, attachment := range candidates {
			attachmentID := strings.TrimSpace(attachment.ID)
			if attachmentID != "" {
				if _, exists := seenAttachmentIDs[attachmentID]; exists {
					continue
				}
				seenAttachmentIDs[attachmentID] = struct{}{}
			}
			jobs = append(jobs, imageInputDownloadJob{
				order:      len(jobs),
				articleID:  article.ID,
				attachment: attachment,
			})
		}
	}

	return jobs
}

func (m *Module) downloadImageInput(
	ctx context.Context,
	event *otogi.Event,
	articleID string,
	attachment otogi.MediaAttachment,
	policy ImageInputPolicy,
) (currentImageInput, bool) {
	if attachment.SizeBytes > 0 && attachment.SizeBytes > policy.MaxImageBytes {
		m.logImageInputSkip(
			ctx,
			event,
			articleID,
			attachment.ID,
			fmt.Sprintf("attachment_too_large limit=%d size=%d", policy.MaxImageBytes, attachment.SizeBytes),
			nil,
		)
		return currentImageInput{}, false
	}
	if strings.TrimSpace(attachment.ID) == "" {
		m.logImageInputSkip(ctx, event, articleID, "", "missing_attachment_id", nil)
		return currentImageInput{}, false
	}

	request := otogi.MediaDownloadRequest{
		Source:       event.Source,
		Conversation: event.Conversation,
		ArticleID:    articleID,
		AttachmentID: attachment.ID,
	}
	if err := request.Validate(); err != nil {
		m.logImageInputSkip(ctx, event, articleID, attachment.ID, "build_media_request_failed", err)
		return currentImageInput{}, false
	}

	buffer := limitedWriteBuffer{limit: policy.MaxImageBytes}
	downloaded, err := m.mediaDownloader.Download(ctx, request, &buffer)
	if err != nil {
		reason := "download_failed"
		if errors.Is(err, errImageInputTooLarge) {
			reason = "download_limit_exceeded"
		}
		m.logImageInputSkip(ctx, event, articleID, attachment.ID, reason, err)
		return currentImageInput{}, false
	}

	data := buffer.Bytes()
	if len(data) == 0 {
		m.logImageInputSkip(ctx, event, articleID, attachment.ID, "empty_download", nil)
		return currentImageInput{}, false
	}
	mimeType := resolveImageInputMIMEType(attachment, downloaded, data)
	if !isSupportedImageInputMIMEType(mimeType) {
		m.logImageInputSkip(ctx, event, articleID, attachment.ID, "unsupported_image_mime_type", nil)
		return currentImageInput{}, false
	}

	metadata := mergeImageAttachmentMetadata(attachment, downloaded)
	return currentImageInput{
		ArticleID:    articleID,
		AttachmentID: metadata.ID,
		MIMEType:     mimeType,
		FileName:     metadata.FileName,
		Caption:      metadata.Caption,
		Data:         data,
	}, true
}

func (m *Module) selectImageInputsWithinBudget(
	ctx context.Context,
	event *otogi.Event,
	results []imageInputDownloadResult,
	policy ImageInputPolicy,
) []currentImageInput {
	selected := make([]currentImageInput, 0, minInt(policy.MaxImages, len(results)))
	remainingImages := policy.MaxImages
	remainingBytes := policy.MaxTotalBytes
	for _, result := range results {
		if !result.ok {
			continue
		}
		if remainingImages <= 0 {
			m.logImageInputSkip(ctx, event, result.input.ArticleID, result.input.AttachmentID, "max_images_reached", nil)
			continue
		}
		size := int64(len(result.input.Data))
		if size > remainingBytes {
			m.logImageInputSkip(
				ctx,
				event,
				result.input.ArticleID,
				result.input.AttachmentID,
				fmt.Sprintf("max_total_bytes_reached remaining=%d size=%d", remainingBytes, size),
				nil,
			)
			continue
		}

		selected = append(selected, result.input)
		remainingImages--
		remainingBytes -= size
	}

	return selected
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
