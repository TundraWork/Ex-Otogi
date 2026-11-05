package qq

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/goleak"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeQQ is a QQ Bot API server: the token and gateway endpoints, and a
// WebSocket gateway whose connections the test drives.
type fakeQQ struct {
	t     *testing.T
	srv   *httptest.Server
	conns chan *peer

	mu       sync.Mutex
	requests []string // "METHOD /path body"
	handle   func(w http.ResponseWriter, r *http.Request, body string) bool
}

// peer is one gateway connection, seen from the server. Heartbeats are
// acknowledged automatically; other frames from the bot arrive on got.
type peer struct {
	t      *testing.T
	conn   *websocket.Conn
	got    chan frame
	closed chan struct{}
}

func newFakeQQ(t *testing.T) *fakeQQ {
	f := &fakeQQ{t: t, conns: make(chan *peer, 8)}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeQQ) serve(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path+" "+string(data))
	handle := f.handle
	f.mu.Unlock()
	if handle != nil && handle(w, r, string(data)) {
		return
	}
	switch r.URL.Path {
	case "/app/getAppAccessToken":
		_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":"7200"}`)
	case "/gateway":
		if r.Header.Get("Authorization") != "QQBot tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"url":"ws`+strings.TrimPrefix(f.srv.URL, "http")+`/ws"}`)
	case "/ws":
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		p := &peer{t: f.t, conn: conn, got: make(chan frame, 16), closed: make(chan struct{})}
		go p.read()
		f.conns <- p
		<-p.closed
		_ = conn.CloseNow()
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (p *peer) read() {
	defer close(p.closed)
	for {
		_, data, err := p.conn.Read(context.Background())
		if err != nil {
			return
		}
		var f frame
		if err := json.Unmarshal(data, &f); err != nil {
			p.t.Errorf("bad frame %s", data)
			return
		}
		if f.Op == opHeartbeat {
			_ = p.conn.Write(context.Background(), websocket.MessageText, []byte(`{"op":11}`))
			continue
		}
		p.got <- f
	}
}

func (p *peer) send(f frame) {
	p.t.Helper()
	data, _ := json.Marshal(f)
	if err := p.conn.Write(context.Background(), websocket.MessageText, data); err != nil {
		p.t.Fatalf("send %s: %v", data, err)
	}
}

// hello greets the bot and returns what it sent back: Identify or Resume.
func (p *peer) hello() frame {
	p.t.Helper()
	p.send(frame{Op: opHello, D: json.RawMessage(`{"heartbeat_interval":50}`)})
	select {
	case f := <-p.got:
		return f
	case <-time.After(5 * time.Second):
		p.t.Fatal("no Identify or Resume")
	}
	return frame{}
}

func (p *peer) dispatch(seq int64, t, id, d string) {
	p.t.Helper()
	p.send(frame{Op: opDispatch, S: seq, T: t, ID: id, D: json.RawMessage(d)})
}

// closeWith closes the connection with code, as QQ does, without waiting
// for the bot's side of the closing handshake.
func (p *peer) closeWith(code websocket.StatusCode) {
	go func() { _ = p.conn.Close(code, "test") }()
}

func (f *fakeQQ) next() *peer {
	f.t.Helper()
	select {
	case p := <-f.conns:
		return p
	case <-time.After(5 * time.Second):
		f.t.Fatal("the bot did not connect")
	}
	return nil
}

// submitter records submitted events and completes them at once.
type submitter struct {
	mu     sync.Mutex
	events []otogi.Event
	got    chan otogi.Event
}

func newSubmitter() *submitter { return &submitter{got: make(chan otogi.Event, 64)} }

func (s *submitter) Submit(_ context.Context, event otogi.Event, done func(otogi.Outcome)) error {
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	s.got <- event
	done(otogi.Outcome{EventID: event.ID})
	return nil
}

func (s *submitter) next(t *testing.T) otogi.Event {
	t.Helper()
	select {
	case e := <-s.got:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event submitted")
	}
	return otogi.Event{}
}

// recorder keeps event log records.
type recorder struct {
	mu      sync.Mutex
	records []otogi.Record
}

func (r *recorder) Record(_ context.Context, rec otogi.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
}

func (r *recorder) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, rec := range r.records {
		out = append(out, rec.Kind)
	}
	return out
}

// newDriver returns a driver for the fake server, with its database in dir.
func newDriver(f *fakeQQ, dir string, k Submitter, rec otogi.Recorder) *Driver {
	d := New(Config{AccountKey: "bot", AppID: "1001", AppSecret: "secret", DatabasePath: filepath.Join(dir, "state.sqlite"), Kernel: k, Logger: quiet, Recorder: rec})
	d.api.base = f.srv.URL
	d.backoff = []time.Duration{10 * time.Millisecond}
	return d
}

// startDriver starts d while the test plays the gateway's side of the
// connection with connect, and stops d when the test ends.
func startDriver(t *testing.T, d *Driver, connect func()) {
	t.Helper()
	started := make(chan error, 1)
	go func() { started <- d.Start(context.Background()) }()
	connect()
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopDriver(t, d) })
}

func stopDriver(t *testing.T, d *Driver) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Stop(ctx); err != nil {
		t.Error(err)
	}
}
