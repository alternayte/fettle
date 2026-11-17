//go:build contract

package agentd_test

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/alternayte/fettle/agentd"
	"github.com/alternayte/fettle/agentd/client"
)

type testAgentd struct {
	url   string
	token string
	cert  []byte
	home  string
	c     *client.Client
}

func startAgentd(t *testing.T, token string) *testAgentd {
	t.Helper()
	return startAgentdWith(t, token, nil)
}

// startAgentdWith starts agentd behind wrap, which sees each request first.
func startAgentdWith(t *testing.T, token string, wrap func(http.Handler) http.Handler) *testAgentd {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := agentd.GenerateCert([]string{"computer.acme.test"})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := agentd.New(agentd.Config{
		Token:   token,
		TLSCert: cert,
		TLSKey:  key,
		Home:    home,
		User:    "fettle",
		Network: agentd.NetworkPolicy{Mode: agentd.NetworkOpen},
		Version: "test",
	}, agentd.NetworkState{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	if wrap != nil {
		h = wrap(h)
	}
	ts := httptest.NewUnstartedServer(h)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13}
	ts.StartTLS()
	t.Cleanup(func() {
		ts.Close()
		_ = srv.Close()
	})
	c, err := client.New(ts.URL, token, cert)
	if err != nil {
		t.Fatal(err)
	}
	return &testAgentd{url: ts.URL, token: token, cert: cert, home: home, c: c}
}

func wantStatus(t *testing.T, err error, status int) {
	t.Helper()
	var ce *client.Error
	if !errors.As(err, &ce) {
		t.Fatalf("want agentd error with status %d, got %v", status, err)
	}
	if ce.Status != status {
		t.Fatalf("want status %d, got %d: %s", status, ce.Status, ce.Message)
	}
}

type chunk struct{ stream, data string }

func runExec(t *testing.T, c *client.Client, req client.ExecRequest) (client.ExecResult, []chunk) {
	t.Helper()
	var chunks []chunk
	res, err := c.Exec(context.Background(), req, func(stream, data string) {
		chunks = append(chunks, chunk{stream, data})
	})
	if err != nil {
		t.Fatalf("exec %+v: %v", req, err)
	}
	return res, chunks
}

func joined(chunks []chunk, stream string) string {
	var b strings.Builder
	for _, ch := range chunks {
		if ch.stream == stream {
			b.WriteString(ch.data)
		}
	}
	return b.String()
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func tarNames(t *testing.T, r io.Reader) []string {
	t.Helper()
	zr, err := zstd.NewReader(r)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	var names []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
}

type tarEntry struct {
	hdr  tar.Header
	body string
}

func makeArchive(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		h := e.hdr
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestSCN_CMP_012_AgentdEndpointsTokenAndPinnedTLS covers every agentd
// endpoint over TLS with the pinned certificate and the computer token.
func TestSCN_CMP_012_AgentdEndpointsTokenAndPinnedTLS(t *testing.T) {
	ctx := context.Background()
	a := startAgentd(t, "token-acme-1")
	c := a.c

	t.Run("browser", func(t *testing.T) {
		site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/report.csv" {
				w.Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
				_, _ = w.Write([]byte("id,total\n1,40\n"))
				return
			}
			_, _ = w.Write([]byte(`<!doctype html><title>Acme sign-in</title><label>User <input id=u></label>
<label>Password <input id=p type=password></label><a href="/report.csv">Report</a>`))
		}))
		defer site.Close()
		if err := c.BrowserOpen(ctx, client.BrowserContextRequest{ID: "bot-1", Ephemeral: true}); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.BrowserClose(ctx, "bot-1") }()
		snap, err := c.BrowserNavigate(ctx, "bot-1", site.URL+"/login")
		if err != nil || snap.Title != "Acme sign-in" {
			t.Fatalf("navigate: %+v %v", snap, err)
		}
		ref := func(role, name string) string {
			for _, e := range snap.Elements {
				if e.Role == role && strings.Contains(e.Name, name) {
					return e.Ref
				}
			}
			t.Fatalf("no %s %q in %+v", role, name, snap.Elements)
			return ""
		}
		user, pass, link := ref("textbox", "User"), ref("textbox", "Password"), ref("link", "Report")
		if err := c.BrowserFill(ctx, "bot-1", client.FillRequest{Site: "https://evil.test", Fields: []client.FillField{{Ref: pass, Value: "x"}}}); err == nil {
			t.Fatal("fill on another site passed the origin check")
		}
		if err := c.BrowserFill(ctx, "bot-1", client.FillRequest{Site: site.URL, Fields: []client.FillField{{Ref: user, Value: "owner@acme.test"}, {Ref: pass, Value: "acme-secret-1"}}}); err != nil {
			t.Fatalf("fill: %v", err)
		}
		if snap, err = c.BrowserSnapshot(ctx, "bot-1"); err != nil {
			t.Fatal(err)
		}
		for _, e := range snap.Elements {
			if strings.Contains(e.Value, "acme-secret-1") {
				t.Fatal("the snapshot shows the password")
			}
		}
		if _, err := c.BrowserPeek(ctx, "bot-1"); err != nil {
			t.Fatalf("peek: %v", err)
		}
		res, err := c.BrowserAct(ctx, "bot-1", client.ActRequest{Ref: link, Action: "click"})
		if err != nil || res.Refused != "" {
			t.Fatalf("act: %+v %v", res, err)
		}
		png, _, err := c.BrowserScreenshot(ctx, "bot-1")
		if err != nil || !bytes.HasPrefix(png, []byte("\x89PNG")) {
			t.Fatalf("screenshot: %d bytes %v", len(png), err)
		}
		var names []string
		for i := 0; i < 50 && len(names) == 0; i++ {
			dl, err := c.BrowserDownloads(ctx, "bot-1")
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range dl {
				names = append(names, d.Name)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !slices.Contains(names, "report.csv") {
			t.Fatalf("downloads %v", names)
		}
	})

	t.Run("mcp stdio", func(t *testing.T) {
		conn, err := c.MCPStdio(ctx, client.MCPStdioRequest{Argv: []string{"cat"}})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		msg := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
		if _, err := io.WriteString(conn, msg); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, len(msg))
		if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != msg {
			t.Fatalf("echo %q %v", buf, err)
		}
	})

	t.Run("screen", func(t *testing.T) {
		_, errX := exec.LookPath("Xvfb")
		_, errV := exec.LookPath("x11vnc")
		sc, err := c.ScreenStart(ctx, "bot-1")
		if errX != nil || errV != nil {
			// A computer without an X server refuses screens (small and browser sizes).
			wantStatus(t, err, http.StatusNotImplemented)
			return
		}
		if err != nil || sc.Display < 10 {
			t.Fatalf("start: %+v %v", sc, err)
		}
		defer func() { _ = c.ScreenStop(ctx, "bot-1") }()
		for _, control := range []bool{false, true} {
			conn, err := c.ScreenDial(ctx, "bot-1", control)
			if err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 12)
			_, err = io.ReadFull(conn, buf)
			_ = conn.Close()
			if err != nil || !strings.HasPrefix(string(buf), "RFB 003.") {
				t.Fatalf("greeting %q %v", buf, err)
			}
		}
		_, err = c.ScreenStart(ctx, "../x")
		wantStatus(t, err, http.StatusBadRequest)
	})

	t.Run("health", func(t *testing.T) {
		h, err := c.Health(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != "test" || h.UID != os.Getuid() || h.NetworkMode != "open" || h.NetworkApplied || h.NetworkRulesHash != "" {
			t.Fatalf("health: %+v", h)
		}
	})

	t.Run("token", func(t *testing.T) {
		for _, tok := range []string{"", "token-acme-2", "token-acme-1x"} {
			bad, err := client.New(a.url, tok, a.cert)
			if err != nil {
				t.Fatal(err)
			}
			_, err = bad.Health(ctx)
			if !errors.Is(err, client.ErrUnauthorized) {
				t.Fatalf("token %q: want ErrUnauthorized, got %v", tok, err)
			}
			_, err = bad.ReadFile(ctx, "x")
			if !errors.Is(err, client.ErrUnauthorized) {
				t.Fatalf("token %q: files: want ErrUnauthorized, got %v", tok, err)
			}
		}
		// A replacement computer has a new token; the old token gets 401.
		b := startAgentd(t, "token-acme-replaced")
		old, err := client.New(b.url, a.token, b.cert)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := old.Health(ctx); !errors.Is(err, client.ErrUnauthorized) {
			t.Fatalf("old token: want ErrUnauthorized, got %v", err)
		}
		// The 401 body is JSON with a plain message.
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.url+"/v1/health", nil)
		hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // reads the raw 401 body only
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), `"error"`) {
			t.Fatalf("raw 401: %d %s", resp.StatusCode, body)
		}
	})

	t.Run("pinned certificate", func(t *testing.T) {
		other, _, err := agentd.GenerateCert([]string{"computer.acme.test"})
		if err != nil {
			t.Fatal(err)
		}
		wrong, err := client.New(a.url, a.token, other)
		if err != nil {
			t.Fatal(err)
		}
		_, err = wrong.Health(ctx)
		var ce *client.Error
		if err == nil || errors.As(err, &ce) || !strings.Contains(err.Error(), "pinned") {
			t.Fatalf("want pin failure, got %v", err)
		}
		if _, err := client.New(strings.Replace(a.url, "https", "http", 1), a.token, a.cert); err == nil {
			t.Fatal("want refusal of a plain http URL")
		}
	})

	t.Run("exec streams in order", func(t *testing.T) {
		res, chunks := runExec(t, c, client.ExecRequest{Shell: "printf a; sleep 0.3; printf b >&2; sleep 0.3; printf c"})
		want := []chunk{{"stdout", "a"}, {"stderr", "b"}, {"stdout", "c"}}
		if !slices.Equal(chunks, want) {
			t.Fatalf("chunks: %v", chunks)
		}
		if res.ExitCode != 0 || res.TimedOut || res.Truncated || res.OutputFile != "" {
			t.Fatalf("result: %+v", res)
		}
	})

	t.Run("exec exit codes", func(t *testing.T) {
		if res, _ := runExec(t, c, client.ExecRequest{Shell: "exit 7"}); res.ExitCode != 7 {
			t.Fatalf("shell exit: %+v", res)
		}
		if res, _ := runExec(t, c, client.ExecRequest{Argv: []string{"sh", "-c", "exit 3"}}); res.ExitCode != 3 {
			t.Fatalf("argv exit: %+v", res)
		}
		res, chunks := runExec(t, c, client.ExecRequest{Argv: []string{"fettle-no-such-command"}})
		if res.ExitCode != 127 || joined(chunks, "stderr") == "" {
			t.Fatalf("missing command: %+v %v", res, chunks)
		}
		_, err := c.Exec(ctx, client.ExecRequest{Argv: []string{"true"}, Shell: "true"}, nil)
		wantStatus(t, err, http.StatusBadRequest)
		_, err = c.Exec(ctx, client.ExecRequest{}, nil)
		wantStatus(t, err, http.StatusBadRequest)
	})

	t.Run("exec cwd and env", func(t *testing.T) {
		t.Setenv("FETTLE_AGENTD_TOKEN", "must-not-leak")
		if err := c.WriteFile(ctx, "bots/b1/marker", []byte("m"), 0); err != nil {
			t.Fatal(err)
		}
		res, chunks := runExec(t, c, client.ExecRequest{
			Shell: `pwd; echo "$GREETING"; echo "token=$FETTLE_AGENTD_TOKEN"; echo "home=$HOME"; ls`,
			Cwd:   "bots/b1",
			Env:   map[string]string{"GREETING": "hello acme"},
		})
		want := filepath.Join(a.home, "bots", "b1") + "\nhello acme\ntoken=\nhome=" + a.home + "\nmarker\n"
		if got := joined(chunks, "stdout"); res.ExitCode != 0 || got != want {
			t.Fatalf("stdout %q, want %q (%+v)", got, want, res)
		}
		_, err := c.Exec(ctx, client.ExecRequest{Shell: "pwd", Cwd: "../"}, nil)
		wantStatus(t, err, http.StatusForbidden)
		_, err = c.Exec(ctx, client.ExecRequest{Shell: "pwd", Cwd: "bots/none"}, nil)
		wantStatus(t, err, http.StatusNotFound)
	})

	t.Run("exec timeout kills the process group", func(t *testing.T) {
		start := time.Now()
		res, chunks := runExec(t, c, client.ExecRequest{Shell: "sleep 30 & echo $!; wait", TimeoutMS: 400})
		if !res.TimedOut || res.ExitCode != 137 || time.Since(start) > 10*time.Second {
			t.Fatalf("result: %+v after %s", res, time.Since(start))
		}
		pid, err := strconv.Atoi(strings.TrimSpace(joined(chunks, "stdout")))
		if err != nil {
			t.Fatalf("child pid: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for syscall.Kill(pid, 0) == nil {
			if time.Now().After(deadline) {
				t.Fatalf("child %d still runs after the timeout", pid)
			}
			time.Sleep(50 * time.Millisecond)
		}
	})

	t.Run("exec output cap and output file", func(t *testing.T) {
		const total = 1572864
		res, chunks := runExec(t, c, client.ExecRequest{Shell: "head -c 1572864 /dev/zero | tr '\\0' a"})
		streamed := len(joined(chunks, "stdout"))
		if streamed != 1<<20 || !res.Truncated || !strings.HasPrefix(res.OutputFile, ".fettle/exec/") || !strings.HasSuffix(res.OutputFile, ".log") {
			t.Fatalf("streamed %d, result %+v", streamed, res)
		}
		full, err := c.ReadFile(ctx, res.OutputFile)
		if err != nil {
			t.Fatal(err)
		}
		if len(full) != total || strings.Trim(string(full), "a") != "" {
			t.Fatalf("output file has %d bytes", len(full))
		}
	})

	t.Run("files", func(t *testing.T) {
		if err := c.WriteFile(ctx, "notes/plan.txt", []byte("v1"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := c.WriteFile(ctx, "notes/plan.txt", []byte("version 2"), 0); err != nil {
			t.Fatal(err)
		}
		got, err := c.ReadFile(ctx, "notes/plan.txt")
		if err != nil || string(got) != "version 2" {
			t.Fatalf("read: %q %v", got, err)
		}
		e, err := c.Stat(ctx, "notes/plan.txt")
		if err != nil || e.Path != "notes/plan.txt" || e.Size != 9 || e.Mode != 0o600 || e.IsDir || e.ModTime.IsZero() {
			t.Fatalf("stat: %+v %v", e, err)
		}
		if err := c.WriteFile(ctx, "notes/deep/x.sh", []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		top, err := c.List(ctx, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(top, func(e client.Entry) bool { return e.Path == "notes" && e.IsDir }) ||
			slices.ContainsFunc(top, func(e client.Entry) bool { return e.Path == "notes/plan.txt" }) {
			t.Fatalf("list top: %+v", top)
		}
		all, err := c.List(ctx, "notes", true)
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		for _, e := range all {
			paths = append(paths, e.Path)
		}
		if !slices.Equal(paths, []string{"notes/deep", "notes/deep/x.sh", "notes/plan.txt"}) {
			t.Fatalf("list recursive: %v", paths)
		}
		if _, err := c.ReadFile(ctx, "notes/none.txt"); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("read missing: %v", err)
		}
		if _, err := c.Stat(ctx, "notes/none.txt"); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("stat missing: %v", err)
		}
		if _, err := c.List(ctx, "notes/plan.txt", false); err == nil {
			t.Fatal("list of a file: want error")
		}
		wantStatus(t, c.DeleteFile(ctx, "notes", false), http.StatusConflict)
		if err := c.DeleteFile(ctx, "notes/plan.txt", false); err != nil {
			t.Fatal(err)
		}
		if err := c.DeleteFile(ctx, "notes", true); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Stat(ctx, "notes"); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("stat after delete: %v", err)
		}
		if err := c.DeleteFile(ctx, "notes", true); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("delete missing: %v", err)
		}
		wantStatus(t, c.DeleteFile(ctx, "", true), http.StatusBadRequest)
		wantStatus(t, c.DeleteFile(ctx, ".", true), http.StatusBadRequest)
	})

	t.Run("path escape refusal", func(t *testing.T) {
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(a.home, "out")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../..", filepath.Join(a.home, "up")); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{"../secret", "a/../../secret", "/etc/passwd", "out/secret", "up/secret"} {
			_, err := c.ReadFile(ctx, p)
			wantStatus(t, err, http.StatusForbidden)
			_, err = c.Stat(ctx, p)
			wantStatus(t, err, http.StatusForbidden)
		}
		wantStatus(t, c.WriteFile(ctx, "out/new", []byte("x"), 0), http.StatusForbidden)
		wantStatus(t, c.WriteFile(ctx, "../new", []byte("x"), 0), http.StatusForbidden)
		if _, err := os.Stat(filepath.Join(outside, "new")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a write went outside the home folder: %v", err)
		}
		_, err := c.List(ctx, "out", false)
		wantStatus(t, err, http.StatusForbidden)
		wantStatus(t, c.DeleteFile(ctx, "out/secret", false), http.StatusForbidden)
		// Deleting the link removes the link, not its target.
		if err := c.DeleteFile(ctx, "out", false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
			t.Fatalf("target was touched: %v", err)
		}
		if err := c.DeleteFile(ctx, "up", false); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("export and import round trip", func(t *testing.T) {
		ws := filepath.Join(a.home, "bots", "b2")
		writeTree(t, ws, map[string]string{
			"notes.md":                                       "notes",
			"sub/data.txt":                                   "data v1",
			"node_modules/pkg/index.js":                      "js",
			"app.log":                                        "log",
			".cache/x":                                       "c",
			".venv/bin/python":                               "py",
			"src/__pycache__/m.pyc":                          "pyc",
			".browser/Default/Preferences":                   "{}",
			".browser/Default/Cache/data_0":                  "cache",
			".browser/Default/Code Cache/js/a":               "cc",
			".browser/Default/GPUCache/index":                "gpu",
			".browser/Default/Service Worker/CacheStorage/k": "sw",
			".browser/Default/Service Worker/ScriptCache/k":  "keep",
		})
		rc, err := c.Export(ctx, "bots/b2", nil)
		if err != nil {
			t.Fatal(err)
		}
		archive, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		names := tarNames(t, bytes.NewReader(archive))
		for _, want := range []string{"notes.md", "sub/", "sub/data.txt", ".browser/Default/Preferences", ".browser/Default/Service Worker/ScriptCache/k"} {
			if !slices.Contains(names, want) {
				t.Fatalf("export lacks %q: %v", want, names)
			}
		}
		for _, n := range names {
			for _, bad := range []string{"node_modules", ".log", ".cache", ".venv", "__pycache__", "/Cache", "Code Cache", "GPUCache", "CacheStorage"} {
				if strings.Contains(n, bad) {
					t.Fatalf("export holds excluded %q: %v", n, names)
				}
			}
		}
		rc, err = c.Export(ctx, "bots/b2", []string{"sub"})
		if err != nil {
			t.Fatal(err)
		}
		if names := tarNames(t, rc); slices.Contains(names, "sub/data.txt") || !slices.Contains(names, "notes.md") {
			t.Fatalf("exclude sub: %v", names)
		}
		rc.Close()
		if _, err := c.Export(ctx, "bots/none", nil); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("export missing: %v", err)
		}

		if err := c.DeleteFile(ctx, "bots/b2/notes.md", false); err != nil {
			t.Fatal(err)
		}
		if err := c.WriteFile(ctx, "bots/b2/sub/data.txt", []byte("data v2"), 0); err != nil {
			t.Fatal(err)
		}
		if err := c.WriteFile(ctx, "bots/b2/extra.txt", []byte("extra"), 0); err != nil {
			t.Fatal(err)
		}
		if err := c.Import(ctx, "bots/b2", bytes.NewReader(archive)); err != nil {
			t.Fatal(err)
		}
		if got, err := c.ReadFile(ctx, "bots/b2/notes.md"); err != nil || string(got) != "notes" {
			t.Fatalf("restored notes: %q %v", got, err)
		}
		if got, err := c.ReadFile(ctx, "bots/b2/sub/data.txt"); err != nil || string(got) != "data v1" {
			t.Fatalf("restored data: %q %v", got, err)
		}
		if _, err := c.Stat(ctx, "bots/b2/extra.txt"); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("extra file survived the import: %v", err)
		}
		if _, err := c.Stat(ctx, "bots/b2/node_modules"); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("node_modules came back: %v", err)
		}
		if err := c.Import(ctx, "bots/b3", bytes.NewReader(archive)); err != nil {
			t.Fatalf("import into a new folder: %v", err)
		}
		if got, err := c.ReadFile(ctx, "bots/b3/notes.md"); err != nil || string(got) != "notes" {
			t.Fatalf("new folder notes: %q %v", got, err)
		}

		escapes := [][]tarEntry{
			{{hdr: tar.Header{Name: "../evil", Typeflag: tar.TypeReg}, body: "x"}},
			{{hdr: tar.Header{Name: "/evil", Typeflag: tar.TypeReg}, body: "x"}},
			{
				{hdr: tar.Header{Name: "l", Typeflag: tar.TypeSymlink, Linkname: "../../.."}},
				{hdr: tar.Header{Name: "l/evil", Typeflag: tar.TypeReg}, body: "x"},
			},
		}
		for i, entries := range escapes {
			err := c.Import(ctx, "bots/b2", bytes.NewReader(makeArchive(t, entries)))
			wantStatus(t, err, http.StatusForbidden)
			if got, err := c.ReadFile(ctx, "bots/b2/notes.md"); err != nil || string(got) != "notes" {
				t.Fatalf("archive %d changed the folder: %q %v", i, got, err)
			}
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(a.home), "evil")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("an archive entry left the home folder: %v", err)
		}
		left, err := c.List(ctx, "bots", false)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range left {
			if strings.Contains(e.Path, ".fettle-") {
				t.Fatalf("import left a temporary folder: %s", e.Path)
			}
		}
		wantStatus(t, c.Import(ctx, "bots/b2", strings.NewReader("not zstd")), http.StatusBadRequest)
	})

	t.Run("manifest and diff", func(t *testing.T) {
		writeTree(t, filepath.Join(a.home, "shared"), map[string]string{
			"keep.txt":        "same",
			"edit.txt":        "before",
			"gone.txt":        "bye",
			"node_modules/xx": "skip",
		})
		base, err := c.Manifest(ctx, "shared")
		if err != nil {
			t.Fatal(err)
		}
		keys := make([]string, 0, len(base))
		for k := range base {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, []string{"edit.txt", "gone.txt", "keep.txt"}) || len(base["keep.txt"]) != 64 {
			t.Fatalf("manifest: %v", base)
		}
		if err := c.WriteFile(ctx, "shared/edit.txt", []byte("after"), 0); err != nil {
			t.Fatal(err)
		}
		if err := c.WriteFile(ctx, "shared/dir/new.txt", []byte("new"), 0); err != nil {
			t.Fatal(err)
		}
		if err := c.DeleteFile(ctx, "shared/gone.txt", false); err != nil {
			t.Fatal(err)
		}
		d, err := c.Diff(ctx, "shared", base)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Added) != 1 || d.Added[0].Path != "dir/new.txt" || string(d.Added[0].Content) != "new" {
			t.Fatalf("added: %+v", d.Added)
		}
		if len(d.Changed) != 1 || d.Changed[0].Path != "edit.txt" || string(d.Changed[0].Content) != "after" {
			t.Fatalf("changed: %+v", d.Changed)
		}
		if !slices.Equal(d.Deleted, []string{"gone.txt"}) {
			t.Fatalf("deleted: %v", d.Deleted)
		}
		if _, err := c.Manifest(ctx, "../"); err == nil {
			t.Fatal("manifest outside home: want error")
		}
	})
}

