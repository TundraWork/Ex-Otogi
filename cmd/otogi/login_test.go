package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// script is an operator at the terminal: it answers each question from its
// list, and keeps what it was asked and told.
type script struct {
	answers []string
	heard   []string
}

func (s *script) answer(question string) (string, error) {
	s.heard = append(s.heard, question)
	if len(s.answers) == 0 {
		return "", errors.New("asked " + question + " with nothing left to answer")
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	return a, nil
}

func (s *script) Phone(context.Context) (string, error) { return s.answer("phone") }
func (s *script) Code(context.Context) (string, error)  { return s.answer("code") }
func (s *script) Password(_ context.Context, hint string) (string, error) {
	return s.answer("password (" + hint + ")")
}

func (s *script) Refused(err error) error {
	s.heard = append(s.heard, "refused: "+err.Error())
	return nil
}

// telegramOf is a Telegram whose account +8190 logs in with code 12345, or
// sends a new code after the first has expired, and with password secret
// when withPassword.
func telegramOf(withPassword, expireFirst bool) loginSteps {
	sent := 0
	return loginSteps{
		loggedIn: func(context.Context, telegram.SessionConfig) (string, bool, error) { return "", false, nil },
		sendCode: func(_ context.Context, _ telegram.SessionConfig, phone string) (telegram.SentCode, error) {
			if phone != "+8190" {
				return telegram.SentCode{}, &otogi.SettingError{Path: "phone", Err: errors.New("not a number")}
			}
			sent++
			return telegram.SentCode{Hash: "h" + strings.Repeat("i", sent), Via: "app"}, nil
		},
		signIn: func(_ context.Context, _ telegram.SessionConfig, phone, hash, code string) (string, error) {
			switch {
			case expireFirst && hash == "hi":
				return "", &otogi.SettingError{Path: "code", Err: telegram.ErrCodeExpired}
			case code != "12345":
				return "", &otogi.SettingError{Path: "code", Err: errors.New("wrong code")}
			case withPassword:
				return "", &telegram.PasswordNeededError{Hint: "the cat"}
			}
			return "Mio (@mio, id 7)", nil
		},
		password: func(_ context.Context, _ telegram.SessionConfig, password string) (string, error) {
			if password != "secret" {
				return "", &otogi.SettingError{Path: "password", Err: errors.New("wrong password")}
			}
			return "Mio (@mio, id 7)", nil
		},
	}
}

func TestLoginAlreadyLoggedIn(t *testing.T) {
	t.Parallel()
	steps := telegramOf(false, false)
	steps.loggedIn = func(context.Context, telegram.SessionConfig) (string, bool, error) {
		return "Mio (@mio, id 7)", true, nil
	}
	s := &script{}
	if who, err := logIn(context.Background(), steps, telegram.SessionConfig{}, s); err != nil || who != "Mio (@mio, id 7)" || len(s.heard) != 0 {
		t.Fatalf("logged in as %q, %v, asked %q", who, err, s.heard)
	}
}

// What Telegram refuses is asked for again, after saying why.
func TestLoginAsksAgainForAWrongCode(t *testing.T) {
	t.Parallel()
	s := &script{answers: []string{"12", "+8190", "11111", "12345"}}
	who, err := logIn(context.Background(), telegramOf(false, false), telegram.SessionConfig{}, s)
	want := []string{"phone", "refused: phone: not a number", "phone", "code", "refused: code: wrong code", "code"}
	if err != nil || who != "Mio (@mio, id 7)" || !slices.Equal(s.heard, want) {
		t.Fatalf("logged in as %q, %v, heard %q", who, err, s.heard)
	}
}

// An expired code is sent again, to the same number.
func TestLoginSendsAnExpiredCodeAgain(t *testing.T) {
	t.Parallel()
	s := &script{answers: []string{"+8190", "12345", "12345"}}
	who, err := logIn(context.Background(), telegramOf(false, true), telegram.SessionConfig{}, s)
	want := []string{"phone", "code", "refused: code: " + telegram.ErrCodeExpired.Error(), "code"}
	if err != nil || who != "Mio (@mio, id 7)" || !slices.Equal(s.heard, want) {
		t.Fatalf("logged in as %q, %v, heard %q", who, err, s.heard)
	}
}

func TestLoginWithPassword(t *testing.T) {
	t.Parallel()
	s := &script{answers: []string{"+8190", "12345", "guess", "secret"}}
	who, err := logIn(context.Background(), telegramOf(true, false), telegram.SessionConfig{}, s)
	want := []string{"phone", "code", "password (the cat)", "refused: password: wrong password", "password (the cat)"}
	if err != nil || who != "Mio (@mio, id 7)" || !slices.Equal(s.heard, want) {
		t.Fatalf("logged in as %q, %v, heard %q", who, err, s.heard)
	}
}

// A failure that is not a typing mistake ends the login.
func TestLoginStopsOnAFailure(t *testing.T) {
	t.Parallel()
	steps := telegramOf(false, false)
	steps.sendCode = func(context.Context, telegram.SessionConfig, string) (telegram.SentCode, error) {
		return telegram.SentCode{}, otogi.ErrUnavailable
	}
	s := &script{answers: []string{"+8190"}}
	if _, err := logIn(context.Background(), steps, telegram.SessionConfig{}, s); !errors.Is(err, otogi.ErrUnavailable) {
		t.Fatalf("error %v", err)
	}
}

func TestLoginAnUnknownAccount(t *testing.T) {
	t.Parallel()
	s := &moduleSettings{store: openStore(t), kinds: moduleKinds}
	err := login(context.Background(), s, Config{DataDir: t.TempDir()}, "nobody")
	if err == nil || !strings.Contains(err.Error(), `no Telegram account "nobody" among the accounts set in the panel`) {
		t.Fatalf("error %v", err)
	}
}
