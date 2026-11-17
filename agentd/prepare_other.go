//go:build !linux

package agentd

import (
	"context"
	"log/slog"
)

// prepareMachine changes nothing outside Linux. Network rules need nftables,
// so health reports that no rules apply.
func prepareMachine(_ context.Context, cfg Config, logger *slog.Logger) (NetworkState, error) {
	if cfg.Network.Mode == NetworkAllowlist {
		logger.Warn("network rules not applied: nftables needs Linux")
	}
	if len(cfg.CA) > 0 || cfg.Proxy != "" {
		logger.Warn("extra roots and proxy not applied: they need a Linux computer")
	}
	return NetworkState{Mode: cfg.Network.Mode}, nil
}
