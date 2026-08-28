package telegram

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type sendStoreFake struct {
	intents     map[string]telegramstore.SendIntent
	peer        telegramstore.Peer
	creates     int
	peerLookups int
	transitions int
}

func (s *sendStoreFake) CreateSendIntent(_ context.Context, in telegramstore.SendIntent, at time.Time) (telegramstore.SendIntent, bool, error) {
	s.creates++
	if old, ok := s.intents[in.Key]; ok {
		if string(old.RequestDigest) != string(in.RequestDigest) {
			return old, false, telegramstore.ErrSendConflict
		}
		return old, false, nil
	}
	in.Status, in.CreatedAt, in.UpdatedAt = telegramstore.SendCreated, at, at
	s.intents[in.Key] = in
	return in, true, nil
}
func (s *sendStoreFake) GetSendIntent(_ context.Context, key string) (telegramstore.SendIntent, bool, error) {
	v, ok := s.intents[key]
	return v, ok, nil
}
func (s *sendStoreFake) TransitionSend(_ context.Context, key string, from, to telegramstore.SendStatus, tr telegramstore.SendTransition) error {
	s.transitions++
	v := s.intents[key]
	if v.Status != from {
		return telegramstore.ErrSendConflict
	}
	v.Status, v.UpdatedAt, v.RetryAt, v.Error = to, tr.At, tr.RetryAt, tr.Error
	v.ProviderMessageID = tr.ProviderMessageID
	s.intents[key] = v
	return nil
}
func (s *sendStoreFake) GetPeer(_ context.Context, kind string, id int64) (telegramstore.Peer, bool, error) {
	s.peerLookups++
	return s.peer, s.peer.Kind == kind && s.peer.ID == id, nil
}
func (s *sendStoreFake) RecoverableSends(context.Context, time.Time) ([]telegramstore.SendIntent, error) {
	var v []telegramstore.SendIntent
	for _, x := range s.intents {
		if x.Status != telegramstore.SendSucceeded && x.Status != telegramstore.SendPermanentFailed {
			v = append(v, x)
		}
	}
	return v, nil
}

type sendAPIFake struct {
	calls  int
	req    *tg.MessagesSendMessageRequest
	result tg.UpdatesClass
	err    error
}

func (a *sendAPIFake) MessagesSendMessage(_ context.Context, req *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error) {
	a.calls++
	a.req = req
	return a.result, a.err
}

type gateFake struct {
	calls int
	err   error
}

func (g *gateFake) Handle(context.Context, tg.UpdatesClass) error { g.calls++; return g.err }

func outboundText(t *testing.T, key, conversation, text string, anns []otogi.Annotation, rels []otogi.Relation) otogi.OutboundMessage {
	t.Helper()
	id, _ := otogi.NewPartID("text")
	part, err := otogi.NewTextPart(id, text, anns, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := otogi.NewMessage([]otogi.PartID{id}, []otogi.Part{part}, rels, nil)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _ := otogi.NewTenantID("tenant")
	platform, _ := otogi.NewPlatformID("telegram")
	account, _ := otogi.NewAccountID("account")
	conv, _ := otogi.NewConversationID(conversation)
	event, _ := otogi.NewEventID("event")
	module, _ := otogi.NewModuleID("module")
	idem, _ := otogi.NewIdempotencyKey(key)
	out, err := otogi.NewOutboundMessage(otogi.OutboundMessageParams{Tenant: tenant, Platform: platform, Account: account, Conversation: conv, OriginatingEvent: event, OriginatingModule: module, Message: msg, IdempotencyKey: idem})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSendCodecGoldenDigestRandomIDAndJournalCorrelation(t *testing.T) {
	ann, _ := otogi.NewAnnotation("bold", 1, 4, otogi.AnnotationBold, "", nil)
	locData, _ := mapper.EncodeMessageLocator(mapper.MessageLocator{Namespace: mapper.NamespaceCommon, Peer: "user:7", MessageID: 44})
	loc, _ := otogi.NewProviderLocator("https://ex-otogi.dev/protocol/telegram/layer228/message", 1, locData)
	platform, _ := otogi.NewPlatformID("telegram")
	account, _ := otogi.NewAccountID("account")
	conv, _ := otogi.NewConversationID("user:7")
	src, _ := otogi.NewSourceEventID("44")
	ref, _ := otogi.NewEventRef(otogi.EventRefParams{Platform: platform, Account: account, Conversation: conv, SourceEvent: src, Locator: loc})
	rel, _ := otogi.NewRelation(otogi.RelationReply, ref, "", nil)
	msg := outboundText(t, "golden-key", "user:7", "A😀B", []otogi.Annotation{ann}, []otogi.Relation{rel})
	s := &sendStoreFake{intents: map[string]telegramstore.SendIntent{}, peer: telegramstore.Peer{Kind: "user", ID: 7, AccessHash: 8}}
	api := &sendAPIFake{result: &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{RandomID: RandomID("golden-key"), ID: 55}}}}
	gate := &gateFake{}
	sender := NewSender(api, s, gate, func() time.Time { return time.Unix(100, 0).UTC() })
	// Golden values intentionally pin the versioned algorithms.
	if got := RandomID("golden-key"); got != 3533333913109694895 {
		t.Fatalf("random_id=%d", got)
	}
	if err := sender.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if api.calls != 1 || gate.calls != 1 {
		t.Fatalf("rpc=%d gate=%d", api.calls, gate.calls)
	}
	if api.req.RandomID != RandomID("golden-key") || api.req.Message != "A😀B" {
		t.Fatalf("request=%+v", api.req)
	}
	if len(api.req.Entities) != 1 {
		t.Fatalf("entities=%d", len(api.req.Entities))
	}
	bold := api.req.Entities[0].(*tg.MessageEntityBold)
	if bold.Offset != 1 || bold.Length != 2 {
		t.Fatalf("UTF16 range=%+v", bold)
	}
	if api.req.ReplyTo.(*tg.InputReplyToMessage).ReplyToMsgID != 44 {
		t.Fatalf("reply=%+v", api.req.ReplyTo)
	}
	intent := s.intents["golden-key"]
	if intent.Status != telegramstore.SendSucceeded || intent.ProviderMessageID != 55 {
		t.Fatalf("intent=%+v", intent)
	}
	if got := RequestDigest(api.req); got != "000268354de87f5e7fe225c10d5a7357249edd98f6cc673eb07d874457b04c412ccc" {
		t.Fatalf("digest=%s", got)
	}
}

