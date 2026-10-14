package kernel

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// timerRetry is how long a failed timer run waits before its next attempt.
const timerRetry = time.Minute

func set(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// startOrder orders modules so every provider of a service starts before the
// modules requiring it, breaking ties by module ID. It rejects duplicate
// providers, missing providers, and cycles.
func startOrder(entries []*moduleEntry) ([]*moduleEntry, error) {
	providers := map[string]*moduleEntry{}
	for _, entry := range entries {
		for name := range entry.provides {
			if other := providers[name]; other != nil {
				return nil, fmt.Errorf("service %q provided by both %q and %q: %w", name, other.descriptor.ID, entry.descriptor.ID, otogi.ErrInvalid)
			}
			providers[name] = entry
		}
	}
	for _, entry := range entries {
		for name := range entry.requires {
			if providers[name] == nil {
				return nil, fmt.Errorf("module %q requires service %q, which no module provides: %w", entry.descriptor.ID, name, otogi.ErrInvalid)
			}
		}
	}
	var order []*moduleEntry
	placed := map[*moduleEntry]bool{}
	for len(order) < len(entries) {
		progressed := false
		for _, entry := range entries { // entries are sorted by ID
			if placed[entry] {
				continue
			}
			ready := true
			for name := range entry.requires {
				if !placed[providers[name]] { // requiring your own service never becomes ready: a cycle
					ready = false
				}
			}
			if ready {
				order, placed[entry], progressed = append(order, entry), true, true
				break
			}
		}
		if !progressed {
			var stuck []otogi.ModuleID
			for _, entry := range entries {
				if !placed[entry] {
					stuck = append(stuck, entry.descriptor.ID)
				}
			}
			return nil, fmt.Errorf("service dependency cycle among %v: %w", stuck, otogi.ErrInvalid)
		}
	}
	return order, nil
}

// job is a scheduled module job. next and pending are owned by the clock.
type job struct {
	entry    *moduleEntry
	name     string
	schedule cron.Schedule
	fn       otogi.Job
	next     time.Time
	pending  atomic.Bool // a run is queued or running; the next one is skipped
}

// clock fires due jobs and timers once a second until the kernel stops.
func (k *Kernel) clock() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-k.runCtx.Done():
			return
		case now := <-ticker.C:
			k.tick(now)
		}
	}
}

func (k *Kernel) tick(now time.Time) {
	if !k.ready() {
		return // draining: no new runs
	}
	for _, j := range k.jobs {
		if j.next.After(now) {
			continue
		}
		at := j.next
		j.next = j.schedule.Next(now)
		k.runJob(j, at)
	}
	if k.cfg.Storage == nil {
		return
	}
	var handlers []otogi.ModuleID
	for _, entry := range k.modules {
		if _, ok := entry.module.(otogi.TimerHandler); ok {
			handlers = append(handlers, entry.descriptor.ID)
		}
	}
	if len(handlers) == 0 {
		return
	}
	err := k.cfg.Storage.DueTimers(k.runCtx, now, handlers, func(module otogi.ModuleID, t otogi.Timer) { k.runTimer(module, t) })
	if err != nil && k.runCtx.Err() == nil {
		k.log.Error("timers: load due timers", "error", err)
	}
}

func (k *Kernel) runJob(j *job, at time.Time) {
	id := j.entry.descriptor.ID
	if j.pending.Swap(true) {
		k.log.Warn("job skipped: previous run still pending", "module", id, "job", j.name)
		return
	}
	d := &delivery{
		module:   j.entry,
		lane:     laneKey{module: id, conversation: otogi.ConversationID("job:" + j.name)},
		origin:   otogi.Origin{Job: j.name, ScheduledAt: at},
		originID: fmt.Sprintf("job:%s:%s:%d", id, j.name, at.Unix()),
		call:     func(ctx context.Context, inv *invocation) error { return j.fn(ctx, inv) },
		done:     func(otogi.ModuleOutcome) { j.pending.Store(false) },
	}
	if !k.enqueueWork(d) {
		j.pending.Store(false)
	}
}

func (k *Kernel) runTimer(module otogi.ModuleID, t otogi.Timer) {
	entry := k.byID[module]
	handler := entry.module.(otogi.TimerHandler)
	running := string(module) + "\x00" + t.Key
	k.mu.Lock()
	if k.timers[running] {
		k.mu.Unlock()
		return
	}
	k.timers[running] = true
	k.mu.Unlock()
	timer := t
	d := &delivery{
		module:   entry,
		lane:     laneKey{module: module, conversation: otogi.ConversationID("timer:" + t.Key)},
		origin:   otogi.Origin{Timer: &timer, ScheduledAt: t.At},
		originID: fmt.Sprintf("timer:%s:%s:%d", module, t.Key, t.At.UnixMilli()),
		call:     func(ctx context.Context, inv *invocation) error { return handler.HandleTimer(ctx, timer, inv) },
		done:     func(o otogi.ModuleOutcome) { k.finishTimer(module, timer, running, o) },
	}
	if !k.enqueueWork(d) {
		k.mu.Lock()
		delete(k.timers, running)
		k.mu.Unlock()
	}
}

// finishTimer deletes a timer after a successful run and postpones a failed
// one. A run abandoned because the kernel stopped is left due, so it fires
// on the next start.
func (k *Kernel) finishTimer(module otogi.ModuleID, t otogi.Timer, running string, o otogi.ModuleOutcome) {
	defer func() {
		k.mu.Lock()
		delete(k.timers, running)
		k.mu.Unlock()
	}()
	if k.runCtx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var err error
	if o.Err == nil {
		err = k.cfg.Storage.FinishTimer(ctx, module, t.Key, t.At)
	} else {
		err = k.cfg.Storage.RetryTimer(ctx, module, t.Key, t.At, time.Now().Add(timerRetry))
	}
	if err != nil {
		k.log.Error("timers: update after run", "module", module, "timer", t.Key, "error", err)
	}
}

// enqueueWork queues a job or timer delivery unless the kernel is draining or
// stopped.
func (k *Kernel) enqueueWork(d *delivery) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.state != stateReady {
		return false
	}
	k.sched.enqueue(d)
	return true
}
