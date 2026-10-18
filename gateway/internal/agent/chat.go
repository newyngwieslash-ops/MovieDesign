package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

type Session struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
	State     string `json:"state"`
	CreatedAt int64  `json:"created_at"`
}
type ChatInput struct {
	RequestID   string            `json:"requestId"`
	Document    json.RawMessage   `json:"document"`
	Text        string            `json:"text"`
	References  []string          `json:"references"`
	Annotations []json.RawMessage `json:"annotations"`
}
type Message struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	RequestID string          `json:"requestId"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	State     string          `json:"state"`
	Error     string          `json:"error,omitempty"`
	CreatedAt int64           `json:"created_at"`
}

type Chat struct {
	DB         *sql.DB
	Turns      *Turns
	Events     *events.Service
	Questions  *questions.Service
	Host       *Host
	CloseEpoch func(string) error
	Grace      time.Duration
	ctx        context.Context
	cancel     context.CancelFunc
	wake       chan struct{}
	wg         sync.WaitGroup
	mu         sync.Mutex
	started    bool
}

func NewChat(db *sql.DB, t *Turns, ev *events.Service, q *questions.Service) *Chat {
	ctx, cancel := context.WithCancel(context.Background())
	return &Chat{DB: db, Turns: t, Events: ev, Questions: q, Grace: 10 * time.Second, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1)}
}
func (c *Chat) Close() { c.cancel(); c.wg.Wait() }
func (c *Chat) Stop()  { c.cancel() }
func (c *Chat) Notify() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
func (c *Chat) Start(h *Host, closeEpoch func(string) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return errors.New("chat already started")
	}
	if _, e := c.DB.Exec("UPDATE chat_dispatch SET state='unknown',error='Runtime restarted; acceptance requires reconciliation' WHERE state IN ('submitting','accepted','running','cancelling')"); e != nil {
		return e
	}
	if _, e := c.DB.Exec("UPDATE chat_sessions SET state='unknown' WHERE state='creating'"); e != nil {
		return e
	}
	if _, e := c.DB.Exec("UPDATE native_questions SET send_state='unknown' WHERE send_state='sending'"); e != nil {
		return e
	}
	c.Host = h
	c.CloseEpoch = closeEpoch
	if h.Relay != nil {
		h.Relay.Turns = c.Turns
	}
	c.started = true
	c.wg.Add(1)
	go func() { defer c.wg.Done(); c.run() }()
	c.Notify()
	return nil
}
func chatError(status int, message string) error {
	return &store.DomainError{Status: status, Code: map[bool]string{true: "not_found", false: "invalid_input"}[status == 404], Message: message}
}
func validScope(p, id string) bool {
	return p != "_app" && (store.Scope{ProjectID: p, Operation: "chat", RequestID: id}).Validate() == nil
}
func (c *Chat) Create(ctx context.Context, p string, raw []byte) (Session, error) {
	in, e := store.DecodeValue[struct {
		RequestID string `json:"requestId"`
		Title     string `json:"title"`
	}](raw)
	if e != nil || !validScope(p, in.RequestID) || strings.TrimSpace(in.Title) == "" || len(in.Title) > 256 {
		return Session{}, chatError(400, "Invalid session")
	}
	out, e := store.WriteRequest(ctx, c.DB, store.Scope{ProjectID: p, Operation: "chat.create", RequestID: in.RequestID}, raw, func(tx *sql.Tx) ([]byte, error) {
		s := Session{ID: uuid.NewString(), ProjectID: p, Title: in.Title, State: "new", CreatedAt: time.Now().UnixMilli()}
		_, e := tx.ExecContext(ctx, "INSERT INTO chat_sessions(id,project_id,title,state,created_at)VALUES(?,?,?,'new',?)", s.ID, p, s.Title, s.CreatedAt)
		if e != nil {
			return nil, e
		}
		return json.Marshal(s)
	})
	var s Session
	if e == nil {
		e = json.Unmarshal(out, &s)
	}
	return s, e
}
func (c *Chat) List(ctx context.Context, p string, offset, limit int) ([]Session, error) {
	if !validScope(p, "list") {
		return nil, chatError(400, "Invalid project")
	}
	var id string
	if c.DB.QueryRowContext(ctx, "SELECT id FROM projects WHERE id=?", p).Scan(&id) != nil {
		return nil, chatError(404, "Project not found")
	}
	rows, e := c.DB.QueryContext(ctx, "SELECT id,project_id,title,state,created_at FROM chat_sessions WHERE project_id=? ORDER BY created_at,rowid LIMIT ? OFFSET ?", p, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var s Session
		if e = rows.Scan(&s.ID, &s.ProjectID, &s.Title, &s.State, &s.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (c *Chat) owned(ctx context.Context, p, session string) error {
	if !validScope(p, session) {
		return chatError(400, "Invalid chat scope")
	}
	var id string
	if c.DB.QueryRowContext(ctx, "SELECT id FROM chat_sessions WHERE id=? AND project_id=?", session, p).Scan(&id) != nil {
		return chatError(404, "Session not found")
	}
	return nil
}
func (c *Chat) Messages(ctx context.Context, p, session string, offset, limit int) ([]Message, error) {
	if e := c.owned(ctx, p, session); e != nil {
		return nil, e
	}
	rows, e := c.DB.QueryContext(ctx, "SELECT m.id,m.session_id,m.request_id,m.role,m.content_json,coalesce(d.state,'completed'),coalesce(d.error,''),m.created_at FROM chat_messages m LEFT JOIN chat_dispatch d ON d.message_id=m.id WHERE m.session_id=? ORDER BY m.created_at,m.rowid LIMIT ? OFFSET ?", session, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var raw string
		if e = rows.Scan(&m.ID, &m.SessionID, &m.RequestID, &m.Role, &raw, &m.State, &m.Error, &m.CreatedAt); e != nil {
			return nil, e
		}
		m.Content = json.RawMessage(raw)
		out = append(out, m)
	}
	return out, rows.Err()
}
func (c *Chat) Send(ctx context.Context, p, session string, raw []byte) (Message, error) {
	if e := c.owned(ctx, p, session); e != nil {
		return Message{}, e
	}
	in, e := store.DecodeValue[ChatInput](raw)
	if e != nil || !validScope(p, in.RequestID) || strings.TrimSpace(in.Text) == "" || len(in.Text) > 65536 || len(in.References) > 16 || in.References == nil || in.Annotations == nil || len(in.Document) == 0 {
		return Message{}, chatError(400, "Invalid chat message")
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(in.Document, &doc) != nil || string(doc["type"]) != `"doc"` {
		return Message{}, chatError(400, "Invalid document")
	}
	out, e := store.WriteRequest(ctx, c.DB, store.Scope{ProjectID: p, Operation: "chat.send." + session, RequestID: in.RequestID}, raw, func(tx *sql.Tx) ([]byte, error) {
		for _, id := range in.References {
			var found string
			if tx.QueryRowContext(ctx, "SELECT asset_id FROM asset_projects WHERE asset_id=? AND project_id=?", id, p).Scan(&found) != nil {
				return nil, chatError(400, "Reference does not belong to project")
			}
		}
		m := Message{ID: uuid.NewString(), SessionID: session, RequestID: in.RequestID, Role: "user", Content: append([]byte(nil), raw...), State: "queued", CreatedAt: time.Now().UnixMilli()}
		if _, e := tx.ExecContext(ctx, "INSERT INTO chat_messages(id,session_id,request_id,role,content_json,created_at)VALUES(?,?,?,'user',?,?)", m.ID, session, in.RequestID, string(raw), m.CreatedAt); e != nil {
			return nil, e
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO chat_dispatch(message_id,native_message_id,state,created_at)VALUES(?,?,'queued',?)", m.ID, "msg_"+strings.ReplaceAll(uuid.NewString(), "-", ""), m.CreatedAt); e != nil {
			return nil, e
		}
		return json.Marshal(m)
	})
	if e != nil {
		return Message{}, e
	}
	var m Message
	if e = json.Unmarshal(out, &m); e != nil {
		return m, e
	}
	c.Notify()
	timer := time.NewTimer(c.Grace)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if e = c.DB.QueryRowContext(context.WithoutCancel(ctx), "SELECT state,error FROM chat_dispatch WHERE message_id=?", m.ID).Scan(&m.State, &m.Error); e != nil {
			return m, e
		}
		if m.State != "queued" && m.State != "submitting" {
			if m.State == "rejected" {
				return m, chatError(502, "Native message rejected")
			}
			return m, nil
		}
		select {
		case <-ctx.Done():
			return m, nil
		case <-timer.C:
			return m, nil
		case <-ticker.C:
		}
	}
}

func (c *Chat) run() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.wake:
		case <-ticker.C:
		}
		if c.Host == nil || c.Host.Relay == nil || !c.Host.Ready(c.ctx) {
			continue
		}
		c.reconcileInterrupted()
		for c.ctx.Err() == nil {
			c.reconcileInactiveQuestions()
			var blocked int
			if c.DB.QueryRow("SELECT count(*) FROM chat_dispatch WHERE terminal=0 AND state IN ('unknown','submitting','accepted','running','cancelling')").Scan(&blocked) != nil || blocked > 0 {
				break
			}
			var id, p, session, native, nativeMessage, raw, state string
			e := c.DB.QueryRow("SELECT d.message_id,s.project_id,s.id,coalesce(s.native_session_id,''),d.native_message_id,m.content_json,s.state FROM chat_dispatch d JOIN chat_messages m ON m.id=d.message_id JOIN chat_sessions s ON s.id=m.session_id WHERE d.state='queued' ORDER BY d.created_at,d.rowid LIMIT 1").Scan(&id, &p, &session, &native, &nativeMessage, &raw, &state)
			if e != nil {
				break
			}
			if c.Host == nil || c.Host.Relay == nil {
				break
			}
			if native == "" {
				if state != "new" {
					c.setState(id, "unknown", "Native session creation requires reconciliation")
					break
				}
				result, e := c.DB.Exec("UPDATE chat_sessions SET state='creating' WHERE id=? AND state='new'", session)
				if e != nil {
					break
				}
				n, _ := result.RowsAffected()
				if n != 1 {
					break
				}
				response, e := c.Host.request(c.ctx, "POST", "/session", map[string]string{"title": "MovieDesign " + session})
				var created struct {
					ID string `json:"id"`
				}
				if e != nil || json.Unmarshal(response, &created) != nil || created.ID == "" {
					_, _ = c.DB.Exec("UPDATE chat_sessions SET state='unknown' WHERE id=?", session)
					c.setState(id, "unknown", "Native session acceptance unknown")
					break
				}
				native = created.ID
				if _, e = c.DB.Exec("UPDATE chat_sessions SET native_session_id=?,state='ready' WHERE id=?", native, session); e != nil {
					break
				}
			}
			scope, e := c.Turns.Begin(context.WithoutCancel(c.ctx), p, session, native)
			if e != nil {
				break
			}
			result, e := c.DB.Exec("UPDATE chat_dispatch SET state='submitting',turn_id=? WHERE message_id=? AND state='queued'", scope.TurnID, id)
			if e != nil {
				if !c.failUnsent(scope, id) {
					return
				}
				continue
			}
			n, _ := result.RowsAffected()
			if n == 0 {
				if c.end(scope) != nil {
					return
				}
				continue
			}
			if e = c.Host.DisconnectTools(c.ctx); e == nil {
				e = c.CloseEpoch("")
			}
			if e == nil {
				e = c.Host.ConnectTools(c.ctx)
			}
			if e != nil {
				if !c.failUnsent(scope, id) {
					return
				}
				continue
			}
			var input ChatInput
			_ = json.Unmarshal([]byte(raw), &input)
			parts := []any{map[string]string{"type": "text", "text": input.Text}}
			if rules, e := c.Questions.ReadRules(c.ctx, p); e == nil && rules.Content != "" {
				parts = append(parts, map[string]string{"type": "text", "text": "当前项目已批准的长期规则：\n" + rules.Content})
			}
			if len(input.References) > 0 || len(input.Annotations) > 0 {
				data, _ := json.Marshal(struct {
					References  []string          `json:"references"`
					Annotations []json.RawMessage `json:"annotations"`
				}{input.References, input.Annotations})
				parts = append(parts, map[string]string{"type": "text", "text": string(data)})
			}
			acceptance := make(chan error, 1)
			c.wg.Add(1)
			go func() {
				defer c.wg.Done()
				_, e := c.Host.request(c.ctx, "POST", "/session/"+url.PathEscape(native)+"/prompt_async", map[string]any{"messageID": nativeMessage, "agent": "build", "parts": parts})
				if errors.Is(e, errNativeRejected) {
					c.setState(id, "rejected", "Native message rejected")
				} else if e != nil {
					c.setState(id, "unknown", "Native prompt acceptance unknown")
				} else {
					c.setState(id, "accepted", "")
				}
				acceptance <- e
				c.Notify()
			}()
			if !c.watch(scope, id, nativeMessage, acceptance) {
				break
			}
		}
	}
}

// Only the controller before prompt_async may assert that a message was never
// submitted. Keep ownership through terminal persistence and the ordinary
// idle/drain/disconnect/epoch barrier, including failures of either operation.
func (c *Chat) failUnsent(scope ToolScope, id string) bool {
	for {
		if c.finish(scope, id, "error", "Native setup failed before prompt submission; no inference sent", "error") == nil {
			return c.end(scope) == nil
		}
		select {
		case <-c.ctx.Done():
			return false
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// A new owned runtime may still have persisted native messages. Read them after
// restart; never recreate a session or POST an uncertain prompt/reply again.
func (c *Chat) reconcileInterrupted() {
	rows, e := c.DB.QueryContext(context.WithoutCancel(c.ctx), `SELECT d.message_id,d.native_message_id,d.state,s.project_id,s.id,coalesce(s.native_session_id,''),coalesce(t.id,''),coalesce(t.epoch,'') FROM chat_dispatch d JOIN chat_messages m ON m.id=d.message_id JOIN chat_sessions s ON s.id=m.session_id LEFT JOIN chat_turns t ON t.id=d.turn_id WHERE d.terminal=0 AND d.state IN ('unknown','cancelling') AND (t.state='interrupted' OR t.id IS NULL)`)
	if e != nil {
		return
	}
	type record struct {
		id, parent, state string
		s                 ToolScope
	}
	var records []record
	for rows.Next() {
		var r record
		if rows.Scan(&r.id, &r.parent, &r.state, &r.s.ProjectID, &r.s.SessionID, &r.s.NativeSessionID, &r.s.TurnID, &r.s.Epoch) == nil {
			records = append(records, r)
		}
	}
	rows.Close()
	for _, r := range records {
		if r.state == "cancelling" {
			if r.s.NativeSessionID != "" {
				if c.Host.Abort(c.ctx, r.s.NativeSessionID) != nil || c.Host.WaitIdle(c.ctx, r.s.NativeSessionID) != nil {
					continue
				}
			}
			_ = c.finish(r.s, r.id, "cancelled", "", "cancelled")
		} else if r.s.NativeSessionID != "" {
			_, _ = c.snapshotEvents(c.ctx, r.s, r.id, r.parent)
		}
	}
}
func (c *Chat) setState(id, state, message string) {
	_, _ = c.DB.Exec("UPDATE chat_dispatch SET state=?,error=? WHERE message_id=? AND terminal=0 AND state!='cancelling'", state, message, id)
}

// Retain the sole worker and old scope while retrying private cleanup only.
// A public terminal never authorizes a new epoch before this barrier succeeds.
func (c *Chat) end(scope ToolScope) error {
	for {
		e := c.Turns.End(c.ctx, scope, func(ctx context.Context) error { return c.Host.WaitIdle(ctx, scope.NativeSessionID) }, func(ctx context.Context) error {
			if e := c.Host.DisconnectTools(ctx); e != nil {
				return e
			}
			return c.CloseEpoch(scope.Epoch)
		})
		if e == nil {
			return nil
		}
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (c *Chat) Cancel(ctx context.Context, p, session string, raw []byte) (map[string]string, error) {
	if e := c.owned(ctx, p, session); e != nil {
		return nil, e
	}
	in, e := store.DecodeValue[struct {
		RequestID string `json:"requestId"`
		MessageID string `json:"message_id,omitempty"`
	}](raw)
	if e != nil || !validScope(p, in.RequestID) {
		return nil, chatError(400, "Invalid cancel")
	}
	_, e = store.WriteRequest(ctx, c.DB, store.Scope{ProjectID: p, Operation: "chat.cancel." + session, RequestID: in.RequestID}, raw, func(tx *sql.Tx) ([]byte, error) {
		if in.MessageID != "" {
			var state string
			if tx.QueryRowContext(ctx, "SELECT d.state FROM chat_dispatch d JOIN chat_messages m ON m.id=d.message_id WHERE m.session_id=? AND m.id=?", session, in.MessageID).Scan(&state) != nil {
				return nil, chatError(404, "Message not found")
			}
			if state != "queued" {
				return nil, chatError(409, "Only queued messages can be withdrawn")
			}
		}
		rows, e := tx.QueryContext(ctx, "SELECT d.message_id FROM chat_dispatch d JOIN chat_messages m ON m.id=d.message_id WHERE m.session_id=? AND (?='' OR d.message_id=?) AND d.state='queued' AND d.terminal=0", session, in.MessageID, in.MessageID)
		if e != nil {
			return nil, e
		}
		var ids []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		for _, id := range ids {
			payload, _ := json.Marshal(ChatDonePayload{SessionID: session, MessageID: id, Done: true, Status: "cancelled"})
			if e = events.Append(ctx, tx, p, session, "", "chat_done", payload); e != nil {
				return nil, e
			}
		}
		_, e = tx.ExecContext(ctx, "UPDATE chat_dispatch SET state=CASE WHEN state='queued' THEN 'cancelled' ELSE 'cancelling' END,terminal=CASE WHEN state='queued' THEN 1 ELSE terminal END WHERE message_id IN(SELECT id FROM chat_messages WHERE session_id=?) AND (?='' OR message_id=?) AND terminal=0", session, in.MessageID, in.MessageID)
		return []byte(`{"status":"cancelling"}`), e
	})
	if e != nil {
		return nil, e
	}
	c.Notify()
	c.Events.Notify()
	return map[string]string{"status": "cancelling"}, nil
}
