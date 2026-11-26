package telegram

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// A login takes several steps: the phone number, the code Telegram sends,
// and, for an account with two-step verification, its password. Each step
// below connects with the session file, takes that step, and disconnects.
// Telegram ties the login to the session's key, which the file keeps, so a
// step can follow the one before on a new connection: between steps only
// the phone number and the code's hash need keeping.

// SentCode is what SendCode did.
type SentCode struct {
	// Hash is Telegram's phone_code_hash, which SignIn needs.
	Hash string
	// Via is where the code went: app, sms, call, flash_call, missed_call,
	// email, fragment, or firebase; empty for a way Telegram added later.
	Via string
	// Length is how many characters the code has, when Telegram says.
	Length int
	// Who is set, and the others are not, when Telegram answered that the
	// session is logged in: no code was sent.
	Who string
}

// PasswordNeededError is SignIn's answer for an account with two-step
// verification: SignInWithPassword finishes the login.
type PasswordNeededError struct {
	// Hint is the hint the account's owner set for the password.
	Hint string
}

func (e *PasswordNeededError) Error() string {
	return "telegram: the account has two-step verification: its password is needed"
}

// ErrCodeExpired is SignIn's answer, inside a *otogi.SettingError on the
// code, when the code is too old: SendCode sends a new one.
var ErrCodeExpired = errors.New("the code expired: send a new one")

var errSignUpUnsupported = errors.New("telegram: the phone number has no account; sign up with an official client first")

// LoggedIn reports whether the session is logged in, and as whom.
func LoggedIn(ctx context.Context, cfg SessionConfig) (who string, ok bool, err error) {
	err = connect(ctx, cfg, func(ctx context.Context, s steps) error {
		who, ok, err = s.loggedIn(ctx)
		return err
	})
	return who, ok, err
}

// SendCode asks Telegram to send a login code for phone, in international
// format.
func SendCode(ctx context.Context, cfg SessionConfig, phone string) (sent SentCode, err error) {
	err = connect(ctx, cfg, func(ctx context.Context, s steps) error {
		sent, err = s.sendCode(ctx, phone)
		return err
	})
	return sent, err
}

// SignIn logs the session in with the code SendCode had sent, and returns
// as whom. For an account with two-step verification the error is a
// *PasswordNeededError.
func SignIn(ctx context.Context, cfg SessionConfig, phone, hash, code string) (who string, err error) {
	err = connect(ctx, cfg, func(ctx context.Context, s steps) error {
		who, err = s.signIn(ctx, phone, hash, code)
		return err
	})
	return who, err
}

// SignInWithPassword finishes a login SignIn answered with a
// *PasswordNeededError, and returns as whom.
func SignInWithPassword(ctx context.Context, cfg SessionConfig, password string) (who string, err error) {
	err = connect(ctx, cfg, func(ctx context.Context, s steps) error {
		who, err = s.password(ctx, password)
		return err
	})
	return who, err
}

// connect runs step on a connection made with the session file.
func connect(ctx context.Context, cfg SessionConfig, step func(context.Context, steps) error) error {
	c, err := cfg.client()
	if err != nil {
		return err
	}
	return c.Run(ctx, func(ctx context.Context) error {
		return step(ctx, steps{api: c.API(), auth: c.Auth()})
	})
}

// steps are the login's steps on one connection.
type steps struct {
	api  *tg.Client
	auth *auth.Client
}

func newSteps(api *tg.Client, appID int, appHash string) steps {
	return steps{api: api, auth: auth.NewClient(api, rand.Reader, appID, appHash)}
}

func (s steps) loggedIn(ctx context.Context) (string, bool, error) {
	status, err := s.auth.Status(ctx)
	if err != nil {
		return "", false, refused(err)
	}
	if !status.Authorized {
		return "", false, nil
	}
	return describe(status.User), true, nil
}

func (s steps) sendCode(ctx context.Context, phone string) (SentCode, error) {
	sent, err := s.auth.SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return SentCode{}, refused(err)
	}
	switch sent := sent.(type) {
	case *tg.AuthSentCode:
		if _, ok := sent.Type.(*tg.AuthSentCodeTypeSetUpEmailRequired); ok {
			return SentCode{}, &otogi.SettingError{Path: "phone", Err: errors.New("this number must set up a login email first, which Telegram asks for: do that in an official Telegram app")}
		}
		via, length := codeType(sent.Type)
		return SentCode{Hash: sent.PhoneCodeHash, Via: via, Length: length}, nil
	case *tg.AuthSentCodeSuccess:
		a, ok := sent.Authorization.(*tg.AuthAuthorization)
		if !ok {
			return SentCode{}, errSignUpUnsupported
		}
		return SentCode{Who: describeUser(a.User)}, nil
	case *tg.AuthSentCodePaymentRequired:
		return SentCode{}, &otogi.SettingError{Path: "phone", Err: errors.New("this number must pay before Telegram sends it a login code: log in with an official Telegram app first")}
	default:
		return SentCode{}, fmt.Errorf("telegram: send code: unexpected answer %T", sent)
	}
}

