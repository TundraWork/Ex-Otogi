package nbnhhsh

import (
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		configValue   any
		wantAPIURL    string
		wantTimeout   time.Duration
		wantErrSubstr string
	}{
		{
			name:        "missing config uses defaults",
			wantAPIURL:  defaultAPIURL,
			wantTimeout: defaultRequestTimeout,
		},
		{
			name: "custom config overrides defaults",
			configValue: map[string]string{
				"api_url":         "https://example.com/guess",
				"request_timeout": "4s",
			},
			wantAPIURL:  "https://example.com/guess",
			wantTimeout: 4 * time.Second,
		},
		{
			name: "partial config keeps default timeout",
			configValue: map[string]string{
				"api_url": "https://example.com/guess",
			},
			wantAPIURL:  "https://example.com/guess",
			wantTimeout: defaultRequestTimeout,
		},
		{
			name: "invalid timeout fails",
			configValue: map[string]string{
				"request_timeout": "soon",
			},
			wantErrSubstr: "parse request_timeout",
		},
		{
			name: "invalid url fails",
			configValue: map[string]string{
				"api_url": "ftp://example.com/guess",
			},
			wantErrSubstr: "api_url scheme must be http or https",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var registry otogi.ConfigRegistry
			if testCase.configValue != nil {
				configs := newConfigRegistryStub()
				mustRegisterConfig(t, configs, testCase.configValue)
				registry = configs
			}

			cfg, err := loadConfig(registry)
			if testCase.wantErrSubstr == "" && err != nil {
				t.Fatalf("loadConfig() unexpected error: %v", err)
			}
			if testCase.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", testCase.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), testCase.wantErrSubstr) {
					t.Fatalf("loadConfig() error = %v, want substring %q", err, testCase.wantErrSubstr)
				}
				return
			}

			if cfg.APIURL != testCase.wantAPIURL {
				t.Fatalf("api_url = %q, want %q", cfg.APIURL, testCase.wantAPIURL)
			}
			if cfg.RequestTimeout != testCase.wantTimeout {
				t.Fatalf("request_timeout = %s, want %s", cfg.RequestTimeout, testCase.wantTimeout)
			}
		})
	}
}
