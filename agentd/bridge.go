package agentd

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// BridgePath is the one path of the WebSocket bridge.
const BridgePath = "/v1/bridge"

// BridgeHandler serves the WebSocket bridge: a WebSocket upgrade on BridgePath becomes a raw
// byte stream to target, the agentd TLS listener. It is for providers that reach a guest
// port only through an HTTPS proxy that ends TLS (e2b). The stream carries agentd's own TLS,
// so the pinned certificate and the computer token protect every request; the bridge itself
// serves nothing else and holds no secret.
func BridgeHandler(target string, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != BridgePath {
			http.NotFound(w, r)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ws.SetReadLimit(-1)
		ctx := context.WithoutCancel(r.Context())
		nc := websocket.NetConn(ctx, ws, websocket.MessageBinary)
		up, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(r.Context(), "tcp", target)
		if err != nil {
			if logger != nil {
				logger.Warn("bridge cannot reach agentd", "error", err.Error())
			}
			_ = ws.Close(websocket.StatusInternalError, "agentd does not answer")
			return
		}
		var once sync.Once
		done := func() { once.Do(func() { _ = up.Close(); _ = nc.Close() }) }
		go func() { _, _ = io.Copy(up, nc); done() }()
		_, _ = io.Copy(nc, up)
		done()
	})
}

// serveBridge listens on addr without TLS and serves the bridge to target until ctx ends.
func serveBridge(ctx context.Context, addr, target string, logger *slog.Logger) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           BridgeHandler(target, logger),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	logger.Info("agentd bridge started", "addr", ln.Addr().String())
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Warn("agentd bridge stopped", "error", err.Error())
		}
	}()
	return nil
}
