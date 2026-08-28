package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"

	gotdauth "github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

func TestUserAuthenticatorSupportsPhoneCodeAndPassword(t *testing.T) {
	var got CodeChallenge
	a := AuthenticatorFuncs{
		PhoneFunc:    func(context.Context) (string, error) { return "+15550001", nil },
		CodeFunc:     func(_ context.Context, c CodeChallenge) (string, error) { got = c; return "12345", nil },
		PasswordFunc: func(context.Context) (string, error) { return "hunter2", nil },
	}
	client := &fakeAuthClient{sent: &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeApp{Length: 5}, PhoneCodeHash: "opaque-hash"}, signInErr: gotdauth.ErrPasswordAuthNeeded}
	if err := NewAuthFlow(a).Run(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if got.Delivery != CodeDeliveryApp || got.Length != 5 {
		t.Fatalf("challenge = %+v", got)
	}
	if client.phone != "+15550001" || client.code != "12345" || client.password != "hunter2" {
		t.Fatalf("callbacks not forwarded: %+v", client)
	}
}

func TestUserAuthenticatorRejectsSignupAndTerms(t *testing.T) {
	a := testAuthenticator()
	client := &fakeAuthClient{sent: &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeSMS{Length: 5}, PhoneCodeHash: "hash"}, signInErr: &gotdauth.SignUpRequired{}}
	err := NewAuthFlow(a).Run(context.Background(), client)
	if !errors.Is(err, ErrUnsupportedAuth) || !strings.Contains(err.Error(), "signup") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "12345") || strings.Contains(err.Error(), "+15550001") {
		t.Fatalf("error leaked credentials: %v", err)
	}
}

func TestUserAuthenticatorRejectsUnsupportedCodeFlows(t *testing.T) {
	tests := []struct {
		name string
		typ  tg.AuthSentCodeTypeClass
	}{
		{"payment", &tg.AuthSentCodeTypeSetUpEmailRequired{}},
		{"email", &tg.AuthSentCodeTypeEmailCode{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := testAuthenticator()
			err := NewAuthFlow(a).Run(context.Background(), &fakeAuthClient{sent: &tg.AuthSentCode{Type: tt.typ, PhoneCodeHash: "secret-hash"}})
			if !errors.Is(err, ErrUnsupportedAuth) {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "secret-hash") {
				t.Fatalf("error leaked code hash: %v", err)
			}
		})
	}
}

func TestUserAuthenticatorRejectsPaymentRequiredResponse(t *testing.T) {
	err := NewAuthFlow(testAuthenticator()).Run(context.Background(), &fakeAuthClient{sent: &tg.AuthSentCodePaymentRequired{PhoneCodeHash: "secret-hash"}})
	if !errors.Is(err, ErrUnsupportedAuth) || !strings.Contains(err.Error(), string(AuthFlowPayment)) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "secret-hash") {
		t.Fatalf("error leaked code hash: %v", err)
	}
}

func TestNamedUnsupportedAuthorizationFlows(t *testing.T) {
	for _, flow := range []AuthFlowKind{AuthFlowSignup, AuthFlowQR, AuthFlowPasskey, AuthFlowPayment, AuthFlowEmail, AuthFlowOAuth} {
		err := UnsupportedAuthorization(flow)
		if !errors.Is(err, ErrUnsupportedAuth) || !strings.Contains(err.Error(), string(flow)) {
			t.Fatalf("flow %q error = %v", flow, err)
		}
	}
}

func testAuthenticator() AuthenticatorFuncs {
	return AuthenticatorFuncs{PhoneFunc: func(context.Context) (string, error) { return "+15550001", nil }, CodeFunc: func(context.Context, CodeChallenge) (string, error) { return "12345", nil }, PasswordFunc: func(context.Context) (string, error) { return "password", nil }}
}

type fakeAuthClient struct {
	sent                  tg.AuthSentCodeClass
	signInErr             error
	phone, code, password string
}

func (f *fakeAuthClient) SendCode(_ context.Context, phone string, _ gotdauth.SendCodeOptions) (tg.AuthSentCodeClass, error) {
	f.phone = phone
	return f.sent, nil
}
func (f *fakeAuthClient) SignIn(_ context.Context, phone, code, _ string) (*tg.AuthAuthorization, error) {
	f.phone, f.code = phone, code
	return nil, f.signInErr
}
func (f *fakeAuthClient) Password(_ context.Context, password string) (*tg.AuthAuthorization, error) {
	f.password = password
	return &tg.AuthAuthorization{}, nil
}
func (f *fakeAuthClient) SignUp(context.Context, gotdauth.SignUp) (*tg.AuthAuthorization, error) {
	return nil, errors.New("unexpected signup")
}
