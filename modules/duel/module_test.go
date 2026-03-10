package duel

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

func TestScoreHand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hand []card
		want int
	}{
		{
			name: "empty hand",
			want: 0,
		},
		{
			name: "face card counts as ten",
			hand: []card{{rank: 13, label: "♤K"}},
			want: 10,
		},
		{
			name: "ace counts as eleven when safe",
			hand: []card{{rank: 1, label: "♤A"}, {rank: 9, label: "♢9"}},
			want: 20,
		},
		{
			name: "ace downgrades to one when needed",
			hand: []card{{rank: 1, label: "♤A"}, {rank: 9, label: "♢9"}, {rank: 10, label: "♡10"}},
			want: 20,
		},
		{
			name: "multiple aces stay optimal",
			hand: []card{{rank: 1, label: "♤A"}, {rank: 1, label: "♢A"}, {rank: 9, label: "♡9"}},
			want: 21,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := scoreHand(testCase.hand); got != testCase.want {
				t.Fatalf("scoreHand() = %d, want %d", got, testCase.want)
			}
		})
	}
}

func TestParseDuelInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command *otogi.CommandInvocation
		want    duelInput
		wantErr string
	}{
		{
			name:    "empty input",
			command: &otogi.CommandInvocation{Name: duelCommandName},
			want:    duelInput{},
		},
		{
			name: "option max",
			command: &otogi.CommandInvocation{
				Name: duelCommandName,
				Options: []otogi.CommandOption{
					{Name: "max", HasValue: true, Value: "6"},
				},
			},
			want: duelInput{maxSet: true, max: 6},
		},
		{
			name: "bare value shorthand",
			command: &otogi.CommandInvocation{
				Name:  duelCommandName,
				Value: "5",
			},
			want: duelInput{maxSet: true, max: 5},
		},
		{
			name: "start shorthand",
			command: &otogi.CommandInvocation{
				Name:  duelCommandName,
				Value: "start",
			},
			want: duelInput{start: true},
		},
		{
			name: "conflicting controls",
			command: &otogi.CommandInvocation{
				Name: duelCommandName,
				Options: []otogi.CommandOption{
					{Name: "start"},
					{Name: "cancel"},
				},
			},
			wantErr: "不能一起发",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseDuelInput(testCase.command)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("parseDuelInput() error = %v, want substring %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDuelInput() unexpected error: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("parseDuelInput() = %+v, want %+v", got, testCase.want)
			}
		})
	}
}

func TestModuleOpenLobbyJoinAndAutoStartAtCapacity(t *testing.T) {
	baseTime := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)
	module, dispatcher, _, _ := newTestModule(baseTime, []card{
		{rank: 10, label: "♧10"},
		{rank: 9, label: "♢9"},
		{rank: 8, label: "♡8"},
	})

	host := actor("host", "房主")
	guestOne := actor("guest-1", "玩家一")
	guestTwo := actor("guest-2", "玩家二")

	if err := module.handleEvent(context.Background(), commandEvent("m1", host, duelCommandName, "3", nil, "")); err != nil {
		t.Fatalf("open lobby: %v", err)
	}
	if len(dispatcher.sendRequests) != 1 {
		t.Fatalf("send requests = %d, want 1", len(dispatcher.sendRequests))
	}

	if err := module.handleEvent(context.Background(), commandEvent("m2", guestOne, joinCommandName, "", nil, "")); err != nil {
		t.Fatalf("guest one join: %v", err)
	}
	if err := module.handleEvent(context.Background(), commandEvent("m3", guestTwo, joinCommandName, "", nil, "")); err != nil {
		t.Fatalf("guest two join: %v", err)
	}

	state := onlyState(t, module)
	if state.phase != phaseGame {
		t.Fatalf("phase = %s, want %s", state.phase, phaseGame)
	}
	if len(state.order) != 3 {
		t.Fatalf("players = %d, want 3", len(state.order))
	}
	if len(dispatcher.editRequests) != 2 {
		t.Fatalf("edit requests = %d, want 2", len(dispatcher.editRequests))
	}
	if !strings.Contains(dispatcher.editRequests[1].Text, "人数已满") {
		t.Fatalf("final board = %q, want start note", dispatcher.editRequests[1].Text)
	}
}

