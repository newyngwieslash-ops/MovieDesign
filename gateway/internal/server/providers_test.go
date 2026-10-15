package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvidersHTTPBoundaryAndRedaction(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secret, err := providers.OpenSecrets(filepath.Join(dir, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	service := providers.NewService(db, secret)
	handler := newHandler("http://tauri.localhost", db, nil, service)
	call := func(method, path, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	cfg := providers.Config{ID: "http", Name: "HTTP provider", Kind: "image", Protocol: "openai-images", ModelID: "custom", BaseURL: "http://127.0.0.1:11434/v1", Enabled: true, Capability: providers.Capability{MaxPromptBytes: 65536, MaxCount: 1}}
	raw, _ := json.Marshal(map[string]any{"requestId": "r1", "action": "create", "config": cfg, "key": "synthetic-http-secret"})
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)
	if w := call("POST", "/api/providers", string(raw), ""); w.Code != 403 {
		t.Fatal("origin", w.Code)
	}
	if w := call("POST", "/api/providers?project_id=p", string(raw), "http://tauri.localhost"); w.Code != 400 {
		t.Fatal("project scope accepted")
	}
	saved := call("POST", "/api/providers", string(raw), "http://tauri.localhost")
	if saved.Code != 200 {
		t.Fatal(saved.Code, saved.Body.String())
	}
	if w := call("POST", "/api/providers", string(raw), "http://tauri.localhost"); w.Body.String() != saved.Body.String() {
		t.Fatal("original receipt changed")
	}
	if w := call("POST", "/api/providers", strings.Replace(string(raw), "synthetic-http-secret", "different", 1), "http://tauri.localhost"); w.Code != 409 {
		t.Fatal("conflicting key replay", w.Code)
	}
	for _, body := range []string{`{"requestId":"r2","action":"disable","id":"http","key":null}`, `{"requestId":"r2","action":"disable","id":"http","Config":{}}`, `{"requestId":"r2","requestId":"r3","action":"disable","id":"http"}`} {
		if w := call("POST", "/api/providers", body, "http://tauri.localhost"); w.Code != 400 {
			t.Fatal("schema accepted", w.Code)
		}
	}
	result := call("GET", "/api/providers", "", "http://tauri.localhost")
	if result.Code != 200 || result.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("GET contract")
	}
	if strings.Contains(saved.Body.String()+result.Body.String()+logs.String(), "synthetic-http-secret") {
		t.Fatal("secret disclosed")
	}
	var journal string
	rows, err := db.QueryContext(context.Background(), `SELECT input_json||response_json FROM write_requests`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		if err = rows.Scan(&journal); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(journal, "synthetic-http-secret") {
			t.Fatal("key journaled")
		}
	}

}
