package e2b

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// EnvdPort is the port of envd, the E2B daemon in each sandbox.
const EnvdPort = 49983

// envdStart is the Connect JSON form of process.StartRequest of the envd process service.
type envdStart struct {
	Process envdProcess `json:"process"`
	Tag     string      `json:"tag,omitempty"`
}

type envdProcess struct {
	Cmd  string            `json:"cmd"`
	Args []string          `json:"args"`
	Envs map[string]string `json:"envs,omitempty"`
}

// envdEvent is one process.StartResponse message.
type envdEvent struct {
	Event struct {
		Start *struct {
			PID uint32 `json:"pid"`
		} `json:"start"`
		End *struct {
			ExitCode int    `json:"exitCode"`
			Status   string `json:"status"`
			Error    string `json:"error"`
		} `json:"end"`
	} `json:"event"`
}

// startProcess starts a background process as root through envd and returns when envd
// reports its start. The process keeps running after the stream closes.
func (p *Provider) startProcess(ctx context.Context, sb *sandbox, tag string, cmd []string, env map[string]string) error {
	msg, err := json.Marshal(envdStart{Process: envdProcess{Cmd: cmd[0], Args: cmd[1:], Envs: env}, Tag: tag})
	if err != nil {
		return err
	}
	var body bytes.Buffer
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(msg)))
	body.Write(hdr[:])
	body.Write(msg)
	u := p.portURL("https", EnvdPort, sb.SandboxID, domainOf(sb.Domain)) + "/process.Process/Start"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/connect+json")
	req.Header.Set("Connect-Protocol-Version", "1")
	// envd picks the user from the basic auth name; agentd starts as root (§10.5).
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("root:")))
	if sb.EnvdAccessToken != "" {
		req.Header.Set("X-Access-Token", sb.EnvdAccessToken)
	}
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("envd: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("envd: %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}
	for {
		if _, err := io.ReadFull(resp.Body, hdr[:]); err != nil {
			return fmt.Errorf("envd: the stream ended before the process started: %w", err)
		}
		n := binary.BigEndian.Uint32(hdr[1:])
		if n > 1<<20 {
			return errors.New("envd: message too large")
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(resp.Body, buf); err != nil {
			return fmt.Errorf("envd: %w", err)
		}
		if hdr[0]&0x02 != 0 { // end of stream
			var end struct {
				Error *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(buf, &end)
			if end.Error != nil {
				return fmt.Errorf("envd: %s: %s", end.Error.Code, end.Error.Message)
			}
			return errors.New("envd: the stream ended before the process started")
		}
		var ev envdEvent
		if err := json.Unmarshal(buf, &ev); err != nil {
			return fmt.Errorf("envd: %w", err)
		}
		switch {
		case ev.Event.Start != nil:
			return nil
		case ev.Event.End != nil:
			e := ev.Event.End
			return fmt.Errorf("envd: %s exited at start: %d %s %s", cmd[0], e.ExitCode, e.Status, e.Error)
		}
	}
}
