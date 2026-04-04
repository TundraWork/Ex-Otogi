package main

import (
	"fmt"
	"log/slog"
	"os"

	"ex-otogi/internal/driver/managementhttp"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "openapi" {
		yaml, err := managementhttp.GenerateOpenAPIYAML()
		if err != nil {
			fmt.Fprintf(os.Stderr, "generate openapi: %v\n", err)
			os.Exit(1)
		}
		os.Stdout.Write(yaml)
		os.Exit(0)
	}

	if err := run(); err != nil {
		slog.Error("bot exited with error", "error", err)
		os.Exit(1)
	}
}
