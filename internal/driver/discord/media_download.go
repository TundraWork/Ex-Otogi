package discord

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"ex-otogi/pkg/otogi/platform"
)

const defaultDiscordMediaDownloadTimeout = 30 * time.Second

// MediaDownloadOption mutates Discord media downloader configuration.
type MediaDownloadOption func(*mediaDownloadConfig)

// WithMediaDownloadTimeout configures the timeout applied to one download attempt.
func WithMediaDownloadTimeout(timeout time.Duration) MediaDownloadOption {
	return func(cfg *mediaDownloadConfig) {
		if timeout > 0 {
			cfg.timeout = timeout
		}
	}
}

type mediaDownloadConfig struct {
	timeout time.Duration
}

// httpClient abstracts the HTTP client used by the media downloader.
type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// MediaDownloader downloads Discord media attachments from Discord CDN URLs.
//
// Discord attachments contain stable CDN URLs embedded in MediaAttachment.URI,
// so no additional locator lookup is required. The downloader performs a simple
// HTTP GET on the URI.
type MediaDownloader struct {
	cfg    mediaDownloadConfig
	client httpClient
}

// NewMediaDownloader creates a Discord media downloader backed by the standard
// http.Client.
func NewMediaDownloader(options ...MediaDownloadOption) *MediaDownloader {
	return newMediaDownloaderWithClient(http.DefaultClient, options...)
}

func newMediaDownloaderWithClient(client httpClient, options ...MediaDownloadOption) *MediaDownloader {
	cfg := mediaDownloadConfig{
		timeout: defaultDiscordMediaDownloadTimeout,
	}
	for _, opt := range options {
		opt(&cfg)
	}

	return &MediaDownloader{
		cfg:    cfg,
		client: client,
	}
}

// Download resolves and streams one Discord attachment into output.
//
// The attachment URI from MediaDownloadRequest is used directly; no session
// lookup is required since Discord CDN URLs are stable and self-contained.
func (d *MediaDownloader) Download(
	ctx context.Context,
	request platform.MediaDownloadRequest,
	output io.Writer,
) (platform.MediaAttachment, error) {
	if err := request.Validate(); err != nil {
		return platform.MediaAttachment{}, fmt.Errorf("discord download media validate: %w", err)
	}
	if output == nil {
		return platform.MediaAttachment{}, fmt.Errorf("%w: missing output writer", platform.ErrInvalidMediaDownloadRequest)
	}

	// Discord CDN URLs are embedded in the URI field by the inbound mapper.
	// The URI is stored in MediaAttachment.URI and reflected as AttachmentID in
	// the download request since we store the URL as the attachment locator.
	uri := request.AttachmentID
	if uri == "" {
		return platform.MediaAttachment{}, fmt.Errorf("%w: empty attachment URI",
			platform.ErrMediaDownloadNotFound)
	}

	downloadCtx := ctx
	if d.cfg.timeout > 0 {
		var cancel context.CancelFunc
		downloadCtx, cancel = context.WithTimeout(ctx, d.cfg.timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, uri, nil)
	if err != nil {
		return platform.MediaAttachment{}, fmt.Errorf("discord download media create request: %w", err)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return platform.MediaAttachment{}, fmt.Errorf("discord download media %s: %w", uri, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return platform.MediaAttachment{}, fmt.Errorf(
			"discord download media %s: unexpected status %d",
			uri, resp.StatusCode,
		)
	}

	if _, err := io.Copy(output, resp.Body); err != nil {
		return platform.MediaAttachment{}, fmt.Errorf("discord download media %s copy: %w", uri, err)
	}

	attachment := platform.MediaAttachment{
		ID:       request.AttachmentID,
		URI:      uri,
		MIMEType: resp.Header.Get("Content-Type"),
	}

	return attachment, nil
}

// Compile-time interface assertion.
var _ platform.MediaDownloader = (*MediaDownloader)(nil)
