package agent

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

type streamFixture struct {
	db      *sql.DB
	relay   *Relay
	scope   ToolScope
	gateway *httptest.Server
	calls   atomic.Int32
	message Message
}

func newStreamFixture(t *testing.T, protocol string, serve http.HandlerFunc) *streamFixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	p, err := (store.Objects{DB: db}).CreateProject(context.Background(), "create", "P")
	if err != nil {
		t.Fatal(err)
	}
	turns := NewTurns(db)
	scope, err := turns.Begin(context.Background(), p.ID, "session", "native")
	if err != nil {
		t.Fatal(err)
	}
	f := &streamFixture{db: db, scope: scope}
	supplier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		f.calls.Add(1)
		serve(w, r)
	}))
	t.Cleanup(supplier.Close)
	f.relay, err = newRelay(db, LLMConfig{Protocol: protocol, BaseURL: supplier.URL, ModelID: "fixture"}, "synthetic-private", "http://tauri.localhost")
	if err != nil {
		t.Fatal(err)
	}
	f.relay.Turns = turns
	f.message = authorizeRelayFixture(t, f.relay, scope)
	f.gateway = httptest.NewServer(f.relay)
	t.Cleanup(f.gateway.Close)
	return f
}

func (f *streamFixture) request(t *testing.T, operation, body string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "POST", f.gateway.URL+inferencePath+"/"+operation, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", f.relay.origin)
	req.Header.Set("X-Opencode-Session-Id", f.scope.NativeSessionID)
	req.Header.Set("Authorization", "Bearer "+f.relay.token)
	if f.relay.config.Protocol == "anthropic" {
		req.Header.Set("X-Api-Key", f.relay.token)
	}
	response, err := f.gateway.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestRelaySafePrefixDurableAndToolSuffixByteIdentical(t *testing.T) {
	for _, tc := range []struct{ protocol, operation, prefix, tool, terminal string }{
		{"openai-compatible", "chat/completions", "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"哈哈 first\"},\"finish_reason\":null}]}\r\n\r\n", "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"type\":\"function\",\"function\":{\"name\":\"owned\",\"arguments\":\"{\\\"value\\\":1}\"}}]},\"finish_reason\":null}]}\n\n", "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"},
		{"openai", "responses", "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r\",\"status\":\"in_progress\",\"output\":[]}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"m\",\"output_index\":0,\"content_index\":0,\"delta\":\"哈哈 first\"}\n\n", "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"call_id\":\"call\",\"name\":\"owned\",\"arguments\":\"{\\\"value\\\":1}\"}}\n\n", "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"},
		{"anthropic", "messages", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"stop_reason\":null}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"哈哈 first\"}}\n\n", "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call\",\"name\":\"owned\",\"input\":{\"value\":1}}}\n\n", "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			finish := make(chan struct{})
			defer close(finish)
			f := newStreamFixture(t, tc.protocol, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				// Deliberately split inside a UTF8 rune and SSE delimiters.
				for _, b := range []byte(tc.prefix + tc.tool) {
					if _, err := w.Write([]byte{b}); err != nil {
						return
					}
				}
				w.(http.Flusher).Flush()
				select {
				case <-finish:
				case <-r.Context().Done():
					return
				}
				fmt.Fprint(w, tc.terminal)
			})
			body := `{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`
			if tc.operation == "responses" {
				body = `{"model":"fixture","stream":true,"input":[{"role":"user","content":"hi"}]}`
			}
			response := f.request(t, tc.operation, body)
			got := make([]byte, len(tc.prefix))
			if _, err := io.ReadFull(response.Body, got); err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.prefix {
				t.Fatal("prefix rewritten")
			}
			rows, err := f.db.Query("SELECT frame,text_delta FROM inference_stream WHERE turn_id=? ORDER BY seq", f.scope.TurnID)
			if err != nil {
				t.Fatal(err)
			}
			var durable, text strings.Builder
			for rows.Next() {
				var frame []byte
				var delta string
				if err = rows.Scan(&frame, &delta); err != nil {
					t.Fatal(err)
				}
				durable.Write(frame)
				text.WriteString(delta)
			}
			rows.Close()
			if durable.String() != tc.prefix || text.String() != "哈哈 first" {
				t.Fatal("visible prefix not durable", durable.String(), text.String())
			}
			var state string
			if err = f.db.QueryRow("SELECT state FROM inference_requests WHERE turn_id=?", f.scope.TurnID).Scan(&state); err != nil || state != "forwarding" {
				t.Fatal(state, err)
			}
			// No suffix byte, including tool arguments, can arrive before validation.
			suffix := make(chan string, 1)
			go func() { b, _ := io.ReadAll(response.Body); suffix <- string(b) }()
			select {
			case b := <-suffix:
				t.Fatal("tool/control suffix exposed before completion", b)
			case <-time.After(75 * time.Millisecond):
			}
			// A separate SQL read completed while supplier was held: no network Tx.
			finish <- struct{}{}
			if b := <-suffix; b != tc.tool+tc.terminal {
				t.Fatal("suffix rewritten or reordered", b)
			}
			var ids string
			f.db.QueryRow("SELECT state,tool_ids_json FROM inference_requests WHERE turn_id=?", f.scope.TurnID).Scan(&state, &ids)
			if state != "completed" || ids != `["call"]` || f.calls.Load() != 1 {
				t.Fatal(state, ids, f.calls.Load())
			}
		})
	}
}

