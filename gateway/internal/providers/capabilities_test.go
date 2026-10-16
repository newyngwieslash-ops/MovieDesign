package providers

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestUnknownParameterRejected(t *testing.T) {
	cap := ImageCapability(validConfig("one", "http://127.0.0.1:11434/v1"))
	in := GenerationInput{Prompt: "保持原文\n", Params: map[string]any{}, Count: 1}
	if err := Validate(cap, in); err != nil {
		t.Fatal(err)
	}
	for _, params := range []map[string]any{{"undeclared": true}, {"model": "override"}, {"size": map[string]any{"nested": true}}, {"quality": true}, {"output_compression": math.NaN()}, {"output_compression": 1.5}, {"output_compression": 101}} {
		in.Params = params
		if Validate(cap, in) == nil {
			t.Fatalf("accepted %#v", params)
		}
	}
	in.Params = map[string]any{}
	for _, ref := range []string{"https://example.com/a.png", "C:/file.png", "../a", "data:image/png;base64,aa"} {
		in.References = []string{ref}
		if Validate(cap, in) == nil {
			t.Fatal("nonasset accepted", ref)
		}
	}
	in.References = nil
	in.Prompt = strings.Repeat("界", 21846)
	if Validate(cap, in) == nil {
		t.Fatal("UTF8 byte ceiling ignored")
	}
	in.Prompt = strings.Repeat("x", 32001)
	if Validate(cap, in) == nil {
		t.Fatal("protocol character ceiling ignored")
	}
}
func TestUnknownParameterSchemaRejected(t *testing.T) {
	for _, schema := range []string{
		`{"type":"object","properties":{"arbitrary":{"type":"string"}},"additionalProperties":false}`,
		`{"type":"object","properties":{"quality":{"type":"string","pattern":".*"}},"additionalProperties":false}`,
		`{"type":"object","properties":{},"additionalProperties":true}`,
		`{"type":"object","properties":{"output_compression":{"type":"number","minimum":-1}},"additionalProperties":false}`,
		`{"type":"object","properties":{"quality":{"type":"boolean"}},"additionalProperties":false}`,
		`{"type":"object","properties":{"quality":{"type":"string","enum":["unsupported"]}},"additionalProperties":false}`,
		`{"type":"object","properties":{},"required":["missing"],"additionalProperties":false}`,
		`{"type":"object","properties":{},"required":null,"additionalProperties":false}`,
		`{"type":"object","properties":{"quality":{"type":"string","enum":null}},"additionalProperties":false}`,
		`{"type":"object","properties":{"safety_identifier":{"type":"string","maxLength":null}},"additionalProperties":false}`,
		`{"type":"object","properties":{"output_compression":{"type":"integer","minimum":null,"maximum":100}},"additionalProperties":false}`,
	} {
		c := validConfig("one", "http://127.0.0.1:11434/v1")
		c.Capability.ParamSchema = json.RawMessage(schema)
		if ValidateConfig(c) == nil {
			t.Fatal("unsupported schema accepted", schema)
		}
	}
	c := validConfig("one", "http://127.0.0.1:11434/v1")
	c.Capability.ParamSchema = json.RawMessage(`{"type":"object","properties":{"quality":{"type":"string","enum":["high"]}},"required":["quality"],"additionalProperties":false}`)
	if err := ValidateConfig(c); err != nil {
		t.Fatal(err)
	}
	cap := ImageCapability(c)
	if Validate(cap, GenerationInput{Prompt: "x", Count: 1, Params: map[string]any{}}) == nil {
		t.Fatal("required skipped")
	}
	if err := Validate(cap, GenerationInput{Prompt: "x", Count: 1, Params: map[string]any{"quality": "high"}}); err != nil {
		t.Fatal(err)
	}
}
func TestSchemaPrimitiveBounds(t *testing.T) {
	cap := Capability{Protocol: "openai-images", Operation: "image", MaxPromptBytes: 65536, MaxCount: 1, ParamSchema: json.RawMessage(`{"type":"object","properties":{"enabled":{"type":"boolean","enum":[false]},"length":{"type":"string","minLength":1,"maxLength":2},"fraction":{"type":"number","minimum":0.1,"maximum":0.3}},"additionalProperties":false}`)}
	in := GenerationInput{Prompt: "x", Count: 1, Params: map[string]any{"enabled": false, "length": "界", "fraction": json.Number("0.2")}}
	if err := Validate(cap, in); err != nil {
		t.Fatal(err)
	}
	for _, p := range []map[string]any{{"enabled": "false"}, {"enabled": true}, {"length": ""}, {"length": "abc"}, {"fraction": math.Inf(1)}} {
		in.Params = p
		if Validate(cap, in) == nil {
			t.Fatal("invalid primitive", p)
		}
	}
}
