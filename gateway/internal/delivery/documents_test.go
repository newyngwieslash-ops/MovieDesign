package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/pipeline"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/testsupport"
	"strings"
	"testing"
)

func TestStoryboardRecoveryCASAndFrozenActor(t *testing.T) {
	ctx := context.Background()
	for _, point := range []string{"prepare", "archive", "before_commit", "after_commit"} {
		t.Run(point, func(t *testing.T) {
			f := setup(t)
			testsupport.CreativeUpstream(t, f.db, f.p, "film", func(raw []byte) error {
				r, e := f.s.WriteDocument(ctx, f.p, raw)
				if e != nil {
					return e
				}
				if r.Delivery.Status != "completed" {
					return fmt.Errorf("%+v", r)
				}
				return nil
			})
			s := pipeline.Storyboard{Panels: []pipeline.Panel{{ID: "p", TimeMS: 0, Framing: "近景", Action: "口播", Transition: "连续"}}, Shots: []pipeline.Shot{{ID: "s", StartMS: 0, EndMS: 2000, PanelIDs: []string{"p"}, NarrativeRole: "讲述", StartState: "开始", EndState: "结束"}}, Timeline: pipeline.Timeline{DurationMS: 2000, ShotIDs: []string{"s"}, AudioIntent: "口播音乐"}}
			in := DocumentRequest{RequestID: "typed", Mode: "create", Format: "md", TypedDocument: &pipeline.Document{Kind: "storyboard", PipelineID: "film", Storyboard: s}, Output: f.input(t, "unused").Output}
			raw, _ := json.Marshal(in)
			f.s.fault = func(at string) error {
				if at == point {
					return errors.New("controlled crash")
				}
				return nil
			}
			first, e := f.s.WriteDocument(ctx, f.p, raw)
			if e != nil || first.Delivery.Status != "unknown" {
				t.Fatalf("fault %+v %v", first, e)
			}
			var key string
			if e = f.db.QueryRow("SELECT 'document-'||json_extract(response_json,'$.Key') FROM write_requests WHERE project_id=? AND operation='document.write' AND request_id='typed'", f.p).Scan(&key); e != nil {
				t.Fatal(e)
			}
			// Restart-equivalent fresh opposite-actor instance enters generic Recover.
			other := NewService(f.db, f.w, "agent")
			got, e := other.Recover(ctx, f.p, key)
			if e != nil || got.Delivery.Status != "completed" {
				t.Fatalf("recover %+v %v", got, e)
			}
			var origin string
			if e = f.db.QueryRow("SELECT origin FROM versions WHERE id=?", got.Delivery.Versions[0].VersionID).Scan(&origin); e != nil || origin != "user" {
				t.Fatal(origin, e)
			}
			again, e := other.WriteDocument(ctx, f.p, raw)
			if e != nil || again.Delivery.Versions[0] != got.Delivery.Versions[0] {
				t.Fatal("lost receipt", again, e)
			}
			if n := count(t, f.db, "SELECT count(*) FROM storyboard_revisions"); n != 1 {
				t.Fatal(n)
			}
			// Freeze two same-base plans before either final commit. First commits,
			// second must roll back all objects/revision and become durably blocked.
			in.Mode = "replace"
			in.TypedDocument.ExpectedRevision = 1
			in.Output.Resources[0].Mode = "new_version"
			in.Output.Resources[0].BaseVersionID = got.Delivery.Versions[0].VersionID
			keys := []string{}
			f.s.fault = func(at string) error {
				if at == "before_commit" {
					return errors.New("hold")
				}
				return nil
			}
			for _, id := range []string{"race-a", "race-b"} {
				in.RequestID = id
				in.TypedDocument.Storyboard.Panels[0].Action = id
				b, _ := json.Marshal(in)
				r, e := f.s.WriteDocument(ctx, f.p, b)
				if e != nil || r.Delivery.Status != "unknown" {
					t.Fatal(r, e)
				}
				var frozen string
				f.db.QueryRow("SELECT 'document-' || json_extract(response_json,'$.Key') FROM write_requests WHERE operation='document.write' AND request_id=?", id).Scan(&frozen)
				keys = append(keys, frozen)
			}
			a, e := other.Recover(ctx, f.p, keys[0])
			if e != nil || a.Delivery.Status != "completed" {
				t.Fatal(a, e)
			}
			b, e := other.Recover(ctx, f.p, keys[1])
			if e != nil || b.Delivery.Status != "blocked" {
				t.Fatal(b, e)
			}
			replay, e := other.Recover(ctx, f.p, keys[1])
			if e != nil || replay.Delivery.Status != "blocked" {
				t.Fatal(replay, e)
			}
			if n := count(t, f.db, "SELECT count(*) FROM versions"); n != 4 {
				t.Fatal("orphan SQL version", n)
			}
			if n := count(t, f.db, "SELECT count(*) FROM storyboard_revisions"); n != 2 {
				t.Fatal(n)
			}
		})
	}
}

