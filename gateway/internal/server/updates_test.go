package server

import (
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdatePrivateReceiptsAndAdmission(t *testing.T) {
	t.Setenv("MOVIEDESIGN_UPDATE_TOKEN", "test-native-token")
	db, err := store.Open(filepath.Join(t.TempDir(), "updates.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := newTestServer(t, "http://tauri.localhost", db, t.TempDir())
	if os.Getenv("MOVIEDESIGN_UPDATE_TOKEN") != "" {
		t.Fatal("private token remains inheritable by managed children")
	}
	policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"schema_version":1,"enabled":true,"min_supported_version":"0.1.0"}`))
	}))
	defer policy.Close()
	s.Updates().PolicyURL = policy.URL
	call := func(path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Origin", "http://tauri.localhost")
		r.Header.Set("X-MovieDesign-Update", token)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code >= 400 {
			var response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(w.Body.Bytes(), &response) != nil {
				t.Fatal("invalid structured update error", w.Body)
			}
			switch response.Error.Code {
			case "invalid_input", "not_found", "waiting_user", "capacity", "upstream":
			default:
				t.Fatal("unexpected update error code", response.Error.Code)
			}
		}
		return w
	}
	decision := `{"requestId":"decision-1","candidateId":"candidate-1","version":"2.0.0","currentVersion":"0.1.0","decision":"approve"}`
	if w := call("/internal/updates/decision", decision, ""); w.Code != 403 {
		t.Fatalf("private control unauthenticated: %d", w.Code)
	}
	if w := call("/internal/updates/decision", decision, "test-native-token"); w.Code != 200 {
		t.Fatalf("decision: %d %s", w.Code, w.Body)
	}
	if w := call("/internal/updates/decision", decision, "test-native-token"); w.Code != 200 {
		t.Fatalf("lost decision response could not recover same original receipt: %d %s", w.Code, w.Body)
	}
	if w := call("/internal/updates/decision", strings.Replace(decision, "approve", "reject", 1), "test-native-token"); w.Code != 409 {
		t.Fatalf("same key different decision: %d", w.Code)
	}
	admit := `{"requestId":"install-1","candidateId":"candidate-1","receiptId":"decision-1","version":"2.0.0","currentVersion":"0.1.0"}`
	s.active.Add(1)
	if w := call("/internal/updates/admit", admit, "test-native-token"); w.Code != 409 {
		t.Fatalf("busy admitted: %d", w.Code)
	}
	s.active.Add(-1)
	if _, e := db.Exec(`INSERT INTO write_requests(project_id,operation,request_id,input_json,state) VALUES('_app','other-work','pending','{}','pending')`); e != nil {
		t.Fatal(e)
	}
	if w := call("/internal/updates/admit", admit, "test-native-token"); w.Code != 409 {
		t.Fatalf("pending background work admitted: %d %s", w.Code, w.Body)
	}
	if _, e := db.Exec(`DELETE FROM write_requests WHERE operation='other-work'`); e != nil {
		t.Fatal(e)
	}
	if w := call("/internal/updates/admit", admit, "test-native-token"); w.Code != 200 {
		t.Fatalf("admit: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/api/projects", "/mcp"} {
		if w := call(path, `{}`, ""); w.Code != 409 {
			t.Fatalf("mutation during reservation %s: %d", path, w.Code)
		}
	}
	if w := call("/internal/updates/admit", admit, "test-native-token"); w.Code != 409 {
		t.Fatalf("repeat installation permitted: %d", w.Code)
	}
	if w := call("/internal/updates/receipt", `{"requestId":"query-1","receiptId":"install-1"}`, "test-native-token"); w.Code != 200 {
		t.Fatalf("receipt: %d", w.Code)
	} else {
		var got map[string]any
		json.Unmarshal(w.Body.Bytes(), &got)
		if got["status"] != "install_started" {
			t.Fatal(got)
		}
	}
	if w := call("/internal/updates/release", `{"requestId":"release-1","receiptId":"install-1"}`, "test-native-token"); w.Code != 200 {
		t.Fatalf("release: %d %s", w.Code, w.Body)
	}
	if w := call("/api/projects", `{}`, ""); w.Code == 409 {
		t.Fatal("reservation leaked")
	}
	// A lost install result remains an original intent; release never permits replay.
	if w := call("/internal/updates/admit", admit, "test-native-token"); w.Code != 409 {
		t.Fatalf("unknown install replayed: %d", w.Code)
	}
	if w := call("/internal/updates/history", `{"requestId":"history"}`, "test-native-token"); w.Code != 200 || !strings.Contains(w.Body.String(), `"install_started"`) {
		t.Fatal("durable app ledger unavailable", w.Code, w.Body)
	}
	// A new server process still recovers the original intent without localStorage
	// or a native frozen candidate. It cannot replay the consumed approval.
	t.Setenv("MOVIEDESIGN_UPDATE_TOKEN", "new-native-launch")
	restarted := newTestServer(t, "http://tauri.localhost", db, t.TempDir())
	r := httptest.NewRequest("POST", "/internal/updates/receipt", strings.NewReader(`{"requestId":"recovery","receiptId":"install-1"}`))
	r.Header.Set("Origin", "http://tauri.localhost")
	r.Header.Set("X-MovieDesign-Update", "new-native-launch")
	w := httptest.NewRecorder()
	restarted.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"install_started"`) {
		t.Fatal("restart lost original intent", w.Code, w.Body)
	}
}

func TestUpdateAdmissionReservesBeforePolicyAndFailsClosed(t *testing.T) {
	t.Setenv("MOVIEDESIGN_UPDATE_TOKEN", "native-only")
	db, err := store.Open(filepath.Join(t.TempDir(), "updates.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := newTestServer(t, "http://tauri.localhost", db, t.TempDir())
	entered, proceed := make(chan struct{}), make(chan struct{})
	policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-proceed
		w.Write([]byte(`{"schema_version":1,"enabled":true,"min_supported_version":"2.0.0","manual_download_url":"https://github.com/newyngwieslash-ops/MovieDesign/releases/latest","manual_only":{"platforms":["win32"],"max_version_exclusive":"2.0.0"}}`))
	}))
	defer policy.Close()
	defer func() {
		select {
		case <-proceed:
		default:
			close(proceed)
		}
	}()
	s.Updates().PolicyURL = policy.URL
	call := func(method, path, body, origin, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("X-MovieDesign-Update", token)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	decision := `{"requestId":"d","candidateId":"c","version":"2.0.0","currentVersion":"0.1.0","decision":"approve"}`
	if w := call("POST", "/internal/updates/decision", decision, "https://evil.example", "native-only"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "/internal/updates/decision", decision, "http://tauri.localhost", "native-only"); w.Code != 200 {
		t.Fatal(w.Body)
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- call("POST", "/internal/updates/admit", `{"requestId":"i","candidateId":"c","receiptId":"d","version":"2.0.0","currentVersion":"0.1.0"}`, "http://tauri.localhost", "native-only")
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("admission deadlocked before policy")
	}
	for _, path := range []string{"/api/projects", "/mcp", "/api/chat"} {
		if w := call("POST", path, `{}`, "http://tauri.localhost", ""); w.Code != 409 {
			t.Fatalf("reservation race %s: %d", path, w.Code)
		}
	}
	if w := call("GET", "/api/health", "", "http://tauri.localhost", ""); w.Code != 200 {
		t.Fatal("health blocked", w.Code)
	}
	if w := call("POST", "/internal/updates/receipt", `{"requestId":"q","receiptId":"i","decisionReceiptId":"d"}`, "http://tauri.localhost", "native-only"); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"approve"`) {
		t.Fatal("original decision query blocked", w.Code, w.Body)
	}
	close(proceed)
	select {
	case w := <-result:
		if w.Code != 409 {
			t.Fatal("manual_only admitted", w.Code, w.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("admission deadlocked after policy")
	}
	s.mu.Lock()
	reserved := s.updateReservation
	s.mu.Unlock()
	if reserved != "" {
		t.Fatal("failed policy leaked reservation")
	}
	var installs int
	if err := db.QueryRow(`SELECT count(*) FROM write_requests WHERE operation='updates.install'`).Scan(&installs); err != nil || installs != 0 {
		t.Fatal("blocked update wrote install intent", installs, err)
	}
}
