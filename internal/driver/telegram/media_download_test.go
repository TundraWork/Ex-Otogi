package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"ex-otogi/pkg/otogi/platform"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestMediaDownloaderDownloadsCachedAttachment(t *testing.T) {
	t.Parallel()

	cache := newMediaLocatorCache()
	cache.RememberMessage(
		ChatRef{ID: "100", Type: platform.ConversationTypeGroup},
		"55",
		&tg.InputPeerChat{ChatID: 100},
		[]messageMediaLocator{
			{
				attachment: MediaPayload{
					ID:        "doc-1",
					Type:      platform.MediaTypeDocument,
					MIMEType:  "text/plain",
					FileName:  "notes.txt",
					SizeBytes: 4,
				},
				location: &tg.InputDocumentFileLocation{
					ID:            123,
					AccessHash:    456,
					FileReference: []byte{1, 2, 3},
				},
			},
		},
	)

	peers := NewPeerCache()
	executor := &stubMediaDownloadExecutor{
		downloadFn: func(
			_ context.Context,
			_ mediaDownloadRPC,
			location tg.InputFileLocationClass,
			output io.Writer,
			_ mediaDownloadConfig,
		) error {
			if _, ok := location.(*tg.InputDocumentFileLocation); !ok {
				t.Fatalf("location type = %T, want *tg.InputDocumentFileLocation", location)
			}
			_, err := output.Write([]byte("test"))
			if err != nil {
				return fmt.Errorf("write output: %w", err)
			}
			return nil
		},
	}

	downloader, err := newMediaDownloader(stubMediaDownloadRPC{}, peers, cache, executor)
	if err != nil {
		t.Fatalf("new media downloader failed: %v", err)
	}

	var output bytes.Buffer
	attachment, err := downloader.Download(context.Background(), platform.MediaDownloadRequest{
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "100",
			Type: platform.ConversationTypeGroup,
		},
		ArticleID:    "55",
		AttachmentID: "doc-1",
	}, &output)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if output.String() != "test" {
		t.Fatalf("output = %q, want test", output.String())
	}
	if attachment.ID != "doc-1" || attachment.FileName != "notes.txt" {
		t.Fatalf("attachment = %+v, want doc-1/notes.txt", attachment)
	}
}

func TestMediaDownloaderCacheMiss(t *testing.T) {
	t.Parallel()

	peers := NewPeerCache()
	peers.RememberConversation(
		ChatRef{ID: "100", Type: platform.ConversationTypeGroup},
		&tg.InputPeerChat{ChatID: 100},
	)
	rpc := stubMediaDownloadRPC{
		messagesGetMessagesFn: func(context.Context, []tg.InputMessageClass) (tg.MessagesMessagesClass, error) {
			return &tg.MessagesMessages{}, nil
		},
	}

	downloader, err := newMediaDownloader(rpc, peers, newMediaLocatorCache(), &stubMediaDownloadExecutor{})
	if err != nil {
		t.Fatalf("new media downloader failed: %v", err)
	}

	_, err = downloader.Download(context.Background(), platform.MediaDownloadRequest{
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "100",
			Type: platform.ConversationTypeGroup,
		},
		ArticleID:    "55",
		AttachmentID: "123",
	}, io.Discard)
	if !errors.Is(err, platform.ErrMediaDownloadNotFound) {
		t.Fatalf("download error = %v, want %v", err, platform.ErrMediaDownloadNotFound)
	}
}

func TestMediaDownloaderRefreshesCacheMiss(t *testing.T) {
	t.Parallel()

	peers := NewPeerCache()
	peers.RememberConversation(
		ChatRef{ID: "100", Type: platform.ConversationTypeGroup},
		&tg.InputPeerChat{ChatID: 100},
	)
	rpc := stubMediaDownloadRPC{
		messagesGetMessagesFn: func(_ context.Context, ids []tg.InputMessageClass) (tg.MessagesMessagesClass, error) {
			if len(ids) != 1 {
				t.Fatalf("messages ids = %d, want 1", len(ids))
			}

			return &tg.MessagesMessages{
				Messages: []tg.MessageClass{
					testDocumentMessage(55, 987, 654, []byte{9, 9, 9}, "notes.txt", "text/plain", 4),
				},
			}, nil
		},
	}
	executor := &stubMediaDownloadExecutor{
		downloadFn: func(
			_ context.Context,
			_ mediaDownloadRPC,
			location tg.InputFileLocationClass,
			output io.Writer,
			_ mediaDownloadConfig,
		) error {
			doc, ok := location.(*tg.InputDocumentFileLocation)
			if !ok {
				t.Fatalf("location type = %T, want *tg.InputDocumentFileLocation", location)
			}
			if doc.ID != 987 || doc.AccessHash != 654 {
				t.Fatalf("location = %+v, want id/access 987/654", doc)
			}

			_, err := output.Write([]byte("test"))
			if err != nil {
				return fmt.Errorf("write output: %w", err)
			}
			return nil
		},
	}

	downloader, err := newMediaDownloader(rpc, peers, newMediaLocatorCache(), executor)
	if err != nil {
		t.Fatalf("new media downloader failed: %v", err)
	}

	var output bytes.Buffer
	attachment, err := downloader.Download(context.Background(), platform.MediaDownloadRequest{
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "100",
			Type: platform.ConversationTypeGroup,
		},
		ArticleID:    "55",
		AttachmentID: "987",
	}, &output)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if output.String() != "test" {
		t.Fatalf("output = %q, want test", output.String())
	}
	if attachment.ID != "987" {
		t.Fatalf("attachment id = %s, want 987", attachment.ID)
	}
}

