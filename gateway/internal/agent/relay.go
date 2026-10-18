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
	DB                 *sql.DB
	Turns              *Turns
	config             LLMConfig
	key, token, origin string
	client             *http.Client
	mu                 sync.Mutex
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
	if scope.TurnID == "" || req.Header.Get("X-Opencode-Session-Id") != scope.NativeSessionID {
		fail()
		return
	}
	var cancelling int
	if e := r.DB.QueryRowContext(req.Context(), "SELECT count(*) FROM chat_dispatch WHERE turn_id=? AND state='cancelling'", scope.TurnID).Scan(&cancelling); e != nil || cancelling > 0 {
		fail()
		return
	}
	release, e := r.Turns.Acquire(scope)
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
	// ponytail: one inference at a time, matching the application's single owned turn.
	r.mu.Lock()
	defer r.mu.Unlock()
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
	upstream, e := http.NewRequestWithContext(req.Context(), "POST", endpoint, bytes.NewReader(raw))
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
	if e == nil {
		defer response.Body.Close()
		if response.StatusCode == 200 {
			data, e = io.ReadAll(io.LimitReader(response.Body, inferenceLimit+1))
			if e == nil && len(data) <= inferenceLimit {
				ids, e = completedInference(operation, data)
			} else {
				e = errors.New("inference stream limit or disconnect")
			}
		} else {
			e = errors.New("inference response rejected")
		}
	}
	state := "completed"
	if e != nil {
		state = "unknown"
	}
	encoded, _ := json.Marshal(ids)
	if _, saveErr := r.DB.ExecContext(ctx, "UPDATE inference_requests SET state=?,tool_ids_json=? WHERE turn_id=? AND step=?", state, string(encoded), scope.TurnID, step); saveErr != nil || e != nil {
		if r.Turns.Abort != nil {
			abortCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = r.Turns.Abort(abortCtx, scope.NativeSessionID)
			cancel()
		}
		fail()
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

func (r *Relay) admit(ctx context.Context, s ToolScope, fingerprint string, results []string) (int, error) {
	tx, e := r.DB.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	var step int
	var state, allowed, used string
	e = tx.QueryRowContext(ctx, "SELECT step,state,tool_ids_json,result_ids_json FROM inference_requests WHERE turn_id=? ORDER BY step DESC LIMIT 1", s.TurnID).Scan(&step, &state, &allowed, &used)
	if e == nil {
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
	encoded, _ := json.Marshal(results)
	if _, e = tx.ExecContext(ctx, "INSERT INTO inference_requests(turn_id,step,fingerprint,state,result_ids_json,created_at)VALUES(?,?,?,'forwarding',?,?)", s.TurnID, step, fingerprint, string(encoded), time.Now().UnixMilli()); e != nil {
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

// ponytail: buffer one complete response (32 MiB / 10 minutes), adding first-token
// latency. Upgrade to a persisted streaming protocol before exposing partial tool
// output; a broken stream must never execute a tool and cause a second paid POST.
func completedInference(operation string, raw []byte) ([]string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), inferenceLimit)
	complete := false
	finished := false
	ids := []string{}
	seen := map[string]bool{}
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
			Choices []struct {
				FinishReason *string `json:"finish_reason"`
				Delta        struct {
					ToolCalls []struct {
						ID string `json:"id"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			ContentBlock struct{ Type, ID string } `json:"content_block"`
			Item         struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(data), &event) != nil || len(event.Error) > 0 || event.Type == "error" || event.Type == "response.failed" || event.Type == "response.incomplete" {
			return nil, errors.New("invalid inference stream")
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
