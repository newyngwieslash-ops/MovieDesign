package tasks

import (
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

type Status string

const (
	Accepted    Status = "accepted"
	Running     Status = "running"
	WaitingUser Status = "waiting_user"
	Unknown     Status = "unknown"
	Completed   Status = "completed"
	Failed      Status = "failed"
	Cancelled   Status = "cancelled"
)

func Terminal(s Status) bool { return s == Completed || s == Failed || s == Cancelled }
func SettlesAgentWait(s Status, source string) bool {
	return Terminal(s) || s == Unknown || s == WaitingUser && source != "billing"
}

type Reference struct {
	AssetID   string `json:"asset_id"`
	VersionID string `json:"version_id,omitempty"`
	Role      string `json:"role"`
}
type Input struct {
	RequestID    string                     `json:"requestId"`
	SessionID    string                     `json:"session_id,omitempty"`
	CapabilityID string                     `json:"capability_id"`
	Prompt       string                     `json:"prompt"`
	Params       json.RawMessage            `json:"params"`
	References   []Reference                `json:"references"`
	Output       delivery.Output            `json:"output"`
	Count        int                        `json:"count,omitempty"`
	QuoteID      string                     `json:"quote_id,omitempty"`
	Assets       []providers.ReferenceAsset `json:"assets,omitempty"`
	// Supplied by trusted capability admission; never accepted from HTTP/MCP JSON.
	Provider providers.FrozenProvider `json:"-"`
	// OriginalWire holds the complete public body from trusted admission.
	// Receipt identity uses it; frozen_input_json keeps resolved internal input.
	OriginalWire json.RawMessage `json:"-"`
	raw          json.RawMessage
}

func (i *Input) UnmarshalJSON(b []byte) error {
	type plain Input
	v, e := store.DecodeValue[plain](b)
	if e != nil {
		return e
	}
	*i = Input(v)
	i.raw = append([]byte(nil), b...)
	return nil
}

type ResolveInput struct {
	RequestID        string `json:"requestId"`
	Action           string `json:"action"`
	ExpectedRevision int64  `json:"expected_revision"`
	raw              json.RawMessage
}

func (i *ResolveInput) UnmarshalJSON(b []byte) error {
	type plain ResolveInput
	v, e := store.DecodeValue[plain](b)
	if e != nil {
		return e
	}
	*i = ResolveInput(v)
	i.raw = append([]byte(nil), b...)
	return nil
}

type Problem struct {
	Source    string    `json:"source"`
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	Upstream  *Upstream `json:"upstream,omitempty"`
	Retryable bool      `json:"retryable"`
}

// Only adapter-classified codes and sanitized messages belong here, never raw bodies/headers.
type Upstream struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}
type Progress struct {
	Phase   string `json:"phase"`
	Percent *int   `json:"percent"`
}
type Billing struct {
	EstimatedCredits *int64 `json:"estimated_credits"`
}
type Output struct {
	TaskOutputKey string  `json:"task_output_key"`
	OutputKey     string  `json:"output_key"`
	Role          string  `json:"role"`
	State         string  `json:"state"`
	AssetID       *string `json:"asset_id,omitempty"`
	VersionID     *string `json:"version_id,omitempty"`
}
type TaskView struct {
	ID           string   `json:"id"`
	CapabilityID string   `json:"capability_id"`
	Status       Status   `json:"status"`
	Revision     int64    `json:"revision"`
	Prompt       string   `json:"prompt"`
	Outputs      []Output `json:"outputs"`
	Problem      *Problem `json:"problem,omitempty"`
	Actions      []string `json:"actions"`
	Progress     Progress `json:"progress"`
	Billing      Billing  `json:"billing"`
}

func invalid(message string) error {
	return &store.DomainError{Status: 400, Code: "invalid_input", Message: message}
}
func conflict() error {
	return &store.DomainError{Status: 409, Code: "invalid_input", Message: "Task revision or state has changed"}
}
func unavailable() error {
	return &store.DomainError{Status: 503, Code: "upstream", Message: "Task service is stopping"}
}
func inputJSON(i Input) ([]byte, error) {
	b, e := json.Marshal(i)
	if e != nil {
		return nil, e
	}
	if i.raw != nil {
		var original Input
		if e = json.Unmarshal(i.raw, &original); e != nil {
			return nil, e
		}
		before, _ := json.Marshal(original)
		if string(before) != string(b) {
			return nil, invalid("Decoded task input was modified")
		}
		b = i.raw
	}
	return store.CanonicalJSON(b)
}
