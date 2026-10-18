package agent

import (
	"context"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTextNativePromptRejectionSettlesImmediately(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "reject.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p, e := (store.Objects{DB: db}).CreateProject(ctx, "p", "P")
	if e != nil {
		t.Fatal(e)
	}
	ev := events.NewService(db)
	defer ev.Close()
	ts := tasks.NewService(db, ev, nil)
	defer ts.Close()
	c := LLMConfig{Protocol: "openai-compatible", BaseURL: "http://127.0.0.1:11434", ModelID: "model", Context: 8192, Output: 512}
	raw, _ := json.Marshal(c)
	f, e := providers.TextProvider(raw, "")
	if e != nil {
		t.Fatal(e)
	}
	v, e := ts.Submit(ctx, p.ID, tasks.Input{RequestID: "r", CapabilityID: "text:configured-llm", Prompt: "hello", Params: json.RawMessage(`{}`), Count: 1, Provider: f, Output: delivery.Output{Resources: []delivery.Resource{{Key: "main", Mode: "create", Name: "doc"}}, Composition: delivery.Composition{Name: "Doc", Type: delivery.CompositionType{Label: "Doc"}, Placements: []delivery.Placement{{Key: "main", Role: "doc"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session" {
			w.Write([]byte(`{"id":"native"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/prompt_async") {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/session/status" {
			w.Write([]byte(`{}`))
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer native.Close()
	h := &Host{base: native.URL, client: native.Client(), active: View{Config: &c}, Relay: &Relay{DB: db}}
	start := time.Now()
	out, e := h.Text(ctx, tasks.Attempt{TaskID: v.ID, ProjectID: p.ID, Number: 1, Provider: f, Input: tasks.Input{Prompt: "hello"}})
	if e == nil || !out.NotAccepted || time.Since(start) > time.Second {
		t.Fatal("native 401 did not settle safely", out, e)
	}
}

func TestTextCleanupFailureBlocksNextSessionUntilOwnedIdle(t *testing.T) {
	var aborts atomic.Int32
	allow := false
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/abort") {
			aborts.Add(1)
			if !allow {
				w.WriteHeader(500)
			}
			return
		}
		if r.URL.Path == "/session/status" {
			w.Write([]byte(`{}`))
			return
		}
		t.Error("cleanup created a new session", r.URL.Path)
	}))
	defer native.Close()
	h := &Host{base: native.URL, client: native.Client(), textSession: "owned-unsettled"}
	if e := h.cleanupText(context.Background()); e == nil || h.textSession == "" {
		t.Fatal("lost unfinished owned cleanup", e)
	}
	allow = true
	if e := h.cleanupText(context.Background()); e != nil || h.textSession != "" || aborts.Load() != 2 {
		t.Fatal("cleanup did not recover", e, aborts.Load())
	}
}

func TestTextNativeDelayedAcceptanceAndEmptyResult(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "reject.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, e := (store.Objects{DB: db}).CreateProject(ctx, "p", "P")
	if e != nil {
		t.Fatal(e)
	}
	ev := events.NewService(db)
	defer ev.Close()
	ts := tasks.NewService(db, ev, nil)
	defer ts.Close()
	c := LLMConfig{Protocol: "openai-compatible", BaseURL: "http://127.0.0.1:11434", ModelID: "model", Context: 8192, Output: 512}
	raw, _ := json.Marshal(c)
	f, e := providers.TextProvider(raw, "")
	if e != nil {
		t.Fatal(e)
	}
	v, e := ts.Submit(ctx, p.ID, tasks.Input{RequestID: "r", CapabilityID: "text:configured-llm", Prompt: "hello", Params: json.RawMessage(`{}`), Count: 1, Provider: f, Output: delivery.Output{Resources: []delivery.Resource{{Key: "main", Mode: "create", Name: "doc"}}, Composition: delivery.Composition{Name: "Doc", Type: delivery.CompositionType{Label: "Doc"}, Placements: []delivery.Placement{{Key: "main", Role: "doc"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session" {
			w.Write([]byte(`{"id":"native"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/prompt_async") {
			time.Sleep(10100 * time.Millisecond)
			db.Exec("INSERT INTO text_inference_requests(turn_id,step,fingerprint,state,created_at) SELECT id,1,'done','completed',0 FROM text_requests")
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/session/status" {
			w.Write([]byte(`{}`))
			return
		}
		var parent string
		db.QueryRow("SELECT native_message_id FROM text_requests").Scan(&parent)
		var complete int
		db.QueryRow("SELECT count(*) FROM text_inference_requests").Scan(&complete)
		if complete == 0 {
			w.Write([]byte(`[]`))
			return
		}
		json.NewEncoder(w).Encode([]any{map[string]any{"info": map[string]any{"id": "assistant", "sessionID": "native", "parentID": parent, "role": "assistant", "finish": "stop", "time": map[string]int{"completed": 1}}, "parts": []any{}}})
	}))
	defer native.Close()
	h := &Host{base: native.URL, client: native.Client(), active: View{Config: &c}, Relay: &Relay{DB: db}}
	start := time.Now()
	out, e := h.Text(ctx, tasks.Attempt{TaskID: v.ID, ProjectID: p.ID, Number: 1, Provider: f, Input: tasks.Input{Prompt: "hello"}})
	if e != nil || out.Status != tasks.Failed || out.Problem == nil || out.Problem.Code != "EMPTY_TEXT" || time.Since(start) < 10*time.Second {
		t.Fatal("delayed empty completion did not settle safely", out, e)
	}
}

func TestTextSessionNeverBorrowsMediaScopeAndRejectsToolResponse(t *testing.T) {
	for _, tool := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "tool"}[tool], func(t *testing.T) {
			db, e := store.Open(filepath.Join(t.TempDir(), "text.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			ctx := context.Background()
			p, e := (store.Objects{DB: db}).CreateProject(ctx, "p", "P")
			if e != nil {
				t.Fatal(e)
			}
			ev := events.NewService(db)
			defer ev.Close()
			ts := tasks.NewService(db, ev, nil)
			defer ts.Close()
			var posts int
			supplier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				if tool {
					w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"forbidden\"}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"))
				} else {
					w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
				}
			}))
			defer supplier.Close()
			c := LLMConfig{Protocol: "openai-compatible", BaseURL: supplier.URL, ModelID: "model", Context: 8192, Output: 512}
			raw, _ := json.Marshal(c)
			f, e := providers.TextProvider(raw, "")
			if e != nil {
				t.Fatal(e)
			}
			task, e := ts.Submit(ctx, p.ID, tasks.Input{RequestID: "text", CapabilityID: "text:configured-llm", Prompt: "exact", Params: json.RawMessage(`{}`), Count: 1, Provider: f, Output: delivery.Output{Resources: []delivery.Resource{{Key: "main", Mode: "create", Name: "doc"}}, Composition: delivery.Composition{Name: "Doc", Type: delivery.CompositionType{Label: "Document"}, Placements: []delivery.Placement{{Key: "main", Role: "文档"}}}}})
			if e != nil {
				t.Fatal(e)
			}
			relay, e := newRelay(db, c, "", "http://tauri.localhost")
			if e != nil {
				t.Fatal(e)
			}
			relay.Turns = NewTurns(db)
			media, e := relay.Turns.Begin(ctx, p.ID, "media", "native-media")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec("INSERT INTO text_requests(id,task_id,attempt,native_session_id,native_message_id,state,created_at)VALUES('text-scope',?,1,'native-text','msg_text','active',0)", task.ID); e != nil {
				t.Fatal(e)
			}
			release := relay.bindText("text-scope", "native-text")
			defer release()
			send := func(body, session string) int {
				r := httptest.NewRequest("POST", inferencePath+"/chat/completions", strings.NewReader(body))
				r.Header.Set("Origin", relay.origin)
				r.Header.Set("Authorization", "Bearer "+relay.token)
				r.Header.Set("X-Opencode-Session-Id", session)
				w := httptest.NewRecorder()
				relay.ServeHTTP(w, r)
				return w.Code
			}
			body := `{"model":"model","stream":true,"max_tokens":512,"messages":[{"role":"user","content":"exact"}]}`
			if !tool {
				// Scope is checked before reading: retiring while the body is read
				// must still refuse at the serialized supplier admission boundary.
				reader, writer := io.Pipe()
				r := httptest.NewRequest("POST", inferencePath+"/chat/completions", reader)
				r.Header.Set("Origin", relay.origin)
				r.Header.Set("Authorization", "Bearer "+relay.token)
				r.Header.Set("X-Opencode-Session-Id", "native-text")
				w := httptest.NewRecorder()
				done := make(chan struct{})
				go func() { relay.ServeHTTP(w, r); close(done) }()
				if _, e = writer.Write([]byte(body)); e != nil {
					t.Fatal(e)
				}
				release()
				writer.Close()
				<-done
				if w.Code == 200 || posts != 0 {
					t.Fatal("retired text scope forwarded", w.Code, posts)
				}
				release = relay.bindText("text-scope", "native-text")
				defer release()
			}
			if send(strings.TrimSuffix(body, "}")+`,"tools":[{"type":"function"}]}`, "native-text") == 200 || posts != 0 {
				t.Fatal("text tool grant escaped")
			}
			code := send(body, "native-text")
			if (code == 200) == tool {
				t.Fatal("wrong completion", code)
			}
			if send(body, "native-text") == 200 || posts != 1 {
				t.Fatal("duplicate supplier post", posts)
			}
			if relay.Turns.Current() != media {
				t.Fatal("text replaced media scope")
			}
		})
	}
}
