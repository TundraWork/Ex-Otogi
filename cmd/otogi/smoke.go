package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/capture"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/modules/echo"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// smoke runs one scripted "hello" event through the kernel and the echo
// module, offline, and prints the captured reply.
func smoke(ctx context.Context, output io.Writer) error {
	captured := capture.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	k, err := kernel.New(kernel.Config{}, []otogi.Module{echo.New()}, nil, captured, log)
	if err != nil {
		return err
	}
	if err := k.Start(ctx); err != nil {
		return err
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = k.Stop(stopCtx)
	}()

	outcomes := make(chan otogi.Outcome, 1)
	hello := otogi.Event{
		Platform: "scripted", Account: "local", Conversation: otogi.Conversation{ID: "smoke"},
		Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming,
		OccurredAt: time.Now(), Message: otogi.TextMessage("hello"),
	}
	if err := k.Submit(ctx, hello, func(o otogi.Outcome) { outcomes <- o }); err != nil {
		return err
	}
	outcome := <-outcomes
	if outcome.Err != nil || len(outcome.Modules) != 1 || outcome.Modules[0].Err != nil {
		return fmt.Errorf("smoke event failed: %+v", outcome)
	}
	effects := captured.Effects()
	if len(effects) != 1 {
		return fmt.Errorf("smoke: captured %d effects, want 1", len(effects))
	}
	_, err = fmt.Fprintln(output, effects[0].Message.PlainText())
	return err
}
