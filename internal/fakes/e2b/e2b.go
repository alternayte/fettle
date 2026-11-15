// Package e2b is the fake E2B API of the tests (§24.4): template builds, sandbox create,
// pause, connect (resume), fork, network, kill and metrics by the E2B OpenAPI document,
// envd process start, and port URLs that end TLS and proxy to a guest port, as the E2B
// proxy does. Machines run the sandboxes: Docker runs the computer image with agentd, and a
// test can give its own.
package e2b

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alternayte/fettle/agentd"
)

// Domain is the sandbox domain of the fake. Port URLs are https://<port>-<id>.<Domain>.
const Domain = "e2b.acme.test"

// EnvdPort is the envd port of a sandbox.
const EnvdPort = 49983

// Machines runs the sandboxes of the fake.
type Machines interface {
	// Image fails when the image of a template build is missing.
	Image(ctx context.Context, ref string) error
	Create(ctx context.Context, id, image string, cpu, memMiB int) error
	// Start runs a background process as root, as envd does.
	Start(ctx context.Context, id string, cmd []string, env map[string]string) error
	Pause(ctx context.Context, id string) error
	Unpause(ctx context.Context, id string) error
	// Fork copies the disk of from into a new machine to.
	Fork(ctx context.Context, from, to string) error
	Remove(ctx context.Context, id string) error
	// Addr returns the host:port of a guest port.
	Addr(ctx context.Context, id string, port int) (string, error)
}

// Server is the fake E2B API and proxy.
type Server struct {
	// URL is the API base URL, https on a loopback address.
	URL string
	// CAPEM is the certificate of the API and of every port URL.
	CAPEM []byte

	key      string
	machines Machines
	srv      *httptest.Server
	pool     *x509.CertPool

	mu        sync.Mutex
	templates map[string]*template // by ID
	sandboxes map[string]*sandbox
	builds    map[string]int // template name -> builds started
	ports     map[int]int    // guest port -> proxied requests
	calls     []string
}

type template struct {
	id, name    string
	cpu, mem    int
	status      string // "", waiting, building, ready, error
	reason      string
	image       string
	lastBuildID string
	buildPolls  int
}

type sandbox struct {
	id, templateID   string
	state            string // running, paused
	envdToken        string
	trafficToken     string
	public           bool
	allowOut         []string
	denyOut          []string
	timeout          int
	autoPause        bool
	metadata         map[string]string
	cmd              []string
	env              map[string]string
	cpu, mem         int
	networkUpdates   int
	timeoutRefreshes int
}

// SandboxInfo is what the fake knows of a sandbox.
type SandboxInfo struct {
	State            string
	Public           bool
	TrafficToken     string
	AllowOut         []string
	DenyOut          []string
	Timeout          int
	AutoPause        bool
	Metadata         map[string]string
	Cmd              []string
	Env              map[string]string
	TimeoutRefreshes int
}

// New starts a fake E2B API that accepts one API key.
func New(apiKey string, machines Machines) (*Server, error) {
	certPEM, keyPEM, err := agentd.GenerateCert([]string{"*." + Domain, "api." + Domain})
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	s := &Server{CAPEM: certPEM, key: apiKey, machines: machines, pool: pool,
		templates: map[string]*template{}, sandboxes: map[string]*sandbox{}, builds: map[string]int{}, ports: map[int]int{}}
	s.srv = httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	s.srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	s.srv.StartTLS()
	s.URL = s.srv.URL
	return s, nil
}

// Close removes every sandbox and stops the server.
func (s *Server) Close() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.sandboxes))
	for id := range s.sandboxes {
		ids = append(ids, id)
	}
	s.sandboxes = map[string]*sandbox{}
	s.mu.Unlock()
	for _, id := range ids {
		_ = s.machines.Remove(context.Background(), id)
	}
	s.srv.Close()
}

// Client returns an HTTP client that trusts the fake and sends every host of Domain to it.
func (s *Server) Client() *http.Client {
	addr := s.srv.Listener.Addr().String()
	d := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: s.pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, a string) (net.Conn, error) {
			host, _, _ := net.SplitHostPort(a)
			if strings.HasSuffix(host, "."+Domain) {
				a = addr
			}
			return d.DialContext(ctx, network, a)
		},
	}}
}

