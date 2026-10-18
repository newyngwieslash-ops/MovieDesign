// Package generation owns public generation admission shared by HTTP and MCP.
package generation

import (
	"context"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"strings"
	"time"
)

type Request struct {
	RequestID    string          `json:"requestId"`
	SessionID    string          `json:"session_id,omitempty"`
	CapabilityID string          `json:"capability_id"`
	Prompt       string          `json:"prompt"`
	Params       map[string]any  `json:"params"`
	References   []string        `json:"references"`
	Count        *int            `json:"count,omitempty"`
	Output       delivery.Output `json:"output"`
	QuoteID      *string         `json:"quote_id,omitempty"`
}

type Service struct {
	TextProvider func(context.Context) (providers.FrozenProvider, error)
	Providers    *providers.Service
	Tasks        *tasks.Service
}
type Result struct {
	Task  tasks.TaskView
	Quote *providers.Quote
}

func (s Service) Capabilities(ctx context.Context) ([]providers.Capability, error) {
	caps := []providers.Capability{}
	if s.Providers != nil {
		items, e := s.Providers.List(ctx)
		if e != nil {
			return nil, e
		}
		for _, item := range items {
			if item.Config.Enabled && item.Config.Kind == "image" {
				caps = append(caps, providers.ImageCapability(item.Config))
			}
		}
	}
	if s.TextProvider != nil {
		if f, e := s.TextProvider(ctx); e == nil {
			caps = append(caps, providers.TextCapability(f.Config))
		}
	}
	return caps, nil
}

func invalid(message string) error {
	return &store.DomainError{Status: 400, Code: "invalid_input", Message: message}
}

// wire is the complete public body, before transport field mapping. Operation is
// gateway-owned; MCP cannot choose its session, provider snapshot or replay domain.
func (s Service) Admit(ctx context.Context, p string, r Request, wire []byte, estimate bool, operation string) (Result, error) {
	var result Result
	if s.Providers == nil || s.Tasks == nil {
		return result, &store.DomainError{Status: 503, Code: "upstream", Message: "Provider service unavailable"}
	}
	if estimate {
		previous, found, err := s.Providers.ReplayQuote(ctx, p, r.RequestID, wire)
		if err != nil {
			return result, err
		}
		if found {
			result.Quote = &previous
			return result, nil
		}
	} else {
		previous, found, err := s.Tasks.ReplaySubmissionOperation(ctx, p, r.RequestID, wire, operation)
		if err != nil {
			return result, err
		}
		if found {
			result.Task = previous
			return result, nil
		}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(wire, &fields) != nil {
		return result, invalid("Invalid generation body")
	}
	for _, key := range []string{"prompt", "params", "references", "output", "count", "quote_id", "session_id", "quoteId"} {
		if b, ok := fields[key]; ok && string(b) == "null" {
			return result, invalid(key + " cannot be null")
		}
	}
	count := 1
	if r.Count != nil {
		count = *r.Count
	}
	if r.Params == nil {
		r.Params = map[string]any{}
	}
	if r.References == nil {
		r.References = []string{}
	}
	isText := r.CapabilityID == "text:configured-llm"
	if !isText && (!strings.HasPrefix(r.CapabilityID, "image:") || len(r.CapabilityID) <= 6) {
		return result, invalid("Expected an explicit image capability ID")
	}
	var f providers.FrozenProvider
	var e error
	if isText {
		if s.TextProvider == nil {
			return result, invalid("Text runtime unavailable")
		}
		f, e = s.TextProvider(ctx)
	} else {
		f, e = s.Providers.Resolve(strings.TrimPrefix(r.CapabilityID, "image:"))
	}
	if e != nil {
		return result, e
	}
	cap := providers.ImageCapability(f.Config)
	if isText {
		cap = providers.TextCapability(f.Config)
	}
	if f.Config.Kind != "image" && !isText {
		return result, invalid("Unsupported image capability")
	}
	output, e := json.Marshal(r.Output)
	if e != nil {
		return result, e
	}
	input := providers.GenerationInput{ProjectID: p, Prompt: r.Prompt, Params: r.Params, References: r.References, Count: count, Output: output}
	if isText {
		e = providers.ValidateText(input, f)
	} else {
		e = providers.Validate(cap, input)
	}
	if e != nil {
		return result, e
	}
	if !isText {
		input.Assets, e = s.Providers.FreezeReferences(ctx, p, input, f)
	}
	if e != nil {
		return result, e
	}
	params, _ := json.Marshal(r.Params)
	task := tasks.Input{RequestID: r.RequestID, SessionID: r.SessionID, CapabilityID: r.CapabilityID, Prompt: r.Prompt, Params: params, Output: r.Output, Count: count, Assets: input.Assets, Provider: f, OriginalWire: wire, Operation: operation}
	task.Actor = "user"
	if operation == "mcp" {
		task.Actor = "agent"
	}
	for i, id := range r.References {
		task.References = append(task.References, tasks.Reference{AssetID: id, Role: providers.ReferenceRole(cap, i)})
	}
	if e = s.Tasks.ValidateAdmission(ctx, p, task); e != nil {
		return result, e
	}
	quote, e := providers.Estimate(input, f)
	if e != nil {
		return result, e
	}
	if estimate {
		if r.QuoteID != nil {
			return result, invalid("Estimate does not accept quote_id")
		}
		quote, e = s.Providers.SaveQuote(ctx, p, r.RequestID, wire, input, f)
		result.Quote = &quote
		return result, e
	}
	if r.QuoteID != nil {
		saved, err := s.Providers.QuoteBinding(ctx, p, *r.QuoteID)
		if err != nil {
			return result, err
		}
		if saved.Quote.InputHash != quote.InputHash || saved.Quote.ExpiresAt <= time.Now().UnixMilli() {
			return result, &store.DomainError{Status: 409, Code: "waiting_user", Message: "Quote expired or input/configuration changed; estimate again"}
		}
		task.QuoteID = saved.Quote.QuoteID
	}
	result.Task, e = s.Tasks.Submit(ctx, p, task)
	return result, e
}
