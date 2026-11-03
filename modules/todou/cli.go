package todou

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// runner runs the todou CLI with args, and stdin when it is not empty, and
// returns its stdout. Tests replace it.
type runner func(ctx context.Context, stdin string, args ...string) ([]byte, error)

// cliError is a failed CLI call, with its exit code and stderr.
type cliError struct {
	args   []string
	code   int
	stderr string
}

func (e *cliError) Error() string {
	return fmt.Sprintf("todou %s: exit %d: %s", strings.Join(e.args, " "), e.code, e.stderr)
}

// execRunner runs the real CLI in config-free mode: the server and token come
// from the environment, so nothing is read from or written to ~/.config/todou.
func execRunner(path, server, token string, timeout time.Duration) runner {
	return func(ctx context.Context, stdin string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = append(os.Environ(), "TODOU_SERVER="+server, "TODOU_TOKEN="+token)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit):
			return nil, &cliError{args: args, code: exit.ExitCode(), stderr: strings.TrimSpace(stderr.String())}
		case err != nil:
			return nil, fmt.Errorf("run todou %s: %w", strings.Join(args, " "), err)
		}
		return stdout.Bytes(), nil
	}
}

type quietKey struct{}

// quiet marks the CLI calls made under ctx as ones whose failure is expected
// or recorded by the caller, so recorded leaves them out.
func quiet(ctx context.Context) context.Context {
	return context.WithValue(ctx, quietKey{}, true)
}

// recorded wraps an agent's runner so every failed call is logged and
// recorded as todou.call_failed, with its arguments, input, exit code, and
// stderr. Calls under quiet are left out: probing each linked project for a
// prefix ref expects refusals, and writes record their own outcome. Calls
// cut short by the caller's context are left to the caller.
func (m *Module) recorded(agent string, run runner) runner {
	return func(ctx context.Context, stdin string, args ...string) ([]byte, error) {
		started := time.Now()
		out, err := run(ctx, stdin, args...)
		if err == nil || ctx.Err() != nil || ctx.Value(quietKey{}) != nil {
			return out, err
		}
		payload := map[string]any{"agent": agent, "args": args, "error": err.Error()}
		if stdin != "" {
			payload["stdin"] = stdin
		}
		var cli *cliError
		if errors.As(err, &cli) {
			payload["exit"], payload["stderr"] = cli.code, cli.stderr
		}
		m.log.Warn("todou: call failed", "agent", agent, "args", args, "duration", time.Since(started), "error", err)
		m.host.Record(ctx, otogi.Record{Kind: "todou.call_failed", Outcome: "error", Duration: time.Since(started), Payload: payload})
		return out, err
	}
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return s
}

// user is the part of a todou UserRef the module shows.
type user struct {
	Login       string `json:"login"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"` // "human" or "machine"
}

func (u *user) name() string {
	switch {
	case u == nil:
		return "someone"
	case u.DisplayName != "":
		return u.DisplayName
	default:
		return u.Login
	}
}

// entry is one record of `todou watch --json`: a comment, an issue event, or
// the closing cursor record.
type entry struct {
	Type         string         `json:"type"` // "comment", "event", or "cursor"
	Kind         string         `json:"kind"` // of a comment: "comment", or "me" for an action line
	NextCursor   string         `json:"next_cursor"`
	ID           int64          `json:"id"`
	Project      string         `json:"project"`
	IssueNumber  int64          `json:"issue_number"`
	IssueRef     string         `json:"issue_ref"`
	Author       *user          `json:"author"` // comments
	Actor        *user          `json:"actor"`  // events
	Body         string         `json:"body"`
	HiddenAt     *string        `json:"hidden_at"`
	EventType    string         `json:"event_type"`
	Payload      map[string]any `json:"payload"`
	CreatedAt    time.Time      `json:"created_at"`
	AgentContext *agentContext  `json:"agent_context"`
	Component    *component     `json:"component"` // comments

	raw   string // the record as received
	agent string // whose watcher saw it, set from the queue
}

// streamer runs the todou CLI with args until it exits or ctx ends, passing
// each stdout line to stdout and each stderr line to stderr. Tests replace it.
type streamer func(ctx context.Context, args []string, stdout func(line []byte) error, stderr func(line string)) error

