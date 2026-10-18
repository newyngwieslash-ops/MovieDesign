package agent

import (
	"context"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHostNeverAttachesWithoutItsPasswordAndValidatesEffectivePermissions(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pw, ok := r.BasicAuth()
		if !ok || user != "opencode" || pw != "private-launch-password" {
			w.WriteHeader(401)
			return
		}
		calls++
		if r.URL.Path == "/global/health" {
			_, _ = w.Write([]byte(`{"healthy":true,"version":"1.19.0"}`))
			return
		}
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"name": "build", "permission": []any{map[string]string{"permission": "*", "pattern": "*", "action": "deny"}, map[string]string{"permission": "question", "pattern": "*", "action": "allow"}}}})
	}))
	defer upstream.Close()
	h := &Host{password: "private-launch-password", base: upstream.URL, client: upstream.Client()}
	if !h.Ready(context.Background()) || calls != 2 {
		t.Fatal("private authenticated readiness missing", calls)
	}
	h.password = "wrong"
	if h.Ready(context.Background()) {
		t.Fatal("attached to unrelated host")
	}
	for _, raw := range []string{`[{"name":"build","permission":[{"permission":"*","pattern":"*","action":"allow"}]}]`, `[{"name":"build","permission":[{"permission":"*","pattern":"*","action":"deny"},{"permission":"read","pattern":"*.txt","action":"allow"}]}]`, `[]`} {
		if verifyPermissions([]byte(raw)) == nil {
			t.Fatal("unsafe merged agent accepted", raw)
		}
	}
}

func TestPortConflictStatusDoesNotContactUnrelatedHost(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := os.WriteFile(filepath.Join(dir, "error.txt"), []byte("port_conflict"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(401) }))
	defer other.Close()
	h := &Host{password: "private", base: other.URL, client: other.Client()}
	for i := 0; i < 3; i++ {
		status := h.Status(context.Background(), &Settings{DB: db}, dir)
		if status.HostReady || calls != 0 {
			t.Fatalf("conflicted host contacted %d times", calls)
		}
	}
}

func TestExactMCPPermissionsRequiredForReadiness(t *testing.T) {
	raw := []byte(`[{"name":"build","permission":[{"permission":"*","pattern":"*","action":"deny"},{"permission":"question","pattern":"*","action":"allow"},{"permission":"design_design_execute","pattern":"*","action":"allow"}]}]`)
	if e := verifyPermissions(raw, "design_design_execute"); e != nil {
		t.Fatal(e)
	}
	if verifyPermissions(raw) == nil || verifyPermissions(raw, "design_design_execute", "design_design_task_wait") == nil {
		t.Fatal("unexpected/missing MCP grant accepted")
	}
}
