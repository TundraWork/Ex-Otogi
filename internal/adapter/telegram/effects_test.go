package telegram

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// rpcLog answers RPCs from a table of handlers and records every request.
type rpcLog struct {
	mu       sync.Mutex
	requests []bin.Encoder
	answer   func(request bin.Encoder, call int) (bin.Encoder, error)
}

func (r *rpcLog) client() *tg.Client {
	return tg.NewClient(rpc(func(_ context.Context, request bin.Encoder) (bin.Encoder, error) {
		r.mu.Lock()
		r.requests = append(r.requests, request)
		call := len(r.requests)
		r.mu.Unlock()
		return r.answer(request, call)
	}))
}

func (r *rpcLog) find(t *testing.T, match func(bin.Encoder) bool) bin.Encoder {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, request := range r.requests {
		if match(request) {
			return request
		}
	}
	t.Fatalf("no matching request among %d", len(r.requests))
	return nil
}

func sentOK(request bin.Encoder, _ int) (bin.Encoder, error) {
	switch request.(type) {
	case *tg.MessagesSendMessageRequest:
		return &tg.UpdateShortSentMessage{ID: 77, Pts: 2, PtsCount: 1}, nil
	case *tg.MessagesSendMediaRequest, *tg.MessagesEditMessageRequest, *tg.MessagesSendReactionRequest:
		return &tg.Updates{}, nil
	case *tg.MessagesDeleteMessagesRequest, *tg.ChannelsDeleteMessagesRequest:
		return &tg.MessagesAffectedMessages{Pts: 3, PtsCount: 1}, nil
	case *tg.UploadSaveFilePartRequest:
		return &tg.BoolTrue{}, nil
	}
	return nil, unexpected(request)
}

func newEffects(t *testing.T, answer func(bin.Encoder, int) (bin.Encoder, error)) (*effects, *rpcLog, *telegramstore.Store) {
	t.Helper()
	log := &rpcLog{answer: answer}
	store := openStore(t)
	m := mapper.New(mapper.Config{Platform: Platform, Account: "main"})
	return &effects{
		api: log.client(), store: store, ref: m.MessageRef, log: quiet, now: time.Now,
		peers: func(context.Context, string) (tg.InputPeerClass, error) {
			return &tg.InputPeerUser{UserID: 7, AccessHash: 70}, nil
		},
		channel: func(_ context.Context, id int64) (tg.InputChannelClass, error) {
			return &tg.InputChannel{ChannelID: id, AccessHash: 90}, nil
		},
	}, log, store
}

var messageRef = mapper.New(mapper.Config{Platform: Platform, Account: "main"}).MessageRef

func send(key string, msg otogi.Message) otogi.Effect {
	return otogi.Effect{Kind: otogi.EffectSend, Key: otogi.IdempotencyKey(key), To: otogi.Destination{Platform: Platform, Account: "main", Conversation: "user:7"}, Message: msg}
}

func TestSendRecordsAndDeduplicates(t *testing.T) {
	x, log, _ := newEffects(t, sentOK)
	ref, err := x.Apply(context.Background(), send("k1", otogi.TextMessage("hi")))
	if err != nil || ref.SourceEvent != "telegram:common:user:7:77" {
		t.Fatalf("Apply = %+v, %v", ref, err)
	}
	req := log.find(t, func(r bin.Encoder) bool { _, ok := r.(*tg.MessagesSendMessageRequest); return ok }).(*tg.MessagesSendMessageRequest)
	if req.RandomID != RandomID("k1") || req.Message != "hi" {
		t.Fatalf("request = %+v", req)
	}
	again, err := x.Apply(context.Background(), send("k1", otogi.TextMessage("hi")))
	if err != nil || !reflect.DeepEqual(again, ref) || len(log.requests) != 1 {
		t.Fatalf("replay = %+v, %v after %d requests", again, err, len(log.requests))
	}
}

