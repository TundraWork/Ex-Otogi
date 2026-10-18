// Package todou forwards activity from a todou issue tracker to chats.
// Projects are linked to chats with /todou commands. A resident `todou
// watch` process streams new activity into a queue per chat, and each chat
// gets the queue as one digest at most once per min_interval, through a
// transactional outbox: in order and exactly once per chat. See
// docs/superpowers/specs/2026-10-16-todou-notifier-design.md.
package todou

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config configures the module; see otogi.example.yaml.
type Config struct {
	// CLI is the path of the todou CLI binary.
	CLI string `yaml:"cli"`
	// Server is the todou server URL.
	Server string `yaml:"server"`
	// Token is the agent account's personal access token (todou_pat_…).
	Token string `yaml:"token"`
	// LinkBase is the web URL issue links point to; default Server.
	LinkBase string `yaml:"link_base"`
	// MinInterval is the least time between two digests to one chat;
	// activity meanwhile waits and goes out together. Default 5m. A chat can
	// set its own with /todou cooldown.
	MinInterval time.Duration `yaml:"min_interval"`
	// Debounce holds a burst of activity together for this long after its
	// first entry before the watcher hands it over; default 10s.
	Debounce time.Duration `yaml:"debounce"`
	// CLITimeout bounds each one-shot CLI call; default 60s.
	CLITimeout time.Duration `yaml:"cli_timeout"`
	// Events lists the activity types to notify; nil means DefaultEvents, an
	// empty list means every type.
	Events []string `yaml:"events"`
	// MaxBodyChars cuts each quoted body; default 800, at most 3000. Quotes
	// are collapsed, so they cost little room on screen, but they count
	// toward Telegram's 4096 per message: 800 keeps about four entries in
	// one message.
	MaxBodyChars int `yaml:"max_body_chars"`
	// MaxAttempts is how many times a digest message is tried, a minute
	// apart, before it is marked failed; default 60.
	MaxAttempts int `yaml:"max_attempts"`
}

// DefaultEvents are the activity types notified when Config.Events is unset.
var DefaultEvents = []string{"comment", "opened", "closed", "reopened", "status_changed", "assigned", "spec_pushed", "spec_review", "question_answered"}

const (
	outboxRetention = 30 * 24 * time.Hour
	// A failed watcher restarts after restartMin, doubling up to restartMax;
	// one that ran for restartMax starts over at restartMin.
	restartMin = 5 * time.Second
	restartMax = 5 * time.Minute
)

// Module is the todou notifier.
type Module struct {
	cfg       Config
	operators []string
	cli       client
	render    renderer
	now       func() time.Time

	host  otogi.Host
	log   *slog.Logger
	store *store
	stop  context.CancelFunc
	done  chan struct{} // closed when the watcher has exited

	mu      sync.Mutex // guards watcher
	watcher watcherState
}

// watcherState is what /todou status shows about the watcher.
type watcherState struct {
	since    time.Time // the running CLI started
	err      string    // why the last one stopped, if it did
	failedAt time.Time
}

