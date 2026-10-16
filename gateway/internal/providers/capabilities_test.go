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

func TestSchemaKeywordCaseExact(t *testing.T) {
	for _, property := range []string{
		`{"TYPE":"string","maxLength":128}`,
		`{"type":"string","MAXLENGTH":128}`,
		`{"type":"string","maxLength":1,"MAXLENGTH":128}`,
		`{"type":"string","maxLength":128,"ENUM":["a"]}`,
	} {
		t.Run(property, func(t *testing.T) {
			c := validConfig("case", "http://127.0.0.1:11434/v1")
			c.Capability.ParamSchema = json.RawMessage(`{"type":"object","properties":{"safety_identifier":` + property + `},"additionalProperties":false}`)
			if ValidateConfig(c) == nil {
				t.Fatal("case variant keyword accepted")
			}
		})
	}
}
func TestSchemaExactNumbers(t *testing.T) {
	cap := ImageCapability(validConfig("number", "http://127.0.0.1:11434/v1"))
	for _, token := range []string{"1.0000000000000001", "100.0000000000000001"} {
		t.Run("integer-"+token, func(t *testing.T) {
			if Validate(cap, GenerationInput{Prompt: "x", Count: 1, Params: map[string]any{"output_format": "jpeg", "output_compression": json.Number(token)}}) == nil {
				t.Fatal("fractional JSON integer accepted")
			}
		})
	}
	for _, token := range []string{"1", "1.0", "1e0"} {
		if e := Validate(cap, GenerationInput{Prompt: "x", Count: 1, Params: map[string]any{"output_format": "jpeg", "output_compression": json.Number(token)}}); e != nil {
			t.Fatal("valid integral spelling", token, e)
		}
	}
	for _, tc := range []struct {
		name, schema, value string
		valid               bool
	}{
		{"fractional-min", `{"type":"number","minimum":1.0000000000000001}`, "1", false},
		{"fractional-max", `{"type":"number","maximum":0.99999999999999999}`, "1", false},
		{"number-range", `{"type":"number","maximum":100}`, "100.0000000000000001", false},
		{"enum", `{"type":"number","enum":[1.0000000000000001]}`, "1", false},
		{"enum-equal", `{"type":"number","enum":[1.0000000000000001]}`, "1.0000000000000001", true},
		{"integer-fractional-min", `{"type":"integer","minimum":1.0000000000000001}`, "1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testCap := cap
			testCap.ParamSchema = json.RawMessage(`{"type":"object","properties":{"value":` + tc.schema + `},"additionalProperties":false}`)
			e := Validate(testCap, GenerationInput{Prompt: "x", Count: 1, Params: map[string]any{"value": json.Number(tc.value)}})
			if (e == nil) != tc.valid {
				t.Fatalf("exact numeric constraint: %v", e)
			}
		})
	}
	for _, bounds := range []string{`"minimum":1.0000000000000001,"maximum":1`, `"minimum":0,"maximum":100.0000000000000001`} {
		t.Run(bounds, func(t *testing.T) {
			c := validConfig("bounds", "http://127.0.0.1:11434/v1")
			c.Capability.ParamSchema = json.RawMessage(`{"type":"object","properties":{"output_compression":{"type":"integer",` + bounds + `}},"additionalProperties":false}`)
			if ValidateConfig(c) == nil {
				t.Fatal("invalid or widened fractional bound accepted")
			}
		})
	}
}

func TestSchemaNumericComplexityBound(t *testing.T) {
	cap := ImageCapability(validConfig("number", "http://127.0.0.1:11434/v1"))
	cap.ParamSchema = json.RawMessage(`{"type":"object","properties":{"value":{"type":"number"}},"additionalProperties":false}`)
	for _, token := range []string{"1e999999999", "1e-999999999", "0." + strings.Repeat("0", 1024) + "1"} {
		if Validate(cap, GenerationInput{Prompt: "x", Count: 1, Params: map[string]any{"value": json.Number(token)}}) == nil {
			t.Fatal("excessive numeric representation accepted")
		}
	}
}
