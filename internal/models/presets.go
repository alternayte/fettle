//go:build !drelgen

package models

// Preset fills the provider form (§8.3).
type Preset struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	APIType    string `json:"api_type"`
	BaseURL    string `json:"base_url"`
	AuthStyle  string `json:"auth_style"`
	AuthHeader string `json:"auth_header,omitempty"`
	NeedsKey   bool   `json:"needs_key"`
	// DefaultModel is a suggested owner model.
	DefaultModel string `json:"default_model,omitempty"`
}

// Presets lists the provider presets. OpenRouter is the default suggestion.
var Presets = []Preset{
	{ID: "openrouter", Name: "OpenRouter", APIType: APIOpenAIChat, BaseURL: "https://openrouter.ai/api/v1", AuthStyle: "bearer", NeedsKey: true, DefaultModel: "anthropic/claude-sonnet-4.5"},
	{ID: "openai", Name: "OpenAI", APIType: APIOpenAIResponses, BaseURL: "https://api.openai.com/v1", AuthStyle: "bearer", NeedsKey: true, DefaultModel: "gpt-5"},
	{ID: "anthropic", Name: "Anthropic", APIType: APIAnthropic, BaseURL: "https://api.anthropic.com/v1", AuthStyle: "x_api_key", NeedsKey: true, DefaultModel: "claude-sonnet-4-5"},
	{ID: "google", Name: "Google", APIType: APIOpenAIChat, BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", AuthStyle: "bearer", NeedsKey: true, DefaultModel: "gemini-2.5-pro"},
	{ID: "deepseek", Name: "DeepSeek", APIType: APIOpenAIChat, BaseURL: "https://api.deepseek.com/v1", AuthStyle: "bearer", NeedsKey: true, DefaultModel: "deepseek-chat"},
	{ID: "ollama", Name: "Ollama", APIType: APIOpenAIChat, BaseURL: "http://127.0.0.1:11434/v1", AuthStyle: "none"},
	{ID: "lmstudio", Name: "LM Studio", APIType: APIOpenAIChat, BaseURL: "http://127.0.0.1:1234/v1", AuthStyle: "none"},
	{ID: "openai-compatible", Name: "OpenAI-compatible", APIType: APIOpenAIChat, AuthStyle: "bearer", NeedsKey: true},
}

// PresetByID returns a preset.
func PresetByID(id string) (Preset, bool) {
	for _, p := range Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
