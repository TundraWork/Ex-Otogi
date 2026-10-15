// Package todou forwards activity from a todou issue tracker to chats.
// Projects are linked to chats with /todou commands; a scheduled poll reads
// new activity through the todou CLI and delivers it through a transactional
// outbox, in order and exactly once per chat. See
// docs/superpowers/specs/2026-10-16-todou-notifier-design.md.
package todou

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
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
	// PollInterval is how often new activity is read; default 30s.
	PollInterval time.Duration `yaml:"poll_interval"`
	// CLITimeout bounds each CLI call; default 60s.
	CLITimeout time.Duration `yaml:"cli_timeout"`
	// Events lists the activity types to notify; nil means DefaultEvents, an
	// empty list means every type.
	Events []string `yaml:"events"`
	// MaxBodyChars cuts quoted bodies; default 300, at most 3000.
	MaxBodyChars int `yaml:"max_body_chars"`
	// MaxAttempts is how many polls a notification is retried before it is
	// marked failed; default 10.
	MaxAttempts int `yaml:"max_attempts"`
}

// DefaultEvents are the activity types notified when Config.Events is unset.
var DefaultEvents = []string{"comment", "opened", "closed", "reopened", "status_changed", "assigned", "spec_pushed", "spec_review", "question_answered"}

const outboxRetention = 30 * 24 * time.Hour

