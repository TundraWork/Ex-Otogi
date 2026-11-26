// Command otogi runs the Ex-Otogi bot. See docs/runbooks for operations.
//
//	otogi [-config otogi.yaml] [run]        run until SIGINT or SIGTERM
//	otogi [-config otogi.yaml] validate     check the config file
//	otogi [-config otogi.yaml] login ACCOUNT  log a Telegram account in
//	otogi [-config otogi.yaml] settings revert ID
//	                                        put back the settings before the
//	                                        last change, with the bot stopped
//	otogi smoke                             offline smoke test
//	otogi openapi                           print the management API's OpenAPI document
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// exitRestart is the exit code after POST /api/restart: not zero, so that
// the supervisor starts the process again (see docs/operations.md).
const exitRestart = 75

func main() { os.Exit(mainExit(os.Args[1:])) }

func mainExit(arguments []string) int {
	flags := flag.NewFlagSet("otogi", flag.ContinueOnError)
	configPath := flags.String("config", "otogi.yaml", "path to the config file")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	command, args := "run", flags.Args()
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop() // restore default handling: a second signal ends the process
	}()

	var err error
	var restart bool
	switch command {
	case "smoke":
		err = smoke(ctx, os.Stdout)
	case "openapi":
		var doc []byte
		if doc, err = management.OpenAPI(); err == nil {
			_, err = fmt.Printf("%s\n", doc)
		}
	case "run", "validate", "login", "settings":
		restart, err = withConfig(ctx, *configPath, command, args)
	default:
		err = fmt.Errorf("unknown command %q (want run, validate, login, settings, smoke, or openapi)", command)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "otogi:", err)
		return 1
	}
	if restart {
		return exitRestart
	}
	return 0
}

// withConfig runs a command that reads the config file. restart is run's.
func withConfig(ctx context.Context, path, command string, args []string) (restart bool, err error) {
	cfg, err := loadConfig(path)
	if err != nil {
		return false, err
	}
	switch command {
	case "validate":
		fmt.Printf("%s: OK\n", path)
		return false, nil
	case "login":
		if len(args) != 1 {
			return false, errors.New("usage: otogi login ACCOUNT")
		}
		return false, offline(ctx, cfg, func(s *moduleSettings) error { return login(ctx, s, cfg, args[0]) })
	case "settings":
		if len(args) != 2 || args[0] != "revert" {
			return false, errors.New("usage: otogi settings revert ID, where ID is a section such as system:accounts or a module's ID")
		}
		return false, offline(ctx, cfg, func(s *moduleSettings) error { return s.revert(ctx, otogi.ModuleID(args[1]), os.Stdout) })
	default:
		level := new(slog.LevelVar)
		return run(ctx, cfg, func(format string) *slog.Logger { return newLogger(format, level) }, level)
	}
}

// offline runs a command on the settings in the data directory, outside the
// running process. What it changes is recorded in the event log, and it
// writes to standard error only what goes wrong.
func offline(ctx context.Context, cfg Config, command func(*moduleSettings) error) (failure error) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	storage, err := modulestore.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer func() {
		if err := storage.Close(); err != nil && failure == nil {
			failure = err
		}
	}()
	events, err := management.OpenEventLog(ctx, filepath.Join(cfg.DataDir, "eventlog.sqlite"), log)
	if err != nil {
		return err
	}
	defer func() {
		if err := events.Close(); err != nil && failure == nil {
			failure = err
		}
	}()
	return command(&moduleSettings{store: storage, kinds: moduleKinds, recorder: events, log: log})
}

// login logs in a Telegram account of those a start would run: the ones set
// in the panel.
func login(ctx context.Context, s *moduleSettings, cfg Config, account string) error {
	accounts, _, err := s.startAccounts(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if a.Account == account {
			session := telegram.SessionConfig{AppID: a.AppID, AppHash: a.AppHash, SessionPath: a.Session}
			who, err := logIn(ctx, telegramSteps(), session, newTerminal())
			if err != nil {
				return err
			}
			fmt.Printf("Logged in %s as %s\n", account, who)
			return nil
		}
	}
	return fmt.Errorf("no Telegram account %q among the accounts set in the panel", account)
}

// newLogger returns the process log. Its level is the general settings',
// which set it when they are loaded and each time they are saved.
func newLogger(format string, level *slog.LevelVar) *slog.Logger {
	options := &slog.HandlerOptions{Level: level}
	if format == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, options))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, options))
}

func logLevel(name string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(name)); err != nil {
		return 0, fmt.Errorf("%q: want debug, info, warn, or error", name)
	}
	return level, nil
}
