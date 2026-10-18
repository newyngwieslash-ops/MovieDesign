package questions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"unicode/utf8"
)

type RulesChange struct {
	Content          string  `json:"content"`
	ExpectedRevision *string `json:"expectedRevision"`
}
type Rules struct {
	Content  string  `json:"content"`
	Revision *string `json:"revision"`
}

func readRules(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, p string) (Rules, error) {
	var r Rules
	var id string
	e := q.QueryRowContext(ctx, "SELECT content,receipt_id FROM project_rules WHERE project_id=?", p).Scan(&r.Content, &id)
	if errors.Is(e, sql.ErrNoRows) {
		return r, nil
	}
	if e == nil {
		r.Revision = &id
	}
	return r, e
}
func (s *Service) ReadRules(ctx context.Context, p string) (Rules, error) {
	return readRules(ctx, s.DB, p)
}
func checkRules(ctx context.Context, tx *sql.Tx, p string, i RulesChange) error {
	r, e := readRules(ctx, tx, p)
	if e != nil {
		return e
	}
	if (r.Revision == nil) != (i.ExpectedRevision == nil) || r.Revision != nil && *r.Revision != *i.ExpectedRevision {
		return fail(409, "Project rules changed; read and merge before proposing")
	}
	return nil
}

// ProposeRules creates a candidate only. The sole human Reply transaction saves it.
func (s *Service) ProposeRules(ctx context.Context, p, session, key string, i RulesChange) (Question, error) {
	var q Question
	if len(i.Content) > 16<<10 || !utf8.ValidString(i.Content) || session == "" || scope(p, key) != nil {
		return q, fail(400, "Invalid project rule proposal")
	}
	raw, _ := json.Marshal(struct {
		Session string
		Change  RulesChange
	}{session, i})
	b, e := store.WriteRequest(ctx, s.DB, store.Scope{ProjectID: p, Operation: "rules.propose", RequestID: key}, raw, func(tx *sql.Tx) ([]byte, error) {
		if e := checkRules(ctx, tx, p, i); e != nil {
			return nil, e
		}
		q, e := s.insert(ctx, tx, p, Question{RequestID: key, SessionID: session, Kind: "project_rules", Rules: &i, Questions: []Info{{Header: "长期项目规则", Question: "仅当你明确要求长期保存这些规则时确认。完整替换内容：\n" + i.Content, Options: []Option{{Label: "保存项目规则", Description: "长期保存完整内容；单次反馈请拒绝此卡"}}}}})
		if e != nil {
			return nil, e
		}
		return json.Marshal(q)
	})
	if e == nil {
		e = json.Unmarshal(b, &q)
		s.Events.Notify()
	}
	return q, e
}
