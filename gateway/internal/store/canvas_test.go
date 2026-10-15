package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/migrations"
	"path/filepath"
	"strings"
	"testing"
)

func canvasFrame(id string, x int) map[string]any {
	return map[string]any{"id": id, "kind": "frame", "parentId": nil, "position": map[string]any{"x": x, "y": 0}, "size": map[string]any{"width": 350, "height": 250}, "zIndex": 0, "hidden": false, "title": "Frame", "collapsed": false}
}
func canvasInput(key string, rev int, before, after any) []byte {
	b, _ := json.Marshal(map[string]any{"requestId": key, "expectedRevision": rev, "command": map[string]any{"type": "canvas.patch", "nodes": []any{map[string]any{"id": "n", "before": before, "after": after}}, "edges": []any{}}})
	return b
}
func TestCanvasAtomicReplay(t *testing.T) {
	s, p, _ := objectFixture(t)
	c := Canvas{DB: s.DB}
	ctx := context.Background()
	a, b := canvasFrame("n", 0), canvasFrame("n", 20)
	first := canvasInput("first", 0, nil, a)
	r, e := c.Execute(ctx, p, first)
	if e != nil || r.Document.Revision != 1 {
		t.Fatalf("first: %+v %v", r, e)
	}
	if _, e = c.Execute(ctx, p, canvasInput("stale", 0, a, b)); e == nil {
		t.Fatal("stale accepted")
	}
	if _, e = c.Execute(ctx, p, canvasInput("bad-before", 1, nil, b)); e == nil {
		t.Fatal("before ignored")
	}
	if _, e = c.Execute(ctx, p, canvasInput("next", 1, a, b)); e != nil {
		t.Fatal(e)
	}
	r, e = c.Execute(ctx, p, first)
	if e != nil || !r.Replayed || r.AppliedRevision != 1 || r.Document.Revision != 2 {
		t.Fatalf("dynamic replay: %+v %v", r, e)
	}
	if _, e = c.Execute(ctx, p, canvasInput("first", 0, nil, b)); e == nil {
		t.Fatal("different input reused")
	}
	for _, raw := range []string{`{"type":"canvas.patch","nodes":[],"edges":[]}`, `{"type":"canvas.batch","commands":[]}`, `{"type":"canvas.batch","commands":[{"type":"canvas.batch","commands":[]}]}`} {
		v := []byte(`{"requestId":"bad","expectedRevision":2,"command":` + raw + `}`)
		if _, e = c.Execute(ctx, p, v); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, e = c.Execute(ctx, p, canvasInput("noop", 2, b, b)); e == nil {
		t.Fatal("noop accepted")
	}
	bad := canvasFrame("n", 1)
	bad["parentId"] = "n"
	if _, e = c.Execute(ctx, p, canvasInput("cycle", 2, b, bad)); e == nil {
		t.Fatal("cycle accepted")
	}
	d, e := c.Document(ctx, p)
	if e != nil || d.Revision != 2 {
		t.Fatalf("partial mutation: %+v %v", d, e)
	}
}
func TestCanvasObjectReferencesAndProjection(t *testing.T) {
	s, p, q := objectFixture(t)
	ctx := context.Background()
	c := Canvas{DB: s.DB}
	typ := objectID(t, mutateObject(t, s, p, "composition_types", "type", "create", "", `{"label":"素材"}`))
	a := objectID(t, mutateObject(t, s, p, "compositions", "a", "create", "", `{"type_id":"`+typ+`","name":"A"}`))
	b := objectID(t, mutateObject(t, s, p, "compositions", "b", "create", "", `{"type_id":"`+typ+`","name":"B"}`))
	r := objectID(t, mutateObject(t, s, p, "resources", "r", "create", "", `{"composition_id":"`+a+`"}`))
	v := objectID(t, mutateObject(t, s, p, "versions", "v", "create", "", `{"resource_id":"`+r+`"}`))
	if _, e := s.DB.Exec("INSERT INTO composition_members(composition_id,resource_id,role) VALUES(?,?,?)", b, r, "reference"); e != nil {
		t.Fatal(e)
	}
	content := func(id string) map[string]any {
		out, e := c.Content(ctx, p, []byte(`{"compositionIds":["`+id+`"]}`))
		if e != nil {
			t.Fatal(e)
		}
		return out["items"].([]any)[0].(map[string]any)
	}
	before := content(b)["revision"].(int64)
	mutateObject(t, s, p, "resources", "rename", "update", r, `{"description":"renamed"}`)
	if content(b)["revision"].(int64) <= before {
		t.Fatal("member resource change did not revise containing composition")
	}
	n := canvasFrame("n", 0)
	delete(n, "title")
	delete(n, "collapsed")
	n["kind"] = "composition"
	n["compositionId"] = b
	n["draft"] = nil
	n["activeItem"] = map[string]any{"type": "version", "resourceId": r, "versionId": v}
	n["memberSelections"] = map[string]any{r: map[string]any{"type": "generation-request", "requestId": "pending", "outputKey": "image"}}
	if _, e := c.Execute(ctx, q, canvasInput("cross", 0, nil, n)); e == nil {
		t.Fatal("cross project reference")
	}
	if _, e := c.Execute(ctx, p, canvasInput("add", 0, nil, n)); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Execute(ctx, p, canvasInput("remove", 1, n, nil)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Mutate(ctx, p, "versions", Mutation{RequestID: "delete", Action: "delete", ID: v}); e == nil {
		t.Fatal("durable undo reference unprotected")
	}
	bad := canvasFrame("n", 0)
	delete(bad, "title")
	delete(bad, "collapsed")
	bad["kind"] = "composition"
	bad["compositionId"] = b
	bad["draft"] = nil
	bad["activeItem"] = map[string]any{"type": "generation-request", "requestId": "r", "outputKey": "x"}
	if _, e := c.Execute(ctx, p, canvasInput("bad", 2, nil, bad)); e == nil {
		t.Fatal("pending active accepted")
	}
	v2 := objectID(t, mutateObject(t, s, p, "versions", "v2", "create", "", `{"resource_id":"`+r+`"}`))
	mutateObject(t, s, p, "versions", "del-v2", "delete", v2, "")
	v3 := objectID(t, mutateObject(t, s, p, "versions", "v3", "create", "", `{"resource_id":"`+r+`"}`))
	var ordinal int
	if e := s.DB.QueryRow("SELECT ordinal FROM canvas_version_state WHERE version_id=?", v3).Scan(&ordinal); e != nil || ordinal != 3 {
		t.Fatalf("ordinal reused: %d %v", ordinal, e)
	}
}
func TestCanvasUpgradeFromM2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m2.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"000001_assets.up.sql", "000002_projects.up.sql", "000003_workspace_requests.up.sql", "000004_delivery_journal.up.sql"} {
		raw, e := migrations.Files.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	_, e = db.Exec(`CREATE TABLE schema_migrations(version uint64 NOT NULL PRIMARY KEY,dirty bool NOT NULL); INSERT INTO schema_migrations VALUES(4,false);
 INSERT INTO projects VALUES('p','Legacy',1);INSERT INTO composition_types VALUES('t','p','Type');INSERT INTO compositions(id,project_id,type_id,name) VALUES('c','p','t','Old');INSERT INTO resources(id,composition_id) VALUES('r','c');INSERT INTO composition_members(composition_id,resource_id,role) VALUES('c','r','source');
 INSERT INTO assets(id,path,status,size,mtime_ms,name,created_at,updated_at) VALUES('a','legacy/file.png','active',20,1,'file.png',1,1);INSERT INTO asset_projects VALUES('a','p');INSERT INTO versions(id,resource_id,asset_id,created_at) VALUES('v1','r','a',10);INSERT INTO versions(id,resource_id,asset_id,source_version_id,created_at) VALUES('v2','r','a','v1',10);`)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	db, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	var ordinal int
	var head, source, file string
	if e = db.QueryRow("SELECT ordinal FROM canvas_version_state WHERE version_id='v2'").Scan(&ordinal); e != nil || ordinal != 2 {
		t.Fatal(ordinal, e)
	}
	if e = db.QueryRow("SELECT current_version_id FROM canvas_resource_state WHERE resource_id='r'").Scan(&head); e != nil || head != "v2" {
		t.Fatal(head, e)
	}
	db.Close()
	db, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = db.QueryRow("SELECT source_version_id FROM versions WHERE id='v2'").Scan(&source); e != nil || source != "v1" {
		t.Fatal(source, e)
	}
	if e = db.QueryRow("SELECT path FROM assets WHERE id='a'").Scan(&file); e != nil || file != "legacy/file.png" {
		t.Fatal(file, e)
	}
}
func TestCanvasGraphAndBoundaries(t *testing.T) {
	s, p, _ := objectFixture(t)
	c := Canvas{DB: s.DB}
	ctx := context.Background()
	a := canvasFrame("n", 0)
	attempt := func(command any, ok bool) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"requestId": "intent", "expectedRevision": 0, "command": command})
		_, e := c.Execute(ctx, p, raw)
		if (e == nil) != ok {
			t.Fatalf("command %v: %v", command, e)
		}
	}
	ch := map[string]any{"id": "n", "before": nil, "after": a}
	for _, command := range []any{
		map[string]any{"type": "canvas.patch", "nodes": []any{ch, ch}, "edges": []any{}},
		map[string]any{"type": "canvas.patch", "nodes": []any{ch}, "edges": []any{map[string]any{"id": "edge", "before": nil, "after": map[string]any{"id": "edge", "source": "n", "target": "absent"}}}},
		map[string]any{"type": "canvas.patch", "nodes": []any{ch}, "edges": []any{map[string]any{"id": "n", "before": nil, "after": map[string]any{"id": "n", "source": "n", "target": "other"}}}},
		map[string]any{"type": "canvas.patch", "nodes": []any{}, "edges": nil},
		map[string]any{"type": "content.import", "items": []any{}},
	} {
		attempt(command, false)
	}
	for _, field := range []string{"title", "hidden", "position", "parentId"} {
		bad := canvasFrame("n", 0)
		delete(bad, field)
		attempt(map[string]any{"type": "canvas.patch", "nodes": []any{map[string]any{"id": "n", "before": nil, "after": bad}}, "edges": []any{}}, false)
	}
	bad := canvasFrame("n", 0)
	bad["title"] = strings.Repeat("😀", 2049)
	attempt(map[string]any{"type": "canvas.patch", "nodes": []any{map[string]any{"id": "n", "before": nil, "after": bad}}, "edges": []any{}}, false)
	a["title"] = strings.Repeat("😀", 2048)
	attempt(map[string]any{"type": "canvas.patch", "nodes": []any{ch}, "edges": []any{}}, true)
	raw := canvasInput("later", 1, a, canvasFrame("n", 5))
	if _, e := c.Execute(ctx, p, raw); e != nil {
		t.Fatal(e)
	}
	// Reopening the actual database preserves the source-specific dynamic replay receipt.
	var filename string
	if e := s.DB.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&filename); e != nil {
		t.Fatal(e)
	}
	s.DB.Close()
	db, e := Open(filename)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	replay, e := (Canvas{DB: db}).Execute(ctx, p, raw)
	if e != nil || !replay.Replayed || replay.Document.Revision != 2 {
		t.Fatal(replay, e)
	}
}

