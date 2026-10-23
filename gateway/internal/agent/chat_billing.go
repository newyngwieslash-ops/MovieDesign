package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

const chatMaxCalls = 41
const chatApprove = "确认本轮聊天费用"

type chatBilling struct {
	ProjectID         string    `json:"project_id"`
	Message           Message   `json:"message"`
	Config            LLMConfig `json:"config"`
	CredentialVersion string    `json:"credential_version"`
	MaxCalls          int       `json:"max_calls"`
}

func chatQuote(b chatBilling) (providers.Quote, error) {
	if Validate(b.Config) != nil || b.MaxCalls != chatMaxCalls || b.Config.Pricing == nil || b.Config.Pricing.Unit != "token" || b.Config.Pricing.EffectiveAt > time.Now().Unix() {
		return providers.Quote{}, chatError(409, "A current explicit chat token price is required")
	}
	p := b.Config.Pricing
	amount := new(big.Int).Mul(big.NewInt(p.UnitAmount), big.NewInt(int64(b.Config.Context)))
	amount.Mul(amount, big.NewInt(int64(b.MaxCalls)))
	credits := new(big.Int).Mul(amount, big.NewInt(p.CreditNumerator))
	credits.Add(credits, big.NewInt(p.CreditDenominator-1))
	credits.Div(credits, big.NewInt(p.CreditDenominator))
	if !amount.IsInt64() || !credits.IsInt64() {
		return providers.Quote{}, chatError(409, "Chat quote overflow")
	}
	raw, e := json.Marshal(b)
	if e != nil {
		return providers.Quote{}, e
	}
	raw, e = store.CanonicalJSON(raw)
	if e != nil {
		return providers.Quote{}, e
	}
	hash := sha256.Sum256(raw)
	return providers.Quote{QuoteID: uuid.NewString(), Currency: p.Currency, EstimatedAmount: amount.Int64(), MinorUnitScale: p.MinorUnitScale, EstimatedCredits: credits.Int64(), ExpiresAt: time.Now().Add(5 * time.Minute).UnixMilli(), InputHash: hex.EncodeToString(hash[:]), Source: p.Source, Version: p.Version, Basis: fmt.Sprintf("本轮最多 %d 次 LLM 请求；每次上下文上限 %d token（输入按 UTF-8 请求字节保守计数），输出最多 %d token。总估算为上下文上限 × 声明单 token 最高价 × %d。只按供应商实际调用计费；这是估算，不是预扣款或保证账单。", b.MaxCalls, b.Config.Context, b.Config.Output, b.MaxCalls)}, nil
}

func currentChatConfig(b chatBilling, config LLMConfig, version string) error {
	a, _ := json.Marshal(b.Config)
	running, _ := json.Marshal(config)
	if string(a) != string(running) || b.CredentialVersion != version {
		return chatError(409, "Running chat LLM configuration or credential changed; withdraw and create a new intent")
	}
	return nil
}

func (c *Chat) createChatBilling(ctx context.Context, tx *sql.Tx, p string, m Message) (questions.Question, error) {
	if c.Host == nil || c.Host.active.Config == nil || c.Host.Relay == nil {
		return questions.Question{}, chatError(409, "Chat runtime configuration unavailable")
	}
	b := chatBilling{ProjectID: p, Message: m, Config: *c.Host.active.Config, CredentialVersion: c.Host.credentialVersion, MaxCalls: chatMaxCalls}
	if e := currentChatConfig(b, *c.Host.active.Config, c.Host.credentialVersion); e != nil {
		return questions.Question{}, e
	}
	raw, e := json.Marshal(b)
	if e != nil {
		return questions.Question{}, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE chat_dispatch SET billing_json=? WHERE message_id=?", string(raw), m.ID); e != nil {
		return questions.Question{}, e
	}
	return c.insertChatBilling(ctx, tx, b)
}

