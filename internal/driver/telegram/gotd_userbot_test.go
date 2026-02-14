package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGotdUserbotSourceConsume(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		run           func(ctx context.Context, fn func(context.Context) error) error
		rawUpdates    []any
		mapResult     Update
		mapAccepted   bool
		mapErr        error
		handlerErr    error
		wantErr       bool
		wantErrSubstr string
	}{
		{
			name: "context cancellation exits cleanly",
			run: func(ctx context.Context, fn func(context.Context) error) error {
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel()
				return fn(cancelCtx)
			},
			wantErr: false,
		},
		{
			name: "handler failure is wrapped",
			run: func(ctx context.Context, fn func(context.Context) error) error {
				return fn(ctx)
			},
			rawUpdates: []any{"raw-1"},
			mapResult: Update{
				Type: UpdateTypeMessage,
			},
			mapAccepted:   true,
			handlerErr:    errors.New("handler failed"),
			wantErr:       true,
			wantErrSubstr: "consume gotd update message",
		},
		{
			name: "mapper failure is wrapped",
			run: func(ctx context.Context, fn func(context.Context) error) error {
				return fn(ctx)
			},
			rawUpdates:    []any{"raw-2"},
			mapErr:        errors.New("map failed"),
			wantErr:       true,
			wantErrSubstr: "map gotd update",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			updates := make(chan any, len(testCase.rawUpdates))
			for _, raw := range testCase.rawUpdates {
				updates <- raw
			}
			close(updates)

			source, err := NewGotdUserbotSource(
				gotdTestClient{run: testCase.run},
				gotdTestStream{updates: updates},
				gotdTestMapper{
					result:   testCase.mapResult,
					accepted: testCase.mapAccepted,
					err:      testCase.mapErr,
				},
			)
			if err != nil {
				t.Fatalf("new source failed: %v", err)
			}

			err = source.Consume(context.Background(), func(_ context.Context, _ Update) error {
				return testCase.handlerErr
			})

			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErrSubstr != "" && (err == nil || !strings.Contains(err.Error(), testCase.wantErrSubstr)) {
				t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstr)
			}
		})
	}
}

type gotdTestClient struct {
	run func(ctx context.Context, fn func(context.Context) error) error
}

func (c gotdTestClient) Run(ctx context.Context, fn func(runCtx context.Context) error) error {
	return c.run(ctx, fn)
}

type gotdTestStream struct {
	updates <-chan any
}

func (s gotdTestStream) Updates(_ context.Context) (<-chan any, error) {
	return s.updates, nil
}

type gotdTestMapper struct {
	result   Update
	accepted bool
	err      error
}

func (m gotdTestMapper) Map(_ context.Context, _ any) (Update, bool, error) {
	if m.err != nil {
		return Update{}, false, m.err
	}
	return m.result, m.accepted, nil
}
