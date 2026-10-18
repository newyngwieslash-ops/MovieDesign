// Package mcp exposes only scoped MovieDesign tools using the official SDK.
package mcp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/agent"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/generation"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
	"net/http"
	"reflect"
	"sync"
	"time"
)

type Services struct {
	Files      *workspace.Service
	Questions  *questions.Service
	FFprobe    string
	Objects    store.Objects
	Generation generation.Service
	Tasks      *tasks.Service
	Delivery   *delivery.Service
}
type Server struct {
	http.Handler
	Turns         *agent.Turns
	services      Services
	token, origin string
	mu            sync.Mutex
	servers       map[*sdk.Server]string
	catalogs      map[*sdk.Server]bool
}

func New(origin string, turns *agent.Turns, services Services) *Server {
	s := &Server{Turns: turns, services: services, token: rand.Text(), origin: origin, servers: map[*sdk.Server]string{}, catalogs: map[*sdk.Server]bool{}}
	protection := new(http.CrossOriginProtection)
	if e := protection.AddTrustedOrigin(origin); e != nil {
		panic(e)
	}
	transport := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		scope := turns.Current()
		server := sdk.NewServer(&sdk.Implementation{Name: "MovieDesign", Version: "0.1.0"}, nil)
		s.register(server, scope)
		server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
			return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
				result, e := next(ctx, method, req)
				if method == "tools/list" && e == nil {
					s.mu.Lock()
					s.catalogs[server] = true
					s.mu.Unlock()
				}
				return result, e
			}
		})
		s.mu.Lock()
		s.servers[server] = scope.Epoch
		s.mu.Unlock()
		return server
	}, &sdk.StreamableHTTPOptions{CrossOriginProtection: protection, SessionTimeout: 5 * time.Minute})
	s.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origins := r.Header.Values("Origin")
		if len(origins) != 1 || origins[0] != origin {
			http.Error(w, "Origin is not allowed", 403)
			return
		}
		auth := r.Header.Values("Authorization")
		if len(auth) != 1 || subtle.ConstantTimeCompare([]byte(auth[0]), []byte("Bearer "+s.token)) != 1 {
			http.Error(w, "Dedicated MCP token required", 401)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		transport.ServeHTTP(w, r)
	})
	return s
}
func (s *Server) Connection() agent.MCPConnection {
	return agent.MCPConnection{URL: "http://127.0.0.1:8001/mcp", Origin: s.origin, Token: s.token, Tools: NativeNames()}
}
func (s *Server) CloseEpoch(epoch string) error {
	s.mu.Lock()
	var closing []*sdk.Server
	for server, e := range s.servers {
		if epoch == "" || e == epoch {
			closing = append(closing, server)
			delete(s.servers, server)
			delete(s.catalogs, server)
		}
	}
	s.mu.Unlock()
	var result error
	for _, server := range closing {
		for session := range server.Sessions() {
			result = errors.Join(result, session.Close())
		}
	}
	return result
}
func business(value any, err error) *sdk.CallToolResult {
	if err != nil {
		code, message := "upstream", "Tool execution unavailable; inspect current state before retrying"
		var d *store.DomainError
		switch {
		case errors.As(err, &d):
			code, message = d.Code, d.Message
		case errors.Is(err, agent.ErrBudget):
			code, message = "capacity", err.Error()
		case errors.Is(err, agent.ErrScope), errors.Is(err, store.ErrRequestConflict), errors.Is(err, providers.ErrInvalid):
			code, message = "invalid_input", err.Error()
		case errors.Is(err, providers.ErrNotFound):
			code, message = "not_found", "Capability not found"
		case errors.Is(err, store.ErrRequestPending):
			code, message = "waiting_user", "Original request is unresolved; do not resubmit"
		}
		value = map[string]any{"error": map[string]any{"code": code, "message": message}}
	}
	raw, _ := json.Marshal(value)
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(raw)}}, StructuredContent: value}
}
func add[I any](s *Server, server *sdk.Server, scope agent.ToolScope, name, description string, call func(context.Context, I, []byte) (any, error)) {
	// Public RawMessage fields carry JSON objects, never Go byte-array data.
	schema, e := jsonschema.For[I](&jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{reflect.TypeFor[json.RawMessage](): {Type: "object"}}})
	if e != nil {
		panic(e)
	}
	resolved, e := schema.Resolve(nil)
	if e != nil {
		panic(e)
	}
	server.AddTool(&sdk.Tool{Name: name, Description: description, InputSchema: schema}, func(ctx context.Context, r *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		done, e := s.Turns.Acquire(scope)
		if e != nil {
			return business(nil, e), nil
		}
		defer done()
		raw := r.Params.Arguments
		var request struct {
			RequestID string `json:"requestId"`
		}
		_ = json.Unmarshal(raw, &request)
		callID := "mcp:" + uuid.NewString()
		// Pinned native host does not forward toolCallId. Even identical RPC/requestId
		// re-execution consumes budget; UI event replay never calls this handler.
		if _, e = s.Turns.Admit(ctx, scope, callID, name, request.RequestID, raw); e != nil {
			return business(nil, e), nil
		}
		input, e := store.DecodeValue[I](raw)
		var value any
		if e == nil {
			var args any
			e = json.Unmarshal(raw, &args)
			if e == nil {
				e = resolved.Validate(args)
			}
		}
		if e != nil {
			e = &store.DomainError{Status: 400, Code: "invalid_input", Message: "Invalid tool arguments"}
		} else {
			value, e = call(ctx, input, raw)
		}
		result := business(value, e)
		saved, _ := json.Marshal(result.StructuredContent)
		if e = s.Turns.Record(context.WithoutCancel(ctx), scope, callID, saved); e != nil {
			return business(nil, e), nil
		}
		return result, nil
	})
}

// CatalogReady requires an actual list response on a live authenticated transport.
func (s *Server) CatalogReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for server := range s.catalogs {
		for range server.Sessions() {
			return true
		}
	}
	return false
}
