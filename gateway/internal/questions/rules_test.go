package questions

import (
	"context"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"path/filepath"
	"testing"
)

func TestProjectRulesRequireExactHumanCardAndRevision(t *testing.T) {
	ctx := context.Background()
	db, e := store.Open(filepath.Join(t.TempDir(), "rules.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p, e := (store.Objects{DB: db}).CreateProject(ctx, "p", "rules")
	if e != nil {
		t.Fatal(e)
	}
	ev := events.NewService(db)
	defer ev.Close()
	s := NewService(db, nil, ev)
	change := RulesChange{Content: "仅使用中文"}
	q, e := s.ProposeRules(ctx, p.ID, "session", "propose", change)
	if e != nil {
		t.Fatal(e)
	}
	if r, e := s.ReadRules(ctx, p.ID); e != nil || r.Content != "" || r.Revision != nil {
		t.Fatal("proposal saved without human", r, e)
	}
	receipt, e := s.Reply(ctx, p.ID, q.ID, HumanReply{RequestID: "approve", SessionID: "session", ExpectedRevision: 1, Answers: [][]string{{"保存项目规则"}}})
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.ReadRules(ctx, p.ID)
	if e != nil || r.Content != change.Content || r.Revision == nil || *r.Revision != receipt.ID {
		t.Fatal(r, e)
	}
	if _, e = s.ProposeRules(ctx, p.ID, "session", "stale", change); e == nil {
		t.Fatal("stale revision accepted")
	}
	if _, e = s.Ask(ctx, p.ID, Question{RequestID: "forge", SessionID: "session", Kind: "project_rules", Rules: &change}); e == nil {
		t.Fatal("public ask forged rule card")
	}
	replay, e := s.Reply(ctx, p.ID, q.ID, HumanReply{RequestID: "approve", SessionID: "session", ExpectedRevision: 1, Answers: [][]string{{"保存项目规则"}}})
	if e != nil || replay.ID != receipt.ID {
		t.Fatal(replay, e)
	}
	_, e = (store.Objects{DB: db}).Mutate(ctx, p.ID, "projects", store.Mutation{RequestID: "delete-rules-project", Action: "delete", ID: p.ID})
	var problem *store.DomainError
	if !errors.As(e, &problem) || problem.Status != 409 {
		t.Fatal("rule history must protect project with explicit conflict", e)
	}
}
