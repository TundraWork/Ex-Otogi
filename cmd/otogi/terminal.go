package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// terminal prompts for Telegram login credentials on stderr and reads stdin.
type terminal struct {
	in  *bufio.Reader
	out io.Writer
}

func newTerminal() *terminal { return &terminal{in: bufio.NewReader(os.Stdin), out: os.Stderr} }

func (t *terminal) Phone(ctx context.Context) (string, error) {
	return t.prompt(ctx, "Phone number (international format): ")
}

func (t *terminal) Code(ctx context.Context) (string, error) {
	return t.prompt(ctx, "Login code: ")
}

func (t *terminal) Password(ctx context.Context, hint string) (string, error) {
	question := "Two-step verification password: "
	if hint != "" {
		question = "Two-step verification password (hint: " + hint + "): "
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return t.prompt(ctx, question)
	}
	if _, err := fmt.Fprint(t.out, question); err != nil {
		return "", fmt.Errorf("prompt: %w", err)
	}
	password, err := term.ReadPassword(fd)
	_, _ = fmt.Fprintln(t.out) // end the prompt line; the password is already read
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(password), nil
}

// Refused says what Telegram refused, before the question is asked again.
func (t *terminal) Refused(err error) error {
	if _, werr := fmt.Fprintln(t.out, err); werr != nil {
		return fmt.Errorf("prompt: %w", werr)
	}
	return nil
}

func (t *terminal) prompt(ctx context.Context, question string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := fmt.Fprint(t.out, question); err != nil {
		return "", fmt.Errorf("prompt: %w", err)
	}
	line, err := t.in.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read answer: %w", err)
	}
	return strings.TrimSpace(line), nil
}