func TestRelayUnknownAndMixedFramesStopPrefixWithoutRewriting(t *testing.T) {
	for _, opaque := range []string{
		"event: vendor.new\ndata: {\"new\":true}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"mixed\",\"tool_calls\":[{\"id\":\"call\"}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"safe-looking\"}}]}\n: comment\rdata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call\"}]}}]}\n\n",
	} {
		t.Run(opaque, func(t *testing.T) {
			if _, safe := safeInferenceFrame("chat/completions", []byte(opaque)); safe {
				t.Fatal("ambiguous frame allowed")
			}
			finish := make(chan struct{})
			defer close(finish)
			first := "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n"
			later := "data: {\"choices\":[{\"delta\":{\"content\":\"later\"}}]}\n\n"
			terminal := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			f := newStreamFixture(t, "openai-compatible", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, first+opaque+later)
				w.(http.Flusher).Flush()
				select {
				case <-finish:
				case <-r.Context().Done():
					return
				}
				fmt.Fprint(w, terminal)
			})
			response := f.request(t, "chat/completions", `{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
			b := make([]byte, len(first))
			io.ReadFull(response.Body, b)
			var count int
			f.db.QueryRow("SELECT count(*) FROM inference_stream").Scan(&count)
			if string(b) != first || count != 1 {
				t.Fatal("unknown frame did not stop prefix", count)
			}
			finish <- struct{}{}
			rest, err := io.ReadAll(response.Body)
			if err != nil || string(rest) != opaque+later+terminal {
				t.Fatal("fallback changed bytes", string(rest), err)
			}
		})
	}
}

func TestRelayInterruptedPartialReopensOriginalUnknownWithoutResend(t *testing.T) {
	for _, existing := range []string{"", "哈哈 ", "different native text"} {
		t.Run(existing, func(t *testing.T) {
			prefix := "data: {\"choices\":[{\"delta\":{\"content\":\"哈哈 full prefix\"}}]}\n\n"
			tool := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call\",\"function\":{\"name\":\"owned\",\"arguments\":\"{\\\"value\\\":\"}}]}}]}\n\n"
			f := newStreamFixture(t, "openai-compatible", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, prefix+tool) })
			var err error
			_, err = f.db.Exec("UPDATE chat_dispatch SET native_message_id='native-parent' WHERE message_id=?", f.message.ID)
			if err != nil {
				t.Fatal(err)
			}
			chat := &Chat{DB: f.db}
			if existing != "" {
				part := nativePart{ID: "text-part", SessionID: f.scope.NativeSessionID, MessageID: "native-assistant", Type: "text", Text: existing}
				// savePart's Notify is harmless through the real event service.
				chat.Events = events.NewService(f.db)
				if err = chat.savePart(context.Background(), f.scope, f.message.ID, 1, part); err != nil {
					t.Fatal(err)
				}
			}
			response := f.request(t, "chat/completions", `{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
			b, readErr := io.ReadAll(response.Body)
			if readErr == nil || string(b) != prefix {
				t.Fatal("incomplete stream escaped as success/tool", string(b), readErr)
			}
			var state string
			f.db.QueryRow("SELECT state FROM inference_requests WHERE turn_id=?", f.scope.TurnID).Scan(&state)
			if state != "unknown" {
				t.Fatal(state)
			}
			if _, err = f.db.Exec("UPDATE chat_dispatch SET state='unknown' WHERE message_id=?", f.message.ID); err != nil {
				t.Fatal(err)
			}
			path := ""
			if err = f.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
				t.Fatal(err)
			}
			if err = f.db.Close(); err != nil {
				t.Fatal(err)
			}
			f.db, err = store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.db.Close()
			chat.DB = f.db
			f.relay.DB = f.db
			for range 2 {
				if err = chat.restoreStreamPartials(); err != nil {
					t.Fatal(err)
				}
			}
			messages, err := chat.Messages(context.Background(), f.scope.ProjectID, f.scope.SessionID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			for _, message := range messages {
				if message.Role == "user" {
					if message.ID != f.message.ID || message.RequestID != "original-key" || message.State != "unknown" {
						t.Fatal(message)
					}
					continue
				}
				var content struct {
					Parts []ChatMessageEnvelope `json:"parts"`
				}
				json.Unmarshal(message.Content, &content)
				for _, part := range content.Parts {
					if part.PartType == "text" {
						text.WriteString(part.Content)
						if part.State != "unknown" {
							t.Fatal(part)
						}
					}
				}
			}
			if text.String() != "哈哈 full prefix" {
				t.Fatal("recovered partial lost/duplicated", text.String())
			}
			if existing != "" {
				var raw string
				f.db.QueryRow("SELECT snapshot_json FROM chat_parts WHERE part_id='text-part'").Scan(&raw)
				if !strings.Contains(raw, existing) {
					t.Fatal("raw native audit replaced", raw)
				}
			}
			// A new Relay instance after physical SQLite reopen still refuses both
			// the original body and a changed fake tool-result follow-up.
			for _, body := range []string{`{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`, `{"model":"fixture","stream":true,"messages":[{"role":"tool","tool_call_id":"call","content":"fake"}]}`} {
				replay := f.request(t, "chat/completions", body)
				io.Copy(io.Discard, replay.Body)
				replay.Body.Close()
				if replay.StatusCode != 400 {
					t.Fatal("restart replay allowed", replay.StatusCode)
				}
			}
			if f.calls.Load() != 1 {
				t.Fatal("duplicate supplier POST", f.calls.Load())
			}
		})
	}
}

