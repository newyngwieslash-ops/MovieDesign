package agent

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
)

type ChatMessageEnvelope struct {
	SessionID    string          `json:"session_id"`
	MessageID    string          `json:"message_id"`
	PartID       string          `json:"part_id,omitempty"`
	CallID       string          `json:"call_id,omitempty"`
	Step         int             `json:"step"`
	Type         string          `json:"type"`
	ContentDelta string          `json:"content_delta,omitempty"`
	Content      string          `json:"content,omitempty"`
	Tool         string          `json:"tool,omitempty"`
	State        string          `json:"state,omitempty"`
	PartType     string          `json:"part_type"`
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`
	ToolOutput   string          `json:"tool_output,omitempty"`
}
type ChatDonePayload struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	Done      bool   `json:"done"`
	Error     string `json:"error,omitempty"`
	Status    string `json:"status"`
}
type nativePart struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	Type      string `json:"type"`
	Text      string `json:"text"`
	CallID    string `json:"callID"`
	Tool      string `json:"tool"`
	State     struct {
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input,omitempty"`
		Output string          `json:"output,omitempty"`
	} `json:"state"`
}
type nativeMessage struct {
	Info struct {
		ID        string          `json:"id"`
		SessionID string          `json:"sessionID"`
		Role      string          `json:"role"`
		ParentID  string          `json:"parentID"`
		Finish    string          `json:"finish"`
		Error     json.RawMessage `json:"error"`
		Time      struct {
			Completed int64 `json:"completed"`
		} `json:"time"`
	} `json:"info"`
	Parts []nativePart `json:"parts"`
}

