package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

func TestDeliveryHTTP(t *testing.T) {
	root := t.TempDir()
	db, e := store.Open(filepath.Join(t.TempDir(), "delivery.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "project", "Test")
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, "projects", p.ID, "files")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0600)
	h := newTestServer(t, "http://tauri.localhost", db, root)
	body := `{"requestId":"deliver","sources":[{"key":"one","path":"projects/` + p.ID + `/files/file.txt"}],"output":{"resources":[{"key":"one","mode":"create","name":"title"}],"composition":{"name":"Movie","type":{"label":"video"},"placements":[{"key":"one","role":"main"}]}}}`
	call := func(method, url, body, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, url, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		if method == "OPTIONS" {
			r.Header.Set("Access-Control-Request-Method", "POST")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	url := "/api/deliver?project_id=" + p.ID
	for _, tc := range []struct {
		method, url, body, origin string
		code                      int
	}{{"POST", url, body, "https://evil.example", 403}, {"GET", url, "", "http://tauri.localhost", 405}, {"OPTIONS", url, "", "http://tauri.localhost", 204}, {"POST", url + "&project_id=" + p.ID, body, "http://tauri.localhost", 400}, {"POST", url, strings.Replace(body, `"role":"main"`, `"role":"main","unknown":true`, 1), "http://tauri.localhost", 400}, {"POST", url, strings.Replace(body, `"requestId":"deliver"`, `"requestId":"deliver","requestId":"other"`, 1), "http://tauri.localhost", 400}, {"POST", url, strings.Replace(body, `"requestId":"deliver"`, `"requestId":"`+strings.Repeat("x", 1<<20)+`"`, 1), "http://tauri.localhost", 413}} {
		w := call(tc.method, tc.url, tc.body, tc.origin)
		if w.Code != tc.code {
			t.Fatalf("boundary %d %s", w.Code, w.Body.String())
		}
	}
	for _, replacement := range []string{"", `,"placements":null`, `,"placements":[]`, `,"placements":[{"key":"one","role":"main"},{"key":"one","role":"other"}]`} {
		bad := strings.Replace(body, `,"placements":[{"key":"one","role":"main"}]`, replacement, 1)
		w := call("POST", url, bad, "http://tauri.localhost")
		if w.Code != 400 {
			t.Fatalf("placements boundary: %d %s", w.Code, w.Body.String())
		}
	}
	first := call("POST", url, body, "http://tauri.localhost")
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	var out map[string]json.RawMessage
	json.Unmarshal(first.Body.Bytes(), &out)
	if len(out) != 1 || !strings.Contains(string(out["delivery"]), `"status":"completed"`) {
		t.Fatal(first.Body.String())
	}
	again := call("POST", url, body, "http://tauri.localhost")
	if again.Body.String() != first.Body.String() {
		t.Fatal("changed receipt")
	}
	if w := call("POST", url, strings.Replace(body, `"name":"title"`, `"name":"other"`, 1), "http://tauri.localhost"); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	var n int
	db.QueryRow("SELECT count(*) FROM versions WHERE origin='user'").Scan(&n)
	if n != 1 {
		t.Fatal("manual origin")
	}
}
