package duel

import (
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"
)

var (
	survivorEmojiSet = []string{"😄", "🤣", "🥰", "😝"}
	bustEmojiSet     = []string{"☢️", "🤡", "☠️", "💩"}
	winnerEmojiSet   = []string{"🥇", "🎉", "🎊", "🆒"}
	loserEmojiSet    = []string{"🌚", "🏳", "😭", "😵"}
	tieEmojiSet      = []string{"🙂", "🙃"}
)

type emojiPicker interface {
	Pick(options []string) string
}

type cryptoEmojiPicker struct{}

func newRandomEmojiPicker() *cryptoEmojiPicker {
	return &cryptoEmojiPicker{}
}

func (p *cryptoEmojiPicker) Pick(options []string) string {
	if len(options) == 0 {
		return ""
	}
	if p == nil || len(options) == 1 {
		return options[0]
	}

	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(options))))
	if err != nil {
		return options[0]
	}

	return options[index.Int64()]
}

type renderer struct {
	picker emojiPicker
}

func newRenderer(picker emojiPicker) *renderer {
	if picker == nil {
		picker = newRandomEmojiPicker()
	}

	return &renderer{picker: picker}
}

func (r *renderer) lobbyBoard(state *gameState, now time.Time, note string) string {
	lines := make([]string, 0, len(state.order)+8)
	lines = append(lines, fmt.Sprintf("对局等待中，还剩 %d 秒。", secondsRemaining(state.deadline, now)))
	if note != "" {
		lines = append(lines, note)
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("房主：%s", state.playerName(state.hostID)))
	if state.invitedOnly() {
		lines = append(lines, fmt.Sprintf("邀请对象：%s", state.invited.Name))
	}
	lines = append(lines, fmt.Sprintf("人数：%d/%d", state.joinedCount(), state.maxPlayers))
	lines = append(lines, "")
	lines = append(lines, "玩家：")
	for _, id := range state.order {
		lines = append(lines, fmt.Sprintf("• %s", state.playerName(id)))
	}
	lines = append(lines, "")
	lines = append(lines, "加入 /duelJoin")
	if state.joinedCount() >= 2 {
		lines = append(lines, "开始 /duel --start")
	}
	lines = append(lines, "取消 /duel --cancel")

	return strings.Join(lines, "\n")
}

func (r *renderer) gameBoard(state *gameState, now time.Time, note string) string {
	lines := make([]string, 0, len(state.order)+6)
	lines = append(lines, fmt.Sprintf("对局进行中，请在 %d 秒内完成出牌哦~", secondsRemaining(state.deadline, now)))
	if note != "" {
		lines = append(lines, note)
	}
	lines = append(lines, "")
	for _, id := range state.order {
		lines = append(lines, r.playerLine(state.player(id), state.player(id).statusIcon(), ""))
	}
	lines = append(lines, "")
	lines = append(lines, "摸牌 /duelHit | 停牌 /duelStand")

	return strings.Join(lines, "\n")
}

func (r *renderer) conclusionBoard(state *gameState, result conclusion) string {
	lines := make([]string, 0, len(state.order)+6)
	lines = append(lines, result.title)
	if result.subtitle != "" {
		lines = append(lines, result.subtitle)
	}
	lines = append(lines, "")
	for _, id := range state.order {
		player := state.player(id)
		lines = append(lines, r.playerLine(player, r.playerEmoji(result, id), result.loserReasons[id]))
	}
	if result.footer != "" {
		lines = append(lines, "")
		lines = append(lines, result.footer)
	}

	return strings.Join(lines, "\n")
}

func (r *renderer) playerLine(player *playerState, emoji string, note string) string {
	if player == nil {
		return ""
	}

	line := fmt.Sprintf("%s %s [%d] [%s]", emoji, player.Name, player.Score, player.handText())
	if strings.TrimSpace(note) != "" {
		line += " · " + note
	}

	return line
}

func (r *renderer) playerEmoji(result conclusion, playerID string) string {
	switch result.kind {
	case conclusionKindBust:
		if _, loser := result.loserReasons[playerID]; loser {
			return r.pick(bustEmojiSet)
		}
		return r.pick(survivorEmojiSet)
	case conclusionKindTie:
		return r.pick(tieEmojiSet)
	case conclusionKindScore, conclusionKindTimeout:
		if _, loser := result.loserReasons[playerID]; loser {
			return r.pick(loserEmojiSet)
		}
		return r.pick(winnerEmojiSet)
	case conclusionKindCanceled:
		return r.pick(tieEmojiSet)
	default:
		return "🙂"
	}
}

func (r *renderer) pick(options []string) string {
	if r == nil || r.picker == nil {
		if len(options) == 0 {
			return ""
		}
		return options[0]
	}

	return r.picker.Pick(options)
}

func secondsRemaining(deadline time.Time, now time.Time) int {
	if deadline.IsZero() {
		return 0
	}

	remaining := deadline.Sub(now).Seconds()
	if remaining <= 0 {
		return 0
	}

	return int(math.Ceil(remaining))
}
