package duel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"ex-otogi/pkg/otogi"
)

const (
	moduleName            = "duel"
	duelCommandName       = "duel"
	joinCommandName       = "dueljoin"
	hitCommandName        = "duelhit"
	standCommandName      = "duelstand"
	defaultReaperInterval = 5 * time.Second
)

var standardDeck = []card{
	{rank: 1, label: "♧A"},
	{rank: 1, label: "♢A"},
	{rank: 1, label: "♡A"},
	{rank: 1, label: "♤A"},
	{rank: 2, label: "♧2"},
	{rank: 2, label: "♢2"},
	{rank: 2, label: "♡2"},
	{rank: 2, label: "♤2"},
	{rank: 3, label: "♧3"},
	{rank: 3, label: "♢3"},
	{rank: 3, label: "♡3"},
	{rank: 3, label: "♤3"},
	{rank: 4, label: "♧4"},
	{rank: 4, label: "♢4"},
	{rank: 4, label: "♡4"},
	{rank: 4, label: "♤4"},
	{rank: 5, label: "♧5"},
	{rank: 5, label: "♢5"},
	{rank: 5, label: "♡5"},
	{rank: 5, label: "♤5"},
	{rank: 6, label: "♧6"},
	{rank: 6, label: "♢6"},
	{rank: 6, label: "♡6"},
	{rank: 6, label: "♤6"},
	{rank: 7, label: "♧7"},
	{rank: 7, label: "♢7"},
	{rank: 7, label: "♡7"},
	{rank: 7, label: "♤7"},
	{rank: 8, label: "♧8"},
	{rank: 8, label: "♢8"},
	{rank: 8, label: "♡8"},
	{rank: 8, label: "♤8"},
	{rank: 9, label: "♧9"},
	{rank: 9, label: "♢9"},
	{rank: 9, label: "♡9"},
	{rank: 9, label: "♤9"},
	{rank: 10, label: "♧10"},
	{rank: 10, label: "♢10"},
	{rank: 10, label: "♡10"},
	{rank: 10, label: "♤10"},
	{rank: 11, label: "♧J"},
	{rank: 11, label: "♢J"},
	{rank: 11, label: "♡J"},
	{rank: 11, label: "♤J"},
	{rank: 12, label: "♧Q"},
	{rank: 12, label: "♢Q"},
	{rank: 12, label: "♡Q"},
	{rank: 12, label: "♤Q"},
	{rank: 13, label: "♧K"},
	{rank: 13, label: "♢K"},
	{rank: 13, label: "♡K"},
	{rank: 13, label: "♤K"},
}

type phase string

const (
	phaseLobby phase = "lobby"
	phaseGame  phase = "game"
)

type deckShuffler interface {
	Shuffle(cards []card) []card
}

type randomDeckShuffler struct{}

