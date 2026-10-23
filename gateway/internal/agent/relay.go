package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

const inferencePath = "/internal/inference"
const inferenceLimit = 32 << 20

// Relay owns a frozen configuration and supplier credential. Native only receives
// the short-lived relay token, never the supplier key or destination.
type Relay struct {
	credentialVersion  string
	DB                 *sql.DB
	Turns              *Turns
	config             LLMConfig
	key, token, origin string
	client             *http.Client
	mu                 sync.Mutex
	textMu             sync.Mutex
	textScopes         map[string]string
}

func (r *Relay) bindText(id, native string) func() {
	r.textMu.Lock()
	if r.textScopes == nil {
		r.textScopes = map[string]string{}
	}
	r.textScopes[native] = id
	r.textMu.Unlock()
	return func() { r.textMu.Lock(); delete(r.textScopes, native); r.textMu.Unlock() }
}
func inferenceTable(s ToolScope) string {
	if s.Epoch == "text" {
		return "text_inference_requests"
	}
	return "inference_requests"
}

func newRelay(db *sql.DB, c LLMConfig, key, origin string) (*Relay, error) {
	client, e := providers.NewInferenceClient(c.BaseURL)
	if e != nil {
		return nil, e
	}
	return &Relay{DB: db, config: c, key: key, token: uuid.NewString(), origin: origin, client: client}, nil
}