// Native deltas have no durable offset. Treat them as invalidation hints and read
// authoritative text; equal adjacent tokens remain intact, SSE replay adds none.
func (c *Chat) snapshotEvents(ctx context.Context, s ToolScope, id, parent string) (bool, error) {
	return c.readSnapshotEvents(ctx, s, id, parent, true)
}
func (c *Chat) readSnapshotEvents(ctx context.Context, s ToolScope, id, parent string, publishTerminal bool) (bool, error) {
	messages, e := c.Host.snapshot(ctx, s.NativeSessionID)
	if e != nil {
		return false, e
	}
	step := 0
	terminal := ""
	for _, m := range messages {
		if m.Info.SessionID != s.NativeSessionID || m.Info.Role != "assistant" || m.Info.ParentID != parent {
			continue
		}
		step++
		firstText := true
		for _, part := range m.Parts {
			if part.SessionID != s.NativeSessionID || part.MessageID != m.Info.ID {
				continue
			}
			if part.Type == "text" && firstText {
				firstText = false
				// The bundled runtime publishes live text deltas before updating
				// its HTTP snapshot. Use our durable, admitted prefix with that
				// native part identity, never a replay-unsafe raw delta offset.
				var prefix string
				e = c.DB.QueryRowContext(ctx, `SELECT coalesce(group_concat(text_delta,''),'') FROM (
				 SELECT j.text_delta FROM inference_stream j JOIN chat_dispatch d ON d.turn_id=j.turn_id
				 WHERE j.scope='chat' AND j.turn_id=? AND j.project_id=? AND j.session_id=? AND j.step=? AND d.message_id=? AND d.native_message_id=? ORDER BY j.seq)`, s.TurnID, s.ProjectID, s.SessionID, step, id, parent).Scan(&prefix)
				if e != nil {
					return false, e
				}
				if strings.HasPrefix(prefix, part.Text) {
					part.Text = prefix
				}
			}
			if e = c.savePart(ctx, s, id, step, part); e != nil {
				return false, e
			}
		}
		if len(m.Info.Error) > 0 && string(m.Info.Error) != "null" {
			terminal = "error"
		} else if m.Info.Time.Completed > 0 && m.Info.Finish != "" && m.Info.Finish != "tool-calls" && m.Info.Finish != "unknown" {
			terminal = "done"
		}
	}
	if terminal != "" {
		if !publishTerminal {
			return true, nil
		}
		message := ""
		if terminal == "error" {
			message = "Native turn failed; no automatic resubmission"
			var uncertain int
			if e = c.DB.QueryRowContext(context.WithoutCancel(ctx), "SELECT count(*) FROM inference_requests WHERE turn_id=? AND state IN ('forwarding','unknown')", s.TurnID).Scan(&uncertain); e != nil {
				return false, e
			}
			if uncertain > 0 {
				message = "Supplier acceptance uncertain; no automatic resubmission"
				terminal = "unknown"
			}
		}
		if e = c.finish(s, id, terminal, message, terminal); e != nil {
			return false, e
		}
		return true, nil
	}
	return false, nil
}
func (c *Chat) savePart(ctx context.Context, s ToolScope, id string, step int, p nativePart) error {
	ctx = context.WithoutCancel(ctx)
	if p.Type != "text" && p.Type != "reasoning" && p.Type != "tool" {
		return nil
	}
	tx, e := c.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	raw, _ := json.Marshal(p)
	var previous string
	e = tx.QueryRowContext(ctx, "SELECT snapshot_json FROM chat_parts WHERE session_id=? AND message_id=? AND part_id=?", s.SessionID, p.MessageID, p.ID).Scan(&previous)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if previous == string(raw) {
		return nil
	}
	var old nativePart
	_ = json.Unmarshal([]byte(previous), &old)
	frame := ChatMessageEnvelope{SessionID: s.SessionID, MessageID: id, PartID: p.ID, CallID: p.CallID, Step: step, Type: p.Type, Tool: p.Tool, State: p.State.Status}
	frame.PartType = p.Type
	frame.ToolInput = p.State.Input
	frame.ToolOutput = p.State.Output
	if p.Type == "tool" {
		frame.Type = "tool_call"
		if p.State.Status == "completed" || p.State.Status == "error" {
			frame.Type = "tool_result"
		}
	} else if strings.HasPrefix(p.Text, old.Text) {
		frame.Type = "content_delta"
		frame.ContentDelta = p.Text[len(old.Text):]
	} else {
		frame.Type = "content_replace"
		frame.Content = p.Text
	}
	history := frame
	history.ContentDelta = ""
	history.Content = p.Text
	public, _ := json.Marshal(history)
	if _, e = tx.ExecContext(ctx, "INSERT INTO chat_parts(session_id,message_id,part_id,snapshot_json,public_json)VALUES(?,?,?,?,?) ON CONFLICT(session_id,message_id,part_id)DO UPDATE SET snapshot_json=excluded.snapshot_json,public_json=excluded.public_json", s.SessionID, p.MessageID, p.ID, string(raw), string(public)); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO chat_messages(id,session_id,request_id,role,content_json,created_at) VALUES(?,?,?,'assistant',json_object('reply_to',?,'parts',json_array(json(?))),?) ON CONFLICT(id) DO UPDATE SET content_json=json_object('reply_to',?,'parts',json((SELECT json_group_array(json(public_json)) FROM chat_parts WHERE session_id=? AND message_id=?)))`, "assistant_"+p.MessageID, s.SessionID, "assistant_"+p.MessageID, id, string(public), time.Now().UnixMilli(), id, s.SessionID, p.MessageID); e != nil {
		return e
	}
	// Stable native IDs remain in private snapshots; normalized public frame has
	// local message identity and persisted part/call linkage, no upstream envelope.
	payload, _ := json.Marshal(frame)
	if e = events.Append(ctx, tx, s.ProjectID, s.SessionID, "", "chat_message", payload); e != nil {
		return e
	}
	if e = tx.Commit(); e == nil {
		c.Events.Notify()
	}
	return e
}
func (c *Chat) finish(s ToolScope, id, kind, message, state string) error {
	ctx := context.Background()
	tx, e := c.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(ctx, "UPDATE chat_dispatch SET terminal=1,state=?,error=? WHERE message_id=? AND terminal=0", state, message, id)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil
	}
	if kind == "unknown" {
		if e = restoreInferencePartial(ctx, tx, id); e != nil {
			return e
		}
	}
	payload, _ := json.Marshal(ChatDonePayload{SessionID: s.SessionID, MessageID: id, Done: true, Error: message, Status: kind})
	if e = events.Append(ctx, tx, s.ProjectID, s.SessionID, "", "chat_done", payload); e != nil {
		return e
	}
	if e = tx.Commit(); e == nil {
		c.Events.Notify()
	}
	return e
}
func (c *Chat) watch(s ToolScope, id, parent string, acceptance <-chan error) bool {
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	hints := make(chan struct{}, 1)
	eventErrors := make(chan struct{}, 1)
	nativeFailure := make(chan struct{}, 1)
	var streamDone = make(chan struct{})
	go func() {
		defer close(streamDone)
		c.eventHints(ctx, s.NativeSessionID, hints, eventErrors, nativeFailure)
	}()
	defer func() { cancel(); <-streamDone }()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	cancelSent := false
	settled := false
	acceptanceUnknown := false
	failed := false
	for {
		select {
		case <-nativeFailure:
			failed = true
		default:
		}
		select {
		case e := <-acceptance:
			settled = true
			acceptance = nil
			if errors.Is(e, errNativeRejected) {
				_ = c.finish(s, id, "error", "Native message rejected", "rejected")
				return c.end(s) == nil
			}
			if e != nil {
				acceptanceUnknown = true
				c.setState(id, "unknown", "Native prompt acceptance unknown")
			} else {
				c.setState(id, "accepted", "")
			}
		default:
		}
		var state string
		if c.DB.QueryRowContext(context.WithoutCancel(ctx), "SELECT state FROM chat_dispatch WHERE message_id=?", id).Scan(&state) != nil {
			return false
		}
		if state == "cancelling" {
			if !cancelSent {
				cancelSent = c.Host.Abort(ctx, s.NativeSessionID) == nil
			}
			// Unknown acceptance can be resolved only by positive completion for
			// the persisted parent, never by an absent native status entry.
			known := settled && !acceptanceUnknown
			if settled && acceptanceUnknown {
				done, e := c.readSnapshotEvents(ctx, s, id, parent, false)
				known = e == nil && done
			}
			if known && cancelSent {
				// The prompt may have arrived after the first abort.
				if c.Host.Abort(ctx, s.NativeSessionID) == nil && c.Host.WaitIdle(ctx, s.NativeSessionID) == nil {
					if e := c.finish(s, id, "cancelled", "", "cancelled"); e == nil {
						return c.end(s) == nil
					}
				}
			}
			select {
			case <-ctx.Done():
				return false
			case <-ticker.C:
			case <-c.wake:
			}
			continue
		}

		if failed && settled {
			status, message := "error", "Native turn failed; no automatic resubmission"
			var uncertain int
			if e := c.DB.QueryRow("SELECT count(*) FROM inference_requests WHERE turn_id=? AND state IN ('forwarding','unknown')", s.TurnID).Scan(&uncertain); e != nil {
				return false
			}
			if uncertain > 0 {
				status = "unknown"
				message = "Supplier acceptance uncertain; no automatic resubmission"
			}
			_ = c.finish(s, id, status, message, status)
			return c.end(s) == nil
		}
		var overBudget int
		if c.DB.QueryRowContext(context.WithoutCancel(ctx), "SELECT count(*) FROM agent_tool_calls WHERE turn_id=? AND accepted=0", s.TurnID).Scan(&overBudget) == nil && overBudget > 0 {
			_ = c.Host.Abort(ctx, s.NativeSessionID)
			_ = c.finish(s, id, "error", "Tool call limit exceeded", "error")
			return c.end(s) == nil
		}
		if e := c.syncQuestions(ctx, s); e != nil && errors.Is(e, ErrBudget) {
			_ = c.finish(s, id, "error", "Tool call limit exceeded", "error")
			return c.end(s) == nil
		}
		done, e := c.snapshotEvents(ctx, s, id, parent)
		if e == nil && done && settled {
			return c.end(s) == nil
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		case <-c.wake:
		case <-hints:
		case <-eventErrors:
		case <-nativeFailure:
			failed = true
		}
	}
}
func (c *Chat) eventHints(ctx context.Context, session string, hints, failures, nativeFailure chan<- struct{}) {
	for ctx.Err() == nil {
		req, e := http.NewRequestWithContext(ctx, "GET", c.Host.base+"/event", nil)
		if e != nil {
			return
		}
		req.SetBasicAuth("opencode", c.Host.password)
		client := *c.Host.client
		client.Timeout = 0
		response, e := client.Do(req)
		if e == nil {
			if response.StatusCode == 200 {
				scanner := bufio.NewScanner(response.Body)
				scanner.Buffer(make([]byte, 4096), 2<<20)
				for scanner.Scan() {
					if strings.HasPrefix(scanner.Text(), "data:") {
						var frame struct {
							Type       string `json:"type"`
							Properties struct {
								SessionID string `json:"sessionID"`
							} `json:"properties"`
						}
						if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "data:"))), &frame) == nil && frame.Type == "session.error" && frame.Properties.SessionID == session {
							select {
							case nativeFailure <- struct{}{}:
							default:
							}
						}
						select {
						case hints <- struct{}{}:
						default:
						}
					}
				}
			}
			response.Body.Close()
		}
		select {
		case failures <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}
