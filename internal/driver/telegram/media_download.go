package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"ex-otogi/pkg/otogi/platform"

	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

const (
	defaultMediaDownloadTimeout = 30 * time.Second
	defaultMediaDownloadThreads = 4
)

// MediaDownloadOption mutates Telegram media download configuration.
type MediaDownloadOption func(*mediaDownloadConfig)

// WithMediaDownloadTimeout configures the timeout bound applied to one download attempt.
func WithMediaDownloadTimeout(timeout time.Duration) MediaDownloadOption {
	return func(cfg *mediaDownloadConfig) {
		if timeout > 0 {
			cfg.timeout = timeout
		}
	}
}

// WithMediaDownloadThreads configures the number of worker threads used for parallel downloads.
func WithMediaDownloadThreads(threads int) MediaDownloadOption {
	return func(cfg *mediaDownloadConfig) {
		if threads > 0 {
			cfg.threads = threads
		}
	}
}

// WithMediaDownloadVerify configures whether gotd hash verification is enabled.
func WithMediaDownloadVerify(verify bool) MediaDownloadOption {
	return func(cfg *mediaDownloadConfig) {
		cfg.verify = verify
	}
}

// MediaDownloader downloads Telegram media referenced by inbound updates.
type MediaDownloader struct {
	cfg      mediaDownloadConfig
	rpc      mediaDownloadRPC
	peers    *PeerCache
	locators *mediaLocatorCache
	executor mediaDownloadExecutor
}

type mediaDownloadConfig struct {
	timeout time.Duration
	threads int
	verify  bool
}

type mediaDownloadRPC interface {
	downloader.Client
	MessagesGetMessages(ctx context.Context, id []tg.InputMessageClass) (tg.MessagesMessagesClass, error)
	ChannelsGetMessages(ctx context.Context, request *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error)
}

type mediaDownloadExecutor interface {
	Download(
		ctx context.Context,
		rpc mediaDownloadRPC,
		location tg.InputFileLocationClass,
		output io.Writer,
		cfg mediaDownloadConfig,
	) error
}

type mediaDownloadFactory interface {
	Download(rpc mediaDownloadRPC, location tg.InputFileLocationClass) mediaDownloadBuilder
}

type mediaDownloadBuilder interface {
	WithThreads(threads int) mediaDownloadBuilder
	WithVerify(verify bool) mediaDownloadBuilder
	Stream(ctx context.Context, output io.Writer) error
	Parallel(ctx context.Context, output io.WriterAt) error
}

type gotdMediaDownloadExecutor struct {
	factory mediaDownloadFactory
}

type gotdMediaDownloadFactory struct {
	downloader *downloader.Downloader
}

type gotdMediaDownloadBuilder struct {
	builder *downloader.Builder
}

// NewMediaDownloader creates a Telegram media downloader backed by gotd MTProto file APIs.
func NewMediaDownloader(
	client *gotdtelegram.Client,
	peers *PeerCache,
	locators *mediaLocatorCache,
	options ...MediaDownloadOption,
) (*MediaDownloader, error) {
	if client == nil {
		return nil, fmt.Errorf("new telegram media downloader: nil client")
	}

	executor := gotdMediaDownloadExecutor{
		factory: gotdMediaDownloadFactory{
			downloader: downloader.NewDownloader(),
		},
	}

	return newMediaDownloader(client.API(), peers, locators, executor, options...)
}

func newMediaDownloader(
	rpc mediaDownloadRPC,
	peers *PeerCache,
	locators *mediaLocatorCache,
	executor mediaDownloadExecutor,
	options ...MediaDownloadOption,
) (*MediaDownloader, error) {
	if rpc == nil {
		return nil, fmt.Errorf("new telegram media downloader: nil rpc client")
	}
	if peers == nil {
		return nil, fmt.Errorf("new telegram media downloader: nil peer cache")
	}
	if locators == nil {
		return nil, fmt.Errorf("new telegram media downloader: nil locator cache")
	}
	if executor == nil {
		return nil, fmt.Errorf("new telegram media downloader: nil executor")
	}

	cfg := mediaDownloadConfig{
		timeout: defaultMediaDownloadTimeout,
		threads: defaultMediaDownloadThreads,
	}
	for _, option := range options {
		option(&cfg)
	}

	return &MediaDownloader{
		cfg:      cfg,
		rpc:      rpc,
		peers:    peers,
		locators: locators,
		executor: executor,
	}, nil
}

