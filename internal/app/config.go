package app

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Mode is local (one owner, local token) or server (accounts).
type Mode string

const (
	ModeLocal  Mode = "local"
	ModeServer Mode = "server"
)

// Config holds the settings from Appendix A.
type Config struct {
	Mode        Mode
	Data        string
	DB          string // "sqlite" or a postgres:// URL
	Addr        string
	BaseURL     string
	MasterKey   string
	ObjectStore string
	S3          S3Config
	Workers     int
	ExtraCAFile string
	LogLevel    string
	Telemetry   bool
	OTLP        string
	// Version is set by the build.
	Version string
	// RescueAfterSeconds overrides the River rescue timeout (tests use 2 s).
	RescueAfterSeconds int
}

// S3Config holds the S3-compatible object store settings.
type S3Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey string
	PathStyle                                      bool
}

// DefaultDataDir returns the OS app-data folder in local mode and /var/lib/fettle in server mode.
func DefaultDataDir(mode Mode) string {
	if mode == ModeServer {
		return "/var/lib/fettle"
	}
	if d, err := os.UserConfigDir(); err == nil {
		if runtime.GOOS == "linux" {
			if x := os.Getenv("XDG_DATA_HOME"); x != "" {
				return filepath.Join(x, "fettle")
			}
			if h, err := os.UserHomeDir(); err == nil {
				return filepath.Join(h, ".local", "share", "fettle")
			}
		}
		return filepath.Join(d, "Fettle")
	}
	return ".fettle"
}

// ConfigFromEnv reads Appendix A variables. Flags override the result.
func ConfigFromEnv() Config {
	mode := Mode(strings.ToLower(os.Getenv("FETTLE_MODE")))
	if mode != ModeServer {
		mode = ModeLocal
	}
	c := Config{
		Mode:        mode,
		Data:        os.Getenv("FETTLE_DATA"),
		DB:          os.Getenv("FETTLE_DB"),
		Addr:        os.Getenv("FETTLE_ADDR"),
		BaseURL:     os.Getenv("FETTLE_BASE_URL"),
		MasterKey:   os.Getenv("FETTLE_MASTER_KEY"),
		ObjectStore: os.Getenv("FETTLE_OBJECT_STORE"),
		ExtraCAFile: os.Getenv("FETTLE_EXTRA_CA_FILE"),
		LogLevel:    os.Getenv("FETTLE_LOG_LEVEL"),
		Telemetry:   strings.EqualFold(os.Getenv("FETTLE_TELEMETRY"), "on"),
		OTLP:        os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		S3: S3Config{
			Endpoint:  os.Getenv("FETTLE_S3_ENDPOINT"),
			Region:    os.Getenv("FETTLE_S3_REGION"),
			Bucket:    os.Getenv("FETTLE_S3_BUCKET"),
			AccessKey: os.Getenv("FETTLE_S3_ACCESS_KEY"),
			SecretKey: os.Getenv("FETTLE_S3_SECRET_KEY"),
			PathStyle: os.Getenv("FETTLE_S3_PATH_STYLE") == "true",
		},
	}
	if n, err := strconv.Atoi(os.Getenv("FETTLE_WORKERS")); err == nil {
		c.Workers = n
	}
	if n, err := strconv.Atoi(os.Getenv("FETTLE_RESCUE_AFTER_S")); err == nil && n > 0 {
		c.RescueAfterSeconds = n
	}
	return c
}

// Normalize fills defaults and validates the configuration.
func (c *Config) Normalize() error {
	if c.Mode == "" {
		c.Mode = ModeLocal
	}
	if c.Data == "" {
		c.Data = DefaultDataDir(c.Mode)
	}
	if c.DB == "" {
		c.DB = "sqlite"
	}
	if c.Addr == "" {
		if c.Mode == ModeLocal {
			c.Addr = "127.0.0.1:7420"
		} else {
			c.Addr = ":7420"
		}
	}
	if c.ObjectStore == "" {
		c.ObjectStore = "disk"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.Mode == ModeLocal {
		if err := RequireLoopback(c.Addr); err != nil {
			return err
		}
	}
	if c.Mode == ModeServer && c.MasterKey == "" {
		return errors.New("server mode needs FETTLE_MASTER_KEY (base64, 32 bytes, or file:<path>)")
	}
	if c.ObjectStore != "disk" && c.ObjectStore != "s3" {
		return fmt.Errorf("FETTLE_OBJECT_STORE must be disk or s3, not %q", c.ObjectStore)
	}
	return nil
}

// IsPostgres reports whether the database is PostgreSQL.
func (c *Config) IsPostgres() bool {
	return strings.HasPrefix(c.DB, "postgres://") || strings.HasPrefix(c.DB, "postgresql://")
}

// SQLitePath returns the SQLite database file.
func (c *Config) SQLitePath() string {
	if strings.HasPrefix(c.DB, "sqlite:") && len(c.DB) > len("sqlite:") {
		return strings.TrimPrefix(c.DB, "sqlite:")
	}
	return filepath.Join(c.Data, "fettle.db")
}

// RequireLoopback refuses a listen address that is not a loopback address (SI-11).
func RequireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("local mode listens only on a loopback address, not %q", addr)
	}
	return nil
}