func TestCanvasExactEnumAdmission(t *testing.T) {
	for _, tc := range []struct{ field, tokens string }{{"backgroundColor", "red orange yellow green cyan blue purple"}, {"expectedKind", "image video audio text file table subject"}, {"preset", "gallery board"}, {"region", "visual details"}} {
		t.Run(tc.field, func(t *testing.T) {
			s, p, _ := objectFixture(t)
			ctx := context.Background()
			c := Canvas{DB: s.DB}
			typ := objectID(t, mutateObject(t, s, p, "composition_types", "type", "create", "", `{"label":"Type"}`))
			composition := objectID(t, mutateObject(t, s, p, "compositions", "composition", "create", "", `{"type_id":"`+typ+`","name":"Subject"}`))
			makeNode := func(value string) map[string]any {
				n := canvasFrame("n", 0)
				if tc.field == "backgroundColor" {
					n[tc.field] = value
					return n
				}
				delete(n, "title")
				delete(n, "collapsed")
				n["draft"] = nil
				if tc.field == "expectedKind" {
					n["kind"] = "placeholder"
					n[tc.field] = value
					return n
				}
				n["kind"] = "composition"
				n["compositionId"] = composition
				n["activeItem"] = nil
				layout := map[string]any{"preset": "gallery", "columns": 1, "sections": []any{map[string]any{"id": "default", "title": "", "resources": []any{}}}, "spans": []any{}, "rows": []any{map[string]any{"sectionId": "default", "region": "visual", "resources": []any{}}}}
				if tc.field == "preset" {
					layout[tc.field] = value
				} else {
					layout["rows"].([]any)[0].(map[string]any)[tc.field] = value
				}
				n["subjectLayout"] = layout
				return n
			}
			tokens := strings.Fields(tc.tokens)
			var before any
			revision := 0
			for _, value := range tokens {
				after := makeNode(value)
				r, e := c.Execute(ctx, p, canvasInput("valid-"+value, revision, before, after))
				if e != nil {
					t.Fatalf("valid %q: %v", value, e)
				}
				revision++
				before = r.Document.Nodes["n"]
			}
			original, e := c.Document(ctx, p)
			if e != nil {
				t.Fatal(e)
			}
			originalJSON, _ := json.Marshal(original)
			for i, value := range []string{tokens[0] + " " + tokens[1], "", " ", "\t", " " + tokens[0], tokens[0] + " ", tokens[0][:len(tokens[0])-1], tokens[0] + "x"} {
				if _, e := c.Execute(ctx, p, canvasInput(fmt.Sprint("invalid-", i), revision, before, makeNode(value))); e == nil {
					t.Errorf("accepted %s=%q", tc.field, value)
				}
				current, e := c.Document(ctx, p)
				if e != nil {
					t.Fatal(e)
				}
				currentJSON, _ := json.Marshal(current)
				if string(currentJSON) != string(originalJSON) {
					t.Fatalf("rejected enum changed document: %q", value)
				}
				var receipts int
				if e = s.DB.QueryRow("SELECT count(*) FROM canvas_commands WHERE project_id=?", p).Scan(&receipts); e != nil || receipts != revision {
					t.Fatalf("rejected enum changed receipts: %d %v", receipts, e)
				}
			}
		})
	}
}

