//go:build linux || darwin

package agentd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/alternayte/fettle/agentd/client"
)

// The teach terminal records each command with its folder, exit code and output, and
// keeps no input that the person typed while echo was off (SCN-TCH-002, the terminal part).
func TestTeachTerminalRecordsCommandsButNotHiddenInput(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "bots", "b1", "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{Token: "teach-token", Insecure: true, Home: home, User: "fettle", Network: NetworkPolicy{Mode: NetworkOpen}, Version: "test"},
		NetworkState{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); _ = srv.Close() })
	c := client.NewInsecureForTest(ts.URL, "teach-token")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if created, err := c.TeachStart(ctx, "t1", client.TeachStart{Cwd: "bots/b1"}); err != nil || !created {
		t.Fatal(created, err)
	}
	if created, err := c.TeachStart(ctx, "t1", client.TeachStart{Cwd: "bots/b1"}); err != nil || created {
		t.Fatal("a second start made a new recorder", err)
	}
	ws, err := c.TeachPTY(ctx, "t1", 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.CloseNow() }()
	var screen strings.Builder
	go func() {
		for {
			_, b, err := ws.Read(ctx)
			if err != nil {
				return
			}
			screen.Write(b)
		}
	}()
	const secret = "hunter2-acme-secret"
	send := func(s string) {
		t.Helper()
		if err := ws.Write(ctx, websocket.MessageBinary, []byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	resize, _ := json.Marshal(map[string]int{"cols": 120, "rows": 40})
	if err := ws.Write(ctx, websocket.MessageText, resize); err != nil {
		t.Fatal(err)
	}
	var steps []client.TeachStep
	waitSteps := func(n int) {
		t.Helper()
		for len(steps) < n {
			if ctx.Err() != nil {
				t.Fatalf("only %d of %d steps: %+v", len(steps), n, steps)
			}
			more, err := c.TeachSteps(ctx, "t1", 0)
			if err != nil {
				t.Fatal(err)
			}
			steps = more
			time.Sleep(100 * time.Millisecond)
		}
	}
	time.Sleep(500 * time.Millisecond)
	send("echo hello-acme\r")
	waitSteps(1)
	send("read -rs -p 'Password: ' pw; echo \"got ${#pw}\"\r")
	time.Sleep(700 * time.Millisecond)
	send(secret + "\r")
	waitSteps(2)
	send("cd out && false\r")
	waitSteps(3)
	send("pwd\r")
	waitSteps(4)

	if s := steps[0]; s.Kind != "command" || s.Command != "echo hello-acme" || s.Cwd != "bots/b1" || s.ExitCode != 0 || !strings.Contains(s.Output, "hello-acme") || s.Secret != "" {
		t.Fatalf("echo step %+v", s)
	}
	if s := steps[1]; s.Secret != client.SecretEchoOff || !strings.Contains(s.Output, "got 19") || !strings.HasPrefix(s.Command, "read -rs") {
		t.Fatalf("hidden input step %+v", s)
	}
	if s := steps[2]; s.Command != "cd out && false" || s.ExitCode != 1 || s.Cwd != "bots/b1" {
		t.Fatalf("false step %+v", s)
	}
	if s := steps[3]; s.Cwd != "bots/b1/out" || !strings.Contains(s.Output, filepath.Join(home, "bots", "b1", "out")) {
		t.Fatalf("pwd step %+v", s)
	}
	for _, s := range steps {
		b, _ := json.Marshal(s)
		if strings.Contains(string(b), secret) {
			t.Fatalf("a step holds the hidden input: %s", b)
		}
	}
	// Taking steps forgets them; stop returns the rest and ends the terminal.
	if more, err := c.TeachSteps(ctx, "t1", steps[3].Seq); err != nil || len(more) != 0 {
		t.Fatalf("after the last seq: %v %+v", err, more)
	}
	if _, err := c.TeachStop(ctx, "t1", steps[3].Seq); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.Read(ctx); err == nil {
		t.Fatal("the terminal still runs after stop")
	}
}
