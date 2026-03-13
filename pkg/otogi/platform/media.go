package platform

import (
	"context"
	"fmt"
	"io"
)

// ServiceMediaDownloader is the canonical service registry key for media downloads.
const ServiceMediaDownloader = "otogi.media_downloader"

// MediaDownloader streams attachment bytes from one concrete source platform.
//
// Modules should use this interface as the standard attachment retrieval path
// instead of calling platform SDKs directly.
type MediaDownloader interface {
	// Download resolves and streams one attachment into output.
	Download(ctx context.Context, request MediaDownloadRequest, output io.Writer) (MediaAttachment, error)
}

// MediaDownloadRequest identifies one attachment download target.
type MediaDownloadRequest struct {
	// Source identifies which configured driver instance must satisfy the download.
	Source EventSource
	// Conversation identifies where the attachment was observed.
	Conversation Conversation
	// ArticleID identifies the source-platform article containing the attachment.
	ArticleID string
	// AttachmentID identifies the attachment within the article.
	AttachmentID string
}

// Validate checks request identity fields required for routing and lookup.
func (r MediaDownloadRequest) Validate() error {
	if r.Source.Platform == "" {
		return fmt.Errorf("%w: missing source platform", ErrInvalidMediaDownloadRequest)
	}
	if r.Source.ID == "" {
		return fmt.Errorf("%w: missing source id", ErrInvalidMediaDownloadRequest)
	}
	if r.Conversation.ID == "" {
		return fmt.Errorf("%w: missing conversation id", ErrInvalidMediaDownloadRequest)
	}
	if r.Conversation.Type == "" {
		return fmt.Errorf("%w: missing conversation type", ErrInvalidMediaDownloadRequest)
	}
	if r.ArticleID == "" {
		return fmt.Errorf("%w: missing article id", ErrInvalidMediaDownloadRequest)
	}
	if r.AttachmentID == "" {
		return fmt.Errorf("%w: missing attachment id", ErrInvalidMediaDownloadRequest)
	}

	return nil
}

// MediaDownloadRequestFromEvent derives a source-bound media request from one event.
func MediaDownloadRequestFromEvent(event *Event, attachmentID string) (MediaDownloadRequest, error) {
	if event == nil {
		return MediaDownloadRequest{}, fmt.Errorf("%w: nil event", ErrInvalidMediaDownloadRequest)
	}

	articleID := ""
	switch {
	case event.Article != nil && event.Article.ID != "":
		articleID = event.Article.ID
	case event.Mutation != nil && event.Mutation.TargetArticleID != "":
		articleID = event.Mutation.TargetArticleID
	}

	request := MediaDownloadRequest{
		Source:       event.Source,
		Conversation: event.Conversation,
		ArticleID:    articleID,
		AttachmentID: attachmentID,
	}
	if err := request.Validate(); err != nil {
		return MediaDownloadRequest{}, fmt.Errorf("media download request from event %s: %w", event.Kind, err)
	}

	return request, nil
}
