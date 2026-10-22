package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProductionBootstrapNeverGivesNativeSupplierCredentialOrDestination(t *testing.T) {
	dir := t.TempDir()
	db, e := store.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	secrets, e := providers.OpenSecrets(filepath.Join(dir, "secrets"))
	if e != nil {
		t.Fatal(e)
	}
	settings := &Settings{DB: db, Secrets: secrets}
	_, e = settings.Execute(context.Background(), []byte(`{"requestId":"save","action":"save","config":{"protocol":"openai-compatible","base_url":"http://127.0.0.1:11434/v1","model_id":"fixture","context":32000,"output":4096},"key":"supplier-secret"}`))
	if e != nil {
		t.Fatal(e)
	}
	_, e = NewHost(settings, filepath.Join(dir, "launch"), MCPConnection{URL: "http://127.0.0.1:8001/mcp", Origin: "http://tauri.localhost", Token: "mcp-private"})
	if e != nil {
		t.Fatal(e)
	}
	launch, e := providers.OpenSecrets(filepath.Join(dir, "launch"))
	if e != nil {
		t.Fatal(e)
	}
	raw, e := launch.Get("launch")
	if e != nil {
		t.Fatal(e)
	}
	var b Bootstrap
	if e = json.Unmarshal([]byte(raw), &b); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(b.Config, "11434") || b.Key == "supplier-secret" || !strings.Contains(b.Config, "/internal/inference") {
		t.Fatal("native bootstrap still bypasses guarded inference boundary")
	}
}

func TestRelayBlocksUncertainAndDuplicateRequestsButAllowsCompletedToolStep(t *testing.T) {
	for _, mode := range []string{"500", "redirect", "disconnect", "tool"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			db, e := store.Open(filepath.Join(dir, "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "create", "P")
			if e != nil {
				t.Fatal(e)
			}
			turns := NewTurns(db)
			scope, e := turns.Begin(context.Background(), p.ID, "session", "native")
			if e != nil {
				t.Fatal(e)
			}
			var calls, redirects atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirects.Add(1) }))
			defer target.Close()
			supplier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer supplier-key" {
					t.Error("supplier credential not frozen")
				}
				switch mode {
				case "500":
					w.WriteHeader(500)
				case "redirect":
					http.Redirect(w, r, target.URL, 307)
				case "disconnect":
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
				case "tool":
					_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call-1\"}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"))
				}
			}))
			defer supplier.Close()
			relay, e := newRelay(db, LLMConfig{Protocol: "openai-compatible", BaseURL: supplier.URL, ModelID: "fixture"}, "supplier-key", "http://tauri.localhost")
			if e != nil {
				t.Fatal(e)
			}
			relay.Turns = turns
			send := func(body, token, origin string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", inferencePath+"/chat/completions", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+token)
				r.Header.Set("Origin", origin)
				r.Header.Set("X-Opencode-Session-Id", scope.NativeSessionID)
				w := httptest.NewRecorder()
				func() {
					defer func() {
						if p := recover(); p != nil && p != http.ErrAbortHandler {
							panic(p)
						}
					}()
					relay.ServeHTTP(w, r)
				}()
				return w
			}
			body := `{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`
			for _, auth := range [][2]string{{"wrong", relay.origin}, {relay.token, "https://evil.example"}} {
				if send(body, auth[0], auth[1]).Code == 200 {
					t.Fatal("private relay authorization bypass")
				}
			}
			first := send(body, relay.token, relay.origin)
			if mode == "tool" && first.Code != 200 {
				t.Fatal(first.Body.String())
			}
			if mode != "tool" && mode != "disconnect" && first.Code == 200 {
				t.Fatal("uncertain stream exposed as success")
			}
			if mode == "disconnect" && (!strings.Contains(first.Body.String(), "partial") || strings.Contains(first.Body.String(), "[DONE]")) {
				t.Fatal("partial text lost or exposed as completed")
			}
			if send(body, relay.token, relay.origin).Code == 200 {
				t.Fatal("duplicate forwarded")
			}
			next := `{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"},{"role":"tool","tool_call_id":"call-1","content":"result"}]}`
			second := send(next, relay.token, relay.origin)
			want := int32(1)
			if mode == "tool" {
				want = 2
				if second.Code != 200 {
					t.Fatal("valid next tool result rejected")
				}
			} else if second.Code == 200 {
				t.Fatal("changed uncertain body was retried")
			}
			if calls.Load() != want || redirects.Load() != 0 {
				t.Fatalf("supplier %d redirect %d", calls.Load(), redirects.Load())
			}
		})
	}
}

