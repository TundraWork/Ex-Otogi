package sleep

import (
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

func TestCodeManagerValidate(t *testing.T) {
	t.Parallel()

	cm, err := newCodeManager(testSigningKey())
	if err != nil {
		t.Fatalf("newCodeManager() error: %v", err)
	}

	scope := codeScope{
		UserID:           "user-456",
		ConversationID:   "chat-123",
		ConversationType: otogi.ConversationTypeGroup,
		SourcePlatform:   otogi.PlatformTelegram,
		SourceID:         "tg-main",
	}
	untilDate := time.Unix(60*100+30, 0).UTC()
	code, err := cm.Generate(scope, untilDate)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if code == "" {
		t.Fatal("generated code is empty")
	}
	if len(code) <= 20 {
		t.Fatalf("code length = %d, want > 20 for embedded wake target", len(code))
	}

	tests := []struct {
		name    string
		userID  string
		now     time.Time
		wantErr bool
		want    wakeTargetScope
	}{
		{
			name:   "valid with same user",
			userID: scope.UserID,
			now:    time.Unix(60*101+59, 0).UTC(),
			want:   scope.target(),
		},
		{
			name:    "wrong user",
			userID:  "user-other",
			now:     time.Unix(60*101+59, 0).UTC(),
			wantErr: true,
		},
		{
			name:   "returns embedded restore target",
			userID: scope.UserID,
			now:    time.Unix(60*101+59, 0).UTC(),
			want:   scope.target(),
		},
		{
			name:    "expired",
			userID:  scope.UserID,
			now:     time.Unix(60*102, 0).UTC(),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := cm.Validate(code, tc.userID, tc.now)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error for invalid code")
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Validate() target = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestCodeManagerValidateAcrossRestart(t *testing.T) {
	t.Parallel()

	scope := codeScope{
		UserID:           "user-456",
		ConversationID:   "chat-123",
		ConversationType: otogi.ConversationTypeGroup,
		SourcePlatform:   otogi.PlatformTelegram,
		SourceID:         "tg-main",
	}
	untilDate := time.Unix(60*100+30, 0).UTC()

	generator, err := newCodeManager(testSigningKey())
	if err != nil {
		t.Fatalf("newCodeManager() generator error: %v", err)
	}
	validator, err := newCodeManager(testSigningKey())
	if err != nil {
		t.Fatalf("newCodeManager() validator error: %v", err)
	}

	code, err := generator.Generate(scope, untilDate)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	target, err := validator.Validate(code, scope.UserID, time.Unix(60*101+59, 0).UTC())
	if err != nil {
		t.Fatalf("Validate() error after restart: %v", err)
	}
	if target != scope.target() {
		t.Fatalf("Validate() target = %#v, want %#v", target, scope.target())
	}
}

func TestCodeManagerValidateMalformed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
	}{
		{
			name: "empty code",
			code: "",
		},
		{
			name: "invalid base64",
			code: "!!!not-valid-base64!!!",
		},
		{
			name: "wrong size",
			code: "Y2hhdC0xMjM",
		},
		{
			name: "wrong version",
			code: "AgAAAAAAAAAAAAAAAAAA",
		},
	}

	cm, err := newCodeManager(testSigningKey())
	if err != nil {
		t.Fatalf("newCodeManager() error: %v", err)
	}
	scope := codeScope{
		UserID:           "user-1",
		ConversationID:   "chat-123",
		ConversationType: otogi.ConversationTypeGroup,
		SourcePlatform:   otogi.PlatformTelegram,
		SourceID:         "tg-main",
	}
	now := time.Unix(60*100, 0).UTC()

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := cm.Validate(tc.code, scope.UserID, now); err == nil {
				t.Fatal("expected error for malformed code")
			}
		})
	}
}