// New validates cfg and returns the module. operators are the
// platform-qualified actor IDs allowed to link and unlink projects; the
// account's own messages always are.
func New(cfg Config, operators []string) (*Module, error) {
	var errs []error
	if cfg.CLI == "" || cfg.Server == "" || cfg.Token == "" {
		errs = append(errs, errors.New("cli, server, and token are required"))
	}
	if cfg.MaxBodyChars < 0 || cfg.MaxBodyChars > 3000 {
		errs = append(errs, errors.New("max_body_chars must be between 1 and 3000"))
	}
	if cfg.MinInterval < 0 || cfg.Debounce < 0 || cfg.CLITimeout < 0 || cfg.MaxAttempts < 0 {
		errs = append(errs, errors.New("min_interval, debounce, cli_timeout, and max_attempts must not be negative"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if cfg.LinkBase == "" {
		cfg.LinkBase = cfg.Server
	}
	cfg.LinkBase = strings.TrimSuffix(cfg.LinkBase, "/")
	if cfg.MinInterval == 0 {
		cfg.MinInterval = 5 * time.Minute
	}
	if cfg.Debounce == 0 {
		cfg.Debounce = 10 * time.Second
	}
	if cfg.CLITimeout == 0 {
		cfg.CLITimeout = 60 * time.Second
	}
	if cfg.Events == nil {
		cfg.Events = DefaultEvents
	}
	if cfg.MaxBodyChars == 0 {
		cfg.MaxBodyChars = 800
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 60
	}
	return &Module{
		cfg: cfg, operators: operators,
		cli:    client{run: execRunner(cfg.CLI, cfg.Server, cfg.Token, cfg.CLITimeout), stream: execStreamer(cfg.CLI, cfg.Server, cfg.Token)},
		render: renderer{linkBase: cfg.LinkBase, maxBody: cfg.MaxBodyChars},
		now:    time.Now,
	}, nil
}

// Descriptor declares /todou. The account's owner may use it too; no reply
// starts with a command prefix, so replies never trigger it.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{
		ID: "todou",
		Commands: []otogi.CommandSpec{{
			Name: "todou", Usage: "link <project> | unlink <project> | list | cooldown [<duration> | default] | status",
			Description: "post a todou project's activity to this chat",
		}},
		Filter: otogi.Filter{Directions: []otogi.Direction{otogi.DirectionIncoming, otogi.DirectionOutgoing}},
	}
}

// Start opens the module's database and starts the watcher.
func (m *Module) Start(ctx context.Context, host otogi.Host) error {
	db, err := host.Database(ctx)
	if err != nil {
		return err
	}
	if m.store, err = openStore(ctx, db); err != nil {
		return err
	}
	m.host, m.log = host, host.Logger()
	ctx, m.stop = context.WithCancel(context.WithoutCancel(ctx))
	m.done = make(chan struct{})
	go m.watch(ctx)
	return nil
}

// Stop stops the watcher; activity it has not handed over yet is read again
// on the next start.
func (m *Module) Stop(ctx context.Context) error {
	if m.stop == nil {
		return nil
	}
	m.stop()
	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("todou: stop watcher: %w", ctx.Err())
	}
}

// watch runs the watcher until ctx ends, restarting it with backoff when it
// fails.
func (m *Module) watch(ctx context.Context) {
	defer close(m.done)
	delay := restartMin
	for {
		started := time.Now()
		err := m.runWatcher(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) >= restartMax {
			delay = restartMin
		}
		m.watcherFailed(ctx, err, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(2*delay, restartMax)
	}
}

// runWatcher arms the digest timer of every chat with a backlog (a crash or
// a failed SetTimer may have left one unarmed), then follows activity from
// the stored cursor, or from now on a first start.
func (m *Module) runWatcher(ctx context.Context) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("todou watcher panicked: %v\n%s", p, debug.Stack())
		}
	}()
	backlog, err := m.store.backlog(ctx)
	if err != nil {
		return err
	}
	for _, to := range backlog {
		if err := m.arm(ctx, to, m.now()); err != nil {
			return err
		}
	}
	cursor, err := m.store.cursor(ctx)
	if err != nil {
		return err
	}
	if cursor == "" {
		if cursor, err = m.cli.now(ctx); err != nil {
			return err
		}
		if err := m.store.ingest(ctx, cursor, nil, m.now()); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.watcher.since = m.now()
	m.mu.Unlock()
	return m.cli.follow(ctx, cursor, m.cfg.Events, m.cfg.Debounce,
		func(line string) { m.log.Info("todou watch: " + line) },
		func(entries []entry, cursor string) error { return m.ingest(ctx, entries, cursor) })
}

func (m *Module) watcherFailed(ctx context.Context, err error, retry time.Duration) {
	m.mu.Lock()
	m.watcher = watcherState{err: err.Error(), failedAt: m.now()}
	m.mu.Unlock()
	m.log.Error("todou: watcher failed", "error", err, "retry_in", retry)
	m.host.Record(ctx, otogi.Record{Kind: "todou.watch_failed", Outcome: "error", Payload: map[string]any{"error": err.Error(), "retry_in": retry.String()}})
}

