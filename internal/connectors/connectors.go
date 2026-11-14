// Package connectors connects bots to MCP servers (§12): remote servers over Streamable
// HTTP with OAuth 2.1 (PKCE, dynamic client registration) or a header key, stdio servers
// inside the zone computer, and Composio. Accounts are zone grants.
package connectors

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/vault"
)

// Errors of the connectors service.
var (
	ErrNotFound    = errors.New("connector not found")
	ErrNoAccount   = errors.New("connector account not found")
	ErrBadInput    = errors.New("a connector needs a name and a URL or a command")
	ErrState       = errors.New("this sign-in link is not valid or has expired; start the connection again")
	ErrNoDiscovery = errors.New("the server does not say where to sign in; use a header key instead")
)

// Service manages connectors and calls their tools.
type Service struct {
	e     *drel.Engine
	audit *audit.Log
	vault *vault.Vault
	http  *http.Client
	// CallbackURL is the OAuth redirect URI of this server.
	CallbackURL func() string
	// Stdio opens a stdio MCP server inside the zone computer of a bot (agentd /v1/mcp/stdio).
	Stdio func(ctx context.Context, spaceID, botID uuid.UUID, command []string) (io.ReadWriteCloser, error)
	now   func() time.Time
}

// New returns the connectors service.
func New(e *drel.Engine, a *audit.Log, v *vault.Vault, client *http.Client) *Service {
	return &Service{e: e, audit: a, vault: v, http: client, now: func() time.Time { return time.Now().UTC() }}
}

// Classify returns the class of a connector tool (§12.1): readOnlyHint is read,
// destructiveHint is write_external, a name or description that says send, post, reply or
// message is send, and the rest is write_external. An owner's setting wins.
func Classify(t *mcp.Tool, set ToolSetting) string {
	if set.Class != "" {
		return set.Class
	}
	if t.Annotations != nil {
		if t.Annotations.ReadOnlyHint {
			return envelopes.ClassRead
		}
		if t.Annotations.DestructiveHint != nil && *t.Annotations.DestructiveHint {
			return envelopes.ClassWriteExternal
		}
	}
	if sendWords.MatchString(t.Name + " " + t.Description) {
		return envelopes.ClassSend
	}
	return envelopes.ClassWriteExternal
}

var sendWords = regexp.MustCompile(`(?i)(^|[^a-z])(send|post|reply|message)`)

// Input creates a connector.
type Input struct {
	Name       string
	Kind       string // mcp_remote, mcp_stdio or composio
	URL        string
	Command    []string
	Auth       string // none, oauth or header
	HeaderName string
	Icon       string
}

// Create adds a connector. An OAuth connector registers Fettle as a client first.
func (s *Service) Create(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, in Input) (*Connector, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Kind == "" {
		in.Kind = "mcp_remote"
	}
	if in.Auth == "" {
		in.Auth = "none"
	}
	switch {
	case in.Name == "":
		return nil, ErrBadInput
	case in.Kind == "mcp_stdio" && len(in.Command) == 0:
		return nil, ErrBadInput
	case in.Kind != "mcp_stdio":
		u, err := url.Parse(in.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, fmt.Errorf("%w: the URL must start with https://", ErrBadInput)
		}
	}
	if in.Auth == "header" && in.HeaderName == "" {
		in.HeaderName = "Authorization"
	}
	c := &Connector{SpaceID: spaceID, Name: in.Name, Kind: in.Kind, URL: in.URL, Command: in.Command, Auth: in.Auth, HeaderName: in.HeaderName, Icon: in.Icon, Tools: map[string]ToolSetting{}}
	if c.Command == nil {
		c.Command = []string{}
	}
	if in.Auth == "oauth" {
		oc, err := s.register(ctx, in.URL)
		if err != nil {
			return nil, err
		}
		c.OAuth = *oc
	}
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), ConnectorMeta).Add(c)
		return s.audit.Record(ctx, actor, &spaceID, "connector.create", "connector:"+c.ID().String(), map[string]string{"name": c.Name, "kind": c.Kind, "auth": c.Auth})
	})
	return c, err
}

// getJSON reads a JSON document.
func (s *Service) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

var resourceMeta = regexp.MustCompile(`resource_metadata="([^"]+)"`)

