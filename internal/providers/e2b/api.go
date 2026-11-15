package e2b

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// APIError is a non-2xx reply of the E2B API.
type APIError struct {
	Status    int
	ErrorCode string
	Message   string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if e.ErrorCode != "" {
		return fmt.Sprintf("e2b: %d %s: %s", e.Status, e.ErrorCode, msg)
	}
	return fmt.Sprintf("e2b: %d: %s", e.Status, msg)
}

func statusOf(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

// call sends one API request with the API key. in is nil for no body; out is nil to drop
// the reply.
func (p *Provider) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.API, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", p.key)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		ae := &APIError{Status: resp.StatusCode}
		var e struct {
			ErrorCode string `json:"error_code"`
			Message   string `json:"message"`
		}
		if json.Unmarshal(raw, &e) == nil {
			ae.ErrorCode, ae.Message = e.ErrorCode, e.Message
		}
		return ae
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("e2b: %s %s: decode: %w", method, path, err)
		}
	}
	return nil
}

func esc(s string) string { return url.PathEscape(s) }

// The request and reply shapes below hold only the fields Fettle uses, by the names of the
// E2B OpenAPI document.

type templateBuildRequest struct {
	Name     string `json:"name"`
	CPUCount int    `json:"cpuCount"`
	MemoryMB int    `json:"memoryMB"`
}

type templateBuildResponse struct {
	TemplateID string `json:"templateID"`
	BuildID    string `json:"buildID"`
}

type templateBuildStart struct {
	FromImage string `json:"fromImage"`
}

type templateBuildInfo struct {
	Status string `json:"status"`
	Reason *struct {
		Message string `json:"message"`
		Step    string `json:"step"`
	} `json:"reason"`
}

type templateAlias struct {
	TemplateID string `json:"templateID"`
}

type templateWithBuilds struct {
	TemplateID  string `json:"templateID"`
	BuildStatus string `json:"buildStatus"`
}

type networkConfig struct {
	AllowPublicTraffic *bool    `json:"allowPublicTraffic,omitempty"`
	AllowOut           []string `json:"allowOut,omitempty"`
	DenyOut            []string `json:"denyOut,omitempty"`
}

type newSandbox struct {
	TemplateID string            `json:"templateID"`
	Timeout    int               `json:"timeout"`
	AutoPause  bool              `json:"autoPause"`
	Network    *networkConfig    `json:"network,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type sandbox struct {
	TemplateID         string  `json:"templateID"`
	SandboxID          string  `json:"sandboxID"`
	EnvdAccessToken    string  `json:"envdAccessToken"`
	TrafficAccessToken *string `json:"trafficAccessToken"`
	Domain             *string `json:"domain"`
}

type sandboxDetail struct {
	SandboxID string  `json:"sandboxID"`
	State     string  `json:"state"`
	Domain    *string `json:"domain"`
}

type connectSandbox struct {
	Timeout int `json:"timeout"`
}

type forkRequest struct {
	Timeout int `json:"timeout"`
	Count   int `json:"count"`
}

type forkResult struct {
	Sandbox *sandbox `json:"sandbox"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type timeoutRequest struct {
	Timeout int `json:"timeout"`
}

type sandboxMetric struct {
	TimestampUnix int64   `json:"timestampUnix"`
	CPUUsedPct    float64 `json:"cpuUsedPct"`
	MemUsed       int64   `json:"memUsed"`
	DiskUsed      int64   `json:"diskUsed"`
}
