package qq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// defaultAPI is where every QQ Bot API call goes, the token included, since
// QQ unified its domains on 2026-08-10.
const defaultAPI = "https://api.bot.qq.com"

// api calls the QQ Bot API v2 as one bot.
type api struct {
	base   string
	appID  string
	secret string
	http   *http.Client
	now    func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// apiError is a failed call, with what QQ said about it.
type apiError struct {
	Method, Path string
	Status       int
	Code         int    // QQ's code or err_code; 0 when the body had none
	Message      string // QQ's message, or the body when it was not QQ's error shape
	TraceID      string
}

func (e *apiError) Error() string {
	s := fmt.Sprintf("qq %s %s: HTTP %d", e.Method, e.Path, e.Status)
	if e.Code != 0 {
		s += fmt.Sprintf(", code %d", e.Code)
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	if e.TraceID != "" {
		s += " (trace " + e.TraceID + ")"
	}
	return s
}

// accessToken returns a token for the Authorization header. QQ hands out the
// same token until its last 60 seconds, then a new one while the old one
// keeps working for those 60 seconds, so the token is refreshed once it has
// less than a minute left.
func (a *api) accessToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && a.now().Before(a.expires.Add(-time.Minute)) {
		return a.token, nil
	}
	body, err := json.Marshal(map[string]string{"appId": a.appID, "clientSecret": a.secret})
	if err != nil {
		return "", fmt.Errorf("qq token: %w", err)
	}
	var out struct {
		AccessToken string          `json:"access_token"`
		ExpiresIn   json.RawMessage `json:"expires_in"` // a number, or a number in a string
		Code        int             `json:"code"`
		Message     string          `json:"message"`
	}
	if err := a.send(ctx, http.MethodPost, "/app/getAppAccessToken", body, "", &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		// QQ reports token failures with HTTP 200 and a code.
		return "", &apiError{Method: http.MethodPost, Path: "/app/getAppAccessToken", Status: http.StatusOK, Code: out.Code, Message: out.Message}
	}
	seconds, err := strconv.Atoi(string(bytes.Trim(out.ExpiresIn, `"`)))
	if err != nil {
		return "", fmt.Errorf("qq token: expires_in %s: %w", out.ExpiresIn, err)
	}
	a.token, a.expires = out.AccessToken, a.now().Add(time.Duration(seconds)*time.Second)
	return a.token, nil
}

// forgetToken drops the cached token after QQ refused it.
func (a *api) forgetToken() {
	a.mu.Lock()
	a.token = ""
	a.mu.Unlock()
}

// tokenExpiry is when the cached token expires; zero before the first one.
func (a *api) tokenExpiry() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.expires
}

// call sends an authorized request with in as its JSON body (nil for none)
// and decodes the response into out (nil to discard it). A refused token is
// fetched again once.
func (a *api) call(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return fmt.Errorf("qq %s %s: encode: %w", method, path, err)
		}
	}
	for attempt := 0; ; attempt++ {
		token, err := a.accessToken(ctx)
		if err != nil {
			return err
		}
		err = a.send(ctx, method, path, body, "QQBot "+token, out)
		var e *apiError
		if attempt == 0 && errors.As(err, &e) && e.Status == http.StatusUnauthorized {
			a.forgetToken()
			continue
		}
		return err
	}
}

func (a *api) send(ctx context.Context, method, path string, body []byte, auth string, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, reader)
	if err != nil {
		return fmt.Errorf("qq %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("qq %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("qq %s %s: read response: %w", method, path, err)
	}
	trace := resp.Header.Get("X-Tps-Trace-Id")
	if resp.StatusCode >= 300 {
		return decodeError(method, path, resp.StatusCode, trace, data)
	}
	// 201 and 202 are accepted but carry an error body: the message waits
	// for review (304023, 304024).
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		if e := decodeError(method, path, resp.StatusCode, trace, data); e.Code != 0 {
			return e
		}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("qq %s %s: decode %q: %w", method, path, data, err)
	}
	return nil
}

func decodeError(method, path string, status int, trace string, data []byte) *apiError {
	e := &apiError{Method: method, Path: path, Status: status, TraceID: trace}
	var body struct {
		Code    int    `json:"code"`
		ErrCode int    `json:"err_code"`
		Message string `json:"message"`
		TraceID string `json:"trace_id"`
	}
	if json.Unmarshal(data, &body) == nil && (body.Code != 0 || body.ErrCode != 0 || body.Message != "") {
		e.Code, e.Message = body.Code, body.Message
		if body.ErrCode != 0 {
			e.Code = body.ErrCode
		}
		if body.TraceID != "" {
			e.TraceID = body.TraceID
		}
		return e
	}
	e.Message = string(data)
	return e
}