func (c *Chat) insertChatBilling(ctx context.Context, tx *sql.Tx, b chatBilling) (questions.Question, error) {
	quote, e := chatQuote(b)
	if e != nil {
		return questions.Question{}, e
	}
	raw, _ := json.Marshal(quote)
	if _, e = tx.ExecContext(ctx, "INSERT INTO price_quotes(id,project_id,input_hash,frozen_json,source,version,expires_at,created_at)VALUES(?,?,?,?,?,?,?,?)", quote.QuoteID, b.ProjectID, quote.InputHash, string(raw), quote.Source, quote.Version, quote.ExpiresAt, time.Now().UnixMilli()); e != nil {
		return questions.Question{}, e
	}
	q, e := c.Questions.ChatBilling(ctx, tx, b.ProjectID, questions.Question{ChatMessageID: b.Message.ID, SessionID: b.Message.SessionID, Kind: "billing", InputHash: quote.InputHash, Quote: &quote, Provider: b.Config.Protocol, Model: b.Config.ModelID, Questions: []questions.Info{{Header: "聊天费用确认", Question: "批准此原消息的有界 LLM 推理？", Options: []questions.Option{{Label: chatApprove, Description: "最多 41 次 LLM 请求；重复回执不会重复推理"}}}}, Impacts: []string{"本卡只批准该消息的聊天推理；工具和媒体生成仍需独立费用卡", "取消不能撤回已发送的请求；超出上下文、输出或次数上限会停止"}})
	if e != nil {
		return q, e
	}
	_, e = tx.ExecContext(ctx, "UPDATE chat_dispatch SET billing_question_id=? WHERE message_id=?", q.ID, b.Message.ID)
	return q, e
}

func loadChatBilling(ctx context.Context, tx *sql.Tx, p, session, id string) (chatBilling, questions.Question, string, error) {
	var b chatBilling
	var q questions.Question
	var frozen, question, content, state string
	e := tx.QueryRowContext(ctx, `SELECT d.billing_json,q.question_json,m.content_json,d.state FROM chat_dispatch d JOIN chat_messages m ON m.id=d.message_id JOIN chat_sessions s ON s.id=m.session_id JOIN questions q ON q.id=d.billing_question_id WHERE d.message_id=? AND s.id=? AND s.project_id=? AND d.terminal=0`, id, session, p).Scan(&frozen, &question, &content, &state)
	if e != nil {
		return b, q, state, e
	}
	if json.Unmarshal([]byte(frozen), &b) != nil || json.Unmarshal([]byte(question), &q) != nil {
		return b, q, state, ErrScope
	}
	actual, e := store.CanonicalJSON([]byte(content))
	expected, ee := store.CanonicalJSON(b.Message.Content)
	if e != nil || ee != nil || string(actual) != string(expected) || b.ProjectID != p || b.Message.SessionID != session || b.Message.ID != id || q.ChatMessageID != id || q.TaskID != "" || q.ProjectID != p || q.SessionID != session || q.Kind != "billing" || q.Quote == nil {
		return b, q, state, ErrScope
	}
	// The original send receipt is the authority for the adopted skill snapshot.
	var receipt string
	if e = tx.QueryRowContext(ctx, "SELECT response_json FROM write_requests WHERE project_id=? AND operation=? AND request_id=?", p, "chat.send."+session, b.Message.RequestID).Scan(&receipt); e != nil {
		return b, q, state, e
	}
	var original Message
	if json.Unmarshal([]byte(receipt), &original) != nil {
		return b, q, state, ErrScope
	}
	// BillingQuestionID is response metadata added after freezing the original message.
	original.BillingQuestionID = ""
	a, _ := json.Marshal(original)
	expected, e = json.Marshal(b.Message)
	if e != nil || string(a) != string(expected) {
		return b, q, state, ErrScope
	}
	return b, q, state, nil
}

