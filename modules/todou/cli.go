package todou

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	AgentContext *struct {
		Agent string `json:"agent"`
	} `json:"agent_context"`
}

// client wraps the CLI calls the module makes.
type client struct{ run runner }

// watch drains entries after cursor (all readable projects, every actor) and
// returns them with the cursor to resume from. Entries after the last cursor
// record are not returned: the CLI replays them next time.
func (c client) watch(ctx context.Context, cursor string, types []string) ([]entry, string, error) {
	args := []string{"watch", "--all-projects", "--poll", "--json", "--any-actor", "--since", cursor}
	if len(types) > 0 {
		args = append(args, "--type", strings.Join(types, ","))
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, "", err
	}
	var entries, pending []entry
	next := ""
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64<<10), 16<<20) // a comment body can be long
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, "", fmt.Errorf("todou watch: bad record %q: %w", tail(string(line), 200), err)
		}
		if e.Type == "cursor" {
			entries, pending, next = append(entries, pending...), nil, e.NextCursor
			continue
		}
		pending = append(pending, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, "", fmt.Errorf("todou watch: read output: %w", err)
	}
	if next == "" {
		return nil, "", errors.New("todou watch: output ended without a cursor record")
	}
	return entries, next, nil
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

func (c client) issue(ctx context.Context, project string, number int64) (issue, error) {
	out, err := c.run(ctx, "issue", "view", project+"/"+strconv.FormatInt(number, 10), "--json")
	if err != nil {
		return issue{}, err
	}
	var i issue
	if err := json.Unmarshal(out, &i); err != nil {
		return issue{}, fmt.Errorf("todou issue view: %w", err)
	}
	return i, nil
}

func (c client) projects(ctx context.Context) ([]string, error) {
	out, err := c.run(ctx, "project", "list", "--json")
	if err != nil {
		return nil, err
	}
	var projects []struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(out, &projects); err != nil {
		return nil, fmt.Errorf("todou project list: %w", err)
	}
	slugs := make([]string, len(projects))
	for i, p := range projects {
		slugs[i] = p.Slug
	}
	return slugs, nil
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
