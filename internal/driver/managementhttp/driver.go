package managementhttp

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"ex-otogi/pkg/otogi/core"
	panel "ex-otogi/pkg/otogi/management"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

const (
	defaultDriverName         = "management-http"
	defaultListenPort         = "8080"
	defaultShutdownGrace      = 5 * time.Second
	defaultManagementHostOnly = "127.0.0.1"
)

// Config configures the management HTTP driver.
type Config struct {
	// Name is the stable driver identifier.
	Name string
	// ListenAddress configures the bind address. A host-only value is expanded
	// with the default management port.
	ListenAddress string
	// BearerToken is the required bearer token for all HTTP API requests.
	BearerToken string
}

// Driver exposes the read-only management API over HTTP.
type Driver struct {
	name         string
	listenAddr   string
	bearerToken  string
	query        panel.Query
	handler      http.Handler
	server       *http.Server
	serverErr    chan error
	mu           sync.Mutex
	shutdownOnce sync.Once
}

// New constructs one management HTTP driver.
func New(query panel.Query, cfg Config) (*Driver, error) {
	if query == nil {
		return nil, fmt.Errorf("new management http driver: nil query service")
	}
	token := strings.TrimSpace(cfg.BearerToken)
	if token == "" {
		return nil, fmt.Errorf("new management http driver: empty bearer token")
	}

	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = defaultDriverName
	}
	listenAddr := normalizeListenAddress(cfg.ListenAddress)
	handler, err := newHandler(query, token)
	if err != nil {
		return nil, fmt.Errorf("new management http driver: %w", err)
	}

	return &Driver{
		name:        name,
		listenAddr:  listenAddr,
		bearerToken: token,
		query:       query,
		handler:     handler,
		serverErr:   make(chan error, 1),
	}, nil
}

// Name returns the stable driver identifier.
func (d *Driver) Name() string {
	return d.name
}

// Start binds the configured listener and serves the management API until
// cancellation or fatal server failure.
func (d *Driver) Start(ctx context.Context, _ core.EventDispatcher) error {
	if d == nil {
		return fmt.Errorf("start management http driver: nil driver")
	}
	if ctx == nil {
		return fmt.Errorf("start management http driver: nil context")
	}

	listener, err := net.Listen("tcp", d.listenAddr)
	if err != nil {
		return fmt.Errorf("start management http driver listen %s: %w", d.listenAddr, err)
	}

	server := &http.Server{
		Addr:              listener.Addr().String(),
		Handler:           d.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	d.mu.Lock()
	d.server = server
	d.listenAddr = listener.Addr().String()
	d.mu.Unlock()

	go func() {
		serveErr := server.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			d.serverErr <- serveErr
			return
		}
		d.serverErr <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultShutdownGrace)
		defer cancel()
		if err := d.shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("start management http driver shutdown: %w", err)
		}
		<-d.serverErr
		return nil
	case err := <-d.serverErr:
		if err != nil {
			return fmt.Errorf("start management http driver serve: %w", err)
		}
		return nil
	}
}

// Shutdown stops the management HTTP server within the supplied context bound.
func (d *Driver) Shutdown(ctx context.Context) error {
	if d == nil {
		return fmt.Errorf("shutdown management http driver: nil driver")
	}
	if ctx == nil {
		return fmt.Errorf("shutdown management http driver: nil context")
	}

	return d.shutdown(ctx)
}

func (d *Driver) shutdown(ctx context.Context) error {
	var shutdownErr error
	d.shutdownOnce.Do(func() {
		d.mu.Lock()
		server := d.server
		d.mu.Unlock()
		if server == nil {
			return
		}
		shutdownErr = server.Shutdown(ctx)
	})
	if shutdownErr != nil {
		return fmt.Errorf("shutdown management http driver: %w", shutdownErr)
	}

	return nil
}

func normalizeListenAddress(addr string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		trimmed = defaultManagementHostOnly
	}
	if host, port, err := net.SplitHostPort(trimmed); err == nil {
		if host == "" {
			host = defaultManagementHostOnly
		}
		if port == "" {
			port = defaultListenPort
		}
		return net.JoinHostPort(host, port)
	}
	return net.JoinHostPort(trimmed, defaultListenPort)
}

func newHandler(query panel.Query, bearerToken string) (http.Handler, error) {
	mux := http.NewServeMux()
	config := huma.DefaultConfig("Ex-Otogi Management API", "1.0.0")
	config.Servers = []*huma.Server{{URL: "/"}}
	api := humago.New(mux, config)
	registerRoutes(api, query)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r.Header.Get("Authorization"), bearerToken) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="management"`)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}

func authorized(headerValue string, token string) bool {
	expected := "Bearer " + token
	if len(headerValue) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(headerValue), []byte(expected)) == 1
}
