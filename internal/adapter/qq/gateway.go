package qq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	qqstore "github.com/TundraWork/Ex-Otogi/internal/adapter/qq/store"
)

// Gateway opcodes.
const (
	opDispatch       = 0
	opHeartbeat      = 1
	opIdentify       = 2
	opResume         = 6
	opReconnect      = 7
	opInvalidSession = 9
	opHello          = 10
	opHeartbeatACK   = 11
)

// intents the bot subscribes to: group and C2C events, and interactions.
const intents = 1<<25 | 1<<26

// readLimit bounds one gateway frame. Dispatches are small; this leaves room
// for long messages with many attachments.
const readLimit = 16 << 20

// frame is one gateway payload.
type frame struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d,omitempty"`
	S  int64           `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
	ID string          `json:"id,omitempty"`
}

// ready is the READY dispatch: the session and the bot's own user.
type ready struct {
	SessionID string `json:"session_id"`
	User      struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"user"`
}

// gateway keeps one WebSocket session to QQ, resuming it across disconnects
// and restarts, and persists every dispatch in the inbox before taking its
// sequence number as seen.
type gateway struct {
	api   *api
	store *qqstore.Store
	wake  chan<- struct{}
	log   *slog.Logger
	now   func() time.Time
	// record writes an event log record for the driver.
	record func(kind, outcome string, payload any)
	// ready is called with the bot's own user whenever a session is ready;
	// both are empty after a resume.
	ready func(id, username string)
	// gap records that events may have been lost.
	gap func(kind, detail string)
	// backoff is the wait before each reconnect attempt in a row.
	backoff []time.Duration
}

// sessionEnd says how a session ended and what to do next.
type sessionEnd struct {
	err      error
	resume   bool // the stored session may be resumed
	fatal    bool // QQ will not take the bot back: stop
	wasReady bool
}

