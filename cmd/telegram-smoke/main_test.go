package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunRejectsMissingConfigurationBeforeLiveCall(t *testing.T) {
	called := false
	var output bytes.Buffer
	err := run(context.Background(), func(string) string { return "" }, &output, func(context.Context, liveConfig) error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "OTOGI_TELEGRAM_APP_ID") {
		t.Fatalf("run error = %v", err)
	}
	if called {
		t.Fatal("live call happened before configuration validation")
	}
}

func TestRunDoesNotEchoSecrets(t *testing.T) {
	values := map[string]string{
		"OTOGI_TELEGRAM_APP_ID":       "12345",
		"OTOGI_TELEGRAM_APP_HASH":     "super-secret-hash",
		"OTOGI_TELEGRAM_SESSION_PATH": "/private/session",
	}
	var output bytes.Buffer
	want := context.Canceled
	err := run(context.Background(), func(key string) string { return values[key] }, &output, func(_ context.Context, got liveConfig) error {
		if got.AppID != 12345 || got.AppHash != values["OTOGI_TELEGRAM_APP_HASH"] {
			t.Fatalf("live config = %+v", got)
		}
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("run error = %v, want %v", err, want)
	}
	combined := output.String() + err.Error()
	if strings.Contains(combined, values["OTOGI_TELEGRAM_APP_HASH"]) {
		t.Fatal("secret was echoed")
	}
}

func TestRunRequiresOnlyConnectivityConfigurationAndReportsVerifiedConnectivity(t *testing.T) {
	values := map[string]string{
		"OTOGI_TELEGRAM_APP_ID":       "12345",
		"OTOGI_TELEGRAM_APP_HASH":     "super-secret-hash",
		"OTOGI_TELEGRAM_SESSION_PATH": "/private/session",
	}
	var output bytes.Buffer
	called := false
	err := run(context.Background(), func(key string) string { return values[key] }, &output, func(_ context.Context, got liveConfig) error {
		called = true
		if got.AppID != 12345 || got.AppHash != values["OTOGI_TELEGRAM_APP_HASH"] || got.SessionPath != values["OTOGI_TELEGRAM_SESSION_PATH"] {
			t.Fatalf("live config = %+v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("live call was not made")
	}
	if got, want := output.String(), "telegram smoke: session connectivity verified\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
