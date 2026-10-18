package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"strings"
	"testing"
)

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