// execStreamer runs the real CLI like execRunner, without a timeout. When ctx
// ends the CLI gets SIGTERM, so it can exit cleanly, and is killed if it is
// still running 10 seconds later.
func execStreamer(path, server, token string) streamer {
	return func(ctx context.Context, args []string, stdout func([]byte) error, stderr func(string)) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = append(os.Environ(), "TODOU_SERVER="+server, "TODOU_TOKEN="+token)
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = 10 * time.Second
		var lineErr error // set by the stdout copier; read after Run waits for it
		cmd.Stdout = &lineWriter{fn: func(line []byte) error {
			if lineErr = stdout(line); lineErr != nil {
				cancel()
			}
			return lineErr
		}}
		errLines := &lineWriter{fn: func(line []byte) error { stderr(string(line)); return nil }}
		cmd.Stderr = errLines
		err := cmd.Run()
		var exit *exec.ExitError
		switch {
		case lineErr != nil:
			return lineErr
		case errors.As(err, &exit) && ctx.Err() == nil:
			return &cliError{args: args, code: exit.ExitCode(), stderr: errLines.last}
		case err != nil:
			return fmt.Errorf("run todou %s: %w", strings.Join(args, " "), err)
		}
		return nil
	}
}

// lineWriter passes each non-blank line written to it to fn and remembers
// the last one. The line is only valid during the call.
type lineWriter struct {
	fn   func(line []byte) error
	buf  []byte
	last string
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := bytes.TrimSpace(w.buf[:i])
		w.buf = w.buf[i+1:]
		if len(line) == 0 {
			continue
		}
		w.last = string(line)
		if err := w.fn(line); err != nil {
			return 0, err
		}
	}
}

// agentContext is set on entries an agent wrote.
type agentContext struct {
	Agent string `json:"agent"` // the harness, e.g. claude-code
	Model string `json:"model"`
}

// client wraps the CLI calls the module makes.
type client struct {
	run    runner
	stream streamer
}

// follow streams entries after cursor (all readable projects, every actor)
// until the CLI exits or ctx ends, calling batch for every batch with the
// cursor after it. debounce holds a batch open for that long after its first
// entry, so a burst arrives together. The watch is invisible: the notifier
// is not an agent waiting for work, which the web shows watches as.
func (c client) follow(ctx context.Context, cursor string, types []string, debounce time.Duration, stderr func(string), batch func([]entry, string) error) error {
	args := []string{"watch", "--all-projects", "--follow", "--invisible", "--json", "--any-actor", "--since", cursor, "--debounce", strconv.Itoa(int(debounce.Seconds()))}
	if len(types) > 0 {
		args = append(args, "--type", strings.Join(types, ","))
	}
	var pending []entry
	err := c.stream(ctx, args, func(line []byte) error {
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			return fmt.Errorf("todou watch: bad record %q: %w", tail(string(line), 200), err)
		}
		if e.Type == "cursor" {
			entries := pending
			pending = nil
			return batch(entries, e.NextCursor)
		}
		e.raw = string(line)
		pending = append(pending, e)
		return nil
	}, stderr)
	if err == nil && ctx.Err() == nil {
		err = errors.New("todou watch --follow exited")
	}
	return err
}

// decodeEntry reads an entry stored as its watch record.
func decodeEntry(raw string) (entry, error) {
	var e entry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return entry{}, fmt.Errorf("todou: decode queued entry: %w", err)
	}
	e.raw = raw
	return e, nil
}

// now returns a cursor at the current position, for a first start.
func (c client) now(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "", "watch", "--all-projects", "--poll", "--print-cursor")
	if err != nil {
		return "", err
	}
	cursor := strings.TrimSpace(string(out))
	if cursor == "" {
		return "", errors.New("todou watch --print-cursor printed no cursor")
	}
	return cursor, nil
}

type issue struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// component is what a comment carries besides its body: questions, for one.
type component struct {
	Questions []question `json:"questions"`
}

// question is one question a comment asks.
type question struct {
	Key      string `json:"key"`
	Header   string `json:"header"`
	Question string `json:"question"`
	Options  []struct {
		Label string `json:"label"`
	} `json:"options"`
}