// register finds the authorization server of an MCP URL (RFC 9728, RFC 8414) and registers
// Fettle as a public client with PKCE (RFC 7591).
func (s *Service) register(ctx context.Context, mcpURL string) (*OAuthClient, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach the connector: %w", err)
	}
	_ = resp.Body.Close()
	u, _ := url.Parse(mcpURL)
	prmURL := u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource"
	if m := resourceMeta.FindStringSubmatch(resp.Header.Get("WWW-Authenticate")); m != nil {
		prmURL = m[1]
	}
	var prm struct {
		Resource string   `json:"resource"`
		Servers  []string `json:"authorization_servers"`
		Scopes   []string `json:"scopes_supported"`
	}
	if err := s.getJSON(ctx, prmURL, &prm); err != nil || len(prm.Servers) == 0 {
		return nil, ErrNoDiscovery
	}
	issuer := strings.TrimRight(prm.Servers[0], "/")
	var as struct {
		Issuer       string   `json:"issuer"`
		Authorize    string   `json:"authorization_endpoint"`
		Token        string   `json:"token_endpoint"`
		Registration string   `json:"registration_endpoint"`
		Methods      []string `json:"code_challenge_methods_supported"`
	}
	if err := s.getJSON(ctx, issuer+"/.well-known/oauth-authorization-server", &as); err != nil {
		if err := s.getJSON(ctx, issuer+"/.well-known/openid-configuration", &as); err != nil {
			return nil, fmt.Errorf("read the sign-in server: %w", err)
		}
	}
	if !slices.Contains(as.Methods, "S256") {
		return nil, errors.New("the sign-in server does not support PKCE with S256")
	}
	if as.Registration == "" {
		return nil, errors.New("the sign-in server does not allow client registration; use a header key instead")
	}
	redirect := s.CallbackURL()
	body, _ := json.Marshal(map[string]any{"client_name": "Fettle", "redirect_uris": []string{redirect}, "grant_types": []string{"authorization_code", "refresh_token"},
		"response_types": []string{"code"}, "token_endpoint_auth_method": "none"})
	rreq, _ := http.NewRequestWithContext(ctx, http.MethodPost, as.Registration, strings.NewReader(string(body)))
	rreq.Header.Set("Content-Type", "application/json")
	rresp, err := s.http.Do(rreq)
	if err != nil {
		return nil, fmt.Errorf("register at the sign-in server: %w", err)
	}
	defer rresp.Body.Close()
	var reg struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if rresp.StatusCode/100 != 2 || json.NewDecoder(rresp.Body).Decode(&reg) != nil || reg.ClientID == "" {
		return nil, fmt.Errorf("the sign-in server refused the registration (status %d)", rresp.StatusCode)
	}
	return &OAuthClient{Issuer: issuer, AuthorizeURL: as.Authorize, TokenURL: as.Token, ClientID: reg.ClientID, Resource: prm.Resource, Scopes: strings.Join(prm.Scopes, " "), Registered: true, RedirectURI: redirect, ClientSecret: reg.ClientSecret}, nil
}

func (c *Connector) oauthConfig() *oauth2.Config {
	o := &oauth2.Config{ClientID: c.OAuth.ClientID, ClientSecret: c.OAuth.ClientSecret, RedirectURL: c.OAuth.RedirectURI,
		Endpoint: oauth2.Endpoint{AuthURL: c.OAuth.AuthorizeURL, TokenURL: c.OAuth.TokenURL, AuthStyle: oauth2.AuthStyleInParams}}
	if c.OAuth.Scopes != "" {
		o.Scopes = strings.Fields(c.OAuth.Scopes)
	}
	return o
}

// Get returns a connector of a space.
func (s *Service) Get(ctx context.Context, spaceID, id uuid.UUID) (*Connector, error) {
	c, err := drel.NewRepository(s.e, ConnectorMeta).Where(Connectors.ID.Eq(id)).Where(Connectors.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, ErrNotFound
	}
	return c, nil
}

// List returns the connectors of a space.
func (s *Service) List(ctx context.Context, spaceID uuid.UUID) ([]*Connector, error) {
	return drel.NewRepository(s.e, ConnectorMeta).Where(Connectors.SpaceID.Eq(spaceID)).OrderBy(Connectors.Name.Asc()).All(ctx)
}