// Builds returns how many builds of a template name started.
func (s *Server) Builds(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.builds[name]
}

// BuildImage returns the image of the last build of a template name.
func (s *Server) BuildImage(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.templates {
		if t.name == name {
			return t.image
		}
	}
	return ""
}

// PortHits returns how many requests the proxy sent to a guest port.
func (s *Server) PortHits(port int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ports[port]
}

// Calls returns the API calls as "METHOD path", in order.
func (s *Server) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.calls...)
}

// Sandbox returns what the fake knows of a sandbox; ok is false when it does not exist.
func (s *Server) Sandbox(id string) (SandboxInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sb, ok := s.sandboxes[id]
	if !ok {
		return SandboxInfo{}, false
	}
	return SandboxInfo{State: sb.state, Public: sb.public, TrafficToken: sb.trafficToken, AllowOut: sb.allowOut, DenyOut: sb.denyOut, Timeout: sb.timeout,
		AutoPause: sb.autoPause, Metadata: sb.metadata, Cmd: sb.cmd, Env: sb.env, TimeoutRefreshes: sb.timeoutRefreshes}, true
}

// Sandboxes returns the IDs of the sandboxes that exist.
func (s *Server) Sandboxes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.sandboxes))
	for id := range s.sandboxes {
		out = append(out, id)
	}
	return out
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"code": status, "message": msg})
}

var portHost = regexp.MustCompile(`^(\d+)-([a-z0-9]+)\.` + regexp.QuoteMeta(Domain) + `$`)

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if m := portHost.FindStringSubmatch(host); m != nil {
		port, _ := strconv.Atoi(m[1])
		s.port(w, r, port, m[2])
		return
	}
	if r.Header.Get("X-API-Key") != s.key {
		fail(w, http.StatusUnauthorized, "Invalid API key")
		return
	}
	s.mu.Lock()
	s.calls = append(s.calls, r.Method+" "+r.URL.Path)
	s.mu.Unlock()
	s.api(w, r)
}

var (
	reAlias       = regexp.MustCompile(`^/templates/aliases/([^/]+)$`)
	reTemplate    = regexp.MustCompile(`^/templates/([^/]+)$`)
	reBuildStart  = regexp.MustCompile(`^/v2/templates/([^/]+)/builds/([^/]+)$`)
	reBuildStatus = regexp.MustCompile(`^/templates/([^/]+)/builds/([^/]+)/status$`)
	reSandbox     = regexp.MustCompile(`^/sandboxes/([^/]+)$`)
	reSandboxOp   = regexp.MustCompile(`^/sandboxes/([^/]+)/(pause|fork|timeout|network|metrics)$`)
	reConnect     = regexp.MustCompile(`^/v2/sandboxes/([^/]+)/connect$`)
)

