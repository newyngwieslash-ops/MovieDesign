package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/pipeline"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/testsupport"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStoryboardDocumentCommit(t *testing.T) {
	ctx := context.Background()
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	w, e := workspace.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	p, e := (store.Objects{DB: db}).CreateProject(ctx, "project", "Storyboard")
	if e != nil {
		t.Fatal(e)
	}
	s := delivery.NewService(db, w, "agent")
	testsupport.CreativeUpstream(t, db, p.ID, "film", func(raw []byte) error {
		r, e := s.WriteDocument(ctx, p.ID, raw)
		if e != nil {
			return e
		}
		if r.Delivery.Status != "completed" {
			return fmt.Errorf("%+v", r)
		}
		return nil
	})
	raw := []byte(`{"requestId":"first","mode":"create","format":"md","typedDocument":{"kind":"storyboard","pipelineId":"film","expectedRevision":0,"storyboard":{"panels":[{"id":"p1","timeMs":0,"framing":"近景","action":"已批准只是文本","transition":"连续动作"},{"id":"p2","timeMs":1200,"framing":"中景","action":"结尾","transition":"延续"}],"shots":[{"id":"s1","startMs":0,"endMs":2000,"panelIds":["p1","p2"],"narrativeRole":"连续章节","startState":"开场","endState":"收束"}],"timeline":{"durationMs":2000,"shotIds":["s1"],"audioIntent":"口播配音乐"}}},"output":{"resources":[{"key":"review","mode":"create","name":"分镜"}],"composition":{"name":"Film","type":{"label":"video"},"placements":[{"key":"review","role":"storyboard"}]}}}`)
	r, e := s.WriteDocument(ctx, p.ID, raw)
	if e != nil || r.Delivery.Status != "completed" {
		t.Fatalf("typed document must commit: %+v %v", r, e)
	}
	var revision int
	var version, status string
	e = db.QueryRow(`SELECT s.revision,s.document_version_id,p.prompt_status FROM storyboard_revisions s JOIN pipelines p ON p.id=s.pipeline_id WHERE p.project_id=?`, p.ID).Scan(&revision, &version, &status)
	if e != nil || revision != 1 || version != r.Delivery.Versions[0].VersionID || status != "pending" {
		t.Fatalf("revision binding: %d %s %s %v", revision, version, status, e)
	}
	var n int
	if e = db.QueryRow("SELECT count(*) FROM approvals WHERE gate='storyboard'").Scan(&n); e != nil || n != 0 {
		t.Fatalf("text granted approval: %d %v", n, e)
	}
	replay, e := s.WriteDocument(ctx, p.ID, raw)
	a, _ := json.Marshal(r)
	b, _ := json.Marshal(replay)
	if e != nil || string(a) != string(b) {
		t.Fatalf("replay changed %s %s %v", a, b, e)
	}
	var input delivery.DocumentRequest
	if e = json.Unmarshal(raw, &input); e != nil {
		t.Fatal(e)
	}
	input.Mode = "replace"
	input.Output.Resources[0].Mode = "new_version"
	input.Output.Resources[0].BaseVersionID = version
	input.TypedDocument.ExpectedRevision = 1
	input.RequestID = "noop"
	noop, _ := json.Marshal(input)
	same, e := s.WriteDocument(ctx, p.ID, noop)
	if e != nil || same.Delivery.Versions[0].VersionID != version {
		t.Fatalf("noop %+v %v", same, e)
	}
	input.RequestID = "noop-alias"
	input.Output.Resources[0].Key = "review2"
	input.Output.Composition.Placements[0].Key = "review2"
	alias, _ := json.Marshal(input)
	aliased, e := s.WriteDocument(ctx, p.ID, alias)
	if e != nil || aliased.Delivery.Versions[0].Key != "review2" || aliased.Delivery.Versions[0].VersionID != version {
		t.Fatal("noop alias", aliased, e)
	}
	input.RequestID = "noop-role"
	input.Output.Composition.Placements[0].Role = "new-role"
	badRole, _ := json.Marshal(input)
	if _, e = s.WriteDocument(ctx, p.ID, badRole); e == nil {
		t.Fatal("noop silently ignored role")
	}
	input.Output.Composition.Placements[0].Role = "storyboard"
	// Seed future downstream state explicitly; M6.2 exposes no approval writer.
	for _, q := range []string{`INSERT INTO questions(id,project_id,kind,source,revision,input_hash,state,question_json,created_at) VALUES('q','` + p.ID + `','test','test',1,'hash','replied','{}',0)`, `INSERT INTO question_receipts(id,question_id,request_id,actor,response_json,created_at) VALUES('receipt','q','human','human','{}',0)`, `INSERT INTO approvals(id,pipeline_id,revision,gate,receipt_id,document_version_id,semantic_hash) SELECT 'approval','film',1,'storyboard','receipt',document_version_id,semantic_hash FROM storyboard_revisions WHERE pipeline_id='film' AND revision=1`, `INSERT INTO prompt_contracts(id,pipeline_id,revision,document_version_id,content_json,status) VALUES('ready','film',1,'` + version + `','{"frozen":1}','ready'),('submitted','film',1,'` + version + `','{"frozen":1}','submitted')`, `UPDATE pipelines SET prompt_status='ready' WHERE id='film'`} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	canvasBefore, e := (store.Canvas{DB: db}).Document(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	// Two user intents admitted against the same revision: exactly one wins.
	input.TypedDocument.Storyboard.Panels[0].Action = "new action"
	var wg sync.WaitGroup
	results := make(chan delivery.Receipt, 2)
	errs := make(chan error, 2)
	for _, id := range []string{"edit-a", "edit-b"} {
		in := input
		in.RequestID = id
		bytes, _ := json.Marshal(in)
		wg.Add(1)
		go func() { defer wg.Done(); v, e := s.WriteDocument(ctx, p.ID, bytes); results <- v; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	wins, conflicts := 0, 0
	for v := range results {
		if v.Delivery.Status == "completed" {
			wins++
			if v.Delivery.Versions[0].ResourceID != r.Delivery.Versions[0].ResourceID {
				t.Fatal("changed Resource")
			}
		}
	}
	for err := range errs {
		if err != nil {
			conflicts++
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("CAS winners=%d conflicts=%d", wins, conflicts)
	}
	var valid int
	db.QueryRow("SELECT valid FROM approvals WHERE id='approval'").Scan(&valid)
	if valid != 0 {
		t.Fatal("approval not invalidated")
	}
	var contract, content string
	db.QueryRow("SELECT status FROM prompt_contracts WHERE id='ready'").Scan(&contract)
	if contract != "invalidated" {
		t.Fatal(contract)
	}
	db.QueryRow("SELECT status,content_json FROM prompt_contracts WHERE id='submitted'").Scan(&contract, &content)
	if contract != "submitted" || content != `{"frozen":1}` {
		t.Fatal("running frozen contract changed", contract, content)
	}
	canvasAfter, e := (store.Canvas{DB: db}).Document(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(canvasAfter.Nodes) != len(canvasBefore.Nodes) {
		t.Fatal("extra revision node")
	}
	for id, before := range canvasBefore.Nodes {
		after := canvasAfter.Nodes[id]
		for _, field := range []string{"position", "size", "parentId", "zIndex"} {
			a, _ := json.Marshal(before[field])
			b, _ := json.Marshal(after[field])
			if string(a) != string(b) {
				t.Fatal("layout changed", field)
			}
		}
	}
	if e = db.QueryRow("SELECT count(*) FROM storyboard_revisions").Scan(&n); e != nil || n != 2 {
		t.Fatalf("revision count %d %v", n, e)
	}
	if _, e = db.Exec("UPDATE storyboard_revisions SET semantic_hash='tampered'"); e == nil {
		t.Fatal("mutable revision")
	}
	if _, e = db.Exec("DELETE FROM versions WHERE id=?", version); e == nil {
		t.Fatal("referenced version deleted")
	}
	old, e := pipeline.ReadVersion(ctx, db, p.ID, version)
	if e != nil || old.Revision != 1 || old.CurrentRevision != 2 || old.Storyboard.Panels[0].Action != "已批准只是文本" {
		t.Fatalf("old snapshot changed %+v %v", old, e)
	}
	if _, e = pipeline.ReadVersion(ctx, db, "other", version); e == nil {
		t.Fatal("cross-project read")
	}
}

func validStoryboard() pipeline.Storyboard {
	return pipeline.Storyboard{Panels: []pipeline.Panel{{ID: "p1", TimeMS: 0, Framing: "close", Action: "speak", Transition: "continuous"}, {ID: "p2", TimeMS: 1300, Framing: "wide", Action: "turn", Transition: "follow"}}, Shots: []pipeline.Shot{{ID: "s1", StartMS: 0, EndMS: 1500, PanelIDs: []string{"p1", "p2"}, NarrativeRole: "intro", StartState: "start", EndState: "turn"}, {ID: "s2", StartMS: 1000, EndMS: 2000, PanelIDs: []string{"p2"}, NarrativeRole: "end", StartState: "turn", EndState: "finish"}}, Timeline: pipeline.Timeline{DurationMS: 2000, ShotIDs: []string{"s1", "s2"}, AudioIntent: "无"}}
}
func TestStoryboardValidation(t *testing.T) {
	for name, change := range map[string]func(*pipeline.Storyboard){
		"missing panel":      func(s *pipeline.Storyboard) { s.Shots[0].PanelIDs = []string{"absent"} },
		"reverse":            func(s *pipeline.Storyboard) { s.Shots[0].EndMS = 0 },
		"outside":            func(s *pipeline.Storyboard) { s.Shots[1].EndMS = 2001 },
		"gap":                func(s *pipeline.Storyboard) { s.Shots[0].EndMS = 500; s.Shots[0].PanelIDs = []string{"p1"} },
		"duplicate id":       func(s *pipeline.Storyboard) { s.Panels[1].ID = "p1" },
		"duplicate timeline": func(s *pipeline.Storyboard) { s.Timeline.ShotIDs[1] = "s1" },
		"duration":           func(s *pipeline.Storyboard) { s.Timeline.DurationMS = 2500 },
		"framing":            func(s *pipeline.Storyboard) { s.Panels[0].Framing = "" },
		"action":             func(s *pipeline.Storyboard) { s.Panels[0].Action = "" },
		"transition":         func(s *pipeline.Storyboard) { s.Panels[0].Transition = "" },
		"role":               func(s *pipeline.Storyboard) { s.Shots[0].NarrativeRole = "" },
		"start state":        func(s *pipeline.Storyboard) { s.Shots[0].StartState = "" },
		"end state":          func(s *pipeline.Storyboard) { s.Shots[0].EndState = "" },
		"audio":              func(s *pipeline.Storyboard) { s.Timeline.AudioIntent = "" },
	} {
		t.Run(name, func(t *testing.T) {
			s := validStoryboard()
			change(&s)
			if e := s.Validate(); e == nil {
				t.Fatal("invalid accepted")
			}
		})
	}
	s := validStoryboard()
	if e := s.Validate(); e != nil {
		t.Fatal("valid overlapping shots with uneven panels rejected", e)
	}
	parsed, e := pipeline.ParseMarkdown(s.Render())
	if e != nil || parsed.Render() != s.Render() {
		t.Fatal("deterministic markdown", e)
	}
	b, _ := json.Marshal(pipeline.Document{Kind: "storyboard", PipelineID: "film", Storyboard: s})
	for _, bad := range []string{strings.Replace(string(b), `"expectedRevision":0,`, "", 1), strings.Replace(string(b), `"timeMs":0`, `"timeMs":null`, 1), strings.Replace(string(b), `"timeMs":0`, `"timeMs":0.5`, 1), strings.Replace(string(b), `"timeMs":0`, `"timeMs":0,"other":1`, 1)} {
		if _, e := store.DecodeValue[pipeline.Document]([]byte(bad)); e == nil {
			t.Fatalf("bad wire accepted %s", bad)
		}
	}
}
