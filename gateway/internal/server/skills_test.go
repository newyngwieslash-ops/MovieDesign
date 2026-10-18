package server

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

func TestSkillsHTTPListContentAndStrictScope(t *testing.T) {
	root := t.TempDir()
	if e := os.Mkdir(filepath.Join(root, "workspace"), 0700); e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s, e := New("http://tauri.localhost", db, filepath.Join(root, "workspace"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, tc := range []struct {
		method, path, origin string
		status               int
	}{
		{"GET", "/api/skills", "http://tauri.localhost", 200},
		{"GET", "/api/skills?name=" + url.QueryEscape("口播视频"), "http://tauri.localhost", 200},
		{"GET", "/api/skills?name=unknown", "http://tauri.localhost", 404},
		{"GET", "/api/skills?name=..%2Fsecret", "http://tauri.localhost", 404},
		{"GET", "/api/skills?name=", "http://tauri.localhost", 400},
		{"GET", "/api/skills?name=a&name=b", "http://tauri.localhost", 400},
		{"GET", "/api/skills?path=secret", "http://tauri.localhost", 400},
		{"GET", "/api/skills?offset=-1", "http://tauri.localhost", 400},
		{"GET", "/api/skills?limit=0", "http://tauri.localhost", 400},
		{"GET", "/api/skills?name=a&limit=1", "http://tauri.localhost", 400},
		{"GET", "/api/skills?offset=1&limit=1", "http://tauri.localhost", 200},
		{"GET", "/api/skills", "http://evil", 403},
		{"POST", "/api/skills", "http://tauri.localhost", 405},
		{"OPTIONS", "/api/skills", "http://tauri.localhost", 204},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("Origin", tc.origin)
		if tc.method == "OPTIONS" {
			r.Header.Set("Access-Control-Request-Method", "GET")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s = %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if tc.status == 200 && !strings.Contains(w.Body.String(), "trigger-words") {
			t.Fatal("skill metadata missing")
		}
	}
}