// Accounts returns the accounts of a connector.
func (s *Service) Accounts(ctx context.Context, connectorID uuid.UUID) ([]*ConnectorAccount, error) {
	return drel.NewRepository(s.e, ConnectorAccountMeta).Where(ConnectorAccounts.ConnectorID.Eq(connectorID)).OrderBy(ConnectorAccounts.Label.Asc()).All(ctx)
}

// Account returns an account of a space.
func (s *Service) Account(ctx context.Context, spaceID, id uuid.UUID) (*ConnectorAccount, error) {
	a, err := drel.NewRepository(s.e, ConnectorAccountMeta).Where(ConnectorAccounts.ID.Eq(id)).Where(ConnectorAccounts.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, ErrNoAccount
	}
	return a, nil
}

// Delete removes a connector and its accounts.
func (s *Service) Delete(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		if _, err := tx.Exec(ctx, "DELETE FROM zone_grants WHERE kind = 'connector_account' AND target_id IN (SELECT id FROM connector_accounts WHERE connector_id = $1)", id); err != nil {
			return err
		}
		n, err := tx.Exec(ctx, "DELETE FROM connectors WHERE id = $1 AND space_id = $2", id, spaceID)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return s.audit.Record(ctx, actor, &spaceID, "connector.delete", "connector:"+id.String(), nil)
	})
}

// SetTool changes the class of a tool or turns it off (§12.1). It applies to the next run.
func (s *Service) SetTool(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID, tool string, set ToolSetting) error {
	if set.Class != "" && !slices.Contains(envelopes.AllClasses, set.Class) {
		return fmt.Errorf("%w: unknown class %s", ErrBadInput, set.Class)
	}
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		c, err := drel.Repo(drel.MustFromContext(ctx), ConnectorMeta).Where(Connectors.ID.Eq(id)).Where(Connectors.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if c == nil {
			return ErrNotFound
		}
		tools := map[string]ToolSetting{}
		for k, v := range c.Tools {
			tools[k] = v
		}
		if set.Class == "" && !set.Disabled {
			delete(tools, tool)
		} else {
			tools[tool] = set
		}
		c.Tools = tools
		return s.audit.Record(ctx, actor, &spaceID, "connector.tool", "connector:"+id.String(), map[string]string{"tool": tool, "class": set.Class, "disabled": fmt.Sprint(set.Disabled)})
	})
}

func aad(accountID uuid.UUID) string { return "connector-account:" + accountID.String() }

// token is the stored credential of an OAuth account.
type token struct {
	Access  string    `json:"access_token"`
	Refresh string    `json:"refresh_token,omitempty"`
	Expiry  time.Time `json:"expiry"`
}

// AddKeyAccount adds an account whose credential is a header value (or none for stdio).
func (s *Service) AddKeyAccount(ctx context.Context, actor audit.Actor, spaceID, connectorID uuid.UUID, label, value string) (*ConnectorAccount, error) {
	c, err := s.Get(ctx, spaceID, connectorID)
	if err != nil {
		return nil, err
	}
	if c.Auth == "oauth" {
		return nil, fmt.Errorf("%w: this connector signs in with OAuth", ErrBadInput)
	}
	return s.saveAccount(ctx, actor, c, label, []byte(value), nil)
}

func (s *Service) saveAccount(ctx context.Context, actor audit.Actor, c *Connector, label string, secret []byte, expires *time.Time) (*ConnectorAccount, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "default"
	}
	a := &ConnectorAccount{ConnectorID: c.ID(), SpaceID: c.SpaceID, Label: label, ExpiresAt: expires}
	// Seal before the row is tracked: the first seal of a space creates its data key in a
	// nested transaction, which writes tracked rows as they are at that moment.
	a.SetID(uuid.Must(uuid.NewV7()))
	sealed, err := s.vault.Seal(ctx, c.SpaceID, secret, aad(a.ID()))
	if err != nil {
		return nil, err
	}
	a.TokenCiphertext = base64.StdEncoding.EncodeToString(sealed)
	err = s.e.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), ConnectorAccountMeta).Add(a)
		return s.audit.Record(ctx, actor, &c.SpaceID, "connector.account_add", "connector:"+c.ID().String(), map[string]string{"label": label})
	})
	return a, err
}

