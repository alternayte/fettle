package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/apptest"
)

// fettle computer add kiln --url tests the host and the token, and names the fault.
func TestKilnCheckTestsHostAndToken(t *testing.T) {
	kh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer acme-kiln-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "unauthorized", "message": "bad token"})
			return
		}
		switch r.URL.Path {
		case "/v1/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case "/v1/templates":
			_ = json.NewEncoder(w).Encode([]any{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(kh.Close)
	e := apptest.Start(t, "sqlite")
	var out struct {
		Pass       bool   `json:"pass"`
		Error      string `json:"error"`
		Configured bool   `json:"configured"`
	}
	e.MustDo("POST", "/computer-setup/kiln-check", map[string]string{"url": kh.URL, "token": "acme-kiln-token"}, &out)
	if !out.Pass || out.Configured {
		t.Fatalf("a working host: %+v", out)
	}
	e.MustDo("POST", "/computer-setup/kiln-check", map[string]string{"url": kh.URL, "token": "acme-wrong-token"}, &out)
	if out.Pass || out.Error == "" || strings.Contains(out.Error, "acme-wrong-token") {
		t.Fatalf("a wrong token: %+v", out)
	}
	if code, _ := e.Do("POST", "/computer-setup/kiln-check", map[string]string{"url": "kiln.acme.test", "token": "x"}, nil); code != 422 {
		t.Fatalf("a URL without a scheme: %d", code)
	}
}
