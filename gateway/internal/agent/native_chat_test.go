package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

func TestPinnedNativeGuardedInferenceFaultsAndHumanQuestion(t *testing.T) {
	binary := os.Getenv("MOVIEDESIGN_OPENCODE_TEST_BINARY")
	if binary == "" {
		t.Skip("set verified project native binary; synthetic owned supplier only")
	}
	trace := map[string]any{"version": "1.19.0", "real_inference": "UNEXECUTED"}
	for _, mode := range []string{"500", "redirect", "disconnect", "question", "stream-question", "stream-disconnect", "stream-retry"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			dir := t.TempDir()
			db, e := store.Open(filepath.Join(dir, "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			p, e := (store.Objects{DB: db}).CreateProject(ctx, "create", "P")
			if e != nil {
				t.Fatal(e)
			}
			turns := NewTurns(db)
			var suppliers, redirects, relayAttempts atomic.Int32
			streamGate := make(chan struct{})
			defer close(streamGate)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirects.Add(1) }))
			defer target.Close()
			supplier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				r.Body.Close()
				n := suppliers.Add(1)
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer supplier-private" {
					t.Error("native bypassed fixed operation/credential")
				}
				switch mode {
				case "500":
					w.WriteHeader(503)
					return
				case "redirect":
					http.Redirect(w, r, target.URL, 307)
					return
				case "disconnect":
					conn, _, e := w.(http.Hijacker).Hijack()
					if e == nil {
						_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 9999\r\n\r\ndata: {\"choices\":[{\"delta\":{\"content\":\"uncertain\"}}]}\n\n"))
						conn.Close()
					}
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(delta any, finish any) {
					b, _ := json.Marshal(map[string]any{"id": "fixture", "object": "chat.completion.chunk", "created": 1, "model": "fixture", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
					fmt.Fprintf(w, "data: %s\n\n", b)
				}
				if n == 1 {
					args := `{"questions":[{"header":"Native","question":"Continue?","options":[{"label":"Yes","description":"Proceed"}]}]}`
					if strings.HasPrefix(mode, "stream-") {
						emit(map[string]any{"role": "assistant", "content": "哈哈 streamed"}, nil)
					}
					emit(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "native_question", "type": "function", "function": map[string]string{"name": "question", "arguments": args}}}}, nil)
					if strings.HasPrefix(mode, "stream-") {
						w.(http.Flusher).Flush()
						select {
						case <-streamGate:
						case <-r.Context().Done():
							return
						}
						if mode != "stream-question" {
							panic(http.ErrAbortHandler)
						}
					}
					emit(map[string]any{}, "tool_calls")
				} else {
					emit(map[string]any{"role": "assistant", "content": "哈哈"}, nil)
					emit(map[string]any{}, "stop")
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer supplier.Close()
			c := LLMConfig{Protocol: "openai-compatible", BaseURL: supplier.URL + "/v1", ModelID: "fixture", Context: 32000, Output: 4096}
			relay, e := newRelay(db, c, "supplier-private", "http://tauri.localhost")
			if e != nil {
				t.Fatal(e)
			}
			relay.Turns = turns
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { relayAttempts.Add(1); relay.ServeHTTP(w, req) }))
			defer gateway.Close()
			config, e := BuildConfig(c, MCPConnection{})
			if e != nil {
				t.Fatal(e)
			}
			var cfg map[string]any
			_ = json.Unmarshal(config, &cfg)
			provider := cfg["provider"].(map[string]any)["moviedesign"].(map[string]any)
			provider["options"].(map[string]any)["baseURL"] = gateway.URL + inferencePath
			provider["models"].(map[string]any)["fixture"].(map[string]any)["headers"] = map[string]string{"Origin": relay.origin}
			config, _ = json.Marshal(cfg)
			listener, e := net.Listen("tcp4", "127.0.0.1:0")
			if e != nil {
				t.Fatal("native fixture port occupied; untouched")
			}
			port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
			listener.Close()
			command := exec.Command(binary, "serve", "--hostname", "127.0.0.1", "--port", port)
			command.Dir = dir
			command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
			command.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + filepath.Join(os.Getenv("SystemRoot"), "System32"), "npm_config_offline=true"}
			for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "ProgramData", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "TEMP", "TMP"} {
				path := filepath.Join(dir, key)
				_ = os.MkdirAll(path, 0700)
				command.Env = append(command.Env, key+"="+path)
			}
			glob, _ := json.Marshal(filepath.Join(dir, "XDG_DATA_HOME", "opencode", "tool-output", "*"))
			command.Env = append(command.Env, "OPENCODE_CONFIG_CONTENT="+string(config), "MOVIEDESIGN_LLM_KEY="+relay.token, "MOVIEDESIGN_TRUNCATE_GLOB="+string(glob[1:len(glob)-1]), "OPENCODE_SERVER_PASSWORD=fixture-private")
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
			h := &Host{base: "http://127.0.0.1:" + port, password: "fixture-private", client: &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}, Relay: relay}
			for !h.Ready(ctx) {
				select {
				case <-ctx.Done():
					t.Fatal("native readiness", ctx.Err())
				case <-time.After(100 * time.Millisecond):
				}
			}
			doc, e := h.request(ctx, "GET", "/doc", nil)
			if e != nil {
				t.Fatal(e)
			}
			for _, path := range []string{`"/session/{sessionID}/message"`, `"/session/{sessionID}/prompt_async"`, `"/question/{requestID}/reply"`, `"/event"`} {
				if !strings.Contains(string(doc), path) {
					t.Fatal("pinned doc route changed", path)
				}
			}
			created, e := h.request(ctx, "POST", "/session", map[string]string{"title": "MovieDesign guarded fixture"})
			if e != nil {
				t.Fatal(e)
			}
			var session struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(created, &session)
			scope, e := turns.Begin(ctx, p.ID, "fixture-session", session.ID)
			if e != nil {
				t.Fatal(e)
			}
			turns.Abort = h.Abort
			if mode == "stream-retry" {
				// Hold only the fixture abort acknowledgment to force the bundled
				// outer retry through production admission; it must not reach vendor.
				turns.Abort = func(context.Context, string) error { return nil }
			}
			ev := events.NewService(db)
			qs := questions.NewService(db, nil, ev)
			chat := NewChat(db, turns, ev, qs)
			chat.Host = h
			defer chat.Close()
			if strings.HasPrefix(mode, "stream-") {
				if _, e = db.Exec("INSERT INTO chat_messages(id,session_id,request_id,role,content_json,created_at)VALUES('fixture-local',?,'fixture-key','user','{}',1)", scope.SessionID); e != nil {
					t.Fatal(e)
				}
				if _, e = db.Exec("INSERT INTO chat_dispatch(message_id,native_message_id,turn_id,state,created_at)VALUES('fixture-local','msg_fixture1234567890',?,'accepted',1)", scope.TurnID); e != nil {
					t.Fatal(e)
				}
			}
			acceptance := make(chan error, 1)
			go func() {
				_, err := h.request(ctx, "POST", "/session/"+session.ID+"/prompt_async", map[string]any{"messageID": "msg_fixture1234567890", "agent": "build", "parts": []any{map[string]string{"type": "text", "text": "Run the owned fixture."}}})
				acceptance <- err
			}()
			if strings.HasPrefix(mode, "stream-") {
				observed := false
				var latest []nativeMessage
				for !observed {
					if _, e = chat.readSnapshotEvents(ctx, scope, "fixture-local", "msg_fixture1234567890", false); e != nil {
						t.Fatal(e)
					}
					snapshot, err := h.snapshot(ctx, session.ID)
					if err != nil {
						t.Fatal(err)
					}
					latest = snapshot
					for _, message := range snapshot {
						for _, part := range message.Parts {
							if part.Type == "tool" {
								t.Fatal("native tool executed before supplier validation")
							}
						}
					}
					messages, err := chat.Messages(ctx, p.ID, scope.SessionID, 0, 100)
					if err != nil {
						t.Fatal(err)
					}
					for _, message := range messages {
						if message.Role == "assistant" && strings.Contains(string(message.Content), "哈哈 streamed") {
							observed = true
						}
					}
					if !observed {
						select {
						case <-ctx.Done():
							var count int
							db.QueryRow("SELECT count(*) FROM inference_stream").Scan(&count)
							t.Fatalf("native safe text not visible before terminal %v supplier=%d relay=%d journal=%d snapshot=%+v", ctx.Err(), suppliers.Load(), relayAttempts.Load(), count, latest)
						case <-time.After(25 * time.Millisecond):
						}
					}
				}
				var durable string
				if e = db.QueryRow("SELECT group_concat(text_delta,'') FROM inference_stream WHERE turn_id=?", scope.TurnID).Scan(&durable); e != nil || durable != "哈哈 streamed" {
					t.Fatal("native text not durable", durable, e)
				}
				t.Log("actual native part identity + durable normalized text/history visible before supplier terminal; tool parts zero")
				streamGate <- struct{}{}
			}
			answered := false
			terminal := false
			var snapshot []nativeMessage
			for !terminal {
				if e = chat.syncQuestions(ctx, scope); e != nil {
					t.Fatal(e)
				}
				cards, e := qs.List(ctx, p.ID, scope.SessionID)
				if e != nil {
					t.Fatal(e)
				}
				if len(cards) > 0 && !answered {
					if mode != "question" && mode != "stream-question" {
						t.Fatal("unexpected question")
					}
					card := cards[0]
					_, e = qs.Reply(ctx, p.ID, card.ID, questions.HumanReply{RequestID: "human", SessionID: scope.SessionID, ExpectedRevision: card.Revision, Answers: [][]string{{"Yes"}}})
					if e != nil {
						t.Fatal(e)
					}
					answered = true
				}
				snapshot, e = h.snapshot(ctx, session.ID)
				if e != nil {
					t.Fatalf("snapshot %v supplier=%d previous=%+v", e, suppliers.Load(), snapshot)
				}
				for _, message := range snapshot {
					if message.Info.Role == "assistant" && (len(message.Info.Error) > 0 || message.Info.Time.Completed > 0 && message.Info.Finish != "tool-calls") {
						terminal = true
					}
				}
				if !terminal {
					select {
					case <-ctx.Done():
						t.Fatalf("native completion timeout supplier=%d snapshot=%+v", suppliers.Load(), snapshot)
					case <-time.After(30 * time.Millisecond):
					}
				}
			}
			if e = h.WaitIdle(ctx, session.ID); e != nil {
				t.Fatal(e)
			}
			if e = <-acceptance; e != nil {
				t.Fatal("native prompt acceptance", e)
			}
			want := int32(1)
			if mode == "question" || mode == "stream-question" {
				want = 2
				if !answered {
					t.Fatal("human reply was skipped")
				}
			}
			if suppliers.Load() != want || redirects.Load() != 0 {
				t.Fatalf("supplier requests %d expected %d; redirects %d", suppliers.Load(), want, redirects.Load())
			}
			if mode == "stream-disconnect" || mode == "stream-retry" {
				for _, message := range snapshot {
					for _, part := range message.Parts {
						if part.Type == "tool" {
							t.Fatal("interrupted native executed tool")
						}
					}
				}
				var state string
				if e = db.QueryRow("SELECT state FROM inference_requests WHERE turn_id=?", scope.TurnID).Scan(&state); e != nil || state != "unknown" {
					t.Fatal("interrupted admission", state, e)
				}
				if mode == "stream-retry" && relayAttempts.Load() < 2 {
					t.Fatal("fixture did not exercise native outer retry", relayAttempts.Load())
				}
			}
			trace[mode] = map[string]any{"supplier_requests": suppliers.Load(), "relay_attempts": relayAttempts.Load(), "redirect_requests": redirects.Load(), "human_receipt": answered, "messages": snapshot}
			t.Logf("actual pinned native %s: supplier=%d relay=%d redirect=%d human=%v", mode, suppliers.Load(), relayAttempts.Load(), redirects.Load(), answered)
		})
	}
	if output := os.Getenv("MOVIEDESIGN_NATIVE_TRACE_OUTPUT"); output != "" {
		raw, _ := json.MarshalIndent(trace, "", "  ")
		if e := os.WriteFile(output, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