func TestModuleDirectChallengeOnlyAllowsInvitedPlayer(t *testing.T) {
	baseTime := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)
	module, dispatcher, _, memory := newTestModule(baseTime, []card{
		{rank: 10, label: "♧10"},
		{rank: 10, label: "♢10"},
	})
	memory.replied = otogi.Memory{
		Actor: actor("target", "受邀者"),
		Article: otogi.Article{
			ID:   "reply-msg",
			Text: "challenge me",
		},
	}
	memory.found = true

	host := actor("host", "房主")
	stranger := actor("stranger", "路人")
	target := actor("target", "受邀者")

	if err := module.handleEvent(context.Background(), commandEvent("m1", host, duelCommandName, "", nil, "reply-msg")); err != nil {
		t.Fatalf("open direct challenge: %v", err)
	}

	if err := module.handleEvent(context.Background(), commandEvent("m2", stranger, joinCommandName, "", nil, "")); err != nil {
		t.Fatalf("stranger join: %v", err)
	}
	if len(dispatcher.sendRequests) != 2 {
		t.Fatalf("send requests = %d, want 2", len(dispatcher.sendRequests))
	}
	if !strings.Contains(dispatcher.sendRequests[1].Text, "还在等 受邀者 确认") {
		t.Fatalf("reply text = %q, want invited-only warning", dispatcher.sendRequests[1].Text)
	}

	if err := module.handleEvent(context.Background(), commandEvent("m3", target, joinCommandName, "", nil, "")); err != nil {
		t.Fatalf("target join: %v", err)
	}

	state := onlyState(t, module)
	if state.phase != phaseGame {
		t.Fatalf("phase = %s, want %s", state.phase, phaseGame)
	}
	if len(state.order) != 2 {
		t.Fatalf("players = %d, want 2", len(state.order))
	}
	if !strings.Contains(dispatcher.editRequests[len(dispatcher.editRequests)-1].Text, "人数已满") {
		t.Fatalf("final board = %q, want game start", dispatcher.editRequests[len(dispatcher.editRequests)-1].Text)
	}
}

func TestModuleBustResolutionMutesOnlyBustedPlayers(t *testing.T) {
	baseTime := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)
	module, dispatcher, moderation, _ := newTestModule(baseTime, []card{
		{rank: 10, label: "♧10"},
		{rank: 9, label: "♢9"},
		{rank: 10, label: "♡10"},
		{rank: 13, label: "♤K"},
		{rank: 5, label: "♧5"},
	})

	host := actor("host", "房主")
	guestOne := actor("guest-1", "玩家一")
	guestTwo := actor("guest-2", "玩家二")

	runCommands(t, module,
		commandEvent("m1", host, duelCommandName, "3", nil, ""),
		commandEvent("m2", guestOne, joinCommandName, "", nil, ""),
		commandEvent("m3", guestTwo, joinCommandName, "", nil, ""),
		commandEvent("m4", host, hitCommandName, "", nil, ""),
		commandEvent("m5", host, standCommandName, "", nil, ""),
		commandEvent("m6", guestOne, hitCommandName, "", nil, ""),
		commandEvent("m7", guestOne, standCommandName, "", nil, ""),
		commandEvent("m8", guestTwo, hitCommandName, "", nil, ""),
		commandEvent("m9", guestTwo, hitCommandName, "", nil, ""),
		commandEvent("m10", guestTwo, hitCommandName, "", nil, ""),
	)

	if len(module.games) != 0 {
		t.Fatalf("active games = %d, want 0", len(module.games))
	}
	if len(moderation.requests) != 1 {
		t.Fatalf("moderation requests = %d, want 1", len(moderation.requests))
	}
	if moderation.requests[0].MemberID != guestTwo.ID {
		t.Fatalf("muted member = %q, want %q", moderation.requests[0].MemberID, guestTwo.ID)
	}
	finalBoard := dispatcher.editRequests[len(dispatcher.editRequests)-1].Text
	if !strings.Contains(finalBoard, "玩家二") || !strings.Contains(finalBoard, "爆牌") {
		t.Fatalf("final board = %q, want bust outcome for 玩家二", finalBoard)
	}
	if !strings.Contains(finalBoard, "😄") || !strings.Contains(finalBoard, "☢️") {
		t.Fatalf("final board = %q, want deterministic random emoji output", finalBoard)
	}
}

