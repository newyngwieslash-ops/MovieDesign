package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/agent"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Actual pinned native process, actual model HTTP/SSE transport, synthetic owned
// model responses only. This is NOT real credential/provider inference acceptance.
func TestPinnedNativeParallelToolsQuestionAndReplay(t *testing.T) {
	binary := os.Getenv("MOVIEDESIGN_OPENCODE_TEST_BINARY")
	if binary == "" {
		t.Skip("set verified project binary for native fixture")
	}
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// setup's SDK session is closed before assigning the native session/epoch.
	f.client.Close()
	if e := f.server.Turns.End(ctx, f.scope, func(context.Context) error { return nil }, func(context.Context) error { return f.server.CloseEpoch("") }); e != nil {
		t.Fatal(e)
	}
	gatewayListener, e := net.Listen("tcp4", "127.0.0.1:8001")
	if e != nil {
		t.Fatal("gateway fixture port occupied; untouched")
	}
	var traceMu sync.Mutex
	var rpcTrace []map[string]any
	var simultaneous atomic.Int32
	both := make(chan struct{})
	gateway := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var rpc map[string]any
			_ = json.Unmarshal(raw, &rpc)
			traceMu.Lock()
			rpcTrace = append(rpcTrace, rpc)
			traceMu.Unlock()
			if rpc["method"] == "tools/call" {
				if simultaneous.Add(1) == 2 {
					close(both)
				}
				select {
				case <-both:
				case <-r.Context().Done():
					return
				case <-time.After(5 * time.Second):
					t.Error("native tools were not concurrent")
					return
				}
			}

		}
		f.server.ServeHTTP(w, r)
	})}
	go gateway.Serve(gatewayListener)
	defer gateway.Close()
	probe, e := net.Listen("tcp4", "127.0.0.1:4096")
	if e != nil {
		t.Fatal("native fixture port occupied; untouched")
	}
	probe.Close()
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected model route %s", r.URL.Path)
			http.Error(w, "fixture route", 400)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		n := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(delta any, finish any) {
			b, _ := json.Marshal(map[string]any{"id": fmt.Sprint("fixture-", n), "object": "chat.completion.chunk", "created": 1, "model": "fixture-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
			w.(http.Flusher).Flush()
		}
		if n == 1 {
			var exposed []string
			for _, tool := range body["tools"].([]any) {
				exposed = append(exposed, tool.(map[string]any)["function"].(map[string]any)["name"].(string))
			}
			expected := append(NativeNames(), "question")
			slices.Sort(expected)
			slices.Sort(exposed)
			if !slices.Equal(exposed, expected) {
				t.Errorf("native effective catalog leaked/missed tools: %v", exposed)
			}
			for _, want := range []string{"design_design_capability_list", "design_design_composition_search", "question"} {
				found := false
				for _, v := range exposed {
					if want == v {
						found = true
					}
				}
				if !found {
					t.Errorf("native effective tool absent: %s; got %v", want, exposed)
				}
			}
			question := `{"questions":[{"header":"Fixture","question":"Choose fixture outcome","options":[{"label":"Continue","description":"Finish local fixture"}]}]}`
			emit(map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"index": 0, "id": "native_parallel_a", "type": "function", "function": map[string]any{"name": "design_design_capability_list", "arguments": "{}"}},
				map[string]any{"index": 1, "id": "native_parallel_b", "type": "function", "function": map[string]any{"name": "design_design_composition_search", "arguments": "{}"}},
				map[string]any{"index": 2, "id": "native_question", "type": "function", "function": map[string]any{"name": "question", "arguments": question}},
			}}, nil)
			emit(map[string]any{}, "tool_calls")
		} else {
			emit(map[string]any{"role": "assistant", "content": "Local fixture complete."}, nil)
			emit(map[string]any{}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	config, e := agent.BuildConfig(agent.LLMConfig{Protocol: "openai-compatible", BaseURL: model.URL + "/v1", ModelID: "fixture-model", Context: 32000, Output: 4096}, f.server.Connection())
	if e != nil {
		t.Fatal(e)
	}
	owned := t.TempDir()
	command := exec.Command(binary, "serve", "--hostname", "127.0.0.1", "--port", "4096")
	command.Dir = owned
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	command.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + filepath.Join(os.Getenv("SystemRoot"), "System32"), "npm_config_offline=true"}
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "ProgramData", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "TEMP", "TMP"} {
		dir := filepath.Join(owned, key)
		os.MkdirAll(dir, 0700)
		command.Env = append(command.Env, key+"="+dir)
	}
	glob, _ := json.Marshal(filepath.Join(owned, "XDG_DATA_HOME", "opencode", "tool-output", "*"))
	command.Env = append(command.Env, "OPENCODE_CONFIG_CONTENT="+string(config), "MOVIEDESIGN_LLM_KEY=synthetic-fixture-key", "MOVIEDESIGN_TRUNCATE_GLOB="+string(glob[1:len(glob)-1]), "OPENCODE_SERVER_PASSWORD=synthetic-fixture-password")
	for _, flag := range []string{"OPENCODE_PURE", "OPENCODE_DISABLE_PROJECT_CONFIG", "OPENCODE_DISABLE_EXTERNAL_SKILLS", "OPENCODE_DISABLE_CLAUDE_CODE", "OPENCODE_DISABLE_DEFAULT_PLUGINS", "OPENCODE_DISABLE_MODELS_FETCH", "OPENCODE_DISABLE_AUTOUPDATE", "OPENCODE_DISABLE_LSP_DOWNLOAD", "OPENCODE_DISABLE_EMBEDDED_WEB_UI"} {
		command.Env = append(command.Env, flag+"=true")
	}
	var logs bytes.Buffer
	command.Stdout = &logs
	command.Stderr = &logs
	if e = command.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		command.Process.Kill()
		command.Wait()
		if t.Failed() {
			t.Log(logs.String())
		}
	}()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirect denied") }}
	request := func(method, path string, body any) ([]byte, error) {
		var reader io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		}
		r, _ := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:4096"+path, reader)
		r.SetBasicAuth("opencode", "synthetic-fixture-password")
		r.Header.Set("Content-Type", "application/json")
		response, e := client.Do(r)
		if e != nil {
			return nil, e
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return raw, fmt.Errorf("%s %s: %d %s", method, path, response.StatusCode, raw)
		}
		return raw, nil
	}
	for {
		if _, e = request("GET", "/global/health", nil); e == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	raw, e := request("POST", "/session", map[string]any{"title": "M5.2 local fixture"})
	if e != nil {
		t.Fatal(e)
	}
	var session struct {
		ID string `json:"id"`
	}
	json.Unmarshal(raw, &session)
	if session.ID == "" {
		t.Fatal(string(raw))
	}
	scope, e := f.server.Turns.Begin(ctx, f.scope.ProjectID, "native-fixture", session.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = request("POST", "/mcp/design/disconnect", nil); e != nil {
		t.Fatal(e)
	}
	f.server.CloseEpoch("")
	if _, e = request("POST", "/mcp/design/connect", nil); e != nil {
		t.Fatal(e)
	}
	eventReq, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:4096/event", nil)
	eventReq.SetBasicAuth("opencode", "synthetic-fixture-password")
	eventResponse, e := client.Do(eventReq)
	if e != nil {
		t.Fatal(e)
	}
	defer eventResponse.Body.Close()
	eventChannel := make(chan map[string]any, 100)
	go func() {
		scanner := bufio.NewScanner(eventResponse.Body)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var event map[string]any
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil {
					select {
					case eventChannel <- event:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	if _, e = request("POST", "/session/"+session.ID+"/prompt_async", map[string]any{"agent": "build", "parts": []any{map[string]any{"type": "text", "text": "Run the local fixture tools."}}}); e != nil {
		t.Fatal(e)
	}
	var question agent.NativeQuestion
	var nativeTrace []map[string]any
	done := false
	for !done {
		select {
		case <-ctx.Done():
			t.Fatal("native trace timeout", ctx.Err())
		case event := <-eventChannel:
			kind, _ := event["type"].(string)
			if kind == "message.part.updated" || kind == "question.asked" || kind == "session.idle" {
				nativeTrace = append(nativeTrace, event)
			}
			if kind == "question.asked" {
				b, _ := json.Marshal(event["properties"])
				if e = json.Unmarshal(b, &question); e != nil {
					t.Fatal(e)
				}
				if question.Tool == nil || question.Tool.CallID != "native_question" || question.ID == question.Tool.CallID {
					t.Fatal("native question association changed", string(b))
				}
				ev := events.NewService(f.db)
				qs := questions.NewService(f.db, f.tasks, ev)
				reject := func(_ context.Context, id string) error {
					_, e := request("POST", "/question/"+id+"/reject", nil)
					return e
				}
				card, e := f.server.Turns.GateQuestion(ctx, scope, question, qs, reject)
				if e != nil {
					t.Fatal(e)
				}
				replayed, e := f.server.Turns.GateQuestion(ctx, scope, question, qs, reject)
				ev.Close()
				if e != nil || card.ID != replayed.ID {
					t.Fatal("native event replay counted/duplicated", e)
				}
				if _, e = request("POST", "/question/"+question.ID+"/reply", map[string]any{"answers": [][]string{{"Continue"}}}); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "session.idle" && question.ID != "" {
				done = true
			}
		}
	}
	var count int
	f.db.QueryRow("SELECT call_count FROM chat_turns WHERE id=?", scope.TurnID).Scan(&count)
	if count != 3 {
		t.Fatal("parallel tools/question/replay budget", count)
	}
	if requests.Load() != 2 {
		t.Fatal("unexpected model request/retry count", requests.Load())
	}
	traceMu.Lock()
	recordedRPC := append([]map[string]any{}, rpcTrace...)
	traceMu.Unlock()
	calls := 0
	for _, rpc := range recordedRPC {
		if rpc["method"] == "tools/call" {
			calls++
			b, _ := json.Marshal(rpc)
			if strings.Contains(string(b), "native_parallel_") {
				t.Fatal("unexpected native ID forwarding", string(b))
			}
		}
	}
	if calls != 2 {
		t.Fatal("MCP executions", calls)
	}
	if !f.server.CatalogReady() {
		t.Fatal("native catalog handshake absent")
	}
	if e = f.server.Turns.End(ctx, scope, func(context.Context) error { _, e := request("GET", "/session/status", nil); return e }, func(context.Context) error {
		_, e := request("POST", "/mcp/design/disconnect", nil)
		if e != nil {
			return e
		}
		return f.server.CloseEpoch(scope.Epoch)
	}); e != nil {
		t.Fatal(e)
	}
	if path := os.Getenv("MOVIEDESIGN_NATIVE_TRACE_OUTPUT"); path != "" {
		trace, _ := json.MarshalIndent(map[string]any{"version": "1.19.0", "model_requests": requests.Load(), "budget_count": count, "rpc": recordedRPC, "native_events": nativeTrace}, "", "  ")
		if e = os.WriteFile(path, trace, 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Log("actual pinned native 1.19.0: two parallel MCP calls + native question, model HTTP requests=2, durable budget=3; question event replay retains one card; RPC IDs do not contain native call IDs; no real inference")
}