func TestUnsupportedGraphIsRejectedBeforeStoreOrRPC(t *testing.T) {
	id, _ := otogi.NewPartID("opaque")
	ext, _ := otogi.NewExtension("https://example.test/x", "x", 1, "application/octet-stream", []byte{1})
	part, _ := otogi.NewOpaquePart(id, ext, nil, nil)
	msg0, _ := otogi.NewMessage([]otogi.PartID{id}, []otogi.Part{part}, nil, nil)
	base := outboundText(t, "key", "user:7", "x", nil, nil)
	out, _ := otogi.NewOutboundMessage(otogi.OutboundMessageParams{Tenant: base.Tenant(), Platform: base.Platform(), Account: base.Account(), Conversation: base.Conversation(), OriginatingEvent: base.OriginatingEvent(), OriginatingModule: base.OriginatingModule(), Message: msg0, IdempotencyKey: base.IdempotencyKey()})
	s := &sendStoreFake{intents: map[string]telegramstore.SendIntent{}}
	api := &sendAPIFake{}
	err := NewSender(api, s, &gateFake{}, time.Now).Send(context.Background(), out)
	if !errors.Is(err, otogi.ErrUnsupported) || s.creates != 0 || api.calls != 0 {
		t.Fatalf("err=%v creates=%d rpc=%d", err, s.creates, api.calls)
	}
}

func TestRateLimitRecordsExactRetryWithoutRetry(t *testing.T) {
	start := time.Unix(1000, 0).UTC()
	received := start.Add(3 * time.Second)
	for _, typ := range []string{"FLOOD_WAIT_17", "SLOWMODE_WAIT_17"} {
		t.Run(typ, func(t *testing.T) {
			s := &sendStoreFake{intents: map[string]telegramstore.SendIntent{}, peer: telegramstore.Peer{Kind: "user", ID: 7, AccessHash: 8}}
			api := &sendAPIFake{err: tgerr.New(420, typ)}
			calls := 0
			err := NewSender(api, s, &gateFake{}, func() time.Time {
				calls++
				if calls == 1 {
					return start
				}
				return received
			}).Send(context.Background(), outboundText(t, typ, "user:7", "x", nil, nil))
			var rate *RateLimitError
			if !errors.As(err, &rate) || !rate.RetryAt.Equal(received.Add(17*time.Second)) || api.calls != 1 {
				t.Fatalf("err=%v calls=%d", err, api.calls)
			}
			if got := s.intents[typ]; got.Status != telegramstore.SendRetryable || got.RetryAt == nil || !got.RetryAt.Equal(rate.RetryAt) {
				t.Fatalf("intent=%+v", got)
			}
		})
	}
}

