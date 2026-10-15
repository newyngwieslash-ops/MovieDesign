package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"sync"
	"time"
)

type Event struct {
	ID        int64           `json:"id"`
	ProjectID string          `json:"project_id"`
	SessionID string          `json:"session_id"`
	TaskID    string          `json:"task_id,omitempty"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt int64           `json:"created_at"`
}
type Service struct {
	DB       *sql.DB
	mu       sync.Mutex
	watchers map[chan struct{}]struct{}
	closed   bool
}

func NewService(db *sql.DB) *Service { return &Service{DB: db, watchers: map[chan struct{}]struct{}{}} }
func (s *Service) Subscribe() (<-chan struct{}, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan struct{}, 1)
	if s.closed {
		close(ch)
	} else {
		s.watchers[ch] = struct{}{}
	}
	return ch, func() { s.mu.Lock(); delete(s.watchers, ch); s.mu.Unlock() }
}
func (s *Service) Notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.watchers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		for ch := range s.watchers {
			close(ch)
			delete(s.watchers, ch)
		}
	}
}

// Append belongs to the caller's transaction. Notify only after successful commit.
func Append(ctx context.Context, tx *sql.Tx, p, session, task, kind string, payload []byte) error {
	if p == "" || p == "_app" || !json.Valid(payload) {
		return &store.DomainError{Status: 400, Code: "invalid_input", Message: "Invalid event scope or payload"}
	}
	var exists string
	if e := tx.QueryRowContext(ctx, "SELECT id FROM tasks WHERE id=? AND project_id=? AND session_id=?", task, p, session).Scan(&exists); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, "INSERT INTO events(project_id,session_id,task_id,type,payload_json,created_at) VALUES(?,?,?,?,?,?)", p, session, task, kind, string(payload), time.Now().UnixMilli())
	return e
}
func (s *Service) Read(ctx context.Context, p, session string, after int64) ([]Event, error) {
	out := []Event{}
	if e := (store.Scope{ProjectID: p, Operation: "events", RequestID: "read"}).Validate(); e != nil || p == "_app" || after < 0 {
		return nil, &store.DomainError{Status: 400, Code: "invalid_input", Message: "Invalid event scope or cursor"}
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var id string
	if e = tx.QueryRowContext(ctx, "SELECT id FROM projects WHERE id=?", p).Scan(&id); e != nil {
		return nil, &store.DomainError{Status: 404, Code: "not_found", Message: "Project not found"}
	}
	// The global ID sequence may have gaps from other projects. A nonzero cursor
	// must belong to this exact subscription; never reveal another project's event.
	if after > 0 {
		var n int
		if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE id=? AND project_id=? AND (?='' OR session_id=?)", after, p, session, session).Scan(&n); e != nil {
			return nil, e
		}
		if n != 1 {
			return nil, &store.DomainError{Status: 400, Code: "invalid_input", Message: "Cursor does not belong to this subscription"}
		}
	}
	rows, e := tx.QueryContext(ctx, "SELECT id,project_id,session_id,coalesce(task_id,''),type,payload_json,created_at FROM events WHERE project_id=? AND (?='' OR session_id=?) AND id>? ORDER BY id LIMIT 100", p, session, session, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var v Event
		var raw string
		if e = rows.Scan(&v.ID, &v.ProjectID, &v.SessionID, &v.TaskID, &v.Type, &raw, &v.CreatedAt); e != nil {
			return nil, e
		}
		v.Payload = json.RawMessage(raw)
		out = append(out, v)
	}
	return out, rows.Err()
}
