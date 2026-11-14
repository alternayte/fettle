// Command fettle-agentd is the daemon inside each computer (C-01). It reads
// its configuration from FETTLE_AGENTD_* variables and serves the agentd
// endpoints until it gets SIGINT or SIGTERM.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/alternayte/fettle/agentd"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := agentd.ConfigFromEnv(os.Getenv)
	for _, k := range agentd.SecretEnv {
		_ = os.Unsetenv(k)
	}
	if err != nil {
		logger.Error("config", "error", err.Error())
		os.Exit(2)
	}
	cfg.Version = version
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := agentd.Run(ctx, cfg, logger); err != nil {
		logger.Error("agentd stopped", "error", err.Error())
		os.Exit(1)
	}
}