// ingest queues a batch for the chats linked to its projects, with the
// cursor after it, and arms those chats' digest timers.
func (m *Module) ingest(ctx context.Context, entries []entry, cursor string) error {
	links, err := m.store.links(ctx, nil)
	if err != nil {
		return err
	}
	byProject := map[string][]otogi.Destination{}
	for _, l := range links {
		byProject[l.Project] = append(byProject[l.Project], l.To)
	}
	var rows []queued
	var chats []otogi.Destination
	for _, e := range entries {
		for _, to := range byProject[e.Project] {
			rows = append(rows, queued{To: to, Entry: e})
			if !slices.Contains(chats, to) {
				chats = append(chats, to)
			}
		}
	}
	now := m.now()
	if err := m.store.ingest(ctx, cursor, rows, now); err != nil {
		return err
	}
	for _, to := range chats {
		if err := m.arm(ctx, to, now); err != nil {
			return err
		}
	}
	return nil
}

// arm sets a chat's digest timer to at. The timer carries the chat.
func (m *Module) arm(ctx context.Context, to otogi.Destination, at time.Time) error {
	payload, err := json.Marshal(to)
	if err != nil {
		return fmt.Errorf("todou: encode timer: %w", err)
	}
	return m.host.SetTimer(ctx, "digest:"+string(payload), at, payload)
}

// HandleTimer runs a chat's digest timer: it finishes delivering the last
// digest, then, if entries are queued and min_interval has passed since the
// last digest, turns them into the next one and delivers it; if it has not
// passed, it re-arms the timer for when it will. The watcher arms the timer
// whenever entries arrive.
func (m *Module) HandleTimer(ctx context.Context, t otogi.Timer, inv otogi.Invocation) error {
	var to otogi.Destination
	if err := json.Unmarshal(t.Payload, &to); err != nil {
		return fmt.Errorf("todou: timer %q: %w", t.Key, err)
	}
	if err := m.deliver(ctx, inv, to); err != nil {
		return err
	}
	queue, err := m.store.queue(ctx, to)
	if err != nil || len(queue) == 0 {
		return err
	}
	last, err := m.store.lastSent(ctx, to)
	if err != nil {
		return err
	}
	cooldown, _, err := m.cooldown(ctx, to)
	if err != nil {
		return err
	}
	if due := last.Add(cooldown); m.now().Before(due) {
		return m.arm(ctx, to, due)
	}
	entries := make([]entry, len(queue))
	for i, q := range queue {
		entries[i] = q.Entry
	}
	now := m.now()
	if err := m.store.digest(ctx, to, queue[len(queue)-1].ID, m.render.digest(entries, m.issues(ctx, entries)), now); err != nil {
		return err
	}
	if err := m.store.prune(ctx, now.Add(-outboxRetention)); err != nil {
		m.log.Warn("todou: prune outbox", "error", err)
	}
	return m.deliver(ctx, inv, to)
}

func titleKey(project string, number int64) string { return fmt.Sprintf("%s#%d", project, number) }

// issues looks up the title (and, for opened issues, the body) of every
// issue the entries mention. A failed lookup leaves the title out.
func (m *Module) issues(ctx context.Context, entries []entry) map[string]issue {
	out := map[string]issue{}
	for _, e := range entries {
		key := titleKey(e.Project, e.IssueNumber)
		if _, done := out[key]; done {
			continue
		}
		i, err := m.cli.issue(ctx, e.Project, e.IssueNumber)
		if err != nil {
			m.log.Warn("todou: issue lookup failed; notifying without the title", "issue", key, "error", err)
		}
		out[key] = i
	}
	return out
}

