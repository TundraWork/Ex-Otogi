package main

import (
	"log/slog"
	"os"
)

func main() {
	if err := run(); err != nil {
		slog.Error("bot exited with error", "error", err)
		os.Exit(1)
	}
}
