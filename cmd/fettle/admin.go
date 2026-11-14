package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/audit"
)

func init() {
	register("admin create-user", "Create a server-mode account (--email, --name, --admin)", runCreateUser)
}

// runCreateUser creates an account in server mode. The password comes from
// FETTLE_PASSWORD, from stdin with --password-stdin, or is generated and printed once.
// An instance admin without a space gets a new space that they own.
func runCreateUser(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("admin create-user", flag.ContinueOnError)
	cfg := configFlags(fs)
	email := fs.String("email", "", "email address of the account")
	name := fs.String("name", "", "display name")
	admin := fs.Bool("admin", false, "make the account an instance admin and the owner of a new space")
	stdin := fs.Bool("password-stdin", false, "read the password from stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		fs.Usage()
		return errUsage
	}
	cfg.Mode = app.ModeServer
	pw := os.Getenv("FETTLE_PASSWORD")
	generated := false
	switch {
	case *stdin:
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return errors.New("no password on stdin")
		}
		pw = strings.TrimRight(line, "\r\n")
	case pw == "":
		b := make([]byte, 18)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		pw, generated = base64.RawURLEncoding.EncodeToString(b), true
	}
	if err := cfg.Normalize(); err != nil {
		return err
	}
	a, err := app.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer a.DB.Close()
	u, err := a.SignIn.CreateUser(ctx, *email, *name, pw, *admin)
	if err != nil {
		return fmt.Errorf("create the account: %w", err)
	}
	if *admin {
		ms, err := a.Spaces.ForUser(ctx, u.ID())
		if err != nil {
			return err
		}
		if len(ms) == 0 {
			if _, err := a.CreateSpace(ctx, audit.System, "My space", "", u.ID()); err != nil {
				return err
			}
		}
	}
	fmt.Printf("Created %s.\n", u.Email)
	if generated {
		fmt.Printf("Password: %s\nKeep it now: Fettle does not show it again.\n", pw)
	}
	return nil
}