// Download resolves one cached Telegram attachment and streams it into output.
func (d *MediaDownloader) Download(
	ctx context.Context,
	request platform.MediaDownloadRequest,
	output io.Writer,
) (platform.MediaAttachment, error) {
	if err := request.Validate(); err != nil {
		return platform.MediaAttachment{}, fmt.Errorf("download media validate: %w", err)
	}
	if output == nil {
		return platform.MediaAttachment{}, fmt.Errorf("%w: missing output writer", platform.ErrInvalidMediaDownloadRequest)
	}

	downloadCtx, cancel := d.withTimeout(ctx)
	defer cancel()

	record, err := d.resolveRecord(downloadCtx, request)
	if err != nil {
		return platform.MediaAttachment{}, fmt.Errorf(
			"download media article %s attachment %s resolve locator: %w",
			request.ArticleID,
			request.AttachmentID,
			err,
		)
	}

	if err = d.executor.Download(downloadCtx, d.rpc, record.location, output, d.cfg); err != nil {
		if isRefreshableMediaDownloadError(err) {
			refreshed, refreshErr := d.refreshLocator(downloadCtx, request, record)
			if refreshErr != nil {
				return platform.MediaAttachment{}, fmt.Errorf(
					"download media article %s attachment %s initial failure: %w; refresh locator: %w",
					request.ArticleID,
					request.AttachmentID,
					err,
					refreshErr,
				)
			}
			record = refreshed
			retryErr := d.executor.Download(downloadCtx, d.rpc, record.location, output, d.cfg)
			if retryErr != nil {
				err = retryErr
			} else {
				err = nil
			}
		}
	}

	if err != nil {
		return platform.MediaAttachment{}, fmt.Errorf(
			"download media article %s attachment %s: %w",
			request.ArticleID,
			request.AttachmentID,
			err,
		)
	}

	attachment, err := mediaLocatorRecordAttachment(record)
	if err != nil {
		return platform.MediaAttachment{}, fmt.Errorf(
			"download media article %s attachment %s: %w",
			request.ArticleID,
			request.AttachmentID,
			err,
		)
	}

	return attachment, nil
}

func (d *MediaDownloader) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if d == nil || d.cfg.timeout <= 0 {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, d.cfg.timeout)
}

func (d *MediaDownloader) resolveRecord(
	ctx context.Context,
	request platform.MediaDownloadRequest,
) (mediaLocatorRecord, error) {
	if record, ok := d.locators.Lookup(request); ok {
		return record, nil
	}

	return d.refreshLocator(ctx, request, mediaLocatorRecord{})
}

func (d *MediaDownloader) refreshLocator(
	ctx context.Context,
	request platform.MediaDownloadRequest,
	current mediaLocatorRecord,
) (mediaLocatorRecord, error) {
	peer, err := d.resolvePeer(request, current)
	if err != nil {
		return mediaLocatorRecord{}, fmt.Errorf("resolve peer for refresh: %w", err)
	}

	message, err := d.fetchMessage(ctx, request, peer)
	if err != nil {
		return mediaLocatorRecord{}, fmt.Errorf("fetch source message: %w", err)
	}

	locators := buildMessageMediaLocators(message.Media)
	if len(locators) == 0 {
		return mediaLocatorRecord{}, fmt.Errorf(
			"refresh locator article %s attachment %s: %w",
			request.ArticleID,
			request.AttachmentID,
			platform.ErrMediaDownloadNotFound,
		)
	}

	d.locators.RememberMessage(ChatRef{
		ID:   request.Conversation.ID,
		Type: request.Conversation.Type,
	}, request.ArticleID, peer, locators)

	record, ok := d.locators.Lookup(request)
	if !ok {
		return mediaLocatorRecord{}, fmt.Errorf(
			"refresh locator article %s attachment %s: %w",
			request.ArticleID,
			request.AttachmentID,
			platform.ErrMediaDownloadNotFound,
		)
	}

	return record, nil
}

func (d *MediaDownloader) resolvePeer(
	request platform.MediaDownloadRequest,
	current mediaLocatorRecord,
) (tg.InputPeerClass, error) {
	if current.inputPeer != nil {
		return cloneInputPeer(current.inputPeer), nil
	}
	if d.peers == nil {
		return nil, fmt.Errorf("peer cache unavailable")
	}

	peer, err := d.peers.Resolve(request.Conversation)
	if err != nil {
		return nil, fmt.Errorf("resolve conversation %s: %w", request.Conversation.ID, err)
	}

	return peer, nil
}