// Module is the todou notifier.
type Module struct {
	cfg       Config
	operators []string
	cli       client
	render    renderer

	host   otogi.Host
	log    *slog.Logger
	store  *store
	titles map[string]issue // project#number -> issue; owned by the poll job
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
	if cfg.PollInterval < 0 || cfg.CLITimeout < 0 || cfg.MaxAttempts < 0 {
		errs = append(errs, errors.New("poll_interval, cli_timeout, and max_attempts must not be negative"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if cfg.LinkBase == "" {
		cfg.LinkBase = cfg.Server
	}
	cfg.LinkBase = strings.TrimSuffix(cfg.LinkBase, "/")
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.CLITimeout == 0 {
		cfg.CLITimeout = 60 * time.Second
	}
	if cfg.Events == nil {
		cfg.Events = DefaultEvents
	}
	if cfg.MaxBodyChars == 0 {
		cfg.MaxBodyChars = 300
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 10
	}
	m := &Module{cfg: cfg, operators: operators, cli: client{run: execRunner(cfg.CLI, cfg.Server, cfg.Token, cfg.CLITimeout)}, titles: map[string]issue{}}
	m.render = renderer{linkBase: cfg.LinkBase, maxBody: cfg.MaxBodyChars, title: m.issueInfo}
	return m, nil
}

// Descriptor declares /todou. The account's owner may use it too; no reply
// starts with a command prefix, so replies never trigger it.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{
		ID: "todou",
		Commands: []otogi.CommandSpec{{
			Name: "todou", Usage: "link <project> | unlink <project> | list | status",
			Description: "post a todou project's activity to this chat",
		}},
		Filter: otogi.Filter{Directions: []otogi.Direction{otogi.DirectionIncoming, otogi.DirectionOutgoing}},
	}
}

// Start opens the module's database and schedules the poll.
func (m *Module) Start(ctx context.Context, host otogi.Host) error {
	db, err := host.Database(ctx)
	if err != nil {
		return err
	}
	if m.store, err = openStore(ctx, db); err != nil {
		return err
	}
	m.host, m.log = host, host.Logger()
	return host.Schedule("poll", "@every "+m.cfg.PollInterval.String(), m.poll)
}

// poll delivers what is pending, reads new activity, commits its
// notifications with the new cursor, and delivers them.
func (m *Module) poll(ctx context.Context, inv otogi.Invocation) error {
	if err := m.deliver(ctx, inv); err != nil {
		return err
	}
	cursor, err := m.store.cursor(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	if cursor == "" { // first start: begin at the current position, no backlog
		if cursor, err = m.cli.now(ctx); err != nil {
			return m.pollFailed(ctx, err)
		}
		return m.store.commit(ctx, cursor, nil, now)
	}
	entries, next, err := m.cli.watch(ctx, cursor, m.cfg.Events)
	if err != nil {
		return m.pollFailed(ctx, err)
	}
	links, err := m.store.links(ctx, nil)
	if err != nil {
		return err
	}
	rows := m.notifications(ctx, entries, links)
	if err := m.store.commit(ctx, next, rows, now); err != nil {
		return err
	}
	if err := m.store.prune(ctx, now.Add(-outboxRetention)); err != nil {
		m.log.Warn("todou: prune outbox", "error", err)
	}
	return m.deliver(ctx, inv)
}

func (m *Module) pollFailed(ctx context.Context, cause error) error {
	if err := m.store.pollFailed(ctx, cause); err != nil {
		m.log.Error("todou: record poll failure", "error", err)
	}
	return fmt.Errorf("todou poll: %w", cause)
}

// notifications renders, per linked conversation in link order, the entries
// of its projects.
func (m *Module) notifications(ctx context.Context, entries []entry, links []link) []outboxRow {
	byProject := map[string][]otogi.Destination{}
	for _, l := range links {
		byProject[l.Project] = append(byProject[l.Project], l.To)
	}
	var order []otogi.Destination
	perChat := map[otogi.Destination][]entry{}
	for _, e := range entries {
		if e.EventType == "title_changed" {
			m.forget(e)
		}
		for _, to := range byProject[e.Project] {
			if _, seen := perChat[to]; !seen {
				order = append(order, to)
			}
			perChat[to] = append(perChat[to], e)
		}
	}
	m.prefetch(ctx, entries, byProject)
	var rows []outboxRow
	for _, to := range order {
		for _, msg := range m.render.messages(perChat[to]) {
			rows = append(rows, outboxRow{To: to, Message: msg})
		}
	}
	return rows
}

func titleKey(project string, number int64) string { return fmt.Sprintf("%s#%d", project, number) }

func (m *Module) forget(e entry) { delete(m.titles, titleKey(e.Project, e.IssueNumber)) }

// prefetch loads the title (and, for opened issues, the body) of every
// issue a linked entry mentions. A failed lookup leaves the title out.
func (m *Module) prefetch(ctx context.Context, entries []entry, byProject map[string][]otogi.Destination) {
	for _, e := range entries {
		key := titleKey(e.Project, e.IssueNumber)
		if _, linked := byProject[e.Project]; !linked {
			continue
		}
		if _, cached := m.titles[key]; cached {
			continue
		}
		i, err := m.cli.issue(ctx, e.Project, e.IssueNumber)
		if err != nil {
			m.log.Warn("todou: issue lookup failed; notifying without the title", "issue", key, "error", err)
		}
		m.titles[key] = i
	}
}

func (m *Module) issueInfo(project string, number int64) issue {
	return m.titles[titleKey(project, number)]
}

// deliver sends pending outbox rows in order. A failure stops delivery to
// that conversation until the next poll, so its notifications stay in order;
// a row that has failed MaxAttempts times is marked failed and recorded, and
// delivery moves on. Each row's effect is keyed by the row, so a row resent
// after a crash is not delivered twice.
func (m *Module) deliver(ctx context.Context, inv otogi.Invocation) error {
	rows, err := m.store.pending(ctx)
	if err != nil {
		return err
	}
	blocked := map[otogi.Destination]bool{}
	for _, r := range rows {
		if blocked[r.To] {
			continue
		}
		_, err := inv.WithOrigin(fmt.Sprintf("outbox:%d", r.ID)).SendTo(ctx, "notify", r.To, r.Message)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			if err := m.store.markSent(ctx, r.ID, time.Now()); err != nil {
				return err
			}
			continue
		}
		failed, recordErr := m.store.attemptFailed(ctx, r.ID, err, m.cfg.MaxAttempts, time.Now())
		if recordErr != nil {
			return recordErr
		}
		if !failed {
			blocked[r.To] = true
			m.log.Warn("todou: delivery failed; retrying next poll", "row", r.ID, "conversation", r.To.Conversation, "attempt", r.Attempts+1, "error", err)
			continue
		}
		m.log.Error("todou: giving up on a notification", "row", r.ID, "conversation", r.To.Conversation, "error", err)
		m.host.Record(ctx, otogi.Record{Kind: "todou.delivery_failed", Outcome: "error", Platform: r.To.Platform, Account: r.To.Account, Conversation: r.To.Conversation,
			Payload: map[string]any{"row": r.ID, "attempts": r.Attempts + 1, "error": err.Error(), "message": r.Message}})
	}
	return nil
}

// Handle answers /todou commands.
func (m *Module) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	verb, project, _ := strings.Cut(e.Command.Args, " ")
	project = strings.TrimSpace(project)
	var reply string
	var err error
	switch verb {
	case "link", "unlink":
		switch {
		case !m.operator(e):
			reply = fmt.Sprintf("Only operators can change links. Your ID is %s.", e.ActorKey())
		case project == "":
			reply = fmt.Sprintf("Usage: /todou %s <project>", verb)
		case verb == "link":
			reply, err = m.link(ctx, e, project)
		default:
			reply, err = m.unlink(ctx, e, project)
		}
	case "list":
		reply, err = m.list(ctx, e)
	case "status":
		reply, err = m.status(ctx, e)
	default:
		reply = "Usage: /todou link <project> | unlink <project> | list | status"
	}
	if err != nil {
		return err
	}
	_, err = inv.Send(ctx, "reply", otogi.Reply(reply, e.Ref()))
	return err
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
	added, err := m.store.addLink(ctx, link{To: e.Destination(), Project: project}, e.ActorKey(), time.Now())
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
	sum, err := m.store.summary(ctx)
	if err != nil {
		return "", err
	}
	to := e.Destination()
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
	var b strings.Builder
	fmt.Fprintf(&b, "Server: %s\nAgent: %s\nPolling every %s", m.cfg.Server, identity, m.cfg.PollInterval)
	switch {
	case !sum.HasCursor:
		b.WriteString(", first poll pending")
	case sum.PolledAt.IsZero():
	default:
		fmt.Fprintf(&b, ", last success %s ago", time.Since(sum.PolledAt).Round(time.Second))
	}
	if sum.PollError != "" {
		fmt.Fprintf(&b, "\nLast poll failed: %s", sum.PollError)
	}
	fmt.Fprintf(&b, "\nOutbox: %d pending, %d failed", sum.Pending, sum.Failed)
	if sum.LastFailureError != "" {
		fmt.Fprintf(&b, " (latest: %s)", sum.LastFailureError)
	}
	fmt.Fprintf(&b, "\nLinked here: %d project(s)", len(links))
	return b.String(), nil
}
