package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestOriginAndHealthContract(t *testing.T) {
	for _, tc := range []struct {
		name, origin, method, path string
		status                     int
	}{
		{"ready", "http://tauri.localhost", "GET", "/api/health", 200},
		{"missing", "", "GET", "/api/health", 403},
		{"null", "null", "GET", "/api/health", 403},
		{"foreign", "https://evil.example", "POST", "/api/upload", 403},
		{"suffix", "http://tauri.localhost.evil.example", "GET", "/api/health", 403},
		{"development disabled in production", "http://localhost:1420", "GET", "/api/health", 403},
		{"missing route", "http://tauri.localhost", "GET", "/api/nope", 404},
		{"method", "http://tauri.localhost", "POST", "/api/health", 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			newTestServer(t, "http://tauri.localhost", nil, t.TempDir()).ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if tc.status == 200 {
				if len(body) != 3 || body["status"] != "ok" || body["idle"] != true || body["safe_to_suspend"] != true {
					t.Fatalf("bad health: %v", body)
				}
			} else if _, ok := body["error"]; !ok {
				t.Fatalf("missing error: %v", body)
			}
			if tc.status == 403 && w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("rejected origin received CORS permission")
			}
		})
	}
	for _, origin := range []string{"http://tauri.localhost", "https://evil.example", ""} {
		r := httptest.NewRequest("OPTIONS", "/api/health", nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", "GET")
		w := httptest.NewRecorder()
		newTestServer(t, "http://tauri.localhost", nil, t.TempDir()).ServeHTTP(w, r)
		want := 403
		if origin == "http://tauri.localhost" {
			want = 204
		}
		if w.Code != want {
			t.Fatalf("preflight %q: %d", origin, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/api/health", nil)
	r.Header.Add("Origin", "http://tauri.localhost")
	r.Header.Add("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	newTestServer(t, "http://tauri.localhost", nil, t.TempDir()).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("duplicate Origin accepted")
	}
}