func TestRelayStreamFaultsKeepSingleAdmission(t *testing.T) {
	for _, mode := range []string{"oversize", "cancel", "write", "journal"} {
		t.Run(mode, func(t *testing.T) {
			prefix := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
			f := newStreamFixture(t, "openai-compatible", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, prefix)
				w.(http.Flusher).Flush()
				if mode == "oversize" {
					fmt.Fprint(w, ":"+strings.Repeat("x", inferenceLimit))
					return
				}
				if mode == "cancel" {
					<-r.Context().Done()
					return
				}
			})
			aborted := make(chan struct{}, 1)
			f.relay.Turns.Abort = func(context.Context, string) error { aborted <- struct{}{}; return nil }
			if mode == "journal" {
				if _, err := f.db.Exec("CREATE TRIGGER refuse_stream BEFORE INSERT ON inference_stream BEGIN SELECT RAISE(ABORT,'fixture persistence failure'); END"); err != nil {
					t.Fatal(err)
				}
			}
			body := `{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`
			if mode == "write" {
				req := httptest.NewRequest("POST", inferencePath+"/chat/completions", strings.NewReader(body))
				req.Header.Set("Origin", f.relay.origin)
				req.Header.Set("Authorization", "Bearer "+f.relay.token)
				req.Header.Set("X-Opencode-Session-Id", f.scope.NativeSessionID)
				func() {
					defer func() {
						if p := recover(); p != http.ErrAbortHandler {
							t.Fatalf("failed write did not reset SSE: %v", p)
						}
					}()
					f.relay.ServeHTTP(&streamWriteFailure{ResponseRecorder: httptest.NewRecorder()}, req)
				}()
			} else {
				response := f.request(t, "chat/completions", body)
				if mode == "cancel" {
					b := make([]byte, len(prefix))
					if _, err := io.ReadFull(response.Body, b); err != nil {
						t.Fatal(err)
					}
					response.Body.Close()
				} else {
					b, err := io.ReadAll(response.Body)
					if mode == "oversize" && (err == nil || string(b) != prefix) {
						t.Fatal("oversize exposed terminal/suffix", len(b), err)
					}
					if mode == "journal" && (response.StatusCode != 400 || strings.Contains(string(b), "partial")) {
						t.Fatal("uncommitted text visible", response.StatusCode, string(b))
					}
				}
			}
			select {
			case <-aborted:
			case <-time.After(5 * time.Second):
				t.Fatal("owned abort/drain did not settle")
			}
			var state string
			if err := f.db.QueryRow("SELECT state FROM inference_requests WHERE turn_id=?", f.scope.TurnID).Scan(&state); err != nil || state != "unknown" {
				t.Fatal(state, err)
			}
			response := f.request(t, "chat/completions", body)
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != 400 || f.calls.Load() != 1 {
				t.Fatal("fault resubmitted", response.StatusCode, f.calls.Load())
			}
		})
	}
}

