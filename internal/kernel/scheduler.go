package kernel

import (
	"sync"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// delivery is one event for one module.
type delivery struct {
	event  *tracked
	module *moduleEntry
	index  int
}

// laneKey groups deliveries that must run one at a time, in order.
type laneKey struct {
	module       otogi.ModuleID
	platform     otogi.PlatformID
	account      otogi.AccountID
	conversation otogi.ConversationID
	unordered    uint64 // nonzero gives OrderNone deliveries a lane of their own
}

// lane is a FIFO of deliveries. A lane present in scheduler.lanes is either
// waiting in the ready queue or being run by exactly one worker, so its
// deliveries never run concurrently.
type lane struct {
	key   laneKey
	queue []*delivery
}

// scheduler hands runnable lanes to workers. All fields are guarded by the
// owning Kernel's mu, which cond also uses.
type scheduler struct {
	cond     *sync.Cond
	lanes    map[laneKey]*lane
	ready    []*lane
	sequence uint64
	stopping bool
}

func (s *scheduler) init(mu *sync.Mutex) {
	s.cond = sync.NewCond(mu)
	s.lanes = make(map[laneKey]*lane)
}

func (s *scheduler) enqueue(d *delivery) {
	e := d.event.event
	key := laneKey{module: d.module.descriptor.ID, platform: e.Platform, account: e.Account, conversation: e.Conversation.ID}
	if d.module.descriptor.Ordering == otogi.OrderNone {
		s.sequence++
		key = laneKey{module: d.module.descriptor.ID, unordered: s.sequence}
	}
	if l, ok := s.lanes[key]; ok {
		l.queue = append(l.queue, d)
		return
	}
	l := &lane{key: key, queue: []*delivery{d}}
	s.lanes[key] = l
	s.ready = append(s.ready, l)
	s.cond.Signal()
}

func (s *scheduler) stop() {
	s.stopping = true
	s.cond.Broadcast()
}

// work runs lanes until the kernel stops and no runnable lane remains. After
// Stop, queued deliveries still complete, without running their modules, so
// every admitted event reports an outcome.
func (k *Kernel) work() {
	k.mu.Lock()
	defer k.mu.Unlock()
	for {
		for len(k.sched.ready) == 0 && !k.sched.stopping {
			k.sched.cond.Wait()
		}
		if len(k.sched.ready) == 0 {
			return
		}
		l := k.sched.ready[0]
		k.sched.ready = k.sched.ready[1:]
		d := l.queue[0]
		k.mu.Unlock()
		k.run(d)
		k.mu.Lock()
		l.queue = l.queue[1:]
		if len(l.queue) == 0 {
			delete(k.sched.lanes, l.key)
		} else {
			k.sched.ready = append(k.sched.ready, l)
			k.sched.cond.Signal()
		}
	}
}