func (randomDeckShuffler) Shuffle(cards []card) []card {
	shuffled := append([]card(nil), cards...)
	rand.Shuffle(len(shuffled), func(i int, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	return shuffled
}

type card struct {
	rank  int
	label string
}

type conversationKey struct {
	tenantID       string
	platform       otogi.Platform
	sourceID       string
	conversationID string
}

type playerState struct {
	ID     string
	Name   string
	hand   []card
	Score  int
	Stood  bool
	Busted bool
}

type participantRef struct {
	ID   string
	Name string
}

type gameState struct {
	key            conversationKey
	target         otogi.OutboundTarget
	boardMessageID string
	phase          phase
	hostID         string
	invited        *participantRef
	maxPlayers     int
	deadline       time.Time
	deck           []card
	order          []string
	players        map[string]*playerState
}

type conclusion struct {
	kind         conclusionKind
	title        string
	subtitle     string
	footer       string
	loserReasons map[string]string
}

type duelInput struct {
	start  bool
	cancel bool
	maxSet bool
	max    int
}

// Module provides multiplayer blackjack-style duels in group conversations.
type Module struct {
	cfg            config
	dispatcher     otogi.SinkDispatcher
	moderation     otogi.ModerationDispatcher
	memory         otogi.MemoryService
	now            func() time.Time
	shuffler       deckShuffler
	renderer       *renderer
	reaperInterval time.Duration
	reaperCancel   context.CancelFunc
	reaperWG       sync.WaitGroup

	mu    sync.Mutex
	games map[conversationKey]*gameState
}

// New creates a duel module with runtime-loaded configuration.
func New() *Module {
	return &Module{}
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return moduleName
}

// Spec declares duel command registrations and the serialized event handler.
func (m *Module) Spec() otogi.ModuleSpec {
	return otogi.ModuleSpec{
		Handlers: []otogi.ModuleHandler{
			{
				Capability: otogi.Capability{
					Name:        "duel-event-handler",
					Description: "manages duel lobbies, gameplay, and timeout cleanup",
					Interest: otogi.InterestSet{
						Kinds: []otogi.EventKind{
							otogi.EventKindArticleCreated,
							otogi.EventKindCommandReceived,
						},
						RequireArticle: true,
					},
					RequiredServices: []string{
						otogi.ServiceSinkDispatcher,
						otogi.ServiceModerationDispatcher,
						otogi.ServiceMemory,
					},
				},
				Subscription: otogi.SubscriptionSpec{
					Name:    "duel-events",
					Workers: 1,
				},
				Handler: m.handleEvent,
			},
		},
		Commands: []otogi.CommandSpec{
			{
				Prefix:      otogi.CommandPrefixOrdinary,
				Name:        duelCommandName,
				Description: "发起对局；回复消息可 1v1，也能用 --max/--start/--cancel 控制等待中的对局",
				Options: []otogi.CommandOptionSpec{
					{
						Name:        "max",
						Alias:       "m",
						HasValue:    true,
						Description: "设置或调整公开对局的人数上限",
					},
					{
						Name:        "start",
						Alias:       "s",
						Description: "报名够两人后提前开始对局",
					},
					{
						Name:        "cancel",
						Alias:       "c",
						Description: "取消当前等待中的对局",
					},
				},
			},
			{
				Prefix:      otogi.CommandPrefixOrdinary,
				Name:        joinCommandName,
				Description: "加入当前对局，或接受 1v1 邀请",
			},
			{
				Prefix:      otogi.CommandPrefixOrdinary,
				Name:        hitCommandName,
				Description: "在对局中摸一张牌",
			},
			{
				Prefix:      otogi.CommandPrefixOrdinary,
				Name:        standCommandName,
				Description: "在对局中停牌并保留当前点数",
			},
		},
	}
}

// OnRegister loads config and resolves runtime dependencies.
func (m *Module) OnRegister(_ context.Context, runtime otogi.ModuleRuntime) error {
	cfg, err := loadConfig(runtime.Config())
	if err != nil {
		return fmt.Errorf("duel load config: %w", err)
	}

	dispatcher, err := otogi.ResolveAs[otogi.SinkDispatcher](
		runtime.Services(),
		otogi.ServiceSinkDispatcher,
	)
	if err != nil {
		return fmt.Errorf("duel resolve sink dispatcher: %w", err)
	}

	moderation, err := otogi.ResolveAs[otogi.ModerationDispatcher](
		runtime.Services(),
		otogi.ServiceModerationDispatcher,
	)
	if err != nil {
		return fmt.Errorf("duel resolve moderation dispatcher: %w", err)
	}

	memoryService, err := otogi.ResolveAs[otogi.MemoryService](
		runtime.Services(),
		otogi.ServiceMemory,
	)
	if err != nil {
		return fmt.Errorf("duel resolve memory service: %w", err)
	}

	m.cfg = cfg
	m.dispatcher = dispatcher
	m.moderation = moderation
	m.memory = memoryService
	if m.now == nil {
		m.now = time.Now
	}
	if m.shuffler == nil {
		m.shuffler = randomDeckShuffler{}
	}
	if m.renderer == nil {
		m.renderer = newRenderer(nil)
	}
	if m.reaperInterval <= 0 {
		m.reaperInterval = defaultReaperInterval
	}
	if m.games == nil {
		m.games = make(map[conversationKey]*gameState)
	}

	return nil
}

// OnStart starts the module lifecycle.
func (m *Module) OnStart(ctx context.Context) error {
	if m.reaperCancel != nil {
		return nil
	}

	reaperCtx, cancel := context.WithCancel(ctx)
	m.reaperCancel = cancel
	m.reaperWG.Add(1)
	go func() {
		defer m.reaperWG.Done()
		defer func() {
			if panicValue := recover(); panicValue != nil {
				slog.Default().ErrorContext(reaperCtx, "duel reaper panic", "panic", panicValue)
			}
		}()

		ticker := time.NewTicker(m.reaperInterval)
		defer ticker.Stop()

		for {
			select {
			case <-reaperCtx.Done():
				return
			case <-ticker.C:
				if err := m.sweepExpiredGames(reaperCtx); err != nil {
					slog.Default().ErrorContext(reaperCtx, "duel reaper sweep failed", "error", err)
				}
			}
		}
	}()

	return nil
}

// OnShutdown clears in-memory duel state.
func (m *Module) OnShutdown(_ context.Context) error {
	if m.reaperCancel != nil {
		m.reaperCancel()
		m.reaperCancel = nil
	}
	m.reaperWG.Wait()

	m.mu.Lock()
	m.games = make(map[conversationKey]*gameState)
	m.mu.Unlock()

	return nil
}

func (m *Module) handleEvent(ctx context.Context, event *otogi.Event) error {
	if event == nil || event.Article == nil {
		return nil
	}
	if m.dispatcher == nil {
		return fmt.Errorf("duel handle event: sink dispatcher not configured")
	}
	if m.moderation == nil {
		return fmt.Errorf("duel handle event: moderation dispatcher not configured")
	}
	if m.memory == nil {
		return fmt.Errorf("duel handle event: memory service not configured")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.expireConversationStateLocked(ctx, conversationKeyFromEvent(event)); err != nil {
		return err
	}
	if event.Kind != otogi.EventKindCommandReceived || event.Command == nil {
		return nil
	}
	if event.Conversation.Type != otogi.ConversationTypeGroup {
		return m.reply(ctx, event, notGroupMessage())
	}
	if strings.TrimSpace(event.Actor.ID) == "" {
		return m.reply(ctx, event, unidentifiedActorMessage())
	}

	switch strings.ToLower(strings.TrimSpace(event.Command.Name)) {
	case duelCommandName:
		return m.handleDuelCommand(ctx, event)
	case joinCommandName:
		return m.handleJoinCommand(ctx, event)
	case hitCommandName:
		return m.handleHitCommand(ctx, event)
	case standCommandName:
		return m.handleStandCommand(ctx, event)
	default:
		return nil
	}
}

func (m *Module) handleDuelCommand(ctx context.Context, event *otogi.Event) error {
	if event == nil || event.Command == nil || event.Article == nil {
		return nil
	}
	if event.Actor.IsBot {
		return m.reply(ctx, event, botCannotJoinMessage())
	}

	key := conversationKeyFromEvent(event)
	state := m.games[key]
	input, err := parseDuelInput(event.Command)
	if err != nil {
		return m.reply(ctx, event, err.Error())
	}

	if state == nil {
		if input.start || input.cancel {
			return m.reply(ctx, event, noPendingDuelMessage())
		}

		replying := strings.TrimSpace(event.Article.ReplyToArticleID) != ""
		if replying {
			return m.openDirectChallenge(ctx, event, key)
		}

		return m.openLobby(ctx, event, key, input)
	}

	if input.start || input.cancel || input.maxSet {
		return m.controlExistingLobby(ctx, event, state, input)
	}

	return m.reply(ctx, event, duelBusyMessage())
}

func (m *Module) handleJoinCommand(ctx context.Context, event *otogi.Event) error {
	if event == nil || event.Article == nil {
		return nil
	}
	if event.Actor.IsBot {
		return m.reply(ctx, event, botCannotJoinMessage())
	}

	key := conversationKeyFromEvent(event)
	state := m.games[key]
	if state == nil || state.phase != phaseLobby {
		return nil
	}
	if state.hasPlayer(event.Actor.ID) {
		return nil
	}
	if state.invitedOnly() && state.invited.ID != event.Actor.ID {
		return m.reply(ctx, event, invitedOnlyMessage(state.invited.Name))
	}
	if state.atCapacity() {
		return nil
	}

	player := state.addPlayer(event.Actor)

	if state.atCapacity() {
		return m.startGame(ctx, state, autoStartNote())
	}

	return m.editBoard(ctx, state, m.renderer.lobbyBoard(state, m.now(), joinedLobbyNote(player.Name)))
}

func (m *Module) handleHitCommand(ctx context.Context, event *otogi.Event) error {
	key := conversationKeyFromEvent(event)
	state := m.games[key]
	if state == nil || state.phase != phaseGame {
		return nil
	}

	player := state.player(event.Actor.ID)
	if !player.canAct() {
		return nil
	}

	card, ok := state.hit(event.Actor.ID)
	if !ok {
		return m.finishCurrentGame(ctx, state, state.scoreConclusion(deckEmptyNote(), m.cfg.LoserMuteDuration))
	}

	if result, done := state.conclusionAfterAction(hitNote(player.Name, card), m.cfg.LoserMuteDuration); done {
		return m.finishCurrentGame(ctx, state, result)
	}

	return m.editBoard(ctx, state, m.renderer.gameBoard(state, m.now(), hitNote(player.Name, card)))
}

func (m *Module) handleStandCommand(ctx context.Context, event *otogi.Event) error {
	key := conversationKeyFromEvent(event)
	state := m.games[key]
	if state == nil || state.phase != phaseGame {
		return nil
	}

	player := state.player(event.Actor.ID)
	if !player.canAct() {
		return nil
	}

	state.stand(event.Actor.ID)
	if result, done := state.conclusionAfterAction(standNote(player.Name), m.cfg.LoserMuteDuration); done {
		return m.finishCurrentGame(ctx, state, result)
	}

	return m.editBoard(ctx, state, m.renderer.gameBoard(state, m.now(), standNote(player.Name)))
}

func (m *Module) openDirectChallenge(
	ctx context.Context,
	event *otogi.Event,
	key conversationKey,
) error {
	replied, found, err := m.memory.GetReplied(ctx, event)
	if err != nil {
		return fmt.Errorf("duel resolve replied message: %w", err)
	}
	if !found {
		return m.reply(ctx, event, directChallengeNotFoundMessage())
	}
	if strings.TrimSpace(replied.Actor.ID) == "" {
		return m.reply(ctx, event, unidentifiedTargetMessage())
	}
	if replied.Actor.ID == event.Actor.ID {
		return m.reply(ctx, event, selfChallengeMessage())
	}
	if replied.Actor.IsBot {
		return m.reply(ctx, event, botCannotJoinMessage())
	}

	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("duel derive outbound target: %w", err)
	}

	state := newLobbyState(
		key,
		target,
		event.Actor,
		&participantRef{ID: replied.Actor.ID, Name: actorName(replied.Actor)},
		2,
		m.now().Add(m.cfg.JoinTimeout),
	)

	message, err := m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             m.renderer.lobbyBoard(state, m.now(), directChallengeCreatedNote(actorName(event.Actor))),
		ReplyToMessageID: event.Article.ID,
	})
	if err != nil {
		return fmt.Errorf("duel send direct challenge board: %w", err)
	}

	state.boardMessageID = message.ID
	m.games[key] = state

	return nil
}

