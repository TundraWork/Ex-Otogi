package telegram

import (
	"context"
	"errors"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// Authenticator supplies login credentials when the saved session is not
// authorized. Implementations typically prompt an operator at a terminal.
type Authenticator interface {
	// Phone returns the account's phone number in international format.
	Phone(ctx context.Context) (string, error)
	// Code returns the login code Telegram just sent.
	Code(ctx context.Context) (string, error)
	// Password returns the two-step verification password.
	Password(ctx context.Context) (string, error)
}

var errSignUpUnsupported = errors.New("telegram: the phone number has no account; sign up with an official client first")

// userAuthenticator adapts Authenticator to gotd's auth flow. Signing up new
// accounts is not supported.
type userAuthenticator struct{ Authenticator }

func (a userAuthenticator) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	return a.Authenticator.Code(ctx)
}

func (userAuthenticator) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return errSignUpUnsupported
}

func (userAuthenticator) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errSignUpUnsupported
}
