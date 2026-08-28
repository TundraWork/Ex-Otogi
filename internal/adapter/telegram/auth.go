package telegram

import (
	"context"
	"errors"
	"fmt"

	gotdauth "github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// ErrUnsupportedAuth reports an authorization path the driver deliberately refuses.
var ErrUnsupportedAuth = errors.New("telegram: unsupported authorization flow")

// AuthFlowKind identifies a refused interactive authorization path.
type AuthFlowKind string

const (
	AuthFlowSignup  AuthFlowKind = "signup"
	AuthFlowQR      AuthFlowKind = "QR"
	AuthFlowPasskey AuthFlowKind = "passkey"
	AuthFlowPayment AuthFlowKind = "payment-required code"
	AuthFlowEmail   AuthFlowKind = "email verification"
	AuthFlowOAuth   AuthFlowKind = "OAuth"
)

// UnsupportedAuthorization returns a stable, secret-free refusal.
func UnsupportedAuthorization(flow AuthFlowKind) error {
	return fmt.Errorf("%w: %s", ErrUnsupportedAuth, flow)
}

// CodeDelivery describes a supported login-code channel without exposing gotd types.
type CodeDelivery string

const (
	CodeDeliveryApp        CodeDelivery = "app"
	CodeDeliverySMS        CodeDelivery = "sms"
	CodeDeliveryCall       CodeDelivery = "call"
	CodeDeliveryFlashCall  CodeDelivery = "flash-call"
	CodeDeliveryMissedCall CodeDelivery = "missed-call"
)

// CodeChallenge contains the non-secret prompt metadata needed by an authenticator.
type CodeChallenge struct {
	Delivery CodeDelivery
	Length   int
}

// Authenticator supplies credentials transiently for phone/code/2FA authorization.
type Authenticator interface {
	Phone(context.Context) (string, error)
	Code(context.Context, CodeChallenge) (string, error)
	Password(context.Context) (string, error)
}

// AuthenticatorFuncs adapts three callbacks to Authenticator.
type AuthenticatorFuncs struct {
	PhoneFunc    func(context.Context) (string, error)
	CodeFunc     func(context.Context, CodeChallenge) (string, error)
	PasswordFunc func(context.Context) (string, error)
}

func (a AuthenticatorFuncs) Phone(ctx context.Context) (string, error) {
	if a.PhoneFunc == nil {
		return "", fmt.Errorf("auth phone: %w", ErrInvalidConfig)
	}
	return a.PhoneFunc(ctx)
}
func (a AuthenticatorFuncs) Code(ctx context.Context, c CodeChallenge) (string, error) {
	if a.CodeFunc == nil {
		return "", fmt.Errorf("auth code: %w", ErrInvalidConfig)
	}
	return a.CodeFunc(ctx, c)
}
func (a AuthenticatorFuncs) Password(ctx context.Context) (string, error) {
	if a.PasswordFunc == nil {
		return "", fmt.Errorf("auth password: %w", ErrInvalidConfig)
	}
	return a.PasswordFunc(ctx)
}

// AuthFlow is the narrow authorization operation consumed by a client session.
type AuthFlow interface {
	Run(context.Context, gotdauth.FlowClient) error
}

// NewAuthFlow creates a phone/code/2FA flow with explicit unsupported outcomes.
func NewAuthFlow(a Authenticator) AuthFlow { return userAuthFlow{auth: userAuthenticator{next: a}} }

type userAuthFlow struct{ auth userAuthenticator }

func (f userAuthFlow) Run(ctx context.Context, client gotdauth.FlowClient) error {
	phone, err := f.auth.Phone(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return errors.New("telegram: obtain phone credential")
	}
	sent, err := client.SendCode(ctx, phone, gotdauth.SendCodeOptions{})
	if err != nil {
		return fmt.Errorf("telegram: send authorization code: %w", err)
	}
	codeSent, ok := sent.(*tg.AuthSentCode)
	if !ok {
		if _, payment := sent.(*tg.AuthSentCodePaymentRequired); payment {
			return UnsupportedAuthorization(AuthFlowPayment)
		}
		if success, authorized := sent.(*tg.AuthSentCodeSuccess); authorized {
			if _, signup := success.Authorization.(*tg.AuthAuthorizationSignUpRequired); signup {
				return UnsupportedAuthorization(AuthFlowSignup)
			}
			if _, complete := success.Authorization.(*tg.AuthAuthorization); complete {
				return nil
			}
		}
		return fmt.Errorf("%w: code response", ErrUnsupportedAuth)
	}
	code, err := f.auth.Code(ctx, codeSent)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrUnsupportedAuth) {
			return err
		}
		return errors.New("telegram: obtain authorization code")
	}
	_, err = client.SignIn(ctx, phone, code, codeSent.PhoneCodeHash)
	if errors.Is(err, gotdauth.ErrPasswordAuthNeeded) {
		password, passwordErr := f.auth.Password(ctx)
		if passwordErr != nil {
			if errors.Is(passwordErr, context.Canceled) || errors.Is(passwordErr, context.DeadlineExceeded) {
				return passwordErr
			}
			return errors.New("telegram: obtain 2FA credential")
		}
		if _, passwordErr = client.Password(ctx, password); passwordErr != nil {
			return fmt.Errorf("telegram: authorize with 2FA: %w", passwordErr)
		}
		return nil
	}
	var signup *gotdauth.SignUpRequired
	if errors.As(err, &signup) {
		return UnsupportedAuthorization(AuthFlowSignup)
	}
	if err != nil {
		return fmt.Errorf("telegram: authorize with code: %w", err)
	}
	return nil
}

