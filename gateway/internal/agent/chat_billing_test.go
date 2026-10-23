package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func chatFixtureConfig(c LLMConfig) LLMConfig {
	if c.Context == 0 {
		c.Context = 32000
	}
	if c.Output == 0 {
		c.Output = 4096
	}
	c.Pricing = &providers.Pricing{Currency: "USD", Unit: "token", UnitAmount: 1, MinorUnitScale: 6, CreditDenominator: 1, Source: "synthetic declared", Version: "v1"}
	return c
}

// Low-level relay fixtures use the real Send, trusted billing hook and human
// reply service before attaching the resulting message to their synthetic turn.
func authorizeRelayFixture(t *testing.T, r *Relay, s ToolScope) Message {
	t.Helper()
	r.config = chatFixtureConfig(r.config)
	ev := events.NewService(r.DB)
	q := questions.NewService(r.DB, nil, ev)
	c := NewChat(r.DB, r.Turns, ev, q)
	defer c.Close()
	c.Host = &Host{Relay: r, active: View{Config: &r.config}, credentialVersion: r.credentialVersion}
	c.Grace = time.Millisecond
	m, e := c.Send(context.Background(), s.ProjectID, s.SessionID, []byte(`{"requestId":"original-key","document":{"type":"doc"},"text":"Run the owned fixture.","references":[],"annotations":[]}`))
	if e != nil {
		t.Fatal(e)
	}
	approveChatFixture(t, c, s.ProjectID, s.SessionID, m.BillingQuestionID, false)
	if _, e = r.DB.Exec("UPDATE chat_dispatch SET turn_id=?,state='accepted' WHERE message_id=?", s.TurnID, m.ID); e != nil {
		t.Fatal(e)
	}
	return m
}

func approveChatFixture(t *testing.T, c *Chat, p, session, id string, reject bool) questions.Receipt {
	t.Helper()
	var raw string
	if e := c.DB.QueryRow("SELECT question_json FROM questions WHERE id=?", id).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var q questions.Question
	if e := json.Unmarshal([]byte(raw), &q); e != nil {
		t.Fatal(e)
	}
	answers := [][]string{{chatApprove}}
	if reject {
		answers = [][]string{}
	}
	receipt, e := c.Questions.Reply(context.Background(), p, id, questions.HumanReply{RequestID: "human-" + id, SessionID: session, ExpectedRevision: q.Revision, Answers: answers, Reject: reject})
	if e != nil {
		t.Fatal(e)
	}
	return receipt
}

