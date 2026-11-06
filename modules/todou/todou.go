// Package todou connects chats to todou issue trackers through the
// operator's todou agent accounts. A chat links a project through one agent,
// with /todou commands. For each agent a resident `todou watch` process
// streams new activity into a queue per chat it is linked to, and each chat
// gets the queue as one digest at most once per min_interval, through a
// transactional outbox: in order and exactly once per chat. Silent links
// only grant access.
package todou

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config configures the module; see otogi.example.yaml.
type Config struct {
	// CLI is the path of the todou CLI binary.
	CLI string `yaml:"cli"`
	// Agents are the todou accounts chats can link projects through, by
	// name.
	Agents map[string]AgentConfig `yaml:"agents"`
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

// AgentConfig is one todou account.
type AgentConfig struct {
	// Server is the todou server URL.
	Server string `yaml:"server"`
	// Token is the account's personal access token (todou_pat_…).
	Token string `yaml:"token"`
	// LinkBase is the web URL issue links point to; default Server.
	LinkBase string `yaml:"link_base"`
}

// DefaultEvents are the activity types notified when Config.Events is unset.
var DefaultEvents = []string{"comment", "opened", "closed", "reopened", "status_changed", "assigned", "spec_pushed", "spec_review", "question_answered",
	"block_added", "block_removed", "block_cleared"}

// agentName is what an agent may be called in config and commands.
var agentName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

const (
	outboxRetention = 30 * 24 * time.Hour
	// A failed watcher restarts after restartMin, doubling up to restartMax;
	// one that ran for restartMax starts over at restartMin.
	restartMin = 5 * time.Second
	restartMax = 5 * time.Minute
)

// Module is the todou module.
type Module struct {
	cfg       Config
	operators []string
	agents    map[string]*agent
	names     []string // agent names, sorted
	render    renderer
	now       func() time.Time

	host     otogi.Host
	log      *slog.Logger
	store    *store
	stop     context.CancelFunc
	watchers sync.WaitGroup // one per agent
}

// agent is one todou account at run time.
type agent struct {
	name     string
	cfg      AgentConfig
	cli      client
	linkBase string

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
// platform-qualified actor IDs allowed to link and unlink projects.
func New(cfg Config, operators []string) (*Module, error) {
	var errs []error
	if cfg.CLI == "" {
		errs = append(errs, errors.New("cli is required"))
	}
	if len(cfg.Agents) == 0 {
		errs = append(errs, errors.New("agents: at least one is required"))
	}
	for name, a := range cfg.Agents {
		switch {
		case !agentName.MatchString(name):
			errs = append(errs, fmt.Errorf("agents.%s: names are lowercase letters, digits, - and _", name))
		case a.Server == "" || a.Token == "":
			errs = append(errs, fmt.Errorf("agents.%s: server and token are required", name))
		}
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
	m := &Module{cfg: cfg, operators: operators, agents: map[string]*agent{}, render: renderer{links: map[string]string{}, maxBody: cfg.MaxBodyChars}, now: time.Now}
	for name, a := range cfg.Agents {
		base := strings.TrimSuffix(a.LinkBase, "/")
		if base == "" {
			base = strings.TrimSuffix(a.Server, "/")
		}
		m.agents[name] = &agent{name: name, cfg: a, linkBase: base,
			cli: client{run: m.recorded(name, execRunner(cfg.CLI, a.Server, a.Token, cfg.CLITimeout)), stream: execStreamer(cfg.CLI, a.Server, a.Token)}}
		m.render.links[name] = base
		m.names = append(m.names, name)
	}
	slices.Sort(m.names)
	return m, nil
}

// Descriptor declares /todou.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{
		ID: "todou",
		Commands: []otogi.CommandSpec{{
			Name: "todou", Usage: "view|comment|move|assign|block … <card> | new <project> <title> | link <agent> <project> [silent] | list | status",
			Description: "work with todou projects linked to this chat, and post their activity",
		}},
		Provides: []string{llm.ToolsService("todou")},
	}
}

// Start opens the module's database, gives links from before agents to the
// only agent, and starts each agent's watcher.
func (m *Module) Start(ctx context.Context, host otogi.Host) error {
	db, err := host.Database(ctx)
	if err != nil {
		return err
	}
	if m.store, err = openStore(ctx, db); err != nil {
		return err
	}
	if len(m.names) == 1 {
		if err := m.store.adopt(ctx, m.names[0]); err != nil {
			return err
		}
	} else if n, err := m.store.unassigned(ctx); err != nil {
		return err
	} else if n > 0 {
		return fmt.Errorf("todou: %d links, queued entries, or cursors name no agent; they belong to the account the module used before agents: start once with only that agent configured", n)
	}
	m.host, m.log = host, host.Logger()
	if err := host.Provide(llm.ToolsService("todou"), m.tools()); err != nil {
		return err
	}
	ctx, m.stop = context.WithCancel(context.WithoutCancel(ctx))
	for _, name := range m.names {
		a := m.agents[name]
		m.watchers.Go(func() { m.watch(ctx, a) })
	}
	return nil
}

// Stop stops the watchers; activity they have not handed over yet is read
// again on the next start.
func (m *Module) Stop(ctx context.Context) error {
	if m.stop == nil {
		return nil
	}
	m.stop()
	done := make(chan struct{})
	go func() {
		m.watchers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("todou: stop watchers: %w", ctx.Err())
	}
}

// watch runs an agent's watcher until ctx ends, restarting it with backoff
// when it fails.
func (m *Module) watch(ctx context.Context, a *agent) {
	delay := restartMin
	for {
		started := time.Now()
		err := m.runWatcher(ctx, a)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) >= restartMax {
			delay = restartMin
		}
		m.watcherFailed(ctx, a, err, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(2*delay, restartMax)
	}
}

// runWatcher arms the digest timer of every chat with a backlog (a crash or
// a failed SetTimer may have left one unarmed), then follows the agent's
// activity from its stored cursor, or from now on a first start.
func (m *Module) runWatcher(ctx context.Context, a *agent) (err error) {
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
	cursor, err := m.store.cursor(ctx, a.name)
	if err != nil {
		return err
	}
	if cursor == "" {
		if cursor, err = a.cli.now(quiet(ctx)); err != nil { // a failure is recorded as todou.watch_failed
			return err
		}
		if err := m.store.ingest(ctx, a.name, cursor, nil, m.now()); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.watcher.since = m.now()
	a.mu.Unlock()
	return a.cli.follow(ctx, cursor, m.cfg.Events, m.cfg.Debounce,
		func(line string) { m.watchSaid(ctx, a, line) },
		func(entries []entry, cursor string) error { return m.ingest(ctx, a, entries, cursor) })
}

// watchSaid logs a line the watcher wrote to stderr. Its idle heartbeats are
// routine; failures it retries on its own are recorded, since they are where
// a delay in notifications comes from.
func (m *Module) watchSaid(ctx context.Context, a *agent, line string) {
	switch lower := strings.ToLower(line); {
	case strings.HasPrefix(lower, "still watching"):
		m.log.Debug("todou watch: "+line, "agent", a.name)
	case strings.Contains(lower, "fail") || strings.Contains(lower, "error") || strings.Contains(lower, "retry") || strings.Contains(lower, "gave up"):
		m.log.Warn("todou watch: "+line, "agent", a.name)
		m.host.Record(ctx, otogi.Record{Kind: "todou.watch_warning", Outcome: "warning", Payload: map[string]any{"agent": a.name, "line": line}})
	default:
		m.log.Info("todou watch: "+line, "agent", a.name)
	}
}

func (m *Module) watcherFailed(ctx context.Context, a *agent, err error, retry time.Duration) {
	a.mu.Lock()
	a.watcher = watcherState{err: err.Error(), failedAt: m.now()}
	a.mu.Unlock()
	m.log.Error("todou: watcher failed", "agent", a.name, "error", err, "retry_in", retry)
	m.host.Record(ctx, otogi.Record{Kind: "todou.watch_failed", Outcome: "error", Payload: map[string]any{"agent": a.name, "error": err.Error(), "retry_in": retry.String()}})
}

// ingest queues an agent's batch for the chats that hear its projects
// through that agent, with the cursor after it, and arms those chats' digest
// timers.
func (m *Module) ingest(ctx context.Context, a *agent, entries []entry, cursor string) error {
	links, err := m.store.links(ctx, nil)
	if err != nil {
		return err
	}
	byProject := map[string][]otogi.Destination{}
	for _, l := range links {
		if l.Agent == a.name && !l.Silent {
			byProject[l.Project] = append(byProject[l.Project], l.To)
		}
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
	if err := m.store.ingest(ctx, a.name, cursor, rows, now); err != nil {
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
	if err := m.store.digest(ctx, to, queue[len(queue)-1].ID, m.render.digest(entries, m.facts(ctx, entries)), now); err != nil {
		return err
	}
	if err := m.store.prune(ctx, now.Add(-outboxRetention)); err != nil {
		m.log.Warn("todou: prune outbox", "error", err)
	}
	return m.deliver(ctx, inv, to)
}

// facts looks up, through each entry's agent, the title (and, for opened
// issues, the body) of every issue the entries mention, the questions behind
// answers, and project slugs for references. A failed lookup leaves that
// fact out; an entry of an agent no longer configured gets none.
func (m *Module) facts(ctx context.Context, entries []entry) facts {
	known := facts{issues: map[string]issue{}, questions: map[string][]question{}, slugs: map[string]map[int64]string{}}
	for _, e := range entries {
		a := m.agents[e.agent]
		if a == nil {
			continue
		}
		if key := titleKey(e.agent, e.Project, e.IssueNumber); !hasKey(known.issues, key) {
			i, err := a.cli.issue(ctx, e.Project, e.IssueNumber)
			if err != nil {
				m.log.Warn("todou: issue lookup failed; notifying without the title", "agent", e.agent, "issue", key, "error", err)
			}
			known.issues[key] = i
		}
		switch e.EventType {
		case "question_answered":
			comment := int64(num(e.Payload["comment_id"]))
			if key := questionKey(e.agent, comment); comment != 0 && !hasKey(known.questions, key) {
				qs, err := a.cli.questions(ctx, e.Project, e.IssueNumber, comment)
				if err != nil {
					m.log.Warn("todou: question lookup failed; answers show their keys", "agent", e.agent, "comment", comment, "error", err)
				}
				known.questions[key] = qs
			}
		case "referenced", "cross_referenced":
			if !hasKey(known.slugs, e.agent) {
				known.slugs[e.agent] = map[int64]string{}
				projects, err := a.cli.projects(ctx)
				if err != nil {
					m.log.Warn("todou: project lookup failed; references show bare numbers", "agent", e.agent, "error", err)
				}
				for _, p := range projects {
					known.slugs[e.agent][p.ID] = p.Slug
				}
			}
		}
	}
	return known
}

func hasKey[V any](m map[string]V, key string) bool {
	_, ok := m[key]
	return ok
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
	verb, rest, _ := strings.Cut(verb, "\n") // "/todou new" may break the line at once
	arg = strings.TrimSpace(rest + " " + arg)
	var reply string
	var err error
	switch verb {
	case "link", "unlink":
		fields := strings.Fields(arg)
		switch {
		case !m.operator(e):
			reply = fmt.Sprintf("Only operators can change links. Your ID is %s.", e.ActorKey())
		case verb == "unlink" && len(fields) == 1:
			reply, err = m.unlink(ctx, e, fields[0])
		case verb == "link" && (len(fields) == 2 || len(fields) == 3 && fields[2] == "silent"):
			reply, err = m.link(ctx, e, fields[0], fields[1], len(fields) == 3)
		case verb == "link":
			reply = "Usage: /todou link <agent> <project> [silent]. Agents: " + strings.Join(m.names, ", ")
		default:
			reply = "Usage: /todou unlink <project>"
		}
	case "view", "new", "comment", "move", "assign", "unassign", "block", "unblock":
		reply, err = m.work(ctx, e, verb, arg)
		var r *refusal
		if errors.As(err, &r) {
			reply, err = r.msg, nil
		}
	case "list":
		reply, err = m.list(ctx, e)
	case "cooldown":
		reply, err = m.setCooldown(ctx, e, arg)
	case "status":
		reply, err = m.status(ctx, e)
	default:
		reply = "Usage: /todou view <card> | new <project> <title> | comment <card> <text> | move <card> <status> | assign|unassign <card> <login>… | " +
			"block|unblock <card> by|blocks <card>… | link <agent> <project> [silent] | unlink <project> | list | cooldown [<duration> | default] | status"
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
			return "Cooldown: " + cooldownText(d) + " (default).", nil
		}
		return "Cooldown: " + cooldownText(d) + ".", nil
	}
	if !m.operator(e) {
		return fmt.Sprintf("Only operators can change the cooldown. Your ID is %s.", e.ActorKey()), nil
	}
	var reply string
	if arg == "default" {
		if err := m.store.clearCooldown(ctx, to); err != nil {
			return "", err
		}
		reply = "Cooldown reset to the default, " + cooldownText(m.cfg.MinInterval) + "."
	} else {
		d, err := time.ParseDuration(arg)
		if err != nil || d < 0 {
			return fmt.Sprintf("%q is not a duration; try 30s, 10m, 1h, or 0.", arg), nil
		}
		if err := m.store.setCooldown(ctx, to, d, e.ActorKey(), m.now()); err != nil {
			return "", err
		}
		reply = "Cooldown set to " + cooldownText(d) + "."
		if d == 0 {
			reply = "Cooldown off."
		}
	}
	return reply, m.arm(ctx, to, m.now())
}

// duration writes d the way people do: 10m rather than 10m0s.
func duration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

// cooldownText is a cooldown for replies; 0 is "off".
func cooldownText(d time.Duration) string {
	if d == 0 {
		return "off"
	}
	return duration(d)
}

func (m *Module) operator(e otogi.Event) bool {
	return e.Actor.ID != "" && slices.Contains(m.operators, e.ActorKey())
}

// link links a project to the chat through an agent, after checking that
// the agent's account can read it.
func (m *Module) link(ctx context.Context, e otogi.Event, name, slug string, silent bool) (string, error) {
	a := m.agents[name]
	if a == nil {
		return fmt.Sprintf("There is no agent called %q. Agents: %s.", name, strings.Join(m.names, ", ")), nil
	}
	projects, err := a.cli.projects(ctx)
	if err != nil {
		return fmt.Sprintf("Could not check the project with todou: %v", err), nil
	}
	if !slices.ContainsFunc(projects, func(p project) bool { return p.Slug == slug }) {
		return fmt.Sprintf("%s cannot read a project named %q. Add the account to the project, then link again.", name, slug), nil
	}
	l := link{To: e.Destination(), Project: slug, Agent: name, Silent: silent}
	old, err := m.store.setLink(ctx, l, e.ActorKey(), m.now())
	if err != nil {
		return "", err
	}
	if old != nil && *old == l {
		return fmt.Sprintf("%s is already linked here, %s.", slug, linkText(l)), nil
	}
	if silent {
		return fmt.Sprintf("Linked %s through %s, silently: this chat can work with its cards, and gets no activity.", slug, name), nil
	}
	return fmt.Sprintf("Linked %s through %s. New activity will be posted here.", slug, name), nil
}

// linkText says how a project is linked.
func linkText(l link) string {
	if l.Silent {
		return "through " + l.Agent + ", silently"
	}
	return "through " + l.Agent
}

func (m *Module) unlink(ctx context.Context, e otogi.Event, project string) (string, error) {
	removed, err := m.store.removeLink(ctx, e.Destination(), project)
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
		return "No todou projects are linked to this chat. An operator can add one with /todou link <agent> <project>.", nil
	}
	lines := make([]string, len(links))
	for i, l := range links {
		lines[i] = "• " + l.Project + ", " + linkText(l)
	}
	return "Linked todou projects:\n" + strings.Join(lines, "\n"), nil
}

// status shows each agent's account and watcher, and this chat's links and
// delivery.
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
	ago := func(t time.Time) string { return duration(m.now().Sub(t).Round(time.Second)) + " ago" }
	var b strings.Builder
	for _, name := range m.names {
		a := m.agents[name]
		whoCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		identity, whoErr := a.cli.whoami(whoCtx)
		cancel()
		if whoErr != nil {
			identity = "unknown: " + whoErr.Error()
		}
		a.mu.Lock()
		w := a.watcher
		a.mu.Unlock()
		fmt.Fprintf(&b, "Agent %s: %s on %s\n  Watcher: ", name, identity, a.cfg.Server)
		switch {
		case w.err != "":
			fmt.Fprintf(&b, "failed %s, restarting: %s", ago(w.failedAt), w.err)
		case w.since.IsZero():
			b.WriteString("starting")
		default:
			fmt.Fprintf(&b, "running since %s", ago(w.since))
		}
		batch, err := m.store.batchAt(ctx, name)
		if err != nil {
			return "", err
		}
		if !batch.IsZero() {
			fmt.Fprintf(&b, ", last batch %s", ago(batch))
		}
		b.WriteString("\n")
	}
	cooldown, _, err := m.cooldown(ctx, to)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "Cooldown: %s", cooldownText(cooldown))
	if !sum.LastSent.IsZero() {
		fmt.Fprintf(&b, ", last digest %s", ago(sum.LastSent))
	}
	fmt.Fprintf(&b, "\nQueued here: %d entries, %d messages pending, %d failed", sum.Queued, sum.Pending, sum.Failed)
	if sum.LastFailureError != "" {
		fmt.Fprintf(&b, " (latest: %s)", sum.LastFailureError)
	}
	fmt.Fprintf(&b, "\nLinked here: %d project(s)", len(links))
	return b.String(), nil
}