func TestSendErrors(t *testing.T) {
	tests := []struct {
		name     string
		first    error
		wantErr  error
		requests int
	}{
		{"duplicate random_id is delivered", tgerr.New(400, "RANDOM_ID_DUPLICATE"), nil, 1},
		{"flood wait is waited out once", tgerr.New(420, "FLOOD_WAIT_0"), nil, 2},
		{"other errors are returned", tgerr.New(400, "CHAT_WRITE_FORBIDDEN"), tgerr.New(400, "CHAT_WRITE_FORBIDDEN"), 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			x, log, store := newEffects(t, func(r bin.Encoder, call int) (bin.Encoder, error) {
				if call == 1 {
					return nil, test.first
				}
				return sentOK(r, call)
			})
			_, err := x.Apply(context.Background(), send("k", otogi.TextMessage("hi")))
			if (test.wantErr == nil) != (err == nil) || len(log.requests) != test.requests {
				t.Fatalf("Apply = %v after %d requests", err, len(log.requests))
			}
			if _, found, _ := store.FindSent(context.Background(), "k"); test.wantErr != nil && found {
				t.Fatal("failed send was recorded")
			}
		})
	}
}

func TestEditDeleteReact(t *testing.T) {
	x, log, _ := newEffects(t, func(r bin.Encoder, call int) (bin.Encoder, error) {
		if edit, ok := r.(*tg.MessagesEditMessageRequest); ok && edit.Message == "same" {
			return nil, tgerr.New(400, "MESSAGE_NOT_MODIFIED")
		}
		return sentOK(r, call)
	})
	ctx := context.Background()
	user, channel := messageRef("user:7", 44), messageRef("channel:9", 45)
	for _, e := range []otogi.Effect{
		{Kind: otogi.EffectEdit, Ref: user, Message: otogi.TextMessage("edited")},
		{Kind: otogi.EffectEdit, Ref: user, Message: otogi.TextMessage("same")}, // replayed edit
		{Kind: otogi.EffectDelete, Ref: user},
		{Kind: otogi.EffectDelete, Ref: channel},
		{Kind: otogi.EffectReact, Ref: user, Reaction: "👍"},
		{Kind: otogi.EffectReact, Ref: user},
	} {
		if _, err := x.Apply(ctx, e); err != nil {
			t.Fatalf("%s: %v", e.Kind, err)
		}
	}
	edit := log.find(t, func(r bin.Encoder) bool { _, ok := r.(*tg.MessagesEditMessageRequest); return ok }).(*tg.MessagesEditMessageRequest)
	if edit.ID != 44 || edit.Message != "edited" {
		t.Errorf("edit = %+v", edit)
	}
	if del := log.find(t, func(r bin.Encoder) bool { _, ok := r.(*tg.MessagesDeleteMessagesRequest); return ok }).(*tg.MessagesDeleteMessagesRequest); !del.Revoke || del.ID[0] != 44 {
		t.Errorf("delete = %+v", del)
	}
	if del := log.find(t, func(r bin.Encoder) bool { _, ok := r.(*tg.ChannelsDeleteMessagesRequest); return ok }).(*tg.ChannelsDeleteMessagesRequest); del.ID[0] != 45 || del.Channel.(*tg.InputChannel).ChannelID != 9 {
		t.Errorf("channel delete = %+v", del)
	}
	var reactions [][]tg.ReactionClass
	for _, r := range log.requests {
		if react, ok := r.(*tg.MessagesSendReactionRequest); ok {
			reactions = append(reactions, react.Reaction)
		}
	}
	if len(reactions) != 2 || reactions[0][0].(*tg.ReactionEmoji).Emoticon != "👍" || len(reactions[1]) != 0 {
		t.Errorf("reactions = %+v", reactions)
	}
	if _, err := x.Apply(ctx, otogi.Effect{Kind: otogi.EffectDelete, Ref: otogi.EventRef{Platform: Platform, SourceEvent: "x"}}); !errors.Is(err, otogi.ErrInvalid) {
		t.Errorf("delete without locator = %v", err)
	}
	if _, err := x.Apply(ctx, otogi.Effect{Kind: "pin", Ref: user}); !errors.Is(err, otogi.ErrUnsupported) {
		t.Errorf("unknown kind = %v", err)
	}
}

