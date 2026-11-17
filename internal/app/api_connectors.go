package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	catalog "github.com/alternayte/fettle/connectors"
	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/connectors"
)

// ConnectorView is a connector with its accounts, without credentials.
type ConnectorView struct {
	ID         string                            `json:"id"`
	Name       string                            `json:"name"`
	Kind       string                            `json:"kind" enum:"mcp_remote,mcp_stdio,composio"`
	URL        string                            `json:"url,omitempty"`
	Command    []string                          `json:"command"`
	Auth       string                            `json:"auth" enum:"none,oauth,header"`
	HeaderName string                            `json:"header_name,omitempty"`
	Icon       string                            `json:"icon"`
	Tools      map[string]connectors.ToolSetting `json:"tools"`
	Env        map[string]string                 `json:"env" doc:"Plain variables of a stdio connector."`
	Variables  []catalog.Variable                `json:"variables" doc:"Variables that the stdio server reads, from its catalogue entry."`
	Accounts   []AccountView                     `json:"accounts"`
}

// AccountView is one connector account. Zones get accounts as grants. It never holds a key
// or a secret value.
type AccountView struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Zones       []string `json:"zones" doc:"Names of the zones that hold this account."`
	SecretEnv   []string `json:"secret_env" doc:"Names of the secret variables that are set. The API never returns their values."`
	NeedsSignIn bool     `json:"needs_sign_in" doc:"True when the connector URL changed and the account must sign in again."`
}

type ConnectorPath struct {
	Space     uuid.UUID `path:"space" doc:"Space ID"`
	Connector uuid.UUID `path:"connector"`
}

func connectorError(err error) error {
	switch {
	case errors.Is(err, connectors.ErrNotFound), errors.Is(err, connectors.ErrNoAccount):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, connectors.ErrNameTaken):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, connectors.ErrBadInput), errors.Is(err, connectors.ErrNoDiscovery), errors.Is(err, connectors.ErrInvalid),
		errors.Is(err, connectors.ErrMissingVar), errors.Is(err, connectors.ErrSignIn):
		return huma.Error422UnprocessableEntity(err.Error())
	}
	return err
}

func accountView(c *connectors.Connector, acc *connectors.ConnectorAccount) AccountView {
	av := AccountView{ID: acc.ID().String(), Label: acc.Label, Zones: []string{}, SecretEnv: acc.SecretEnvNames, NeedsSignIn: c.Auth == "oauth" && acc.TokenCiphertext == ""}
	if av.SecretEnv == nil {
		av.SecretEnv = []string{}
	}
	return av
}

func (a *App) connectorView(ctx context.Context, c *connectors.Connector) ConnectorView {
	v := ConnectorView{ID: c.ID().String(), Name: c.Name, Kind: c.Kind, URL: c.URL, Command: c.Command, Auth: c.Auth, HeaderName: c.HeaderName, Icon: c.Icon, Tools: c.Tools,
		Env: c.Env, Variables: c.Variables, Accounts: []AccountView{}}
	if v.Command == nil {
		v.Command = []string{}
	}
	if v.Env == nil {
		v.Env = map[string]string{}
	}
	if v.Variables == nil {
		v.Variables = []catalog.Variable{}
	}
	accs, _ := a.Connectors.Accounts(ctx, c.ID())
	zs, _ := a.Zones.List(ctx, c.SpaceID)
	for _, acc := range accs {
		av := accountView(c, acc)
		for _, z := range zs {
			if ok, _ := a.Zones.HasGrant(ctx, z.ID(), "connector_account", acc.ID()); ok {
				av.Zones = append(av.Zones, z.Name)
			}
		}
		v.Accounts = append(v.Accounts, av)
	}
	return v
}