func TestTaskReferencesAndCanvasOutputs(t *testing.T) {
	s, p, q := objectFixture(t)
	ctx := context.Background()
	typ := objectID(t, mutateObject(t, s, p, "composition_types", "type", "create", "", `{"label":"Shots"}`))
	cid := objectID(t, mutateObject(t, s, p, "compositions", "comp", "create", "", `{"type_id":"`+typ+`","name":"Shot"}`))
	rid := objectID(t, mutateObject(t, s, p, "resources", "res", "create", "", `{"composition_id":"`+cid+`"}`))
	vid := objectID(t, mutateObject(t, s, p, "versions", "ver", "create", "", `{"resource_id":"`+rid+`"}`))
	_, e := s.DB.Exec(`INSERT INTO tasks(id,project_id,request_id,status,frozen_input_json,provider_json,view_json,created_at,updated_at,current_attempt)VALUES('task',?,'task','waiting_user','{}','{}','{}',1,1,1)`, p)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.Exec("INSERT INTO task_attempts(task_id,attempt,state,created_at)VALUES('task',1,'billing',1)")
	if e != nil {
		t.Fatal(e)
	}
	source := `{"composition_id":"` + cid + `","type_id":"` + typ + `","ordinal":1}`
	_, e = s.DB.Exec("INSERT INTO task_outputs(task_output_key,task_id,attempt,output_key,role,state,version_id,source_json)VALUES('opaque','task',1,'image','image','declared',?,?)", vid, source)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Mutate(ctx, p, "versions", Mutation{RequestID: "delete", Action: "delete", ID: vid}); e == nil {
		t.Fatal("task output version deleted")
	}
	n := canvasFrame("n", 0)
	delete(n, "title")
	delete(n, "collapsed")
	n["kind"] = "composition"
	n["compositionId"] = cid
	n["draft"] = nil
	n["activeItem"] = map[string]any{"type": "task-output", "taskId": "task", "outputKey": "image"}
	c := Canvas{DB: s.DB}
	if _, e = c.Execute(ctx, p, canvasInput("taskref", 0, nil, n)); e != nil {
		t.Fatal("durable task output unavailable", e)
	}
	if _, e = c.Execute(ctx, q, canvasInput("foreign", 0, nil, n)); e == nil {
		t.Fatal("cross project task ref accepted")
	}
	out, e := c.Content(ctx, p, []byte(`{"compositionIds":["`+cid+`"]}`))
	if e != nil {
		t.Fatal(e)
	}
	outputs := out["items"].([]any)[0].(map[string]any)["taskOutputs"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("missing task output projection: %v", outputs)
	}
}
