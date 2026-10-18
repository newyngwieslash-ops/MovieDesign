package server

import (
	"net/http/httptest"
	"testing"
)

func TestMCPRequiresDedicatedToken(t *testing.T) {
	h := newTestServer(t, "http://tauri.localhost", nil, t.TempDir())
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Origin", "http://tauri.localhost")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("MCP must reject absent dedicated token before protocol: got %d", w.Code)
	}
}
