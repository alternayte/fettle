package kiln_test

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/alternayte/fettle/agentd"
	"github.com/alternayte/fettle/agentd/client"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/providers/kiln"
)

// fakeKiln serves one sandbox whose agentd port stream proxies to a TLS server, as a Kiln
// host carries a raw TCP connection over a WebSocket.
func fakeKiln(t *testing.T, agentdAddr string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer acme-kiln-token" {
			http.Error(w, `{"code":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/sandboxes/sb-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "sb-1", "state": "running"})
		case "/v1/sandboxes/sb-1/ports/7421/stream":
			ws, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			nc := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)
			up, err := net.Dial("tcp", agentdAddr)
			if err != nil {
				_ = nc.Close()
				return
			}
			go func() { _, _ = io.Copy(up, nc); _ = up.Close() }()
			_, _ = io.Copy(nc, up)
			_ = nc.Close()
		default:
			http.Error(w, `{"code":"not_found"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The agentd client pins the guest certificate through the stream: TLS runs end to end
// inside the WebSocket, so the Kiln host never sees the plain requests.
func TestKilnStreamCarriesPinnedTLS(t *testing.T) {
	certPEM, keyPEM, err := agentd.GenerateCert([]string{"fettle-computer"})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer acme-agentd-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "stream-test", "user": "fettle"})
		}))
	}()
	kh := fakeKiln(t, ln.Addr().String())

	p := kiln.New(kh.URL, "acme-kiln-token", "fettle-computer:dev", "test")
	m, err := p.Attach(t.Context(), "sb-1")
	if err != nil {
		t.Fatal(err)
	}
	url, err := m.AgentdURL(t.Context())
	if err != nil || !strings.HasPrefix(url, "https://") {
		t.Fatalf("agentd URL %q %v", url, err)
	}
	cl, err := client.New(url, "acme-agentd-token", certPEM, computers.DialOf(m))
	if err != nil {
		t.Fatal(err)
	}
	h, err := cl.Health(t.Context())
	if err != nil || h.Version != "stream-test" {
		t.Fatalf("health through the stream: %+v %v", h, err)
	}

	other, _, _ := agentd.GenerateCert([]string{"x"})
	bad, _ := client.New(url, "acme-agentd-token", other, computers.DialOf(m))
	if _, err := bad.Health(t.Context()); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("a client pinned to another certificate connected: %v", err)
	}

	wrong := kiln.New(kh.URL, "wrong", "fettle-computer:dev", "test")
	if _, err := wrong.Attach(t.Context(), "sb-1"); err == nil {
		t.Fatal("a wrong Kiln token attached")
	}
	if _, err := p.Attach(t.Context(), "sb-gone"); !errors.Is(err, computers.ErrGone) {
		t.Fatalf("a lost sandbox: %v", err)
	}
}
