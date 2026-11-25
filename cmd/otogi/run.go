package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/qq"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/modules/utility"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/TundraWork/Ex-Otogi/web"
)

// driver is the lifecycle every platform driver provides.
type driver interface {
	Start(ctx context.Context) error
	Drain(ctx context.Context) error
	Stop(ctx context.Context) error
	Done() <-chan struct{}
	Err() error
	Ready() bool
}

// run opens the module store and the event log, starts the management API,
// the kernel, and the drivers of the accounts set in the panel, waits for ctx
// to end (a signal, POST /api/shutdown, or POST /api/restart) or a driver to
// stop, then shuts down within the shutdown timeout. It returns the first
// failure, or nil after a requested stop; restart says the stop was asked
// for with POST /api/restart, for the process to be started again. newLog
// makes the process log, in the format the process settings name.
func run(ctx context.Context, cfg Config, newLog func(format string) *slog.Logger, level *slog.LevelVar) (restart bool, failure error) {
	ctx, requestShutdown := context.WithCancel(ctx)
	defer requestShutdown()
	started := time.Now()

	storage, err := modulestore.Open(ctx, cfg.DataDir)
	if err != nil {
		return false, err
	}
	defer func() {
		if err := storage.Close(); err != nil && failure == nil {
			failure = err
		}
	}()
	settings := &moduleSettings{store: storage, kinds: moduleKinds}
	limits, err := settings.startProcess(ctx, cfg)
	if err != nil {
		return false, err
	}
	log := newLog(limits.LogFormat)
	events, err := management.OpenEventLog(ctx, filepath.Join(cfg.DataDir, "eventlog.sqlite"), log)
	if err != nil {
		return false, err
	}
	defer func() {
		if err := events.Close(); err != nil && failure == nil {
			failure = err
		}
	}()
	var restartBy atomic.Pointer[string] // who asked for a restart, once one did
	process := func(state string, err error) {
		payload := map[string]string{"state": state}
		outcome := "ok"
		if err != nil {
			payload["error"], outcome = err.Error(), "error"
		}
		if by := restartBy.Load(); by != nil {
			payload["restart_requested_by"] = *by
		}
		events.Record(context.Background(), otogi.Record{At: time.Now(), Kind: "process.state", Component: "process", Outcome: outcome, Payload: payload})
	}
	// Modules get views of the runtime built below; status and commands are
	// only called once the process runs.
	var status func(context.Context) utility.Status
	var k *kernel.Kernel
	sys := newSystem(level, events, log)
	settings.recorder, settings.log, settings.system = events, log, sys
	settings.env = moduleEnv{
		operators: sys.operatorList,
		llm:       sys.provider,
		status:    func(ctx context.Context) utility.Status { return status(ctx) },
		commands:  func() []utility.CommandInfo { return commandsOf(k.Descriptors()) },
	}
	if err := settings.loadSections(ctx, cfg); err != nil {
		return false, err
	}
	telegramAccounts, qqAccounts, err := settings.startAccounts(ctx, cfg)
	if err != nil {
		return false, err
	}
	// The pruner starts once the retention is loaded.
	var background sync.WaitGroup
	pruneCtx, stopPruning := context.WithCancel(context.Background())
	background.Go(func() { events.RunPruner(pruneCtx, sys.retention) })
	defer func() {
		stopPruning()
		background.Wait()
	}()
	routes := newRouter()
	kernelConfig := kernel.Config{
		Workers: limits.Workers, MaxInFlight: limits.MaxInFlight, HandlerTimeout: time.Duration(limits.HandlerTimeout),
		Recorder: events, Storage: storage,
	}
	middleware := []otogi.Middleware{namesMiddleware{events: events, log: log}, kernel.CommandParser(&sys.prefixes)}
	k, err = kernel.New(kernelConfig, nil, middleware, routes, log) // the modules join once it runs
	if err != nil {
		return false, err
	}
	settings.kernel = k
	var drivers []driver
	var statuses []func(context.Context) management.DriverStatus
	var telegramDrivers []*telegram.Driver
	for _, a := range telegramAccounts {
		d := telegram.New(telegram.Config{
			AppID: a.AppID, AppHash: a.AppHash, AccountKey: a.Account,
			SessionPath: a.Session, DatabasePath: a.Database, UploadThreads: a.UploadThreads,
			Kernel: k, Logger: log, Recorder: events,
		})
		routes.set(destination{telegram.Platform, otogi.AccountID(a.Account)}, d)
		drivers = append(drivers, d)
		statuses = append(statuses, func(ctx context.Context) management.DriverStatus {
			s := d.Status(ctx)
			return management.DriverStatus{Platform: string(s.Platform), Account: s.Account, Ready: s.Ready, Error: s.Error,
				PendingInbox: s.PendingInbox, FailedInbox: s.FailedInbox, Gaps: s.Gaps, Details: s}
		})
		telegramDrivers = append(telegramDrivers, d)
	}
	var qqDrivers []*qq.Driver
	for _, a := range qqAccounts {
		d := qq.New(qq.Config{
			AccountKey: a.Account, AppID: a.AppID, AppSecret: a.AppSecret, DatabasePath: a.Database,
			Kernel: k, Logger: log, Recorder: events,
		})
		routes.set(destination{qq.Platform, otogi.AccountID(a.Account)}, d)
		drivers = append(drivers, d)
		statuses = append(statuses, func(ctx context.Context) management.DriverStatus {
			s := d.Status(ctx)
			return management.DriverStatus{Platform: string(s.Platform), Account: s.Account, Ready: s.Ready, Error: s.Error,
				PendingInbox: s.PendingInbox, FailedInbox: s.FailedInbox, Gaps: s.Gaps, Details: s}
		})
		qqDrivers = append(qqDrivers, d)
	}
	status = func(ctx context.Context) utility.Status {
		stats, health := k.Stats(), events.Health(ctx)
		s := utility.Status{
			Version: version(), Uptime: time.Since(started), Ready: allReady(drivers),
			Kernel:   utility.KernelStatus{State: stats.State, InFlight: stats.InFlight, MaxInFlight: stats.MaxInFlight, Workers: stats.Workers},
			EventLog: utility.EventLogStatus{OK: health.OK, LastError: health.LastError},
		}
		for _, d := range telegramDrivers {
			ds := d.Status(ctx)
			s.Drivers = append(s.Drivers, utility.DriverStatus{
				Name: "telegram/" + ds.Account, Ready: ds.Ready, Error: ds.Error,
				Detail: fmt.Sprintf("%d pending, %d failed inbox rows, %d gaps", ds.PendingInbox, ds.FailedInbox, ds.Gaps),
			})
		}
		for _, d := range qqDrivers {
			ds := d.Status(ctx)
			s.Drivers = append(s.Drivers, utility.DriverStatus{
				Name: "qq/" + ds.Account, Ready: ds.Ready, Error: ds.Error,
				Detail: fmt.Sprintf("%d pending, %d failed inbox rows, %d gaps", ds.PendingInbox, ds.FailedInbox, ds.Gaps),
			})
		}
		return s
	}

	if addr := *cfg.Management.Addr; addr != "" {
		var key []byte
		if cfg.Management.PublicURL != "" {
			if key, err = management.LoadCookieKey(filepath.Join(cfg.DataDir, "panel.key")); err != nil {
				return false, err
			}
		}
		o := cfg.Management.OIDC
		server, err := management.StartServer(management.ServerConfig{
			PublicURL: cfg.Management.PublicURL, CookieKey: key, Panel: web.Panel(),
			OIDC: management.OIDC{Issuer: o.Issuer, ClientID: o.ClientID, ClientSecret: o.ClientSecret, Allow: o.Allow},
			Addr: addr, Token: cfg.Management.Token, Events: events, Logger: log,
			ConfigFile: cfg.path, DataDir: cfg.DataDir,
			Restart: func(by string) {
				restartBy.Store(&by)
				log.Info("otogi: restart requested", "by", by)
				requestShutdown()
			},
			Shutdown: requestShutdown, Modules: newModuleAPI(settings), System: newSystemAPI(settings), Secrets: newModuleAPI(settings), Media: routes.FetchMedia, Version: version(),
			Ready: func() bool { return allReady(drivers) },
			Running: func() []management.Running {
				out := []management.Running{}
				for _, r := range k.Running() {
					out = append(out, management.Running{
						Module: string(r.Module), Origin: r.Origin, Event: string(r.Event),
						Platform: string(r.Platform), Account: string(r.Account), Conversation: string(r.Conversation), Started: r.Started,
					})
				}
				return out
			},
			Status: func(ctx context.Context) management.Status {
				stats := k.Stats()
				s := management.Status{
					Version: version(), StartedAt: started, Ready: allReady(drivers), Drivers: []management.DriverStatus{},
					Kernel: management.KernelStatus{
						State: stats.State, InFlight: stats.InFlight, MaxInFlight: stats.MaxInFlight, Workers: stats.Workers,
						HandlerTimeoutMS: stats.HandlerTimeout.Milliseconds(),
					},
					EventLog: management.EventLogStatus{Health: events.Health(ctx), RetentionMS: sys.retention().Milliseconds()},
				}
				for _, status := range statuses {
					d := status(ctx)
					d.LastEvent = events.LastReceived(ctx, d.Platform, d.Account)
					s.Drivers = append(s.Drivers, d)
				}
				return s
			},
		})
		if err != nil {
			return false, err
		}
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Stop(stopCtx); err != nil && failure == nil {
				failure = err
			}
		}()
	}

	// A stop may be asked for from here on: the management API is up before
	// the kernel and the drivers are. Whatever fails to start then stops the
	// rest the same way, so the exit says a stop or a restart was asked for.
	failure = k.Start(ctx)
	if failure == nil {
		// Each module that is on joins on its own, so one that fails to
		// start stays out instead of stopping the process.
		settings.mu.Lock()
		_, failure = settings.startWaiting(ctx)
		settings.mu.Unlock()
	}
	var running []driver
	for _, d := range drivers {
		if failure != nil {
			break
		}
		if failure = d.Start(ctx); failure == nil {
			running = append(running, d)
		}
	}
	if failure == nil {
		log.Info("otogi: running", "modules", len(k.Descriptors()), "drivers", len(running))
		process("running", nil)
		failure = waitForStop(ctx, running)
	}
	if ctx.Err() != nil && errors.Is(failure, ctx.Err()) {
		failure = nil // a stop was requested during startup
	}
	process("stopping", failure)
	if err := shutdown(cmp.Or(time.Duration(limits.ShutdownTimeout), 30*time.Second), k, running, log); err != nil && failure == nil {
		failure = err
	}
	process("stopped", failure)
	return failure == nil && restartBy.Load() != nil, failure
}