func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	fail := func() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"Inference unavailable or acceptance uncertain; automatic resubmission denied"}}`)
	}
	token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if r.config.Protocol == "anthropic" {
		token = req.Header.Get("X-Api-Key")
	}
	if req.Method != "POST" || len(req.Header.Values("Origin")) != 1 || req.Header.Get("Origin") != r.origin || subtle.ConstantTimeCompare([]byte(token), []byte(r.token)) != 1 || req.URL.RawQuery != "" || r.Turns == nil {
		fail()
		return
	}
	operation := strings.TrimPrefix(req.URL.Path, inferencePath+"/")
	expected := map[string]string{"openai-compatible": "chat/completions", "openai": "responses", "anthropic": "messages"}[r.config.Protocol]
	if operation != expected {
		fail()
		return
	}
	scope := r.Turns.Current()
	r.textMu.Lock()
	textID := r.textScopes[req.Header.Get("X-Opencode-Session-Id")]
	r.textMu.Unlock()
	if textID != "" {
		scope = ToolScope{TurnID: textID, NativeSessionID: req.Header.Get("X-Opencode-Session-Id"), Epoch: "text"}
		if e := r.DB.QueryRowContext(req.Context(), "SELECT t.project_id,t.session_id FROM text_requests r JOIN tasks t ON t.id=r.task_id WHERE r.id=? AND r.native_session_id=?", textID, scope.NativeSessionID).Scan(&scope.ProjectID, &scope.SessionID); e != nil {
			fail()
			return
		}
	}
	if scope.TurnID == "" || req.Header.Get("X-Opencode-Session-Id") != scope.NativeSessionID {
		fail()
		return
	}
	var cancelling int
	check := "SELECT count(*) FROM chat_dispatch WHERE turn_id=? AND state='cancelling'"
	if textID != "" {
		check = "SELECT count(*) FROM text_requests r JOIN tasks t ON t.id=r.task_id WHERE r.id=? AND (r.state<>'active' OR t.status='cancelled')"
	}
	if e := r.DB.QueryRowContext(req.Context(), check, scope.TurnID).Scan(&cancelling); e != nil || cancelling > 0 {
		fail()
		return
	}
	release := func() {}
	var e error
	if textID == "" {
		release, e = r.Turns.Acquire(scope)
	}
	if e != nil {
		fail()
		return
	}
	defer release()
	raw, e := io.ReadAll(http.MaxBytesReader(w, req.Body, inferenceLimit))
	if e != nil {
		fail()
		return
	}
	canonical, e := store.CanonicalJSON(raw)
	if e != nil {
		fail()
		return
	}
	if textID == "" {
		raw, e = chatRequest(canonical, operation, r.config)
		if e != nil {
			fail()
			return
		}
		canonical, e = store.CanonicalJSON(raw)
		if e != nil {
			fail()
			return
		}
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(canonical, &body)
	var model string
	var stream bool
	if json.Unmarshal(body["model"], &model) != nil || model != r.config.ModelID || json.Unmarshal(body["stream"], &stream) != nil || !stream {
		fail()
		return
	}
	digest := sha256.Sum256(canonical)
	fingerprint := hex.EncodeToString(digest[:])
	results := resultIDs(operation, body)
	if textID != "" {
		var toolList []any
		if b := body["tools"]; len(b) > 0 && (json.Unmarshal(b, &toolList) != nil || len(toolList) > 0) {
			fail()
			return
		}
		if len(results) > 0 {
			fail()
			return
		}
		limit := body["max_tokens"]
		if operation == "responses" {
			limit = body["max_output_tokens"]
		}
		if len(body["max_completion_tokens"]) > 0 {
			limit = body["max_completion_tokens"]
		}
		var tokens int
		if json.Unmarshal(limit, &tokens) != nil || tokens < 1 || tokens > r.config.Output || len(canonical) > r.config.Context-r.config.Output {
			fail()
			return
		}
	}
	// ponytail: one inference at a time, matching the application's single owned turn.
	r.mu.Lock()
	defer r.mu.Unlock()
	if textID != "" {
		r.textMu.Lock()
		active := r.textScopes[scope.NativeSessionID] == textID
		r.textMu.Unlock()
		if !active {
			fail()
			return
		}
	}
	// A queued model request may outlive cancellation or text-scope retirement.
	if e := r.DB.QueryRowContext(req.Context(), check, scope.TurnID).Scan(&cancelling); e != nil || cancelling > 0 {
		fail()
		return
	}
	ctx := context.Background()
	step, e := r.admit(ctx, scope, fingerprint, results)
	if e != nil {
		fail()
		return
	}
	endpoint, e := providers.Endpoint(r.config.BaseURL, operation)
	if e != nil {
		fail()
		return
	}
	upstreamCtx, upstreamCancel := context.WithTimeout(req.Context(), 10*time.Minute)
	defer upstreamCancel()
	upstream, e := http.NewRequestWithContext(upstreamCtx, "POST", endpoint, bytes.NewReader(raw))
	if e != nil {
		fail()
		return
	}
	upstream.GetBody = nil // No transport replay, even if a future client enables keep-alive.
	upstream.Header.Set("Content-Type", "application/json")
	if r.config.Protocol == "anthropic" {
		upstream.Header.Set("x-api-key", r.key)
		upstream.Header.Set("anthropic-version", "2023-06-01")
	} else if r.key != "" {
		upstream.Header.Set("Authorization", "Bearer "+r.key)
	}
	response, e := r.client.Do(upstream)
	var data []byte
	var ids []string
	prefix, started := 0, false
	if e == nil {
		defer response.Body.Close()
		if response.StatusCode == 200 {
			data, prefix, started, e = r.readInference(upstreamCtx, w, response.Body, scope, step, operation)
			if e == nil {
				ids, e = completedInference(operation, data, r.key, r.token)
			}
		} else {
			e = errors.New("inference response rejected")
		}
	}
	state := "completed"
	if textID != "" && len(ids) > 0 {
		e = errors.New("tools forbidden in text scope")
	}
	if e != nil {
		state = "unknown"
		if response != nil && response.StatusCode >= 400 && response.StatusCode < 500 {
			state = "rejected"
		}
	}
	encoded, _ := json.Marshal(ids)
	saveCtx, saveCancel := context.WithTimeout(ctx, 5*time.Second)
	_, saveErr := r.DB.ExecContext(saveCtx, "UPDATE "+inferenceTable(scope)+" SET state=?,tool_ids_json=? WHERE turn_id=? AND step=?", state, string(encoded), scope.TurnID, step)
	saveCancel()
	if saveErr == nil && e == nil {
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			started = true
		}
		e = writeInference(w, data[prefix:])
		if e != nil {
			saveCtx, saveCancel = context.WithTimeout(ctx, 5*time.Second)
			_, _ = r.DB.ExecContext(saveCtx, "UPDATE "+inferenceTable(scope)+" SET state='unknown' WHERE turn_id=? AND step=?", scope.TurnID, step)
			saveCancel()
		}
	}
	if saveErr != nil || e != nil {
		if r.Turns.Abort != nil {
			abortCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = r.Turns.Abort(abortCtx, scope.NativeSessionID)
			cancel()
		}
		if started {
			// Reset an incomplete SSE response instead of returning a clean EOF
			// that the native provider might interpret as successful completion.
			panic(http.ErrAbortHandler)
		}
		fail()
		return
	}
}

func (r *Relay) admit(ctx context.Context, s ToolScope, fingerprint string, results []string) (int, error) {
	tx, e := r.DB.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	if s.Epoch != "text" {
		var id string
		if e = tx.QueryRowContext(ctx, `SELECT d.message_id FROM chat_dispatch d JOIN chat_messages m ON m.id=d.message_id JOIN chat_sessions c ON c.id=m.session_id JOIN chat_turns t ON t.id=d.turn_id WHERE d.turn_id=? AND c.id=? AND c.project_id=? AND c.native_session_id=? AND t.epoch=? AND t.state='active' AND d.state IN ('submitting','accepted','running') AND d.terminal=0`, s.TurnID, s.SessionID, s.ProjectID, s.NativeSessionID, s.Epoch).Scan(&id); e != nil {
			return 0, e
		}
		if e = approvedChat(ctx, tx, s.ProjectID, s.SessionID, id, r.config, r.credentialVersion); e != nil {
			return 0, e
		}
	}
	var step int
	var state, allowed, used string
	e = tx.QueryRowContext(ctx, "SELECT step,state,tool_ids_json,result_ids_json FROM "+inferenceTable(s)+" WHERE turn_id=? ORDER BY step DESC LIMIT 1", s.TurnID).Scan(&step, &state, &allowed, &used)
	if e == nil {
		if s.Epoch == "text" {
			return 0, ErrScope
		}
		if state != "completed" {
			return 0, ErrScope
		}
		var ids, previous []string
		_ = json.Unmarshal([]byte(allowed), &ids)
		_ = json.Unmarshal([]byte(used), &previous)
		fresh := map[string]bool{}
		for _, id := range results {
			fresh[id] = true
		}
		for _, id := range previous {
			delete(fresh, id)
		}
		if len(ids) == 0 || len(fresh) != len(ids) {
			return 0, ErrScope
		}
		for _, id := range ids {
			if !fresh[id] {
				return 0, ErrScope
			}
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return 0, e
	}
	step++
	if s.Epoch != "text" && step > chatMaxCalls {
		return 0, ErrScope
	}
	encoded, _ := json.Marshal(results)
	if _, e = tx.ExecContext(ctx, "INSERT INTO "+inferenceTable(s)+"(turn_id,step,fingerprint,state,result_ids_json,created_at)VALUES(?,?,?,'forwarding',?,?)", s.TurnID, step, fingerprint, string(encoded), time.Now().UnixMilli()); e != nil {
		return 0, e
	}
	return step, tx.Commit()
}

func resultIDs(operation string, body map[string]json.RawMessage) []string {
	ids := []string{}
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	var messages []struct {
		Role, Type, CallID string
		ToolCallID         string `json:"tool_call_id"`
		Content            json.RawMessage
	}
	if operation == "responses" {
		var input []struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
		}
		_ = json.Unmarshal(body["input"], &input)
		for _, v := range input {
			if v.Type == "function_call_output" {
				add(v.CallID)
			}
		}
	} else {
		_ = json.Unmarshal(body["messages"], &messages)
		for _, m := range messages {
			if m.Role == "tool" {
				add(m.ToolCallID)
			}
			if operation == "messages" {
				var parts []struct {
					Type string `json:"type"`
					ID   string `json:"tool_use_id"`
				}
				_ = json.Unmarshal(m.Content, &parts)
				for _, p := range parts {
					if p.Type == "tool_result" {
						add(p.ID)
					}
				}
			}
		}
	}
	return ids
}

// All tool/function and terminal records remain withheld until this whole-stream
// check succeeds. Early text is durable and carries no tool execution authority.
func completedInference(operation string, raw []byte, secrets ...string) ([]string, error) {
	for _, secret := range secrets {
		if secret != "" && bytes.Contains(raw, []byte(secret)) {
			return nil, errors.New("inference reflected private credential")
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), inferenceLimit)
	complete := false
	finished := false
	ids := []string{}
	seen := map[string]bool{}
	sensitiveTail := ""
	add := func(id string) {
		if id != "" && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			if operation == "chat/completions" {
				complete = true
			}
			continue
		}
		var event struct {
			Type    string          `json:"type"`
			Error   json.RawMessage `json:"error"`
			Delta   json.RawMessage `json:"delta"`
			Choices []struct {
				FinishReason *string `json:"finish_reason"`
				Delta        struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						ID string `json:"id"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			ContentBlock struct{ Type, ID, Text string } `json:"content_block"`
			Item         struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(data), &event) != nil || len(event.Error) > 0 || event.Type == "error" || event.Type == "response.failed" || event.Type == "response.incomplete" {
			return nil, errors.New("invalid inference stream")
		}
		var text strings.Builder
		for _, choice := range event.Choices {
			text.WriteString(choice.Delta.Content)
		}
		if event.Type == "response.output_text.delta" {
			var delta string
			_ = json.Unmarshal(event.Delta, &delta)
			text.WriteString(delta)
		}
		if event.ContentBlock.Type == "text" {
			text.WriteString(event.ContentBlock.Text)
		}
		if event.Type == "content_block_delta" {
			var delta struct{ Type, Text string }
			_ = json.Unmarshal(event.Delta, &delta)
			if delta.Type == "text_delta" {
				text.WriteString(delta.Text)
			}
		}
		var sensitive bool
		sensitiveTail, sensitive = inferenceCredential([]byte(data), text.String(), sensitiveTail, secrets...)
		if sensitive {
			return nil, errors.New("inference reflected private credential")
		}
		for _, choice := range event.Choices {
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finished = true
			}
			for _, call := range choice.Delta.ToolCalls {
				add(call.ID)
			}
		}
		if event.ContentBlock.Type == "tool_use" {
			add(event.ContentBlock.ID)
		}
		if event.Item.Type == "function_call" {
			add(event.Item.CallID)
		}
		if operation == "responses" && event.Type == "response.completed" || operation == "messages" && event.Type == "message_stop" {
			complete = true
		}
	}
	if scanner.Err() != nil || !complete || operation == "chat/completions" && !finished {
		return nil, errors.New("incomplete inference stream")
	}
	return ids, nil
}