// issue reads a card's title and body. --brief reads no timeline and leaves
// no read mark: the agent account only reports the card, it does not work
// it.
func (c client) issue(ctx context.Context, project string, number int64) (issue, error) {
	out, err := c.run(ctx, "", "issue", "view", project+"/"+strconv.FormatInt(number, 10), "--brief", "--json")
	if err != nil {
		return issue{}, err
	}
	var view struct {
		Issue issue `json:"issue"`
	}
	if err := json.Unmarshal(out, &view); err != nil {
		return issue{}, fmt.Errorf("todou issue view: %w", err)
	}
	if view.Issue.Title == "" {
		return issue{}, fmt.Errorf("todou issue view %s/%d: no title in the output", project, number)
	}
	return view.Issue, nil
}

// questions reads the questions a comment asks; reading a comment leaves no
// read mark.
func (c client) questions(ctx context.Context, project string, number, commentID int64) ([]question, error) {
	out, err := c.run(ctx, "", "comment", "view", project+"/"+strconv.FormatInt(number, 10), strconv.FormatInt(commentID, 10), "--json")
	if err != nil {
		return nil, err
	}
	var view struct {
		Component *component `json:"component"`
	}
	if err := json.Unmarshal(out, &view); err != nil {
		return nil, fmt.Errorf("todou comment view: %w", err)
	}
	if view.Component == nil {
		return nil, nil
	}
	return view.Component.Questions, nil
}

// project is a project the agent account can read.
type project struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
}

func (c client) projects(ctx context.Context) ([]project, error) {
	out, err := c.run(ctx, "", "project", "list", "--json")
	if err != nil {
		return nil, err
	}
	var projects []project
	if err := json.Unmarshal(out, &projects); err != nil {
		return nil, fmt.Errorf("todou project list: %w", err)
	}
	return projects, nil
}

// whoami describes the agent account the token belongs to.
func (c client) whoami(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "", "whoami", "--json")
	if err != nil {
		return "", err
	}
	var me struct {
		Login       string `json:"login"`
		DisplayName string `json:"display_name"`
		Kind        string `json:"kind"`
	}
	if err := json.Unmarshal(out, &me); err != nil {
		return "", fmt.Errorf("todou whoami: %w", err)
	}
	return fmt.Sprintf("%s (@%s, %s)", me.DisplayName, me.Login, me.Kind), nil
}

// card is a card as `issue view --brief --json` shows it.
type card struct {
	Number int64  `json:"number"`
	Ref    string `json:"ref"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Status struct {
		Name     string `json:"name"`
		Category string `json:"category"`
	} `json:"status"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Assignees []user `json:"assignees"`
	BlockedBy []edge `json:"blocked_by"`
	Blocks    []edge `json:"blocks"`
}

// edge is one side of a block between cards.
type edge struct {
	Ref       string  `json:"ref"`
	ClearedAt *string `json:"cleared_at"`
}

// view reads a card by any ref the CLI accepts; project, when set, is the
// project the ref is read in (a prefix that contradicts it is refused).
// --brief leaves no read mark.
func (c client) view(ctx context.Context, project, ref string) (card, error) {
	args := []string{"issue", "view", ref, "--brief", "--json"}
	if project != "" {
		args = append(args, "-p", project)
	}
	out, err := c.run(ctx, "", args...)
	if err != nil {
		return card{}, err
	}
	var view struct {
		Issue card `json:"issue"`
	}
	if err := json.Unmarshal(out, &view); err != nil {
		return card{}, fmt.Errorf("todou issue view: %w", err)
	}
	return view.Issue, nil
}

// remark is a comment as `comment list --json` shows it.
type remark struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Author    *user     `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	HiddenAt  *string   `json:"hidden_at"`
}

// comments reads a card's last n comments, without a read mark.
func (c client) comments(ctx context.Context, project string, number int64, n int) ([]remark, error) {
	out, err := c.run(ctx, "", "comment", "list", cardPath(project, number), "--last", strconv.Itoa(n), "--json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Comments []remark `json:"comments"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("todou comment list: %w", err)
	}
	return list.Comments, nil
}

// hit is one search result.
type hit struct {
	Kind  string `json:"kind"`
	Field string `json:"field"`
	Issue struct {
		Ref    string `json:"ref"`
		Title  string `json:"title"`
		Status struct {
			Name string `json:"name"`
		} `json:"status"`
	} `json:"issue"`
	Snippet struct {
		Text string `json:"text"`
	} `json:"snippet"`
}

