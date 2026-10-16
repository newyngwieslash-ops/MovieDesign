package providers

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
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
	Type      string       `json:"type"`
	Enum      []any        `json:"enum,omitempty"`
	Minimum   *json.Number `json:"minimum,omitempty"`
	Maximum   *json.Number `json:"maximum,omitempty"`
	MinLength *int         `json:"minLength,omitempty"`
	MaxLength *int         `json:"maxLength,omitempty"`
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
		var properties map[string]json.RawMessage
		if json.Unmarshal(object["properties"], &properties) == nil {
			for _, rawProperty := range properties {
				if _, e := store.DecodeValue[parameter](rawProperty); e != nil {
					return parameterSchema{}, invalid("Unknown or invalid property schema keyword")
				}
				var p map[string]json.RawMessage
				if json.Unmarshal(rawProperty, &p) != nil || p == nil {
					return parameterSchema{}, invalid("Property schema must be an object")
				}
				for name, value := range p {
					if string(value) == "null" {
						return parameterSchema{}, invalid("Schema keywords cannot be null")
					}
					if (name == "minimum" || name == "maximum") && (len(value) == 0 || value[0] == '"') {
						return parameterSchema{}, invalid("Numeric bounds must be JSON numbers")
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
		for _, bound := range []*json.Number{p.Minimum, p.Maximum} {
			if bound != nil {
				if _, ok := number(*bound); !ok {
					return s, invalid("Invalid or excessive numeric bound")
				}
			}
		}
		if (p.Minimum != nil || p.Maximum != nil) && p.Type != "number" && p.Type != "integer" || (p.MinLength != nil || p.MaxLength != nil) && p.Type != "string" {
			return s, invalid("Bounds do not match type")
		}
		if p.Minimum != nil && p.Maximum != nil && compareNumbers(*p.Minimum, *p.Maximum) > 0 || p.MinLength != nil && *p.MinLength < 0 || p.MaxLength != nil && *p.MaxLength < 0 || p.MinLength != nil && p.MaxLength != nil && *p.MinLength > *p.MaxLength {
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

var jsonNumeric = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

func number(v any) (*big.Rat, bool) {
	var token string
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, false
		}
		token = strconv.FormatFloat(x, 'g', -1, 64)
	case json.Number:
		token = string(x)
	case int:
		token = strconv.Itoa(x)
	case int64:
		token = strconv.FormatInt(x, 10)
	default:
		return nil, false
	}
	// Bound both representation and exponent before big.Rat allocates powers of ten.
	if len(token) > 1024 || !jsonNumeric.MatchString(token) {
		return nil, false
	}
	if i := strings.IndexAny(token, "eE"); i >= 0 {
		exponent, e := strconv.ParseInt(token[i+1:], 10, 32)
		if e != nil || exponent < -1024 || exponent > 1024 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(token)
}
func compareNumbers(a, b json.Number) int {
	left, _ := number(a)
	right, _ := number(b)
	return left.Cmp(right)
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
		if !ok || p.Type == "integer" && !n.IsInt() {
			return false
		}
		if p.Minimum != nil {
			bound, ok := number(*p.Minimum)
			if !ok || n.Cmp(bound) < 0 {
				return false
			}
		}
		if p.Maximum != nil {
			bound, ok := number(*p.Maximum)
			if !ok || n.Cmp(bound) > 0 {
				return false
			}
		}
	default:
		return false
	}
	if p.Enum != nil {
		for _, allowed := range p.Enum {
			if p.Type == "number" || p.Type == "integer" {
				a, ok := number(allowed)
				b, valid := number(v)
				if ok && valid && a.Cmp(b) == 0 {
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
	lo, hi := json.Number("0"), json.Number("100")
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
		if b.Minimum != nil && (p.Minimum == nil || compareNumbers(*p.Minimum, *b.Minimum) < 0) || b.Maximum != nil && (p.Maximum == nil || compareNumbers(*p.Maximum, *b.Maximum) > 0) || b.MaxLength != nil && (p.MaxLength == nil || *p.MaxLength > *b.MaxLength) {
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
	return fmt.Sprint(v)
}
