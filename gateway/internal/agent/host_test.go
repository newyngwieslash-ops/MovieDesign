package agent

import (
	"context"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
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

func TestPortBindFailureStatusDoesNotContactUnrelatedHost(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var calls atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(401) }))
	defer other.Close()
	h := &Host{password: "private", base: other.URL, client: other.Client()}
	for _, tc := range []struct{ marker, message string }{
		{"port_conflict", "agent 启动失败：127.0.0.1:4096 已占用；未连接或关闭现有服务。"},
		{"port_permission_denied", "agent 启动失败：127.0.0.1:4096 绑定被拒绝；可能受系统端口保留或权限限制，请检查后重新启动。"},
		{"port_bind_failed", "agent 启动失败：127.0.0.1:4096 绑定失败，请检查系统网络状态后重新启动。"},
		{"start_failed", "agent 启动失败，请检查应用专用 binary 后重新启动。"},
		{"exited", "受管 agent 已退出，请重新启动应用。"},
		{"unknown", "agent 未就绪；媒体工具尚未注册。"},
	} {
		t.Run(tc.marker, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "error.txt"), []byte(tc.marker), 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				status := h.Status(context.Background(), &Settings{DB: db}, dir)
				if status.HostReady || status.ToolsReady || status.Message != tc.message {
					t.Fatalf("marker %q produced status %+v; want message %q and readiness false", tc.marker, status, tc.message)
				}
				if calls.Load() != 0 {
					t.Fatalf("failed host contacted %d times", calls.Load())
				}
			}
		})
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
