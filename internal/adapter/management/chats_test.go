package management

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Received events build the chat directory: titles as the platform last
// gave them, names the operator sets, the latest message, and daily counts,
// which go with the records while the chats stay.
func TestChatDirectory(t *testing.T) {
	l := openLog(t)
	ctx := context.Background()
	day1 := time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC)
	receive := func(at time.Time, conversation, title string, action otogi.EventAction, text string) {
		e := otogi.Event{ID: otogi.EventID("e-" + text), Platform: "telegram", Account: "main", Action: action, Message: otogi.TextMessage(text),
			Conversation: otogi.Conversation{ID: otogi.ConversationID(conversation), Kind: otogi.ConversationGroup, Title: title}}
		l.Record(ctx, otogi.Record{At: at, Kind: "event.received", Component: "kernel", Event: e.ID, Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID, Payload: e})
	}
	receive(day1, "chat:1", "Friends", otogi.ActionMessageCreated, "one")
	receive(day1.Add(2*time.Hour), "chat:1", "", otogi.ActionMessageCreated, "two") // the next day, without a title
	receive(day1.Add(3*time.Hour), "chat:1", "", otogi.ActionMessageEdited, "two, edited")
	receive(day1.Add(time.Hour), "user:7", "Alice", otogi.ActionMessageCreated, "hi")
	l.Record(ctx, otogi.Record{At: day1, Kind: "module.completed", Component: "module:chat", Conversation: "chat:2"})

	if ok, err := l.NameChat(ctx, "telegram", "user:7", "Alice (test)"); !ok || err != nil {
		t.Fatalf("NameChat = %v, %v", ok, err)
	}
	if ok, _ := l.NameChat(ctx, "telegram", "chat:404", "x"); ok {
		t.Fatal("named a chat never seen")
	}
	chats, err := l.Chats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 2 {
		t.Fatalf("chats %+v", chats)
	}
	friends, alice := chats[0], chats[1]
	if friends.Key() != "telegram:chat:1" || friends.Title != "Friends" || !friends.FirstSeen.Equal(day1) || !friends.LastSeen.Equal(day1.Add(3*time.Hour)) ||
		friends.LastMessage == nil || !strings.Contains(string(friends.LastMessage.Payload), `"two"`) {
		t.Fatalf("friends %+v, last message %+v", friends, friends.LastMessage)
	}
	if alice.Name != "Alice (test)" || alice.Title != "Alice" {
		t.Fatalf("alice %+v", alice)
	}
	days, err := l.ChatDays(ctx, "telegram", "chat:1", day1.Add(-24*time.Hour))
	if err != nil || len(days) != 2 || days[0] != (ChatDay{"2026-11-01", 1}) || days[1] != (ChatDay{"2026-11-02", 1}) {
		t.Fatalf("days %+v, %v", days, err)
	}

	if err := l.Prune(ctx, day1.Add(time.Hour+time.Minute)); err != nil {
		t.Fatal(err)
	}
	chats, _ = l.Chats(ctx)
	days, _ = l.ChatDays(ctx, "telegram", "chat:1", day1.Add(-24*time.Hour))
	if len(chats) != 2 || chats[1].LastMessage != nil || len(days) != 1 {
		t.Fatalf("after pruning: chats %+v, days %+v", chats, days)
	}
}

