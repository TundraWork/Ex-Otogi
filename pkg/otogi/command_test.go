package otogi

import (
	"strings"
	"testing"
	"time"
)

func TestParseCommandCandidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		text          string
		wantMatched   bool
		wantErrSubstr string
		wantPrefix    CommandPrefix
		wantName      string
		wantMention   string
		wantTokens    []string
	}{
		{
			name:        "ordinary command with mention and value tokens",
			text:        " /Raw@MyBot hello world ",
			wantMatched: true,
			wantPrefix:  CommandPrefixOrdinary,
			wantName:    "raw",
			wantMention: "MyBot",
			wantTokens:  []string{"hello", "world"},
		},
		{
			name:        "system command candidate",
			text:        "~history --limit 5",
			wantMatched: true,
			wantPrefix:  CommandPrefixSystem,
			wantName:    "history",
			wantTokens:  []string{"--limit", "5"},
		},
		{
			name:        "non command text",
			text:        "hello",
			wantMatched: false,
		},
		{
			name:          "missing command name",
			text:          "/",
			wantMatched:   true,
			wantErrSubstr: "missing command name",
		},
		{
			name:          "unsupported long option equals format",
			text:          "/raw --id=1",
			wantMatched:   true,
			wantErrSubstr: "unsupported option format",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			candidate, matched, err := ParseCommandCandidate(testCase.text)
			if matched != testCase.wantMatched {
				t.Fatalf("matched = %v, want %v", matched, testCase.wantMatched)
			}
			if testCase.wantErrSubstr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", testCase.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), testCase.wantErrSubstr) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstr)
				}
				return
			}
			if !matched {
				return
			}

			if candidate.Prefix != testCase.wantPrefix {
				t.Fatalf("prefix = %q, want %q", candidate.Prefix, testCase.wantPrefix)
			}
			if candidate.Name != testCase.wantName {
				t.Fatalf("name = %q, want %q", candidate.Name, testCase.wantName)
			}
			if candidate.Mention != testCase.wantMention {
				t.Fatalf("mention = %q, want %q", candidate.Mention, testCase.wantMention)
			}
			if strings.Join(candidate.Tokens, ",") != strings.Join(testCase.wantTokens, ",") {
				t.Fatalf("tokens = %v, want %v", candidate.Tokens, testCase.wantTokens)
			}
		})
	}
}

func TestBindCommand(t *testing.T) {
	t.Parallel()

	spec := CommandSpec{
		Prefix: CommandPrefixOrdinary,
		Name:   "raw",
		Options: []CommandOptionSpec{
			{Name: "article", Alias: "a", HasValue: true, Required: true},
			{Name: "json", Alias: "j"},
		},
	}
	sourceEvent := &Event{
		ID:         "evt-source",
		Kind:       EventKindArticleCreated,
		OccurredAt: time.Unix(10, 0).UTC(),
		Platform:   PlatformTelegram,
		Conversation: Conversation{
			ID:   "chat-1",
			Type: ConversationTypeGroup,
		},
		Article: &Article{ID: "msg-1", Text: "/raw"},
	}

	tests := []struct {
		name          string
		text          string
		wantErrSubstr string
		wantValue     string
		wantOptions   int
	}{
		{
			name:        "bind valid long and short options",
			text:        "/raw --article 114514 hello world -j",
			wantValue:   "hello world",
			wantOptions: 2,
		},
		{
			name:          "unknown option",
			text:          "/raw --unknown 1",
			wantErrSubstr: "unknown option",
		},
		{
			name:          "missing required option",
			text:          "/raw hello",
			wantErrSubstr: "missing required option",
		},
		{
			name:          "missing option value",
			text:          "/raw --article",
			wantErrSubstr: "requires a value",
		},
		{
			name:        "bind short alias option",
			text:        "/raw -a 114514 tail",
			wantValue:   "tail",
			wantOptions: 1,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			candidate, matched, err := ParseCommandCandidate(testCase.text)
			if err != nil {
				t.Fatalf("parse candidate failed: %v", err)
			}
			if !matched {
				t.Fatal("expected command candidate match")
			}

			invocation, bindErr := BindCommand(candidate, spec, sourceEvent)
			if testCase.wantErrSubstr == "" && bindErr != nil {
				t.Fatalf("unexpected bind error: %v", bindErr)
			}
			if testCase.wantErrSubstr != "" {
				if bindErr == nil {
					t.Fatalf("expected bind error containing %q", testCase.wantErrSubstr)
				}
				if !strings.Contains(bindErr.Error(), testCase.wantErrSubstr) {
					t.Fatalf("bind error = %v, want substring %q", bindErr, testCase.wantErrSubstr)
				}
				return
			}

			if invocation.Name != "raw" {
				t.Fatalf("name = %q, want raw", invocation.Name)
			}
			if invocation.Value != testCase.wantValue {
				t.Fatalf("value = %q, want %q", invocation.Value, testCase.wantValue)
			}
			if len(invocation.Options) != testCase.wantOptions {
				t.Fatalf("options len = %d, want %d", len(invocation.Options), testCase.wantOptions)
			}
			if invocation.SourceEventID != sourceEvent.ID {
				t.Fatalf("source event id = %q, want %q", invocation.SourceEventID, sourceEvent.ID)
			}
			if invocation.SourceEventKind != sourceEvent.Kind {
				t.Fatalf("source event kind = %q, want %q", invocation.SourceEventKind, sourceEvent.Kind)
			}
		})
	}
}

func TestCommandReceivedEventValidate(t *testing.T) {
	t.Parallel()

	base := &Event{
		ID:         "evt-command",
		Kind:       EventKindCommandReceived,
		OccurredAt: time.Unix(10, 0).UTC(),
		Platform:   PlatformTelegram,
		Conversation: Conversation{
			ID:   "chat-1",
			Type: ConversationTypeGroup,
		},
		Actor: Actor{ID: "actor-1"},
		Article: &Article{
			ID:   "msg-1",
			Text: "/raw",
		},
		Command: &CommandInvocation{
			Name:            "raw",
			SourceEventID:   "evt-source",
			SourceEventKind: EventKindArticleCreated,
			RawInput:        "/raw",
		},
	}

	if err := base.Validate(); err != nil {
		t.Fatalf("validate command event failed: %v", err)
	}

	invalid := *base
	command := *base.Command
	command.SourceEventID = ""
	invalid.Command = &command
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected invalid command event to fail validation")
	}
}
