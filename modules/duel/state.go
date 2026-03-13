package duel

import (
	"strings"
	"time"

	"ex-otogi/pkg/otogi/platform"
)

type conclusionKind string

const (
	conclusionKindCanceled conclusionKind = "canceled"
	conclusionKindTie      conclusionKind = "tie"
	conclusionKindBust     conclusionKind = "bust"
	conclusionKindScore    conclusionKind = "score"
	conclusionKindTimeout  conclusionKind = "timeout"
)

func newLobbyState(
	key conversationKey,
	target platform.OutboundTarget,
	host platform.Actor,
	invited *participantRef,
	maxPlayers int,
	deadline time.Time,
) *gameState {
	state := &gameState{
		key:        key,
		target:     target,
		phase:      phaseLobby,
		hostID:     host.ID,
		invited:    invited,
		maxPlayers: maxPlayers,
		deadline:   deadline,
		order:      []string{host.ID},
		players: map[string]*playerState{
			host.ID: newPlayer(host),
		},
	}

	return state
}

func newPlayer(actor platform.Actor) *playerState {
	return &playerState{
		ID:   actor.ID,
		Name: actorName(actor),
	}
}

func (s *gameState) player(id string) *playerState {
	if s == nil {
		return nil
	}

	return s.players[id]
}

func (s *gameState) playerName(id string) string {
	switch {
	case s == nil:
		return "神秘人"
	case s.players[id] != nil:
		return s.players[id].Name
	case s.invited != nil && s.invited.ID == id:
		return s.invited.Name
	default:
		return "神秘人"
	}
}

func (s *gameState) hasPlayer(id string) bool {
	return s.player(id) != nil
}

func (s *gameState) addPlayer(actor platform.Actor) *playerState {
	if s == nil {
		return nil
	}
	if player := s.player(actor.ID); player != nil {
		return player
	}

	player := newPlayer(actor)
	s.players[actor.ID] = player
	s.order = append(s.order, actor.ID)

	return player
}

func (s *gameState) invitedOnly() bool {
	return s != nil && s.invited != nil
}

func (s *gameState) joinedCount() int {
	if s == nil {
		return 0
	}

	return len(s.order)
}

func (s *gameState) atCapacity() bool {
	return s != nil && s.joinedCount() >= s.maxPlayers
}

func (s *gameState) start(deck []card, deadline time.Time) {
	if s == nil {
		return
	}

	s.phase = phaseGame
	s.deadline = deadline
	s.deck = append([]card(nil), deck...)
}

func (s *gameState) expired(now time.Time) bool {
	if s == nil || s.deadline.IsZero() {
		return false
	}

	return !now.Before(s.deadline)
}

func (s *gameState) drawCard() (card, bool) {
	if s == nil || len(s.deck) == 0 {
		return card{}, false
	}

	drawn := s.deck[0]
	s.deck = s.deck[1:]

	return drawn, true
}

func (s *gameState) hit(playerID string) (card, bool) {
	player := s.player(playerID)
	if !player.canAct() {
		return card{}, false
	}

	drawn, ok := s.drawCard()
	if !ok {
		return card{}, false
	}

	player.take(drawn)

	return drawn, true
}

func (s *gameState) stand(playerID string) bool {
	player := s.player(playerID)
	if !player.canAct() {
		return false
	}

	player.Stood = true

	return true
}

func (s *gameState) nonBustedPlayers() []string {
	ids := make([]string, 0, len(s.order))
	for _, id := range s.order {
		if player := s.player(id); player != nil && !player.Busted {
			ids = append(ids, id)
		}
	}

	return ids
}

func (s *gameState) bustedPlayers() []string {
	ids := make([]string, 0, len(s.order))
	for _, id := range s.order {
		if player := s.player(id); player != nil && player.Busted {
			ids = append(ids, id)
		}
	}

	return ids
}

func (s *gameState) allNonBustedFinished() bool {
	for _, id := range s.order {
		player := s.player(id)
		if player == nil || player.Busted {
			continue
		}
		if !player.Stood {
			return false
		}
	}

	return true
}

func (s *gameState) lowestScorePlayers() []string {
	lowestScore := -1
	losers := make([]string, 0, len(s.order))
	for _, id := range s.order {
		player := s.player(id)
		if player == nil || player.Busted {
			continue
		}

		switch {
		case lowestScore < 0 || player.Score < lowestScore:
			lowestScore = player.Score
			losers = []string{id}
		case player.Score == lowestScore:
			losers = append(losers, id)
		}
	}

	return losers
}