type streamWriteFailure struct{ *httptest.ResponseRecorder }

func (w *streamWriteFailure) Write([]byte) (int, error) {
	return 0, errors.New("fixture disconnected writer")
}

func TestRelayJournalOmitsReflectedCredential(t *testing.T) {
	first := "data: {\"choices\":[{\"delta\":{\"content\":\"synthetic-\"}}]}\n\n"
	second := "data: {\"choices\":[{\"delta\":{\"content\":\"private\"}}]}\n\n"
	terminal := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	f := newStreamFixture(t, "openai-compatible", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, first+second+terminal) })
	response := f.request(t, "chat/completions", `{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	b, err := io.ReadAll(response.Body)
	if err == nil || string(b) != first {
		t.Fatal("known credential escaped in completed suffix", string(b), err)
	}
	var text string
	if err = f.db.QueryRow("SELECT group_concat(text_delta,'') FROM inference_stream").Scan(&text); err != nil || strings.Contains(text, f.relay.key) || text != "synthetic-" {
		t.Fatal("credential entered journal", text, err)
	}
	var state string
	f.db.QueryRow("SELECT state FROM inference_requests WHERE turn_id=?", f.scope.TurnID).Scan(&state)
	if state != "unknown" {
		t.Fatal("reflected credential accepted", state)
	}
}

func TestRelayRejectsKnownCredentialAcrossBufferedTextDeltas(t *testing.T) {
	for _, tc := range []struct{ protocol, operation, first, second, terminal string }{
		{"openai-compatible", "chat/completions", `{"choices":[{"delta":{"content":"synthetic-"}}]}`, `{"choices":[{"delta":{"content":"private"}}]}`, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{"openai", "responses", `{"type":"response.output_text.delta","delta":"synthetic-"}`, `{"type":"response.output_text.delta","delta":"private"}`, "data: {\"type\":\"response.completed\"}\n\n"},
		{"anthropic", "messages", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"synthetic-"}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"private"}}`, "data: {\"type\":\"message_stop\"}\n\n"},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			first := "data: " + tc.first + "\n\n"
			f := newStreamFixture(t, tc.protocol, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, first+"data: {\"vendor_metadata\":true}\n\n"+"data: "+tc.second+"\n\n"+tc.terminal)
			})
			body := `{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`
			if tc.operation == "responses" {
				body = strings.Replace(body, "messages", "input", 1)
			}
			response := f.request(t, tc.operation, body)
			b, err := io.ReadAll(response.Body)
			if err == nil || string(b) != first {
				t.Fatal("buffered secret suffix exposed", len(b), err)
			}
			var state string
			f.db.QueryRow("SELECT state FROM inference_requests WHERE turn_id=?", f.scope.TurnID).Scan(&state)
			if state != "unknown" || f.calls.Load() != 1 {
				t.Fatal(state, f.calls.Load())
			}
		})
	}
	if _, err := completedInference("chat/completions", []byte("data: {\"id\":\"\\u0073ynthetic-private\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"), "synthetic-private"); err == nil {
		t.Fatal("JSON-escaped credential accepted in completed metadata")
	}
}

func TestRelayRejectsEscapedCredentialMetadataBeforeJournalAndSuffix(t *testing.T) {
	for _, special := range []string{"&", "<", ">", `"`, `\`} {
		for _, buffered := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/buffered=%v", special, buffered), func(t *testing.T) {
				secret := "fixture" + special + "private"
				encoded, _ := json.Marshal(secret)
				prefix := "data: {\"choices\":[{\"delta\":{\"content\":\"safe prefix\"}}]}\n\n"
				metadata := "data: {\"id\":" + string(encoded) + ",\"choices\":[{\"delta\":{},\"finish_reason\":null}]}\n\n"
				between := ""
				if buffered {
					between = "data: {\"vendor_metadata\":true}\n\n"
				}
				terminal := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
				f := newStreamFixture(t, "openai-compatible", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, prefix+between+metadata+terminal) })
				f.relay.key = secret
				response := f.request(t, "chat/completions", `{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
				b, err := io.ReadAll(response.Body)
				if err == nil || string(b) != prefix {
					t.Errorf("escaped credential metadata released: bytes=%d read_error=%v", len(b), err)
				}
				var state string
				var count int
				f.db.QueryRow("SELECT state FROM inference_requests WHERE turn_id=?", f.scope.TurnID).Scan(&state)
				f.db.QueryRow("SELECT count(*) FROM inference_stream WHERE turn_id=?", f.scope.TurnID).Scan(&count)
				if state != "unknown" || count != 1 || f.calls.Load() != 1 {
					t.Errorf("credential accepted or journaled: state=%s journal=%d supplier=%d", state, count, f.calls.Load())
				}
			})
		}
	}
	if _, sensitive := inferenceCredential([]byte(`{"fixture\u0026private":"metadata"}`), "", "", "fixture&private"); !sensitive {
		t.Error("decoded credential in a JSON key was missed")
	}
}