func (m *Module) openLobby(
	ctx context.Context,
	event *otogi.Event,
	key conversationKey,
	input duelInput,
) error {
	maxPlayers := m.cfg.DefaultMaxPlayers
	if input.maxSet {
		if input.max > m.cfg.MaxPlayersLimit {
			return m.reply(ctx, event, maxPlayersMessage(m.cfg.MaxPlayersLimit))
		}
		if input.max < 2 {
			return m.reply(ctx, event, minimumPlayersMessage())
		}
		maxPlayers = input.max
	}

	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("duel derive outbound target: %w", err)
	}

	state := newLobbyState(key, target, event.Actor, nil, maxPlayers, m.now().Add(m.cfg.JoinTimeout))

	message, err := m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             m.renderer.lobbyBoard(state, m.now(), lobbyCreatedNote(actorName(event.Actor))),
		ReplyToMessageID: event.Article.ID,
	})
	if err != nil {
		return fmt.Errorf("duel send lobby board: %w", err)
	}

	state.boardMessageID = message.ID
	m.games[key] = state

	return nil
}

func (m *Module) controlExistingLobby(
	ctx context.Context,
	event *otogi.Event,
	state *gameState,
	input duelInput,
) error {
	if state.phase != phaseLobby {
		return m.reply(ctx, event, duelAlreadyStartedMessage())
	}
	if event.Actor.ID != state.hostID {
		return m.reply(ctx, event, lobbyControlOnlyHostMessage())
	}
	if input.start && input.cancel {
		return m.reply(ctx, event, conflictingControlMessage())
	}
	if input.cancel {
		return m.cancelLobby(ctx, state, cancelLobbyNote(state.playerName(state.hostID)))
	}
	if input.maxSet {
		if state.invitedOnly() {
			return m.reply(ctx, event, fixedDirectChallengeMessage())
		}
		if input.max < state.joinedCount() {
			return m.reply(ctx, event, maxTooSmallForCurrentPlayersMessage())
		}
		if input.max > m.cfg.MaxPlayersLimit {
			return m.reply(ctx, event, maxPlayersMessage(m.cfg.MaxPlayersLimit))
		}
		if input.max < 2 {
			return m.reply(ctx, event, minimumPlayersMessage())
		}
		state.maxPlayers = input.max
		if err := m.editBoard(ctx, state, m.renderer.lobbyBoard(state, m.now(), maxUpdatedNote(input.max))); err != nil {
			return err
		}
	}
	if input.start {
		if state.joinedCount() < 2 {
			return m.reply(ctx, event, minimumPlayersMessage())
		}
		return m.startGame(ctx, state, hostStartNote())
	}

	return nil
}

