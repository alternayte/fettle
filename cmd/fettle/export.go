package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/vault"
)

func init() {
	register("export", "Write a portable archive of a space (--space, --out; FETTLE_EXPORT_PASSPHRASE adds the vault)", runExport)
	register("import", "Read a space archive into this instance (FETTLE_EXPORT_PASSPHRASE opens its vault)", runImport)
}

// openOffline opens the app of a data folder without serving it.
func openOffline(ctx context.Context, args []string, name string, extra func(fs *flag.FlagSet)) (*app.App, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	cfg := configFlags(fs)
	if extra != nil {
		extra(fs)
	}
	pos, err := parseArgs(fs, args)
	if err != nil {
		return nil, nil, err
	}
	a, err := app.New(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return a, pos, nil
}

func runExport(ctx context.Context, args []string) error {
	var space, out string
	a, _, err := openOffline(ctx, args, "export", func(fs *flag.FlagSet) {
		fs.StringVar(&space, "space", "", "space name or ID (default: the only space)")
		fs.StringVar(&out, "out", "", "the archive file (default: <space>.fettle.tar.gz)")
	})
	if err != nil {
		return err
	}
	defer a.DB.Close()
	rows, err := a.DB.Engine.Query(ctx, "SELECT id, name FROM spaces ORDER BY name")
	if err != nil {
		return err
	}
	var id uuid.UUID
	var name string
	n := 0
	for rows.Next() {
		var sid uuid.UUID
		var sname string
		if err := rows.Scan(&sid, &sname); err != nil {
			rows.Close()
			return err
		}
		if space == "" || space == sid.String() || strings.EqualFold(space, sname) {
			id, name = sid, sname
			n++
		}
	}
	rows.Close()
	switch {
	case n == 0:
		return fmt.Errorf("no space %q", space)
	case n > 1:
		return errors.New("this instance has several spaces: name one with --space")
	}
	if out == "" {
		out = strings.ReplaceAll(strings.ToLower(name), " ", "-") + ".fettle.tar.gz"
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	pass := os.Getenv("FETTLE_EXPORT_PASSPHRASE")
	if err := a.ExportSpace(ctx, id, f, pass); err != nil {
		_ = f.Close()
		_ = os.Remove(out)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if pass == "" {
		fmt.Printf("Wrote %s. The vault is not in it; set FETTLE_EXPORT_PASSPHRASE to include it.\n", out)
	} else {
		fmt.Printf("Wrote %s with the vault, sealed with the passphrase.\n", out)
	}
	return nil
}

func runImport(ctx context.Context, args []string) error {
	var owner string
	a, pos, err := openOffline(ctx, args, "import", func(fs *flag.FlagSet) {
		fs.StringVar(&owner, "owner", "", "email of the account that owns the space (server mode; default: the local owner)")
	})
	if err != nil {
		return err
	}
	defer a.DB.Close()
	if len(pos) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: fettle import <file> [--owner email]")
		return errUsage
	}
	file := pos[0]
	var ownerID uuid.UUID
	if owner == "" {
		u, err := a.LocalOwner(ctx)
		if err != nil {
			return err
		}
		ownerID = u.ID()
	} else if err := a.DB.Engine.QueryRow(ctx, "SELECT id FROM users WHERE lower(email) = lower($1)", owner).Scan(&ownerID); err != nil {
		return fmt.Errorf("no account %s", owner)
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	res, err := a.ImportSpace(ctx, f, ownerID, os.Getenv("FETTLE_EXPORT_PASSPHRASE"))
	if err != nil {
		return err
	}
	total := 0
	for _, n := range res.Rows {
		total += n
	}
	fmt.Printf("Imported space %s: %d rows, %d files, %d vault items.\n", res.SpaceID, total, res.Objects, res.Vault)
	return nil
}

func init() {
	register("backup", "Write a consistent copy of the SQLite database and the object store into a folder", runBackup)
}

func runBackup(ctx context.Context, args []string) error {
	a, pos, err := openOffline(ctx, args, "backup", nil)
	if err != nil {
		return err
	}
	defer a.DB.Close()
	if len(pos) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: fettle backup <dir>")
		return errUsage
	}
	n, err := a.Backup(ctx, pos[0])
	if err != nil {
		return err
	}
	if a.DB.Postgres {
		fmt.Printf("Copied %d objects to %s. The database is PostgreSQL: back it up with pg_dump.\n", n, pos[0])
	} else {
		fmt.Printf("Wrote %s with the database and %d objects.\n", pos[0], n)
	}
	switch a.MasterKeySource {
	case vault.SourceKeychain:
		fmt.Println("The master key is in the OS keychain. Keep a copy of it: the backup does not hold it.")
	case vault.SourceFile:
		fmt.Println("The master key is master.key in the data folder. Keep a copy of it: the backup does not hold it.")
	default:
		fmt.Println("The master key is FETTLE_MASTER_KEY. Keep a copy of it: the backup does not hold it.")
	}
	return nil
}
