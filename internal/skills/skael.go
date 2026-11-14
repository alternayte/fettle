package skills

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
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
)

// SkaelConfig is the Skael sync of a space (§13.1): the registry URL, an API key and the
// set to pull.
type SkaelConfig struct {
	URL string
	Key string
	Set string
}

// ErrNoSkael means Skael sync is not set up for the space.
var ErrNoSkael = errors.New("Skael sync is not set up; add the URL, the key and the set in Settings, Skills")

// Skael talks to a Skael registry.
type Skael struct {
	HTTP *http.Client
}

func (k *Skael) do(ctx context.Context, c SkaelConfig, method, p string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+p, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := k.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("reach Skael: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("Skael %s %s: status %d: %s", method, p, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	if b, ok := out.(*[]byte); ok {
		*b, err = io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// SyncResult tells what a sync changed.
type SyncResult struct {
	Skills  int `json:"skills"`
	Updated int `json:"updated"`
}

// Sync pulls the skills of the configured set into the library. A skill whose Skael
// version did not change keeps its version.
func (s *Service) Sync(ctx context.Context, k *Skael, actor audit.Actor, spaceID uuid.UUID, c SkaelConfig) (SyncResult, error) {
	if c.URL == "" || c.Key == "" || c.Set == "" {
		return SyncResult{}, ErrNoSkael
	}
	var res struct {
		Entries []struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
			SHA256  string `json:"sha256"`
		} `json:"entries"`
	}
	if err := k.do(ctx, c, http.MethodGet, "/api/sets/"+url.PathEscape(c.Set)+"/resolve", nil, &res); err != nil {
		return SyncResult{}, err
	}
	out := SyncResult{Skills: len(res.Entries)}
	for _, e := range res.Entries {
		if sk, err := s.ByName(ctx, spaceID, e.Name); err == nil {
			if v, err := s.Version(ctx, sk, sk.CurrentVersion); err == nil && v.Upstream == e.Version {
				continue
			}
		}
		var archive []byte
		if err := k.do(ctx, c, http.MethodGet, fmt.Sprintf("/api/skills/%s/versions/%d/download", url.PathEscape(e.Name), e.Version), nil, &archive); err != nil {
			return out, err
		}
		files, err := FromTarGz(archive)
		if err != nil {
			return out, fmt.Errorf("the Skael archive of %s does not read: %w", e.Name, err)
		}
		if _, _, err := s.Store(ctx, actor, spaceID, files, "skael", e.Version); err != nil {
			return out, fmt.Errorf("store %s: %w", e.Name, err)
		}
		out.Updated++
	}
	return out, nil
}

// Activation reports to Skael that a run loaded a skill.
func (k *Skael) Activation(ctx context.Context, c SkaelConfig, name string, upstream int) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return k.do(ctx, c, http.MethodPost, "/api/events", map[string]any{"skill_name": name, "agent": "fettle", "event_source": "tool_invocation", "version": upstream}, nil)
}