func (m *Module) startGame(ctx context.Context, state *gameState, note string) error {
	state.start(m.shuffler.Shuffle(standardDeck), m.now().Add(m.cfg.GameTimeout))

	return m.editBoard(ctx, state, m.renderer.gameBoard(state, m.now(), note))
}

func (m *Module) cancelLobby(ctx context.Context, state *gameState, reason string) error {
	delete(m.games, state.key)

	return m.editBoard(ctx, state, m.renderer.conclusionBoard(state, conclusion{
		kind:     conclusionKindCanceled,
		title:    "决斗已取消。",
		subtitle: reason,
	}))
}

func (m *Module) expireConversationStateLocked(ctx context.Context, key conversationKey) error {
	state := m.games[key]
	if state == nil {
		return nil
	}

	return m.expireStateLocked(ctx, state)
}

func (m *Module) expireStateLocked(ctx context.Context, state *gameState) error {
	if state == nil || !state.expired(m.now()) {
		return nil
	}
	switch state.phase {
	case phaseLobby:
		if state.joinedCount() < 2 {
			return m.cancelLobby(ctx, state, lobbyTimeoutCancelNote())
		}
		return m.startGame(ctx, state, lobbyTimeoutStartNote())
	case phaseGame:
		return m.finishCurrentGame(ctx, state, state.timeoutConclusion(m.cfg.LoserMuteDuration))
	default:
		return nil
	}
}

