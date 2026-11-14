//go:build contract

package app_test

import (
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/apptest"
)

func TestSCN_MOD_002_OwnerRoleRefusesProviderWithoutTools(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	var p struct {
		ID    string `json:"id"`
		Smoke struct {
			Tools  bool `json:"tools"`
			Vision bool `json:"vision"`
			Checks []struct {
				Key  string `json:"key"`
				Pass bool   `json:"pass"`
			} `json:"checks"`
		} `json:"smoke"`
	}
	e.MustDo("POST", e.SP("/model-providers"), map[string]any{"name": "weak", "api_type": "openai-chat", "base_url": e.FakeURL + "/mode/notools,novision/v1", "key": "k-weak-1234"}, &p)
	if c, b := e.Do("PUT", e.SP("/model-roles/owner"), map[string]any{"targets": []map[string]any{{"provider_id": p.ID, "model": "fake/small"}}}, nil); c != 422 || !strings.Contains(b, "test this provider") {
		t.Fatalf("untested provider for owner: %d %s", c, b)
	}
	e.MustDo("POST", e.SP("/model-providers/%s/test", p.ID), map[string]any{"model": "fake/small"}, &p)
	if p.Smoke.Tools || p.Smoke.Vision || len(p.Smoke.Checks) != 4 {
		t.Fatalf("smoke %+v", p.Smoke)
	}
	c, b := e.Do("PUT", e.SP("/model-roles/owner"), map[string]any{"targets": []map[string]any{{"provider_id": p.ID, "model": "fake/small"}}}, nil)
	if c != 422 || !strings.Contains(b, "tool call check") {
		t.Fatalf("owner role accepted a provider without tools: %d %s", c, b)
	}
	// Other roles may use it.
	e.MustDo("PUT", e.SP("/model-roles/summarizer"), map[string]any{"targets": []map[string]any{{"provider_id": p.ID, "model": "fake/small"}}}, nil)
}
