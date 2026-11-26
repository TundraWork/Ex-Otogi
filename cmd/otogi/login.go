package main

import (
	"context"
	"errors"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// loginSteps are the steps of a Telegram login (see the telegram adapter's
// login.go), each on a connection of its own; tests replace them.
type loginSteps struct {
	loggedIn func(ctx context.Context, cfg telegram.SessionConfig) (string, bool, error)
	sendCode func(ctx context.Context, cfg telegram.SessionConfig, phone string) (telegram.SentCode, error)
	signIn   func(ctx context.Context, cfg telegram.SessionConfig, phone, hash, code string) (string, error)
	password func(ctx context.Context, cfg telegram.SessionConfig, password string) (string, error)
}

func telegramSteps() loginSteps {
	return loginSteps{loggedIn: telegram.LoggedIn, sendCode: telegram.SendCode, signIn: telegram.SignIn, password: telegram.SignInWithPassword}
}

// prompter asks the operator for what a login needs, and tells them what
// they typed wrong.
type prompter interface {
	Phone(ctx context.Context) (string, error)
	Code(ctx context.Context) (string, error)
	Password(ctx context.Context, hint string) (string, error)
	Refused(err error) error
}

// logIn logs the session in, asking p for the phone number, the code, and
// the password when the account has one, and returns as whom. A value
// Telegram refuses is asked for again; an expired code is sent again.
func logIn(ctx context.Context, steps loginSteps, cfg telegram.SessionConfig, p prompter) (string, error) {
	who, ok, err := steps.loggedIn(ctx, cfg)
	if err != nil || ok {
		return who, err
	}
	var phone string
	var sent telegram.SentCode
	for {
		if phone, err = p.Phone(ctx); err != nil {
			return "", err
		}
		if sent, err = steps.sendCode(ctx, cfg, phone); !typo(err) {
			break
		}
		if err := p.Refused(err); err != nil {
			return "", err
		}
	}
	if err != nil || sent.Who != "" {
		return sent.Who, err
	}
	for {
		code, err := p.Code(ctx)
		if err != nil {
			return "", err
		}
		who, err := steps.signIn(ctx, cfg, phone, sent.Hash, code)
		var needed *telegram.PasswordNeededError
		switch {
		case errors.As(err, &needed):
			return logInWithPassword(ctx, steps, cfg, p, needed.Hint)
		case errors.Is(err, telegram.ErrCodeExpired):
			if err := p.Refused(err); err != nil {
				return "", err
			}
			if sent, err = steps.sendCode(ctx, cfg, phone); err != nil || sent.Who != "" {
				return sent.Who, err
			}
		case typo(err):
			if err := p.Refused(err); err != nil {
				return "", err
			}
		default:
			return who, err
		}
	}
}

func logInWithPassword(ctx context.Context, steps loginSteps, cfg telegram.SessionConfig, p prompter, hint string) (string, error) {
	for {
		password, err := p.Password(ctx, hint)
		if err != nil {
			return "", err
		}
		who, err := steps.password(ctx, cfg, password)
		if !typo(err) {
			return who, err
		}
		if err := p.Refused(err); err != nil {
			return "", err
		}
	}
}

// typo reports a value Telegram refused, which the operator may type again.
func typo(err error) bool { return errors.Is(err, otogi.ErrInvalid) }
