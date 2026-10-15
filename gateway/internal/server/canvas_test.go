package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanvasHTTPAdmission(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "canvas.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "Canvas", "p")
	if e != nil {
		t.Fatal(e)
	}
	h := newTestServer(t, "http://tauri.localhost", db, t.TempDir())
	call := func(method, path, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path+"?project_id="+p.ID, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	changes := []any{}
	for i := 0; i < 10000; i++ {
		id := fmt.Sprint("n", i)
		changes = append(changes, map[string]any{"id": id, "before": nil, "after": map[string]any{"id": id, "kind": "frame", "parentId": nil, "position": map[string]int{"x": i * 500, "y": 0}, "size": map[string]int{"width": 350, "height": 250}, "zIndex": i, "hidden": false, "title": "Frame", "collapsed": false}})
	}
	raw, _ := json.Marshal(map[string]any{"requestId": "large", "expectedRevision": 0, "command": map[string]any{"type": "canvas.patch", "nodes": changes, "edges": []any{}}})
	if len(raw) <= 1<<20 {
		t.Fatal("fixture not >1MiB")
	}
	w := call("POST", "/api/canvas/commands", string(raw), "http://tauri.localhost")
	if w.Code != 200 {
		t.Fatalf("large valid: %d %s", w.Code, w.Body.String())
	}
	var receipt store.CanvasReceipt
	json.Unmarshal(w.Body.Bytes(), &receipt)
	if len(receipt.Document.Nodes) != 10000 || receipt.Document.Revision != 1 {
		t.Fatal("not persisted")
	}
	for _, tc := range []struct {
		method, path, body, origin string
		status                     int
	}{{"POST", "/api/canvas/commands", strings.Repeat(" ", 16<<20) + "{}", "http://tauri.localhost", 413}, {"POST", "/api/canvas/commands", string(raw), "", 403}, {"POST", "/api/canvas/nodes", "{}", "http://tauri.localhost", 405}, {"POST", "/api/canvas/content", `{"compositionIds":[],"offset":0}`, "http://tauri.localhost", 400}, {"POST", "/api/canvas/content", `{"compositionIds":["x","x"]}`, "http://tauri.localhost", 400}, {"POST", "/api/canvas/content", `{"compositionIds":["missing"]}`, "http://tauri.localhost", 200}, {"POST", "/api/canvas/commands", `{"requestId":"x","expectedRevision":0,"command":{"type":"canvas.patch","nodes":[],"edges":[]}}`, "http://tauri.localhost", 409}, {"POST", "/api/canvas/commands", `{"requestId":"x","requestId":"y"}`, "http://tauri.localhost", 400}} {
		w := call(tc.method, tc.path, tc.body, tc.origin)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	w = call("GET", "/api/canvas/nodes", "", "http://tauri.localhost")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("document unavailable")
	}
}

func TestCanvasHTTPRejectsNonEnumWithoutReceipt(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "enum.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "Canvas", "p")
	if e != nil {
		t.Fatal(e)
	}
	h := newTestServer(t, "http://tauri.localhost", db, t.TempDir())
	for _, value := range []string{"red orange", " red", "red ", "", "re"} {
		body := fmt.Sprintf(`{"requestId":"invalid","expectedRevision":0,"command":{"type":"canvas.patch","nodes":[{"id":"n","before":null,"after":{"id":"n","kind":"frame","parentId":null,"position":{"x":0,"y":0},"size":{"width":350,"height":250},"zIndex":0,"hidden":false,"title":"Frame","collapsed":false,"backgroundColor":%q}}],"edges":[]}}`, value)
		r := httptest.NewRequest("POST", "/api/canvas/commands?project_id="+p.ID, strings.NewReader(body))
		r.Header.Set("Origin", "http://tauri.localhost")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "CANVAS_V3_INVALID_COMMAND") {
			t.Fatalf("%q: %d %s", value, w.Code, w.Body.String())
		}
		d, e := (store.Canvas{DB: db}).Document(context.Background(), p.ID)
		if e != nil || d.Revision != 0 || len(d.Nodes) != 0 {
			t.Fatal("rejection mutated document", d, e)
		}
		var count int
		if e = db.QueryRow("SELECT count(*) FROM canvas_commands WHERE project_id=?", p.ID).Scan(&count); e != nil || count != 0 {
			t.Fatal("rejection created receipt", count, e)
		}
	}
}
