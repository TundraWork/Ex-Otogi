package discord

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseRuntimeConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		raw           []byte
		wantErr       bool
		wantErrSubstr string
		wantToken     string
		wantPublish   time.Duration
		wantBuffer    int
		wantDownload  time.Duration
	}{
		{
			name:    "empty config returns error",
			raw:     nil,
			wantErr: true,
		},
		{
			name:    "invalid json returns error",
			raw:     []byte(`{not json}`),
			wantErr: true,
		},
		{
			name:          "missing bot_token returns error",
			raw:           []byte(`{"publish_timeout": "2s"}`),
			wantErr:       true,
			wantErrSubstr: "bot_token",
		},
		{
			name:         "minimal valid config applies defaults",
			raw:          []byte(`{"bot_token": "tok-abc"}`),
			wantToken:    "tok-abc",
			wantPublish:  defaultRuntimePublishTimeout,
			wantBuffer:   defaultRuntimeUpdateBuffer,
			wantDownload: defaultDiscordMediaDownloadTimeout,
		},
		{
			name: "all fields parsed",
			raw: mustMarshal(t, map[string]any{
				"bot_token":        "tok-xyz",
				"publish_timeout":  "10s",
				"update_buffer":    512,
				"download_timeout": "1m",
			}),
			wantToken:    "tok-xyz",
			wantPublish:  10 * time.Second,
			wantBuffer:   512,
			wantDownload: time.Minute,
		},
		{
			name: "invalid publish_timeout returns error",
			raw: mustMarshal(t, map[string]any{
				"bot_token":       "tok",
				"publish_timeout": "notaduration",
			}),
			wantErr:       true,
			wantErrSubstr: "publish_timeout",
		},
		{
			name: "zero publish_timeout returns error",
			raw: mustMarshal(t, map[string]any{
				"bot_token":       "tok",
				"publish_timeout": "0s",
			}),
			wantErr:       true,
			wantErrSubstr: "publish_timeout",
		},
		{
			name: "invalid download_timeout returns error",
			raw: mustMarshal(t, map[string]any{
				"bot_token":        "tok",
				"download_timeout": "bad",
			}),
			wantErr:       true,
			wantErrSubstr: "download_timeout",
		},
		{
			name: "zero update_buffer falls back to default",
			raw: mustMarshal(t, map[string]any{
				"bot_token":     "tok",
				"update_buffer": 0,
			}),
			wantToken:    "tok",
			wantBuffer:   defaultRuntimeUpdateBuffer,
			wantPublish:  defaultRuntimePublishTimeout,
			wantDownload: defaultDiscordMediaDownloadTimeout,
		},
		{
			name: "bot_token is trimmed",
			raw: mustMarshal(t, map[string]any{
				"bot_token": "  tok-trimmed  ",
			}),
			wantToken:    "tok-trimmed",
			wantPublish:  defaultRuntimePublishTimeout,
			wantBuffer:   defaultRuntimeUpdateBuffer,
			wantDownload: defaultDiscordMediaDownloadTimeout,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := parseRuntimeConfig(testCase.raw)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if testCase.wantErrSubstr != "" && !containsStr(err.Error(), testCase.wantErrSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), testCase.wantErrSubstr)
				}

				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if testCase.wantToken != "" && cfg.botToken != testCase.wantToken {
				t.Errorf("botToken = %q, want %q", cfg.botToken, testCase.wantToken)
			}
			if testCase.wantPublish != 0 && cfg.publishTimeout != testCase.wantPublish {
				t.Errorf("publishTimeout = %v, want %v", cfg.publishTimeout, testCase.wantPublish)
			}
			if testCase.wantBuffer != 0 && cfg.updateBuffer != testCase.wantBuffer {
				t.Errorf("updateBuffer = %d, want %d", cfg.updateBuffer, testCase.wantBuffer)
			}
			if testCase.wantDownload != 0 && cfg.downloadTimeout != testCase.wantDownload {
				t.Errorf("downloadTimeout = %v, want %v", cfg.downloadTimeout, testCase.wantDownload)
			}
		})
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustMarshal: %v", err)
	}

	return b
}
