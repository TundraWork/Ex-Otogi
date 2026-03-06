package sleep

import (
	"testing"
	"time"
)

func TestParseSleepDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantDur time.Duration
		wantErr bool
	}{
		{
			name:    "go duration minutes",
			input:   "30m",
			wantDur: 30 * time.Minute,
		},
		{
			name:    "go duration hours and minutes",
			input:   "1h30m",
			wantDur: 90 * time.Minute,
		},
		{
			name:    "plain integer as seconds",
			input:   "3600",
			wantDur: time.Hour,
		},
		{
			name:    "one second minimum",
			input:   "1s",
			wantDur: time.Second,
		},
		{
			name:    "twelve hours maximum",
			input:   "12h",
			wantDur: 12 * time.Hour,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "zero duration",
			input:   "0",
			wantErr: true,
		},
		{
			name:    "negative duration",
			input:   "-5m",
			wantErr: true,
		},
		{
			name:    "exceeds maximum",
			input:   "13h",
			wantErr: true,
		},
		{
			name:    "invalid format",
			input:   "abc",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dur, err := parseSleepDuration(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dur != tc.wantDur {
				t.Fatalf("duration = %v, want %v", dur, tc.wantDur)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dur  time.Duration
		want string
	}{
		{
			name: "hours and minutes",
			dur:  2*time.Hour + 30*time.Minute,
			want: "2小时30分钟",
		},
		{
			name: "hours only",
			dur:  3 * time.Hour,
			want: "3小时",
		},
		{
			name: "minutes only",
			dur:  45 * time.Minute,
			want: "45分钟",
		},
		{
			name: "seconds only",
			dur:  30 * time.Second,
			want: "30秒",
		},
		{
			name: "minutes and seconds",
			dur:  5*time.Minute + 10*time.Second,
			want: "5分钟10秒",
		},
		{
			name: "zero duration",
			dur:  0,
			want: "0秒",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := formatDuration(tc.dur)
			if got != tc.want {
				t.Fatalf("formatDuration(%v) = %q, want %q", tc.dur, got, tc.want)
			}
		})
	}
}