func TestChatBillingUnboundTurnCannotSpend(t *testing.T) {
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
	s, err := turns.Begin(context.Background(), p.ID, "session", "native")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	supplier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer supplier.Close()
	relay, err := newRelay(db, LLMConfig{Protocol: "openai-compatible", BaseURL: supplier.URL, ModelID: "fixture", Context: 32000, Output: 4096}, "dummy", "http://tauri.localhost")
	if err != nil {
		t.Fatal(err)
	}
	relay.Turns = turns
	req := httptest.NewRequest("POST", inferencePath+"/chat/completions", strings.NewReader(`{"model":"fixture","stream":true,"max_tokens":4096,"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Authorization", "Bearer "+relay.token)
	req.Header.Set("Origin", relay.origin)
	req.Header.Set("X-Opencode-Session-Id", s.NativeSessionID)
	relay.ServeHTTP(httptest.NewRecorder(), req)
	if calls.Load() != 0 {
		t.Fatalf("unapproved chat reached supplier: %d POSTs", calls.Load())
	}
}

func TestChatBillingProtocolPayloadsAtSupplierBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, item string
		valid                bool
	}{
		{"responses_tool_image", "openai", `{"type":"function_call_output","call_id":"call-1","output":[{"type":"input_image","image_url":"https://example.invalid/large.png"}]}`, false},
		{"responses_tool_null", "openai", `{"type":"function_call_output","call_id":"call-1","output":null}`, false},
		{"responses_tool_missing", "openai", `{"type":"function_call_output","call_id":"call-1"}`, false},
		{"responses_tool_object", "openai", `{"type":"function_call_output","call_id":"call-1","output":{"text":"hello"}}`, false},
		{"responses_tool_bad_text", "openai", `{"type":"function_call_output","call_id":"call-1","output":[{"type":"input_text","text":null}]}`, false},
		{"responses_user_null", "openai", `{"role":"user","content":null}`, false},
		{"compatible_user_null", "openai-compatible", `{"role":"user","content":null}`, false},
		{"anthropic_user_null", "anthropic", `{"role":"user","content":null}`, false},
		{"responses_function_missing_arguments", "openai", `{"type":"function_call","call_id":"call-1","name":"question"}`, false},
		{"responses_reasoning_missing_summary", "openai", `{"type":"reasoning","id":"r"}`, false},
		{"responses_reasoning_bad_summary", "openai", `{"type":"reasoning","id":"r","summary":[{"type":"summary_text","text":false}]}`, false},
		{"responses_text_missing", "openai", `{"role":"user","content":[{"type":"input_text"}]}`, false},
		{"compatible_text_object", "openai-compatible", `{"role":"user","content":[{"type":"text","text":{"image_url":"https://example.invalid/image"}}]}`, false},
		{"compatible_fake_tool_calls", "openai-compatible", `{"role":"assistant","content":null,"tool_calls":[{}]}`, false},
		{"anthropic_tool_missing_input", "anthropic", `{"role":"assistant","content":[{"type":"tool_use","id":"call-1","name":"question"}]}`, false},
		{"anthropic_tool_result_image", "anthropic", `{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"image","source":{"type":"url","url":"https://example.invalid/image"}}]}]}`, false},
		{"anthropic_thinking_missing_signature", "anthropic", `{"role":"assistant","content":[{"type":"thinking","thinking":"hello"}]}`, false},
		{"responses_tool_file", "openai", `{"type":"function_call_output","call_id":"call-1","output":[{"type":"input_file","file_id":"hidden"}]}`, false},
		{"responses_tool_audio", "openai", `{"type":"function_call_output","call_id":"call-1","output":[{"type":"input_audio","data":"hidden"}]}`, false},
		{"responses_function_null_arguments", "openai", `{"type":"function_call","call_id":"call-1","name":"question","arguments":null}`, false},
		{"responses_reasoning_null_summary", "openai", `{"type":"reasoning","id":"r","summary":null}`, false},
		{"responses_reasoning_hidden_content", "openai", `{"type":"reasoning","id":"r","summary":[],"content":[{"type":"input_image","image_url":"https://example.invalid/image"}]}`, false},
		{"responses_item_reference", "openai", `{"type":"item_reference","id":"hidden"}`, false},
		{"responses_type_number", "openai", `{"type":42,"role":"user","content":"hi"}`, false},
		{"responses_empty_user_array", "openai", `{"role":"user","content":[]}`, false},
		{"compatible_tool_null", "openai-compatible", `{"role":"tool","tool_call_id":"call-1","content":null}`, false},
		{"compatible_tool_calls_wrong_role", "openai-compatible", `{"role":"user","tool_calls":[{"id":"call-1","type":"function","function":{"name":"question","arguments":"{}"}}]}`, false},
		{"anthropic_tool_null_input", "anthropic", `{"role":"assistant","content":[{"type":"tool_use","id":"call-1","name":"question","input":null}]}`, false},
		{"anthropic_tool_result_null_text", "anthropic", `{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"text","text":null}]}]}`, false},
		{"anthropic_redacted_thinking", "anthropic", `{"role":"assistant","content":[{"type":"redacted_thinking","data":"hidden"}]}`, false},
		{"responses_reasoning_empty_summary", "openai", `{"type":"reasoning","id":"r","summary":[]}`, true},
		{"responses_reasoning_text_content", "openai", `{"type":"reasoning","id":"r","summary":[],"content":[{"type":"reasoning_text","text":"hello"}]}`, true},
		{"compatible_tool_text_array", "openai-compatible", `{"role":"tool","tool_call_id":"call-1","content":[{"type":"text","text":"asset:owned"}]}`, true},
		{"responses_assistant_text", "openai", `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}`, true},
		{"responses_tool_string", "openai", `{"type":"function_call_output","call_id":"call-1","output":"ok"}`, true},
		{"responses_tool_text_array", "openai", `{"type":"function_call_output","call_id":"call-1","output":[{"type":"input_text","text":"ok"}]}`, true},
		{"responses_function", "openai", `{"type":"function_call","call_id":"call-1","name":"question","arguments":"{}"}`, true},
		{"responses_reasoning", "openai", `{"type":"reasoning","id":"r","summary":[{"type":"summary_text","text":"hello"}]}`, true},
		{"compatible_assistant_tool_null", "openai-compatible", `{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"question","arguments":"{}"}}]}`, true},
		{"compatible_assistant_tool_only", "openai-compatible", `{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"question","arguments":"{}"}}]}`, true},
		{"anthropic_tool_result_string", "anthropic", `{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":"ok"}]}`, true},
		{"anthropic_tool_result_text_array", "anthropic", `{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"text","text":"ok"}]}]}`, true},
		{"anthropic_tool_use", "anthropic", `{"role":"assistant","content":[{"type":"tool_use","id":"call-1","name":"question","input":{}}]}`, true},
		{"anthropic_thinking", "anthropic", `{"role":"assistant","content":[{"type":"thinking","thinking":"hello","signature":"signed"},{"type":"text","text":"answer"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation, field, response := "chat/completions", "messages", "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			if tc.protocol == "openai" {
				operation, field, response = "responses", "input", "data: {\"type\":\"response.completed\"}\n\n"
			} else if tc.protocol == "anthropic" {
				operation, response = "messages", "data: {\"type\":\"message_stop\"}\n\n"
			}
			f := newStreamFixture(t, tc.protocol, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) })
			result := f.request(t, operation, fmt.Sprintf(`{"model":"fixture","stream":true,%q:[{"role":"user","content":"hi"},%s]}`, field, tc.item))
			_, e := io.Copy(io.Discard, result.Body)
			result.Body.Close()
			if e != nil {
				t.Fatal(e)
			}
			want := int32(0)
			if tc.valid {
				want = 1
			}
			if f.calls.Load() != want || tc.valid && result.StatusCode != 200 {
				t.Fatalf("supplier POSTs=%d want=%d HTTP=%d", f.calls.Load(), want, result.StatusCode)
			}
		})
	}
}

func TestChatBillingWorkerWaitsForHumanAndRejectDoesNotBlockQueue(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.autoApprove.Store(false)
	first, e := f.chat.Send(context.Background(), f.project, f.session, f.input("first"))
	if e != nil {
		t.Fatal(e)
	}
	second, e := f.chat.Send(context.Background(), f.project, f.session, f.input("second"))
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(300 * time.Millisecond)
	if f.prompts.Load() != 0 {
		t.Fatal("prompt before human receipt")
	}
	replay, e := f.chat.Send(context.Background(), f.project, f.session, f.input("first"))
	if e != nil || replay.ID != first.ID || replay.BillingQuestionID != first.BillingQuestionID {
		t.Fatal("send identity changed", e)
	}
	if _, e = f.chat.Send(context.Background(), f.project, f.session, []byte(strings.Replace(string(f.input("first")), "hello", "changed", 1))); e == nil {
		t.Fatal("changed original input accepted")
	}
	approveChatFixture(t, f.chat, f.project, f.session, first.BillingQuestionID, true)
	if f.prompts.Load() != 0 {
		t.Fatal("rejected message submitted")
	}
	receipt := approveChatFixture(t, f.chat, f.project, f.session, second.BillingQuestionID, false)
	duplicate, e := f.chat.Questions.Reply(context.Background(), f.project, second.BillingQuestionID, questions.HumanReply{RequestID: "human-" + second.BillingQuestionID, SessionID: f.session, ExpectedRevision: 1, Answers: [][]string{{chatApprove}}})
	if e != nil || duplicate.ID != receipt.ID {
		t.Fatal("duplicate receipt", e)
	}
	waitChat(t, func() bool { return f.prompts.Load() == 1 })
	var state string
	if e = f.chat.DB.QueryRow("SELECT state FROM chat_dispatch WHERE message_id=?", first.ID).Scan(&state); e != nil || state != "cancelled" {
		t.Fatal("rejected queue head not terminal", state, e)
	}
}

func expireChatCard(t *testing.T, c *Chat, id string, expiry int64) questions.Question {
	t.Helper()
	var raw string
	if e := c.DB.QueryRow("SELECT question_json FROM questions WHERE id=?", id).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var q questions.Question
	if e := json.Unmarshal([]byte(raw), &q); e != nil {
		t.Fatal(e)
	}
	q.Quote.ExpiresAt = expiry
	card, _ := json.Marshal(q)
	quote, _ := json.Marshal(q.Quote)
	if _, e := c.DB.Exec("UPDATE questions SET question_json=? WHERE id=?", string(card), id); e != nil {
		t.Fatal(e)
	}
	if _, e := c.DB.Exec("UPDATE price_quotes SET expires_at=?,frozen_json=? WHERE id=?", expiry, string(quote), q.Quote.QuoteID); e != nil {
		t.Fatal(e)
	}
	return q
}

func TestChatBillingExpiryRenewalTargetsAndRunningSnapshot(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.autoApprove.Store(false)
	f.chat.Close()
	m, e := f.chat.Send(context.Background(), f.project, f.session, f.input("pending"))
	if e != nil {
		t.Fatal(e)
	}
	original := expireChatCard(t, f.chat, m.BillingQuestionID, time.Now().Add(-time.Second).UnixMilli())
	if _, e = f.chat.Questions.Reply(context.Background(), f.project, original.ID, questions.HumanReply{RequestID: "expired", SessionID: f.session, ExpectedRevision: 1, Answers: [][]string{{chatApprove}}}); e == nil {
		t.Fatal("expired human approval accepted")
	}
	renew := questions.Question{ID: original.ID, Revision: 1, RequestID: "renew", SessionID: f.session, ChatMessageID: m.ID, Kind: "billing"}
	for _, mode := range []string{"neither", "both", "ordinary", "wrong-card", "wrong-revision", "wrong-session"} {
		bad := renew
		bad.RequestID = mode
		switch mode {
		case "neither":
			bad.ChatMessageID = ""
		case "both":
			bad.TaskID = "forged-task"
		case "ordinary":
			bad.Kind = "choice"
			bad.Questions = []questions.Info{{Header: "fake", Question: "approve?", Options: []questions.Option{{Label: chatApprove}}}}
		case "wrong-card":
			bad.ID = "another"
		case "wrong-revision":
			bad.Revision = 2
		case "wrong-session":
			bad.SessionID = "another"
		}
		if _, e = f.chat.Questions.Ask(context.Background(), f.project, bad); e == nil {
			t.Fatal("forged billing accepted", mode)
		}
	}
	fresh, e := f.chat.Questions.Ask(context.Background(), f.project, renew)
	if e != nil || fresh.ID == original.ID || fresh.ChatMessageID != m.ID {
		t.Fatal("renewal", fresh, e)
	}
	again, e := f.chat.Questions.Ask(context.Background(), f.project, renew)
	if e != nil || again.ID != fresh.ID {
		t.Fatal("renewal replay changed", e)
	}
	expireChatCard(t, f.chat, fresh.ID, time.Now().Add(-time.Second).UnixMilli())
	renew.RequestID = "stale-old-id"
	if _, e = f.chat.Questions.Ask(context.Background(), f.project, renew); e == nil {
		t.Fatal("old revision1 reused replacement revision1")
	}
	renew.ID = fresh.ID
	renew.RequestID = "fresh-renew"
	fresh, e = f.chat.Questions.Ask(context.Background(), f.project, renew)
	if e != nil {
		t.Fatal(e)
	}
	approveChatFixture(t, f.chat, f.project, f.session, fresh.ID, false)
	check := func(config LLMConfig, version string) error {
		tx, e := f.chat.DB.BeginTx(context.Background(), nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		return approvedChat(context.Background(), tx, f.project, f.session, m.ID, config, version)
	}
	if e = check(f.chat.Host.Relay.config, ""); e != nil {
		t.Fatal(e)
	}
	// Saving next-launch settings cannot replace a running immutable authorization.
	if _, e = f.chat.DB.Exec("INSERT INTO llm_settings(id,config_json,credential_version)VALUES(1,'{}','next-launch')"); e != nil {
		t.Fatal(e)
	}
	if e = check(f.chat.Host.Relay.config, ""); e != nil {
		t.Fatal("pending settings revoked running authorization", e)
	}
	changed := f.chat.Host.Relay.config
	changed.ModelID = "another"
	if check(changed, "") == nil || check(f.chat.Host.Relay.config, "rotated") == nil {
		t.Fatal("different running runtime reused old authorization")
	}
}

func TestChatBillingContinuationOutlivesQuoteButCannotRenewOrRestartBudget(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.autoApprove.Store(false)
	f.chat.Close()
	m, e := f.chat.Send(context.Background(), f.project, f.session, f.input("expiry"))
	if e != nil {
		t.Fatal(e)
	}
	approveChatFixture(t, f.chat, f.project, f.session, m.BillingQuestionID, false)
	if _, e = f.chat.DB.Exec("UPDATE chat_sessions SET native_session_id='ses_native',state='ready' WHERE id=?", f.session); e != nil {
		t.Fatal(e)
	}
	scope, e := f.chat.Turns.Begin(context.Background(), f.project, f.session, "ses_native")
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UnixMilli()
	if _, e = f.chat.DB.Exec("UPDATE chat_dispatch SET turn_id=?,state='accepted' WHERE message_id=?", scope.TurnID, m.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.chat.DB.Exec("INSERT INTO inference_requests(turn_id,step,fingerprint,state,created_at)VALUES(?,1,'first','completed',?)", scope.TurnID, now); e != nil {
		t.Fatal(e)
	}
	q := expireChatCard(t, f.chat, m.BillingQuestionID, now+1)
	time.Sleep(3 * time.Millisecond)
	tx, e := f.chat.DB.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	e = approvedChat(context.Background(), tx, f.project, f.session, m.ID, f.chat.Host.Relay.config, "")
	tx.Rollback()
	if e != nil {
		t.Fatal("started authorized turn expired", e)
	}
	if _, e = f.chat.Questions.Ask(context.Background(), f.project, questions.Question{ID: q.ID, Revision: q.Revision, RequestID: "renew-started", Kind: "billing", ChatMessageID: m.ID, SessionID: f.session}); e == nil {
		t.Fatal("started message renewed")
	}
	// First admission must also precede expiry, not only the human receipt.
	if _, e = f.chat.DB.Exec("UPDATE inference_requests SET created_at=? WHERE turn_id=?", q.Quote.ExpiresAt, scope.TurnID); e != nil {
		t.Fatal(e)
	}
	tx, e = f.chat.DB.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	e = approvedChat(context.Background(), tx, f.project, f.session, m.ID, f.chat.Host.Relay.config, "")
	tx.Rollback()
	if e == nil {
		t.Fatal("late first spend accepted")
	}
}

func TestChatBillingApprovedQueuedExpiryRequiresFreshHumanCard(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.autoApprove.Store(false)
	first, e := f.chat.Send(context.Background(), f.project, f.session, f.input("first"))
	if e != nil {
		t.Fatal(e)
	}
	approveChatFixture(t, f.chat, f.project, f.session, first.BillingQuestionID, false)
	waitChat(t, func() bool { return f.prompts.Load() == 1 })
	second, e := f.chat.Send(context.Background(), f.project, f.session, f.input("second"))
	if e != nil {
		t.Fatal(e)
	}
	approveChatFixture(t, f.chat, f.project, f.session, second.BillingQuestionID, false)
	old := expireChatCard(t, f.chat, second.BillingQuestionID, time.Now().Add(-time.Second).UnixMilli())
	f.mu.Lock()
	f.finished = true
	f.mu.Unlock()
	waitChat(t, func() bool {
		var state string
		_ = f.chat.DB.QueryRow("SELECT state FROM chat_dispatch WHERE message_id=?", first.ID).Scan(&state)
		return state == "done"
	})
	time.Sleep(300 * time.Millisecond)
	var state string
	var turn sql.NullString
	if e = f.chat.DB.QueryRow("SELECT state,turn_id FROM chat_dispatch WHERE message_id=?", second.ID).Scan(&state, &turn); e != nil || state != "queued" || turn.Valid || f.prompts.Load() != 1 {
		t.Fatal("expired queued approval submitted", state, turn, f.prompts.Load(), e)
	}
	fresh, e := f.chat.Questions.Ask(context.Background(), f.project, questions.Question{ID: old.ID, Revision: old.Revision, Kind: "billing", RequestID: "renew-second", SessionID: f.session, ChatMessageID: second.ID})
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(300 * time.Millisecond)
	if f.prompts.Load() != 1 {
		t.Fatal("renewal auto-approved")
	}
	approveChatFixture(t, f.chat, f.project, f.session, fresh.ID, false)
	waitChat(t, func() bool { return f.prompts.Load() == 2 })
}

func TestChatBillingQuoteAndRequestEnvelope(t *testing.T) {
	c := chatFixtureConfig(LLMConfig{Protocol: "openai-compatible", BaseURL: "http://127.0.0.1:12345", ModelID: "fixture"})
	q, e := chatQuote(chatBilling{Config: c, MaxCalls: 41})
	if e != nil || q.EstimatedAmount != 32000*41 {
		t.Fatal(q, e)
	}
	for _, mode := range []string{"missing", "future", "overflow", "zero"} {
		t.Run(mode, func(t *testing.T) {
			copy := c
			price := *c.Pricing
			copy.Pricing = &price
			switch mode {
			case "missing":
				copy.Pricing = nil
			case "future":
				price.EffectiveAt = time.Now().Unix() + 60
			case "overflow":
				copy.Context = 2000000
				price.UnitAmount = 1000000000000
			case "zero":
				price.UnitAmount = 0
			}
			q, e := chatQuote(chatBilling{Config: copy, MaxCalls: 41})
			if mode == "zero" {
				if e != nil || q.EstimatedAmount != 0 {
					t.Fatal(q, e)
				}
			} else if e == nil {
				t.Fatal("invalid price accepted")
			}
		})
	}
	for _, operation := range []string{"chat/completions", "responses", "messages"} {
		t.Run(operation, func(t *testing.T) {
			field, key := "messages", "max_tokens"
			if operation == "responses" {
				field, key = "input", "max_output_tokens"
			}
			body := fmt.Sprintf(`{"model":"fixture","stream":true,%q:[{"role":"user","content":"hi"}]}`, field)
			normalized, e := chatRequest([]byte(body), operation, c)
			var parsed map[string]any
			_ = json.Unmarshal(normalized, &parsed)
			if e != nil || parsed[key] != float64(c.Output) {
				t.Fatal("missing output cap not bounded", string(normalized), e)
			}
			for _, extra := range []string{fmt.Sprintf(`,%q:4097`, key), fmt.Sprintf(`,%q:0`, key), fmt.Sprintf(`,%q:"4096"`, key), `,"max_tokens":1,"max_output_tokens":1`, `,"n":2`, `,"previous_response_id":"unbounded"`} {
				if _, e := chatRequest([]byte(strings.TrimSuffix(body, "}")+extra+"}"), operation, c); e == nil {
					t.Fatal("invalid request accepted", extra)
				}
			}
			for _, content := range []string{strings.Repeat("a", c.Context), `[{"type":"image_url","image_url":{"url":"https://example.test/image"}}]`} {
				bad := fmt.Sprintf(`{"model":"fixture","stream":true,%q:[{"role":"user","content":%q}]}`, field, content)
				if strings.HasPrefix(content, "[") {
					bad = fmt.Sprintf(`{"model":"fixture","stream":true,%q:[{"role":"user","content":%s}]}`, field, content)
				}
				if _, e := chatRequest([]byte(bad), operation, c); e == nil {
					t.Fatal("unbounded input accepted")
				}
			}
		})
	}
}

func TestChatBillingRelayChecksBindingAnd41PostBudget(t *testing.T) {
	for _, mode := range []string{"unapproved", "rejected", "model", "credential", "scope", "quote", "cancelled", "input", "output", "malformed", "budget"} {
		t.Run(mode, func(t *testing.T) {
			db, e := store.Open(filepath.Join(t.TempDir(), "db"))
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
			var calls atomic.Int32
			supplier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["max_tokens"] != float64(4096) {
					t.Error("supplier missing normalized bound")
				}
				n := calls.Add(1)
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call-%d\"}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", n)
			}))
			defer supplier.Close()
			config := chatFixtureConfig(LLMConfig{Protocol: "openai-compatible", BaseURL: supplier.URL, ModelID: "fixture"})
			relay, e := newRelay(db, config, "dummy", "http://tauri.localhost")
			if e != nil {
				t.Fatal(e)
			}
			relay.Turns = turns
			ev := events.NewService(db)
			q := questions.NewService(db, nil, ev)
			c := NewChat(db, turns, ev, q)
			defer c.Close()
			c.Host = &Host{Relay: relay, active: View{Config: &config}}
			c.Grace = time.Millisecond
			m, e := c.Send(context.Background(), p.ID, scope.SessionID, []byte(`{"requestId":"send","document":{"type":"doc"},"text":"hello","references":[],"annotations":[]}`))
			if e != nil {
				t.Fatal(e)
			}
			if mode != "unapproved" {
				approveChatFixture(t, c, p.ID, scope.SessionID, m.BillingQuestionID, mode == "rejected")
			}
			if _, e = db.Exec("UPDATE chat_dispatch SET turn_id=?,state=CASE WHEN terminal=0 THEN 'accepted' ELSE state END WHERE message_id=?", scope.TurnID, m.ID); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "model":
				relay.config.ModelID = "changed"
			case "credential":
				relay.credentialVersion = "rotated"
			case "scope":
				scope.SessionID = "different"
				turns.active = scope
			case "quote":
				_, e = db.Exec("UPDATE price_quotes SET version='forged'")
			case "cancelled":
				_, e = db.Exec("UPDATE chat_dispatch SET state='cancelling'")
			}
			if e != nil {
				t.Fatal(e)
			}
			count := 1
			if mode == "budget" {
				count = 42
			}
			for n := 0; n < count; n++ {
				messages := []any{map[string]string{"role": "user", "content": fmt.Sprint(n)}}
				if n > 0 {
					messages = append(messages, map[string]string{"role": "tool", "tool_call_id": fmt.Sprintf("call-%d", n), "content": "ok"})
				}
				wire := map[string]any{"model": relay.config.ModelID, "stream": true, "messages": messages}
				switch mode {
				case "input":
					wire["messages"] = []any{map[string]string{"role": "user", "content": strings.Repeat("a", relay.config.Context)}}
				case "output":
					wire["max_tokens"] = 4097
				case "malformed":
					wire["max_tokens"] = "4096"
				}
				body, _ := json.Marshal(wire)
				req := httptest.NewRequest("POST", inferencePath+"/chat/completions", strings.NewReader(string(body)))
				req.Header.Set("Authorization", "Bearer "+relay.token)
				req.Header.Set("Origin", relay.origin)
				req.Header.Set("X-Opencode-Session-Id", scope.NativeSessionID)
				w := httptest.NewRecorder()
				relay.ServeHTTP(w, req)
				if mode == "budget" && n < 41 && w.Code != 200 {
					t.Fatalf("approved step %d denied: %s", n, w.Body.String())
				}
			}
			expected := int32(0)
			if mode == "budget" {
				expected = 41
			}
			if calls.Load() != expected {
				t.Fatal("supplier count", calls.Load(), expected)
			}
		})
	}
}
