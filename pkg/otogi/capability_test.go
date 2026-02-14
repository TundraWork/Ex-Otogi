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
			name: "require message matches when message is present",
			interest: InterestSet{
				Kinds:          []EventKind{EventKindMessageCreated},
				RequireMessage: true,
			},
			event: &Event{
				Kind:    EventKindMessageCreated,
				Message: &Message{ID: "m1"},
			},
			want: true,
		},
		{
			name: "require message rejects missing message",
			interest: InterestSet{
				Kinds:          []EventKind{EventKindMessageCreated},
				RequireMessage: true,
			},
			event: &Event{
				Kind: EventKindMessageCreated,
			},
			want: false,
		},
		{
			name: "require message rejects nil event",
			interest: InterestSet{
				RequireMessage: true,
			},
			event: nil,
			want:  false,
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
			name: "require message allows equal strictness",
			allowed: InterestSet{
				Kinds:          []EventKind{EventKindMessageCreated},
				RequireMessage: true,
			},
			filter: InterestSet{
				Kinds:          []EventKind{EventKindMessageCreated},
				RequireMessage: true,
			},
			wantAllow: true,
		},
		{
			name: "require message rejects weaker filter",
			allowed: InterestSet{
				Kinds:          []EventKind{EventKindMessageCreated},
				RequireMessage: true,
			},
			filter: InterestSet{
				Kinds: []EventKind{EventKindMessageCreated},
			},
			wantAllow: false,
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