func TestMediaDownloaderRetriesExpiredFileReference(t *testing.T) {
	t.Parallel()

	cache := newMediaLocatorCache()
	cache.RememberMessage(
		ChatRef{ID: "100", Type: platform.ConversationTypeGroup},
		"55",
		&tg.InputPeerChat{ChatID: 100},
		[]messageMediaLocator{
			{
				attachment: MediaPayload{
					ID:        "123",
					Type:      platform.MediaTypeDocument,
					MIMEType:  "text/plain",
					FileName:  "notes.txt",
					SizeBytes: 4,
				},
				location: &tg.InputDocumentFileLocation{
					ID:            123,
					AccessHash:    456,
					FileReference: []byte{1, 2, 3},
				},
			},
		},
	)
	rpc := stubMediaDownloadRPC{
		messagesGetMessagesFn: func(_ context.Context, ids []tg.InputMessageClass) (tg.MessagesMessagesClass, error) {
			if len(ids) != 1 {
				t.Fatalf("messages ids = %d, want 1", len(ids))
			}

			return &tg.MessagesMessages{
				Messages: []tg.MessageClass{
					testDocumentMessage(55, 123, 456, []byte{7, 8, 9}, "notes.txt", "text/plain", 4),
				},
			}, nil
		},
	}

	callCount := 0
	executor := &stubMediaDownloadExecutor{
		downloadFn: func(
			_ context.Context,
			_ mediaDownloadRPC,
			location tg.InputFileLocationClass,
			output io.Writer,
			_ mediaDownloadConfig,
		) error {
			callCount++
			doc, ok := location.(*tg.InputDocumentFileLocation)
			if !ok {
				t.Fatalf("location type = %T, want *tg.InputDocumentFileLocation", location)
			}

			if callCount == 1 {
				if doc.ID != 123 {
					t.Fatalf("first download location id = %d, want 123", doc.ID)
				}
				if !bytes.Equal(doc.FileReference, []byte{1, 2, 3}) {
					t.Fatalf("first file reference = %v, want [1 2 3]", doc.FileReference)
				}
				return tgerr.New(400, "FILE_REFERENCE_EXPIRED")
			}

			if doc.ID != 123 || doc.AccessHash != 456 {
				t.Fatalf("refreshed location = %+v, want id/access 123/456", doc)
			}
			if !bytes.Equal(doc.FileReference, []byte{7, 8, 9}) {
				t.Fatalf("refreshed file reference = %v, want [7 8 9]", doc.FileReference)
			}
			_, err := output.Write([]byte("test"))
			if err != nil {
				return fmt.Errorf("write output: %w", err)
			}
			return nil
		},
	}

	downloader, err := newMediaDownloader(rpc, NewPeerCache(), cache, executor)
	if err != nil {
		t.Fatalf("new media downloader failed: %v", err)
	}

	var output bytes.Buffer
	attachment, err := downloader.Download(context.Background(), platform.MediaDownloadRequest{
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "100",
			Type: platform.ConversationTypeGroup,
		},
		ArticleID:    "55",
		AttachmentID: "123",
	}, &output)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("download calls = %d, want 2", callCount)
	}
	if output.String() != "test" {
		t.Fatalf("output = %q, want test", output.String())
	}
	if attachment.ID != "123" {
		t.Fatalf("attachment id = %s, want 123", attachment.ID)
	}
}

