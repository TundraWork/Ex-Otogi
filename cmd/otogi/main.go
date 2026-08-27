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
	var outputText string
	if len(messages) == 1 && len(messages[0].Message().Parts()) == 1 {
		outputText, _, _ = messages[0].Message().Parts()[0].Text()
	}
	if outputText != "echo: hello" {
		return fmt.Errorf("verify capture: %w", otogi.ErrInvalidState)
	}
	if _, err := fmt.Fprintln(output, outputText); err != nil {
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
	action := otogi.ActionMessageCreated
	partID, err := otogi.NewPartID("part")
	if err != nil {
		return otogi.EventInput{}, err
	}
	part, err := otogi.NewTextPart(partID, "hello", nil, nil, nil)
	if err != nil {
		return otogi.EventInput{}, err
	}
	message, err := otogi.NewMessage([]otogi.PartID{partID}, []otogi.Part{part}, nil, nil)
	if err != nil {
		return otogi.EventInput{}, err
	}
	input, err := otogi.NewEventInput(otogi.EventInputParams{
		Platform: platform, Account: account, Tenant: tenant, Conversation: conversation,
		Action: action, Direction: otogi.DirectionIncoming, Completeness: otogi.CompletenessComplete, OccurredAt: now, IngestedAt: now, Message: message,
	})
	if err != nil {
		return otogi.EventInput{}, fmt.Errorf("construct hello event: %w", err)
	}
	return input, nil
}