// search finds cards, comments, and specs in a project matching every term.
func (c client) search(ctx context.Context, project string, terms []string, limit int) ([]hit, error) {
	args := append([]string{"search"}, terms...)
	out, err := c.run(ctx, "", append(args, "-p", project, "-L", strconv.Itoa(limit), "--json")...)
	if err != nil {
		return nil, err
	}
	var result struct {
		Items []hit `json:"items"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("todou search: %w", err)
	}
	return result.Items, nil
}

// projectFacts is what a write needs to know about a project: its statuses
// in order, its labels, and its members.
type projectFacts struct {
	Statuses []string `json:"statuses"`
	Closed   []string `json:"closed_statuses"`
	Labels   []string `json:"labels"`
	Members  []string `json:"members"`
}

func (c client) facts(ctx context.Context, project string) (projectFacts, error) {
	var f projectFacts
	var statuses []struct{ Name, Category string }
	if err := c.readJSON(ctx, &statuses, "status", "list", "-p", project, "--json"); err != nil {
		return f, err
	}
	var labels []struct{ Name string }
	if err := c.readJSON(ctx, &labels, "label", "list", "-p", project, "--json"); err != nil {
		return f, err
	}
	var members []struct {
		User user `json:"user"`
	}
	if err := c.readJSON(ctx, &members, "project", "members", "-p", project, "--json"); err != nil {
		return f, err
	}
	for _, s := range statuses {
		f.Statuses = append(f.Statuses, s.Name)
		if s.Category == "closed" {
			f.Closed = append(f.Closed, s.Name)
		}
	}
	for _, l := range labels {
		f.Labels = append(f.Labels, l.Name)
	}
	for _, m := range members {
		f.Members = append(f.Members, m.User.Login)
	}
	return f, nil
}

// readJSON runs a read and decodes its JSON output into v.
func (c client) readJSON(ctx context.Context, v any, args ...string) error {
	out, err := c.run(ctx, "", args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("todou %s: %w", strings.Join(args[:2], " "), err)
	}
	return nil
}

func cardPath(project string, number int64) string {
	return project + "/" + strconv.FormatInt(number, 10)
}

// create files a card. The body goes through stdin, so Markdown survives
// verbatim.
func (c client) create(ctx context.Context, project, title, body, status string, labels, assignees []string) (card, error) {
	args := []string{"issue", "create", "-p", project, "--title", title, "--body-file", "-", "--json"}
	if status != "" {
		args = append(args, "--status", status)
	}
	for _, l := range labels {
		args = append(args, "--label", l)
	}
	for _, a := range assignees {
		args = append(args, "--assignee", a)
	}
	out, err := c.run(ctx, body, args...)
	if err != nil {
		return card{}, err
	}
	var created card
	if err := json.Unmarshal(out, &created); err != nil {
		return card{}, fmt.Errorf("todou issue create: %w", err)
	}
	return created, nil
}

// comment adds a comment, returning its ID. The body goes through stdin.
func (c client) comment(ctx context.Context, project string, number int64, body string) (int64, error) {
	out, err := c.run(ctx, body, "comment", "add", cardPath(project, number), "--body-file", "-", "--json")
	if err != nil {
		return 0, err
	}
	var added struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(out, &added); err != nil {
		return 0, fmt.Errorf("todou comment add: %w", err)
	}
	return added.ID, nil
}

func (c client) move(ctx context.Context, project string, number int64, status string) error {
	_, err := c.run(ctx, "", "issue", "status", cardPath(project, number), status, "--json")
	return err
}

func (c client) assign(ctx context.Context, project string, number int64, add, remove []string) error {
	args := []string{"issue", "edit", cardPath(project, number), "--json"}
	for _, a := range add {
		args = append(args, "--add-assignee", a)
	}
	for _, r := range remove {
		args = append(args, "--remove-assignee", r)
	}
	_, err := c.run(ctx, "", args...)
	return err
}

// block records (or, with unblock, removes) that the card waits for the
// cards in by and that the cards in blocks wait for it.
func (c client) block(ctx context.Context, project string, number int64, by, blocks []string, unblock bool) error {
	verb := "block"
	if unblock {
		verb = "unblock"
	}
	args := []string{"issue", verb, cardPath(project, number), "--json"}
	if len(by) > 0 {
		args = append(args, "--by", strings.Join(by, ","))
	}
	for _, b := range blocks {
		args = append(args, "--blocks", b)
	}
	_, err := c.run(ctx, "", args...)
	return err
}
