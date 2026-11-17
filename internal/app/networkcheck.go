package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/agentd/client"
	"github.com/alternayte/fettle/internal/zones"
)

// DefaultCheckURL is the HTTPS page that Check network fetches. FETTLE_NETWORK_CHECK_URL
// names another (tests use a fixture site).
const DefaultCheckURL = "https://example.com/"

// NetworkStep is one step of a fetch: dns, connect or tls.
type NetworkStep struct {
	Name   string `json:"name" enum:"dns,connect,tls"`
	State  string `json:"state" enum:"pass,fail,skip"`
	Detail string `json:"detail,omitempty"`
}

// NetworkProbe is one fetch of the check URL, with curl or with Chromium.
type NetworkProbe struct {
	Tool   string        `json:"tool" enum:"curl,chromium"`
	Pass   bool          `json:"pass"`
	Steps  []NetworkStep `json:"steps"`
	Detail string        `json:"detail,omitempty"`
}

// NetworkCheck is the result of Check network on a zone: start its computer and fetch one
// HTTPS page with curl and with Chromium, with DNS, connect and TLS apart.
type NetworkCheck struct {
	Zone     string         `json:"zone"`
	Provider string         `json:"provider"`
	URL      string         `json:"url"`
	Pass     bool           `json:"pass"`
	Proxy    bool           `json:"proxy" doc:"The computer uses a proxy."`
	Roots    bool           `json:"roots" doc:"The computer has extra root certificates."`
	StartMS  int64          `json:"start_ms"`
	Probes   []NetworkProbe `json:"probes"`
	Error    string         `json:"error,omitempty"`
}

func checkURL(z *zones.Zone) string {
	u := os.Getenv("FETTLE_NETWORK_CHECK_URL")
	if u == "" {
		u = DefaultCheckURL
	}
	if z.Network != "allowlist" {
		return u
	}
	pu, err := url.Parse(u)
	if err == nil && hostOnList(z.AllowHosts, pu.Hostname()) {
		return u
	}
	// An allowlist zone fetches its first listed site, so the check tests the zone and not
	// its list.
	for _, h := range z.AllowHosts {
		h = strings.TrimPrefix(strings.TrimSpace(h), "*.")
		if h != "" {
			return "https://" + h + "/"
		}
	}
	return u
}

func hostOnList(list []string, host string) bool {
	host = strings.ToLower(host)
	for _, h := range list {
		h = strings.ToLower(strings.TrimSpace(h))
		if base, ok := strings.CutPrefix(h, "*."); ok {
			if host == base || strings.HasSuffix(host, "."+base) {
				return true
			}
		} else if host == h {
			return true
		}
	}
	return false
}

// CheckNetwork runs Check network on a zone.
func (a *App) CheckNetwork(ctx context.Context, z *zones.Zone) NetworkCheck {
	out := NetworkCheck{Zone: z.Name, URL: checkURL(z), Probes: []NetworkProbe{}}
	if row, err := a.Computers.ProviderOf(ctx, z); err == nil {
		out.Provider = row.Kind
	}
	if out.Provider != "host" {
		if n, err := a.resolveNetwork(ctx); err == nil {
			out.Proxy, out.Roots = n.Proxy != nil, len(n.Roots) > 0
		}
	}
	start := time.Now()
	l, err := a.Computers.Acquire(ctx, z.ID())
	out.StartMS = time.Since(start).Milliseconds()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer l.Release()
	host := ""
	if u, err := url.Parse(out.URL); err == nil {
		host = u.Hostname()
	}
	allowed := z.Network != "allowlist" || hostOnList(z.AllowHosts, host)
	out.Probes = append(out.Probes, curlProbe(ctx, l.Client, out.URL, out.Proxy, allowed, host))
	if z.Size == "small" {
		out.Probes = append(out.Probes, NetworkProbe{Tool: "chromium", Pass: true, Detail: "A small zone has no browser.", Steps: []NetworkStep{
			{Name: "dns", State: "skip"}, {Name: "connect", State: "skip"}, {Name: "tls", State: "skip"},
		}})
	} else {
		out.Probes = append(out.Probes, chromiumProbe(ctx, l.Client, out.URL))
	}
	out.Pass = true
	for _, p := range out.Probes {
		out.Pass = out.Pass && p.Pass
	}
	return out
}

// curlResult is the part of curl's --write-out %{json} that the check reads.
type curlResult struct {
	ExitCode   int     `json:"exitcode"`
	ErrorMsg   string  `json:"errormsg"`
	HTTPCode   int     `json:"http_code"`
	Connect    float64 `json:"time_connect"`
	AppConnect float64 `json:"time_appconnect"`
}

func curlProbe(ctx context.Context, c *client.Client, u string, proxy, allowed bool, host string) NetworkProbe {
	var stdout, stderr strings.Builder
	cmd := "curl -sS -o /dev/null --max-time 20 -w '%{json}' " + shellQuote(u)
	res, err := c.Exec(ctx, client.ExecRequest{Shell: cmd, TimeoutMS: 30000}, func(stream, data string) {
		if stream == "stdout" {
			stdout.WriteString(data)
		} else {
			stderr.WriteString(data)
		}
	})
	if err != nil {
		return NetworkProbe{Tool: "curl", Detail: err.Error(), Steps: steps("fail", "skip", "skip", err.Error())}
	}
	var r curlResult
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &r); jerr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("curl ended with exit code %d", res.ExitCode)
		}
		return NetworkProbe{Tool: "curl", Detail: msg, Steps: steps("fail", "skip", "skip", msg)}
	}
	return classifyCurl(r, proxy, allowed, host)
}

