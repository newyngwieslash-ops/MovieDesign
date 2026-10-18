package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"net/url"
	"strings"
	"time"
)

func (h *Host) TextProvider(ctx context.Context) (providers.FrozenProvider, error) {
	if h == nil || h.Relay == nil || h.active.Config == nil {
		return providers.FrozenProvider{}, errors.New("Text runtime unavailable")
	}
	raw, _ := json.Marshal(h.active.Config)
	return providers.TextProvider(raw, h.credentialVersion)
}
func (h *Host) Text(ctx context.Context, a tasks.Attempt) (tasks.Outcome, error) {
	if h == nil {
		return tasks.Outcome{NotAccepted: true}, errors.New("Text runtime unavailable")
	}
	h.textMu.Lock()
	defer h.textMu.Unlock()
	if e := h.cleanupText(ctx); e != nil {
		return tasks.Outcome{NotAccepted: true}, e
	}
	frozen, e := h.TextProvider(ctx)
	if e != nil {
		return tasks.Outcome{NotAccepted: true}, e
	}
	lhs, _ := json.Marshal(frozen)
	rhs, _ := json.Marshal(a.Provider)
	if string(lhs) != string(rhs) {
		return tasks.Outcome{NotAccepted: true}, errors.New("Frozen LLM differs from running host; restart before creating a new intent")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	id, parent := uuid.NewString(), "msg_"+strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = h.Relay.DB.ExecContext(ctx, "INSERT INTO text_requests(id,task_id,attempt,native_message_id,state,created_at)VALUES(?,?,?,?,'creating',?)", id, a.TaskID, a.Number, parent, time.Now().UnixMilli()); e != nil {
		return tasks.Outcome{}, e
	}
	raw, e := h.request(ctx, "POST", "/session", map[string]any{"title": "MovieDesign text " + a.TaskID, "permission": []any{map[string]string{"permission": "*", "pattern": "*", "action": "deny"}}})
	var session struct {
		ID string `json:"id"`
	}
	if e != nil || json.Unmarshal(raw, &session) != nil || session.ID == "" {
		if !errors.Is(e, errNativeRejected) {
			h.textSession = "unknown"
		}
		return tasks.Outcome{NotAccepted: errors.Is(e, errNativeRejected)}, errNativeUnknown
	}
	h.textSession = session.ID
	if _, e = h.Relay.DB.ExecContext(ctx, "UPDATE text_requests SET native_session_id=?,state='active' WHERE id=?", session.ID, id); e != nil {
		return tasks.Outcome{}, e
	}
	release := h.Relay.bindText(id, session.ID)
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		release()
		_ = h.cleanupText(cleanup)
	}()
	disabled := map[string]bool{"question": false}
	for _, name := range h.tools {
		disabled[name] = false
	}
	accepted := make(chan error, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		_, e := h.request(ctx, "POST", "/session/"+url.PathEscape(session.ID)+"/prompt_async", map[string]any{"messageID": parent, "agent": "text", "tools": disabled, "parts": []any{map[string]string{"type": "text", "text": a.Input.Prompt}}})
		accepted <- e
	}()
	defer func() { cancel(); <-joined }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-accepted:
			accepted = nil
			if err != nil {
				return tasks.Outcome{NotAccepted: errors.Is(err, errNativeRejected)}, err
			}
		case <-ctx.Done():
			return tasks.Outcome{}, ctx.Err()
		case <-ticker.C:
		}
		var status string
		if e = h.Relay.DB.QueryRowContext(ctx, "SELECT status FROM tasks WHERE id=?", a.TaskID).Scan(&status); e != nil {
			return tasks.Outcome{}, e
		}
		if status == "cancelled" {
			return tasks.Outcome{}, context.Canceled
		}
		out, ready, e := h.textResult(ctx, id, session.ID, parent)
		if ready || e != nil {
			return out, e
		}
	}
}

// Retain uncertain native lifetime across calls. Cleanup is read/abort only;
// no new prompt can start until the previous owned session is provably idle.
func (h *Host) cleanupText(ctx context.Context) error {
	if h.textSession == "" {
		return nil
	}
	if h.textSession == "unknown" {
		return errNativeUnknown
	}
	if e := h.Abort(ctx, h.textSession); e != nil {
		return e
	}
	if e := h.WaitIdle(ctx, h.textSession); e != nil {
		return e
	}
	h.textSession = ""
	return nil
}
func (h *Host) textResult(ctx context.Context, id, session, parent string) (tasks.Outcome, bool, error) {
	var state string
	_ = h.Relay.DB.QueryRowContext(ctx, "SELECT state FROM text_inference_requests WHERE turn_id=? ORDER BY step DESC LIMIT 1", id).Scan(&state)
	if state == "unknown" || state == "rejected" {
		return tasks.Outcome{NotAccepted: state == "rejected"}, true, errNativeUnknown
	}
	messages, e := h.snapshot(ctx, session)
	if e != nil {
		return tasks.Outcome{}, false, e
	}
	for _, m := range messages {
		if m.Info.SessionID != session || m.Info.ParentID != parent || m.Info.Role != "assistant" {
			continue
		}
		if len(m.Info.Error) > 0 && string(m.Info.Error) != "null" {
			return tasks.Outcome{NotAccepted: state == ""}, true, errNativeUnknown
		}
		if m.Info.Time.Completed == 0 || m.Info.Finish == "" {
			continue
		}
		var content strings.Builder
		for _, part := range m.Parts {
			if part.Type == "tool" {
				return tasks.Outcome{}, true, errNativeUnknown
			}
			if part.Type == "text" && part.SessionID == session && part.MessageID == m.Info.ID {
				content.WriteString(part.Text)
			}
		}
		if state != "completed" {
			// Supplier completion may commit while the native snapshot is in flight.
			_ = h.Relay.DB.QueryRowContext(ctx, "SELECT state FROM text_inference_requests WHERE turn_id=? ORDER BY step DESC LIMIT 1", id).Scan(&state)
			if state != "completed" {
				return tasks.Outcome{}, true, errNativeUnknown
			}
		}
		if strings.TrimSpace(content.String()) == "" {
			return tasks.Outcome{Status: tasks.Failed, Problem: &tasks.Problem{Source: "provider", Code: "EMPTY_TEXT", Message: "Model completed without document text"}}, true, nil
		}
		result, _ := json.Marshal(tasks.TextResult{Data: []tasks.TextContent{{Text: content.String()}}})
		return tasks.Outcome{Status: tasks.Failed, Result: result, Problem: &tasks.Problem{Source: "delivery", Code: "RESULT_AWAITING_DELIVERY", Message: "Text saved for delivery", Retryable: true}}, true, nil
	}
	return tasks.Outcome{}, false, nil
}
func (h *Host) QueryText(ctx context.Context, a tasks.Attempt) (tasks.Outcome, error) {
	if h == nil || h.Relay == nil {
		return tasks.Outcome{Status: tasks.Unknown}, nil
	}
	var id, session, parent string
	if e := h.Relay.DB.QueryRowContext(ctx, "SELECT id,coalesce(native_session_id,''),native_message_id FROM text_requests WHERE task_id=? AND attempt=?", a.TaskID, a.Number).Scan(&id, &session, &parent); e != nil || session == "" {
		return tasks.Outcome{Status: tasks.Unknown}, nil
	}
	// A native process restart may lose its transient session. Queries never bind
	// this session to current media MCP scope or resubmit the original prompt.
	out, ready, e := h.textResult(ctx, id, session, parent)
	if !ready && e == nil {
		return tasks.Outcome{Status: tasks.Unknown}, nil
	}
	return out, e
}
