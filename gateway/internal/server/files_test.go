package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, origin string, db *sql.DB, root string) *Server {
	t.Helper()
	s, e := New(origin, db, root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFilesHTTP(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "http.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	h := newTestServer(t, "http://tauri.localhost", db, t.TempDir())
	p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "test", "project")
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, route string, body io.Reader, ctype, rangeValue string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, route, body)
		r.Header.Set("Origin", "http://tauri.localhost")
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		if rangeValue != "" {
			r.Header.Set("Range", rangeValue)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	post := func(route string, v any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(v)
		return call("POST", route+"?project_id="+p.ID, bytes.NewReader(b), "application/json", "")
	}
	var multipartBody bytes.Buffer
	mw := multipart.NewWriter(&multipartBody)
	part, _ := mw.CreateFormFile("file", "hello.txt")
	io.WriteString(part, "hello world")
	mw.WriteField("requestId", "upload")
	mw.Close()
	w := call("POST", "/api/upload?project_id="+p.ID, &multipartBody, mw.FormDataContentType(), "")
	if w.Code != 200 {
		t.Fatalf("upload %d %s", w.Code, w.Body)
	}
	var staged struct {
		Path string `json:"path"`
	}
	json.Unmarshal(w.Body.Bytes(), &staged)
	w = post("/api/upload/commit", map[string]any{"requestId": "commit", "path": staged.Path})
	if w.Code != 200 {
		t.Fatalf("commit %d %s", w.Code, w.Body)
	}
	var committed struct {
		Path string `json:"relative"`
	}
	json.Unmarshal(w.Body.Bytes(), &committed)
	route := "/api/files/content?project_id=" + p.ID + "&path=" + url.QueryEscape(committed.Path)
	w = call("GET", route, nil, "", "bytes=1-4")
	if w.Code != 206 || w.Body.String() != "ello" || w.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatalf("range %d %s", w.Code, w.Body)
	}
	w = call("GET", route, nil, "", "bytes=999-1000")
	if w.Code != 416 || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("invalid range %d %s", w.Code, w.Body)
	}
	w = post("/api/files/content", map[string]any{"requestId": "empty", "path": committed.Path, "content": ""})
	if w.Code != 200 {
		t.Fatalf("empty content %d %s", w.Code, w.Body)
	}
	for _, body := range []any{map[string]any{"requestId": "wrong", "path": committed.Path, "new_name": "a.txt", "content": ""}, map[string]any{"requestId": "wrong", "path": committed.Path, "newName": "a.txt"}} {
		w = post("/api/files/rename", body)
		if w.Code != 400 {
			t.Fatalf("closed DTO %d %s", w.Code, w.Body)
		}
	}
	w = post("/api/files/content", map[string]any{"requestId": "missingcontent", "path": committed.Path})
	if w.Code != 400 {
		t.Fatal("missing content accepted")
	}
	w = call("GET", "/api/files/content?project_id="+p.ID+"&path="+url.QueryEscape(staged.Path), nil, "", "")
	if w.Code != 400 {
		t.Fatalf("private read %d %s", w.Code, w.Body)
	}
	w = call("GET", "/api/files/list?project_id="+p.ID+"&offset=0&limit=1", nil, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"coverage"`) {
		t.Fatalf("list %d %s", w.Code, w.Body)
	}
	w = call("GET", "/api/files/list?project_id="+p.ID+"&limit=1&limit=2", nil, "", "")
	if w.Code != 400 {
		t.Fatal("duplicate query accepted")
	}
	r := httptest.NewRequest("POST", "/api/files/delete?project_id="+p.ID, strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("origin accepted")
	}
	r = httptest.NewRequest("OPTIONS", route, nil)
	r.Header.Set("Origin", "http://tauri.localhost")
	r.Header.Set("Access-Control-Request-Method", "GET")
	r.Header.Set("Access-Control-Request-Headers", "Range")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("range preflight", w.Code)
	}
}

func TestFilesHTTPActiveContentAndAssetRegistration(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "files.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	h := newTestServer(t, "http://tauri.localhost", db, t.TempDir())
	p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "p", "p")
	if e != nil {
		t.Fatal(e)
	}
	s := workspace.NewService(db, h.workspace)
	v, e := s.Stage(context.Background(), p.ID, "upload", "active.svg", strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`))
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Commit(context.Background(), p.ID, "commit", v)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "/api/files/content?project_id="+p.ID+"&path="+url.QueryEscape(a.Path), nil)
	r.Header.Set("Origin", "http://tauri.localhost")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("active content %d %+v", w.Code, w.Header())
	}
	b, _ := json.Marshal(map[string]any{"requestId": "register", "action": "create", "value": map[string]string{"path": a.Path}})
	r = httptest.NewRequest("POST", "/api/assets?project_id="+p.ID, bytes.NewReader(b))
	r.Header.Set("Origin", "http://tauri.localhost")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got store.Asset
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.ID != a.ID || got.FullHash == nil {
		t.Fatalf("register %d %s", w.Code, w.Body)
	}
	for _, raw := range []string{`{"requestId":"n","path":"x","content":"","unique":null}`, `{"requestId":"n","path":"x","content":"","Unique":true}`} {
		r = httptest.NewRequest("POST", "/api/files/content?project_id="+p.ID, strings.NewReader(raw))
		r.Header.Set("Origin", "http://tauri.localhost")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("optional input %d %s", w.Code, w.Body)
		}
	}
}