func TestSCN_CON_007_StdioVariablesReachTheProcessNotTheURL(t *testing.T) {
	var mu sync.Mutex
	var urls []string
	a := startAgentdWith(t, "token-acme-stdio", func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			urls = append(urls, r.URL.String())
			mu.Unlock()
			h.ServeHTTP(w, r)
		})
	})
	ctx := context.Background()
	const token = "acme-jira-token-5521"
	conn, err := a.c.MCPStdio(ctx, client.MCPStdioRequest{
		Argv: []string{"sh", "-c", `printf '%s|%s\n' "$JIRA_URL" "$JIRA_PERSONAL_TOKEN"; cat`},
		Env:  map[string]string{"JIRA_URL": "https://jira.acme.test", "JIRA_PERSONAL_TOKEN": token},
	})
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.Close()
	if err != nil || line != "https://jira.acme.test|"+token+"\n" {
		t.Fatalf("the process saw %q %v", line, err)
	}
	mu.Lock()
	seen := strings.Join(urls, "\n")
	mu.Unlock()
	if !strings.Contains(seen, "/v1/mcp/stdio") || strings.Contains(seen, token) || strings.Contains(seen, "jira.acme.test") || strings.Contains(seen, "printf") {
		t.Fatalf("a variable or the command is in the request URL: %s", seen)
	}

	// A program that does not start fails with a message, not a broken stream.
	_, err = a.c.MCPStdio(ctx, client.MCPStdioRequest{Argv: []string{"acme-no-such-program"}})
	if err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("missing program: %v", err)
	}
}
