package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
)

var errInboxFailed = errors.New("telegram: inbox write failed earlier; restart required")

type inboxStore interface {
	InsertInbox(context.Context, string, []byte, time.Time) (bool, error)
}

// inboxHandler is the update manager's handler. gotd saves its position only
// after Handle returns, so persisting routed updates here makes them durable
// before gotd moves on.
//
// If a write fails, gotd would still advance its position in memory and
// skip the update. So the handler trips guard, which stops gotd from saving
// any further position, and reports the failure so the driver shuts down. On
// restart gotd resumes from the last saved position.
type inboxHandler struct {
	store inboxStore
	guard *positionGuard
	peers *peers.Manager // set before the client runs
	fail  func(error)
	wake  chan<- struct{}
	log   *slog.Logger
	now   func() time.Time
}

func (h *inboxHandler) Handle(ctx context.Context, u tg.UpdatesClass) error {
	if h.guard.tripped.Load() {
		return errInboxFailed
	}
	var batch []tg.UpdateClass
	var users []tg.UserClass
	var chats []tg.ChatClass
	switch v := u.(type) {
	case *tg.Updates:
		batch, users, chats = v.Updates, v.Users, v.Chats
	case *tg.UpdatesCombined:
		batch, users, chats = v.Updates, v.Users, v.Chats
	default:
		// Short forms only arrive before the manager starts; its startup
		// getDifference covers them.
		return nil
	}
	if h.peers != nil {
		if err := h.peers.Apply(ctx, users, chats); err != nil {
			h.log.Warn("telegram: save peers", "error", err)
		}
	}
	for _, update := range batch {
		box, pts, ok := mapper.Routed(update)
		if !ok {
			h.log.Debug("telegram: update kind not routed", "type", update.TypeName())
			continue
		}
		var buf bin.Buffer
		if err := update.Encode(&buf); err != nil {
			return h.trip(fmt.Errorf("encode %T: %w", update, err))
		}
		if _, err := h.store.InsertInbox(ctx, box+":"+strconv.Itoa(pts), buf.Buf, h.now()); err != nil {
			return h.trip(err)
		}
	}
	select {
	case h.wake <- struct{}{}:
	default:
	}
	return nil
}

func (h *inboxHandler) trip(err error) error {
	h.guard.tripped.Store(true)
	err = fmt.Errorf("telegram: persist update: %w", err)
	h.fail(err)
	return err
}

// positionGuard wraps gotd's state storage and refuses position writes once
// the inbox handler has failed, so a skipped update is fetched again after
// restart.
type positionGuard struct {
	updates.StateStorage
	tripped atomic.Bool
}

func (g *positionGuard) check() error {
	if g.tripped.Load() {
		return errInboxFailed
	}
	return nil
}

func (g *positionGuard) SetState(ctx context.Context, userID int64, state updates.State) error {
	if err := g.check(); err != nil {
		return err
	}
	return g.StateStorage.SetState(ctx, userID, state)
}

func (g *positionGuard) SetPts(ctx context.Context, userID int64, pts int) error {
	if err := g.check(); err != nil {
		return err
	}
	return g.StateStorage.SetPts(ctx, userID, pts)
}

func (g *positionGuard) SetQts(ctx context.Context, userID int64, qts int) error {
	if err := g.check(); err != nil {
		return err
	}
	return g.StateStorage.SetQts(ctx, userID, qts)
}

func (g *positionGuard) SetDate(ctx context.Context, userID int64, date int) error {
	if err := g.check(); err != nil {
		return err
	}
	return g.StateStorage.SetDate(ctx, userID, date)
}

func (g *positionGuard) SetSeq(ctx context.Context, userID int64, seq int) error {
	if err := g.check(); err != nil {
		return err
	}
	return g.StateStorage.SetSeq(ctx, userID, seq)
}

func (g *positionGuard) SetDateSeq(ctx context.Context, userID int64, date, seq int) error {
	if err := g.check(); err != nil {
		return err
	}
	return g.StateStorage.SetDateSeq(ctx, userID, date, seq)
}

func (g *positionGuard) SetChannelPts(ctx context.Context, userID, channelID int64, pts int) error {
	if err := g.check(); err != nil {
		return err
	}
	return g.StateStorage.SetChannelPts(ctx, userID, channelID, pts)
}