func TestRelayCredentialScanHandlesLargeJSONNumbers(t *testing.T) {
	for _, buffered := range []bool{false, true} {
		for _, reflection := range []bool{false, true} {
			t.Run(fmt.Sprintf("buffered=%v/reflection=%v", buffered, reflection), func(t *testing.T) {
				secret := "fixture&private"
				id := "harmless metadata"
				if reflection {
					id = secret
				}
				encoded, _ := json.Marshal(id)
				prefix := "data: {\"choices\":[{\"delta\":{\"content\":\"safe prefix\"}}]}\n\n"
				metadata := "data: {\"created\":1e1000,\"id\":" + string(encoded) + ",\"choices\":[{\"delta\":{},\"finish_reason\":null}]}\n\n"
				between := ""
				if buffered {
					between = "data: {\"vendor_metadata\":true}\n\n"
				}
				terminal := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
				body := prefix + between + metadata + terminal
				f := newStreamFixture(t, "openai-compatible", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
				f.relay.key = secret
				response := f.request(t, "chat/completions", `{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
				b, err := io.ReadAll(response.Body)
				var state string
				var count int
				f.db.QueryRow("SELECT state FROM inference_requests WHERE turn_id=?", f.scope.TurnID).Scan(&state)
				f.db.QueryRow("SELECT count(*) FROM inference_stream WHERE turn_id=?", f.scope.TurnID).Scan(&count)
				if reflection {
					if err == nil || string(b) != prefix || state != "unknown" || count != 1 {
						t.Errorf("large number hid later credential: bytes=%d read_error=%v state=%s journal=%d", len(b), err, state, count)
					}
				} else {
					wantCount := 2
					if buffered {
						wantCount = 1
					}
					if err != nil || string(b) != body || state != "completed" || count != wantCount {
						t.Errorf("valid large-number metadata changed: bytes=%d read_error=%v state=%s journal=%d", len(b), err, state, count)
					}
				}
				if f.calls.Load() != 1 {
					t.Fatal("supplier POST repeated", f.calls.Load())
				}
			})
		}
	}
}

func TestChatStartupRecoveryAdoptsRetainedNativeTextWithoutDuplicate(t *testing.T) {
	prefix := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
	f := newStreamFixture(t, "openai-compatible", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, prefix) })
	if _, err := f.db.Exec("UPDATE chat_dispatch SET native_message_id='parent' WHERE message_id=?", f.message.ID); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, "chat/completions", `{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if _, err := io.ReadAll(response.Body); err == nil {
		t.Fatal("fixture must end as incomplete")
	}
	response.Body.Close()
	if _, err := f.db.Exec("UPDATE chat_turns SET state='interrupted' WHERE id=?", f.scope.TurnID); err != nil {
		t.Fatal(err)
	}
	var nativeText atomic.Value
	nativeText.Store("")
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("recovery sent native mutation")
			w.WriteHeader(500)
			return
		}
		var messages []nativeMessage
		for i, text := range []string{nativeText.Load().(string), "step two"} {
			var message nativeMessage
			message.Info.ID = fmt.Sprint("retained-", i)
			message.Info.SessionID = f.scope.NativeSessionID
			message.Info.ParentID = "parent"
			message.Info.Role = "assistant"
			message.Parts = []nativePart{{ID: fmt.Sprint("retained-part-", i), SessionID: f.scope.NativeSessionID, MessageID: message.Info.ID, Type: "text", Text: text}}
			messages = append(messages, message)
		}
		foreign := messages[0]
		foreign.Info.ID = "foreign"
		foreign.Info.ParentID = "wrong-parent"
		messages = append(messages, foreign)
		json.NewEncoder(w).Encode(messages)
	}))
	defer native.Close()
	ev := events.NewService(f.db)
	defer ev.Close()
	chat := NewChat(f.db, NewTurns(f.db), ev, nil)
	defer chat.Close()
	// Start performs production startup restoration synchronously. A nil relay
	// pauses only the background worker, allowing retained snapshots in a fixed
	// order through the actual reconcileInterrupted entrypoint below.
	if err := chat.Start(&Host{base: native.URL, client: native.Client()}, nil); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"", "par", "partial", "partial plus suffix", "partial plus suffix"} {
		nativeText.Store(text)
		chat.reconcileInterrupted()
		if err := chat.restoreStreamPartials(); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := chat.Messages(context.Background(), f.scope.ProjectID, f.scope.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var visible strings.Builder
	var original bool
	for _, message := range messages {
		if message.Role == "user" {
			original = message.ID == f.message.ID && message.RequestID == "original-key" && message.State == "unknown"
			continue
		}
		var content struct {
			Parts []ChatMessageEnvelope `json:"parts"`
		}
		json.Unmarshal(message.Content, &content)
		for _, part := range content.Parts {
			visible.WriteString(part.Content)
		}
	}
	if !original || visible.String() != "partial plus suffixstep two" {
		t.Errorf("startup/native reconciliation duplicated or lost text: original=%v visible=%q", original, visible.String())
	}
	frames, err := ev.Read(context.Background(), f.scope.ProjectID, f.scope.SessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var eventText strings.Builder
	for _, event := range frames {
		if event.Type != "chat_message" {
			continue
		}
		var part ChatMessageEnvelope
		json.Unmarshal(event.Payload, &part)
		eventText.WriteString(part.ContentDelta)
	}
	if eventText.String() != "partialstep two plus suffix" {
		t.Errorf("normalized event replay repeated prefix: %q", eventText.String())
	}
	var raw string
	f.db.QueryRow("SELECT snapshot_json FROM chat_parts WHERE session_id=? AND json_extract(snapshot_json,'$.id')='retained-part-0'", f.scope.SessionID).Scan(&raw)
	if !strings.Contains(raw, "retained-0") || f.calls.Load() != 1 {
		t.Fatal("native audit missing or inference repeated", raw, f.calls.Load())
	}
	var before, after int
	f.db.QueryRow("SELECT count(*) FROM events").Scan(&before)
	chat.reconcileInterrupted()
	chat.restoreStreamPartials()
	f.db.QueryRow("SELECT count(*) FROM events").Scan(&after)
	if before != after {
		t.Errorf("repeated recovery/snapshot created events: %d -> %d", before, after)
	}
}

func TestChatJournalPrefixUsesNativeIdentityScopeAndStepWithoutReplay(t *testing.T) {
	f := newStreamFixture(t, "openai-compatible", func(http.ResponseWriter, *http.Request) { t.Error("snapshot test must not infer") })
	if _, err := f.db.Exec("UPDATE chat_dispatch SET native_message_id='parent' WHERE message_id=?", f.message.ID); err != nil {
		t.Fatal(err)
	}
	appendText := func(step, seq int, text, project, session string) {
		t.Helper()
		if _, err := f.db.Exec("INSERT INTO inference_stream(scope,turn_id,step,seq,project_id,session_id,frame,text_delta,created_at)VALUES('chat',?,?,?,?,?,'',?,1)", f.scope.TurnID, step, seq, project, session, text); err != nil {
			t.Fatal(err)
		}
	}
	appendText(1, 1, "哈哈", f.scope.ProjectID, f.scope.SessionID)
	appendText(1, 2, " secret-from-other-project", "foreign", f.scope.SessionID)
	appendText(1, 3, " secret-from-other-session", f.scope.ProjectID, "foreign")
	appendText(2, 1, "step two", f.scope.ProjectID, f.scope.SessionID)
	var rawText atomic.Value
	rawText.Store("")
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var messages []nativeMessage
		for i, parts := range [][]string{{rawText.Load().(string), " after-tool"}, {""}} {
			var message nativeMessage
			message.Info.ID = fmt.Sprint("native-", i)
			message.Info.SessionID = f.scope.NativeSessionID
			message.Info.ParentID = "parent"
			message.Info.Role = "assistant"
			for j, text := range parts {
				message.Parts = append(message.Parts, nativePart{ID: fmt.Sprintf("part-%d-%d", i, j), SessionID: f.scope.NativeSessionID, MessageID: message.Info.ID, Type: "text", Text: text})
			}
			messages = append(messages, message)
		}
		json.NewEncoder(w).Encode(messages)
	}))
	defer native.Close()
	ev := events.NewService(f.db)
	defer ev.Close()
	chat := &Chat{DB: f.db, Events: ev, Host: &Host{base: native.URL, client: native.Client()}}
	read := func(parent string) {
		t.Helper()
		if _, err := chat.readSnapshotEvents(context.Background(), f.scope, f.message.ID, parent, false); err != nil {
			t.Fatal(err)
		}
	}
	read("parent")
	var before int
	f.db.QueryRow("SELECT count(*) FROM events WHERE type='chat_message'").Scan(&before)
	read("parent")
	read("wrong-parent")
	var after int
	f.db.QueryRow("SELECT count(*) FROM events WHERE type='chat_message'").Scan(&after)
	if before != 3 || after != before {
		t.Fatal("replayed snapshot duplicated delta or crossed parent", before, after)
	}
	appendText(1, 4, "哈哈", f.scope.ProjectID, f.scope.SessionID)
	read("parent")
	rawText.Store("哈")
	read("parent")
	rawText.Store("")
	read("parent")
	messages, err := chat.Messages(context.Background(), f.scope.ProjectID, f.scope.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, message := range messages {
		if message.Role != "assistant" {
			continue
		}
		var content struct {
			Parts []ChatMessageEnvelope `json:"parts"`
		}
		json.Unmarshal(message.Content, &content)
		for _, part := range content.Parts {
			text.WriteString(part.Content)
			if !strings.HasPrefix(part.PartID, "part-") {
				t.Fatal("invented native identity", part)
			}
		}
	}
	if text.String() != "哈哈哈哈 after-toolstep two" {
		t.Fatal("wrong scope/step/text part or stale snapshot rollback", text.String())
	}
	f.db.QueryRow("SELECT count(*) FROM events WHERE type='chat_message'").Scan(&after)
	if after != before+1 {
		t.Fatal("stale native snapshot replayed text", after, before)
	}
}

// This subprocess is deliberately killed after an admitted visible prefix, so
// recovery exercises actual process/SQLite handle loss rather than a DB mock.
func TestRelayPhysicalProcessRestartRetainsReadableUnknown(t *testing.T) {
	var calls atomic.Int32
	prefix := "data: {\"choices\":[{\"delta\":{\"content\":\"physical partial\"}}]}\n\n"
	supplier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		calls.Add(1)
		fmt.Fprint(w, prefix)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(supplier.Close)
	dir := t.TempDir()
	start := func(phase string) streamProcessReady {
		t.Helper()
		readyPath := filepath.Join(dir, phase+".json")
		command := exec.Command(os.Args[0], "-test.run=^TestRelayStreamProcessHelper$", "-test.timeout=45s")
		command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
		command.Env = append(os.Environ(), "MOVIEDESIGN_STREAM_PROCESS="+phase, "MOVIEDESIGN_STREAM_DIR="+dir, "MOVIEDESIGN_STREAM_SUPPLIER="+supplier.URL)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { command.Process.Kill(); command.Wait() })
		deadline := time.NewTimer(15 * time.Second)
		defer deadline.Stop()
		for {
			b, err := os.ReadFile(readyPath)
			var ready streamProcessReady
			if err == nil && json.Unmarshal(b, &ready) == nil {
				ready.process = command.Process
				return ready
			}
			select {
			case <-deadline.C:
				t.Fatal("owned subprocess readiness timeout")
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	first := start("first")
	send := func(ready streamProcessReady) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("POST", ready.URL+inferencePath+"/chat/completions", strings.NewReader(`{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+ready.Token)
		req.Header.Set("Origin", "http://tauri.localhost")
		req.Header.Set("X-Opencode-Session-Id", ready.Scope.NativeSessionID)
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := send(first)
	b := make([]byte, len(prefix))
	if _, err := io.ReadFull(response.Body, b); err != nil || string(b) != prefix {
		t.Fatal(string(b), err)
	}
	if err := first.process.Kill(); err != nil {
		t.Fatal(err)
	}
	first.process.Wait()
	if _, err := io.ReadAll(response.Body); err == nil {
		t.Fatal("physical crash looked like completed SSE")
	}
	response.Body.Close()
	restarted := start("restart")
	history, err := http.Get(restarted.URL + "/history")
	if err != nil {
		t.Fatal(err)
	}
	var messages []Message
	err = json.NewDecoder(history.Body).Decode(&messages)
	history.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var user, partial bool
	for _, message := range messages {
		if message.Role == "user" {
			user = message.ID == restarted.MessageID && message.RequestID == "original-key" && message.State == "unknown"
		}
		if message.Role == "assistant" {
			partial = strings.Contains(string(message.Content), `"content":"physical partial"`) && strings.Contains(string(message.Content), `"state":"unknown"`)
		}
	}
	if !user || !partial {
		t.Fatal("physical restart lost original unknown/partial", messages)
	}
	for range 2 {
		replay := send(restarted)
		io.Copy(io.Discard, replay.Body)
		replay.Body.Close()
		if replay.StatusCode != 400 {
			t.Fatal("physical restart replay allowed", replay.StatusCode)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("physical restart resubmitted supplier request", calls.Load())
	}
}

type streamProcessReady struct {
	MessageID  string
	URL, Token string
	Scope      ToolScope
	process    *os.Process
}

func TestRelayStreamProcessHelper(t *testing.T) {
	phase := os.Getenv("MOVIEDESIGN_STREAM_PROCESS")
	if phase == "" {
		t.Skip("owned physical restart subprocess helper")
	}
	dir := os.Getenv("MOVIEDESIGN_STREAM_DIR")
	db, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	turns := NewTurns(db)
	ready := streamProcessReady{}
	if phase == "first" {
		p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "create", "P")
		if e != nil {
			t.Fatal(e)
		}
		ready.Scope, e = turns.Begin(context.Background(), p.ID, "session", "native")
		if e != nil {
			t.Fatal(e)
		}
	} else {
		b, e := os.ReadFile(filepath.Join(dir, "first.json"))
		if e != nil || json.Unmarshal(b, &ready) != nil {
			t.Fatal("missing original process scope", e)
		}
		if _, err = db.Exec("UPDATE chat_dispatch SET state='unknown' WHERE state='accepted'"); err != nil {
			t.Fatal(err)
		}
		if err = (&Chat{DB: db}).restoreStreamPartials(); err != nil {
			t.Fatal(err)
		}
	}
	relay, err := newRelay(db, LLMConfig{Protocol: "openai-compatible", BaseURL: os.Getenv("MOVIEDESIGN_STREAM_SUPPLIER"), ModelID: "fixture"}, "synthetic-private", "http://tauri.localhost")
	if err != nil {
		t.Fatal(err)
	}
	relay.Turns = turns
	if phase == "first" {
		m := authorizeRelayFixture(t, relay, ready.Scope)
		ready.MessageID = m.ID
		if _, err = db.Exec("UPDATE chat_dispatch SET native_message_id='native-parent' WHERE message_id=?", m.ID); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.Handle(inferencePath+"/", relay)
	mux.HandleFunc("/history", func(w http.ResponseWriter, r *http.Request) {
		messages, e := (&Chat{DB: db}).Messages(r.Context(), ready.Scope.ProjectID, ready.Scope.SessionID, 0, 100)
		if e != nil {
			http.Error(w, "fixture history failed", 500)
			return
		}
		json.NewEncoder(w).Encode(messages)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	ready.URL, ready.Token = server.URL, relay.token
	b, _ := json.Marshal(ready)
	if err = os.WriteFile(filepath.Join(dir, phase+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	select {}
}

// The supplier cannot complete until the client has checked for the first text.
// This catches complete-response buffering without a first-token speed threshold.
func TestRelaySafeTextBeforeSupplierCompletion(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := (store.Objects{DB: db}).CreateProject(context.Background(), "create", "P")
	if err != nil {
		t.Fatal(err)
	}
	turns := NewTurns(db)
	scope, err := turns.Begin(context.Background(), p.ID, "session", "native")
	if err != nil {
		t.Fatal(err)
	}
	written, finish := make(chan struct{}), make(chan struct{})
	supplier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"first text\"},\"finish_reason\":null}]}\n\n")
		w.(http.Flusher).Flush()
		close(written)
		select {
		case <-finish:
		case <-req.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer supplier.Close()
	relay, err := newRelay(db, LLMConfig{Protocol: "openai-compatible", BaseURL: supplier.URL, ModelID: "fixture"}, "synthetic-private", "http://tauri.localhost")
	if err != nil {
		t.Fatal(err)
	}
	relay.Turns = turns
	authorizeRelayFixture(t, relay, scope)
	gateway := httptest.NewServer(relay)
	defer gateway.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", gateway.URL+inferencePath+"/chat/completions", strings.NewReader(`{"model":"fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Origin", relay.origin)
	req.Header.Set("Authorization", "Bearer "+relay.token)
	req.Header.Set("X-Opencode-Session-Id", scope.NativeSessionID)
	observed := make(chan error, 1)
	go func() {
		response, err := gateway.Client().Do(req)
		if err == nil {
			defer response.Body.Close()
			line, readErr := bufio.NewReader(response.Body).ReadString('\n')
			err = readErr
			if err == nil && !strings.Contains(line, "first text") {
				err = fmt.Errorf("unexpected first frame: %q", line)
			}
		}
		observed <- err
	}()
	<-written
	select {
	case err = <-observed:
		close(finish)
		if err != nil {
			t.Fatal(err)
		}
		t.Log("safe text observed while supplier completion gate remained closed")
	case <-time.After(350 * time.Millisecond):
		close(finish)
		if err = <-observed; err != nil {
			t.Fatal(err)
		}
		t.Fatal("safe text withheld until supplier completion gate opened")
	}
}
