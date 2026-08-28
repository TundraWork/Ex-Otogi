package updates

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"sync"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

type journalCorrelation struct {
	mu        sync.Mutex
	byUpdate  map[[32]byte][]int64
	remaining map[int64]int
	order     []int64
}

func newJournalCorrelation() *journalCorrelation {
	return &journalCorrelation{byUpdate: make(map[[32]byte][]int64), remaining: make(map[int64]int)}
}

func (c *journalCorrelation) register(id int64, packet tg.UpdatesClass) error {
	updates := packetUpdates(packet)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.remaining[id]; !exists {
		c.order = append(c.order, id)
	}
	for _, update := range updates {
		key, err := correlationKey(update)
		if err != nil {
			return err
		}
		c.byUpdate[key] = append(c.byUpdate[key], id)
		c.remaining[id]++
	}
	return nil
}

func (c *journalCorrelation) claim(update tg.UpdateClass) (int64, bool) {
	key, err := correlationKey(update)
	if err != nil {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := c.byUpdate[key]
	if len(ids) == 0 {
		return 0, false
	}
	id := ids[0]
	if len(ids) == 1 {
		delete(c.byUpdate, key)
	} else {
		c.byUpdate[key] = ids[1:]
	}
	c.remaining[id]--
	complete := c.remaining[id] == 0
	if complete {
		delete(c.remaining, id)
	}
	if complete {
		c.removeOrder(id)
	}
	return id, complete
}

func (c *journalCorrelation) has(update tg.UpdateClass) bool {
	key, err := correlationKey(update)
	if err != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byUpdate[key]) > 0
}

func (c *journalCorrelation) claimSuperseded() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := append([]int64(nil), c.order...)
	c.order = nil
	clear(c.remaining)
	clear(c.byUpdate)
	return ids
}

func (c *journalCorrelation) removeOrder(id int64) {
	for i, candidate := range c.order {
		if candidate == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

func packetUpdates(packet tg.UpdatesClass) []tg.UpdateClass {
	switch value := packet.(type) {
	case *tg.Updates:
		return value.Updates
	case *tg.UpdatesCombined:
		return value.Updates
	case *tg.UpdateShort:
		return []tg.UpdateClass{value.Update}
	case *tg.UpdateShortMessage:
		return []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.Message{ID: value.ID}, Pts: value.Pts, PtsCount: value.PtsCount}}
	case *tg.UpdateShortChatMessage:
		return []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.Message{ID: value.ID}, Pts: value.Pts, PtsCount: value.PtsCount}}
	case *tg.UpdateShortSentMessage:
		return []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.MessageEmpty{ID: value.ID}, Pts: value.Pts, PtsCount: value.PtsCount}}
	default:
		return nil
	}
}

func correlationKey(update tg.UpdateClass) ([32]byte, error) {
	var kind byte
	var messageID, pts int
	if value, ok := update.(*tg.UpdateNewMessage); ok {
		kind, pts = 1, value.Pts
		if message, ok := value.Message.(interface{ GetID() int }); ok {
			messageID = message.GetID()
		}
	}
	if kind != 0 && messageID > 0 {
		var identity [17]byte
		identity[0] = kind
		binary.LittleEndian.PutUint64(identity[1:9], uint64(messageID))
		binary.LittleEndian.PutUint64(identity[9:17], uint64(pts))
		return sha256.Sum256(identity[:]), nil
	}
	payload, err := encodeTL(update)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

type correlatingHandler struct {
	correlation *journalCorrelation
	next        telegram.UpdateHandler
}

func (h correlatingHandler) Handle(ctx context.Context, packet tg.UpdatesClass) error {
	updates := packetUpdates(packet)
	if len(updates) == 0 {
		ids := h.correlation.claimSuperseded()
		for _, id := range ids {
			completed := context.WithValue(ctx, journalContextKey{}, id)
			completed = context.WithValue(completed, journalCompleteKey{}, true)
			if err := h.next.Handle(completed, &tg.Updates{}); err != nil {
				return err
			}
		}
		if len(ids) == 0 {
			return h.next.Handle(ctx, packet)
		}
		return nil
	}
	matched := false
	for _, update := range updates {
		if h.correlation.has(update) {
			matched = true
			break
		}
	}
	if !matched {
		ids := h.correlation.claimSuperseded()
		for _, id := range ids[:max(0, len(ids)-1)] {
			completed := context.WithValue(ctx, journalContextKey{}, id)
			completed = context.WithValue(completed, journalCompleteKey{}, true)
			if err := h.next.Handle(completed, &tg.Updates{}); err != nil {
				return err
			}
		}
		if len(ids) > 0 {
			ctx = context.WithValue(ctx, journalContextKey{}, ids[len(ids)-1])
			ctx = context.WithValue(ctx, journalCompleteKey{}, true)
		}
		return h.next.Handle(ctx, packet)
	}
	type delivery struct {
		id       int64
		complete bool
		updates  []tg.UpdateClass
	}
	deliveries := make([]delivery, 0, len(updates))
	for _, update := range updates {
		id, complete := h.correlation.claim(update)
		if len(deliveries) > 0 && deliveries[len(deliveries)-1].id == id {
			last := &deliveries[len(deliveries)-1]
			last.updates = append(last.updates, update)
			last.complete = complete
			continue
		}
		deliveries = append(deliveries, delivery{id: id, complete: complete, updates: []tg.UpdateClass{update}})
	}
	users, chats := packetEntities(packet)
	for _, delivery := range deliveries {
		deliveryCtx := ctx
		if delivery.id > 0 {
			deliveryCtx = context.WithValue(deliveryCtx, journalContextKey{}, delivery.id)
			deliveryCtx = context.WithValue(deliveryCtx, journalCompleteKey{}, delivery.complete)
		}
		if err := h.next.Handle(deliveryCtx, &tg.Updates{Updates: delivery.updates, Users: users, Chats: chats}); err != nil {
			return err
		}
	}
	return nil
}

func packetEntities(packet tg.UpdatesClass) ([]tg.UserClass, []tg.ChatClass) {
	switch value := packet.(type) {
	case *tg.Updates:
		return value.Users, value.Chats
	case *tg.UpdatesCombined:
		return value.Users, value.Chats
	default:
		return nil, nil
	}
}
