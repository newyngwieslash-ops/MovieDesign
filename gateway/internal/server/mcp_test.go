package server

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

type mcpHeaders struct{ token string }

func (h mcpHeaders) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Origin", "http://tauri.localhost")
	r.Header.Set("Authorization", "Bearer "+h.token)
	return http.DefaultTransport.RoundTrip(r)
}
func TestMCPDeliveryTrustedActorAndHTTPUnknownRecovery(t *testing.T) {
	ctx := context.Background()
	db, e := store.Open(filepath.Join(t.TempDir(), "actors.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p, e := (store.Objects{DB: db}).CreateProject(ctx, "create-project", "Actors")
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "projects", p.ID, "files")
	if e = os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"agent-first", "user-first", "agent-unknown"} {
		if e = os.WriteFile(filepath.Join(dir, name+".txt"), []byte("source"), 0600); e != nil {
			t.Fatal(e)
		}
	}

	h := newTestServer(t, "http://tauri.localhost", db, root)
	if _, e = h.turns.Begin(ctx, p.ID, "actor-session", "actor-native"); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	defer h.mcp.CloseEpoch("")
	client := sdk.NewClient(&sdk.Implementation{Name: "actor-test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: mcpHeaders{h.mcp.Connection().Token}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	body := func(id string) string {
		return fmt.Sprintf(`{"requestId":%q,"sources":[{"key":"one","path":"projects/%s/files/%s.txt"}],"output":{"resources":[{"key":"one","mode":"create","name":"Document"}],"composition":{"name":%q,"type":{"label":"Documents"},"placements":[{"key":"one","role":"main"}]}}}`, id, p.ID, id, id)
	}
	mcpCall := func(wire string) map[string]any {
		t.Helper()
		r, e := session.CallTool(ctx, &sdk.CallToolParams{Name: "design_deliver", Arguments: json.RawMessage(wire)})
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(r.StructuredContent)
		var v map[string]any
		json.Unmarshal(b, &v)
		if v["delivery"] == nil {
			t.Fatalf("delivery failed: %s", b)
		}
		return v["delivery"].(map[string]any)
	}
	httpCall := func(wire string) map[string]any {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/deliver?project_id="+p.ID, strings.NewReader(wire))
		r.Header.Set("Origin", "http://tauri.localhost")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var v map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return v["delivery"].(map[string]any)
	}
	checkActor := func(receipt map[string]any, want string) {
		t.Helper()
		if receipt["status"] != "completed" {
			t.Fatal(receipt)
		}
		version := receipt["versions"].([]any)[0].(map[string]any)["versionId"]
		var actor string
		if e := db.QueryRow("SELECT origin FROM versions WHERE id=?", version).Scan(&actor); e != nil || actor != want {
			t.Errorf("version actor=%q want=%q err=%v", actor, want, e)
		}
	}
	first := mcpCall(body("agent-first"))
	checkActor(first, "agent")
	checkActor(httpCall(body("user-first")), "user")
	if _, e = db.Exec(`CREATE TRIGGER fixture_pause_delivery BEFORE INSERT ON versions BEGIN SELECT RAISE(FAIL,'fixture interruption'); END`); e != nil {
		t.Fatal(e)
	}
	pending := mcpCall(body("agent-unknown"))
	if pending["status"] != "unknown" {
		t.Fatal("expected interrupted agent delivery", pending)
	}
	if _, e = db.Exec("DROP TRIGGER fixture_pause_delivery"); e != nil {
		t.Fatal(e)
	}
	recovered := httpCall(body("agent-unknown"))
	checkActor(recovered, "agent")
	checkActor(mcpCall(body("agent-unknown")), "agent")
	var n int
	if e = db.QueryRow("SELECT count(*) FROM versions").Scan(&n); e != nil || n != 3 {
		t.Fatal("replay duplicated versions", n, e)
	}
}