func TestDocumentRequiresExplicitContentAndReplaysFrozenUnknown(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	o := f.input(t, "unused").Output
	b, _ := json.Marshal(DocumentRequest{RequestID: "missing", Mode: "create", Format: "md", Output: o})
	if _, e := f.s.WriteDocument(ctx, f.p, b); e == nil {
		t.Fatal("missing content accepted as empty document")
	}
	b, _ = json.Marshal(DocumentRequest{RequestID: "unknown", Mode: "create", Format: "md", Content: "frozen", Output: o})
	f.s.fault = func(at string) error {
		if at == "archive" {
			return errors.New("interrupted archive")
		}
		return nil
	}
	first, e := f.s.WriteDocument(ctx, f.p, b)
	if e != nil || first.Delivery.Status != "unknown" {
		t.Fatal(first, e)
	}
	var reserved string
	if e = f.db.QueryRow("SELECT version_id FROM delivery_sources").Scan(&reserved); e != nil {
		t.Fatal(e)
	}
	f.s = NewService(f.db, f.w, "agent")
	got, e := f.s.WriteDocument(ctx, f.p, b)
	if e != nil || got.Delivery.Status != "completed" || got.Delivery.Versions[0].VersionID != reserved || count(t, f.db, "SELECT count(*) FROM versions WHERE origin='user'") != 1 {
		t.Fatal(got, e)
	}
	c := store.Canvas{DB: f.db}
	d, e := c.Document(ctx, f.p)
	if e != nil {
		t.Fatal(e)
	}
	for id, node := range d.Nodes {
		raw, _ := json.Marshal(map[string]any{"requestId": "remove", "expectedRevision": d.Revision, "command": map[string]any{"type": "canvas.patch", "nodes": []any{map[string]any{"id": id, "before": node, "after": nil}}, "edges": []any{}}})
		if _, e = c.Execute(ctx, f.p, raw); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = f.s.WriteDocument(ctx, f.p, b); e != nil {
		t.Fatal(e)
	}
	d, e = c.Document(ctx, f.p)
	if e != nil || len(d.Nodes) != 0 {
		t.Fatal("replay resurrected deleted node", d, e)
	}
	if _, e = f.s.WriteDocument(ctx, f.p, []byte(strings.Replace(string(b), "frozen", "changed", 1))); e == nil {
		t.Fatal("unknown replay input changed")
	}
}

func TestDocumentImmutableRevisionReplayAndAtomicPatch(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	o := f.input(t, "unused").Output
	request := DocumentRequest{RequestID: "doc-create", Mode: "create", Format: "md", Content: "hello\nworld", Output: o}
	run := func(i DocumentRequest) (Receipt, error) {
		b, _ := json.Marshal(i)
		return f.s.WriteDocument(ctx, f.p, b)
	}
	first, e := run(request)
	if e != nil || first.Delivery.Status != "completed" {
		t.Fatal(first, e)
	}
	var canvas string
	if e = f.db.QueryRow("SELECT document_json FROM canvas_documents WHERE project_id=?", f.p).Scan(&canvas); e != nil {
		t.Fatal("document was not placed on Canvas", e)
	}
	var document struct {
		Nodes map[string]any `json:"nodes"`
	}
	if json.Unmarshal([]byte(canvas), &document) != nil || len(document.Nodes) != 1 {
		t.Fatal(canvas)
	}
	replay, e := run(request)
	if e != nil || replay.Delivery.Versions[0] != first.Delivery.Versions[0] {
		t.Fatal(replay, e)
	}
	request.RequestID = "doc-patch"
	request.Mode = "patch"
	request.Content = ""
	request.Output.Resources[0].Mode = "new_version"
	request.Output.Resources[0].BaseVersionID = first.Delivery.Versions[0].VersionID
	request.Edits = []DocumentEdit{{Exact: "world", Replacement: "reader", Occurrence: 1}}
	second, e := run(request)
	if e != nil || second.Delivery.Status != "completed" {
		t.Fatal(second, e)
	}
	if second.Delivery.Versions[0].ResourceID != first.Delivery.Versions[0].ResourceID || second.Delivery.Versions[0].VersionID == first.Delivery.Versions[0].VersionID {
		t.Fatal("document identity lost")
	}
	request.Edits[0].Replacement = "changed"
	if _, e = run(request); e == nil {
		t.Fatal("changed replay accepted")
	}
	request.RequestID = "bad-patch"
	request.Edits[0].Exact = "absent"
	if _, e = run(request); e == nil {
		t.Fatal("missing anchor accepted")
	}
	if n := count(t, f.db, "SELECT count(*) FROM versions"); n != 2 {
		t.Fatal(n)
	}
	request.RequestID = "bad-format"
	request.Format = "pdf"
	if _, e = run(request); e == nil {
		t.Fatal("unsupported format")
	}
}
