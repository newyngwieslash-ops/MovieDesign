package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/agent"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"image"
	"image/png"
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
func TestPinnedNativeParallelToolsQuestionAndReplay(t *testing.T) { runNativeTools(t, false) }
func TestPinnedNativeMediaTextBillingRulesCanvas(t *testing.T)    { runNativeTools(t, true) }
func runNativeTools(t *testing.T, extended bool) {
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
	var inference http.Handler
	gateway := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/inference/") {
			if inference == nil {
				w.WriteHeader(503)
				return
			}
			inference.ServeHTTP(w, r)
			return
		}
		if r.Method == "POST" {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var rpc map[string]any
			_ = json.Unmarshal(raw, &rpc)
			traceMu.Lock()
			rpcTrace = append(rpcTrace, rpc)
			traceMu.Unlock()
			if rpc["method"] == "tools/call" && !extended {
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
		if extended && n != 1 {
			if n == 2 {
				if tools, _ := body["tools"].([]any); len(tools) != 0 {
					t.Errorf("text native tools leaked: %v", tools)
				}
				emit(map[string]any{"role": "assistant", "content": "# Native text document"}, nil)
				emit(map[string]any{}, "stop")
			} else if n == 3 || n == 4 {
				name, args := "design_design_project_rules", `{"action":"save","requestId":"native-rules","content":"人物名字保持一致","expectedRevision":null}`
				if n == 4 {
					name = "design_design_execute"
					args = `{"requestId":"native-image","capabilityId":"image:local","prompt":"synthetic image","params":{},"references":[],` + output + `}`
				}
				emit(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint("step", n), "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}, nil)
				emit(map[string]any{}, "tool_calls")
			} else {
				emit(map[string]any{"role": "assistant", "content": "Local complete"}, nil)
				emit(map[string]any{}, "stop")
			}
		} else if n == 1 {
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
			if extended {
				args := `{"requestId":"native-text","capabilityId":"text:configured-llm","prompt":"Write a document","params":{},"references":[],` + output + `}`
				emit(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "nested-text", "type": "function", "function": map[string]any{"name": "design_design_execute", "arguments": args}}}}, nil)
				emit(map[string]any{}, "tool_calls")
			} else {
				question := `{"questions":[{"header":"Fixture","question":"Choose fixture outcome","options":[{"label":"Continue","description":"Finish local fixture"}]}]}`
				emit(map[string]any{"role": "assistant", "tool_calls": []any{
					map[string]any{"index": 0, "id": "native_parallel_a", "type": "function", "function": map[string]any{"name": "design_design_capability_list", "arguments": "{}"}},
					map[string]any{"index": 1, "id": "native_parallel_b", "type": "function", "function": map[string]any{"name": "design_design_composition_search", "arguments": "{}"}},
					map[string]any{"index": 2, "id": "native_question", "type": "function", "function": map[string]any{"name": "question", "arguments": question}},
				}}, nil)
				emit(map[string]any{}, "tool_calls")
			}
		} else {
			emit(map[string]any{"role": "assistant", "content": "Local fixture complete."}, nil)
			emit(map[string]any{}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	owned := t.TempDir()
	secrets, e := providers.OpenSecrets(filepath.Join(owned, "secrets"))
	if e != nil {
		t.Fatal(e)
	}
	settings := &agent.Settings{DB: f.db, Secrets: secrets}
	input, _ := json.Marshal(map[string]any{"requestId": "native-relay", "action": "save", "config": agent.LLMConfig{Protocol: "openai-compatible", BaseURL: model.URL + "/v1", ModelID: "fixture-model", Context: 32000, Output: 4096, Pricing: &providers.Pricing{Currency: "USD", Unit: "token", UnitAmount: 1, MinorUnitScale: 6, CreditNumerator: 1, CreditDenominator: 1, Source: "synthetic upper bound", Version: "v1"}}, "key": "synthetic-fixture-key"})
	if _, e = settings.Execute(ctx, input); e != nil {
		t.Fatal(e)
	}
	host, e := agent.NewHost(settings, filepath.Join(owned, "bootstrap"), f.server.Connection())
	if e != nil {
		t.Fatal(e)
	}
	host.Relay.Turns = f.server.Turns
	f.server.Turns.Abort = host.Abort
	inference = host.Relay
	qs := questions.NewService(f.db, f.tasks, f.tasks.Events)
	if extended {
		var imagePosts atomic.Int32
		imageSupplier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			imagePosts.Add(1)
			var b bytes.Buffer
			png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 6)))
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(b.Bytes())}}})
		}))
		defer imageSupplier.Close()
		defer func() {
			if imagePosts.Load() != 1 {
				t.Errorf("image posts=%d", imagePosts.Load())
			}
		}()
		f.db.Exec("UPDATE provider_configs SET config_json=json_set(config_json,'$.base_url',?) WHERE id='local'", imageSupplier.URL)
		d := delivery.NewService(f.db, f.providers.Files.Workspace, "agent")
		d.CompleteTask = f.tasks.CompleteDelivery
		f.tasks.Executor = tasks.InstallText(tasks.ImageExecutor(f.providers, d), f.providers, d, host.Text, host.QueryText)
		f.server.services.Generation.TextProvider = host.TextProvider
		f.server.services.Delivery = d
		f.server.services.Files = f.providers.Files
		f.server.services.Questions = qs
	}
	bootstrapStore, e := providers.OpenSecrets(filepath.Join(owned, "bootstrap"))
	if e != nil {
		t.Fatal(e)
	}
	launch, e := bootstrapStore.Get("launch")
	if e != nil {
		t.Fatal(e)
	}
	var bootstrap agent.Bootstrap
	if e = json.Unmarshal([]byte(launch), &bootstrap); e != nil {
		t.Fatal(e)
	}
	config := []byte(bootstrap.Config)
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
	command.Env = append(command.Env, "OPENCODE_CONFIG_CONTENT="+string(config), "MOVIEDESIGN_LLM_KEY="+bootstrap.Key, "MOVIEDESIGN_TRUNCATE_GLOB="+string(glob[1:len(glob)-1]), "OPENCODE_SERVER_PASSWORD="+bootstrap.Password)
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
		r.SetBasicAuth("opencode", bootstrap.Password)
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
	eventReq.SetBasicAuth("opencode", bootstrap.Password)
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
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var question agent.NativeQuestion
	var nativeTrace []map[string]any
	done := false
	for !done {
		select {
		case <-ctx.Done():
			t.Fatal("native trace timeout", ctx.Err())
		case <-ticker.C:
			if extended {
				cards, e := qs.List(ctx, scope.ProjectID, scope.SessionID)
				if e != nil {
					t.Fatal(e)
				}
				for _, card := range cards {
					if card.State == "pending" {
						_, e = qs.Reply(ctx, scope.ProjectID, card.ID, questions.HumanReply{RequestID: "human-" + card.ID, SessionID: scope.SessionID, ExpectedRevision: card.Revision, Answers: [][]string{{card.Questions[0].Options[0].Label}}})
						if e != nil {
							t.Fatal(e)
						}
					}
				}
			}
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
			if kind == "session.idle" && (question.ID != "" || extended && requests.Load() == 5) {
				done = true
			}
		}
	}
	var count int
	f.db.QueryRow("SELECT call_count FROM chat_turns WHERE id=?", scope.TurnID).Scan(&count)
	if count != 3 {
		t.Fatal("parallel tools/question/replay budget", count)
	}
	expectedRequests := int32(2)
	if extended {
		expectedRequests = 5
	}
	if requests.Load() != expectedRequests {
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
	expectedCalls := 2
	if extended {
		expectedCalls = 3
	}
	if calls != expectedCalls {
		t.Fatal("MCP executions", calls)
	}
	if extended {
		doc, e := (store.Canvas{DB: f.db}).Document(ctx, scope.ProjectID)
		if e != nil || len(doc.Nodes) != 2 {
			t.Fatalf("actual Canvas nodes %v %v", doc, e)
		}
		var completed, versions int
		f.db.QueryRow("SELECT count(*) FROM tasks WHERE status='completed'").Scan(&completed)
		f.db.QueryRow("SELECT count(*) FROM versions").Scan(&versions)
		if completed != 2 || versions != 2 {
			t.Fatalf("tasks=%d versions=%d", completed, versions)
		}
		var agents, receipts int
		f.db.QueryRow("SELECT count(*) FROM versions WHERE origin='agent'").Scan(&agents)
		f.db.QueryRow("SELECT count(*) FROM question_receipts WHERE actor='human'").Scan(&receipts)
		if agents != 2 || receipts != 3 {
			t.Fatalf("actor/billing/rules receipt mismatch: agent=%d human=%d", agents, receipts)
		}
		rules, e := qs.ReadRules(ctx, scope.ProjectID)
		if e != nil || rules.Content != "人物名字保持一致" {
			t.Fatal(rules, e)
		}
		for _, node := range doc.Nodes {
			if node["activeItem"] == nil {
				t.Fatal("Canvas has no real Version", node)
			}
		}
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
	if extended {
		t.Log("actual pinned native 1.19.0: nested text tools=0, exact 18+question catalog, 2 human billing receipts, human rules receipt, one image POST, 2 completed tasks/Versions/Canvas nodes; synthetic supplier only")
		return
	}
	t.Log("actual pinned native 1.19.0: two parallel MCP calls + native question, model HTTP requests=2, durable budget=3; question event replay retains one card; RPC IDs do not contain native call IDs; no real inference")
}
