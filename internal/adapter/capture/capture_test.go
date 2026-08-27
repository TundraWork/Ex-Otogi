package capture

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestSendRejectsCanceledContextWithoutCapturing(t *testing.T) {
	t.Parallel()
	messenger := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := messenger.Send(ctx, testMessage(t, 0))

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send error = %v, want context.Canceled", err)
	}
	if got := messenger.Messages(); len(got) != 0 {
		t.Fatalf("Messages length = %d, want 0", len(got))
	}
}

func TestSendHasCapacitySixteenAndDoesNotBlock(t *testing.T) {
	t.Parallel()
	messenger := New()
	for i := 0; i < 16; i++ {
		if err := messenger.Send(context.Background(), testMessage(t, i)); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	if err := messenger.Send(context.Background(), testMessage(t, 16)); !errors.Is(err, otogi.ErrCapacity) {
		t.Fatalf("seventeenth Send error = %v, want ErrCapacity", err)
	}
	if got := messenger.Messages(); len(got) != 16 {
		t.Fatalf("Messages length = %d, want 16", len(got))
	}
}

func TestMessagesReturnsDefensiveSnapshot(t *testing.T) {
	t.Parallel()
	messenger := New()
	want := testMessage(t, 0)
	if err := messenger.Send(context.Background(), want); err != nil {
		t.Fatalf("Send: %v", err)
	}

	first := messenger.Messages()
	first[0] = testMessage(t, 1)
	first = append(first, testMessage(t, 2))

	got := messenger.Messages()
	if len(got) != 1 || got[0].Text().String() != want.Text().String() {
		t.Fatalf("Messages = %#v, want independent one-message snapshot", got)
	}
}

func testMessage(t *testing.T, n int) otogi.OutboundMessage {
	t.Helper()
	tenant, _ := otogi.NewTenantID("tenant")
	platform, _ := otogi.NewPlatformID("platform")
	account, _ := otogi.NewAccountID("account")
	conversation, _ := otogi.NewConversationID("conversation")
	event, _ := otogi.NewEventID(fmt.Sprintf("event-%d", n))
	module, _ := otogi.NewModuleID("module")
	text, _ := otogi.NewText(fmt.Sprintf("message-%d", n))
	key, _ := otogi.NewIdempotencyKey(fmt.Sprintf("key-%d", n))
	message, err := otogi.NewOutboundMessage(otogi.OutboundMessageParams{
		Tenant: tenant, Platform: platform, Account: account, Conversation: conversation,
		OriginatingEvent: event, OriginatingModule: module, Text: text, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("NewOutboundMessage: %v", err)
	}
	return message
}
