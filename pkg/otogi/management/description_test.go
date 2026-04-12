package management

import (
	"fmt"
	"strings"
	"testing"
)

func TestTruncateDescription(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "whitespace only", input: "   \t\n  ", want: ""},
		{name: "short text", input: "hello world", want: "hello world"},
		{name: "exactly 140 runes", input: strings.Repeat("x", 140), want: strings.Repeat("x", 140)},
		{name: "141 runes gets truncated", input: strings.Repeat("x", 141), want: strings.Repeat("x", 140) + "..."},
		{name: "whitespace normalization", input: "hello   world\t\tfoo", want: "hello world foo"},
		{name: "leading/trailing whitespace", input: "  hello  ", want: "hello"},
		{name: "unicode preserved", input: strings.Repeat("世", 141), want: strings.Repeat("世", 140) + "..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateDescription(tt.input)
			if got != tt.want {
				t.Errorf("TruncateDescription(%q) = %q, want %q", tt.input, got, tt.want)
			}
			// Verify the result never exceeds maxDescriptionRunes + 3 (for "...")
			if len([]rune(got)) > maxDescriptionRunes+3 {
				t.Errorf("result too long: %d runes", len([]rune(got)))
			}
		})
	}
}

func TestTruncateDescriptionLongInput(t *testing.T) {
	long := fmt.Sprintf("This is a long article text. %s", strings.Repeat("blah ", 50))
	got := TruncateDescription(long)
	if len([]rune(got)) > maxDescriptionRunes+3 {
		t.Errorf("result too long: %d runes, want at most %d", len([]rune(got)), maxDescriptionRunes+3)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ellipsis suffix, got: %q", got)
	}
}
