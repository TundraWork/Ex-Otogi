package otogi

import "testing"

func TestInterestSetMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		interest InterestSet
		event    *Event
		want     bool
	}{
		{
			name: "require article matches when article is present",
			interest: InterestSet{
				Kinds:          []EventKind{EventKindArticleCreated},
				RequireArticle: true,
			},
			event: &Event{
				Kind:    EventKindArticleCreated,
				Article: &Article{ID: "m1"},
			},
			want: true,
		},
		{
			name: "require article rejects missing article",
			interest: InterestSet{
				Kinds:          []EventKind{EventKindArticleCreated},
				RequireArticle: true,
			},
			event: &Event{
				Kind: EventKindArticleCreated,
			},
			want: false,
		},
		{
			name: "require article rejects nil event",
			interest: InterestSet{
				RequireArticle: true,
			},
			event: nil,
			want:  false,
		},
		{
			name: "source filter matches platform wildcard",
			interest: InterestSet{
				Sources: []EventSource{
					{Platform: PlatformTelegram},
				},
			},
			event: &Event{
				Kind:   EventKindArticleCreated,
				Source: EventSource{Platform: PlatformTelegram, ID: "tg-main"},
			},
			want: true,
		},
		{
			name: "source filter rejects mismatch",
			interest: InterestSet{
				Sources: []EventSource{
					{Platform: PlatformTelegram, ID: "tg-main"},
				},
			},
			event: &Event{
				Kind:   EventKindArticleCreated,
				Source: EventSource{Platform: PlatformTelegram, ID: "tg-alt"},
			},
			want: false,
		},
		{
			name: "require command and command name matches",
			interest: InterestSet{
				Kinds:          []EventKind{EventKindCommandReceived},
				RequireCommand: true,
				CommandNames:   []string{"raw"},
			},
			event: &Event{
				Kind:    EventKindCommandReceived,
				Command: &CommandInvocation{Name: "raw"},
			},
			want: true,
		},
		{
			name: "command name mismatch rejects",
			interest: InterestSet{
				Kinds:        []EventKind{EventKindCommandReceived},
				CommandNames: []string{"raw"},
			},
			event: &Event{
				Kind:    EventKindCommandReceived,
				Command: &CommandInvocation{Name: "history"},
			},
			want: false,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := testCase.interest.Matches(testCase.event)
			if got != testCase.want {
				t.Fatalf("matches = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestInterestSetAllows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		allowed   InterestSet
		filter    InterestSet
		wantAllow bool
	}{
		{
			name: "require article allows equal strictness",
			allowed: InterestSet{
				Kinds:          []EventKind{EventKindArticleCreated},
				RequireArticle: true,
			},
			filter: InterestSet{
				Kinds:          []EventKind{EventKindArticleCreated},
				RequireArticle: true,
			},
			wantAllow: true,
		},
		{
			name: "require article rejects weaker filter",
			allowed: InterestSet{
				Kinds:          []EventKind{EventKindArticleCreated},
				RequireArticle: true,
			},
			filter: InterestSet{
				Kinds: []EventKind{EventKindArticleCreated},
			},
			wantAllow: false,
		},
		{
			name: "command names allow subset",
			allowed: InterestSet{
				Kinds:        []EventKind{EventKindCommandReceived},
				CommandNames: []string{"raw", "history"},
			},
			filter: InterestSet{
				Kinds:        []EventKind{EventKindCommandReceived},
				CommandNames: []string{"raw"},
			},
			wantAllow: true,
		},
		{
			name: "require command rejects weaker filter",
			allowed: InterestSet{
				Kinds:          []EventKind{EventKindCommandReceived},
				RequireCommand: true,
			},
			filter: InterestSet{
				Kinds: []EventKind{EventKindCommandReceived},
			},
			wantAllow: false,
		},
		{
			name: "source filter allows subset",
			allowed: InterestSet{
				Sources: []EventSource{
					{Platform: PlatformTelegram},
				},
			},
			filter: InterestSet{
				Sources: []EventSource{
					{Platform: PlatformTelegram, ID: "tg-main"},
				},
			},
			wantAllow: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := testCase.allowed.Allows(testCase.filter)
			if got != testCase.wantAllow {
				t.Fatalf("allows = %v, want %v", got, testCase.wantAllow)
			}
		})
	}
}

func TestNewDefaultSubscriptionSpec(t *testing.T) {
	t.Parallel()

	spec := NewDefaultSubscriptionSpec("worker")
	if spec.Name != "worker" {
		t.Fatalf("name = %s, want worker", spec.Name)
	}
	if spec.Buffer != 0 {
		t.Fatalf("buffer = %d, want 0", spec.Buffer)
	}
	if spec.Workers != 0 {
		t.Fatalf("workers = %d, want 0", spec.Workers)
	}
	if spec.HandlerTimeout != 0 {
		t.Fatalf("handler timeout = %s, want 0", spec.HandlerTimeout)
	}
	if spec.Backpressure != "" {
		t.Fatalf("backpressure = %q, want empty", spec.Backpressure)
	}
}