func TestSendMediaUploadsAndCaptions(t *testing.T) {
	x, log, _ := newEffects(t, sentOK)
	msg := otogi.Message{Roots: []otogi.PartID{"m", "t"}, Parts: []otogi.Part{
		{ID: "m", Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaImage, Filename: "a.jpg", Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: []byte("jpeg bytes")}}},
		{ID: "t", Kind: otogi.PartText, Text: "caption"},
	}}
	if _, err := x.Apply(context.Background(), send("media", msg)); err != nil {
		t.Fatal(err)
	}
	log.find(t, func(r bin.Encoder) bool { _, ok := r.(*tg.UploadSaveFilePartRequest); return ok })
	req := log.find(t, func(r bin.Encoder) bool { _, ok := r.(*tg.MessagesSendMediaRequest); return ok }).(*tg.MessagesSendMediaRequest)
	if _, ok := req.Media.(*tg.InputMediaUploadedPhoto); !ok || req.Message != "caption" || req.RandomID != RandomID("media") {
		t.Fatalf("send media = %+v", req)
	}
	two := msg
	two.Parts = append(two.Parts, otogi.Part{ID: "t2", Kind: otogi.PartText, Text: "second"})
	if _, err := x.Apply(context.Background(), send("two", two)); !errors.Is(err, otogi.ErrUnsupported) {
		t.Fatalf("two text parts = %v", err)
	}
}

func TestFetchMediaDownloadsByLocator(t *testing.T) {
	x, log, _ := newEffects(t, func(r bin.Encoder, _ int) (bin.Encoder, error) {
		if _, ok := r.(*tg.UploadGetFileRequest); ok {
			return &tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: []byte("photo bytes")}, nil
		}
		return nil, unexpected(r)
	})
	data, _ := mapper.EncodeMediaLocator(mapper.MediaLocator{Kind: "photo", ID: 1, AccessHash: 2, FileReference: []byte{3}, ThumbSize: "y"})
	src := otogi.MediaSource{Kind: otogi.MediaSourceOpaque, Locator: otogi.ProviderLocator{Namespace: "https://ex-otogi.dev/protocol/telegram/layer228/media", Version: 1, Data: data}}
	got, err := x.FetchMedia(context.Background(), src)
	if err != nil || !bytes.Equal(got, []byte("photo bytes")) {
		t.Fatalf("FetchMedia = %q, %v", got, err)
	}
	req := log.find(t, func(r bin.Encoder) bool { _, ok := r.(*tg.UploadGetFileRequest); return ok }).(*tg.UploadGetFileRequest)
	if loc := req.Location.(*tg.InputPhotoFileLocation); loc.ThumbSize != "y" || loc.ID != 1 {
		t.Fatalf("location = %+v", req.Location)
	}
	if _, err := x.FetchMedia(context.Background(), otogi.MediaSource{Kind: otogi.MediaSourceExternal}); !errors.Is(err, otogi.ErrUnsupported) {
		t.Fatalf("non-Telegram source = %v", err)
	}
}

func TestEncodeContentEntitiesAndReply(t *testing.T) {
	msg := otogi.Message{
		Roots:     []otogi.PartID{"t"},
		Parts:     []otogi.Part{{ID: "t", Kind: otogi.PartText, Text: "A😀B", Annotations: []otogi.Annotation{{Offset: 1, Length: 4, Kind: otogi.AnnotationBold}}}},
		Relations: []otogi.Relation{{Kind: otogi.RelationReply, Target: messageRef("user:7", 44)}},
	}
	c, err := encodeContent(msg, "user:7")
	if err != nil {
		t.Fatal(err)
	}
	if bold := c.entities[0].(*tg.MessageEntityBold); bold.Offset != 1 || bold.Length != 2 { // the emoji is two UTF-16 units
		t.Fatalf("entity = %#v", bold)
	}
	if c.replyTo.ReplyToMsgID != 44 {
		t.Fatalf("reply = %#v", c.replyTo)
	}
	if _, err := encodeContent(msg, "user:8"); !errors.Is(err, otogi.ErrUnsupported) {
		t.Fatalf("reply to another conversation = %v", err)
	}
}
