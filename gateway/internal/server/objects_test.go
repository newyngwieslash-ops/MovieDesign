package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

func TestObjectsHTTPReplayAndBoundary(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "http.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := New("http://tauri.localhost", db, t.TempDir())
	call := func(method, path, body, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	post := func(path, body string, status int) string {
		t.Helper()
		w := call("POST", path, body, "http://tauri.localhost")
		if w.Code != status {
			t.Fatalf("POST %s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	body := `{"requestId":"bootstrap","action":"create","value":{"name":"电影项目"}}`
	first := post("/api/projects", body, 200)
	if again := post("/api/projects", body, 200); first != again {
		t.Fatal("bootstrap replay changed receipt")
	}
	var p store.Project
	if err = json.Unmarshal([]byte(first), &p); err != nil || p.ID == "" {
		t.Fatalf("project: %s", first)
	}
	post("/api/projects", `{"requestId":"bootstrap","action":"create","value":{"name":"different"}}`, 409)
	post("/api/projects", `{"requestId":"bootstrap","action":"create","id":"","value":{"name":"电影项目"}}`, 400)
	path := "/api/compositions?project_id=" + p.ID
	for _, tc := range []struct {
		path, body, origin string
		status             int
	}{
		{path, `{"action":"create","value":{}}`, "http://tauri.localhost", 400},
		{path, `{"requestId":"x","action":"create","value":{"Name":"x"}}`, "http://tauri.localhost", 400},
		{path, `{"requestId":"x","action":"create","value":{"name":"x","extra":true}}`, "http://tauri.localhost", 400},
		{path, `{"requestId":"x","action":"create","value":{"name":"x","name":"y"}}`, "http://tauri.localhost", 400},
		{path, `{"requestId":"x","action":"create","value":{"name":"\ud800"}}`, "http://tauri.localhost", 400},
		{path, `{"requestId":"x","action":"create","value":{}}`, "https://evil.example", 403},
		{"/api/compositions", `{"requestId":"x","action":"create","value":{}}`, "http://tauri.localhost", 400},
		{"/api/compositions?project_id=../x", `{}`, "http://tauri.localhost", 400},
		{path, `{"requestId":"x","action":"create","value":{"name":"` + strings.Repeat("x", 1<<20) + `"}}`, "http://tauri.localhost", 413},
	} {
		w := call("POST", tc.path, tc.body, tc.origin)
		if w.Code != tc.status {
			t.Fatalf("boundary: %d %s", w.Code, w.Body.String())
		}
		var e struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		json.Unmarshal(w.Body.Bytes(), &e)
		if e.Error.Code != "invalid_input" {
			t.Fatal("unstructured boundary error")
		}
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM write_requests`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rejected request wrote receipt: %d %v", n, err)
	}
	typeRaw := post("/api/composition-types?project_id="+p.ID, `{"requestId":"type","action":"create","value":{"label":"视频"}}`, 200)
	var typ store.CompositionType
	json.Unmarshal([]byte(typeRaw), &typ)
	objectRaw := post(path, `{"requestId":"object","action":"create","value":{"name":"片头","type_id":"`+typ.ID+`"}}`, 200)
	var c store.Composition
	json.Unmarshal([]byte(objectRaw), &c)
	for _, route := range []string{"/api/projects", "/api/composition-types?project_id=" + p.ID, path, "/api/resources?project_id=" + p.ID, "/api/versions?project_id=" + p.ID, "/api/assets?project_id=" + p.ID} {
		w := call("GET", route, "", "http://tauri.localhost")
		if w.Code != 200 {
			t.Fatalf("list: %s %d %s", route, w.Code, w.Body.String())
		}
		var page store.Page
		if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.Items == nil {
			t.Fatalf("page: %s", w.Body.String())
		}
	}
	w := call("GET", path+"&id="+c.ID, "", "http://tauri.localhost")
	if w.Code != 200 || w.Body.String() != objectRaw {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	for _, query := range []string{"&offset=-1", "&limit=201", "&limit=0", "&limit=no", "&project_id=other", "&unknown=x", "&ignored=%ZZ", "&project_id=other;ignored=x", "&id=" + c.ID + "&offset=1"} {
		w := call("GET", path+query, "", "http://tauri.localhost")
		if w.Code != 400 {
			t.Fatalf("query %s: %d", query, w.Code)
		}
	}
	for _, route := range []string{"/api/projects", "/api/composition-types", "/api/compositions", "/api/resources", "/api/versions", "/api/assets"} {
		r := httptest.NewRequest("OPTIONS", route, nil)
		r.Header.Set("Origin", "http://tauri.localhost")
		r.Header.Set("Access-Control-Request-Method", "POST")
		r.Header.Set("Access-Control-Request-Headers", "Content-Type")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatalf("preflight %s: %d", route, w.Code)
		}
		w = call(http.MethodPatch, route, "", "http://tauri.localhost")
		if w.Code != 405 {
			t.Fatalf("method %s: %d", route, w.Code)
		}
	}
}
