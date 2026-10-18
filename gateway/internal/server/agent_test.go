package server

import (
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestLLMSettingsAreTypedWriteOnlyRecoverableAndOriginGuarded(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secrets, err := providers.OpenSecrets(filepath.Join(dir, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	h := newHandler("http://tauri.localhost", db, nil, providers.NewService(db, secrets))
	call := func(method, path, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	raw := `{"requestId":"llm-1","action":"save","config":{"protocol":"openai-compatible","base_url":"http://127.0.0.1:11434/v1","model_id":"test","context":32000,"output":4096},"key":"synthetic-LLM-secret"}`
	for _, origin := range []string{"", "null", "https://evil.example"} {
		if w := call("POST", "/api/agent/settings", raw, origin); w.Code != 403 {
			t.Fatal("Origin bypass", w.Code)
		}
	}
	saved := call("POST", "/api/agent/settings", raw, "http://tauri.localhost")
	if saved.Code != 200 {
		t.Fatal("LLM save missing", saved.Code, saved.Body.String())
	}
	for _, p := range []string{"/api/agent/settings", "/api/agent/settings/receipt?request_id=llm-1", "/api/agent/status"} {
		w := call("GET", p, "", "http://tauri.localhost")
		if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-LLM-secret") {
			t.Fatal("write-only read", p, w.Code, w.Body.String())
		}
	}
	if w := call("POST", "/api/agent/settings", raw, "http://tauri.localhost"); w.Body.String() != saved.Body.String() {
		t.Fatal("replay changed")
	}
	if w := call("POST", "/api/agent/settings", strings.Replace(raw, "synthetic-LLM-secret", "changed", 1), "http://tauri.localhost"); w.Code != 409 {
		t.Fatal("key conflict not rejected", w.Code)
	}
	if w := call("POST", "/api/agent/settings", strings.Replace(raw, `"protocol":"openai-compatible"`, `"protocol":"openai-compatible","npm":"evil"`, 1), "http://tauri.localhost"); w.Code != 400 {
		t.Fatal("unapproved package accepted", w.Code)
	}
	if w := call("GET", "/api/providers", "", "http://tauri.localhost"); strings.Contains(w.Body.String(), "test") {
		t.Fatal("LLM leaked into media providers")
	}
	var journal string
	if err := db.QueryRow(`SELECT input_json||response_json FROM write_requests WHERE operation='llm_settings'`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(journal, "synthetic-LLM-secret") {
		t.Fatal("key in journal")
	}
}
