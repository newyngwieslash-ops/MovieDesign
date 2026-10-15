package server

import (
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type requestFixture struct {
	RequestID string `json:"requestId" validate:"required"`
	Nested    struct {
		Prompt string `json:"prompt"`
	} `json:"nested"`
}

func TestRequestDecode(t *testing.T) {
	for _, body := range []string{`{}`, `{"requestId":"r","unknown":1}`, `{"requestId":"r"} {}`, `{"requestId":"a","requestId":"b"}`, `{"requestId":"r","nested":{"prompt":"a","prompt":"b"}}`, `{"requestId":"r","nested":{"extra":1}}`, `[]`, `null`, `{`, `{"requestId":"` + strings.Repeat("a", 1<<20) + `"}`} {
		t.Run(body[:min(len(body), 50)], func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			w := httptest.NewRecorder()
			if _, err := DecodeJSON[requestFixture](w, r); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"requestId":"r","nested":{"prompt":"  中文\n"}}`))
	v, err := DecodeJSON[requestFixture](httptest.NewRecorder(), r)
	if err != nil || v.Nested.Prompt != "  中文\n" {
		t.Fatalf("prompt changed: %+v %v", v, err)
	}
	if err := Validate(requestFixture{}); err == nil {
		t.Fatal("missing requestId accepted")
	}
	for _, id := range []string{"", "../p", "a/b", " a", "a?x", strings.Repeat("a", 129)} {
		if err := (Scope{ProjectID: id, Operation: "objects.create", RequestID: "r"}).Validate(); err == nil {
			t.Fatalf("invalid project %q", id)
		}
	}
	if err := (Scope{ProjectID: "project-1", Operation: "objects.create", RequestID: "r"}).Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestCanonicalPreservesPrompt(t *testing.T) {
	a, err := CanonicalJSON([]byte(`{"prompt":"  中文\n","references":["a","b"],"n":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalJSON([]byte(`{"n":9007199254740993,"references":["a","b"],"prompt":"  中文\n"}`))
	if err != nil || string(a) != string(b) {
		t.Fatalf("order changed identity: %s %s %v", a, b, err)
	}
	c, err := CanonicalJSON([]byte(`{"prompt":"中文","references":["a","b"],"n":9007199254740993}`))
	if err != nil || string(a) == string(c) {
		t.Fatal("prompt normalized")
	}
	if !strings.Contains(string(a), "9007199254740993") {
		t.Fatal("number rounded")
	}
	for _, raw := range []string{`{"a":1,"\u0061":2}`, `{"x":[{"a":1,"a":2}]}`, `{} {}`, `null`, `[]`} {
		if _, err := CanonicalJSON([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestRequestPreflight(t *testing.T) {
	for _, tc := range []struct {
		method, headers string
		want            int
	}{
		{"GET", "Content-Type, Last-Event-ID", 204},
		{"POST", "Content-Type", 403},
		{"GET", "Authorization", 403},
	} {
		r := httptest.NewRequest("OPTIONS", "/api/health", nil)
		r.Header.Set("Origin", "http://tauri.localhost")
		r.Header.Set("Access-Control-Request-Method", tc.method)
		r.Header.Set("Access-Control-Request-Headers", tc.headers)
		w := httptest.NewRecorder()
		New("http://tauri.localhost", nil, t.TempDir()).ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s %s: %d", tc.method, tc.headers, w.Code)
		}
	}
}

func TestRequestRejectedRoutesDoNotWrite(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "boundary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := New("http://tauri.localhost", db, t.TempDir())
	for _, tc := range []struct {
		method, path, origin, preflight string
		status                          int
	}{
		{"POST", "/api/projects?project_id=p", "https://evil.example", "", 403},
		{"OPTIONS", "/api/projects?project_id=p", "http://tauri.localhost", "POST", 403},
		{"POST", "/api/projects?project_id=../p", "http://tauri.localhost", "", 404},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"requestId":"r"}`))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Access-Control-Request-Method", tc.preflight)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	for _, table := range []string{"projects", "write_requests", "file_operations"} {
		var n int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("unexpected write %s: %d %v", table, n, err)
		}
	}
}

func TestRequestRejectsFieldAliases(t *testing.T) {
	for _, body := range []string{
		`{"requestId":"a","RequestID":"b"}`,
		`{"RequestID":"b"}`,
		`{"requestId":"r","nested":{"Prompt":"x"}}`,
		`{"requestId":"r","nested":{"prompt":"a","PROMPT":"b"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			if _, err := DecodeJSON[requestFixture](httptest.NewRecorder(), r); err == nil {
				t.Fatal("case alias accepted")
			}
		})
	}
}

func TestCanonicalSurrogateEscapes(t *testing.T) {
	for _, raw := range []string{`{"prompt":"\ud800"}`, `{"prompt":"\udfff"}`, `{"prompt":"\ud800x"}`, `{"prompt":"\ud800\u0041"}`, `{"prompt":"\ud800\ud800"}`, `{"\udfff":"x"}`} {
		t.Run(raw, func(t *testing.T) {
			if _, err := CanonicalJSON([]byte(raw)); err == nil {
				t.Fatal("unpaired surrogate accepted")
			}
		})
	}
	for _, raw := range []string{`{"prompt":"\ud83d\ude00"}`, `{"prompt":"\\ud800"}`, `{"prompt":"\\\\udfff"}`, `{"prompt":"�"}`} {
		if _, err := CanonicalJSON([]byte(raw)); err != nil {
			t.Fatalf("valid input rejected %s: %v", raw, err)
		}
	}
	a, err := CanonicalJSON([]byte(`{"prompt":"\ud83d\ude00"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalJSON([]byte(`{"prompt":"😀"}`))
	if err != nil || string(a) != string(b) {
		t.Fatalf("pair changed: %s %s %v", a, b, err)
	}
}

func TestRequestExactNestedDTOsAndRawMessage(t *testing.T) {
	type child struct {
		Prompt string `json:"prompt"`
	}
	type mutation struct {
		Children []*child        `json:"children"`
		Deferred json.RawMessage `json:"deferred"`
	}
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"children":[{"prompt":"  中文"}],"deferred":{"LATER":"own schema"}}`, true},
		{`{"children":[{"Prompt":"x"}]}`, false},
		{`{"children":[{"prompt":"x","PROMPT":"y"}]}`, false},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		value, err := DecodeJSON[mutation](httptest.NewRecorder(), r)
		if (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.body, err)
		}
		if tc.valid && value.Children[0].Prompt != "  中文" {
			t.Fatal("nested prompt changed")
		}
	}
}
