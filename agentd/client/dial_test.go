package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alternayte/fettle/agentd/client"
)

// A refused websocket handshake must carry agentd's reason, not only the status. Without it
// a connector shows "expected handshake response status code 101 but got 400".
func TestWebsocketDialKeepsAgentdReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"the MCP server did not start: exec: \"docker\": executable file not found in $PATH"}`))
	}))
	defer srv.Close()
	c := client.NewInsecureForTest(srv.URL, "t")
	ctx := context.Background()
	dials := map[string]func() error{
		"mcp":    func() error { _, err := c.MCPStdio(ctx, client.MCPStdioRequest{Argv: []string{"docker"}}); return err },
		"screen": func() error { _, err := c.ScreenDial(ctx, "s", false); return err },
		"teach":  func() error { _, err := c.TeachPTY(ctx, "s", 80, 24); return err },
	}
	for name, dial := range dials {
		err := dial()
		var ae *client.Error
		if !errors.As(err, &ae) || ae.Status != http.StatusBadRequest || !strings.Contains(ae.Message, `"docker": executable file not found`) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}