func validChatQuote(ctx context.Context, tx *sql.Tx, b chatBilling, q questions.Question, first bool) error {
	quote, e := chatQuote(b)
	if e != nil {
		return e
	}
	var raw string
	var expiry int64
	if e = tx.QueryRowContext(ctx, "SELECT frozen_json,expires_at FROM price_quotes WHERE id=? AND project_id=? AND input_hash=? AND version=?", q.Quote.QuoteID, b.ProjectID, q.InputHash, q.Quote.Version).Scan(&raw, &expiry); e != nil {
		return e
	}
	expected, _ := json.Marshal(q.Quote)
	if string(expected) != raw || expiry != q.Quote.ExpiresAt || first && expiry <= time.Now().UnixMilli() || quote.InputHash != q.InputHash || quote.Version != q.Quote.Version || quote.EstimatedAmount != q.Quote.EstimatedAmount || quote.EstimatedCredits != q.Quote.EstimatedCredits {
		return chatError(409, "Chat quote expired or changed")
	}
	return nil
}

func (c *Chat) renewChatBilling(ctx context.Context, tx *sql.Tx, p string, request questions.Question) (questions.Question, error) {
	b, old, state, e := loadChatBilling(ctx, tx, p, request.SessionID, request.ChatMessageID)
	if e != nil {
		return questions.Question{}, e
	}
	if c.Host == nil || c.Host.active.Config == nil {
		return questions.Question{}, ErrScope
	}
	if state != "queued" || old.ID != request.ID || old.Revision != request.Revision || (old.State != "pending" && old.State != "approved") || old.Quote.ExpiresAt > time.Now().UnixMilli() {
		return questions.Question{}, chatError(409, "Original chat card is not renewable")
	}
	var started bool
	if e = tx.QueryRowContext(ctx, "SELECT turn_id IS NOT NULL FROM chat_dispatch WHERE message_id=?", b.Message.ID).Scan(&started); e != nil {
		return questions.Question{}, e
	}
	if started {
		return questions.Question{}, chatError(409, "A dispatched chat message cannot be renewed")
	}
	if e = currentChatConfig(b, *c.Host.active.Config, c.Host.credentialVersion); e != nil {
		return questions.Question{}, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE questions SET state='superseded',revision=revision+1,question_json=json_set(question_json,'$.state','superseded','$.revision',revision+1,'$.reason','报价已被新卡替代') WHERE id=?", old.ID); e != nil {
		return questions.Question{}, e
	}
	return c.insertChatBilling(ctx, tx, b)
}

func (c *Chat) decideChatBilling(ctx context.Context, tx *sql.Tx, p string, q questions.Question, reject bool) error {
	b, bound, state, e := loadChatBilling(ctx, tx, p, q.SessionID, q.ChatMessageID)
	if e != nil {
		return e
	}
	if bound.ID != q.ID || state != "queued" {
		return ErrScope
	}
	if reject {
		if _, e = tx.ExecContext(ctx, "UPDATE chat_dispatch SET state='cancelled',terminal=1,error='聊天费用确认已拒绝' WHERE message_id=? AND state='queued'", q.ChatMessageID); e != nil {
			return e
		}
		payload, _ := json.Marshal(ChatDonePayload{SessionID: q.SessionID, MessageID: q.ChatMessageID, Done: true, Status: "cancelled"})
		return events.Append(ctx, tx, p, q.SessionID, "", "chat_done", payload)
	}
	if c.Host == nil || c.Host.active.Config == nil {
		return ErrScope
	}
	if e = currentChatConfig(b, *c.Host.active.Config, c.Host.credentialVersion); e != nil {
		return e
	}
	return validChatQuote(ctx, tx, b, q, true)
}

