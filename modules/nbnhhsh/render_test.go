package nbnhhsh

import (
	"strings"
	"testing"

	"ex-otogi/pkg/otogi/platform"
)

func TestExtractAbbreviations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "extracts unique abbreviations in order",
			input: "yyds, xswl! 114514 yyds srh",
			want:  []string{"yyds", "xswl", "114514", "srh"},
		},
		{
			name:  "mixed text without abbreviations",
			input: "你好，世界",
			want:  nil,
		},
		{
			name:  "letters and digits stay grouped",
			input: "abc123 xyz789",
			want:  []string{"abc123", "xyz789"},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := extractAbbreviations(testCase.input)
			if strings.Join(got, ",") != strings.Join(testCase.want, ",") {
				t.Fatalf("extractAbbreviations(%q) = %v, want %v", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestUsageMessageHasValidEntities(t *testing.T) {
	t.Parallel()

	message := usageMessage()
	if !strings.Contains(message.Text, "/nbnhhsh <欲翻译的内容>") {
		t.Fatalf("usage text = %q, missing main command usage", message.Text)
	}
	if !strings.Contains(message.Text, "/srh") {
		t.Fatalf("usage text = %q, missing alias command", message.Text)
	}
	if err := platform.ValidateTextEntities(message.Text, message.Entities); err != nil {
		t.Fatalf("usage entities invalid: %v", err)
	}
}

func TestRenderGuessResultsHasValidEntities(t *testing.T) {
	t.Parallel()

	message := renderGuessResults([]guessResult{
		{Name: "yyds", Trans: []string{"永远的神"}},
		{Name: "xswl", Trans: []string{"笑死我了", "笑死我啦"}},
		{Name: "dbq", Inputting: []string{"对不起"}},
		{Name: "orz"},
	})

	for _, want := range []string{
		"yyds",
		"永远的神",
		"xswl",
		"笑死我了 / 笑死我啦",
		"dbq",
		"对不起",
		"orz",
		"/translate",
	} {
		if !strings.Contains(message.Text, want) {
			t.Fatalf("rendered text = %q, missing %q", message.Text, want)
		}
	}
	if err := platform.ValidateTextEntities(message.Text, message.Entities); err != nil {
		t.Fatalf("rendered entities invalid: %v", err)
	}
}

func TestRenderGuessResultsEmpty(t *testing.T) {
	t.Parallel()

	message := renderGuessResults(nil)
	if message.Text != emptyResultMessage {
		t.Fatalf("empty rendered text = %q, want %q", message.Text, emptyResultMessage)
	}
	if len(message.Entities) != 0 {
		t.Fatalf("empty rendered entities = %v, want none", message.Entities)
	}
}
