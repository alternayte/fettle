// Package agentd is the daemon that runs inside each computer. It serves
// exec, files and workspace endpoints over HTTPS to the Fettle server and
// refuses every request without the computer token.
package agentd

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alternayte/fettle/agentd/client"
)

// NetworkState is what the boot step did to the network.
type NetworkState struct {
	Mode      string
	Applied   bool
	RulesHash string
}

// Server serves the agentd endpoints for one home folder.
type Server struct {
	cfg       Config
	home      string
	root      *os.Root
	logger    *slog.Logger
	started   time.Time
	network   NetworkState
	tokenHash [32]byte
	handler   http.Handler
	browsers  *browsers
}

// New returns a server for cfg. The home folder is created when missing.
func New(cfg Config, network NetworkState, logger *slog.Logger) (*Server, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	if err := os.MkdirAll(cfg.Home, 0o755); err != nil {
		return nil, fmt.Errorf("home folder: %w", err)
	}
	root, err := os.OpenRoot(cfg.Home)
	if err != nil {
		return nil, fmt.Errorf("home folder: %w", err)
	}
	if network.Mode == "" {
		network.Mode = cfg.Network.Mode
	}
	s := &Server{
		cfg:       cfg,
		home:      cfg.Home,
		root:      root,
		logger:    logger,
		started:   time.Now(),
		network:   network,
		tokenHash: sha256.Sum256([]byte(cfg.Token)),
		browsers:  newBrowsers(),
	}
	s.handler = s.routes()
	return s, nil
}

// Handler returns the HTTP handler with authentication.
func (s *Server) Handler() http.Handler { return s.handler }

// Close releases the home folder handle.
func (s *Server) Close() error {
	s.browsers.closeAll()
	return s.root.Close()
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.health)
	mux.HandleFunc("POST /v1/exec", s.exec)
	mux.HandleFunc("GET /v1/files", s.readFile)
	mux.HandleFunc("PUT /v1/files", s.writeFile)
	mux.HandleFunc("DELETE /v1/files", s.deleteFile)
	mux.HandleFunc("GET /v1/files/list", s.listFiles)
	mux.HandleFunc("GET /v1/files/stat", s.statFile)
	mux.HandleFunc("POST /v1/workspace/export", s.export)
	mux.HandleFunc("POST /v1/workspace/import", s.importWorkspace)
	mux.HandleFunc("POST /v1/workspace/manifest", s.manifest)
	mux.HandleFunc("POST /v1/workspace/diff", s.diff)
	mux.HandleFunc("POST /v1/browser/contexts", s.createBrowserContext)
	mux.HandleFunc("DELETE /v1/browser/contexts/{id}", s.deleteBrowserContext)
	mux.HandleFunc("POST /v1/browser/{ctx}/navigate", s.browserNavigate)
	mux.HandleFunc("POST /v1/browser/{ctx}/snapshot", s.browserSnapshot)
	mux.HandleFunc("POST /v1/browser/{ctx}/act", s.browserAct)
	mux.HandleFunc("POST /v1/browser/{ctx}/screenshot", s.browserScreenshot)
	mux.HandleFunc("POST /v1/browser/{ctx}/downloads", s.browserDownloads)
	// A websocket upgrade is a GET; Appendix B names this route POST /v1/mcp/stdio (D-135).
	mux.HandleFunc("GET /v1/mcp/stdio", s.mcpStdio)
	for _, p := range []string{"/v1/health", "/v1/exec", "/v1/files", "/v1/files/list", "/v1/files/stat",
		"/v1/workspace/export", "/v1/workspace/import", "/v1/workspace/manifest", "/v1/workspace/diff"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	return s.logRequests(s.authenticate(mux))
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || got == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		h := sha256.Sum256([]byte(got))
		if subtle.ConstantTimeCompare(h[:], s.tokenHash[:]) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	uid := os.Getuid()
	name := ""
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		name = u.Username
	}
	writeJSON(w, http.StatusOK, client.Health{
		Version:          s.cfg.Version,
		UptimeS:          int64(time.Since(s.started).Seconds()),
		User:             name,
		UID:              uid,
		NetworkRulesHash: s.network.RulesHash,
		NetworkMode:      s.network.Mode,
		NetworkApplied:   s.network.Applied,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, client.ErrorReply{Error: msg})
}

// errPathEscapes marks a path that leaves the home folder.
var errPathEscapes = errors.New("path escapes the home folder")

// httpError is an error with an HTTP status and a message for the caller.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func badRequest(format string, a ...any) error {
	return &httpError{status: http.StatusBadRequest, msg: fmt.Sprintf(format, a...)}
}

// fail writes err as a JSON error. rel names the path in the message.
func (s *Server) fail(w http.ResponseWriter, rel string, err error) {
	var he *httpError
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &he):
		writeError(w, he.status, he.msg)
	case errors.As(err, &mbe):
		writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
	case errors.Is(err, errPathEscapes), isRootEscape(err):
		writeError(w, http.StatusForbidden, rel+": path escapes the home folder")
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound, rel+": not found")
	case errors.Is(err, syscall.ENOTEMPTY):
		writeError(w, http.StatusConflict, rel+": folder is not empty")
	case errors.Is(err, syscall.ENOTDIR):
		writeError(w, http.StatusBadRequest, rel+": a part of the path is not a folder")
	case errors.Is(err, syscall.EISDIR):
		writeError(w, http.StatusBadRequest, rel+": path is a folder")
	case errors.Is(err, fs.ErrPermission):
		writeError(w, http.StatusForbidden, rel+": permission denied")
	default:
		s.logger.Error("request failed", "path", rel, "error", err.Error())
		writeError(w, http.StatusInternalServerError, rel+": "+cleanMessage(err))
	}
}

// isRootEscape reports an os.Root refusal for a path or a symlink that
// leaves the root. The os package does not export this error.
func isRootEscape(err error) bool {
	return err != nil && strings.Contains(err.Error(), "path escapes from parent")
}

// cleanMessage returns the innermost error text, without absolute paths.
func cleanMessage(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err.Error()
	}
	return err.Error()
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return err
		}
		return badRequest("bad request body: %s", err.Error())
	}
	return nil
}

// Run applies the boot step of the machine, then serves until ctx ends.
func Run(ctx context.Context, cfg Config, logger *slog.Logger) error {
	network, err := prepareMachine(ctx, cfg, logger)
	if err != nil {
		return err
	}
	s, err := New(cfg, network, logger)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if !cfg.Insecure {
		cert, err := tls.X509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("TLS key pair: %w", err)
		}
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}
		ln = tls.NewListener(ln, srv.TLSConfig)
	} else {
		logger.Warn("serving plain HTTP because FETTLE_AGENTD_INSECURE_LISTEN is 1")
	}
	logger.Info("agentd started", "addr", ln.Addr().String(), "version", cfg.Version,
		"uid", os.Getuid(), "network_mode", network.Mode, "network_applied", network.Applied)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
