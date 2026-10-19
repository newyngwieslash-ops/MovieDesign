package pipeline_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/pipeline"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
	"path/filepath"
	"sync"
	"testing"
)

func TestApprovalRequiresUpstreamHumanGates(t *testing.T) {
	f := approvalSetup(t)
	if _, e := f.write("storyboard", "ungated", 0, "", ""); e == nil {
		t.Fatal("storyboard without upstream human gates committed")
	}
	if _, e := f.write("script", "script-ungated", 0, "", "script"); e == nil {
		t.Fatal("script without intent approval committed")
	}
}

type approvalFixture struct {
	t    *testing.T
	db   *sql.DB
	docs *delivery.Service
	q    *questions.Service
	p    string
}

func approvalSetup(t *testing.T) *approvalFixture {
	t.Helper()
	ctx := context.Background()
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	w, e := workspace.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { w.Close() })
	p, e := (store.Objects{DB: db}).CreateProject(ctx, "approval-project", "Approval")
	if e != nil {
		t.Fatal(e)
	}
	ev := events.NewService(db)
	t.Cleanup(ev.Close)
	return &approvalFixture{t, db, delivery.NewService(db, w, "user"), questions.NewService(db, nil, ev), p.ID}
}
func (f *approvalFixture) write(gate, key string, revision int64, base, content string) (delivery.Receipt, error) {
	d := pipeline.Document{Kind: gate, PipelineID: "film", ExpectedRevision: revision, Content: content}
	if gate == "storyboard" {
		d.Content = ""
		d.Storyboard = pipeline.Storyboard{Panels: []pipeline.Panel{{ID: "p", TimeMS: 0, Framing: "close", Action: "speak", Transition: "continuous"}}, Shots: []pipeline.Shot{{ID: "s", StartMS: 0, EndMS: 2000, PanelIDs: []string{"p"}, NarrativeRole: "intro", StartState: "start", EndState: "end"}}, Timeline: pipeline.Timeline{DurationMS: 2000, ShotIDs: []string{"s"}, AudioIntent: "none"}}
		switch content {
		case "panel":
			d.Storyboard.Panels[0].Action = "changed"
		case "shot":
			d.Storyboard.Shots[0].NarrativeRole = "changed"
		case "timeline":
			d.Storyboard.Timeline.AudioIntent = "music"
		}
	}
	mode, outmode := "create", "create"
	if base != "" {
		mode, outmode = "replace", "new_version"
	}
	res := map[string]any{"key": gate, "mode": outmode, "name": gate}
	if base != "" {
		res["baseVersionId"] = base
	}
	raw, _ := json.Marshal(map[string]any{"requestId": key, "mode": mode, "format": "md", "typedDocument": d, "output": map[string]any{"resources": []any{res}, "composition": map[string]any{"name": "Film", "type": map[string]any{"label": "video"}, "placements": []any{map[string]any{"key": gate, "role": gate}}}}})
	return f.docs.WriteDocument(context.Background(), f.p, raw)
}
func (f *approvalFixture) create(gate, key string, rev int64, base, content string) pipeline.Binding {
	f.t.Helper()
	r, e := f.write(gate, key, rev, base, content)
	if e != nil || r.Delivery.Status != "completed" {
		f.t.Fatalf("write %s: %+v %v", key, r, e)
	}
	b, e := pipeline.BindingForVersion(context.Background(), f.db, f.p, r.Delivery.Versions[0].VersionID)
	if e != nil {
		f.t.Fatal(e)
	}
	return b
}
func (f *approvalFixture) propose(key string, b pipeline.Binding) questions.Question {
	f.t.Helper()
	q, e := f.q.ProposeCreative(context.Background(), f.p, "human-session", key, b)
	if e != nil {
		f.t.Fatal(e)
	}
	return q
}
func (f *approvalFixture) reply(q questions.Question, key string) (questions.Receipt, error) {
	return f.q.Reply(context.Background(), f.p, q.ID, questions.HumanReply{SessionID: q.SessionID, RequestID: key, ExpectedRevision: q.Revision, Answers: [][]string{{q.Questions[0].Options[0].Label}}})
}
func (f *approvalFixture) approve(b pipeline.Binding) questions.Question {
	f.t.Helper()
	q := f.propose("propose-"+b.DocumentVersionID, b)
	if _, e := f.reply(q, "reply-"+b.DocumentVersionID); e != nil {
		f.t.Fatal(e)
	}
	return q
}
func (f *approvalFixture) upstream() (pipeline.Binding, pipeline.Binding) {
	i := f.create("intent", "intent", 0, "", "intent")
	f.approve(i)
	s := f.create("script", "script", 0, "", "script")
	f.approve(s)
	return i, s
}
func (f *approvalFixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if e := f.db.QueryRow(query, args...).Scan(&n); e != nil {
		f.t.Fatal(e)
	}
	return n
}
func TestApprovalReceiptBindingReplayAndAtomicRollback(t *testing.T) {
	f := approvalSetup(t)
	f.upstream()
	b := f.create("storyboard", "board", 0, "", "")
	q := f.propose("board-card", b)
	if _, e := f.q.Reply(context.Background(), "other", q.ID, questions.HumanReply{SessionID: q.SessionID, RequestID: "cross", ExpectedRevision: 1, Answers: [][]string{{"批准分镜"}}}); e == nil {
		t.Fatal("cross-project card accepted")
	}
	tx, e := f.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pipeline.ApproveStoryboard(context.Background(), tx, pipeline.HumanReceipt{ProjectID: f.p, QuestionID: q.ID, ReceiptID: "fake", Binding: b}); e == nil {
		t.Fatal("forged human receipt accepted")
	}
	tx.Rollback()
	if _, e = f.db.Exec(`CREATE TRIGGER approval_failure BEFORE INSERT ON approvals WHEN NEW.gate='storyboard' BEGIN SELECT RAISE(ABORT,'controlled approval failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = f.reply(q, "rollback"); e == nil {
		t.Fatal("expected rollback")
	}
	if f.count("SELECT count(*) FROM question_receipts WHERE question_id=?", q.ID) != 0 {
		t.Fatal("receipt survived failed approval transaction")
	}
	if _, e = f.db.Exec("DROP TRIGGER approval_failure"); e != nil {
		t.Fatal(e)
	}
	r, e := f.reply(q, "approve")
	if e != nil {
		t.Fatal(e)
	}
	same, e := f.reply(q, "approve")
	if e != nil || r.ID != same.ID {
		t.Fatal("double-click replay changed", r, same, e)
	}
	if _, e = f.reply(q, "duplicate-new-key"); e == nil {
		t.Fatal("second approval accepted")
	}
	if f.count("SELECT count(*) FROM approvals WHERE gate='storyboard'") != 1 {
		t.Fatal("duplicate approval")
	}
	current, e := pipeline.ReadVersion(context.Background(), f.db, f.p, b.DocumentVersionID)
	if e != nil || current.ApprovalStatus != "approved" {
		t.Fatal(current, e)
	}
	a := pipeline.Approval{Binding: b, ReceiptID: r.ID}
	if !pipeline.ApprovalMatches(current, a) {
		t.Fatal("exact binding mismatch")
	}
	a.Revision++
	if pipeline.ApprovalMatches(current, a) {
		t.Fatal("revision ignored")
	}
	a.Revision--
	a.SemanticHash = "wrong"
	if pipeline.ApprovalMatches(current, a) {
		t.Fatal("hash ignored")
	}
	// Re-instantiated service replay retains original human receipt and no new effect.
	f.q = questions.NewService(f.db, nil, f.q.Events)
	same, e = f.reply(q, "approve")
	if e != nil || same.ID != r.ID {
		t.Fatal("restart replay", same, e)
	}
}
func TestInvalidationDirectionalAndFrozenContracts(t *testing.T) {
	for _, change := range []string{"panel", "shot", "timeline"} {
		t.Run(change, func(t *testing.T) {
			f := approvalSetup(t)
			intent, script := f.upstream()
			b := f.create("storyboard", "board", 0, "", "")
			q := f.approve(b)
			late := f.propose("late", b)
			for _, status := range []string{"pending", "ready", "submitted"} {
				if _, e := f.db.Exec("INSERT INTO prompt_contracts(id,pipeline_id,revision,document_version_id,content_json,status)VALUES(?,'film',1,?,'{\"frozen\":true}',?)", status, b.DocumentVersionID, status); e != nil {
					t.Fatal(e)
				}
			}
			next := f.create("storyboard", "edit", 1, b.DocumentVersionID, change)
			if f.count("SELECT count(*) FROM approvals WHERE gate IN ('intent','script') AND valid=1") != 2 {
				t.Fatal("downstream edit invalidated upstream")
			}
			if f.count("SELECT count(*) FROM approvals WHERE gate='storyboard' AND valid=1") != 0 {
				t.Fatal("stale approval authorized new revision")
			}
			if _, e := f.reply(late, "late-human"); e == nil {
				t.Fatal("late old card accepted")
			}
			if f.count("SELECT count(*) FROM question_receipts WHERE question_id=?", late.ID) != 0 {
				t.Fatal("late receipt persisted")
			}
			if _, e := f.reply(q, "new-old-key"); e == nil {
				t.Fatal("old approved card accepted")
			}
			if f.count("SELECT count(*) FROM prompt_contracts WHERE status='invalidated'") != 2 || f.count("SELECT count(*) FROM prompt_contracts WHERE status='submitted' AND content_json='{\"frozen\":true}'") != 1 {
				t.Fatal("contract invalidation altered frozen submitted facts")
			}
			f.approve(next)
			// Provider/execution params do not participate in creative revision/hash.
			if _, e := f.db.Exec("UPDATE prompt_contracts SET content_json='{\"resolution\":\"1080p\",\"price\":2}' WHERE status='invalidated'"); e != nil {
				t.Fatal(e)
			}
			r, e := pipeline.ReadVersion(context.Background(), f.db, f.p, next.DocumentVersionID)
			if e != nil || r.ApprovalStatus != "approved" {
				t.Fatal("provider params invalidated semantics", r, e)
			}
			updatedScript := f.create("script", "script-change", 1, script.DocumentVersionID, "new script")
			if f.count("SELECT count(*) FROM approvals WHERE gate='intent' AND valid=1") != 1 || f.count("SELECT count(*) FROM approvals WHERE gate IN ('script','storyboard') AND valid=1") != 0 {
				t.Fatal("script invalidation direction")
			}
			if _, e = f.write("storyboard", "blocked", 2, next.DocumentVersionID, change); e == nil {
				t.Fatal("unapproved new script allowed storyboard")
			}
			f.approve(updatedScript)
			if e = pipeline.CheckBinding(context.Background(), f.db, f.p, next); e == nil {
				t.Fatal("old storyboard reapproved against new script")
			}
			rebound := f.create("storyboard", "rebind", 2, next.DocumentVersionID, change)
			if rebound.Revision != 3 {
				t.Fatal("same board on changed source must create revision")
			}
			f.approve(rebound)
			f.create("intent", "intent-change", 1, intent.DocumentVersionID, "new intent")
			if f.count("SELECT count(*) FROM approvals WHERE valid=1") != 0 {
				t.Fatal("intent invalidation failed")
			}
			if f.count("SELECT count(*) FROM question_receipts") < 5 {
				t.Fatal("historical receipts deleted")
			}
		})
	}
}
func TestApprovalEditRaceAndUntypedGuard(t *testing.T) {
	f := approvalSetup(t)
	_, script := f.upstream()
	b := f.create("storyboard", "board", 0, "", "")
	q := f.propose("race", b)
	var wg sync.WaitGroup
	wg.Add(2)
	var replyErr, errorWrite error
	go func() { defer wg.Done(); _, replyErr = f.reply(q, "race-reply") }()
	go func() {
		defer wg.Done()
		_, errorWrite = f.write("storyboard", "race-edit", 1, b.DocumentVersionID, "panel")
	}()
	wg.Wait()
	if errorWrite != nil {
		t.Fatal(errorWrite)
	}
	if f.count("SELECT count(*) FROM approvals WHERE gate='storyboard' AND valid=1") != 0 {
		t.Fatal("race left stale approval valid", replyErr)
	}
	for _, mode := range []string{"replace", "patch"} {
		body := map[string]any{"requestId": "untyped-" + mode, "mode": mode, "format": "md", "output": map[string]any{"resources": []any{map[string]any{"key": "script", "mode": "new_version", "name": "script", "baseVersionId": script.DocumentVersionID}}, "composition": map[string]any{"name": "Film", "type": map[string]any{"label": "video"}, "placements": []any{map[string]any{"key": "script", "role": "script"}}}}}
		if mode == "replace" {
			body["content"] = "silent script change"
		} else {
			body["edits"] = []any{map[string]any{"exact": "script", "occurrence": 1, "replacement": "changed"}}
		}
		raw, _ := json.Marshal(body)
		r, e := f.docs.WriteDocument(context.Background(), f.p, raw)
		if e == nil && r.Delivery.Status != "blocked" {
			t.Fatal("untyped registered artifact replacement accepted", r)
		}
	}
	if f.count("SELECT count(*) FROM creative_artifacts WHERE gate='script'") != 1 {
		t.Fatal("untyped edit changed creative artifact")
	}
}