// classifyCurl turns a curl result into the DNS, connect and TLS steps.
func classifyCurl(r curlResult, proxy, allowed bool, host string) NetworkProbe {
	p := NetworkProbe{Tool: "curl"}
	msg := strings.TrimSpace(r.ErrorMsg)
	switch r.ExitCode {
	case 0:
		p.Pass = true
		p.Steps = steps("pass", "pass", "pass", "")
		p.Detail = fmt.Sprintf("HTTP %d", r.HTTPCode)
		return p
	case 5, 6:
		p.Steps = steps("fail", "skip", "skip", msg)
	case 7:
		if !allowed {
			msg = host + " is not on the allowlist of this zone. (" + msg + ")"
		}
		p.Steps = steps("pass", "fail", "skip", msg)
	case 28:
		if r.Connect == 0 {
			p.Steps = steps("pass", "fail", "skip", msg)
		} else {
			p.Steps = steps("pass", "pass", "fail", msg)
		}
	case 35, 51, 53, 54, 58, 59, 60, 64, 66, 77, 80, 82, 83, 90, 91:
		p.Steps = steps("pass", "pass", "fail", msg)
		if r.ExitCode == 60 || r.ExitCode == 77 || r.ExitCode == 35 {
			msg = "This network inspects HTTPS. Add your company root certificate in Settings, Computers. (" + msg + ")"
			p.Steps[2].Detail = msg
		}
	default:
		if !allowed {
			msg = host + " is not on the allowlist of this zone. (" + msg + ")"
		} else if proxy && strings.Contains(msg, "CONNECT") {
			msg = "The proxy did not open a connection. (" + msg + ")"
		}
		p.Steps = steps("pass", "fail", "skip", msg)
	}
	if proxy && p.Steps[0].State == "pass" {
		p.Steps[0].Detail = "The proxy looks up the host."
	}
	p.Detail = msg
	return p
}

// steps returns the three steps; the first step in state fail gets the detail.
func steps(dns, connect, tls, detail string) []NetworkStep {
	out := []NetworkStep{{Name: "dns", State: dns}, {Name: "connect", State: connect}, {Name: "tls", State: tls}}
	for i := range out {
		if out[i].State == "fail" {
			out[i].Detail = detail
			break
		}
	}
	return out
}

var chromiumCodeRe = regexp.MustCompile(`net::(ERR_[A-Z0-9_]+)`)

func chromiumProbe(ctx context.Context, c *client.Client, u string) NetworkProbe {
	id := "network-check-" + uuid.NewString()[:8]
	if err := c.BrowserOpen(ctx, client.BrowserContextRequest{ID: id, Ephemeral: true}); err != nil {
		return NetworkProbe{Tool: "chromium", Detail: err.Error(), Steps: steps("skip", "skip", "skip", "")}
	}
	defer func() { _ = c.BrowserClose(context.WithoutCancel(ctx), id) }()
	_, err := c.BrowserNavigate(ctx, id, u)
	if err == nil {
		return NetworkProbe{Tool: "chromium", Pass: true, Steps: steps("pass", "pass", "pass", "")}
	}
	return classifyChromium(err.Error())
}

// classifyChromium reads the net::ERR code that agentd keeps at the end of a browser error.
func classifyChromium(msg string) NetworkProbe {
	p := NetworkProbe{Tool: "chromium", Detail: msg}
	m := chromiumCodeRe.FindStringSubmatch(msg)
	code := ""
	if m != nil {
		code = m[1]
	}
	switch {
	case code == "ERR_NAME_NOT_RESOLVED":
		p.Steps = steps("fail", "skip", "skip", msg)
	case strings.HasPrefix(code, "ERR_CERT_") || strings.HasPrefix(code, "ERR_SSL_") || code == "ERR_BAD_SSL_CLIENT_AUTH_CERT":
		p.Steps = steps("pass", "pass", "fail", msg)
	default:
		p.Steps = steps("pass", "fail", "skip", msg)
	}
	return p
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (a *App) registerNetworkCheck(api huma.API) {
	huma.Register(api, op("check-zone-network", http.MethodPost, "/spaces/{space}/zones/{zone}/computer/network-check", "Start the zone's computer and fetch one HTTPS page with curl and Chromium; report DNS, connect and TLS apart", "computers"), func(ctx context.Context, in *ZonePath) (*struct{ Body NetworkCheck }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		z, err := a.Zones.Get(ctx, in.Space, in.Zone)
		if err != nil {
			return nil, zoneError(err)
		}
		out := a.CheckNetwork(ctx, z)
		_ = a.Audit.Record(ctx, actorOf(p), &in.Space, "computer.network_check", "zone:"+z.ID().String(), map[string]string{"pass": boolText(out.Pass), "url": out.URL})
		return &struct{ Body NetworkCheck }{Body: out}, nil
	})
}
