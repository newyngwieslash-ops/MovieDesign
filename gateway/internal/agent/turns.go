package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"sync"
	"time"
)

type ToolScope struct{ ProjectID, SessionID, TurnID, Epoch, NativeSessionID string }

var ErrScope = errors.New("no active owned agent turn")
var ErrTurnBusy = errors.New("agent turn queued behind active turn")

// ponytail: one active turn across the application. Upgrade to independent native
// runtimes and connection isolation before allowing concurrent sessions.
type Turns struct {
	DB      *sql.DB
	mu      sync.Mutex
	active  ToolScope
	closing bool
	work    sync.WaitGroup
	Abort   func(context.Context, string) error
	changed chan struct{}
}

func NewTurns(db *sql.DB) *Turns { return &Turns{DB: db, changed: make(chan struct{})} }

// Begin is a gateway controller API, never an MCP argument. A busy turn remains
// queued at the caller; only End after native idle and transport close releases it.
func (t *Turns) Begin(ctx context.Context, project, session, native string) (ToolScope, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active.TurnID != "" {
		return ToolScope{}, ErrTurnBusy
	}
	if native == "" {
		return ToolScope{}, ErrScope
	}
	if (store.Scope{ProjectID: project, Operation: "agent", RequestID: session}).Validate() != nil || project == "_app" {
		return ToolScope{}, ErrScope
	}
	tx, e := t.DB.BeginTx(ctx, nil)
	if e != nil {
		return ToolScope{}, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "INSERT INTO chat_sessions(id,project_id,native_session_id,created_at)VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING", session, project, native, time.Now().UnixMilli()); e != nil {
		return ToolScope{}, e
	}
	var p, n string
	if e = tx.QueryRowContext(ctx, "SELECT project_id,native_session_id FROM chat_sessions WHERE id=?", session).Scan(&p, &n); e != nil || p != project || n != native {
		return ToolScope{}, ErrScope
	}
	s := ToolScope{ProjectID: project, SessionID: session, NativeSessionID: native, TurnID: uuid.NewString(), Epoch: uuid.NewString()}
	if _, e = tx.ExecContext(ctx, "INSERT INTO chat_turns(id,session_id,epoch,state,created_at)VALUES(?,?,?,'active',?)", s.TurnID, session, s.Epoch, time.Now().UnixMilli()); e != nil {
		return ToolScope{}, e
	}
	if e = tx.Commit(); e != nil {
		return ToolScope{}, e
	}
	t.active = s
	t.closing = false
	return s, nil
}
func (t *Turns) Current() ToolScope {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closing {
		return ToolScope{}
	}
	return t.active
}
func (t *Turns) Acquire(s ToolScope) (func(), error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s.TurnID == "" || t.closing || s != t.active {
		return nil, ErrScope
	}
	t.work.Add(1)
	return t.work.Done, nil
}

