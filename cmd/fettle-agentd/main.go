// Command fettle-agentd is the daemon inside each computer (C-01). It reads
// its configuration from FETTLE_AGENTD_* variables and serves the agentd
// endpoints until it gets SIGINT or SIGTERM, or until the pipe in
// FETTLE_AGENTD_PARENT_FD ends.
package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/alternayte/fettle/agentd"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if len(os.Args) > 1 && os.Args[1] == "filter-proxy" {
		// The filter proxy runs as its own user; agentd starts it at boot (§10.5).
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := agentd.RunFilterProxy(ctx, os.Stdin, logger.With("process", "filter-proxy")); err != nil {
			logger.Error("filter proxy stopped", "error", err.Error())
			os.Exit(1)
		}
		return
	}
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The host provider passes a pipe whose write end it holds. The end of the pipe means
	// that the parent process is gone, so agentd stops instead of running on as an orphan.
	if v := os.Getenv("FETTLE_AGENTD_PARENT_FD"); v != "" {
		fd, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			logger.Error("config", "error", "FETTLE_AGENTD_PARENT_FD is not a number")
			os.Exit(2)
		}
		parent := os.NewFile(uintptr(fd), "parent")
		go func() {
			_, _ = io.Copy(io.Discard, parent)
			logger.Info("the parent process is gone; agentd stops")
			cancel()
		}()
	}
	if err := agentd.Run(ctx, cfg, logger); err != nil {
		logger.Error("agentd stopped", "error", err.Error())
		os.Exit(1)
	}
}
