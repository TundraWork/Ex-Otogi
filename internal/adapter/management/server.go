package management

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"
)

// ServerConfig configures the management HTTP API.
type ServerConfig struct {
	// Addr is the listen address, for example "127.0.0.1:8090".
	Addr string
	// Token, if set, is required as a bearer token on /api routes. It must be
	// set when Addr is not a loopback address.
	Token string
	// Events is the event log to query.
	Events *EventLog
	// Status returns the current status document for GET /api/status.
	Status func(ctx context.Context) any
	// Ready reports whether the process should receive work (GET /readyz).
	Ready func() bool
	// Shutdown starts a graceful shutdown (POST /api/shutdown).
	Shutdown func()
	// Logger receives server errors.
	Logger *slog.Logger
}

// Server is a running management API.
type Server struct {
	http *http.Server
	ln   net.Listener
	done chan error
}

// StartServer listens on cfg.Addr and serves the API until Stop.
func StartServer(cfg ServerConfig) (*Server, error) {
	if cfg.Token == "" && !loopback(cfg.Addr) {
		return nil, fmt.Errorf("management: %s is not a loopback address, so management.token is required", cfg.Addr)
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("management: listen: %w", err)
	}
	s := &Server{
		http: &http.Server{Handler: newHandler(cfg), ReadHeaderTimeout: 10 * time.Second},
		ln:   ln,
		done: make(chan error, 1),
	}
	go func() { s.done <- s.http.Serve(ln) }()
	cfg.Logger.Info("management: listening", "addr", ln.Addr().String())
	return s, nil
}

// Addr returns the address the server listens on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Stop shuts the server down gracefully within ctx.
func (s *Server) Stop(ctx context.Context) error {
	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("management: shutdown: %w", err)
	}
	if err := <-s.done; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("management: serve: %w", err)
	}
	return nil
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func newHandler(cfg ServerConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if cfg.Ready() {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready"})
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, cfg.Status(r.Context()))
	})
	api.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		q, err := parseQuery(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		page, err := cfg.Events.Query(r.Context(), q)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, page)
	})
	api.HandleFunc("GET /api/events/{id}/trace", func(w http.ResponseWriter, r *http.Request) {
		entries, err := cfg.Events.Trace(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"records": entries})
	})
	api.HandleFunc("POST /api/shutdown", func(w http.ResponseWriter, _ *http.Request) {
		cfg.Shutdown()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "shutting down"})
	})
	mux.Handle("/api/", authorize(cfg.Token, api))
	return mux
}

func authorize(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or wrong bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func parseQuery(r *http.Request) (Query, error) {
	v := r.URL.Query()
	q := Query{
		Kind: v.Get("kind"), Component: v.Get("component"), Event: v.Get("event"), Platform: v.Get("platform"),
		Account: v.Get("account"), Conversation: v.Get("conversation"), Outcome: v.Get("outcome"), Text: v.Get("q"),
	}
	for _, f := range []struct {
		name string
		dst  *int64
	}{{"before", &q.Before}, {"after", &q.After}} {
		if s := v.Get(f.name); s != "" {
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil || n <= 0 {
				return Query{}, fmt.Errorf("%s must be a positive record ID", f.name)
			}
			*f.dst = n
		}
	}
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return Query{}, errors.New("limit must be a positive integer")
		}
		q.Limit = n
	}
	for _, f := range []struct {
		name string
		dst  *time.Time
	}{{"since", &q.Since}, {"until", &q.Until}} {
		if s := v.Get(f.name); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return Query{}, fmt.Errorf("%s must be an RFC 3339 time", f.name)
			}
			*f.dst = t
		}
	}
	return q, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body) // the client is gone if this fails
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
