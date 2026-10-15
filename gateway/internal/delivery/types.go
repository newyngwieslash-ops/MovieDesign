package delivery

import (
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

type Request struct {
	RequestID string   `json:"requestId"`
	Sources   []Source `json:"sources"`
	Output    Output   `json:"output"`
	raw       json.RawMessage
}

// Preserve complete wire input, including omitted versus explicit optional fields.
func (r *Request) UnmarshalJSON(b []byte) error {
	type plain Request
	v, e := store.DecodeValue[plain](b)
	if e != nil {
		return e
	}
	*r = Request(v)
	r.raw = append([]byte(nil), b...)
	return nil
}

type Source struct {
	Key           string `json:"key"`
	Path          string `json:"path,omitempty"`
	TaskOutputKey string `json:"taskOutputKey,omitempty"`
}
type Output struct {
	Resources   []Resource  `json:"resources"`
	Composition Composition `json:"composition"`
}
type Resource struct {
	Key           string `json:"key"`
	Mode          string `json:"mode"`
	Name          string `json:"name"`
	BaseVersionID string `json:"baseVersionId,omitempty"`
}
type Composition struct {
	Name       string          `json:"name"`
	Type       CompositionType `json:"type"`
	Placements []Placement     `json:"placements"`
}

func (c *Composition) UnmarshalJSON(b []byte) error {
	type plain Composition
	v, e := store.DecodeValue[plain](b)
	if e != nil {
		return e
	}
	if len(v.Placements) < 1 || len(v.Placements) > 50 {
		return invalid("Composition requires 1–50 placements")
	}
	*c = Composition(v)
	return nil
}

type CompositionType struct {
	ID    string `json:"id,omitempty"`
	Label string `json:"label,omitempty"`
}
type Placement struct {
	Key  string `json:"key"`
	Role string `json:"role"`
}
type Version struct {
	Key        string `json:"key"`
	ResourceID string `json:"resourceId"`
	VersionID  string `json:"versionId"`
	AssetID    string `json:"assetId"`
}
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Result struct {
	Status   string    `json:"status"`
	Versions []Version `json:"versions"`
	Problem  *Problem  `json:"problem,omitempty"`
}
type Receipt struct {
	Delivery Result `json:"delivery"`
}

// Optional strings are absent or nonempty strings, never null or empty aliases.
func optionalStrings(b []byte, names ...string) error {
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(b, &fields); e != nil {
		return e
	}
	for _, name := range names {
		if raw, ok := fields[name]; ok {
			var value string
			if e := json.Unmarshal(raw, &value); e != nil || value == "" {
				return invalid(name + " must be a nonempty string when present")
			}
		}
	}
	return nil
}
func (s *Source) UnmarshalJSON(b []byte) error {
	type plain Source
	v, e := store.DecodeValue[plain](b)
	if e != nil {
		return e
	}
	if e = optionalStrings(b, "path", "taskOutputKey"); e != nil {
		return e
	}
	*s = Source(v)
	return nil
}
func (r *Resource) UnmarshalJSON(b []byte) error {
	type plain Resource
	v, e := store.DecodeValue[plain](b)
	if e != nil {
		return e
	}
	if e = optionalStrings(b, "baseVersionId"); e != nil {
		return e
	}
	*r = Resource(v)
	return nil
}
func (c *CompositionType) UnmarshalJSON(b []byte) error {
	type plain CompositionType
	v, e := store.DecodeValue[plain](b)
	if e != nil {
		return e
	}
	if e = optionalStrings(b, "id", "label"); e != nil {
		return e
	}
	*c = CompositionType(v)
	return nil
}
