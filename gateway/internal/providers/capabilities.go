package providers

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

type Constraints struct {
	MaxPromptCharacters int   `json:"max_prompt_characters,omitempty"`
	MaxImageBytes       int64 `json:"max_image_bytes,omitempty"`
	MaxPixels           int64 `json:"max_pixels,omitempty"`
}
type GenerationInput struct {
	ProjectID  string          `json:"project_id"`
	Prompt     string          `json:"prompt"`
	Params     map[string]any  `json:"params"`
	References []string        `json:"references"`
	Count      int             `json:"count"`
	Output     json.RawMessage `json:"output"`
	// Trusted admission binds real owned media; HTTP never decodes these fields.
	Assets []ReferenceAsset `json:"assets,omitempty"`
}
type ReferenceAsset struct {
	Asset  store.Asset `json:"asset"`
	Role   string      `json:"role"`
	SHA256 string      `json:"sha256"`
	Width  int         `json:"width"`
	Height int         `json:"height"`
}
type parameter struct {
	Type      string   `json:"type"`
	Enum      []any    `json:"enum,omitempty"`
	Minimum   *float64 `json:"minimum,omitempty"`
	Maximum   *float64 `json:"maximum,omitempty"`
	MinLength *int     `json:"minLength,omitempty"`
	MaxLength *int     `json:"maxLength,omitempty"`
}
type parameterSchema struct {
	Type                 string               `json:"type"`
	Properties           map[string]parameter `json:"properties"`
	Required             []string             `json:"required,omitempty"`
	AdditionalProperties *bool                `json:"additionalProperties"`
}

