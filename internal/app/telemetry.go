package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"
)

// TelemetryCounts is the complete telemetry payload. The settings page lists these fields;
// nothing else is ever sent (C-13, REQ-CORE-012).
type TelemetryCounts struct {
	Version   string `json:"version"`
	Mode      string `json:"mode"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Database  string `json:"database"`
	Spaces    int64  `json:"spaces"`
	Bots      int64  `json:"bots"`
	Runs24h   int64  `json:"runs_24h"`
	Providers int64  `json:"model_providers"`
}

// TelemetryFields lists the field names for the settings page.
var TelemetryFields = []string{"version", "mode", "os", "arch", "database", "spaces", "bots", "runs_24h", "model_providers"}

// Telemetry sends anonymous counts once a day when the instance setting is on and
// FETTLE_TELEMETRY_URL names an endpoint.
type Telemetry struct {
	a      *App
	URL    string
	stop   chan struct{}
	once   sync.Once
	period time.Duration
}

// NewTelemetry returns the telemetry sender.
func NewTelemetry(a *App) *Telemetry {
	return &Telemetry{a: a, URL: os.Getenv("FETTLE_TELEMETRY_URL"), stop: make(chan struct{}), period: 24 * time.Hour}
}

// Enabled reports whether telemetry is on: the setting, or FETTLE_TELEMETRY=on at start.
func (t *Telemetry) Enabled(ctx context.Context) bool {
	return t.a.Cfg.Telemetry || t.a.Settings.Bool(ctx, nil, "telemetry.enabled")
}

// Counts collects the payload.
func (t *Telemetry) Counts(ctx context.Context) TelemetryCounts {
	c := TelemetryCounts{Version: t.a.Cfg.Version, Mode: string(t.a.Cfg.Mode), OS: runtime.GOOS, Arch: runtime.GOARCH, Database: "sqlite"}
	if t.a.DB.Postgres {
		c.Database = "postgres"
	}
	e := t.a.DB.Engine
	_ = e.QueryRow(ctx, "SELECT COUNT(*) FROM spaces").Scan(&c.Spaces)
	_ = e.QueryRow(ctx, "SELECT COUNT(*) FROM bots WHERE status = 'active'").Scan(&c.Bots)
	_ = e.QueryRow(ctx, "SELECT COUNT(*) FROM runs WHERE created_at >= $1", time.Now().UTC().Add(-24*time.Hour)).Scan(&c.Runs24h)
	_ = e.QueryRow(ctx, "SELECT COUNT(*) FROM model_providers").Scan(&c.Providers)
	return c
}

// SendNow sends the counts once when telemetry is on. It reports whether it sent.
func (t *Telemetry) SendNow(ctx context.Context) (bool, error) {
	if !t.Enabled(ctx) || t.URL == "" {
		return false, nil
	}
	b, _ := json.Marshal(t.Counts(ctx))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, bytes.NewReader(b))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.a.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	return true, nil
}

// Start sends once now and then every period while the app runs.
func (t *Telemetry) Start(ctx context.Context) {
	go func() {
		_, _ = t.SendNow(ctx)
		tick := time.NewTicker(t.period)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				_, _ = t.SendNow(ctx)
			case <-t.stop:
				return
			}
		}
	}()
}

// Stop ends the sender.
func (t *Telemetry) Stop() { t.once.Do(func() { close(t.stop) }) }