func approvedChat(ctx context.Context, tx *sql.Tx, p, session, id string, config LLMConfig, version string) error {
	b, q, state, e := loadChatBilling(ctx, tx, p, session, id)
	if e != nil {
		return e
	}
	if q.State != "approved" || (state != "queued" && state != "submitting" && state != "accepted" && state != "running") {
		return ErrScope
	}
	if e = currentChatConfig(b, config, version); e != nil {
		return e
	}
	var firstAt sql.NullInt64
	if e = tx.QueryRowContext(ctx, "SELECT min(i.created_at) FROM inference_requests i JOIN chat_dispatch d ON d.turn_id=i.turn_id WHERE d.message_id=?", id).Scan(&firstAt); e != nil {
		return e
	}
	if firstAt.Valid && firstAt.Int64 >= q.Quote.ExpiresAt {
		return ErrScope
	}
	if e = validChatQuote(ctx, tx, b, q, !firstAt.Valid); e != nil {
		return e
	}
	var n int
	e = tx.QueryRowContext(ctx, `SELECT count(*) FROM question_receipts WHERE question_id=? AND actor='human' AND created_at<? AND (? IS NULL OR created_at<=?) AND json_extract(response_json,'$.state')='approved' AND json_extract(response_json,'$.chat_message_id')=? AND json_extract(response_json,'$.revision')=? AND json_extract(response_json,'$.answers')=?`, q.ID, q.Quote.ExpiresAt, firstAt, firstAt, id, q.Revision, `[["确认本轮聊天费用"]]`).Scan(&n)
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrScope
	}
	return nil
}

// ponytail: Serialized UTF-8 bytes bound text-only context conservatively.
// Relax only with an exact tokenizer and verified multimodal accounting;
// serialized bytes cannot bound image/audio/file input tokens.
func chatRequest(raw []byte, operation string, c LLMConfig) ([]byte, error) {
	if c.Context <= c.Output || c.Output < 1 {
		return nil, ErrScope
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		return nil, ErrScope
	}
	for _, key := range []string{"previous_response_id", "conversation", "prompt", "audio", "modalities"} {
		if _, ok := body[key]; ok {
			return nil, ErrScope
		}
	}
	if rawTools, ok := body["tools"]; ok {
		var tools []map[string]json.RawMessage
		if json.Unmarshal(rawTools, &tools) != nil {
			return nil, ErrScope
		}
		for _, tool := range tools {
			if rawType, exists := tool["type"]; exists {
				var typ string
				if json.Unmarshal(rawType, &typ) != nil || typ != "function" {
					return nil, ErrScope
				}
			}
		}
	}
	for _, key := range []string{"n", "best_of"} {
		if value, ok := body[key]; ok {
			var n int
			if json.Unmarshal(value, &n) != nil || n != 1 {
				return nil, ErrScope
			}
		}
	}
	key := "max_tokens"
	if operation == "responses" {
		key = "max_output_tokens"
	}
	found := 0
	for _, name := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if v, ok := body[name]; ok {
			var n int
			if json.Unmarshal(v, &n) != nil || n < 1 || n > c.Output {
				return nil, ErrScope
			}
			found++
			if operation == "responses" && name != "max_output_tokens" || operation == "messages" && name != "max_tokens" || operation == "chat/completions" && name == "max_output_tokens" {
				return nil, ErrScope
			}
		}
	}
	if found > 1 {
		return nil, ErrScope
	}
	if found == 0 {
		body[key] = json.RawMessage(fmt.Sprint(c.Output))
	}
	inputKey := "messages"
	if operation == "responses" {
		inputKey = "input"
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(body[inputKey], &messages) != nil || len(messages) == 0 {
		return nil, ErrScope
	}
	for _, m := range messages {
		for _, key := range []string{"audio", "image", "image_url", "file", "video", "encrypted_content"} {
			if _, ok := m[key]; ok {
				return nil, ErrScope
			}
		}
		var kind string
		if rawType, exists := m["type"]; exists {
			if !chatString(rawType, true) || json.Unmarshal(rawType, &kind) != nil {
				return nil, ErrScope
			}
		}
		if operation == "responses" {
			switch kind {
			case "function_call":
				if !chatString(m["call_id"], true) || !chatString(m["name"], true) || !chatString(m["arguments"], false) {
					return nil, ErrScope
				}
				continue
			case "function_call_output":
				if !chatString(m["call_id"], true) || !chatText(m["output"], "input_text") {
					return nil, ErrScope
				}
				continue
			case "reasoning":
				if !chatString(m["id"], true) || !chatTextParts(m["summary"], "summary_text") {
					return nil, ErrScope
				}
				if content, exists := m["content"]; exists && !chatTextParts(content, "reasoning_text") {
					return nil, ErrScope
				}
				continue
			}
		}
		if kind != "" && kind != "message" {
			return nil, ErrScope
		}
		var role string
		if json.Unmarshal(m["role"], &role) != nil {
			return nil, ErrScope
		}
		switch role {
		case "user", "assistant", "system", "developer", "tool":
		default:
			return nil, ErrScope
		}
		if operation == "messages" && role != "user" && role != "assistant" || operation == "responses" && role == "tool" {
			return nil, ErrScope
		}
		toolCalls := false
		if calls, exists := m["tool_calls"]; exists {
			var list []struct {
				ID       string                     `json:"id"`
				Type     string                     `json:"type"`
				Function map[string]json.RawMessage `json:"function"`
			}
			if operation != "chat/completions" || role != "assistant" || json.Unmarshal(calls, &list) != nil || len(list) == 0 {
				return nil, ErrScope
			}
			for _, call := range list {
				if call.ID == "" || call.Type != "function" || !chatString(call.Function["name"], true) || !chatString(call.Function["arguments"], false) {
					return nil, ErrScope
				}
			}
			toolCalls = true
		}
		if role == "tool" && !chatString(m["tool_call_id"], true) {
			return nil, ErrScope
		}
		content := m["content"]
		if toolCalls && (len(content) == 0 || string(content) == "null") {
			continue
		}
		if operation == "messages" {
			if !chatAnthropicContent(content, role) {
				return nil, ErrScope
			}
		} else {
			typ := "text"
			if operation == "responses" {
				typ = "input_text"
				if role == "assistant" {
					typ = "output_text"
				}
			}
			if !chatText(content, typ) {
				return nil, ErrScope
			}
		}
	}
	if system, exists := body["system"]; exists && (operation != "messages" || !chatText(system, "text")) {
		return nil, ErrScope
	}
	normalized, e := json.Marshal(body)
	if e != nil {
		return nil, e
	}
	if len(normalized) > c.Context-c.Output {
		return nil, chatError(409, "Chat input exceeds authorized context envelope")
	}
	return normalized, nil
}