func (m *Module) sweepExpiredGames(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, state := range m.games {
		if err := m.expireStateLocked(ctx, state); err != nil {
			return err
		}
	}

	return nil
}

func (m *Module) finishCurrentGame(ctx context.Context, state *gameState, result conclusion) error {
	delete(m.games, state.key)

	var finishErr error
	if err := m.editBoard(ctx, state, m.renderer.conclusionBoard(state, result)); err != nil {
		finishErr = err
	}

	if m.cfg.LoserMuteDuration <= 0 {
		return finishErr
	}
	untilDate := m.now().Add(m.cfg.LoserMuteDuration)
	for _, id := range state.loserOrder(result.loserReasons) {
		if err := m.moderation.RestrictMember(ctx, otogi.RestrictMemberRequest{
			Target:      state.target,
			MemberID:    id,
			Permissions: duelMutedPermissions(),
			UntilDate:   untilDate,
		}); err != nil {
			finishErr = errors.Join(finishErr, fmt.Errorf("restrict loser %s: %w", id, err))
		}
	}

	return finishErr
}

func (m *Module) editBoard(ctx context.Context, state *gameState, text string) error {
	if strings.TrimSpace(state.boardMessageID) == "" {
		if err := m.sendReplacementBoard(ctx, state, text); err != nil {
			return fmt.Errorf("duel send replacement board: %w", err)
		}

		return nil
	}

	if err := m.dispatcher.EditMessage(ctx, otogi.EditMessageRequest{
		Target:    state.target,
		MessageID: state.boardMessageID,
		Text:      text,
	}); err != nil {
		if resendErr := m.sendReplacementBoard(ctx, state, text); resendErr != nil {
			return fmt.Errorf(
				"duel edit board: %w",
				errors.Join(err, fmt.Errorf("send replacement board: %w", resendErr)),
			)
		}
	}

	return nil
}