func TestModuleTimeoutMarksUnfinishedPlayersAsLosers(t *testing.T) {
	baseTime := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)
	module, dispatcher, moderation, _ := newTestModule(baseTime, []card{
		{rank: 10, label: "♧10"},
	})

	host := actor("host", "房主")
	guest := actor("guest", "玩家")

	runCommands(t, module,
		commandEvent("m1", host, duelCommandName, "2", nil, ""),
		commandEvent("m2", guest, joinCommandName, "", nil, ""),
		commandEvent("m3", guest, hitCommandName, "", nil, ""),
		commandEvent("m4", guest, standCommandName, "", nil, ""),
	)

	module.now = func() time.Time {
		return baseTime.Add(module.cfg.GameTimeout + time.Second)
	}
	if err := module.handleEvent(context.Background(), articleEvent("m5", actor("watcher", "旁观者"))); err != nil {
		t.Fatalf("timeout sweep: %v", err)
	}

	if len(moderation.requests) != 1 {
		t.Fatalf("moderation requests = %d, want 1", len(moderation.requests))
	}
	if moderation.requests[0].MemberID != host.ID {
		t.Fatalf("timed out member = %q, want %q", moderation.requests[0].MemberID, host.ID)
	}
	finalBoard := dispatcher.editRequests[len(dispatcher.editRequests)-1].Text
	if !strings.Contains(finalBoard, "超时") {
		t.Fatalf("final board = %q, want timeout outcome", finalBoard)
	}
	if !strings.Contains(finalBoard, "🥇") || !strings.Contains(finalBoard, "🌚") {
		t.Fatalf("final board = %q, want deterministic timeout emoji output", finalBoard)
	}
}

func TestScoreConclusionTieDoesNotMuteAnyone(t *testing.T) {
	t.Parallel()

	state := &gameState{
		order: []string{"a", "b"},
		players: map[string]*playerState{
			"a": {ID: "a", Name: "甲", Score: 10},
			"b": {ID: "b", Name: "乙", Score: 10},
		},
	}

	result := state.scoreConclusion("按当前点数结算。", 5*time.Minute)
	if len(result.loserReasons) != 0 {
		t.Fatalf("loser reasons = %v, want none", result.loserReasons)
	}
	if !strings.Contains(result.subtitle, "平手") {
		t.Fatalf("subtitle = %q, want tie", result.subtitle)
	}
}

type fixedShuffler struct {
	deck []card
}

func (s fixedShuffler) Shuffle(_ []card) []card {
	return append([]card(nil), s.deck...)
}

type fixedEmojiPicker struct{}

func (fixedEmojiPicker) Pick(options []string) string {
	if len(options) == 0 {
		return ""
	}

	return options[0]
}

type captureDispatcher struct {
	mu           sync.Mutex
	sendRequests []otogi.SendMessageRequest
	editRequests []otogi.EditMessageRequest
	nextID       int
}

func (d *captureDispatcher) SendMessage(
	_ context.Context,
	request otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.nextID++
	d.sendRequests = append(d.sendRequests, request)

	return &otogi.OutboundMessage{
		ID:     "board-" + strconv.Itoa(d.nextID),
		Target: request.Target,
	}, nil
}

func (d *captureDispatcher) EditMessage(_ context.Context, request otogi.EditMessageRequest) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.editRequests = append(d.editRequests, request)

	return nil
}

func (*captureDispatcher) DeleteMessage(context.Context, otogi.DeleteMessageRequest) error {
	return nil
}

func (*captureDispatcher) SetReaction(context.Context, otogi.SetReactionRequest) error {
	return nil
}

func (*captureDispatcher) ListSinks(context.Context) ([]otogi.EventSink, error) {
	return nil, nil
}

func (*captureDispatcher) ListSinksByPlatform(context.Context, otogi.Platform) ([]otogi.EventSink, error) {
	return nil, nil
}

