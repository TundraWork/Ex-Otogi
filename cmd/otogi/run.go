package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
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

// run opens the event log, starts the management API, the kernel, and the
// drivers, waits for ctx to end (a signal or POST /api/shutdown) or a driver
// to stop, then shuts down within the configured timeout. It returns the
// first failure, or nil after a requested stop.
func run(ctx context.Context, cfg Config, log *slog.Logger) (failure error) {
	ctx, requestShutdown := context.WithCancel(ctx)
	defer requestShutdown()
	started := time.Now()

	events, err := management.OpenEventLog(ctx, filepath.Join(cfg.DataDir, "eventlog.sqlite"), log)
	if err != nil {
		return err
	}
	defer func() {
		if err := events.Close(); err != nil && failure == nil {
			failure = err
		}
	}()
	process := func(state string, err error) {
		payload := map[string]string{"state": state}
		outcome := "ok"
		if err != nil {
			payload["error"], outcome = err.Error(), "error"
		}
		events.Record(context.Background(), otogi.Record{At: time.Now(), Kind: "process.state", Component: "process", Outcome: outcome, Payload: payload})
	}
	var background sync.WaitGroup
	pruneCtx, stopPruning := context.WithCancel(context.Background())
	background.Go(func() { events.RunPruner(pruneCtx, cfg.EventLog.Retention) })
	defer func() {
		stopPruning()
		background.Wait()
	}()

	modules, err := buildModules(cfg.Modules)
	if err != nil {
		return err
	}
	routes := router{}
	kernelConfig := cfg.Kernel.kernel()
	kernelConfig.Recorder = events
	k, err := kernel.New(kernelConfig, modules, nil, routes, log)
	if err != nil {
		return err
	}
	var drivers []driver
	var statuses []func(context.Context) any
	for _, a := range cfg.Telegram {
		d := telegram.New(telegram.Config{
			AppID: a.AppID, AppHash: a.AppHash, AccountKey: a.Account,
			SessionPath: a.Session, DatabasePath: a.Database,
			Kernel: k, Logger: log, Recorder: events,
		})
		routes[destination{telegram.Platform, otogi.AccountID(a.Account)}] = d
		drivers = append(drivers, d)
		statuses = append(statuses, func(ctx context.Context) any { return d.Status(ctx) })
	}

	if addr := *cfg.Management.Addr; addr != "" {
		server, err := management.StartServer(management.ServerConfig{
			Addr: addr, Token: cfg.Management.Token, Events: events, Logger: log,
			Shutdown: requestShutdown,
			Ready:    func() bool { return allReady(drivers) },
			Status: func(ctx context.Context) any {
				driverStatus := make([]any, len(statuses))
				for i, status := range statuses {
					driverStatus[i] = status(ctx)
				}
				return map[string]any{
					"version": version(), "started_at": started, "uptime": time.Since(started).Round(time.Second).String(),
					"ready": allReady(drivers), "kernel": k.Stats(), "drivers": driverStatus, "event_log": events.Health(ctx),
				}
			},
		})
		if err != nil {
			return err
		}
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Stop(stopCtx); err != nil && failure == nil {
				failure = err
			}
		}()
	}

	if err := k.Start(ctx); err != nil {
		return err
	}
	var running []driver
	for _, d := range drivers {
		if err := d.Start(ctx); err != nil {
			failure = err
			break
		}
		running = append(running, d)
	}
	if failure == nil {
		log.Info("otogi: running", "modules", len(modules), "drivers", len(running))
		process("running", nil)
		failure = waitForStop(ctx, running)
	}
	if ctx.Err() != nil && errors.Is(failure, ctx.Err()) {
		failure = nil // a stop was requested during startup
	}
	process("stopping", failure)
	if err := shutdown(cfg, k, running, log); err != nil && failure == nil {
		failure = err
	}
	process("stopped", failure)
	return failure
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
// events, then stops drivers and the kernel, all within ShutdownTimeout.
func shutdown(cfg Config, k *kernel.Kernel, drivers []driver, log *slog.Logger) error {
	log.Info("otogi: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
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