func decode(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	b, err := io.ReadAll(r.Body)
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return err
	}
	return json.Unmarshal(b, v)
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := r.URL.Path
	switch {
	case r.Method == http.MethodGet && p == "/v2/templates":
		s.mu.Lock()
		out := []map[string]any{}
		for _, t := range s.templates {
			out = append(out, s.templateJSON(t))
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, out)
	case r.Method == http.MethodPost && p == "/v3/templates":
		var in struct {
			Name     string `json:"name"`
			CPUCount int    `json:"cpuCount"`
			MemoryMB int    `json:"memoryMB"`
		}
		if err := decode(r, &in); err != nil || in.Name == "" {
			fail(w, http.StatusBadRequest, "name is required")
			return
		}
		s.mu.Lock()
		var t *template
		for _, x := range s.templates {
			if x.name == in.Name {
				t = x
			}
		}
		if t == nil {
			t = &template{id: randHex(10), name: in.Name}
			s.templates[t.id] = t
		}
		t.cpu, t.mem, t.status, t.reason, t.lastBuildID, t.buildPolls = max(in.CPUCount, 2), max(in.MemoryMB, 512), "waiting", "", randHex(16), 0
		res := map[string]any{"templateID": t.id, "buildID": t.lastBuildID, "public": false, "names": []string{t.name}, "tags": []string{}, "aliases": []string{t.name}}
		s.mu.Unlock()
		writeJSON(w, http.StatusAccepted, res)
	case r.Method == http.MethodPost && reBuildStart.MatchString(p):
		m := reBuildStart.FindStringSubmatch(p)
		var in struct {
			FromImage    string `json:"fromImage"`
			FromTemplate string `json:"fromTemplate"`
			StartCmd     string `json:"startCmd"`
		}
		if err := decode(r, &in); err != nil || (in.FromImage == "") == (in.FromTemplate == "") {
			fail(w, http.StatusBadRequest, "exactly one of fromImage or fromTemplate must be given")
			return
		}
		s.mu.Lock()
		t := s.templates[m[1]]
		if t == nil || t.lastBuildID != m[2] {
			s.mu.Unlock()
			fail(w, http.StatusNotFound, "build not found")
			return
		}
		t.status, t.image = "building", in.FromImage
		s.builds[t.name]++
		s.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	case r.Method == http.MethodGet && reBuildStatus.MatchString(p):
		m := reBuildStatus.FindStringSubmatch(p)
		s.mu.Lock()
		t := s.templates[m[1]]
		if t == nil || t.lastBuildID != m[2] {
			s.mu.Unlock()
			fail(w, http.StatusNotFound, "build not found")
			return
		}
		t.buildPolls++
		image, finish := t.image, t.status == "building" && t.buildPolls >= 2
		s.mu.Unlock()
		if finish {
			// The build ends at the second check, so a client must poll.
			status, reason := "ready", ""
			if err := s.machines.Image(ctx, image); err != nil {
				status, reason = "error", err.Error()
			}
			s.mu.Lock()
			t.status, t.reason = status, reason
			s.mu.Unlock()
		}
		s.mu.Lock()
		res := map[string]any{"templateID": t.id, "buildID": t.lastBuildID, "status": t.status, "logs": []string{}, "logEntries": []any{}}
		if t.reason != "" {
			res["reason"] = map[string]any{"message": t.reason, "step": "base"}
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, res)
	case r.Method == http.MethodGet && reAlias.MatchString(p):
		name := reAlias.FindStringSubmatch(p)[1]
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, t := range s.templates {
			if t.name == name {
				writeJSON(w, http.StatusOK, map[string]any{"templateID": t.id, "public": false})
				return
			}
		}
		fail(w, http.StatusNotFound, "template not found")
	case r.Method == http.MethodGet && reTemplate.MatchString(p):
		id := reTemplate.FindStringSubmatch(p)[1]
		s.mu.Lock()
		defer s.mu.Unlock()
		t := s.templates[id]
		if t == nil {
			fail(w, http.StatusNotFound, "template not found")
			return
		}
		res := s.templateJSON(t)
		res["builds"] = []map[string]any{{"buildID": t.lastBuildID, "status": t.status, "cpuCount": t.cpu, "memoryMB": t.mem}}
		writeJSON(w, http.StatusOK, res)
	case r.Method == http.MethodPost && p == "/v2/sandboxes":
		s.create(w, r)
	case reSandbox.MatchString(p):
		id := reSandbox.FindStringSubmatch(p)[1]
		switch r.Method {
		case http.MethodGet:
			s.mu.Lock()
			sb := s.sandboxes[id]
			var res map[string]any
			if sb != nil {
				res = map[string]any{"templateID": sb.templateID, "sandboxID": sb.id, "clientID": "fake", "state": sb.state, "envdVersion": "0.2.0",
					"cpuCount": sb.cpu, "memoryMB": sb.mem, "diskSizeMB": 10240, "domain": Domain, "metadata": sb.metadata,
					"startedAt": time.Now().UTC().Format(time.RFC3339), "endAt": time.Now().Add(time.Duration(sb.timeout) * time.Second).UTC().Format(time.RFC3339)}
			}
			s.mu.Unlock()
			if res == nil {
				fail(w, http.StatusNotFound, "sandbox not found")
				return
			}
			writeJSON(w, http.StatusOK, res)
		case http.MethodDelete:
			s.mu.Lock()
			sb := s.sandboxes[id]
			delete(s.sandboxes, id)
			s.mu.Unlock()
			if sb == nil {
				fail(w, http.StatusNotFound, "sandbox not found")
				return
			}
			_ = s.machines.Remove(ctx, id)
			w.WriteHeader(http.StatusNoContent)
		default:
			fail(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case reSandboxOp.MatchString(p):
		m := reSandboxOp.FindStringSubmatch(p)
		s.sandboxOp(w, r, m[1], m[2])
	case r.Method == http.MethodPost && reConnect.MatchString(p):
		id := reConnect.FindStringSubmatch(p)[1]
		var in struct {
			Timeout int `json:"timeout"`
		}
		_ = decode(r, &in)
		s.mu.Lock()
		sb := s.sandboxes[id]
		if sb == nil {
			s.mu.Unlock()
			fail(w, http.StatusNotFound, "sandbox not found")
			return
		}
		paused := sb.state == "paused"
		s.mu.Unlock()
		status := http.StatusOK
		if paused {
			if err := s.machines.Unpause(ctx, id); err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			status = http.StatusCreated
		}
		s.mu.Lock()
		sb.state = "running"
		if in.Timeout > sb.timeout {
			sb.timeout = in.Timeout // the time to live is only extended
		}
		res := s.sandboxJSON(sb)
		s.mu.Unlock()
		writeJSON(w, status, res)
	default:
		fail(w, http.StatusNotFound, "no route "+r.Method+" "+p)
	}
}

func (s *Server) templateJSON(t *template) map[string]any {
	return map[string]any{"templateID": t.id, "buildID": t.lastBuildID, "cpuCount": t.cpu, "memoryMB": t.mem, "diskSizeMB": 10240,
		"public": false, "aliases": []string{t.name}, "names": []string{t.name}, "buildStatus": t.status, "envdVersion": "0.2.0",
		"createdAt": time.Now().UTC().Format(time.RFC3339), "updatedAt": time.Now().UTC().Format(time.RFC3339), "createdBy": nil,
		"lastSpawnedAt": nil, "spawnCount": 0, "buildCount": 1}
}

func (s *Server) sandboxJSON(sb *sandbox) map[string]any {
	res := map[string]any{"templateID": sb.templateID, "sandboxID": sb.id, "clientID": "fake", "envdVersion": "0.2.0",
		"envdAccessToken": sb.envdToken, "domain": Domain}
	if sb.public {
		res["trafficAccessToken"] = nil
	} else {
		res["trafficAccessToken"] = sb.trafficToken
	}
	return res
}

func (s *Server) newSandbox(t *template) *sandbox {
	return &sandbox{id: "i" + randHex(9), templateID: t.id, state: "running", envdToken: randHex(16), trafficToken: randHex(16),
		public: true, cpu: t.cpu, mem: t.mem, timeout: 300}
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TemplateID string            `json:"templateID"`
		Timeout    int               `json:"timeout"`
		AutoPause  bool              `json:"autoPause"`
		Metadata   map[string]string `json:"metadata"`
		EnvVars    map[string]string `json:"envVars"`
		Network    *struct {
			AllowPublicTraffic *bool    `json:"allowPublicTraffic"`
			AllowOut           []string `json:"allowOut"`
			DenyOut            []string `json:"denyOut"`
		} `json:"network"`
	}
	if err := decode(r, &in); err != nil || in.TemplateID == "" {
		fail(w, http.StatusBadRequest, "templateID is required")
		return
	}
	s.mu.Lock()
	t := s.templates[in.TemplateID]
	for _, x := range s.templates {
		if x.name == in.TemplateID {
			t = x
		}
	}
	if t == nil || t.status != "ready" {
		s.mu.Unlock()
		fail(w, http.StatusNotFound, "template '"+in.TemplateID+"' not found or not ready")
		return
	}
	sb := s.newSandbox(t)
	if in.Timeout > 0 {
		sb.timeout = in.Timeout
	}
	sb.autoPause, sb.metadata = in.AutoPause, in.Metadata
	if in.Network != nil {
		if in.Network.AllowPublicTraffic != nil {
			sb.public = *in.Network.AllowPublicTraffic
		}
		sb.allowOut, sb.denyOut = in.Network.AllowOut, in.Network.DenyOut
	}
	image := t.image
	s.mu.Unlock()
	if err := s.machines.Create(r.Context(), sb.id, image, sb.cpu, sb.mem); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": 500, "error_code": "sandbox_create_failed", "message": err.Error()})
		return
	}
	s.mu.Lock()
	s.sandboxes[sb.id] = sb
	res := s.sandboxJSON(sb)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, res)
}