// Raw JSON null must not count as a string (Unmarshal into string accepts it).
func chatString(raw json.RawMessage, nonempty bool) bool {
	var text *string
	return json.Unmarshal(raw, &text) == nil && text != nil && (!nonempty || *text != "")
}

func chatText(raw json.RawMessage, typ string) bool {
	if chatString(raw, false) {
		return true
	}
	var parts []json.RawMessage
	return json.Unmarshal(raw, &parts) == nil && len(parts) > 0 && chatTextParts(raw, typ)
}

func chatTextParts(raw json.RawMessage, typ string) bool {
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || parts == nil {
		return false
	}
	for _, part := range parts {
		var kind string
		if json.Unmarshal(part["type"], &kind) != nil || kind != typ || !chatString(part["text"], false) {
			return false
		}
	}
	return true
}

func chatAnthropicContent(raw json.RawMessage, role string) bool {
	if chatString(raw, false) {
		return true
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		var kind string
		if json.Unmarshal(part["type"], &kind) != nil {
			return false
		}
		switch kind {
		case "text":
			if !chatString(part["text"], false) {
				return false
			}
		case "tool_use":
			var input map[string]json.RawMessage
			if role != "assistant" || !chatString(part["id"], true) || !chatString(part["name"], true) || json.Unmarshal(part["input"], &input) != nil || input == nil {
				return false
			}
		case "tool_result":
			if role != "user" || !chatString(part["tool_use_id"], true) || !chatText(part["content"], "text") {
				return false
			}
		case "thinking":
			if role != "assistant" || !chatString(part["thinking"], false) || !chatString(part["signature"], true) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
