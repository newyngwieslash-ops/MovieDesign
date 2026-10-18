package mcp

import (
	"context"
	"encoding/json"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/agent"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/generation"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"slices"
)

var names = []string{"design_capability_list", "design_capability_get", "design_execute", "design_deliver", "design_task_query", "design_task_wait", "design_task_resolve", "design_composition_search", "design_composition_get", "design_composition_edit", "design_resource_get", "design_resource_edit"}

func Names() []string { return slices.Clone(names) }
func NativeNames() []string {
	n := Names()
	for i := range n {
		n[i] = "design_" + n[i]
	}
	return n
}

type executeInput struct {
	RequestID    string          `json:"requestId"`
	CapabilityID string          `json:"capabilityId"`
	Prompt       string          `json:"prompt"`
	Params       map[string]any  `json:"params"`
	References   []string        `json:"references"`
	Count        *int            `json:"count,omitempty"`
	Output       delivery.Output `json:"output"`
	QuoteID      *string         `json:"quoteId,omitempty"`
}
type taskInput struct {
	TaskID string `json:"taskId"`
}
type getInput struct {
	ID string `json:"id"`
}
type pageInput struct {
	Offset int `json:"offset,omitempty"`
	Limit  int `json:"limit,omitempty"`
}
type resolveInput struct {
	RequestID        string `json:"requestId"`
	TaskID           string `json:"taskId"`
	Action           string `json:"action"`
	ExpectedRevision int64  `json:"expected_revision"`
}

func invalid(message string) error {
	return &store.DomainError{Status: 400, Code: "invalid_input", Message: message}
}
func (s *Server) register(server *sdk.Server, scope agent.ToolScope) {
	add(s, server, scope, "design_execute", "Execute image capability and wait through billing; always declare output and stable requestId.", func(ctx context.Context, i executeInput, raw []byte) (any, error) {
		r, e := s.services.Generation.Admit(ctx, scope.ProjectID, generation.Request{RequestID: i.RequestID, SessionID: scope.SessionID, CapabilityID: i.CapabilityID, Prompt: i.Prompt, Params: i.Params, References: i.References, Count: i.Count, Output: i.Output, QuoteID: i.QuoteID}, raw, false, "mcp")
		if e != nil {
			return nil, e
		}
		v, e := s.services.Tasks.Wait(ctx, scope.ProjectID, r.Task.ID)
		if ctx.Err() != nil {
			return map[string]any{"task_id": r.Task.ID, "wait_interrupted": true, "message": "Waiting stopped; the durable task was not cancelled."}, nil
		}
		return v, e
	})
	add(s, server, scope, "design_deliver", "Deliver managed sources using the shared recoverable delivery journal.", func(ctx context.Context, i delivery.Request, _ []byte) (any, error) {
		if s.services.Delivery == nil {
			return nil, invalid("Delivery unavailable")
		}
		return s.services.Delivery.Deliver(ctx, scope.ProjectID, i)
	})
	add(s, server, scope, "design_task_query", "Get a complete task view in the owned project.", func(ctx context.Context, i taskInput, _ []byte) (any, error) {
		return s.services.Tasks.Query(ctx, scope.ProjectID, i.TaskID)
	})
	add(s, server, scope, "design_task_wait", "Wait through billing until ordinary user action, unknown or terminal state.", func(ctx context.Context, i taskInput, _ []byte) (any, error) {
		return s.services.Tasks.Wait(ctx, scope.ProjectID, i.TaskID)
	})
	add(s, server, scope, "design_task_resolve", "Cancel a task at its expected revision. Retry requires a human action through the application.", func(ctx context.Context, i resolveInput, _ []byte) (any, error) {
		if i.Action != "cancel" {
			return nil, &store.DomainError{Status: 409, Code: "waiting_user", Message: "Retry requires an explicit human action in the application"}
		}
		return s.services.Tasks.Resolve(ctx, scope.ProjectID, i.TaskID, tasks.ResolveInput{RequestID: i.RequestID, Action: i.Action, ExpectedRevision: i.ExpectedRevision})
	})
	add(s, server, scope, "design_capability_list", "List configured image capabilities with complete pagination coverage.", func(ctx context.Context, i pageInput, _ []byte) (any, error) {
		caps, e := s.capabilities(ctx)
		if e != nil {
			return nil, e
		}
		if i.Limit == 0 {
			i.Limit = 50
		}
		if i.Offset < 0 || i.Limit < 1 || i.Limit > 100 {
			return nil, invalid("Invalid pagination")
		}
		start := min(i.Offset, len(caps))
		end := min(start+i.Limit, len(caps))
		return map[string]any{"capabilities": caps[start:end], "coverage": store.Coverage{Total: len(caps), NextOffset: end, Exhausted: end == len(caps)}}, nil
	})
	add(s, server, scope, "design_capability_get", "Get one configured image capability, schema and provider knowledge card.", func(ctx context.Context, i getInput, _ []byte) (any, error) {
		caps, e := s.capabilities(ctx)
		if e != nil {
			return nil, e
		}
		for _, cap := range caps {
			if cap.ID == i.ID {
				return map[string]any{"capability": cap, "knowledge": providers.ImageKnowledgeCard}, nil
			}
		}
		return nil, providers.ErrNotFound
	})
	add(s, server, scope, "design_composition_search", "Page through compositions in the owned project.", func(ctx context.Context, i pageInput, _ []byte) (any, error) {
		if i.Limit == 0 {
			i.Limit = 50
		}
		return s.services.Objects.List(ctx, scope.ProjectID, "compositions", i.Offset, i.Limit)
	})
	for _, kind := range []string{"composition", "resource"} {
		add(s, server, scope, "design_"+kind+"_get", "Read an object in the owned project.", func(ctx context.Context, i getInput, _ []byte) (any, error) {
			raw, e := s.services.Objects.Get(ctx, scope.ProjectID, kind+"s", i.ID)
			if e != nil {
				return nil, e
			}
			var v any
			e = json.Unmarshal(raw, &v)
			return v, e
		})
		add(s, server, scope, "design_"+kind+"_edit", "Create/update/delete object metadata using shared validation; content revisions require delivery.", func(ctx context.Context, i store.Mutation, _ []byte) (any, error) {
			raw, e := s.services.Objects.Mutate(ctx, scope.ProjectID, kind+"s", i)
			if e != nil {
				return nil, e
			}
			var v any
			e = json.Unmarshal(raw, &v)
			return v, e
		})
	}
}
func (s *Server) capabilities(ctx context.Context) ([]providers.Capability, error) {
	caps := []providers.Capability{}
	if s.services.Generation.Providers == nil {
		return caps, nil
	}
	items, e := s.services.Generation.Providers.List(ctx)
	if e != nil {
		return nil, e
	}
	for _, v := range items {
		if v.Config.Enabled && v.Config.Kind == "image" {
			caps = append(caps, providers.ImageCapability(v.Config))
		}
	}
	return caps, nil
}
