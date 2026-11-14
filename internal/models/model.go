package models

import (
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// ModelInfo is one model that a provider offers.
type ModelInfo struct {
	ID            string  `json:"id"`
	ContextWindow int     `json:"context_window,omitempty"`
	PriceIn       float64 `json:"price_in,omitempty"`
	PriceOut      float64 `json:"price_out,omitempty"`
}

// Smoke is the result of the provider test.
type Smoke struct {
	Model  string        `json:"model"`
	Checks []CheckResult `json:"checks"`
	Vision bool          `json:"vision"`
	Tools  bool          `json:"tools"`
}

// CheckResult is one check of the provider test.
type CheckResult struct {
	Key    string `json:"key"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
	MS     int64  `json:"ms"`
}

// ModelProvider is a configured model provider of a space.
type ModelProvider struct {
	drel.Model[uuid.UUID] `db:"table=model_providers,unique_index=uq_model_providers_space_name[space_id,name]"`
	SpaceID               uuid.UUID         `db:"space_id,references=spaces,on_delete=cascade"`
	Name                  string            `db:"name"`
	Preset                string            `db:"preset"`
	APIType               string            `db:"api_type,check=api_type IN ('openai-chat','openai-responses','anthropic-messages')"`
	BaseURL               string            `db:"base_url"`
	AuthStyle             string            `db:"auth_style,check=auth_style IN ('bearer','x_api_key','header','none')"`
	AuthHeader            string            `db:"auth_header"`
	SecretItemID          *uuid.UUID        `db:"secret_item_id,references=vault_items,on_delete=set_null"`
	Headers               map[string]string `db:"headers"`
	Models                []ModelInfo       `db:"models"`
	Smoke                 Smoke             `db:"smoke"`
	SmokeAt               *time.Time        `db:"smoke_at"`
}

// Target is one ordered choice of a role.
type Target struct {
	ProviderID    uuid.UUID `json:"provider_id"`
	Model         string    `json:"model"`
	Params        string    `json:"params,omitempty"`
	PriceIn       float64   `json:"price_in,omitempty"`
	PriceOut      float64   `json:"price_out,omitempty"`
	ContextWindow int       `json:"context_window,omitempty"`
}

// ModelRole holds ordered targets for one role, at space level (BotID nil) or bot level.
type ModelRole struct {
	drel.Model[uuid.UUID] `db:"table=model_roles,unique_index=uq_model_roles[space_id,bot_key,role]"`
	SpaceID               uuid.UUID  `db:"space_id,references=spaces,on_delete=cascade"`
	BotID                 *uuid.UUID `db:"bot_id"`
	BotKey                string     `db:"bot_key"`
	Role                  string     `db:"role,check=role IN ('router','owner','verifier','computer_use','summarizer')"`
	Targets               []Target   `db:"targets"`
}