func parseSchema(raw json.RawMessage) (parameterSchema, error) {
	// Explicit null is not an omitted JSON Schema keyword.
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		for _, value := range object {
			if string(value) == "null" {
				return parameterSchema{}, invalid("Schema keywords cannot be null")
			}
		}
		var properties map[string]map[string]json.RawMessage
		if json.Unmarshal(object["properties"], &properties) == nil {
			for _, p := range properties {
				if p == nil {
					return parameterSchema{}, invalid("Property schema must be an object")
				}
				for _, value := range p {
					if string(value) == "null" {
						return parameterSchema{}, invalid("Schema keywords cannot be null")
					}
				}
			}
		}
	}
	s, e := store.DecodeValue[parameterSchema](raw)
	if e != nil || s.Type != "object" || s.Properties == nil || s.AdditionalProperties == nil || *s.AdditionalProperties {
		return s, invalid("Expected restricted object parameter schema")
	}
	seen := map[string]bool{}
	for _, k := range s.Required {
		if _, ok := s.Properties[k]; !ok || seen[k] {
			return s, invalid("Invalid required parameter")
		}
		seen[k] = true
	}
	for _, p := range s.Properties {
		if !slices.Contains([]string{"string", "number", "integer", "boolean"}, p.Type) {
			return s, invalid("Unsupported parameter type")
		}
		if p.Minimum != nil && (math.IsNaN(*p.Minimum) || math.IsInf(*p.Minimum, 0)) || p.Maximum != nil && (math.IsNaN(*p.Maximum) || math.IsInf(*p.Maximum, 0)) {
			return s, invalid("Nonfinite bounds")
		}
		if (p.Minimum != nil || p.Maximum != nil) && p.Type != "number" && p.Type != "integer" || (p.MinLength != nil || p.MaxLength != nil) && p.Type != "string" {
			return s, invalid("Bounds do not match type")
		}
		if p.Minimum != nil && p.Maximum != nil && *p.Minimum > *p.Maximum || p.MinLength != nil && *p.MinLength < 0 || p.MaxLength != nil && *p.MaxLength < 0 || p.MinLength != nil && p.MaxLength != nil && *p.MinLength > *p.MaxLength {
			return s, invalid("Invalid bounds")
		}
		if p.Enum != nil && len(p.Enum) == 0 {
			return s, invalid("Empty enum")
		}
		for _, v := range p.Enum {
			q := p
			q.Enum = nil
			if !validParameter(q, v) {
				return s, invalid("Invalid enum value")
			}
		}
	}
	return s, nil
}
func number(v any) (float64, bool) {
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case json.Number:
		var e error
		n, e = x.Float64()
		if e != nil {
			return 0, false
		}
	case int:
		n = float64(x)
	case int64:
		n = float64(x)
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}
func validParameter(p parameter, v any) bool {
	switch p.Type {
	case "string":
		s, ok := v.(string)
		if !ok || !utf8.ValidString(s) {
			return false
		}
		n := utf8.RuneCountInString(s)
		if p.MinLength != nil && n < *p.MinLength || p.MaxLength != nil && n > *p.MaxLength {
			return false
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return false
		}
	case "number", "integer":
		n, ok := number(v)
		if !ok || p.Type == "integer" && math.Trunc(n) != n || p.Minimum != nil && n < *p.Minimum || p.Maximum != nil && n > *p.Maximum {
			return false
		}
	default:
		return false
	}
	if p.Enum != nil {
		for _, allowed := range p.Enum {
			if p.Type == "number" || p.Type == "integer" {
				a, _ := number(allowed)
				b, _ := number(v)
				if a == b {
					return true
				}
			} else if allowed == v {
				return true
			}
		}
		return false
	}
	return true
}
func commonSchema(c Capability) json.RawMessage {
	sizes := c.Sizes
	if len(sizes) == 0 {
		sizes = []string{"1024x1024", "1536x1024", "1024x1536"}
	}
	enumeration := func(values ...string) parameter {
		v := make([]any, len(values))
		for i, s := range values {
			v[i] = s
		}
		return parameter{Type: "string", Enum: v}
	}
	lo, hi := 0.0, 100.0
	limit := 128
	closed := false
	s := parameterSchema{Type: "object", AdditionalProperties: &closed, Properties: map[string]parameter{
		"size": enumeration(sizes...), "quality": enumeration("auto", "low", "medium", "high"), "background": enumeration("auto", "opaque", "transparent"), "output_format": enumeration("png", "jpeg"), "output_compression": {Type: "integer", Minimum: &lo, Maximum: &hi}, "moderation": enumeration("auto", "low"), "safety_identifier": {Type: "string", MaxLength: &limit},
	}}
	b, _ := json.Marshal(s)
	return b
}
func validateDeclaration(c Config) error {
	d := c.Capability
	if d.ID != "" || d.Operation != "" || d.Protocol != "" || d.ModelID != "" || d.KnowledgeCard != "" {
		return invalid("Catalog identity is gateway-owned")
	}
	if c.Kind != "image" {
		if len(d.ParamSchema) > 0 || len(d.ReferenceRoles) > 0 || d.Constraints != (Constraints{}) {
			return invalid("Video schema is not implemented")
		}
		return nil
	}
	if d.Constraints.MaxPromptCharacters < 0 || d.Constraints.MaxPromptCharacters > 32000 || d.Constraints.MaxImageBytes < 0 || d.Constraints.MaxImageBytes > 20<<20 || d.Constraints.MaxPixels < 0 || d.Constraints.MaxPixels > 16777216 {
		return invalid("Constraints may only tighten adapter limits")
	}
	for _, size := range d.Sizes {
		if !slices.Contains([]string{"1024x1024", "1536x1024", "1024x1536"}, size) {
			return invalid("Size unsupported by conservative image template")
		}
	}
	masks := 0
	for i, role := range d.ReferenceRoles {
		if role == "mask" {
			masks++
			if i == 0 {
				return invalid("Mask must follow source image")
			}
		} else if role != "image" {
			return invalid("Unsupported reference role")
		}
	}
	if len(d.ReferenceRoles) > d.MaxReferences || masks > 1 || len(d.ReferenceRoles) > 0 && !d.Edit {
		return invalid("Invalid ordered reference roles")
	}
	if len(d.ParamSchema) == 0 {
		return nil
	}
	s, e := parseSchema(d.ParamSchema)
	if e != nil {
		return e
	}
	base, _ := parseSchema(commonSchema(d))
	for k, p := range s.Properties {
		b, ok := base.Properties[k]
		if !ok || b.Type != p.Type {
			return invalid("Unsupported adapter parameter: " + k)
		}
		if b.Enum != nil {
			if len(p.Enum) == 0 {
				return invalid("Schema cannot widen adapter enum")
			}
			for _, v := range p.Enum {
				if !validParameter(b, v) {
					return invalid("Schema cannot widen adapter enum")
				}
			}
		}
		if b.Minimum != nil && (p.Minimum == nil || *p.Minimum < *b.Minimum) || b.Maximum != nil && (p.Maximum == nil || *p.Maximum > *b.Maximum) || b.MaxLength != nil && (p.MaxLength == nil || *p.MaxLength > *b.MaxLength) {
			return invalid("Schema cannot widen adapter bounds")
		}
	}
	return nil
}
func ImageCapability(c Config) Capability {
	cap := c.Capability
	cap.ID = "image:" + c.ID
	cap.Operation = "image"
	cap.Protocol = c.Protocol
	cap.ModelID = c.ModelID
	cap.KnowledgeCard = "knowledge/providers/openai-images.md"
	if len(cap.ParamSchema) == 0 {
		cap.ParamSchema = commonSchema(cap)
	}
	if cap.Constraints.MaxPromptCharacters == 0 {
		cap.Constraints.MaxPromptCharacters = 32000
	}
	if cap.Constraints.MaxImageBytes == 0 {
		cap.Constraints.MaxImageBytes = 20 << 20
	}
	if cap.Constraints.MaxPixels == 0 {
		cap.Constraints.MaxPixels = 16777216
	}
	return cap
}
func Validate(cap Capability, in GenerationInput) error {
	if cap.Operation != "image" || cap.Protocol != "openai-images" {
		return invalid("Unsupported capability protocol")
	}
	chars := cap.Constraints.MaxPromptCharacters
	if chars == 0 {
		chars = 32000
	}
	if in.Prompt == "" || !utf8.ValidString(in.Prompt) || len(in.Prompt) > 65536 || len(in.Prompt) > cap.MaxPromptBytes || utf8.RuneCountInString(in.Prompt) > chars {
		return invalid("Prompt exceeds UTF-8 byte or model character limit")
	}
	if in.Count < 1 || in.Count > cap.MaxCount || in.Count > 10 {
		return invalid("Invalid output count")
	}
	if len(in.References) > cap.MaxReferences || len(in.References) > 16 || len(in.References) > 0 && !cap.Edit || len(cap.ReferenceRoles) > 0 && len(in.References) != len(cap.ReferenceRoles) {
		return invalid("Invalid ordered references")
	}
	for _, id := range in.References {
		if !identifier.MatchString(id) || len(id) > 128 {
			return invalid("References require asset IDs")
		}
	}
	s, e := parseSchema(cap.ParamSchema)
	if e != nil {
		return e
	}
	if in.Params == nil {
		return invalid("params must be an object")
	}
	for _, k := range s.Required {
		if _, ok := in.Params[k]; !ok {
			return invalid("Missing parameter: " + k)
		}
	}
	for k, v := range in.Params {
		p, ok := s.Properties[k]
		if !ok || !validParameter(p, v) {
			return invalid("Invalid or undeclared parameter: " + k)
		}
	}
	if in.Params["background"] == "transparent" && in.Params["output_format"] == "jpeg" {
		return invalid("Transparent background requires PNG")
	}
	if _, ok := in.Params["output_compression"]; ok && in.Params["output_format"] != "jpeg" {
		return invalid("Compression requires JPEG output")
	}
	return nil
}
func ReferenceRole(cap Capability, index int) string {
	if len(cap.ReferenceRoles) > index {
		return cap.ReferenceRoles[index]
	}
	return "image"
}
func parameterText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if n, ok := number(v); ok {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}