func (s steps) signIn(ctx context.Context, phone, hash, code string) (string, error) {
	a, err := s.auth.SignIn(ctx, phone, code, hash)
	if errors.Is(err, auth.ErrPasswordAuthNeeded) {
		p, err := s.api.AccountGetPassword(ctx)
		if err != nil {
			return "", refused(err)
		}
		return "", &PasswordNeededError{Hint: p.Hint}
	}
	if err != nil {
		return "", refused(err)
	}
	return describeUser(a.User), nil
}

func (s steps) password(ctx context.Context, password string) (string, error) {
	a, err := s.auth.Password(ctx, password)
	if err != nil {
		return "", refused(err)
	}
	return describeUser(a.User), nil
}

// codeType is where a code went and how long it is.
func codeType(t tg.AuthSentCodeTypeClass) (string, int) {
	switch t := t.(type) {
	case *tg.AuthSentCodeTypeApp:
		return "app", t.Length
	case *tg.AuthSentCodeTypeSMS:
		return "sms", t.Length
	case *tg.AuthSentCodeTypeSMSWord, *tg.AuthSentCodeTypeSMSPhrase:
		return "sms", 0
	case *tg.AuthSentCodeTypeCall:
		return "call", t.Length
	case *tg.AuthSentCodeTypeFlashCall:
		return "flash_call", 0
	case *tg.AuthSentCodeTypeMissedCall:
		return "missed_call", t.Length
	case *tg.AuthSentCodeTypeEmailCode:
		return "email", t.Length
	case *tg.AuthSentCodeTypeFragmentSMS:
		return "fragment", t.Length
	case *tg.AuthSentCodeTypeFirebaseSMS:
		return "firebase", t.Length
	default:
		return "", 0
	}
}

// refused says what Telegram refused: what the operator typed wrong is a
// *otogi.SettingError on that field, and a flood wait is
// otogi.ErrUnavailable with how long to wait.
func refused(err error) error {
	if d, ok := tgerr.AsFloodWait(err); ok {
		return fmt.Errorf("telegram: Telegram asks to wait %s before trying again: %w", d.Round(time.Second), otogi.ErrUnavailable)
	}
	if errors.Is(err, auth.ErrPasswordInvalid) { // gotd's name for PASSWORD_HASH_INVALID
		return &otogi.SettingError{Path: "password", Err: errors.New("wrong password")}
	}
	var signUp *auth.SignUpRequired
	if errors.As(err, &signUp) {
		return &otogi.SettingError{Path: "phone", Err: errSignUpUnsupported}
	}
	reasons := map[string]struct {
		path   string
		reason error
	}{
		"PHONE_NUMBER_INVALID":    {"phone", errors.New("not a phone number Telegram takes: write it in international format, such as +81 90 1234 5678")},
		"PHONE_NUMBER_BANNED":     {"phone", errors.New("this phone number is banned from Telegram")},
		"PHONE_NUMBER_UNOCCUPIED": {"phone", errSignUpUnsupported},
		"PHONE_CODE_INVALID":      {"code", errors.New("wrong code")},
		"PHONE_CODE_EMPTY":        {"code", errors.New("the code is empty")},
		"PHONE_CODE_EXPIRED":      {"code", ErrCodeExpired},
	}
	for name, r := range reasons {
		if tgerr.Is(err, name) {
			return &otogi.SettingError{Path: r.path, Err: r.reason}
		}
	}
	return fmt.Errorf("telegram: log in: %w", err)
}

func describeUser(u tg.UserClass) string {
	user, ok := u.(*tg.User)
	if !ok {
		return "an account Telegram did not describe"
	}
	return describe(user)
}

// describe is how a login names the account: "First Last (@name, id 1)".
func describe(u *tg.User) string {
	return strings.TrimSpace(fmt.Sprintf("%s %s (@%s, id %d)", u.FirstName, u.LastName, u.Username, u.ID))
}
