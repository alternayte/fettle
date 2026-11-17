package connectors

import (
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	catalog "github.com/alternayte/fettle/connectors"
)

// OAuthClient is the dynamic registration of Fettle at a connector's authorization server.
type OAuthClient struct {
	Issuer       string `json:"issuer"`
	AuthorizeURL string `json:"authorization_endpoint"`
	TokenURL     string `json:"token_endpoint"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret_sealed,omitempty"` // sealed, base64
	// PlainSecret is a client secret from before secrets were sealed. A migration moves it
	// here, and SealClientSecrets seals it into ClientSecret at start.
	PlainSecret string `json:"client_secret_plain,omitempty"`
	Resource    string `json:"resource,omitempty"`
	Scopes      string `json:"scopes,omitempty"`
	Registered  bool   `json:"registered"`
	RedirectURI string `json:"redirect_uri"`
}

// ToolSetting is an owner's change to one connector tool (§12.1).
type ToolSetting struct {
	Class    string `json:"class,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// Connector is an MCP server that bots reach through its accounts (§12).
type Connector struct {
	drel.Model[uuid.UUID] `db:"unique_index=uq_connectors_space_name[space_id,name]"`
	SpaceID               uuid.UUID              `db:"space_id,references=spaces,on_delete=cascade"`
	Name                  string                 `db:"name"`
	Kind                  string                 `db:"kind,check=kind IN ('mcp_remote','mcp_stdio','composio')"`
	URL                   string                 `db:"url"`
	Command               []string               `db:"command"`
	Auth                  string                 `db:"auth,check=auth IN ('none','oauth','header')"`
	HeaderName            string                 `db:"header_name"`
	OAuth                 OAuthClient            `db:"oauth"`
	Tools                 map[string]ToolSetting `db:"tools"`
	Icon                  string                 `db:"icon"`
	// Env holds the plain variables of a stdio connector. Account secrets win over them.
	Env map[string]string `db:"env"`
	// Variables are the variables that the stdio server reads, from its catalogue entry.
	Variables []catalog.Variable `db:"variables"`
}

// ConnectorAccount is one login at a connector. Zones get accounts as grants.
type ConnectorAccount struct {
	drel.Model[uuid.UUID] `db:"index=idx_connector_accounts_connector[connector_id]"`
	ConnectorID           uuid.UUID `db:"connector_id,references=connectors,on_delete=cascade"`
	SpaceID               uuid.UUID `db:"space_id"`
	Label                 string    `db:"label"`
	// TokenCiphertext is the sealed credential: OAuth tokens as JSON, the header value, or
	// the secret variables of a stdio account as JSON. It is empty when an OAuth account
	// must sign in again.
	TokenCiphertext string     `db:"token_ciphertext"`
	Scopes          string     `db:"scopes"`
	ExpiresAt       *time.Time `db:"expires_at"`
	// SecretEnvNames are the names of the sealed secret variables, which the API shows.
	SecretEnvNames []string `db:"secret_env_names"`
}

// OAuthState is one pending OAuth authorization (PKCE verifier and where it goes).
type OAuthState struct {
	drel.Model[uuid.UUID] `db:"table=oauth_states"`
	State                 string    `db:"state,unique"`
	ConnectorID           uuid.UUID `db:"connector_id,references=connectors,on_delete=cascade"`
	Label                 string    `db:"label"`
	// AccountID is the account that signs in again, or nil for a new account.
	AccountID *uuid.UUID `db:"account_id"`
	Verifier  string     `db:"verifier"` // sealed, base64
	UserID    uuid.UUID  `db:"user_id"`
	ExpiresAt time.Time  `db:"expires_at"`
}