// run keeps a session until ctx ends or QQ refuses the bot for good.
func (g *gateway) run(ctx context.Context) error {
	failures := 0
	for {
		end := g.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if end.fatal {
			return end.err
		}
		if end.wasReady {
			failures = 0
		}
		wait := g.backoff[min(failures, len(g.backoff)-1)]
		failures++
		g.log.Warn("qq: gateway disconnected", "error", end.err, "resume", end.resume, "retry_in", wait)
		g.record("driver.state", "error", map[string]any{"state": "reconnecting", "error": end.err.Error(), "resume": end.resume, "retry_in": wait.String()})
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// session connects once and runs until the connection ends.
func (g *gateway) session(ctx context.Context) sessionEnd {
	var url struct {
		URL string `json:"url"`
	}
	if err := g.api.call(ctx, http.MethodGet, "/gateway", nil, &url); err != nil {
		return sessionEnd{err: err, resume: true}
	}
	conn, _, err := websocket.Dial(ctx, url.URL, nil)
	if err != nil {
		return sessionEnd{err: fmt.Errorf("qq gateway: dial %s: %w", url.URL, err), resume: true}
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(readLimit)
	s := &session{g: g, conn: conn}
	return s.run(ctx)
}

// session is one connection.
type session struct {
	g     *gateway
	conn  *websocket.Conn
	id    string
	seq   atomic.Int64 // read by the heartbeat
	acked chan struct{}
	ready bool
}

func (s *session) run(ctx context.Context) sessionEnd {
	hello, err := s.read(ctx)
	if err != nil {
		return s.ended(err)
	}
	if hello.Op != opHello {
		return sessionEnd{err: fmt.Errorf("qq gateway: expected Hello, got op %d", hello.Op), resume: true}
	}
	var h struct {
		Interval int64 `json:"heartbeat_interval"`
	}
	if err := json.Unmarshal(hello.D, &h); err != nil || h.Interval <= 0 {
		return sessionEnd{err: fmt.Errorf("qq gateway: bad Hello %s", hello.D), resume: true}
	}
	if err := s.start(ctx); err != nil {
		return sessionEnd{err: err, resume: true}
	}

	// The heartbeat closes the connection when it fails, so the read loop
	// alone sees how the session ended, close code included.
	s.acked = make(chan struct{}, 1)
	s.acked <- struct{}{}
	beatCtx, stopBeats := context.WithCancel(ctx)
	beatErr := make(chan error, 1)
	go func() {
		err := s.heartbeat(beatCtx, time.Duration(h.Interval)*time.Millisecond)
		if err != nil {
			_ = s.conn.CloseNow()
		}
		beatErr <- err
	}()
	end := s.receive(ctx)
	stopBeats()
	if err := <-beatErr; err != nil && websocket.CloseStatus(end.err) == -1 {
		end.err = err
	}
	return end
}

// start resumes the stored session, or identifies when there is none.
func (s *session) start(ctx context.Context) error {
	token, err := s.g.api.accessToken(ctx)
	if err != nil {
		return err
	}
	stored, ok, err := s.g.store.Session(ctx)
	if err != nil {
		return err
	}
	if ok {
		// Only READY names the bot's own user, which it needs to tell its
		// own group messages apart; a database from before that was kept
		// starts one fresh session to learn it.
		if self, _, err := s.g.store.Bot(ctx); err != nil {
			return err
		} else if self == "" {
			if err := s.g.store.ClearSession(ctx); err != nil {
				return err
			}
			s.g.gap("bot_unknown", fmt.Sprintf("identifying instead of resuming session %s at seq %d to learn the bot's own user", stored.ID, stored.Seq))
			ok = false
		}
	}
	if ok {
		s.id = stored.ID
		s.seq.Store(stored.Seq)
		return s.write(ctx, opResume, map[string]any{"token": "QQBot " + token, "session_id": stored.ID, "seq": stored.Seq})
	}
	return s.write(ctx, opIdentify, map[string]any{"token": "QQBot " + token, "intents": intents, "shard": []int{0, 1}, "properties": map[string]string{}})
}

// heartbeat beats every interval and ends the connection when QQ did not
// acknowledge the previous beat, which means the connection is dead.
func (s *session) heartbeat(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		select {
		case <-s.acked:
		default:
			return fmt.Errorf("qq gateway: no heartbeat ACK in %s", interval)
		}
		if err := s.beat(ctx); err != nil {
			return err
		}
	}
}

func (s *session) beat(ctx context.Context) error {
	var seq any // null before the first dispatch
	if n := s.seq.Load(); n > 0 {
		seq = n
	}
	return s.write(ctx, opHeartbeat, seq)
}

// receive reads frames until the connection ends.
func (s *session) receive(ctx context.Context) sessionEnd {
	for {
		f, err := s.read(ctx)
		if err != nil {
			return s.ended(err)
		}
		switch f.Op {
		case opDispatch:
			if err := s.dispatch(ctx, f); err != nil {
				return sessionEnd{err: err, resume: true, wasReady: s.ready}
			}
		case opHeartbeatACK:
			select {
			case s.acked <- struct{}{}:
			default:
			}
		case opHeartbeat:
			if err := s.beat(ctx); err != nil {
				return s.ended(err)
			}
		case opReconnect:
			return sessionEnd{err: errors.New("qq gateway: QQ asked to reconnect"), resume: true, wasReady: s.ready}
		case opInvalidSession:
			return s.invalid(ctx)
		default:
			s.g.log.Debug("qq gateway: ignored frame", "op", f.Op, "type", f.T)
		}
	}
}

// dispatch handles one Dispatch frame.
func (s *session) dispatch(ctx context.Context, f frame) error {
	if f.S > 0 {
		s.seq.Store(f.S)
	}
	switch f.T {
	case "READY":
		var r ready
		if err := json.Unmarshal(f.D, &r); err != nil {
			return fmt.Errorf("qq gateway: decode READY: %w", err)
		}
		s.id = r.SessionID
		if err := s.g.store.SetSession(ctx, qqstore.Session{ID: s.id, Seq: s.seq.Load(), UpdatedAt: s.g.now()}); err != nil {
			return err
		}
		if err := s.g.store.SetBot(ctx, r.User.ID, r.User.Username); err != nil {
			return err
		}
		s.becameReady("identified", r.User.ID, r.User.Username)
		return nil
	case "RESUMED":
		s.becameReady("resumed", "", "")
		return nil
	}
	if s.id == "" {
		return fmt.Errorf("qq gateway: %s before READY", f.T)
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("qq gateway: encode %s: %w", f.T, err)
	}
	inserted, err := s.g.store.Receive(ctx, dedupKey(f), raw, qqstore.Session{ID: s.id, Seq: s.seq.Load(), UpdatedAt: s.g.now()})
	if err != nil {
		return err
	}
	if !inserted {
		s.g.log.Debug("qq gateway: duplicate dispatch", "type", f.T, "id", f.ID)
		return nil
	}
	select {
	case s.g.wake <- struct{}{}:
	default:
	}
	return nil
}

func (s *session) becameReady(how, userID, username string) {
	s.ready = true
	s.g.log.Info("qq: gateway "+how, "session", s.id, "seq", s.seq.Load())
	s.g.record("driver.state", "ready", map[string]any{"state": how, "session": s.id, "seq": s.seq.Load()})
	s.g.ready(userID, username)
}

// invalid handles Invalid Session: the session cannot be resumed, so the
// next connection identifies afresh. Events since the last one received may
// be lost, and QQ has no history to fetch them from: that is a gap.
func (s *session) invalid(ctx context.Context) sessionEnd {
	stored, had, err := s.g.store.Session(ctx)
	if err == nil {
		err = s.g.store.ClearSession(ctx)
	}
	if err != nil {
		return sessionEnd{err: err, resume: false, wasReady: s.ready}
	}
	if had {
		detail := fmt.Sprintf("session %s ended at seq %d; last event %s", stored.ID, stored.Seq, stored.UpdatedAt.Format(time.RFC3339))
		s.g.gap("invalid_session", detail)
	}
	return sessionEnd{err: errors.New("qq gateway: invalid session; identifying again"), resume: false, wasReady: s.ready}
}

// ended classifies a read or write failure by the close code QQ sent.
func (s *session) ended(err error) sessionEnd {
	end := sessionEnd{err: fmt.Errorf("qq gateway: %w", err), resume: true, wasReady: s.ready}
	switch code := websocket.CloseStatus(err); {
	case code == 4914 || code == 4915:
		// The bot is delisted or banned.
		end.fatal = true
	case code == 4001 || code == 4002 || code >= 4010 && code <= 4014:
		// Invalid opcode, payload, shard, or intents: a bug or a console
		// setting, which reconnecting does not fix.
		end.fatal = true
	case code == 4004:
		// The token was refused; fetch a new one and resume.
		s.g.api.forgetToken()
	case code == 4006 || code == 4007 || code >= 4900 && code <= 4913:
		// The session or sequence is invalid: identify afresh.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		end = s.invalid(ctx)
		end.err = fmt.Errorf("qq gateway: closed with %d: %w", code, err)
	}
	return end
}

func (s *session) read(ctx context.Context) (frame, error) {
	_, data, err := s.conn.Read(ctx)
	if err != nil {
		return frame{}, err
	}
	var f frame
	if err := json.Unmarshal(data, &f); err != nil {
		return frame{}, fmt.Errorf("decode frame %q: %w", data, err)
	}
	return f, nil
}

func (s *session) write(ctx context.Context, op int, d any) error {
	data, err := json.Marshal(map[string]any{"op": op, "d": d})
	if err != nil {
		return fmt.Errorf("qq gateway: encode op %d: %w", op, err)
	}
	if err := s.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("qq gateway: write op %d: %w", op, err)
	}
	return nil
}

// dedupKey identifies a dispatch across repeated pushes. A message is keyed
// by its own ID, so the same message pushed twice, or as both an @ event and
// a group message event, is kept once.
func dedupKey(f frame) string {
	var d struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(f.D, &d) == nil && d.ID != "" && isMessage(f.T) {
		return "message:" + d.ID
	}
	if f.ID != "" {
		return "event:" + f.ID
	}
	return fmt.Sprintf("%s:%d", f.T, f.S)
}