// StartAuthorize returns the URL where a person signs in to add an OAuth account.
func (s *Service) StartAuthorize(ctx context.Context, actor audit.Actor, spaceID, connectorID, userID uuid.UUID, label string) (string, error) {
	c, err := s.Get(ctx, spaceID, connectorID)
	if err != nil {
		return "", err
	}
	if c.Auth != "oauth" || !c.OAuth.Registered {
		return "", fmt.Errorf("%w: this connector does not sign in with OAuth", ErrBadInput)
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	state := hex.EncodeToString(b)
	verifier := oauth2.GenerateVerifier()
	sealed, err := s.vault.Seal(ctx, spaceID, []byte(verifier), "oauth-state:"+state)
	if err != nil {
		return "", err
	}
	err = s.e.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), OAuthStateMeta).Add(&OAuthState{State: state, ConnectorID: c.ID(), Label: label, Verifier: base64.StdEncoding.EncodeToString(sealed), UserID: userID, ExpiresAt: s.now().Add(15 * time.Minute)})
		return nil
	})
	if err != nil {
		return "", err
	}
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}
	if c.OAuth.Resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", c.OAuth.Resource))
	}
	return c.oauthConfig().AuthCodeURL(state, opts...), nil
}

// Callback finishes an OAuth sign-in: it checks the state, trades the code with the PKCE
// verifier and stores the tokens sealed with the space data key.
func (s *Service) Callback(ctx context.Context, state, code string) (*ConnectorAccount, error) {
	var st *OAuthState
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		var err error
		st, err = drel.Repo(drel.MustFromContext(ctx), OAuthStateMeta).Where(OAuthStates.State.Eq(state)).FirstOrNil(ctx)
		if err != nil || st == nil {
			return ErrState
		}
		_, err = drel.MustFromContext(ctx).Exec(ctx, "DELETE FROM oauth_states WHERE id = $1", st.ID())
		return err
	})
	if err != nil {
		return nil, err
	}
	if s.now().After(st.ExpiresAt) {
		return nil, ErrState
	}
	c, err := drel.NewRepository(s.e, ConnectorMeta).Where(Connectors.ID.Eq(st.ConnectorID)).FirstOrNil(ctx)
	if err != nil || c == nil {
		return nil, ErrNotFound
	}
	sealed, _ := base64.StdEncoding.DecodeString(st.Verifier)
	verifier, err := s.vault.Open(ctx, c.SpaceID, sealed, "oauth-state:"+state)
	if err != nil {
		return nil, err
	}
	octx := context.WithValue(ctx, oauth2.HTTPClient, s.http)
	opts := []oauth2.AuthCodeOption{oauth2.VerifierOption(string(verifier))}
	if c.OAuth.Resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", c.OAuth.Resource))
	}
	tok, err := c.oauthConfig().Exchange(octx, code, opts...)
	if err != nil {
		return nil, fmt.Errorf("the sign-in server refused the code: %w", err)
	}
	raw, _ := json.Marshal(token{Access: tok.AccessToken, Refresh: tok.RefreshToken, Expiry: tok.Expiry})
	var exp *time.Time
	if !tok.Expiry.IsZero() {
		e := tok.Expiry.UTC()
		exp = &e
	}
	s.vault.Redactor.Register(c.SpaceID, c.Name+" token", tok.AccessToken)
	if tok.RefreshToken != "" {
		s.vault.Redactor.Register(c.SpaceID, c.Name+" refresh token", tok.RefreshToken)
	}
	return s.saveAccount(ctx, audit.Actor{Kind: "user", ID: st.UserID.String()}, c, st.Label, raw, exp)
}