type userAuthenticator struct{ next Authenticator }

func (a userAuthenticator) Phone(ctx context.Context) (string, error)    { return a.next.Phone(ctx) }
func (a userAuthenticator) Password(ctx context.Context) (string, error) { return a.next.Password(ctx) }
func (a userAuthenticator) SignUp(context.Context) (gotdauth.UserInfo, error) {
	return gotdauth.UserInfo{}, UnsupportedAuthorization(AuthFlowSignup)
}
func (a userAuthenticator) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return UnsupportedAuthorization(AuthFlowSignup)
}
func (a userAuthenticator) Code(ctx context.Context, s *tg.AuthSentCode) (string, error) {
	challenge, err := codeChallenge(s)
	if err != nil {
		return "", err
	}
	return a.next.Code(ctx, challenge)
}
func codeChallenge(s *tg.AuthSentCode) (CodeChallenge, error) {
	if s == nil {
		return CodeChallenge{}, fmt.Errorf("%w: missing code challenge", ErrUnsupportedAuth)
	}
	switch v := s.Type.(type) {
	case *tg.AuthSentCodeTypeApp:
		return CodeChallenge{CodeDeliveryApp, v.Length}, nil
	case *tg.AuthSentCodeTypeSMS:
		return CodeChallenge{CodeDeliverySMS, v.Length}, nil
	case *tg.AuthSentCodeTypeCall:
		return CodeChallenge{CodeDeliveryCall, v.Length}, nil
	case *tg.AuthSentCodeTypeFlashCall:
		return CodeChallenge{Delivery: CodeDeliveryFlashCall}, nil
	case *tg.AuthSentCodeTypeMissedCall:
		return CodeChallenge{CodeDeliveryMissedCall, v.Length}, nil
	case *tg.AuthSentCodeTypeEmailCode:
		return CodeChallenge{}, UnsupportedAuthorization(AuthFlowEmail)
	case *tg.AuthSentCodeTypeSetUpEmailRequired:
		return CodeChallenge{}, UnsupportedAuthorization(AuthFlowEmail)
	case *tg.AuthSentCodeTypeFragmentSMS:
		return CodeChallenge{}, UnsupportedAuthorization(AuthFlowPayment)
	default:
		return CodeChallenge{}, fmt.Errorf("%w: code delivery", ErrUnsupportedAuth)
	}
}