func (s *Server) sandboxOp(w http.ResponseWriter, r *http.Request, id, op string) {
	ctx := r.Context()
	s.mu.Lock()
	sb := s.sandboxes[id]
	s.mu.Unlock()
	if sb == nil {
		fail(w, http.StatusNotFound, "sandbox not found")
		return
	}
	switch {
	case op == "pause" && r.Method == http.MethodPost:
		s.mu.Lock()
		paused := sb.state == "paused"
		s.mu.Unlock()
		if paused {
			fail(w, http.StatusConflict, "sandbox is already paused")
			return
		}
		if err := s.machines.Pause(ctx, id); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.mu.Lock()
		sb.state = "paused"
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case op == "fork" && r.Method == http.MethodPost:
		var in struct {
			Timeout int `json:"timeout"`
			Count   int `json:"count"`
		}
		_ = decode(r, &in)
		if in.Count == 0 {
			in.Count = 1
		}
		s.mu.Lock()
		running := sb.state == "running"
		t := s.templates[sb.templateID]
		s.mu.Unlock()
		if !running {
			fail(w, http.StatusConflict, "sandbox is not running")
			return
		}
		out := []map[string]any{}
		for range in.Count {
			s.mu.Lock()
			f := s.newSandbox(t)
			f.public, f.allowOut, f.denyOut, f.metadata, f.autoPause = sb.public, sb.allowOut, sb.denyOut, sb.metadata, sb.autoPause
			f.cmd, f.env, f.cpu, f.mem = sb.cmd, sb.env, sb.cpu, sb.mem
			if in.Timeout > 0 {
				f.timeout = in.Timeout
			}
			s.mu.Unlock()
			err := s.machines.Fork(ctx, id, f.id)
			if err == nil && len(f.cmd) > 0 {
				// The fork holds the memory of its parent, so the same process runs in it.
				err = s.machines.Start(ctx, f.id, f.cmd, f.env)
			}
			if err != nil {
				_ = s.machines.Remove(ctx, f.id)
				out = append(out, map[string]any{"error": map[string]any{"code": 500, "message": err.Error()}})
				continue
			}
			s.mu.Lock()
			s.sandboxes[f.id] = f
			out = append(out, map[string]any{"sandbox": s.sandboxJSON(f)})
			s.mu.Unlock()
		}
		writeJSON(w, http.StatusCreated, out)
	case op == "timeout" && r.Method == http.MethodPost:
		var in struct {
			Timeout int `json:"timeout"`
		}
		_ = decode(r, &in)
		s.mu.Lock()
		sb.timeout = in.Timeout
		sb.timeoutRefreshes++
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case op == "network" && r.Method == http.MethodPut:
		var in struct {
			AllowOut []string `json:"allowOut"`
			DenyOut  []string `json:"denyOut"`
		}
		_ = decode(r, &in)
		s.mu.Lock()
		sb.allowOut, sb.denyOut = in.AllowOut, in.DenyOut
		sb.networkUpdates++
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case op == "metrics" && r.Method == http.MethodGet:
		s.mu.Lock()
		mem := int64(sb.mem) << 20
		cpu := sb.cpu
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, []map[string]any{{"timestamp": time.Now().UTC().Format(time.RFC3339), "timestampUnix": time.Now().Unix(),
			"cpuCount": cpu, "cpuUsedPct": 12.5, "memUsed": 256 << 20, "memTotal": mem, "memCache": 0, "diskUsed": 1 << 30, "diskTotal": 10 << 30}})
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// port serves a port URL: the proxy ends TLS here and sends plain HTTP to the guest port.
func (s *Server) port(w http.ResponseWriter, r *http.Request, port int, id string) {
	s.mu.Lock()
	sb := s.sandboxes[id]
	var state, traffic, envdToken string
	var public bool
	if sb != nil {
		state, traffic, envdToken, public = sb.state, sb.trafficToken, sb.envdToken, sb.public
	}
	s.mu.Unlock()
	switch {
	case sb == nil:
		fail(w, http.StatusBadGateway, "The sandbox was not found")
		return
	case state != "running":
		fail(w, http.StatusBadGateway, "The sandbox is paused")
		return
	case port == EnvdPort:
		if r.Header.Get("X-Access-Token") != envdToken {
			fail(w, http.StatusUnauthorized, "invalid envd access token")
			return
		}
		s.envd(w, r, id)
		return
	case !public && r.Header.Get("e2b-traffic-access-token") != traffic:
		fail(w, http.StatusForbidden, "missing or invalid traffic access token")
		return
	}
	addr, err := s.machines.Addr(r.Context(), id, port)
	if err != nil {
		fail(w, http.StatusBadGateway, "The sandbox is running but port is not open")
		return
	}
	s.mu.Lock()
	s.ports[port]++
	s.mu.Unlock()
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = "http", addr
			pr.Out.Header.Del("e2b-traffic-access-token")
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			fail(w, http.StatusBadGateway, err.Error())
		},
	}
	rp.ServeHTTP(w, r)
}