type captureModerationDispatcher struct {
	mu       sync.Mutex
	requests []otogi.RestrictMemberRequest
}

func (d *captureModerationDispatcher) RestrictMember(
	_ context.Context,
	request otogi.RestrictMemberRequest,
) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.requests = append(d.requests, request)

	return nil
}

type memoryStub struct {
	replied otogi.Memory
	found   bool
	err     error
}

func (*memoryStub) Get(context.Context, otogi.MemoryLookup) (otogi.Memory, bool, error) {
	return otogi.Memory{}, false, nil
}

func (m *memoryStub) GetReplied(context.Context, *otogi.Event) (otogi.Memory, bool, error) {
	return m.replied, m.found, m.err
}

func (*memoryStub) GetReplyChain(context.Context, *otogi.Event) ([]otogi.ReplyChainEntry, error) {
	return nil, nil
}

func (*memoryStub) ListConversationContextBefore(
	context.Context,
	otogi.ConversationContextBeforeQuery,
) ([]otogi.ConversationContextEntry, error) {
	return nil, nil
}

func newTestModule(
	baseTime time.Time,
	deck []card,
) (*Module, *captureDispatcher, *captureModerationDispatcher, *memoryStub) {
	dispatcher := &captureDispatcher{}
	moderation := &captureModerationDispatcher{}
	memory := &memoryStub{}
	module := &Module{
		cfg: config{
			JoinTimeout:       time.Minute,
			GameTimeout:       time.Minute,
			LoserMuteDuration: 5 * time.Minute,
			DefaultMaxPlayers: 4,
			MaxPlayersLimit:   8,
		},
		dispatcher: dispatcher,
		moderation: moderation,
		memory:     memory,
		now: func() time.Time {
			return baseTime
		},
		shuffler: fixedShuffler{deck: deck},
		renderer: newRenderer(fixedEmojiPicker{}),
		games:    make(map[conversationKey]*gameState),
	}

	return module, dispatcher, moderation, memory
}

func actor(id string, name string) otogi.Actor {
	return otogi.Actor{
		ID:          id,
		DisplayName: name,
	}
}

func commandEvent(
	messageID string,
	actor otogi.Actor,
	commandName string,
	value string,
	options []otogi.CommandOption,
	replyTo string,
) *otogi.Event {
	return &otogi.Event{
		ID:         "evt-" + messageID,
		Kind:       otogi.EventKindCommandReceived,
		OccurredAt: time.Now(),
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: otogi.Conversation{
			ID:    "chat-1",
			Type:  otogi.ConversationTypeGroup,
			Title: "Test Group",
		},
		Actor: actor,
		Article: &otogi.Article{
			ID:               messageID,
			Text:             "/" + commandName,
			ReplyToArticleID: replyTo,
		},
		Command: &otogi.CommandInvocation{
			Name:            commandName,
			Value:           value,
			Options:         append([]otogi.CommandOption(nil), options...),
			SourceEventID:   "src-" + messageID,
			SourceEventKind: otogi.EventKindArticleCreated,
			RawInput:        "/" + commandName,
		},
	}
}

func articleEvent(messageID string, actor otogi.Actor) *otogi.Event {
	return &otogi.Event{
		ID:         "evt-" + messageID,
		Kind:       otogi.EventKindArticleCreated,
		OccurredAt: time.Now(),
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: otogi.Conversation{
			ID:    "chat-1",
			Type:  otogi.ConversationTypeGroup,
			Title: "Test Group",
		},
		Actor: actor,
		Article: &otogi.Article{
			ID:   messageID,
			Text: "hello",
		},
	}
}

func runCommands(t *testing.T, module *Module, events ...*otogi.Event) {
	t.Helper()

	for _, event := range events {
		if err := module.handleEvent(context.Background(), event); err != nil {
			t.Fatalf("handleEvent(%s) failed: %v", event.ID, err)
		}
	}
}

func onlyState(t *testing.T, module *Module) *gameState {
	t.Helper()

	if len(module.games) != 1 {
		t.Fatalf("games len = %d, want 1", len(module.games))
	}
	for _, state := range module.games {
		return state
	}

	t.Fatal("expected one game state")
	return nil
}
