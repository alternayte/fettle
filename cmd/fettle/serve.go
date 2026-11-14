package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/vault"
)

func configFlags(fs *flag.FlagSet) *app.Config {
	cfg := app.ConfigFromEnv()
	cfg.Version = version
	fs.StringVar(&cfg.Data, "data", cfg.Data, "data folder")
	fs.StringVar(&cfg.DB, "db", cfg.DB, "sqlite or a postgres:// URL")
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address")
	mode := string(cfg.Mode)
	fs.Var(modeValue{cfg: &cfg, s: &mode}, "mode", "local or server")
	if n, err := strconv.Atoi(os.Getenv("FETTLE_RESCUE_SECONDS")); err == nil {
		cfg.RescueAfterSeconds = n
	}
	return &cfg
}

type modeValue struct {
	cfg *app.Config
	s   *string
}

func (m modeValue) String() string {
	if m.s == nil {
		return ""
	}
	return *m.s
}

func (m modeValue) Set(v string) error {
	if v != "local" && v != "server" {
		return fmt.Errorf("mode must be local or server")
	}
	*m.s = v
	m.cfg.Mode = app.Mode(v)
	return nil
}

func init() {
	register("serve", "Run the server in the foreground", runServe)
	register("up", "Start a local server in the background and print its URL", runUp)
	register("down", "Stop the background local server", runDown)
	register("migrate", "Apply database migrations and exit", runMigrate)
	register("version", "Print the version", func(context.Context, []string) error {
		fmt.Println("fettle", version)
		return nil
	})
	register("vault rekey", "Re-wrap all data keys with a new master key", runRekey)
}

func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfg := configFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := app.New(ctx, cfg)
	if err != nil {
		return err
	}
	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := a.Start(sigCtx, a.Handler()); err != nil {
		_ = a.Shutdown(context.Background())
		return err
	}
	if cfg.Mode == app.ModeLocal {
		_ = os.WriteFile(filepath.Join(cfg.Data, "fettle.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		_ = os.WriteFile(filepath.Join(cfg.Data, "fettle.addr"), []byte(a.Addr()), 0o600)
	}
	a.Log.Info("fettle is ready", "url", a.URL(), "mode", cfg.Mode, "data", cfg.Data)
	<-sigCtx.Done()
	a.Log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = a.Shutdown(sctx)
	if cfg.Mode == app.ModeLocal {
		_ = os.Remove(filepath.Join(cfg.Data, "fettle.pid"))
		_ = os.Remove(filepath.Join(cfg.Data, "fettle.addr"))
	}
	return err
}

func runMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	cfg := configFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cfg.Normalize(); err != nil {
		return err
	}
	d, err := app.OpenDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer d.Close()
	n, err := d.Migrate(ctx, cfg)
	if err != nil {
		return err
	}
	fmt.Printf("Applied %d migrations.\n", n)
	return nil
}

func readFile(p string) string {
	b, _ := os.ReadFile(p)
	return strings.TrimSpace(string(b))
}

func runUp(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	cfg := configFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cfg.Normalize(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Data, 0o700); err != nil {
		return err
	}
	if pid := readFile(filepath.Join(cfg.Data, "fettle.pid")); pid != "" {
		if n, _ := strconv.Atoi(pid); n > 0 && alive(n) {
			return printURL(cfg)
		}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logDir := filepath.Join(cfg.Data, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(logDir, "fettle.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "serve", "--data", cfg.Data, "--db", cfg.DB, "--addr", cfg.Addr, "--mode", string(cfg.Mode))
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = detach()
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if addr := readFile(filepath.Join(cfg.Data, "fettle.addr")); addr != "" {
			if resp, err := http.Get("http://" + addr + "/healthz"); err == nil {
				resp.Body.Close()
				return printURL(cfg)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("the server did not start within 60 s; read %s", filepath.Join(logDir, "fettle.log"))
}

func printURL(cfg *app.Config) error {
	addr := readFile(filepath.Join(cfg.Data, "fettle.addr"))
	tok := readFile(filepath.Join(cfg.Data, "local.token"))
	if addr == "" || tok == "" {
		return errors.New("the server runs, but its address or token file is missing")
	}
	fmt.Printf("Fettle runs at http://%s/#token=%s\n", addr, tok)
	return nil
}

func runDown(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	cfg := configFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cfg.Normalize(); err != nil {
		return err
	}
	pid, _ := strconv.Atoi(readFile(filepath.Join(cfg.Data, "fettle.pid")))
	if pid <= 0 || !alive(pid) {
		fmt.Println("Fettle is not running.")
		return nil
	}
	if err := terminate(pid); err != nil {
		return err
	}
	for i := 0; i < 350; i++ {
		if !alive(pid) {
			fmt.Println("Fettle stopped.")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("process %d did not stop within 35 s", pid)
}

func runRekey(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("vault rekey", flag.ContinueOnError)
	cfg := configFlags(fs)
	newKey := fs.String("new-key", "", "the new master key (base64, 32 bytes) or file:<path>; empty makes a new key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := app.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer a.DB.Close()
	var k []byte
	if *newKey == "" {
		k = vault.NewKey()
	} else if k, err = vault.ParseMasterKey(*newKey); err != nil {
		return err
	}
	n, err := a.Vault.Rekey(ctx, k)
	if err != nil {
		return err
	}
	if err := a.StoreMasterKey(k); err != nil {
		return err
	}
	fmt.Printf("Re-wrapped %d data keys.\n", n)
	if a.MasterKeySource == vault.SourceEnv {
		fmt.Println("Set FETTLE_MASTER_KEY to the new key before the next start:")
		fmt.Println(base64.StdEncoding.EncodeToString(k))
	}
	return nil
}
