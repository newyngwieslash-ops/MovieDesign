package questions

import (
	"context"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"path/filepath"
	"testing"
)

func TestQuestionDecisionsDurableScopedAndStrict(t *testing.T) {
	ctx := context.Background()
	db, e := store.Open(filepath.Join(t.TempDir(), "questions.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p, e := (store.Objects{DB: db}).CreateProject(ctx, "project", "Choices")
	if e != nil {
		t.Fatal(e)
	}
	ev := events.NewService(db)
	defer ev.Close()
	ts := tasks.NewService(db, ev, nil)
	defer ts.Close()
	s := NewService(db, ts, ev)
	q, e := s.Ask(ctx, p.ID, Question{RequestID: "ask", SessionID: "session", Kind: "choice", Questions: []Info{{Header: "Pick", Question: "<script>literal</script>", Options: []Option{{Label: "A", Description: "first"}, {Label: "B", Description: "second"}}, Multiple: true}}})
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range [][][]string{nil, {}, {{"A", "A"}}, {{"unknown"}}, {{"A"}, {"B"}}} {
		if _, e = s.Reply(ctx, p.ID, q.ID, HumanReply{RequestID: "invalid", SessionID: "session", ExpectedRevision: 1, Answers: a}); e == nil {
			t.Fatal("invalid answers accepted", a)
		}
	}
	first, e := s.Reply(ctx, p.ID, q.ID, HumanReply{RequestID: "reply", SessionID: "session", ExpectedRevision: 1, Answers: [][]string{{"B", "A"}}})
	if e != nil {
		t.Fatal(e)
	}
	same, e := s.Reply(ctx, p.ID, q.ID, HumanReply{RequestID: "reply", SessionID: "session", ExpectedRevision: 1, Answers: [][]string{{"B", "A"}}})
	if e != nil || same.ID != first.ID {
		t.Fatal("receipt replay", same, e)
	}
	if _, e = s.Reply(ctx, p.ID, q.ID, HumanReply{RequestID: "reply", SessionID: "session", ExpectedRevision: 1, Answers: [][]string{{"A", "B"}}}); e == nil {
		t.Fatal("changed original body accepted")
	}
	if _, e = s.Reply(ctx, p.ID, q.ID, HumanReply{RequestID: "new", SessionID: "session", ExpectedRevision: 1, Answers: [][]string{{"A"}}}); e == nil {
		t.Fatal("old card accepted")
	}
	var actor string
	var n int
	db.QueryRow("SELECT actor FROM question_receipts WHERE question_id=?", q.ID).Scan(&actor)
	db.QueryRow("SELECT count(*) FROM question_receipts").Scan(&n)
	if actor != "human" || n != 1 {
		t.Fatal(actor, n)
	}
	list, e := s.List(ctx, p.ID, "other")
	if e != nil || len(list) != 0 {
		t.Fatal("wrong session", list, e)
	}
	list, e = s.List(ctx, p.ID, "session")
	if e != nil || len(list) != 1 || list[0].State != "answered" {
		t.Fatal(list, e)
	}
	replay, e := ev.Read(ctx, p.ID, "session", 0)
	if e != nil || len(replay) != 2 || replay[1].Type != "question_resolved" {
		t.Fatal("non-task event persistence", replay, e)
	}
	// Event failure must roll back both answer and receipt.
	q, e = s.Ask(ctx, p.ID, Question{RequestID: "rollback", SessionID: "session", Kind: "storyboard", Questions: []Info{{Header: "Storyboard", Question: "Approve plan", Options: []Option{{Label: "Approve"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TRIGGER fail_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'fixture'); END"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Reply(ctx, p.ID, q.ID, HumanReply{RequestID: "rollback-reply", SessionID: "session", ExpectedRevision: 1, Answers: [][]string{{"Approve"}}}); e == nil {
		t.Fatal("expected transaction failure")
	}
	var state string
	db.QueryRow("SELECT state FROM questions WHERE id=?", q.ID).Scan(&state)
	db.QueryRow("SELECT count(*) FROM question_receipts WHERE question_id=?", q.ID).Scan(&n)
	if state != "pending" || n != 0 {
		t.Fatal("partial decision committed", state, n)
	}
	if _, e = s.Ask(ctx, p.ID, Question{RequestID: "custom", SessionID: "session", Kind: "storyboard", Questions: []Info{{Header: "No", Question: "No", Custom: true}}}); e == nil {
		t.Fatal("storyboard custom authorization")
	}
}
func TestQuestionReplyRejectsBodyActor(t *testing.T) {
	for _, extra := range []string{`,"actor":"human"`, `,"channel":"human"`, `,"session_id":"fake"`, `,"Reject":true`} {
		var i HumanReply
		if e := json.Unmarshal([]byte(`{"requestId":"a","expected_revision":1,"answers":[["A"]]`+extra+`}`), &i); e == nil {
			t.Fatal("body authority accepted", extra)
		}
	}
}