// deliver sends a chat's undelivered digest messages in order. A failure
// stops there and fails the timer run, which the kernel retries a minute
// later, so the chat's messages stay in order; a message that has failed
// MaxAttempts times is marked failed and recorded, and delivery moves on.
// Each message's effect is keyed by its row, so a message resent after a
// crash is not delivered twice.
func (m *Module) deliver(ctx context.Context, inv otogi.Invocation, to otogi.Destination) error {
	rows, err := m.store.pending(ctx, to)
	if err != nil {
		return err
	}
	for _, r := range rows {
		_, err := inv.WithOrigin(fmt.Sprintf("outbox:%d", r.ID)).SendTo(ctx, "notify", r.To, r.Message)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			if err := m.store.markSent(ctx, r.ID, m.now()); err != nil {
				return err
			}
			continue
		}
		failed, recordErr := m.store.attemptFailed(ctx, r.ID, err, m.cfg.MaxAttempts, m.now())
		if recordErr != nil {
			return recordErr
		}
		if !failed {
			return fmt.Errorf("todou: deliver row %d (attempt %d of %d): %w", r.ID, r.Attempts+1, m.cfg.MaxAttempts, err)
		}
		m.log.Error("todou: giving up on a notification", "row", r.ID, "conversation", r.To.Conversation, "error", err)
		m.host.Record(ctx, otogi.Record{Kind: "todou.delivery_failed", Outcome: "error", Platform: r.To.Platform, Account: r.To.Account, Conversation: r.To.Conversation,
			Payload: map[string]any{"row": r.ID, "attempts": r.Attempts + 1, "error": err.Error(), "message": r.Message}})
	}
	return nil
}

// Handle answers /todou commands.
func (m *Module) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	verb, arg, _ := strings.Cut(e.Command.Args, " ")
	arg = strings.TrimSpace(arg)
	var reply string
	var err error
	switch verb {
	case "link", "unlink":
		switch {
		case !m.operator(e):
			reply = fmt.Sprintf("Only operators can change links. Your ID is %s.", e.ActorKey())
		case arg == "":
			reply = fmt.Sprintf("Usage: /todou %s <project>", verb)
		case verb == "link":
			reply, err = m.link(ctx, e, arg)
		default:
			reply, err = m.unlink(ctx, e, arg)
		}
	case "list":
		reply, err = m.list(ctx, e)
	case "cooldown":
		reply, err = m.setCooldown(ctx, e, arg)
	case "status":
		reply, err = m.status(ctx, e)
	default:
		reply = "Usage: /todou link <project> | unlink <project> | list | cooldown [<duration> | default] | status"
	}
	if err != nil {
		return err
	}
	_, err = inv.Send(ctx, "reply", otogi.Reply(reply, e.Ref()))
	return err
}

// cooldown is a chat's cooldown between digests and whether the chat set it
// rather than using min_interval.
func (m *Module) cooldown(ctx context.Context, to otogi.Destination) (time.Duration, bool, error) {
	d, set, err := m.store.cooldown(ctx, to)
	if err != nil || !set {
		return m.cfg.MinInterval, false, err
	}
	return d, true, nil
}

// setCooldown shows the chat's cooldown, or, for operators, sets it (a
// duration such as 10m, or 0 for none) or returns it to the default. The
// chat's timer is re-armed so a shorter cooldown applies at once.
func (m *Module) setCooldown(ctx context.Context, e otogi.Event, arg string) (string, error) {
	to := e.Destination()
	if arg == "" {
		d, set, err := m.cooldown(ctx, to)
		if err != nil {
			return "", err
		}
		if !set {
			return fmt.Sprintf("Cooldown here: %s (the default). Operators can change it with /todou cooldown <duration>, e.g. 10m, or 0 for none.", d), nil
		}
		return fmt.Sprintf("Cooldown here: %s. Operators can change it with /todou cooldown <duration>, or reset it with /todou cooldown default.", d), nil
	}
	if !m.operator(e) {
		return fmt.Sprintf("Only operators can change the cooldown. Your ID is %s.", e.ActorKey()), nil
	}
	var reply string
	if arg == "default" {
		if err := m.store.clearCooldown(ctx, to); err != nil {
			return "", err
		}
		reply = fmt.Sprintf("Cooldown here reset to the default, %s.", m.cfg.MinInterval)
	} else {
		d, err := time.ParseDuration(arg)
		if err != nil || d < 0 {
			return fmt.Sprintf("%q is not a duration. Use e.g. 30s, 10m, 1h, or 0 for none.", arg), nil
		}
		if err := m.store.setCooldown(ctx, to, d, e.ActorKey(), m.now()); err != nil {
			return "", err
		}
		reply = fmt.Sprintf("Cooldown here set to %s: at most one digest per %s.", d, d)
		if d == 0 {
			reply = "Cooldown here set to 0: every batch of activity is posted as it arrives."
		}
	}
	return reply, m.arm(ctx, to, m.now())
}