// credential returns the header to send for an account, refreshing an OAuth token when it
// expires. Tokens are registered for redaction (§11.4).
func (s *Service) credential(ctx context.Context, c *Connector, a *ConnectorAccount) (name, value string, err error) {
	if c.Auth == "none" {
		return "", "", nil
	}
	sealed, err := base64.StdEncoding.DecodeString(a.TokenCiphertext)
	if err != nil {
		return "", "", err
	}
	raw, err := s.vault.Open(ctx, c.SpaceID, sealed, aad(a.ID()))
	if err != nil {
		return "", "", err
	}
	if c.Auth == "header" {
		s.vault.Redactor.Register(c.SpaceID, c.Name+" key", string(raw))
		return c.HeaderName, string(raw), nil
	}
	var t token
	if err := json.Unmarshal(raw, &t); err != nil {
		return "", "", err
	}
	if !t.Expiry.IsZero() && s.now().Add(time.Minute).After(t.Expiry) && t.Refresh != "" {
		octx := context.WithValue(ctx, oauth2.HTTPClient, s.http)
		nt, err := c.oauthConfig().TokenSource(octx, &oauth2.Token{RefreshToken: t.Refresh, Expiry: time.Unix(1, 0)}).Token()
		if err != nil {
			return "", "", fmt.Errorf("the connector sign-in expired; connect the account again: %w", err)
		}
		t = token{Access: nt.AccessToken, Refresh: nt.RefreshToken, Expiry: nt.Expiry}
		raw, _ = json.Marshal(t)
		resealed, err := s.vault.Seal(ctx, c.SpaceID, raw, aad(a.ID()))
		if err != nil {
			return "", "", err
		}
		if _, err := s.e.Exec(ctx, "UPDATE connector_accounts SET token_ciphertext = $1, expires_at = $2, updated_at = $3 WHERE id = $4", base64.StdEncoding.EncodeToString(resealed), nt.Expiry.UTC(), s.now(), a.ID()); err != nil {
			return "", "", err
		}
	}
	s.vault.Redactor.Register(c.SpaceID, c.Name+" token", t.Access)
	if t.Refresh != "" {
		s.vault.Redactor.Register(c.SpaceID, c.Name+" refresh token", t.Refresh)
	}
	return "Authorization", "Bearer " + t.Access, nil
}

type headerTransport struct {
	base        http.RoundTripper
	name, value string
}

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if h.name != "" {
		r = r.Clone(r.Context())
		r.Header.Set(h.name, h.value)
	}
	return h.base.RoundTrip(r)
}

// session connects to the connector of an account. botID picks the zone computer for stdio.
func (s *Service) session(ctx context.Context, c *Connector, a *ConnectorAccount, botID uuid.UUID) (*mcp.ClientSession, error) {
	cl := mcp.NewClient(&mcp.Implementation{Name: "fettle", Version: "1"}, nil)
	if c.Kind == "mcp_stdio" {
		if s.Stdio == nil {
			return nil, errors.New("stdio connectors need a computer")
		}
		conn, err := s.Stdio(ctx, c.SpaceID, botID, c.Command)
		if err != nil {
			return nil, err
		}
		return cl.Connect(ctx, &mcp.IOTransport{Reader: conn, Writer: conn}, nil)
	}
	name, value, err := s.credential(ctx, c, a)
	if err != nil {
		return nil, err
	}
	base := s.http.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	hc := &http.Client{Transport: headerTransport{base: base, name: name, value: value}, Timeout: 2 * time.Minute}
	return cl.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.URL, HTTPClient: hc, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
}

// ToolInfo is one connector tool with its class.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Class       string          `json:"class"`
	Disabled    bool            `json:"disabled"`
	Schema      json.RawMessage `json:"schema"`
	ReadOnly    bool            `json:"read_only_hint"`
	Destructive bool            `json:"destructive_hint"`
}

// Tools lists the tools of a connector through one account, with classes (§12.1, Test).
func (s *Service) Tools(ctx context.Context, c *Connector, a *ConnectorAccount, botID uuid.UUID) ([]ToolInfo, error) {
	cs, err := s.session(ctx, c, a, botID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cs.Close() }()
	res, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, err
	}
	out := make([]ToolInfo, 0, len(res.Tools))
	for _, t := range res.Tools {
		set := c.Tools[t.Name]
		schema, _ := json.Marshal(t.InputSchema)
		ti := ToolInfo{Name: t.Name, Description: t.Description, Class: Classify(t, set), Disabled: set.Disabled, Schema: schema}
		if t.Annotations != nil {
			ti.ReadOnly = t.Annotations.ReadOnlyHint
			ti.Destructive = t.Annotations.DestructiveHint != nil && *t.Annotations.DestructiveHint
		}
		out = append(out, ti)
	}
	return out, nil
}

// Call runs one tool and returns its text output.
func (s *Service) Call(ctx context.Context, c *Connector, a *ConnectorAccount, botID uuid.UUID, tool string, args json.RawMessage) (string, error) {
	cs, err := s.session(ctx, c, a, botID)
	if err != nil {
		return "", err
	}
	defer func() { _ = cs.Close() }()
	var in map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: in})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, ct := range res.Content {
		if t, ok := ct.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
			b.WriteString("\n")
		}
	}
	text := strings.TrimSpace(b.String())
	if res.IsError {
		return "", errors.New(text)
	}
	return text, nil
}