func (d *MediaDownloader) fetchMessage(
	ctx context.Context,
	request platform.MediaDownloadRequest,
	peer tg.InputPeerClass,
) (*tg.Message, error) {
	messageID, err := parseMessageID(request.ArticleID)
	if err != nil {
		return nil, fmt.Errorf("parse article id %s: %w", request.ArticleID, err)
	}

	inputMessage := &tg.InputMessageID{ID: messageID}
	result, err := d.fetchMessages(ctx, peer, []tg.InputMessageClass{inputMessage})
	if err != nil {
		return nil, fmt.Errorf("fetch messages: %w", err)
	}

	collection, ok := result.(interface{ GetMessages() []tg.MessageClass })
	if !ok {
		return nil, fmt.Errorf("fetch messages: unsupported result type %T", result)
	}
	for _, item := range collection.GetMessages() {
		message, ok := item.(*tg.Message)
		if !ok || message == nil {
			continue
		}
		if message.ID == messageID {
			return message, nil
		}
	}

	return nil, fmt.Errorf("fetch messages article %s: %w", request.ArticleID, platform.ErrMediaDownloadNotFound)
}

func (d *MediaDownloader) fetchMessages(
	ctx context.Context,
	peer tg.InputPeerClass,
	ids []tg.InputMessageClass,
) (tg.MessagesMessagesClass, error) {
	if channelPeer, ok := peer.(*tg.InputPeerChannel); ok {
		result, err := d.rpc.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{
				ChannelID:  channelPeer.ChannelID,
				AccessHash: channelPeer.AccessHash,
			},
			ID: ids,
		})
		if err != nil {
			return nil, fmt.Errorf("channels get messages: %w", err)
		}
		return result, nil
	}

	result, err := d.rpc.MessagesGetMessages(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("messages get messages: %w", err)
	}
	return result, nil
}

func isRefreshableMediaDownloadError(err error) bool {
	return tg.IsFileReferenceEmpty(err) ||
		tg.IsFileReferenceExpired(err) ||
		tg.IsFileReferenceInvalid(err) ||
		tg.IsLocationInvalid(err) ||
		errors.Is(err, platform.ErrMediaDownloadNotFound)
}

func (e gotdMediaDownloadExecutor) Download(
	ctx context.Context,
	rpc mediaDownloadRPC,
	location tg.InputFileLocationClass,
	output io.Writer,
	cfg mediaDownloadConfig,
) error {
	if rpc == nil {
		return fmt.Errorf("download media: nil rpc client")
	}
	if location == nil {
		return fmt.Errorf("%w: missing media location", platform.ErrMediaDownloadNotFound)
	}
	if output == nil {
		return fmt.Errorf("%w: missing output writer", platform.ErrInvalidMediaDownloadRequest)
	}
	if e.factory == nil {
		return fmt.Errorf("download media: nil download factory")
	}

	builder := e.factory.Download(rpc, location).
		WithThreads(cfg.threads).
		WithVerify(cfg.verify)

	if writerAt, ok := output.(io.WriterAt); ok && cfg.threads > 1 {
		if err := builder.Parallel(ctx, writerAt); err != nil {
			return fmt.Errorf("parallel download media: %w", err)
		}
		return nil
	}

	if err := builder.Stream(ctx, output); err != nil {
		return fmt.Errorf("stream download media: %w", err)
	}

	return nil
}

func (f gotdMediaDownloadFactory) Download(
	rpc mediaDownloadRPC,
	location tg.InputFileLocationClass,
) mediaDownloadBuilder {
	return gotdMediaDownloadBuilder{
		builder: f.downloader.Download(rpc, location),
	}
}

func (b gotdMediaDownloadBuilder) WithThreads(threads int) mediaDownloadBuilder {
	b.builder = b.builder.WithThreads(threads)
	return b
}

func (b gotdMediaDownloadBuilder) WithVerify(verify bool) mediaDownloadBuilder {
	b.builder = b.builder.WithVerify(verify)
	return b
}

func (b gotdMediaDownloadBuilder) Stream(ctx context.Context, output io.Writer) error {
	_, err := b.builder.Stream(ctx, output)
	if err != nil {
		return fmt.Errorf("stream builder: %w", err)
	}

	return nil
}

func (b gotdMediaDownloadBuilder) Parallel(ctx context.Context, output io.WriterAt) error {
	_, err := b.builder.Parallel(ctx, output)
	if err != nil {
		return fmt.Errorf("parallel builder: %w", err)
	}

	return nil
}

var _ platform.MediaDownloader = (*MediaDownloader)(nil)
