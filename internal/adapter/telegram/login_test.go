package telegram

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// telegramAnswers is a Telegram that answers each request by its type.
func telegramAnswers(answers func(req bin.Encoder) (bin.Encoder, error)) steps {
	return newSteps(tg.NewClient(rpc(func(_ context.Context, req bin.Encoder) (bin.Encoder, error) { return answers(req) })), 1, "hash")
}

var mio = &tg.User{ID: 7, FirstName: "Mio", LastName: "Akiyama", Username: "mio"}

const mioIs = "Mio Akiyama (@mio, id 7)"

func settingPath(t *testing.T, err error) string {
	t.Helper()
	var setting *otogi.SettingError
	if !errors.As(err, &setting) || !errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("error %v, want a *otogi.SettingError", err)
	}
	return setting.Path
}

func TestLoggedIn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	in := telegramAnswers(func(bin.Encoder) (bin.Encoder, error) { return &tg.UserClassVector{Elems: []tg.UserClass{mio}}, nil })
	if who, ok, err := in.loggedIn(ctx); err != nil || !ok || who != mioIs {
		t.Fatalf("logged in: %q %t %v", who, ok, err)
	}
	out := telegramAnswers(func(bin.Encoder) (bin.Encoder, error) { return nil, tgerr.New(401, "AUTH_KEY_UNREGISTERED") })
	if who, ok, err := out.loggedIn(ctx); err != nil || ok || who != "" {
		t.Fatalf("not logged in: %q %t %v", who, ok, err)
	}
}

func TestSendCode(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		typ    tg.AuthSentCodeTypeClass
		via    string
		length int
	}{
		{"app", &tg.AuthSentCodeTypeApp{Length: 5}, "app", 5},
		{"sms", &tg.AuthSentCodeTypeSMS{Length: 6}, "sms", 6},
		{"word", &tg.AuthSentCodeTypeSMSWord{}, "sms", 0},
		{"email", &tg.AuthSentCodeTypeEmailCode{EmailPattern: "m***@example.com", Length: 6}, "email", 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			s := telegramAnswers(func(req bin.Encoder) (bin.Encoder, error) {
				if r, ok := req.(*tg.AuthSendCodeRequest); !ok || r.PhoneNumber != "+8190" || r.APIID != 1 || r.APIHash != "hash" {
					t.Errorf("request %#v", req)
				}
				return &tg.AuthSentCode{Type: test.typ, PhoneCodeHash: "h1"}, nil
			})
			sent, err := s.sendCode(context.Background(), "+8190")
			if err != nil || sent != (SentCode{Hash: "h1", Via: test.via, Length: test.length}) {
				t.Fatalf("sent %+v, %v", sent, err)
			}
		})
	}
}

// Telegram may answer that the session is logged in already: no code went.
func TestSendCodeAlreadyLoggedIn(t *testing.T) {
	t.Parallel()
	s := telegramAnswers(func(bin.Encoder) (bin.Encoder, error) {
		return &tg.AuthSentCodeSuccess{Authorization: &tg.AuthAuthorization{User: mio}}, nil
	})
	if sent, err := s.sendCode(context.Background(), "+8190"); err != nil || sent != (SentCode{Who: mioIs}) {
		t.Fatalf("sent %+v, %v", sent, err)
	}
}

// What Telegram refuses to send a code for is the phone number's to fix,
// in an official app when Telegram asks for something the bot cannot do.
func TestSendCodeRefusals(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]func() (bin.Encoder, error){
		"payment": func() (bin.Encoder, error) { return &tg.AuthSentCodePaymentRequired{PhoneCodeHash: "h"}, nil },
		"email": func() (bin.Encoder, error) {
			return &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeSetUpEmailRequired{}, PhoneCodeHash: "h"}, nil
		},
		"invalid":    func() (bin.Encoder, error) { return nil, tgerr.New(400, "PHONE_NUMBER_INVALID") },
		"unoccupied": func() (bin.Encoder, error) { return nil, tgerr.New(400, "PHONE_NUMBER_UNOCCUPIED") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := telegramAnswers(func(bin.Encoder) (bin.Encoder, error) { return answer() }).sendCode(context.Background(), "+8190")
			if path := settingPath(t, err); path != "phone" {
				t.Fatalf("path %q of %v", path, err)
			}
		})
	}
}

