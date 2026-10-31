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
)

// runner runs the todou CLI with args and returns its stdout. Tests replace it.
type runner func(ctx context.Context, args ...string) ([]byte, error)

// cliError is a failed CLI call, with its exit code and the end of stderr.
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
	return func(ctx context.Context, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = append(os.Environ(), "TODOU_SERVER="+server, "TODOU_TOKEN="+token)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit):
			return nil, &cliError{args: args, code: exit.ExitCode(), stderr: tail(stderr.String(), 500)}
		case err != nil:
			return nil, fmt.Errorf("run todou %s: %w", strings.Join(args, " "), err)
		}
		return stdout.Bytes(), nil
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
	out, err := c.run(ctx, "watch", "--all-projects", "--poll", "--print-cursor")
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
	out, err := c.run(ctx, "issue", "view", project+"/"+strconv.FormatInt(number, 10), "--brief", "--json")
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
	out, err := c.run(ctx, "comment", "view", project+"/"+strconv.FormatInt(number, 10), strconv.FormatInt(commentID, 10), "--json")
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
	out, err := c.run(ctx, "project", "list", "--json")
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
	out, err := c.run(ctx, "whoami", "--json")
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
