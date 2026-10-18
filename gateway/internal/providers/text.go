package providers

import (
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"strings"
	"unicode/utf8"
)

const TextKnowledgeCard = `# Configured LLM document generation
Capability text:configured-llm uses the running, frozen LLM connection in an independent tools-disabled native session. Exactly one document output; params and references must be empty. Output tokens are bounded by the configured output limit. Quotes conservatively reserve context-limit tokens at the user-declared upper per-token price (including input/output); they are estimates, not measured supplier charges. Missing pricing is not free. Unknown results only permit query/recovery of the original task; never issue another paid request automatically. Cancellation cannot guarantee supplier cancellation. Document output is staged and delivered as an immutable Version on Canvas. Native tools, paths, model overrides, temperature and arbitrary provider options are unsupported.`

func KnowledgeCard(operation string) string {
	if operation == "text" {
		return TextKnowledgeCard
	}
	return ImageKnowledgeCard
}

func TextProvider(raw json.RawMessage, version string) (FrozenProvider, error) {
	c, e := store.DecodeValue[struct {
		Protocol string   `json:"protocol"`
		BaseURL  string   `json:"base_url"`
		ModelID  string   `json:"model_id"`
		Context  int      `json:"context"`
		Output   int      `json:"output"`
		Pricing  *Pricing `json:"pricing,omitempty"`
	}](raw)
	if e != nil {
		return FrozenProvider{}, invalid("Invalid LLM snapshot")
	}
	f := FrozenProvider{Config: Config{ID: "configured-llm", Name: "Configured LLM", Kind: "llm", Protocol: c.Protocol, BaseURL: c.BaseURL, ModelID: c.ModelID, Enabled: true, Pricing: c.Pricing, TextContext: c.Context, TextOutput: c.Output, Capability: Capability{MaxPromptBytes: min(65536, c.Context-c.Output), MaxCount: 1}}, CredentialVersion: version}
	return f, ValidateConfig(f.Config)
}
func TextCapability(c Config) Capability {
	return Capability{ID: "text:configured-llm", Operation: "text", Protocol: c.Protocol, ModelID: c.ModelID, KnowledgeCard: "knowledge/providers/text.md", MaxPromptBytes: min(65536, c.TextContext-c.TextOutput), MaxCount: 1, ParamSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
}
func ValidateText(in GenerationInput, f FrozenProvider) error {
	if f.Config.Kind != "llm" || len(in.Params) != 0 || len(in.References) != 0 || len(in.Assets) != 0 || in.Count != 1 || strings.TrimSpace(in.Prompt) == "" || len(in.Prompt) > min(65536, f.Config.TextContext-f.Config.TextOutput) || !utf8.ValidString(in.Prompt) {
		return invalid("Text requires one output, exact bounded prompt, empty params and references; native options/tools are unsupported")
	}
	return nil
}
