// Package testenv gives tests a migrated SQLite database and a migrated PostgreSQL
// database from a shared testcontainer, so each storage test runs on both (REQ-CORE-002).
package testenv

import (
	"context"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/alternayte/fettle/internal/app"
)

// Dialects lists the storage backends every storage test runs on.
var Dialects = []string{"sqlite", "postgres"}

var (
	pgOnce    sync.Once
	pgBaseURL string // URL of the maintenance database
	pgErr     error
	pgSeq     atomic.Int64
	tmplOnce  sync.Once
	tmplErr   error
)

const templateDB = "fettle_template"

func startPostgres() {
	if u := os.Getenv("FETTLE_TEST_PG"); u != "" {
		pgBaseURL = u
		return
	}
	tclog.SetDefault(stdlog.New(io.Discard, "", 0))
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		// The log line can come before Docker Desktop forwards the host port, so both wait.
		testcontainers.WithWaitStrategy(wait.ForAll(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
			wait.ForListeningPort("5432/tcp"),
		).WithDeadline(90*time.Second)),
		testcontainers.WithCmd("postgres", "-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off", "-c", "max_connections=500"),
	)
	if err != nil {
		pgErr = err
		return
	}
	pgBaseURL, pgErr = c.ConnectionString(ctx, "sslmode=disable")
}

func withDB(base, name string) string {
	i := strings.LastIndex(base, "/")
	q := ""
	if j := strings.Index(base[i:], "?"); j >= 0 {
		q = base[i+j:]
	}
	return base[:i+1] + name + q
}

func adminExec(ctx context.Context, sql string) error {
	conn, err := pgx.Connect(ctx, pgBaseURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, sql)
	return err
}

// PostgresURL creates a fresh migrated database and returns its URL.
func PostgresURL(t testing.TB) string {
	t.Helper()
	pgOnce.Do(startPostgres)
	if pgErr != nil {
		t.Fatalf("postgres testcontainer: %v", pgErr)
	}
	ctx := context.Background()
	tmplOnce.Do(func() {
		// The first connection can meet a port that the host forwards a moment late.
		for i := 0; i < 30; i++ {
			if tmplErr = adminExec(ctx, "SELECT 1"); tmplErr == nil {
				break
			}
			time.Sleep(time.Second)
		}
		if tmplErr != nil {
			return
		}
		_ = adminExec(ctx, "DROP DATABASE IF EXISTS "+templateDB)
		if tmplErr = adminExec(ctx, "CREATE DATABASE "+templateDB); tmplErr != nil {
			return
		}
		cfg := app.Config{DB: withDB(pgBaseURL, templateDB)}
		d, err := app.OpenDatabase(ctx, &cfg)
		if err != nil {
			tmplErr = err
			return
		}
		_, tmplErr = d.Migrate(ctx, &cfg)
		d.Close()
	})
	if tmplErr != nil {
		t.Fatalf("postgres template: %v", tmplErr)
	}
	name := fmt.Sprintf("fettle_t%d_%d", os.Getpid(), pgSeq.Add(1))
	if err := adminExec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, templateDB)); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() { _ = adminExec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })
	return withDB(pgBaseURL, name)
}

// EmptyPostgresURL creates a fresh database without migrations.
func EmptyPostgresURL(t testing.TB) string {
	t.Helper()
	pgOnce.Do(startPostgres)
	if pgErr != nil {
		t.Fatalf("postgres testcontainer: %v", pgErr)
	}
	name := fmt.Sprintf("fettle_e%d_%d", os.Getpid(), pgSeq.Add(1))
	if err := adminExec(context.Background(), "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() { _ = adminExec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })
	return withDB(pgBaseURL, name)
}

// Config returns a config with a temporary data folder for one dialect.
func Config(t testing.TB, dialect string) *app.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := &app.Config{Mode: app.ModeLocal, Data: dir, Addr: "127.0.0.1:0", ObjectStore: "disk", MasterKey: TestMasterKey, LogLevel: "warn"}
	if dialect == "postgres" {
		cfg.DB = PostgresURL(t)
	} else {
		cfg.DB = "sqlite:" + filepath.Join(dir, "fettle.db")
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestMasterKey is a fixed master key for tests (32 zero bytes are never used in production).
const TestMasterKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

// Open returns a migrated database for one dialect.
func Open(t testing.TB, dialect string) (*app.Database, *app.Config) {
	t.Helper()
	cfg := Config(t, dialect)
	ctx := context.Background()
	d, err := app.OpenDatabase(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	if _, err := d.Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	return d, cfg
}

// Each runs fn once per dialect as a subtest.
func Each(t *testing.T, fn func(t *testing.T, dialect string)) {
	t.Helper()
	for _, d := range Dialects {
		t.Run(d, func(t *testing.T) { fn(t, d) })
	}
}
