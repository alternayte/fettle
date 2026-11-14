package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alternayte/drel"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivermigrate"
	_ "modernc.org/sqlite" // SQLite driver
)

// Database holds the open storage handles. drel serves all feature data; River uses
// its own pool on the same database.
type Database struct {
	Engine   *drel.Engine
	Postgres bool
	// SQLite: the drel pool and River's single-connection pool.
	SQL      *sql.DB
	RiverSQL *sql.DB
	// PostgreSQL: River's pool.
	PGPool *pgxpool.Pool
}

// sqliteDSN enables WAL, a busy timeout, foreign keys and immediate write transactions.
func sqliteDSN(path string) string {
	return "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(15000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
}

// OpenDatabase opens the database named in cfg. It does not migrate.
func OpenDatabase(ctx context.Context, cfg *Config) (*Database, error) {
	if cfg.IsPostgres() {
		eng, err := drel.NewEngine(cfg.DB, drel.WithMaxConns(20))
		if err != nil {
			return nil, fmt.Errorf("open postgres: %w", err)
		}
		pool, err := pgxpool.New(ctx, cfg.DB)
		if err != nil {
			eng.Close()
			return nil, fmt.Errorf("open postgres pool: %w", err)
		}
		return &Database{Engine: eng, Postgres: true, PGPool: pool}, nil
	}
	path := cfg.SQLitePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetConnMaxIdleTime(5 * time.Minute)
	eng, err := drel.NewEngine("", drel.WithSQLDB(db, "sqlite"))
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	rdb, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		eng.Close()
		return nil, err
	}
	rdb.SetMaxOpenConns(1)
	return &Database{Engine: eng, SQL: db, RiverSQL: rdb}, nil
}

// Close closes every handle.
func (d *Database) Close() {
	if d.Engine != nil {
		d.Engine.Close()
	}
	if d.RiverSQL != nil {
		_ = d.RiverSQL.Close()
	}
	if d.PGPool != nil {
		d.PGPool.Close()
	}
}

// Migrate applies drel and River migrations once, under a lock that also serializes
// two processes that start at the same time (REQ-CORE-003).
func (d *Database) Migrate(ctx context.Context, cfg *Config, extra ...func(context.Context) error) (int, error) {
	unlock, err := d.migrationLock(ctx, cfg)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if !d.Postgres {
		// The file lock is held, so a sentinel row left by a crashed process is stale.
		_, _ = d.Engine.Exec(ctx, "DELETE FROM drel_migration_lock WHERE id = 1")
	}
	n, err := d.Engine.ApplyMigrationsFS(ctx, migrationSets(d.Postgres)...)
	if err != nil {
		return n, fmt.Errorf("apply migrations: %w", err)
	}
	if err := d.migrateRiver(ctx); err != nil {
		return n, err
	}
	for _, m := range extra {
		if err := m(ctx); err != nil {
			return n, fmt.Errorf("apply sign-in migrations: %w", err)
		}
	}
	return n, nil
}

func (d *Database) migrateRiver(ctx context.Context) error {
	if d.Postgres {
		m, err := rivermigrate.New(riverpgxv5.New(d.PGPool), nil)
		if err != nil {
			return err
		}
		_, err = m.Migrate(ctx, rivermigrate.DirectionUp, nil)
		return err
	}
	m, err := rivermigrate.New(riversqlite.New(d.RiverSQL), nil)
	if err != nil {
		return err
	}
	_, err = m.Migrate(ctx, rivermigrate.DirectionUp, nil)
	return err
}

// migrationLockKey is the PostgreSQL advisory lock key for Fettle startup migrations.
const migrationLockKey int64 = 0x666574746c65 // "fettle"

func (d *Database) migrationLock(ctx context.Context, cfg *Config) (func(), error) {
	if d.Postgres {
		conn, err := d.PGPool.Acquire(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
			conn.Release()
			return nil, fmt.Errorf("migration lock: %w", err)
		}
		return func() {
			_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockKey)
			conn.Release()
		}, nil
	}
	return lockFile(ctx, cfg.SQLitePath()+".migrate.lock")
}

// ErrNotMigrated means the schema is behind the binary.
var ErrNotMigrated = errors.New("database migrations are not applied")

// Pending reports whether migrations are missing, for /readyz.
func (d *Database) Pending(ctx context.Context) error {
	var n int
	if err := d.Engine.QueryRow(ctx, "SELECT count(*) FROM drel_migrations").Scan(&n); err != nil {
		return fmt.Errorf("%w: %w", ErrNotMigrated, err)
	}
	want := 0
	for _, f := range migrationSets(d.Postgres) {
		ents, err := fsGlobUp(f)
		if err != nil {
			return err
		}
		want += ents
	}
	if n < want {
		return fmt.Errorf("%w: %d of %d", ErrNotMigrated, n, want)
	}
	return nil
}