func (s *gameState) loserOrder(reasons map[string]string) []string {
	if len(reasons) == 0 {
		return nil
	}

	ordered := make([]string, 0, len(reasons))
	for _, id := range s.order {
		if _, exists := reasons[id]; exists {
			ordered = append(ordered, id)
		}
	}

	return ordered
}

func (s *gameState) loserNames(reasons map[string]string) string {
	names := make([]string, 0, len(reasons))
	for _, id := range s.loserOrder(reasons) {
		names = append(names, s.playerName(id))
	}

	return strings.Join(names, "、")
}

func (s *gameState) conclusionAfterAction(note string, muteDuration time.Duration) (conclusion, bool) {
	bustedPlayers := s.bustedPlayers()
	switch {
	case len(bustedPlayers) == len(s.order):
		return s.bustConclusion(allBustNote(), muteDuration), true
	case len(bustedPlayers) > 0 && len(s.nonBustedPlayers()) == 1:
		return s.bustConclusion(loneSurvivorNote(), muteDuration), true
	case s.allNonBustedFinished() && len(bustedPlayers) > 0:
		return s.bustConclusion(note, muteDuration), true
	case s.allNonBustedFinished():
		return s.scoreConclusion(note, muteDuration), true
	default:
		return conclusion{}, false
	}
}

func (s *gameState) scoreConclusion(note string, muteDuration time.Duration) conclusion {
	losers := s.lowestScorePlayers()
	if len(losers) == 0 {
		return conclusion{
			kind:     conclusionKindCanceled,
			title:    "决斗结束。",
			subtitle: note,
		}
	}
	if len(losers) == len(s.nonBustedPlayers()) {
		return conclusion{
			kind:     conclusionKindTie,
			title:    "决斗结束。",
			subtitle: tieSubtitle(),
		}
	}

	reasons := loserReasonMap(losers, "点数最低")
	return conclusion{
		kind:         conclusionKindScore,
		title:        "决斗结束。",
		subtitle:     note,
		footer:       footerWithMute(s.loserNames(reasons), muteDuration),
		loserReasons: reasons,
	}
}

func (s *gameState) bustConclusion(note string, muteDuration time.Duration) conclusion {
	reasons := loserReasonMap(s.bustedPlayers(), "爆牌")
	return conclusion{
		kind:         conclusionKindBust,
		title:        "决斗结束。",
		subtitle:     note,
		footer:       footerWithMute(s.loserNames(reasons), muteDuration),
		loserReasons: reasons,
	}
}

func (s *gameState) timeoutConclusion(muteDuration time.Duration) conclusion {
	reasons := loserReasonMap(s.bustedPlayers(), "爆牌")
	if reasons == nil {
		reasons = make(map[string]string)
	}
	for _, id := range s.order {
		player := s.player(id)
		if player != nil && player.canAct() {
			reasons[id] = "超时"
		}
	}

	return conclusion{
		kind:         conclusionKindTimeout,
		title:        "决斗结束。",
		subtitle:     timeoutNote(),
		footer:       footerWithMute(s.loserNames(reasons), muteDuration),
		loserReasons: reasons,
	}
}

func (p *playerState) take(card card) {
	if p == nil {
		return
	}

	p.hand = append(p.hand, card)
	p.Score = scoreHand(p.hand)
	p.Busted = p.Score > 21
	if p.Score == 21 {
		p.Stood = true
	}
}

func (p *playerState) canAct() bool {
	switch {
	case p == nil:
		return false
	case p.Busted || p.Stood:
		return false
	default:
		return p.Score < 21
	}
}

func (p *playerState) statusIcon() string {
	switch {
	case p == nil:
		return "⏳"
	case p.Busted:
		return "💥"
	case p.Score == 21:
		return "✅"
	case p.Stood:
		return "🏁"
	default:
		return "⏳"
	}
}

func (p *playerState) handText() string {
	if p == nil || len(p.hand) == 0 {
		return "还没出手"
	}

	labels := make([]string, 0, len(p.hand))
	for _, card := range p.hand {
		labels = append(labels, card.label)
	}

	return strings.Join(labels, " ")
}

func footerWithMute(loserNames string, muteDuration time.Duration) string {
	if loserNames == "" {
		return ""
	}
	if muteDuration <= 0 {
		return loserFooterNoMute(loserNames)
	}

	return loserFooter(loserNames)
}

func loserReasonMap(ids []string, reason string) map[string]string {
	if len(ids) == 0 {
		return nil
	}

	reasons := make(map[string]string, len(ids))
	for _, id := range ids {
		reasons[id] = reason
	}

	return reasons
}