// envd serves process.Process/Start of the envd Connect API: it starts the process as the
// user of the basic auth name and streams the start event.
func (s *Server) envd(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost || r.URL.Path != "/process.Process/Start" {
		fail(w, http.StatusNotFound, "no envd route "+r.URL.Path)
		return
	}
	user := ""
	if raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Basic "); ok {
		if b, err := base64.StdEncoding.DecodeString(raw); err == nil {
			user, _, _ = strings.Cut(string(b), ":")
		}
	}
	if user != "root" {
		fail(w, http.StatusForbidden, "the fake starts processes only as root")
		return
	}
	if r.Header.Get("Content-Type") != "application/connect+json" {
		fail(w, http.StatusUnsupportedMediaType, "want application/connect+json")
		return
	}
	var hdr [5]byte
	if _, err := io.ReadFull(r.Body, hdr[:]); err != nil {
		fail(w, http.StatusBadRequest, "no message")
		return
	}
	msg := make([]byte, binary.BigEndian.Uint32(hdr[1:]))
	if _, err := io.ReadFull(r.Body, msg); err != nil {
		fail(w, http.StatusBadRequest, "short message")
		return
	}
	var in struct {
		Process struct {
			Cmd  string            `json:"cmd"`
			Args []string          `json:"args"`
			Envs map[string]string `json:"envs"`
		} `json:"process"`
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(msg, &in); err != nil || in.Process.Cmd == "" {
		fail(w, http.StatusBadRequest, "bad StartRequest")
		return
	}
	cmd := append([]string{in.Process.Cmd}, in.Process.Args...)
	w.Header().Set("Content-Type", "application/connect+json")
	w.WriteHeader(http.StatusOK)
	frame := func(flags byte, v any) {
		b, _ := json.Marshal(v)
		var h [5]byte
		h[0] = flags
		binary.BigEndian.PutUint32(h[1:], uint32(len(b)))
		_, _ = w.Write(h[:])
		_, _ = w.Write(b)
	}
	if err := s.machines.Start(r.Context(), id, cmd, in.Process.Envs); err != nil {
		frame(0x02, map[string]any{"error": map[string]any{"code": "internal", "message": err.Error()}})
		return
	}
	s.mu.Lock()
	if sb := s.sandboxes[id]; sb != nil {
		sb.cmd, sb.env = cmd, in.Process.Envs
	}
	s.mu.Unlock()
	frame(0, map[string]any{"event": map[string]any{"start": map[string]any{"pid": 101}}})
	frame(0x02, map[string]any{})
}

// ErrNoPort is the error of Addr for a guest port that the machine does not publish.
var ErrNoPort = errors.New("the port is not open")
