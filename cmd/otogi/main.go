// Command otogi runs the Ex-Otogi bot. See docs/runbooks for operations.
//
//	otogi [-config otogi.yaml] [run]        run until SIGINT or SIGTERM
//	otogi [-config otogi.yaml] validate     check the config file
//	otogi [-config otogi.yaml] login ACCOUNT  log a Telegram account in
//	otogi smoke                             offline smoke test
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram"
)

func main() { os.Exit(mainExit()) }

func mainExit() int {
	flags := flag.NewFlagSet("otogi", flag.ContinueOnError)
	configPath := flags.String("config", "otogi.yaml", "path to the config file")
	if err := flags.Parse(os.Args[1:]); err != nil {
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
	switch command {
	case "smoke":
		err = smoke(ctx, os.Stdout)
	case "run", "validate", "login":
		err = withConfig(ctx, *configPath, command, args)
	default:
		err = fmt.Errorf("unknown command %q (want run, validate, login, or smoke)", command)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "otogi:", err)
		return 1
	}
	return 0
}

func withConfig(ctx context.Context, path, command string, args []string) error {
	cfg, err := loadConfig(path)
	if err != nil {
		return err
	}
	switch command {
	case "validate":
		if _, err := buildModules(cfg.Modules); err != nil {
			return err
		}
		fmt.Printf("%s: OK (%d Telegram account(s), %d module(s))\n", path, len(cfg.Telegram), len(cfg.Modules))
		return nil
	case "login":
		if len(args) != 1 {
			return errors.New("usage: otogi login ACCOUNT")
		}
		return login(ctx, cfg, args[0])
	default:
		log, err := newLogger(cfg.Log)
		if err != nil {
			return err
		}
		return run(ctx, cfg, log)
	}
}

func login(ctx context.Context, cfg Config, account string) error {
	for _, a := range cfg.Telegram {
		if a.Account == account {
			who, err := telegram.Login(ctx, telegram.SessionConfig{AppID: a.AppID, AppHash: a.AppHash, SessionPath: a.Session}, newTerminal())
			if err != nil {
				return err
			}
			fmt.Printf("Logged in %s as %s\n", account, who)
			return nil
		}
	}
	return fmt.Errorf("no Telegram account %q in the config", account)
}

func newLogger(cfg LogConfig) (*slog.Logger, error) {
	level, err := logLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	options := &slog.HandlerOptions{Level: level}
	if cfg.Format == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, options)), nil
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, options)), nil
}

func logLevel(name string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(name)); err != nil {
		return 0, fmt.Errorf("log.level %q: want debug, info, warn, or error", name)
	}
	return level, nil
}
