// Command otogi runs the experimental scripted event bridge.
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/capture"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/scripted"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/modules/echo"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const cleanupTimeout = 5 * time.Second

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, output io.Writer) (runErr error) {
	captured := capture.New()
	generator, err := otogi.NewEventIDGenerator(rand.Reader)
	if err != nil {
		return fmt.Errorf("construct event ID generator: %w", err)
	}
	runtime, err := kernel.New(kernel.DefaultConfig(), []otogi.Module{echo.New()}, captured, generator)
	if err != nil {
		return fmt.Errorf("construct kernel: %w", err)
	}
	cleanup := func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		return runtime.Stop(cleanupCtx)
	}
	defer func() {
		if err := cleanup(); runErr == nil && err != nil {
			runErr = fmt.Errorf("stop kernel: %w", err)
		}
	}()

	input, err := helloInput(time.Now().UTC())
	if err != nil {
		return err
	}
	if err := runtime.Start(ctx); err != nil {
		return fmt.Errorf("start kernel: %w", err)
	}
	source := scripted.New([]otogi.EventInput{input})
	if err := source.Run(ctx, kernelSink{runtime: runtime}); err != nil {
		return err
	}
	if err := runtime.Drain(ctx); err != nil {
		return fmt.Errorf("drain kernel: %w", err)
	}

	deliveries := runtime.Deliveries()
	messages := captured.Messages()
	if len(deliveries) != 1 || deliveries[0].Status() != otogi.ResultSucceeded || deliveries[0].Err() != nil {
		return fmt.Errorf("verify delivery: %w", otogi.ErrInvalidState)
	}
	if len(messages) != 1 || messages[0].Text().String() != "echo: hello" {
		return fmt.Errorf("verify capture: %w", otogi.ErrInvalidState)
	}
	if _, err := fmt.Fprintln(output, messages[0].Text().String()); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

type kernelSink struct {
	runtime *kernel.Kernel
}

func (s kernelSink) Submit(ctx context.Context, input otogi.EventInput) error {
	_, err := s.runtime.Submit(ctx, input)
	return err
}

func helloInput(now time.Time) (otogi.EventInput, error) {
	platform, err := otogi.NewPlatformID("scripted")
	if err != nil {
		return otogi.EventInput{}, err
	}
	account, err := otogi.NewAccountID("local")
	if err != nil {
		return otogi.EventInput{}, err
	}
	tenant, err := otogi.NewTenantID("default")
	if err != nil {
		return otogi.EventInput{}, err
	}
	conversation, err := otogi.NewConversationID("smoke")
	if err != nil {
		return otogi.EventInput{}, err
	}
	kind, err := otogi.NewEventKind("text.message")
	if err != nil {
		return otogi.EventInput{}, err
	}
	text, err := otogi.NewText("hello")
	if err != nil {
		return otogi.EventInput{}, err
	}
	input, err := otogi.NewEventInput(otogi.EventInputParams{
		Platform: platform, Account: account, Tenant: tenant, Conversation: conversation,
		Kind: kind, SchemaVersion: 1, OccurredAt: now, IngestedAt: now, Text: text,
	})
	if err != nil {
		return otogi.EventInput{}, fmt.Errorf("construct hello event: %w", err)
	}
	return input, nil
}
