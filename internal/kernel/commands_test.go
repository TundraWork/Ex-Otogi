package kernel

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestCommandParser(t *testing.T) {
	parser := CommandParser([]string{"/", "!"})
	tests := []struct {
		text, action string
		want         *otogi.Command
	}{
		{"/remind 10m  tea", "", &otogi.Command{Name: "remind", Args: "10m  tea", Argv: []string{"10m", "tea"}}},
		{"/Sleep@my_bot", "", &otogi.Command{Name: "sleep"}},
		{"!ping", "", &otogi.Command{Name: "ping"}},
		{"just text", "", nil},
		{"/ not a command", "", nil},
		{"/path/to/file", "", nil},
		{"/ping", string(otogi.ActionMessageEdited), nil}, // edits are not commands
	}
	for _, test := range tests {
		e := event("c", test.text)
		if test.action != "" {
			e.Action = otogi.EventAction(test.action)
		}
		if verdict, err := parser.Intercept(context.Background(), &e); verdict != otogi.Continue || err != nil {
			t.Fatalf("%q: verdict %v, %v", test.text, verdict, err)
		}
		if (e.Command == nil) != (test.want == nil) || (e.Command != nil && (e.Command.Name != test.want.Name || e.Command.Args != test.want.Args || !slices.Equal(e.Command.Argv, test.want.Argv))) {
			t.Errorf("%q: command = %+v, want %+v", test.text, e.Command, test.want)
		}
	}
}

// "@Otogi /status" in a group runs /status; a mention of someone else
// before a command does not.
func TestCommandAfterMentionOfTheAccount(t *testing.T) {
	parser := CommandParser([]string{"/"})
	for _, test := range []struct {
		mentioned bool
		want      bool
	}{{true, true}, {false, false}} {
		e := event("c", "@Otogi /status now")
		e.Mentioned = test.mentioned
		e.Message.Parts[0].Annotations = []otogi.Annotation{{Offset: 0, Length: 6, Kind: otogi.AnnotationMention, Target: "user:B1"}}
		if _, err := parser.Intercept(context.Background(), &e); err != nil {
			t.Fatal(err)
		}
		if got := e.Command != nil && e.Command.Name == "status" && e.Command.Args == "now"; got != test.want {
			t.Errorf("mentioned %v: command = %+v", test.mentioned, e.Command)
		}
	}
}

func TestCommandsAndFiltersRouteEvents(t *testing.T) {
	var mu sync.Mutex
	got := map[otogi.ModuleID][]string{}
	handler := func(id otogi.ModuleID) func(context.Context, otogi.Event, otogi.Invocation) error {
		return func(_ context.Context, e otogi.Event, _ otogi.Invocation) error {
			mu.Lock()
			got[id] = append(got[id], e.Message.PlainText())
			mu.Unlock()
			return nil
		}
	}
	chat := module("chat", handler("chat")) // plain message.created subscriber
	sleeper := moduleFunc{descriptor: otogi.ModuleDescriptor{ID: "sleeper", Commands: []otogi.CommandSpec{{Name: "sleep"}}}, handle: handler("sleeper")}
	watcher := moduleFunc{descriptor: otogi.ModuleDescriptor{ID: "watcher", Subscribes: []otogi.EventAction{otogi.ActionMessageCreated},
		Filter: otogi.Filter{Directions: []otogi.Direction{otogi.DirectionIncoming, otogi.DirectionOutgoing}, ConversationKinds: []otogi.ConversationKind{otogi.ConversationGroup}}}, handle: handler("watcher")}
	k := start(t, Config{}, []otogi.Module{chat, sleeper, watcher}, []otogi.Middleware{CommandParser([]string{"/"})}, nil)

	send := func(text string, direction otogi.Direction, kind otogi.ConversationKind) {
		e := event("c", text)
		e.Direction, e.Conversation.Kind = direction, kind
		await(t, submit(t, k, e))
	}
	send("/sleep 1h", otogi.DirectionIncoming, otogi.ConversationGroup)    // only the command owner
	send("/unknown", otogi.DirectionIncoming, otogi.ConversationGroup)     // an ordinary message
	send("my own reply", otogi.DirectionOutgoing, otogi.ConversationGroup) // only modules opting in to outgoing
	send("in private", otogi.DirectionIncoming, otogi.ConversationDirect)  // watcher wants groups only

	want := map[otogi.ModuleID][]string{
		"chat":    {"/unknown", "in private"},
		"sleeper": {"/sleep 1h"},
		"watcher": {"/unknown", "my own reply"},
	}
	for id, texts := range want {
		if !slices.Equal(got[id], texts) {
			t.Errorf("%s got %q, want %q", id, got[id], texts)
		}
	}
}

func TestDuplicateAndInvalidCommandsAreRejected(t *testing.T) {
	ok := func(context.Context, otogi.Event, otogi.Invocation) error { return nil }
	declare := func(id otogi.ModuleID, name string) otogi.Module {
		return moduleFunc{descriptor: otogi.ModuleDescriptor{ID: id, Commands: []otogi.CommandSpec{{Name: name}}}, handle: ok}
	}
	for name, modules := range map[string][]otogi.Module{
		"duplicate": {declare("a", "x"), declare("b", "x")},
		"invalid":   {declare("a", "Bad Name")},
	} {
		if _, err := New(Config{}, modules, nil, &messenger{}, quiet); !errors.Is(err, otogi.ErrInvalid) {
			t.Errorf("%s: New = %v, want ErrInvalid", name, err)
		}
	}
}