func TestMediaDownloaderRefreshesChannelLocator(t *testing.T) {
	t.Parallel()

	peers := NewPeerCache()
	peers.RememberConversation(
		ChatRef{ID: "200", Type: platform.ConversationTypeChannel},
		&tg.InputPeerChannel{ChannelID: 200, AccessHash: 777},
	)

	channelCalls := 0
	rpc := stubMediaDownloadRPC{
		channelsGetMessagesFn: func(
			_ context.Context,
			request *tg.ChannelsGetMessagesRequest,
		) (tg.MessagesMessagesClass, error) {
			channelCalls++
			channel, ok := request.Channel.(*tg.InputChannel)
			if !ok {
				t.Fatalf("channel type = %T, want *tg.InputChannel", request.Channel)
			}
			if channel.ChannelID != 200 || channel.AccessHash != 777 {
				t.Fatalf("channel = %+v, want id/access 200/777", channel)
			}

			return &tg.MessagesChannelMessages{
				Messages: []tg.MessageClass{
					testDocumentMessage(55, 321, 654, []byte{4, 5, 6}, "notes.txt", "text/plain", 4),
				},
			}, nil
		},
	}
	executor := &stubMediaDownloadExecutor{
		downloadFn: func(
			_ context.Context,
			_ mediaDownloadRPC,
			location tg.InputFileLocationClass,
			output io.Writer,
			_ mediaDownloadConfig,
		) error {
			doc, ok := location.(*tg.InputDocumentFileLocation)
			if !ok {
				t.Fatalf("location type = %T, want *tg.InputDocumentFileLocation", location)
			}
			if doc.ID != 321 || doc.AccessHash != 654 {
				t.Fatalf("location = %+v, want id/access 321/654", doc)
			}
			_, err := output.Write([]byte("test"))
			if err != nil {
				return fmt.Errorf("write output: %w", err)
			}
			return nil
		},
	}

	downloader, err := newMediaDownloader(rpc, peers, newMediaLocatorCache(), executor)
	if err != nil {
		t.Fatalf("new media downloader failed: %v", err)
	}

	var output bytes.Buffer
	_, err = downloader.Download(context.Background(), platform.MediaDownloadRequest{
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "200",
			Type: platform.ConversationTypeChannel,
		},
		ArticleID:    "55",
		AttachmentID: "321",
	}, &output)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if channelCalls != 1 {
		t.Fatalf("channel fetch calls = %d, want 1", channelCalls)
	}
	if output.String() != "test" {
		t.Fatalf("output = %q, want test", output.String())
	}
}

func TestGotdMediaDownloadExecutorUsesParallelWhenWriterAtAvailable(t *testing.T) {
	t.Parallel()

	builder := &stubMediaDownloadBuilder{}
	executor := gotdMediaDownloadExecutor{
		factory: stubMediaDownloadFactory{builder: builder},
	}
	output := newWriterAtBuffer()

	err := executor.Download(
		context.Background(),
		stubMediaDownloadRPC{},
		&tg.InputDocumentFileLocation{ID: 1},
		output,
		mediaDownloadConfig{threads: 4, verify: true},
	)
	if err != nil {
		t.Fatalf("Download() error = %v, want nil", err)
	}
	if builder.parallelCalls != 1 {
		t.Fatalf("parallel calls = %d, want 1", builder.parallelCalls)
	}
	if builder.streamCalls != 0 {
		t.Fatalf("stream calls = %d, want 0", builder.streamCalls)
	}
	if builder.threads != 4 {
		t.Fatalf("threads = %d, want 4", builder.threads)
	}
	if !builder.verify {
		t.Fatal("verify = false, want true")
	}
}

func TestGotdMediaDownloadExecutorUsesStreamForWriterOnly(t *testing.T) {
	t.Parallel()

	builder := &stubMediaDownloadBuilder{}
	executor := gotdMediaDownloadExecutor{
		factory: stubMediaDownloadFactory{builder: builder},
	}

	err := executor.Download(
		context.Background(),
		stubMediaDownloadRPC{},
		&tg.InputPhotoFileLocation{ID: 1},
		&bytes.Buffer{},
		mediaDownloadConfig{threads: 4},
	)
	if err != nil {
		t.Fatalf("Download() error = %v, want nil", err)
	}
	if builder.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", builder.streamCalls)
	}
	if builder.parallelCalls != 0 {
		t.Fatalf("parallel calls = %d, want 0", builder.parallelCalls)
	}
}

type stubMediaDownloadRPC struct {
	messagesGetMessagesFn func(context.Context, []tg.InputMessageClass) (tg.MessagesMessagesClass, error)
	channelsGetMessagesFn func(context.Context, *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error)
}

