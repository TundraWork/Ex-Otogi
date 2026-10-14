package telegram

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type sendAPIFake struct {
	requests []*tg.MessagesSendMessageRequest
	results  []error // per call; nil means success
}

func (f *sendAPIFake) MessagesSendMessage(_ context.Context, req *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error) {
	f.requests = append(f.requests, req)
	if i := len(f.requests) - 1; i < len(f.results) && f.results[i] != nil {
		return nil, f.results[i]
	}
	return &tg.UpdateShortSentMessage{ID: 77, Pts: 2, PtsCount: 1}, nil
}

func newSender(t *testing.T, api *sendAPIFake) (*sender, *telegramstore.Store, *[]tg.UpdatesClass) {
	t.Helper()
	store := openStore(t)
	var handled []tg.UpdatesClass
	m := mapper.New(mapper.Config{Platform: Platform, Account: "main", AuthorizedUserID: 42})
	return &sender{
		api: api, store: store, log: quiet, now: time.Now, ref: m.MessageRef,
		resolve: func(_ context.Context, conversation string) (tg.InputPeerClass, error) {
			return &tg.InputPeerUser{UserID: 7, AccessHash: 70}, nil
		},
		updates: gotdtelegram.UpdateHandlerFunc(func(_ context.Context, u tg.UpdatesClass) error {
			handled = append(handled, u)
			return nil
		}),
	}, store, &handled
}

func outbound(key string) otogi.OutboundMessage {
	return otogi.OutboundMessage{Platform: Platform, Account: "main", Conversation: "user:7", Key: otogi.IdempotencyKey(key), Message: otogi.TextMessage("hi")}
}

func TestSendRecordsAndDeduplicates(t *testing.T) {
	api := &sendAPIFake{}
	s, _, handled := newSender(t, api)
	ref, err := s.Send(context.Background(), outbound("k1"))
	if err != nil {
		t.Fatal(err)
	}
	if ref.SourceEvent != "telegram:common:user:7:77" || ref.Conversation != "user:7" {
		t.Fatalf("ref = %+v", ref)
	}
	if req := api.requests[0]; req.RandomID != RandomID("k1") || req.Message != "hi" {
		t.Fatalf("request = %+v", req)
	}
	if len(*handled) != 1 {
		t.Fatal("send result was not handed to the update manager")
	}
	// A replay of the same effect returns the original message without sending.
	again, err := s.Send(context.Background(), outbound("k1"))
	if err != nil || !reflect.DeepEqual(again, ref) || len(api.requests) != 1 {
		t.Fatalf("replay = %+v, %v; requests = %d", again, err, len(api.requests))
	}
}

func TestSendTreatsRandomIDDuplicateAsDelivered(t *testing.T) {
	api := &sendAPIFake{results: []error{tgerr.New(400, "RANDOM_ID_DUPLICATE")}}
	s, _, _ := newSender(t, api)
	ref, err := s.Send(context.Background(), outbound("k1"))
	if err != nil || ref.Conversation != "user:7" || ref.SourceEvent != "" {
		t.Fatalf("Send = %+v, %v", ref, err)
	}
}

func TestSendWaitsOutOneFloodWait(t *testing.T) {
	api := &sendAPIFake{results: []error{tgerr.New(420, "FLOOD_WAIT_0")}}
	s, _, _ := newSender(t, api)
	if _, err := s.Send(context.Background(), outbound("k1")); err != nil || len(api.requests) != 2 {
		t.Fatalf("Send = %v after %d requests", err, len(api.requests))
	}
}

func TestSendReturnsOtherErrorsWithoutRecording(t *testing.T) {
	rpcErr := tgerr.New(400, "CHAT_WRITE_FORBIDDEN")
	api := &sendAPIFake{results: []error{rpcErr}}
	s, store, _ := newSender(t, api)
	if _, err := s.Send(context.Background(), outbound("k1")); !errors.Is(err, rpcErr) {
		t.Fatalf("Send = %v", err)
	}
	if _, found, _ := store.FindSent(context.Background(), "k1"); found {
		t.Fatal("failed send was recorded")
	}
	msg := outbound("k2")
	msg.Message = otogi.Message{Roots: []otogi.PartID{"m"}, Parts: []otogi.Part{{ID: "m", Kind: otogi.PartMedia, Media: &otogi.Media{}}}}
	if _, err := s.Send(context.Background(), msg); !errors.Is(err, otogi.ErrUnsupported) || len(api.requests) != 1 {
		t.Fatalf("unsupported content = %v after %d requests", err, len(api.requests))
	}
}

func TestEncodeMessageEntitiesAndReply(t *testing.T) {
	m := mapper.New(mapper.Config{Platform: Platform, Account: "main", AuthorizedUserID: 42})
	msg := otogi.OutboundMessage{Conversation: "user:7", Message: otogi.Message{
		Roots:     []otogi.PartID{"t"},
		Parts:     []otogi.Part{{ID: "t", Kind: otogi.PartText, Text: "A😀B", Annotations: []otogi.Annotation{{Offset: 1, Length: 4, Kind: otogi.AnnotationBold}}}},
		Relations: []otogi.Relation{{Kind: otogi.RelationReply, Target: m.MessageRef("user:7", 44)}},
	}}
	req, err := encodeMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	bold, ok := req.Entities[0].(*tg.MessageEntityBold)
	if !ok || bold.Offset != 1 || bold.Length != 2 { // the emoji is two UTF-16 units
		t.Fatalf("entity = %#v", req.Entities[0])
	}
	if reply, ok := req.ReplyTo.(*tg.InputReplyToMessage); !ok || reply.ReplyToMsgID != 44 {
		t.Fatalf("reply = %#v", req.ReplyTo)
	}
	msg.Message.Relations[0].Target = m.MessageRef("user:8", 44)
	if _, err := encodeMessage(msg); !errors.Is(err, otogi.ErrUnsupported) {
		t.Fatalf("reply to another conversation = %v", err)
	}
}