func TestPeerReconstruction(t *testing.T) {
	for _, tc := range []struct {
		conversation string
		peer         telegramstore.Peer
		want         any
	}{{"user:7", telegramstore.Peer{Kind: "user", ID: 7, AccessHash: 8}, &tg.InputPeerUser{}}, {"chat:9", telegramstore.Peer{Kind: "chat", ID: 9}, &tg.InputPeerChat{}}, {"channel:11", telegramstore.Peer{Kind: "channel", ID: 11, AccessHash: 12}, &tg.InputPeerChannel{}}} {
		t.Run(tc.conversation, func(t *testing.T) {
			s := &sendStoreFake{intents: map[string]telegramstore.SendIntent{}, peer: tc.peer}
			api := &sendAPIFake{result: &tg.UpdateShortSentMessage{ID: 3}}
			if err := NewSender(api, s, &gateFake{}, time.Now).Send(context.Background(), outboundText(t, tc.conversation, tc.conversation, "x", nil, nil)); err != nil {
				t.Fatal(err)
			}
			switch tc.want.(type) {
			case *tg.InputPeerUser:
				if _, ok := api.req.Peer.(*tg.InputPeerUser); !ok {
					t.Fatal(api.req.Peer)
				}
			case *tg.InputPeerChat:
				if _, ok := api.req.Peer.(*tg.InputPeerChat); !ok {
					t.Fatal(api.req.Peer)
				}
			case *tg.InputPeerChannel:
				if _, ok := api.req.Peer.(*tg.InputPeerChannel); !ok {
					t.Fatal(api.req.Peer)
				}
			}
		})
	}
}

func TestRestartReconciliationMarksUncertainInFlightAmbiguous(t *testing.T) {
	now := time.Unix(2000, 0).UTC()
	s := &sendStoreFake{intents: map[string]telegramstore.SendIntent{
		"created": {Key: "created", Status: telegramstore.SendCreated},
		"flight":  {Key: "flight", Status: telegramstore.SendInFlight},
		"done":    {Key: "done", Status: telegramstore.SendSucceeded},
	}}
	sender := NewSender(&sendAPIFake{}, s, &gateFake{}, func() time.Time { return now })
	if err := sender.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.intents["created"].Status != telegramstore.SendCreated {
		t.Fatalf("created changed: %+v", s.intents["created"])
	}
	if s.intents["flight"].Status != telegramstore.SendAmbiguous {
		t.Fatalf("flight=%+v", s.intents["flight"])
	}
}

func TestUnsupportedAnnotationAndRelationAreRejectedBeforeMutation(t *testing.T) {
	unsupported, _ := otogi.NewAnnotation("mention", 0, 1, otogi.AnnotationMention, "", nil)
	for _, message := range []otogi.OutboundMessage{
		outboundText(t, "annotation", "user:7", "x", []otogi.Annotation{unsupported}, nil),
		outboundText(t, "relation", "user:7", "x", nil, []otogi.Relation{func() otogi.Relation {
			platform, _ := otogi.NewPlatformID("telegram")
			account, _ := otogi.NewAccountID("account")
			conversation, _ := otogi.NewConversationID("user:7")
			source, _ := otogi.NewSourceEventID("1")
			ref, _ := otogi.NewEventRef(otogi.EventRefParams{Platform: platform, Account: account, Conversation: conversation, SourceEvent: source})
			relation, _ := otogi.NewRelation(otogi.RelationForward, ref, "", nil)
			return relation
		}()}),
	} {
		s := &sendStoreFake{intents: map[string]telegramstore.SendIntent{}, peer: telegramstore.Peer{Kind: "user", ID: 7, AccessHash: 8}}
		api := &sendAPIFake{}
		err := NewSender(api, s, &gateFake{}, time.Now).Send(context.Background(), message)
		if !errors.Is(err, otogi.ErrUnsupported) || s.creates != 0 || api.calls != 0 {
			t.Fatalf("err=%v creates=%d calls=%d", err, s.creates, api.calls)
		}
	}
}

func TestUnsupportedRelationWinsBeforeMissingPeerLookup(t *testing.T) {
	platform, _ := otogi.NewPlatformID("telegram")
	account, _ := otogi.NewAccountID("account")
	conversation, _ := otogi.NewConversationID("user:7")
	source, _ := otogi.NewSourceEventID("1")
	ref, _ := otogi.NewEventRef(otogi.EventRefParams{Platform: platform, Account: account, Conversation: conversation, SourceEvent: source})
	relation, _ := otogi.NewRelation(otogi.RelationForward, ref, "", nil)
	s := &sendStoreFake{intents: map[string]telegramstore.SendIntent{}}
	err := NewSender(&sendAPIFake{}, s, &gateFake{}, time.Now).Send(context.Background(), outboundText(t, "relation-first", "user:7", "x", nil, []otogi.Relation{relation}))
	if !errors.Is(err, otogi.ErrUnsupported) || s.creates != 0 {
		t.Fatalf("err=%v creates=%d", err, s.creates)
	}
}