// The people who send messages in a chat are kept with their latest name,
// which also names them where a later event has none; an operator's name
// for a chat wins over the platform's title.
func TestChatPeopleAndNames(t *testing.T) {
	l := openLog(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	send := func(actor, name string, direction otogi.Direction) {
		at = at.Add(time.Minute)
		e := otogi.Event{ID: otogi.EventID("e" + at.Format("1504")), Platform: "qq", Account: "otogi", Action: otogi.ActionMessageCreated, Direction: direction,
			Conversation: otogi.Conversation{ID: "group:G1", Kind: otogi.ConversationGroup, Description: "喝茶聊天", Members: 42},
			Actor:        otogi.Actor{ID: otogi.ActorID(actor), Name: name}, Message: otogi.TextMessage("hi")}
		l.Record(ctx, otogi.Record{At: at, Kind: "event.received", Component: "kernel", Event: e.ID, Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID, Payload: e})
	}
	send("user:M1", "Alice", otogi.DirectionIncoming)
	send("user:M1", "", otogi.DirectionIncoming)
	send("user:M2", "Bob", otogi.DirectionIncoming)
	send("user:B1", "Otogi", otogi.DirectionOutgoing) // the bot is not one of the people

	people, err := l.ChatPeople(ctx, "qq", "group:G1", 10)
	if err != nil || len(people) != 2 || people[0].Name != "Bob" || people[1].Name != "Alice" || people[1].Messages != 2 {
		t.Fatalf("people %+v, %v", people, err)
	}
	chats, _ := l.Chats(ctx)
	if c := chats[0]; c.People != 2 || c.Members != 42 || c.Description != "喝茶聊天" {
		t.Fatalf("chat %+v", c)
	}
	if _, err := l.NameChat(ctx, "qq", "group:G1", "茶话会"); err != nil {
		t.Fatal(err)
	}
	if chat, person, err := l.Names(ctx, "qq", "group:G1", "user:M1"); chat != "茶话会" || person != "Alice" || err != nil {
		t.Fatalf("names %q %q %v", chat, person, err)
	}
	if chat, person, err := l.Names(ctx, "qq", "user:U9", "user:U9"); chat != "" || person != "" || err != nil {
		t.Fatalf("unknown names %q %q %v", chat, person, err)
	}
}

// People are found across chats and platforms by any name they were seen
// with or by their ID, each once, under the latest name, with the chats
// they were last seen in; with nothing to find, the most recently seen.
func TestPeople(t *testing.T) {
	l := openLog(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	send := func(platform, conversation, title, actor, name string) {
		at = at.Add(time.Minute)
		e := otogi.Event{ID: otogi.EventID("e" + at.Format("1504")), Platform: otogi.PlatformID(platform), Account: "a", Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming,
			Conversation: otogi.Conversation{ID: otogi.ConversationID(conversation), Kind: otogi.ConversationGroup, Title: title},
			Actor:        otogi.Actor{ID: otogi.ActorID(actor), Name: name}, Message: otogi.TextMessage("hi")}
		l.Record(ctx, otogi.Record{At: at, Kind: "event.received", Component: "kernel", Event: e.ID, Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID, Payload: e})
	}
	send("telegram", "chat:1", "Friends", "user:5", "Alice")
	send("telegram", "chat:2", "Work", "user:5", "Alice W.") // the latest name, in another chat
	send("telegram", "chat:3", "", "user:6", "Bob")
	send("qq", "group:G1", "茶话会", "user:A1", "alice")
	send("telegram", "chat:3", "", "user:56", "Carol")
	if _, err := l.NameChat(ctx, "telegram", "chat:2", "The office"); err != nil {
		t.Fatal(err)
	}
	found := func(q string, limit int) string {
		t.Helper()
		people, err := l.People(ctx, q, limit)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, p := range people {
			var chats []string
			for _, c := range p.Chats {
				chats = append(chats, c.ID+"="+c.Name)
			}
			out = append(out, p.ID+" "+p.Name+" "+strings.Join(chats, ","))
		}
		return strings.Join(out, "; ")
	}
	for _, c := range []struct {
		q     string
		limit int
		want  string
	}{
		// By a name, whatever its case, on every platform, by an earlier name too.
		{"ALICE", 20, "qq:user:A1 alice qq:group:G1=茶话会; telegram:user:5 Alice W. telegram:chat:2=The office,telegram:chat:1=Friends"},
		{"alice w", 20, "telegram:user:5 Alice W. telegram:chat:2=The office,telegram:chat:1=Friends"},
		// By ID, the one of exactly that ID first; the chat without a title has no name.
		{"telegram:user:5", 20, "telegram:user:5 Alice W. telegram:chat:2=The office,telegram:chat:1=Friends; telegram:user:56 Carol telegram:chat:3="},
		{"user:6", 20, "telegram:user:6 Bob telegram:chat:3="},
		{"qq:", 20, "qq:user:A1 alice qq:group:G1=茶话会"},
		{"nobody", 20, ""},
		// Nothing to find: the most recently seen, as many as asked for.
		{"", 2, "telegram:user:56 Carol telegram:chat:3=; qq:user:A1 alice qq:group:G1=茶话会"},
		{"  ", 20, "telegram:user:56 Carol telegram:chat:3=; qq:user:A1 alice qq:group:G1=茶话会; telegram:user:6 Bob telegram:chat:3=; telegram:user:5 Alice W. telegram:chat:2=The office,telegram:chat:1=Friends"},
	} {
		if got := found(c.q, c.limit); got != c.want {
			t.Errorf("People(%q, %d):\n got %s\nwant %s", c.q, c.limit, got, c.want)
		}
	}
	people, _ := l.People(ctx, "bob", 20)
	if len(people) != 1 || !people[0].LastSeen.Equal(time.Date(2026, 11, 1, 12, 3, 0, 0, time.UTC)) {
		t.Fatalf("Bob %+v", people)
	}
}

// A person seen in many chats lists the few they were last seen in.
func TestPersonsChatsAreFew(t *testing.T) {
	l := openLog(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for i := range 5 {
		at = at.Add(time.Minute)
		e := otogi.Event{ID: otogi.EventID(fmt.Sprintf("e%d", i)), Platform: "qq", Account: "a", Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming,
			Conversation: otogi.Conversation{ID: otogi.ConversationID(fmt.Sprintf("group:%d", i)), Kind: otogi.ConversationGroup},
			Actor:        otogi.Actor{ID: "user:A1", Name: "Alice"}, Message: otogi.TextMessage("hi")}
		l.Record(ctx, otogi.Record{At: at, Kind: "event.received", Component: "kernel", Event: e.ID, Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID, Payload: e})
	}
	people, err := l.People(ctx, "", 20)
	if err != nil || len(people) != 1 || len(people[0].Chats) != 3 || people[0].Chats[0].ID != "qq:group:4" || people[0].Chats[2].ID != "qq:group:2" {
		t.Fatalf("people %+v, %v", people, err)
	}
}
