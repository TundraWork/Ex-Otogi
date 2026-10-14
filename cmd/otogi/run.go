package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

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
}

// run starts the kernel and drivers, waits for ctx to end or a driver to
// stop, then shuts everything down within the configured timeout. It returns
// the first failure, or nil after a requested stop.
func run(ctx context.Context, cfg Config, log *slog.Logger) error {
	modules, err := buildModules(cfg.Modules)
	if err != nil {
		return err
	}
	routes := router{}
	k, err := kernel.New(cfg.Kernel.kernel(), modules, nil, routes, log)
	if err != nil {
		return err
	}
	var drivers []driver
	for _, a := range cfg.Telegram {
		d := telegram.New(telegram.Config{
			AppID: a.AppID, AppHash: a.AppHash, AccountKey: a.Account,
			SessionPath: a.Session, DatabasePath: a.Database,
			Kernel: k, Logger: log,
		})
		routes[destination{telegram.Platform, otogi.AccountID(a.Account)}] = d
		drivers = append(drivers, d)
	}

	if err := k.Start(ctx); err != nil {
		return err
	}
	var started []driver
	var failure error
	for _, d := range drivers {
		if err := d.Start(ctx); err != nil {
			failure = err
			break
		}
		started = append(started, d)
	}
	if failure == nil {
		log.Info("otogi: running", "modules", len(modules), "drivers", len(started))
		failure = waitForStop(ctx, started)
	}
	if ctx.Err() != nil && errors.Is(failure, ctx.Err()) {
		failure = nil // a signal arrived during startup: a requested stop
	}
	if err := shutdown(cfg, k, started, log); err != nil && failure == nil {
		failure = err
	}
	return failure
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