func TestSameKeyDifferentPayloadConflictsWithoutSecondRPC(t *testing.T) {
	s := &sendStoreFake{intents: map[string]telegramstore.SendIntent{}, peer: telegramstore.Peer{Kind: "user", ID: 7, AccessHash: 8}}
	api := &sendAPIFake{result: &tg.UpdateShortSentMessage{ID: 1}}
	sender := NewSender(api, s, &gateFake{}, time.Now)
	if err := sender.Send(context.Background(), outboundText(t, "same", "user:7", "first", nil, nil)); err != nil {
		t.Fatal(err)
	}
	err := sender.Send(context.Background(), outboundText(t, "same", "user:7", "second", nil, nil))
	if !errors.Is(err, telegramstore.ErrSendConflict) || api.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, api.calls)
	}
}

func TestAmbiguousRedeliveryDoesNotTransitionOrCallRPC(t *testing.T) {
	now := time.Unix(5000, 0).UTC()
	s := &sendStoreFake{intents: map[string]telegramstore.SendIntent{}, peer: telegramstore.Peer{Kind: "user", ID: 7, AccessHash: 8}}
	api := &sendAPIFake{err: context.DeadlineExceeded}
	sender := NewSender(api, s, &gateFake{}, func() time.Time { return now })
	message := outboundText(t, "ambiguous-key", "user:7", "x", nil, nil)
	if err := sender.Send(context.Background(), message); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	transitions := s.transitions
	if err := sender.Send(context.Background(), message); !errors.Is(err, ErrAmbiguousSend) {
		t.Fatalf("err=%v", err)
	}
	if api.calls != 1 || s.transitions != transitions {
		t.Fatalf("rpc=%d transitions=%d want=%d", api.calls, s.transitions, transitions)
	}
}

func TestSucceededReplayDoesNotRequireCurrentPeer(t *testing.T) {
	s := &sendStoreFake{intents: map[string]telegramstore.SendIntent{}, peer: telegramstore.Peer{Kind: "user", ID: 7, AccessHash: 8}}
	api := &sendAPIFake{result: &tg.UpdateShortSentMessage{ID: 9}}
	sender := NewSender(api, s, &gateFake{}, time.Now)
	message := outboundText(t, "succeeded-replay", "user:7", "x", nil, nil)
	if err := sender.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	s.peer = telegramstore.Peer{}
	lookups := s.peerLookups
	if err := sender.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if api.calls != 1 || s.peerLookups != lookups {
		t.Fatalf("rpc=%d peer lookups=%d want=%d", api.calls, s.peerLookups, lookups)
	}
}

func TestDifferentPayloadConflictsBeforePeerLookup(t *testing.T) {
	s := &sendStoreFake{intents: map[string]telegramstore.SendIntent{}, peer: telegramstore.Peer{Kind: "user", ID: 7, AccessHash: 8}}
	api := &sendAPIFake{result: &tg.UpdateShortSentMessage{ID: 9}}
	sender := NewSender(api, s, &gateFake{}, time.Now)
	if err := sender.Send(context.Background(), outboundText(t, "digest-first", "user:7", "first", nil, nil)); err != nil {
		t.Fatal(err)
	}
	s.peer = telegramstore.Peer{}
	lookups := s.peerLookups
	err := sender.Send(context.Background(), outboundText(t, "digest-first", "user:7", "second", nil, nil))
	if !errors.Is(err, telegramstore.ErrSendConflict) || s.peerLookups != lookups || api.calls != 1 {
		t.Fatalf("err=%v lookups=%d rpc=%d", err, s.peerLookups, api.calls)
	}
}

func TestEligibleRetryUsesDurablePeerWhenAccessHashChanges(t *testing.T) {
	start := time.Unix(6000, 0).UTC()
	now := start
	s := &sendStoreFake{intents: map[string]telegramstore.SendIntent{}, peer: telegramstore.Peer{Kind: "user", ID: 7, AccessHash: 8}}
	api := &sendAPIFake{err: tgerr.New(420, "FLOOD_WAIT_1")}
	sender := NewSender(api, s, &gateFake{}, func() time.Time { return now })
	message := outboundText(t, "retry-peer", "user:7", "x", nil, nil)
	if err := sender.Send(context.Background(), message); err == nil {
		t.Fatal("missing rate limit")
	}
	now = start.Add(2 * time.Second)
	s.peer.AccessHash = 99
	api.err = nil
	api.result = &tg.UpdateShortSentMessage{ID: 10}
	if err := sender.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	peer := api.req.Peer.(*tg.InputPeerUser)
	if peer.AccessHash != 8 || api.calls != 2 {
		t.Fatalf("peer=%+v calls=%d", peer, api.calls)
	}
}