// End keeps the old scope occupied on idle/disconnect failure. Never reassign a
// live transport to another project; the caller must recover/abort that runtime.
func (t *Turns) End(ctx context.Context, s ToolScope, idle, disconnect func(context.Context) error) error {
	if idle == nil || disconnect == nil {
		return ErrScope
	}
	if e := idle(ctx); e != nil {
		return e
	}
	t.mu.Lock()
	if s != t.active {
		t.mu.Unlock()
		return ErrScope
	}
	t.closing = true
	t.mu.Unlock()
	drained := make(chan struct{})
	go func() { t.work.Wait(); close(drained) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-drained:
	}
	if e := disconnect(ctx); e != nil {
		return e
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, e := t.DB.ExecContext(ctx, "UPDATE chat_turns SET state='closed' WHERE id=? AND state='active'", s.TurnID); e != nil {
		return e
	}
	t.active = ToolScope{}
	close(t.changed)
	t.changed = make(chan struct{})
	return nil
}

// Admit atomically persists the execution identity and increments the budget.
// A native event identity is verifiable; MCP executions use fresh gateway IDs
// because 1.19.0 does not forward native toolCallId. requestId is never a bypass.
func (t *Turns) Admit(ctx context.Context, s ToolScope, call, tool, request string, raw []byte) (bool, error) {
	tx, e := t.DB.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	var accepted int
	var priorTool, priorRequest, priorRaw string
	e = tx.QueryRowContext(ctx, "SELECT accepted,tool,request_id,input_json FROM agent_tool_calls WHERE turn_id=? AND call_id=?", s.TurnID, call).Scan(&accepted, &priorTool, &priorRequest, &priorRaw)
	if e == nil {
		if priorTool != tool || priorRequest != request || priorRaw != string(raw) {
			return false, store.ErrRequestConflict
		}
		if accepted == 0 {
			return false, ErrBudget
		}
		return false, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return false, e
	}
	result, e := tx.ExecContext(ctx, "UPDATE chat_turns SET call_count=call_count+1 WHERE id=? AND epoch=? AND state='active' AND call_count<40", s.TurnID, s.Epoch)
	if e != nil {
		return false, e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return false, e
	}
	if n == 1 {
		accepted = 1
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO agent_tool_calls(turn_id,call_id,tool,request_id,input_json,accepted,created_at)VALUES(?,?,?,?,?,?,?)", s.TurnID, call, tool, request, string(raw), accepted, time.Now().UnixMilli()); e != nil {
		return false, e
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	if accepted == 0 {
		if t.Abort != nil {
			abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = t.Abort(abortCtx, s.NativeSessionID)
		}
		return false, ErrBudget
	}
	return true, nil
}
func (t *Turns) Record(ctx context.Context, s ToolScope, call string, result []byte) error {
	_, e := t.DB.ExecContext(ctx, "UPDATE agent_tool_calls SET result_json=? WHERE turn_id=? AND call_id=?", string(result), s.TurnID, call)
	return e
}

type NativeQuestion struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionID"`
	Questions []questions.Info `json:"questions"`
	Tool      *struct {
		MessageID string `json:"messageID"`
		CallID    string `json:"callID"`
	} `json:"tool,omitempty"`
}

// GateQuestion must run before publishing a native question to UI. It emits only
// ordinary choice cards; it cannot mint billing/storyboard authorization.
func (t *Turns) GateQuestion(ctx context.Context, s ToolScope, q NativeQuestion, qs *questions.Service, reject func(context.Context, string) error) (questions.Question, error) {
	done, e := t.Acquire(s)
	if e != nil {
		return questions.Question{}, e
	}
	defer done()
	if q.SessionID != s.NativeSessionID || q.ID == "" || reject == nil {
		return questions.Question{}, ErrScope
	}
	call := "question:" + q.ID
	if q.Tool != nil && q.Tool.CallID != "" {
		call = "native:" + q.Tool.CallID
	}
	raw, _ := json.Marshal(q)
	_, e = t.Admit(ctx, s, call, "question", q.ID, raw)
	if e != nil {
		rejectCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = reject(rejectCtx, q.ID)
		return questions.Question{}, e
	}
	return qs.Ask(ctx, s.ProjectID, questions.Question{RequestID: q.ID, SessionID: s.SessionID, Kind: "choice", Questions: q.Questions})
}

// BeginQueued waits for the application's active turn to drain. The chat adapter
// persists user messages before calling this; cancellation never submits inference.
func (t *Turns) BeginQueued(ctx context.Context, project, session, native string) (ToolScope, error) {

	for {
		t.mu.Lock()
		changed := t.changed
		t.mu.Unlock()
		scope, e := t.Begin(ctx, project, session, native)
		if !errors.Is(e, ErrTurnBusy) {
			return scope, e
		}
		select {
		case <-ctx.Done():
			return ToolScope{}, ctx.Err()
		case <-changed:
		}
	}
}

// Recover runs once at gateway startup, before the new owned native host exists.
// Old transport tokens are gone; persisted calls remain audit/replay evidence.
func (t *Turns) Recover(ctx context.Context) error {
	_, e := t.DB.ExecContext(ctx, "UPDATE chat_turns SET state='interrupted' WHERE state='active'")
	return e
}