func allReady(drivers []driver) bool {
	for _, d := range drivers {
		if !d.Ready() {
			return false
		}
	}
	return true
}

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	v := info.Main.Version
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			v += " " + s.Value
		}
	}
	return v
}

// waitForStop returns nil when ctx ends, or a driver's error when one stops.
func waitForStop(ctx context.Context, drivers []driver) error {
	stopped := make(chan driver, len(drivers))
	for _, d := range drivers {
		go func() {
			select {
			case <-d.Done():
				stopped <- d
			case <-ctx.Done():
			}
		}()
	}
	select {
	case <-ctx.Done():
		return nil
	case d := <-stopped:
		if err := d.Err(); err != nil {
			return err
		}
		return errors.New("a driver stopped unexpectedly")
	}
}

// shutdown drains drivers (no new dispatch), lets the kernel finish in-flight
// events, then stops drivers and the kernel, all within timeout.
func shutdown(timeout time.Duration, k *kernel.Kernel, drivers []driver, log *slog.Logger) error {
	log.Info("otogi: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var errs []error
	for _, d := range drivers {
		errs = append(errs, d.Drain(ctx))
	}
	if err := k.Drain(ctx); err != nil {
		errs = append(errs, fmt.Errorf("drain kernel: %w", err))
	}
	for _, d := range drivers {
		errs = append(errs, d.Stop(ctx))
	}
	errs = append(errs, k.Stop(ctx))
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	log.Info("otogi: stopped")
	return nil
}
