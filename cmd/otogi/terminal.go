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

func (t *terminal) Password(ctx context.Context) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return t.prompt(ctx, "Two-step verification password: ")
	}
	if _, err := fmt.Fprint(t.out, "Two-step verification password: "); err != nil {
		return "", fmt.Errorf("prompt: %w", err)
	}
	password, err := term.ReadPassword(fd)
	_, _ = fmt.Fprintln(t.out) // end the prompt line; the password is already read
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(password), nil
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
