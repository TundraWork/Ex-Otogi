package qq

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const c2c = `{"id":"m1","content":"hello","timestamp":"2026-11-06T10:00:00+08:00","author":{"id":"a1","user_openid":"U1","username":"Alice"},"message_scene":{"source":"default","ext":["msg_idx=REFIDX_1"]}}`

// The bot identifies, persists dispatches before taking their sequence
// number, keeps a message pushed twice once, and resumes its session after
// a restart, so nothing sent while it was down is lost.
func TestGatewayIdentifiesPersistsAndResumes(t *testing.T) {
	f := newFakeQQ(t)
	dir := t.TempDir()
	k, rec := newSubmitter(), &recorder{}
	d := newDriver(f, dir, k, rec)
	var p *peer
	startDriver(t, d, func() {
		p = f.next()
		identify := p.hello()
		var in struct {
			Token   string `json:"token"`
			Intents int    `json:"intents"`
		}
		if err := json.Unmarshal(identify.D, &in); err != nil || identify.Op != opIdentify || in.Token != "QQBot tok" || in.Intents != intents {
			t.Fatalf("identify = %d %s", identify.Op, identify.D)
		}
		p.dispatch(1, "READY", "", `{"session_id":"S1","user":{"id":"B1","username":"Otogi"}}`)
	})
	p.dispatch(2, "C2C_MESSAGE_CREATE", "C2C_MESSAGE_CREATE:e1", c2c)
	p.dispatch(3, "C2C_MESSAGE_CREATE", "C2C_MESSAGE_CREATE:e1-again", c2c) // pushed twice
	e := k.next(t)
	if e.Conversation.ID != "user:U1" || e.Actor.ID != "user:U1" || e.Actor.Name != "Alice" || e.Message.PlainText() != "hello" || e.SourceID != "m1" || e.Direction != otogi.DirectionIncoming {
		t.Fatalf("event = %+v", e)
	}
	if !e.OccurredAt.Equal(time.Date(2026, 11, 6, 2, 0, 0, 0, time.UTC)) {
		t.Errorf("occurred at %v", e.OccurredAt)
	}
	waitFor(t, "seq 3 stored", func() bool { return d.Status(context.Background()).Seq == 3 })
	if s := d.Status(context.Background()); s.Session != "S1" || s.Bot != "B1 Otogi" || s.PendingInbox != 0 {
		t.Fatalf("status = %+v", s)
	}
	stopDriver(t, d)

	// A restart resumes the stored session.
	d2 := newDriver(f, dir, k, rec)
	startDriver(t, d2, func() {
		p = f.next()
		resume := p.hello()
		if resume.Op != opResume || !strings.Contains(string(resume.D), `"session_id":"S1"`) || !strings.Contains(string(resume.D), `"seq":3`) {
			t.Fatalf("resume = %d %s", resume.Op, resume.D)
		}
		p.dispatch(4, "C2C_MESSAGE_CREATE", "C2C_MESSAGE_CREATE:e2", strings.Replace(c2c, `"m1"`, `"m2"`, 1))
		p.dispatch(0, "RESUMED", "", `""`)
	})
	if e := k.next(t); e.SourceID != "m2" {
		t.Fatalf("replayed event = %+v", e)
	}
	select {
	case e := <-k.got:
		t.Fatalf("the duplicate was dispatched: %+v", e)
	default:
	}
}

// Invalid Session means events may be lost: a gap is recorded and the bot
// identifies afresh.
func TestInvalidSessionRecordsAGap(t *testing.T) {
	f := newFakeQQ(t)
	k, rec := newSubmitter(), &recorder{}
	d := newDriver(f, t.TempDir(), k, rec)
	var p *peer
	startDriver(t, d, func() {
		p = f.next()
		p.hello()
		p.dispatch(1, "READY", "", `{"session_id":"S1","user":{"id":"B1"}}`)
	})
	p.send(frame{Op: opInvalidSession, D: json.RawMessage(`false`)})
	p = f.next()
	if again := p.hello(); again.Op != opIdentify {
		t.Fatalf("after Invalid Session: op %d", again.Op)
	}
	p.dispatch(1, "READY", "", `{"session_id":"S2","user":{"id":"B1"}}`)
	waitFor(t, "the new session", func() bool { return d.Status(context.Background()).Session == "S2" })
	if s := d.Status(context.Background()); s.Gaps != 1 || !slices.Contains(rec.kinds(), "driver.gap") {
		t.Fatalf("status = %+v, records = %v", s, rec.kinds())
	}
}

// Reconnect (op 7) and a dropped connection resume; a ban stops the driver.
func TestReconnectsAndStopsWhenBanned(t *testing.T) {
	f := newFakeQQ(t)
	k, rec := newSubmitter(), &recorder{}
	d := newDriver(f, t.TempDir(), k, rec)
	var p *peer
	startDriver(t, d, func() {
		p = f.next()
		p.hello()
		p.dispatch(1, "READY", "", `{"session_id":"S1","user":{"id":"B1"}}`)
	})
	p.send(frame{Op: opReconnect})
	p = f.next()
	if again := p.hello(); again.Op != opResume {
		t.Fatalf("after Reconnect: op %d", again.Op)
	}
	p.closeWith(websocket.StatusCode(4009))
	p = f.next()
	if again := p.hello(); again.Op != opResume {
		t.Fatalf("after 4009: op %d", again.Op)
	}
	p.closeWith(websocket.StatusCode(4915))
	select {
	case <-d.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the driver kept going after a ban")
	}
	if err := d.Err(); err == nil || !strings.Contains(err.Error(), "4915") {
		t.Fatalf("err = %v", err)
	}
}

// A wrong secret fails startup plainly.
func TestStartFailsOnBadCredentials(t *testing.T) {
	f := newFakeQQ(t)
	f.handle = func(w http.ResponseWriter, r *http.Request, _ string) bool {
		if r.URL.Path == "/app/getAppAccessToken" {
			_, _ = w.Write([]byte(`{"code":100016,"message":"invalid appsecret"}`))
			return true
		}
		return false
	}
	d := newDriver(f, t.TempDir(), newSubmitter(), &recorder{})
	err := d.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "code 100016: invalid appsecret") {
		t.Fatalf("err = %v", err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