// oauthCallback ends a connector sign-in. The state is the secret that ties the code to
// the person who started it.
func (a *App) oauthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		http.Redirect(w, r, "/settings/connectors?error="+url.QueryEscape(e), http.StatusFound)
		return
	}
	acc, err := a.Connectors.Callback(r.Context(), q.Get("state"), q.Get("code"))
	if err != nil {
		http.Redirect(w, r, "/settings/connectors?error="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/settings/connectors?connected="+acc.ConnectorID.String(), http.StatusFound)
}

func (a *App) registerConnectors(api huma.API) {
	huma.Register(api, op("connector-catalog", http.MethodGet, "/connectors/catalog", "Known MCP servers for the Connect screen, with the variables of stdio servers", "connectors"), func(ctx context.Context, _ *struct{}) (*struct {
		Body struct {
			Connectors []catalog.Entry `json:"connectors"`
		}
	}, error) {
		entries, err := catalog.Catalog()
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Connectors []catalog.Entry `json:"connectors"`
			}
		}{}
		out.Body.Connectors = entries
		return out, nil
	})

	type listOut struct {
		Body struct {
			Connectors []ConnectorView `json:"connectors"`
		}
	}
	huma.Register(api, op("list-connectors", http.MethodGet, "/spaces/{space}/connectors", "List the connectors of a space", "connectors"), func(ctx context.Context, in *SpacePath) (*listOut, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		cs, err := a.Connectors.List(ctx, in.Space)
		if err != nil {
			return nil, err
		}
		out := &listOut{}
		out.Body.Connectors = []ConnectorView{}
		for _, c := range cs {
			out.Body.Connectors = append(out.Body.Connectors, a.connectorView(ctx, c))
		}
		return out, nil
	})

	huma.Register(api, op("create-connector", http.MethodPost, "/spaces/{space}/connectors", "Add an MCP connector. An OAuth connector registers Fettle at its sign-in server.", "connectors"), func(ctx context.Context, in *struct {
		SpacePath
		Body struct {
			Name       string             `json:"name" minLength:"1" maxLength:"80"`
			Kind       string             `json:"kind,omitempty" enum:"mcp_remote,mcp_stdio"`
			URL        string             `json:"url,omitempty" maxLength:"500"`
			Command    []string           `json:"command,omitempty" maxItems:"40"`
			Auth       string             `json:"auth,omitempty" enum:"none,oauth,header"`
			HeaderName string             `json:"header_name,omitempty" maxLength:"100"`
			Icon       string             `json:"icon,omitempty" maxLength:"40"`
			Env        map[string]string  `json:"env,omitempty" doc:"Plain variables of a stdio connector. Put secrets on the account as secret_env."`
			Variables  []catalog.Variable `json:"variables,omitempty" maxItems:"40" doc:"Variables that the stdio server reads, as the catalogue entry names them."`
		}
	}) (*struct{ Body ConnectorView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		c, err := a.Connectors.Create(ctx, actorOf(p), in.Space, connectors.Input{Name: in.Body.Name, Kind: in.Body.Kind, URL: in.Body.URL, Command: in.Body.Command, Auth: in.Body.Auth,
			HeaderName: in.Body.HeaderName, Icon: in.Body.Icon, Env: in.Body.Env, Variables: in.Body.Variables})
		if err != nil {
			return nil, connectorError(err)
		}
		return &struct{ Body ConnectorView }{Body: a.connectorView(ctx, c)}, nil
	})

	huma.Register(api, op("update-connector", http.MethodPatch, "/spaces/{space}/connectors/{connector}", "Edit a connector. Accounts, grants and tool settings stay. The kind cannot change. A new URL of an OAuth connector needs a new sign-in of each account.", "connectors"), func(ctx context.Context, in *struct {
		ConnectorPath
		Body struct {
			Name       *string           `json:"name,omitempty" minLength:"1" maxLength:"80"`
			Icon       *string           `json:"icon,omitempty" maxLength:"40"`
			URL        *string           `json:"url,omitempty" maxLength:"500" doc:"Remote connectors only."`
			Command    []string          `json:"command,omitempty" maxItems:"40" doc:"Stdio connectors only."`
			Env        map[string]string `json:"env,omitempty" doc:"Stdio connectors only. Replaces all plain variables."`
			HeaderName *string           `json:"header_name,omitempty" maxLength:"100" doc:"Connectors with a header key only."`
		}
	}) (*struct{ Body ConnectorView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		c, err := a.Connectors.Update(ctx, actorOf(p), in.Space, in.Connector, connectors.Patch{Name: in.Body.Name, Icon: in.Body.Icon, URL: in.Body.URL, Command: in.Body.Command, Env: in.Body.Env, HeaderName: in.Body.HeaderName})
		if err != nil {
			return nil, connectorError(err)
		}
		return &struct{ Body ConnectorView }{Body: a.connectorView(ctx, c)}, nil
	})

	huma.Register(api, op("update-connector-account", http.MethodPatch, "/spaces/{space}/connectors/{connector}/accounts/{account}", "Rename an account, or replace its key or secret variables. The account keeps its grants.", "connectors"), func(ctx context.Context, in *struct {
		ConnectorPath
		Account uuid.UUID `path:"account"`
		Body    struct {
			Label     *string           `json:"label,omitempty" minLength:"1" maxLength:"80"`
			Value     *string           `json:"value,omitempty" minLength:"1" maxLength:"4000" doc:"Write only. The new header key."`
			SecretEnv map[string]string `json:"secret_env,omitempty" doc:"Write only. Sets each named secret variable; an empty value removes it."`
		}
	}) (*struct{ Body AccountView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		c, err := a.Connectors.Get(ctx, in.Space, in.Connector)
		if err != nil {
			return nil, connectorError(err)
		}
		if acc, err := a.Connectors.Account(ctx, in.Space, in.Account); err != nil || acc.ConnectorID != c.ID() {
			return nil, huma.Error404NotFound("account not found")
		}
		acc, err := a.Connectors.UpdateAccount(ctx, actorOf(p), in.Space, in.Account, connectors.AccountPatch{Label: in.Body.Label, Value: in.Body.Value, SecretEnv: in.Body.SecretEnv})
		if err != nil {
			return nil, connectorError(err)
		}
		av := accountView(c, acc)
		zs, _ := a.Zones.List(ctx, in.Space)
		for _, z := range zs {
			if ok, _ := a.Zones.HasGrant(ctx, z.ID(), "connector_account", acc.ID()); ok {
				av.Zones = append(av.Zones, z.Name)
			}
		}
		return &struct{ Body AccountView }{Body: av}, nil
	})

	huma.Register(api, op("add-composio", http.MethodPost, "/spaces/{space}/connectors/composio", "Add Composio's MCP endpoint with a Composio key", "connectors"), func(ctx context.Context, in *struct {
		SpacePath
		Body struct {
			Key string `json:"key" minLength:"8" maxLength:"500"`
		}
	}) (*struct{ Body ConnectorView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		u := a.Settings.String(ctx, nil, "connectors.composio_url")
		c, err := a.Connectors.Create(ctx, actorOf(p), in.Space, connectors.Input{Name: "Composio", Kind: "composio", URL: u, Auth: "header", HeaderName: "x-api-key", Icon: "plug"})
		if err != nil {
			return nil, connectorError(err)
		}
		if _, err := a.Connectors.AddKeyAccount(ctx, actorOf(p), in.Space, c.ID(), "Composio", in.Body.Key, nil); err != nil {
			return nil, connectorError(err)
		}
		return &struct{ Body ConnectorView }{Body: a.connectorView(ctx, c)}, nil
	})

	huma.Register(api, op("delete-connector", http.MethodDelete, "/spaces/{space}/connectors/{connector}", "Delete a connector, its accounts and their grants", "connectors"), func(ctx context.Context, in *ConnectorPath) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		return nil, connectorError(a.Connectors.Delete(ctx, actorOf(p), in.Space, in.Connector))
	})

	huma.Register(api, op("add-connector-account", http.MethodPost, "/spaces/{space}/connectors/{connector}/accounts", "Add an account with a header key, or a stdio account with secret variables", "connectors"), func(ctx context.Context, in *struct {
		ConnectorPath
		Body struct {
			Label     string            `json:"label" minLength:"1" maxLength:"80"`
			Value     string            `json:"value,omitempty" maxLength:"4000" doc:"Write only. The API never returns it."`
			SecretEnv map[string]string `json:"secret_env,omitempty" doc:"Write only. Secret variables of a stdio account. The API returns only their names."`
		}
	}) (*struct{ Body AccountView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		acc, err := a.Connectors.AddKeyAccount(ctx, actorOf(p), in.Space, in.Connector, in.Body.Label, in.Body.Value, in.Body.SecretEnv)
		if err != nil {
			return nil, connectorError(err)
		}
		c, err := a.Connectors.Get(ctx, in.Space, in.Connector)
		if err != nil {
			return nil, connectorError(err)
		}
		return &struct{ Body AccountView }{Body: accountView(c, acc)}, nil
	})

	huma.Register(api, op("authorize-connector", http.MethodPost, "/spaces/{space}/connectors/{connector}/authorize", "Start an OAuth sign-in for a new account, or for an account that must sign in again; open the returned URL", "connectors"), func(ctx context.Context, in *struct {
		ConnectorPath
		Body struct {
			Label     string `json:"label" minLength:"1" maxLength:"80"`
			AccountID string `json:"account_id,omitempty" doc:"The account that signs in again. It keeps its grants."`
		}
	}) (*struct {
		Body struct {
			URL string `json:"url"`
		}
	}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		var accountID *uuid.UUID
		if in.Body.AccountID != "" {
			id, err := uuid.Parse(in.Body.AccountID)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("account_id is not valid")
			}
			accountID = &id
		}
		u, err := a.Connectors.StartAuthorize(ctx, actorOf(p), in.Space, in.Connector, auth.From(ctx).UserID, in.Body.Label, accountID)
		if err != nil {
			return nil, connectorError(err)
		}
		out := &struct {
			Body struct {
				URL string `json:"url"`
			}
		}{}
		out.Body.URL = u
		return out, nil
	})

	huma.Register(api, op("test-connector", http.MethodPost, "/spaces/{space}/connectors/{connector}/test", "List the tools of a connector with their classes, through one account", "connectors"), func(ctx context.Context, in *struct {
		ConnectorPath
		Body struct {
			AccountID uuid.UUID `json:"account_id"`
			BotID     string    `json:"bot_id,omitempty" doc:"For a stdio connector: the bot whose zone computer runs it. Default: the primary bot."`
		}
	}) (*struct {
		Body struct {
			Tools []connectors.ToolInfo `json:"tools"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		c, err := a.Connectors.Get(ctx, in.Space, in.Connector)
		if err != nil {
			return nil, connectorError(err)
		}
		acc, err := a.Connectors.Account(ctx, in.Space, in.Body.AccountID)
		if err != nil || acc.ConnectorID != c.ID() {
			return nil, huma.Error404NotFound("account not found")
		}
		bot := uuid.Nil
		if in.Body.BotID != "" {
			if bot, err = uuid.Parse(in.Body.BotID); err != nil {
				return nil, huma.Error422UnprocessableEntity("bot_id is not valid")
			}
		} else if pb, err := a.Bots.Primary(ctx, in.Space); err == nil {
			bot = pb.ID()
		}
		tools, err := a.Connectors.Tools(ctx, c, acc, bot)
		if errors.Is(err, connectors.ErrMissingVar) || errors.Is(err, connectors.ErrSignIn) {
			return nil, connectorError(err)
		}
		if err != nil {
			return nil, huma.Error502BadGateway("The connector did not answer: " + a.Vault.Redactor.String(ctx, in.Space, err.Error()))
		}
		out := &struct {
			Body struct {
				Tools []connectors.ToolInfo `json:"tools"`
			}
		}{}
		out.Body.Tools = tools
		return out, nil
	})

	huma.Register(api, op("set-connector-tool", http.MethodPut, "/spaces/{space}/connectors/{connector}/tools/{tool}", "Change the class of a connector tool, or turn it off. It applies to the next run.", "connectors"), func(ctx context.Context, in *struct {
		ConnectorPath
		Tool string `path:"tool"`
		Body struct {
			Class    string `json:"class,omitempty" enum:"read,write_local,write_external,send,admin"`
			Disabled bool   `json:"disabled"`
		}
	}) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		return nil, connectorError(a.Connectors.SetTool(ctx, actorOf(p), in.Space, in.Connector, in.Tool, connectors.ToolSetting{Class: in.Body.Class, Disabled: in.Body.Disabled}))
	})
}
