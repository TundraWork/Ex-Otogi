package mapper

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestPeerIDs(t *testing.T) {
	cases := []struct {
		peer tg.PeerClass
		want string
	}{
		{&tg.PeerUser{UserID: 12}, "user:12"},
		{&tg.PeerChat{ChatID: 34}, "chat:34"},
		{&tg.PeerChannel{ChannelID: 56}, "channel:56"},
	}
	for _, tc := range cases {
		if got, err := PeerID(tc.peer); err != nil || got != tc.want {
			t.Errorf("PeerID(%T)=%q,%v want %q", tc.peer, got, err, tc.want)
		}
	}
}

func TestDirectionAndActor(t *testing.T) {
	incoming := &tg.Message{PeerID: &tg.PeerUser{UserID: 7}}
	incoming.SetFromID(&tg.PeerUser{UserID: 7})
	direction, actor, err := messageFlow(incoming)
	if err != nil || direction != "incoming" || actor != "user:7" {
		t.Fatalf("incoming=%q,%q,%v", direction, actor, err)
	}
	outgoing := &tg.Message{Out: true, PeerID: &tg.PeerChannel{ChannelID: 9}}
	outgoing.SetFromID(&tg.PeerChannel{ChannelID: 9})
	direction, actor, err = messageFlow(outgoing)
	if err != nil || direction != "outgoing" || actor != "channel:9" {
		t.Fatalf("outgoing=%q,%q,%v", direction, actor, err)
	}
	missing := &tg.Message{PeerID: &tg.PeerChat{ChatID: 2}}
	direction, actor, err = messageFlow(missing)
	if err != nil || direction != "incoming" || actor != "" {
		t.Fatalf("missing=%q,%q,%v", direction, actor, err)
	}
}
