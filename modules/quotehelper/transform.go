package quotehelper

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const substitutionTimeout = 2 * time.Second

type substitutionRunner interface {
	Run(ctx context.Context, expression string, input string) (string, error)
}

type execSubstitutionRunner struct {
	command string
	timeout time.Duration
}

func newExecSubstitutionRunner() substitutionRunner {
	return execSubstitutionRunner{
		command: "sed",
		timeout: substitutionTimeout,
	}
}

func (r execSubstitutionRunner) Run(ctx context.Context, expression string, input string) (string, error) {
	if strings.TrimSpace(expression) == "" {
		return "", fmt.Errorf("run substitution: empty expression")
	}

	commandCtx := ctx
	cancel := func() {}
	if r.timeout > 0 {
		commandCtx, cancel = context.WithTimeout(ctx, r.timeout)
	}
	defer cancel()

	// #nosec G204 -- This uses a fixed sed binary with argv, not a shell pipeline.
	cmd := exec.CommandContext(commandCtx, r.command, "-e", expression)
	cmd.Stdin = strings.NewReader(input + "\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrText := strings.TrimSpace(stderr.String())
		if stderrText != "" {
			return "", fmt.Errorf("run substitution %q: %w: %s", expression, err, stderrText)
		}
		return "", fmt.Errorf("run substitution %q: %w", expression, err)
	}

	result := stdout.String()
	result = strings.TrimSuffix(result, "\n")
	result = strings.TrimSuffix(result, "\r")

	return result, nil
}

func parseSubstitutionExpression(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "s/") {
		return "", false
	}

	parts := strings.Split(trimmed, "/")
	if len(parts) != 4 || parts[0] != "s" {
		return "", false
	}
	if parts[3] != "" && parts[3] != "g" {
		return "", false
	}

	return trimmed, true
}

func transformForYou(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, char := range text {
		switch char {
		case '我', '咱':
			builder.WriteRune('您')
		case '你', '您':
			builder.WriteRune('我')
		default:
			builder.WriteRune(char)
		}
	}

	return builder.String()
}

var collectivePronounReplacer = strings.NewReplacer(
	"我", "大伙自己",
	"你", "大伙自己",
	"您", "大伙自己",
)

func transformForWe(text string) string {
	return collectivePronounReplacer.Replace(text)
}
