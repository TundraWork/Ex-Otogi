package todou

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeTodou writes a shell script standing in for the CLI.
func fakeTodou(t *testing.T, body string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "todou")
	env := `if [ "$TODOU_SERVER" != "https://todou.test" ] || [ "$TODOU_TOKEN" != "todou_pat_x" ]; then echo "bad env" >&2; exit 1; fi` + "\n"
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+env+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

// The real subprocess path: credentials in the environment, batches on
// stdout, exit codes and stderr surfaced, SIGTERM on cancel. The subtests
// run one at a time: a fork while another subtest is still writing its
// script fails with "text file busy".
func TestExecCLI(t *testing.T) {
	t.Parallel()
	t.Run("follow", func(t *testing.T) {
		script := fakeTodou(t, `echo '{"type":"comment","id":1,"project":"web","issue_number":1,"body":"hi"}'
echo '{"type":"cursor","next_cursor":"c9"}'
echo '{"type":"comment","id":2,"project":"web","issue_number":1,"body":"no cursor yet"}'
echo "token revoked" >&2
exit 1
`)
		c := client{stream: execStreamer(script, "https://todou.test", "todou_pat_x")}
		var stderr, batches []string
		err := c.follow(context.Background(), "c0", nil, 10*time.Second, func(line string) { stderr = append(stderr, line) }, func(entries []entry, next string) error {
			for _, e := range entries {
				batches = append(batches, e.Body+"@"+next)
			}
			return nil
		})
		var cliErr *cliError
		if !errors.As(err, &cliErr) || cliErr.code != 1 || cliErr.stderr != "token revoked" {
			t.Fatalf("error = %v", err)
		}
		if !slices.Equal(batches, []string{"hi@c9"}) || !slices.Equal(stderr, []string{"token revoked"}) {
			t.Fatalf("batches = %v, stderr = %v", batches, stderr)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		script := fakeTodou(t, `echo '{"type":"cursor","next_cursor":"c1"}'
exec sleep 30
`)
		c := client{stream: execStreamer(script, "https://todou.test", "todou_pat_x")}
		ctx, cancel := context.WithCancel(context.Background())
		start := time.Now()
		err := c.follow(ctx, "c0", nil, time.Second, func(string) {}, func([]entry, string) error { cancel(); return nil })
		if ctx.Err() == nil || time.Since(start) > 5*time.Second {
			t.Fatalf("follow returned %v after %s", err, time.Since(start))
		}
	})
	t.Run("one-shot", func(t *testing.T) {
		script := fakeTodou(t, `if [ "$1" = fail ]; then echo "gave up on a network outage" >&2; exit 4; fi
echo "c5"
`)
		c := client{run: execRunner(script, "https://todou.test", "todou_pat_x", 5*time.Second)}
		if got, err := c.now(context.Background()); err != nil || got != "c5" {
			t.Fatalf("now = %q, %v", got, err)
		}
		_, err := c.run(context.Background(), "fail")
		var cliErr *cliError
		if !errors.As(err, &cliErr) || cliErr.code != 4 || !strings.Contains(cliErr.stderr, "network outage") {
			t.Fatalf("error = %v", err)
		}
	})
}