func TestSignIn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ok := telegramAnswers(func(req bin.Encoder) (bin.Encoder, error) {
		if r, is := req.(*tg.AuthSignInRequest); !is || r.PhoneNumber != "+8190" || r.PhoneCodeHash != "h1" || r.PhoneCode != "12345" {
			t.Errorf("request %#v", req)
		}
		return &tg.AuthAuthorization{User: mio}, nil
	})
	if who, err := ok.signIn(ctx, "+8190", "h1", "12345"); err != nil || who != mioIs {
		t.Fatalf("signed in as %q, %v", who, err)
	}

	password := telegramAnswers(func(req bin.Encoder) (bin.Encoder, error) {
		switch req.(type) {
		case *tg.AuthSignInRequest:
			return nil, tgerr.New(401, "SESSION_PASSWORD_NEEDED")
		case *tg.AccountGetPasswordRequest:
			p := &tg.AccountPassword{Hint: "the cat", NewAlgo: &tg.PasswordKdfAlgoUnknown{}, NewSecureAlgo: &tg.SecurePasswordKdfAlgoUnknown{}}
			p.SetFlags()
			return p, nil
		}
		return nil, errors.New("unexpected")
	})
	_, err := password.signIn(ctx, "+8190", "h1", "12345")
	var needed *PasswordNeededError
	if !errors.As(err, &needed) || needed.Hint != "the cat" {
		t.Fatalf("with two-step verification: %v", err)
	}

	for name, test := range map[string]struct {
		answer error
		target error
	}{
		"wrong":   {tgerr.New(400, "PHONE_CODE_INVALID"), nil},
		"expired": {tgerr.New(400, "PHONE_CODE_EXPIRED"), ErrCodeExpired},
	} {
		_, err := telegramAnswers(func(bin.Encoder) (bin.Encoder, error) { return nil, test.answer }).signIn(ctx, "+8190", "h1", "1")
		if path := settingPath(t, err); path != "code" || (test.target != nil && !errors.Is(err, test.target)) {
			t.Fatalf("%s: %v", name, err)
		}
	}

	signUp := telegramAnswers(func(bin.Encoder) (bin.Encoder, error) { return &tg.AuthAuthorizationSignUpRequired{}, nil })
	if _, err := signUp.signIn(ctx, "+8190", "h1", "1"); !errors.Is(err, errSignUpUnsupported) {
		t.Fatalf("an unknown number: %v", err)
	}
}

func TestSignInWithPassword(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	answer := func(checked func() (bin.Encoder, error)) steps {
		return telegramAnswers(func(req bin.Encoder) (bin.Encoder, error) {
			switch r := req.(type) {
			case *tg.AccountGetPasswordRequest:
				return srpParameters(t), nil
			case *tg.AuthCheckPasswordRequest:
				if srp, ok := r.Password.(*tg.InputCheckPasswordSRP); !ok || len(srp.A) == 0 || len(srp.M1) == 0 {
					t.Errorf("password %#v", r.Password)
				}
				return checked()
			}
			return nil, errors.New("unexpected")
		})
	}
	ok := answer(func() (bin.Encoder, error) { return &tg.AuthAuthorization{User: mio}, nil })
	if who, err := ok.password(ctx, "secret"); err != nil || who != mioIs {
		t.Fatalf("signed in as %q, %v", who, err)
	}
	wrong := answer(func() (bin.Encoder, error) { return nil, tgerr.New(400, "PASSWORD_HASH_INVALID") })
	if _, err := wrong.password(ctx, "secret"); settingPath(t, err) != "password" {
		t.Fatalf("a wrong password: %v", err)
	}
}

// A flood wait is no mistake of the operator's: it says how long to wait.
func TestFloodWait(t *testing.T) {
	t.Parallel()
	s := telegramAnswers(func(bin.Encoder) (bin.Encoder, error) { return nil, tgerr.New(420, "FLOOD_WAIT_30") })
	_, err := s.sendCode(context.Background(), "+8190")
	if !errors.Is(err, otogi.ErrUnavailable) || errors.Is(err, otogi.ErrInvalid) || !strings.Contains(err.Error(), "30s") {
		t.Fatalf("flood wait: %v", err)
	}
}

// srpParameters are a password's SRP parameters, as gotd's own tests have
// them (telegram/auth/user_test.go).
func srpParameters(t *testing.T) *tg.AccountPassword {
	t.Helper()
	unhex := func(s string) []byte {
		b, err := hex.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	algo := &tg.PasswordKdfAlgoSHA256SHA256PBKDF2HMACSHA512iter100000SHA256ModPow{
		Salt1: unhex("4D11FB6BEC38F9D2546BB0F61E4F1C99A1BC0DB8F0D5F35B1291B37B213123D7ED48F3C6794D495B"),
		Salt2: unhex("A1B181AAFE88188680AE32860D60BB01"),
		G:     3,
		P: unhex("C71CAEB9C6B1C9048E6C522F70F13F73980D40238E3E21C14934D037563D930F" +
			"48198A0AA7C14058229493D22530F4DBFA336F6E0AC925139543AED44CCE7C37" +
			"20FD51F69458705AC68CD4FE6B6B13ABDC9746512969328454F18FAF8C595F64" +
			"2477FE96BB2A941D5BCD1D4AC8CC49880708FA9B378E3C4F3A9060BEE67CF9A4" +
			"A4A695811051907E162753B56B0F6B410DBA74D8A84B2A14B3144E0EF1284754" +
			"FD17ED950D5965B4B9DD46582DB1178D169C6BC465B0D6FF9CA3928FEF5B9AE4" +
			"E418FC15E83EBEA0F87FA9FF5EED70050DED2849F47BF959D956850CE929851F" +
			"0D8115F635B105EE2E4E15D04B2454BF6F4FADF034B10403119CD8E3B92FCC5B"),
	}
	p := &tg.AccountPassword{NewAlgo: algo, NewSecureAlgo: &tg.SecurePasswordKdfAlgoPBKDF2HMACSHA512iter100000{}}
	p.SetCurrentAlgo(algo)
	return p
}
