package agent

import (
	"encoding/json"
	"os"
	"testing"
)

func TestConfigDeniesUnknownToolsAndOnlyAllowsExactMCPNames(t *testing.T) {
	c := LLMConfig{Protocol: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1", ModelID: "local-model", Context: 32000, Output: 4096}
	raw, err := BuildConfig(c, MCPConnection{URL: "http://127.0.0.1:8001/mcp", Origin: "http://tauri.localhost", Token: "synthetic-token", Tools: []string{"design_design_read"}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if json.Unmarshal(raw, &got) != nil {
		t.Fatal("invalid JSON")
	}
	p, ok := got["permission"].(map[string]any)
	if !ok || p["*"] != "deny" || p["question"] != "allow" || p["design_design_read"] != "allow" || p["design_*"] != nil {
		t.Fatal("tools must default deny with exact exceptions", string(raw))
	}
	for _, bad := range []string{"design_*", "bash", "design_question?", "design_design_read/evil"} {
		if _, err := BuildConfig(c, MCPConnection{URL: "http://127.0.0.1:8001/mcp", Origin: "http://tauri.localhost", Token: "token", Tools: []string{bad}}); err == nil {
			t.Fatalf("unsafe name accepted: %s", bad)
		}
	}
	for _, protocol := range []string{"npm:evil", "file:///plugin.js", "anthropic-compatible", ""} {
		bad := c
		bad.Protocol = protocol
		if _, err := BuildConfig(bad, MCPConnection{}); err == nil {
			t.Fatalf("unknown protocol accepted %s", protocol)
		}
	}
	for _, url := range []string{"http://127.0.0.1:4096", "http://169.254.169.254", "https://example.com/{env:SECRET}", "https://x/{file:secret}"} {
		bad := c
		bad.BaseURL = url
		if _, err := BuildConfig(bad, MCPConnection{}); err == nil {
			t.Fatalf("unsafe URL accepted %s", url)
		}
	}
	for _, id := range []string{"{env:OPENCODE_SERVER_PASSWORD}", "{file:private}"} {
		bad := c
		bad.ModelID = id
		if _, err := BuildConfig(bad, MCPConnection{}); err == nil {
			t.Fatal("model interpolation accepted")
		}
	}
	// The saved live /doc sample catches accidental unsupported top-level fields;
	// the opt-in runtime check validates nested configuration against the real host.
	sample, err := os.ReadFile("testdata/opencode-1.19.0-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Config struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"config"`
	}
	if err := json.Unmarshal(sample, &schema); err != nil {
		t.Fatal(err)
	}
	for field := range got {
		if schema.Config.Properties[field] == nil {
			t.Fatalf("field missing from pinned schema: %s", field)
		}
	}
}