func (stubMediaDownloadRPC) UploadGetFile(context.Context, *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	return nil, errors.New("unexpected UploadGetFile call")
}

func (stubMediaDownloadRPC) UploadGetFileHashes(context.Context, *tg.UploadGetFileHashesRequest) ([]tg.FileHash, error) {
	return nil, errors.New("unexpected UploadGetFileHashes call")
}

func (stubMediaDownloadRPC) UploadReuploadCDNFile(context.Context, *tg.UploadReuploadCDNFileRequest) ([]tg.FileHash, error) {
	return nil, errors.New("unexpected UploadReuploadCDNFile call")
}

func (stubMediaDownloadRPC) UploadGetCDNFileHashes(context.Context, *tg.UploadGetCDNFileHashesRequest) ([]tg.FileHash, error) {
	return nil, errors.New("unexpected UploadGetCDNFileHashes call")
}

func (stubMediaDownloadRPC) UploadGetWebFile(context.Context, *tg.UploadGetWebFileRequest) (*tg.UploadWebFile, error) {
	return nil, errors.New("unexpected UploadGetWebFile call")
}

func (r stubMediaDownloadRPC) MessagesGetMessages(
	ctx context.Context,
	ids []tg.InputMessageClass,
) (tg.MessagesMessagesClass, error) {
	if r.messagesGetMessagesFn == nil {
		return nil, errors.New("unexpected MessagesGetMessages call")
	}

	return r.messagesGetMessagesFn(ctx, ids)
}

func (r stubMediaDownloadRPC) ChannelsGetMessages(
	ctx context.Context,
	request *tg.ChannelsGetMessagesRequest,
) (tg.MessagesMessagesClass, error) {
	if r.channelsGetMessagesFn == nil {
		return nil, errors.New("unexpected ChannelsGetMessages call")
	}

	return r.channelsGetMessagesFn(ctx, request)
}

type stubMediaDownloadExecutor struct {
	downloadFn func(context.Context, mediaDownloadRPC, tg.InputFileLocationClass, io.Writer, mediaDownloadConfig) error
}

func (e *stubMediaDownloadExecutor) Download(
	ctx context.Context,
	rpc mediaDownloadRPC,
	location tg.InputFileLocationClass,
	output io.Writer,
	cfg mediaDownloadConfig,
) error {
	if e.downloadFn == nil {
		return nil
	}

	return e.downloadFn(ctx, rpc, location, output, cfg)
}

type stubMediaDownloadFactory struct {
	builder *stubMediaDownloadBuilder
}

func (f stubMediaDownloadFactory) Download(mediaDownloadRPC, tg.InputFileLocationClass) mediaDownloadBuilder {
	return f.builder
}

type stubMediaDownloadBuilder struct {
	threads       int
	verify        bool
	streamCalls   int
	parallelCalls int
}

func (b *stubMediaDownloadBuilder) WithThreads(threads int) mediaDownloadBuilder {
	b.threads = threads
	return b
}

func (b *stubMediaDownloadBuilder) WithVerify(verify bool) mediaDownloadBuilder {
	b.verify = verify
	return b
}

func (b *stubMediaDownloadBuilder) Stream(context.Context, io.Writer) error {
	b.streamCalls++
	return nil
}

func (b *stubMediaDownloadBuilder) Parallel(context.Context, io.WriterAt) error {
	b.parallelCalls++
	return nil
}

type writerAtBuffer struct {
	bytes.Buffer
}

func newWriterAtBuffer() *writerAtBuffer {
	return &writerAtBuffer{}
}

func (b *writerAtBuffer) WriteAt(p []byte, off int64) (int, error) {
	current := b.Bytes()
	required := int(off) + len(p)
	if required > len(current) {
		grown := make([]byte, required)
		copy(grown, current)
		b.Buffer.Reset()
		_, _ = b.Buffer.Write(grown)
		current = b.Bytes()
	}
	copy(current[off:], p)
	return len(p), nil
}

func testDocumentMessage(
	id int,
	documentID int64,
	accessHash int64,
	fileReference []byte,
	fileName string,
	mimeType string,
	size int64,
) *tg.Message {
	document := &tg.Document{
		ID:            documentID,
		AccessHash:    accessHash,
		FileReference: append([]byte(nil), fileReference...),
		MimeType:      mimeType,
		Size:          size,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: fileName},
		},
	}
	media := &tg.MessageMediaDocument{}
	media.SetDocument(document)

	return &tg.Message{
		ID:    id,
		Media: media,
	}
}