func TestRelayRejectsFakeCompletionWithoutFinish(t *testing.T) {
	if _, e := completedInference("chat/completions", []byte("data: [DONE]\n\n")); e == nil {
		t.Fatal("empty stream masqueraded as completed inference")
	}
}

func TestKnownBundledProtocolCompletionAndResultIDs(t *testing.T) {
	for _, tc := range []struct{ operation, response, body string }{{"responses", "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call\"}}\n\ndata: {\"type\":\"response.completed\"}\n\n", `{"input":[{"type":"function_call_output","call_id":"call","output":"ok"}]}`}, {"messages", "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"call\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":"ok"}]}]}`}} {
		t.Run(tc.operation, func(t *testing.T) {
			ids, e := completedInference(tc.operation, []byte(tc.response))
			if e != nil || len(ids) != 1 || ids[0] != "call" {
				t.Fatal("protocol tool identity", ids, e)
			}
			var body map[string]json.RawMessage
			_ = json.Unmarshal([]byte(tc.body), &body)
			results := resultIDs(tc.operation, body)
			if len(results) != 1 || results[0] != "call" {
				t.Fatal("protocol result linkage", results)
			}
			if _, e = completedInference(tc.operation, []byte(strings.Split(tc.response, "\n\n")[0]+"\n\n")); e == nil {
				t.Fatal("partial protocol completion accepted")
			}
		})
	}
}

func TestResponsesAndAnthropicRelayOperationsCredentialsAndNextStep(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			db, e := store.Open(filepath.Join(t.TempDir(), "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "P", "p")
			if e != nil {
				t.Fatal(e)
			}
			turns := NewTurns(db)
			_, e = turns.Begin(context.Background(), p.ID, "session", "native")
			if e != nil {
				t.Fatal(e)
			}
			operation := "responses"
			response := "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
			first := `{"model":"fixture","stream":true,"input":[{"role":"user","content":"hello"}]}`
			next := `{"model":"fixture","stream":true,"input":[{"role":"user","content":"hello"},{"type":"function_call_output","call_id":"call","output":"ok"}]}`
			if protocol == "anthropic" {
				operation = "messages"
				response = "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"call\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
				first = `{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hello"}]}`
				next = `{"model":"fixture","stream":true,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":"ok"}]}]}`
			}
			var calls atomic.Int32
			supplier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/v1/"+operation {
					t.Error("unfrozen operation", r.URL.Path)
				}
				if protocol == "anthropic" {
					if r.Header.Get("X-Api-Key") != "supplier" || r.Header.Get("Anthropic-Version") != "2023-06-01" || r.Header.Get("Authorization") != "" {
						t.Error("anthropic credentials/version")
					}
				} else if r.Header.Get("Authorization") != "Bearer supplier" {
					t.Error("responses credentials")
				}
				if r.Header.Get("Origin") != "" || r.Header.Get("X-Opencode-Session-Id") != "" {
					t.Error("private relay headers escaped")
				}
				fmt.Fprint(w, response)
			}))
			defer supplier.Close()
			relay, e := newRelay(db, LLMConfig{Protocol: protocol, BaseURL: supplier.URL + "/v1", ModelID: "fixture"}, "supplier", "http://tauri.localhost")
			if e != nil {
				t.Fatal(e)
			}
			relay.Turns = turns
			send := func(body string) int {
				r := httptest.NewRequest("POST", inferencePath+"/"+operation, strings.NewReader(body))
				r.Header.Set("Origin", relay.origin)
				r.Header.Set("X-Opencode-Session-Id", "native")
				if protocol == "anthropic" {
					r.Header.Set("X-Api-Key", relay.token)
				} else {
					r.Header.Set("Authorization", "Bearer "+relay.token)
				}
				w := httptest.NewRecorder()
				relay.ServeHTTP(w, r)
				return w.Code
			}
			if send(first) != 200 || send(next) != 200 || send(next) == 200 || calls.Load() != 2 {
				t.Fatal("known protocol step/replay failed", calls.Load())
			}
		})
	}
}