func (m *Module) sendReplacementBoard(ctx context.Context, state *gameState, text string) error {
	message, err := m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target: state.target,
		Text:   text,
	})
	if err != nil {
		return fmt.Errorf("send board message: %w", err)
	}

	state.boardMessageID = message.ID

	return nil
}

func (m *Module) reply(ctx context.Context, event *otogi.Event, text string) error {
	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("duel derive reply target: %w", err)
	}

	if _, err := m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             text,
		ReplyToMessageID: event.Article.ID,
	}); err != nil {
		return fmt.Errorf("duel send reply: %w", err)
	}

	return nil
}

func parseDuelInput(command *otogi.CommandInvocation) (duelInput, error) {
	if command == nil {
		return duelInput{}, fmt.Errorf("%s", duelUsageMessage())
	}

	var input duelInput
	for _, option := range command.Options {
		switch option.Name {
		case "start":
			input.start = true
		case "cancel":
			input.cancel = true
		case "max":
			if !option.HasValue {
				return duelInput{}, fmt.Errorf("%s", maxRequiresValueMessage())
			}
			maxPlayers, err := strconv.Atoi(strings.TrimSpace(option.Value))
			if err != nil {
				return duelInput{}, fmt.Errorf("%s", maxMustBeIntegerMessage())
			}
			input.maxSet = true
			input.max = maxPlayers
		}
	}

	switch value := strings.ToLower(strings.TrimSpace(command.Value)); value {
	case "":
	case "start":
		input.start = true
	case "cancel":
		input.cancel = true
	default:
		maxPlayers, err := strconv.Atoi(value)
		if err != nil {
			return duelInput{}, fmt.Errorf("%s", duelUsageMessage())
		}
		input.maxSet = true
		input.max = maxPlayers
	}

	if input.start && input.cancel {
		return duelInput{}, fmt.Errorf("%s", conflictingControlMessage())
	}
	if input.maxSet && (input.start || input.cancel) {
		return duelInput{}, fmt.Errorf("%s", maxCannotMixWithControlsMessage())
	}

	return input, nil
}

func conversationKeyFromEvent(event *otogi.Event) conversationKey {
	if event == nil {
		return conversationKey{}
	}

	return conversationKey{
		tenantID:       event.TenantID,
		platform:       event.Source.Platform,
		sourceID:       event.Source.ID,
		conversationID: event.Conversation.ID,
	}
}

func actorName(actor otogi.Actor) string {
	if displayName := strings.TrimSpace(actor.DisplayName); displayName != "" {
		return displayName
	}
	if username := strings.TrimSpace(actor.Username); username != "" {
		return "@" + username
	}
	if id := strings.TrimSpace(actor.ID); id != "" {
		return "用户 " + id
	}

	return "神秘人"
}

func scoreHand(hand []card) int {
	total := 0
	aces := 0
	for _, card := range hand {
		switch {
		case card.rank == 1:
			total++
			aces++
		case card.rank >= 10:
			total += 10
		default:
			total += card.rank
		}
	}
	for aces > 0 && total+10 <= 21 {
		total += 10
		aces--
	}

	return total
}

func duelMutedPermissions() otogi.MemberPermissions {
	return otogi.MemberPermissions{
		SendMessages: false,
		SendMedia:    false,
		SendPolls:    false,
		SendOther:    false,
		EmbedLinks:   false,
		ChangeInfo:   true,
		InviteUsers:  true,
		PinMessages:  true,
		ManageTopics: true,
	}
}

var (
	_ otogi.Module          = (*Module)(nil)
	_ otogi.ModuleRegistrar = (*Module)(nil)
)