func (m *Module) operator(e otogi.Event) bool {
	return e.Direction == otogi.DirectionOutgoing || (e.ActorKey() != "" && slices.Contains(m.operators, e.ActorKey()))
}

func (m *Module) link(ctx context.Context, e otogi.Event, project string) (string, error) {
	projects, err := m.cli.projects(ctx)
	if err != nil {
		return fmt.Sprintf("Could not check the project with todou: %v", err), nil
	}
	if !slices.Contains(projects, project) {
		return fmt.Sprintf("The agent account cannot read a project named %q. Add it to the project as a reader, then link again.", project), nil
	}
	added, err := m.store.addLink(ctx, link{To: e.Destination(), Project: project}, e.ActorKey(), m.now())
	if err != nil {
		return "", err
	}
	if !added {
		return fmt.Sprintf("%s is already linked to this chat.", project), nil
	}
	return fmt.Sprintf("Linked %s. New activity will be posted here.", project), nil
}

func (m *Module) unlink(ctx context.Context, e otogi.Event, project string) (string, error) {
	removed, err := m.store.removeLink(ctx, link{To: e.Destination(), Project: project})
	if err != nil {
		return "", err
	}
	if !removed {
		return fmt.Sprintf("%s is not linked to this chat.", project), nil
	}
	return fmt.Sprintf("Unlinked %s.", project), nil
}

func (m *Module) list(ctx context.Context, e otogi.Event) (string, error) {
	to := e.Destination()
	links, err := m.store.links(ctx, &to)
	if err != nil {
		return "", err
	}
	if len(links) == 0 {
		return "No todou projects are linked to this chat. An operator can add one with /todou link <project>.", nil
	}
	names := make([]string, len(links))
	for i, l := range links {
		names[i] = l.Project
	}
	return "Linked todou projects: " + strings.Join(names, ", "), nil
}

func (m *Module) status(ctx context.Context, e otogi.Event) (string, error) {
	to := e.Destination()
	sum, err := m.store.summary(ctx, to)
	if err != nil {
		return "", err
	}
	links, err := m.store.links(ctx, &to)
	if err != nil {
		return "", err
	}
	whoCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	identity, whoErr := m.cli.whoami(whoCtx)
	if whoErr != nil {
		identity = "unknown: " + whoErr.Error()
	}
	m.mu.Lock()
	w := m.watcher
	m.mu.Unlock()
	ago := func(t time.Time) string { return m.now().Sub(t).Round(time.Second).String() + " ago" }

	var b strings.Builder
	fmt.Fprintf(&b, "Server: %s\nAgent: %s\n", m.cfg.Server, identity)
	switch {
	case w.err != "":
		fmt.Fprintf(&b, "Watcher: failed %s, restarting: %s", ago(w.failedAt), w.err)
	case w.since.IsZero():
		b.WriteString("Watcher: starting")
	default:
		fmt.Fprintf(&b, "Watcher: running since %s", ago(w.since))
	}
	if sum.HasCursor {
		fmt.Fprintf(&b, ", last batch %s", ago(sum.BatchAt))
	}
	cooldown, _, err := m.cooldown(ctx, to)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "\nDigests here: at most one per %s", cooldown)
	if !sum.LastSent.IsZero() {
		fmt.Fprintf(&b, ", last sent %s", ago(sum.LastSent))
	}
	fmt.Fprintf(&b, "\nQueued here: %d entries, %d messages pending, %d failed", sum.Queued, sum.Pending, sum.Failed)
	if sum.LastFailureError != "" {
		fmt.Fprintf(&b, " (latest: %s)", sum.LastFailureError)
	}
	fmt.Fprintf(&b, "\nLinked here: %d project(s)", len(links))
	return b.String(), nil
}
