// Package mcp is the fake MCP server fakemail of the tests (§24.4). It has list_inbox
// (readOnlyHint), send_email (no hints) and delete_email (destructiveHint), over Streamable
// HTTP with OAuth or a header key, and over stdio. It records every call.
package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alternayte/fettle/internal/fakes/oauth"
)

// Call is one recorded tool call.
type Call struct {
	Tool    string
	Args    map[string]any
	Account string // the OAuth user or the header key that made the call
}

// Mail is the fake mail service.
type Mail struct {
	mu    sync.Mutex
	calls []Call
	// OAuth, when set, protects the HTTP handler with bearer tokens from this server.
	OAuth *oauth.Server
	// HeaderKey, when set, protects the HTTP handler with the X-Api-Key header.
	HeaderKey string
	// URL is the public base URL of the HTTP handler, for the resource metadata.
	URL string
	// ShowHost adds the host name to list_inbox, so a test sees where the server runs.
	ShowHost bool
	// ShowAccount adds the caller to list_inbox, so a test sees which account a stdio
	// process serves.
	ShowAccount bool
	// SendDelay holds send_email after it records the call, so a test can stop Fettle
	// while the send is in flight.
	SendDelay time.Duration
}

// Calls returns the recorded calls.
func (m *Mail) Calls() []Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Call(nil), m.calls...)
}

func (m *Mail) record(c Call) {
	m.mu.Lock()
	m.calls = append(m.calls, c)
	m.mu.Unlock()
}

type ctxKey struct{}

func boolp(b bool) *bool { return &b }

// Server returns the MCP server. account names the caller in recorded calls.
func (m *Mail) Server(account func(ctx context.Context) string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "fakemail", Version: "1.0.0"}, nil)
	obj := func(props string, req ...string) json.RawMessage {
		r, _ := json.Marshal(req)
		return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{%s},"required":%s}`, props, r))
	}
	add := func(t *mcp.Tool, text func(args map[string]any) string) {
		s.AddTool(t, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args map[string]any
			_ = json.Unmarshal(req.Params.Arguments, &args)
			acc := account(ctx)
			m.record(Call{Tool: t.Name, Args: args, Account: acc})
			body := text(args)
			if m.ShowAccount && t.Name == "list_inbox" {
				body += "\nSigned in as " + acc + "."
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: body}}}, nil
		})
	}
	add(&mcp.Tool{Name: "list_inbox", Description: "List the newest emails of the inbox.", InputSchema: obj(`"limit":{"type":"integer"}`), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(map[string]any) string {
			text := "1. From ana@acme.test: The Q3 numbers are ready.\n2. From ben@acme.test: Lunch on Friday?\n3. From noreply@shop.test: Your order shipped."
			if m.ShowHost {
				h, _ := os.Hostname()
				text += "\nServed by host " + h + "."
			}
			return text
		})
	add(&mcp.Tool{Name: "send_email", Description: "Send an email.", InputSchema: obj(`"to":{"type":"string"},"subject":{"type":"string"},"body":{"type":"string"}`, "to", "subject", "body")},
		func(a map[string]any) string {
			time.Sleep(m.SendDelay)
			return fmt.Sprintf("Sent to %v.", a["to"])
		})
	add(&mcp.Tool{Name: "delete_email", Description: "Delete an email by its number.", InputSchema: obj(`"id":{"type":"integer"}`, "id"), Annotations: &mcp.ToolAnnotations{DestructiveHint: boolp(true)}},
		func(a map[string]any) string { return fmt.Sprintf("Deleted email %v.", a["id"]) })
	return s
}

// Handler serves fakemail over Streamable HTTP, with the OAuth resource metadata.
func (m *Mail) Handler() http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return m.Server(func(ctx context.Context) string { s, _ := ctx.Value(ctxKey{}).(string); return s })
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" || r.URL.Path == "/.well-known/oauth-protected-resource/mcp" {
			w.Header().Set("Content-Type", "application/json")
			auth := []string{}
			if m.OAuth != nil {
				auth = []string{m.OAuth.URL}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": m.URL + "/mcp", "authorization_servers": auth, "bearer_methods_supported": []string{"header"}})
			return
		}
		account := ""
		switch {
		case m.OAuth != nil:
			account = m.OAuth.UserOf(oauth.BearerOf(r))
			if account == "" {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`, m.URL))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		case m.HeaderKey != "":
			if r.Header.Get("X-Api-Key") != m.HeaderKey {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			account = "key:" + m.HeaderKey[:4]
		}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, account)))
	})
}

// ServeStdio serves fakemail on stdin and stdout until the client closes. With the
// variable FAKEMAIL_TOKEN, the caller is the user of that token, as UserOfToken names it.
func (m *Mail) ServeStdio(ctx context.Context) error {
	account := "stdio"
	if tok := os.Getenv("FAKEMAIL_TOKEN"); tok != "" {
		account = UserOfToken(tok)
	}
	return m.Server(func(context.Context) string { return account }).Run(ctx, &mcp.StdioTransport{})
}

// UserOfToken names the fake user of a token without the token itself, so tool output
// shows which account called without a secret in it.
func UserOfToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return "user-" + hex.EncodeToString(sum[:4])
}
