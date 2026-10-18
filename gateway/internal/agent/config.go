package agent

import (
	"encoding/json"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"regexp"
	"strings"
)

type LLMConfig struct {
	Protocol string `json:"protocol"`
	BaseURL  string `json:"base_url"`
	ModelID  string `json:"model_id"`
	Context  int    `json:"context"`
	Output   int    `json:"output"`
}
type MCPConnection struct {
	URL, Origin, Token string
	Tools              []string
}

var ErrInvalid = errors.New("invalid LLM configuration")

func Validate(c LLMConfig) error {
	if c.Protocol != "openai-compatible" && c.Protocol != "openai" && c.Protocol != "anthropic" {
		return ErrInvalid
	}
	if providers.ValidateBaseURL(c.BaseURL) != nil || strings.ContainsAny(c.BaseURL+c.ModelID, "{}\r\n") || strings.TrimSpace(c.ModelID) == "" || len(c.ModelID) > 256 || c.Context < 1024 || c.Context > 2000000 || c.Output < 1 || c.Output > c.Context {
		return ErrInvalid
	}
	return nil
}

// BuildConfig emits only verified bundled SDKs. Credentials enter child environment,
// never config files; interpolated user values are forbidden at the trust boundary.
func BuildConfig(c LLMConfig, m MCPConnection) ([]byte, error) {
	if c != (LLMConfig{}) {
		if err := Validate(c); err != nil {
			return nil, err
		}
	}
	permission := map[string]any{"*": "deny", "question": "allow"}
	external := map[string]string{"*": "deny", "{env:MOVIEDESIGN_TRUNCATE_GLOB}": "deny"}
	permission["external_directory"] = external
	config := map[string]any{"$schema": "https://opencode.ai/config.json", "permission": permission, "plugin": []string{}, "autoupdate": false, "share": "disabled", "lsp": false, "formatter": false, "watcher": map[string]any{"ignore": []string{"**"}}, "enabled_providers": []string{"moviedesign"}, "compaction": map[string]any{"auto": false}}
	agents := map[string]any{"build": map[string]any{"permission": permission}}
	for _, name := range []string{"plan", "general", "explore"} {
		agents[name] = map[string]any{"disable": true}
	}
	for _, name := range []string{"compaction", "summary", "title"} {
		agents[name] = map[string]any{"permission": map[string]any{"*": "deny", "external_directory": external}}
	}
	config["agent"] = agents
	if c != (LLMConfig{}) {
		npm := "@ai-sdk/" + c.Protocol
		config["model"] = "moviedesign/" + c.ModelID
		config["small_model"] = "moviedesign/" + c.ModelID
		config["provider"] = map[string]any{"moviedesign": map[string]any{"name": "MovieDesign", "npm": npm, "options": map[string]string{"baseURL": c.BaseURL, "apiKey": "{env:MOVIEDESIGN_LLM_KEY}"}, "models": map[string]any{c.ModelID: map[string]any{"name": c.ModelID, "limit": map[string]int{"context": c.Context, "output": c.Output}}}}}
	}
	if m.URL != "" {
		if m.URL != "http://127.0.0.1:8001/mcp" || (m.Origin != "http://tauri.localhost" && m.Origin != "http://localhost:1420") || m.Token == "" || strings.ContainsAny(m.Token, "{}\r\n") {
			return nil, ErrInvalid
		}
		for _, tool := range m.Tools {
			if !regexp.MustCompile(`^design_design_[a-z][a-z0-9_]*$`).MatchString(tool) {
				return nil, ErrInvalid
			}
			permission[tool] = "allow"
		}
		config["mcp"] = map[string]any{"design": map[string]any{"type": "remote", "url": m.URL, "oauth": false, "headers": map[string]string{"Origin": m.Origin, "Authorization": "Bearer " + m.Token}}}
	} else if len(m.Tools) > 0 || m.Token != "" || m.Origin != "" {
		return nil, ErrInvalid
	}
	return json.Marshal(config)
}
