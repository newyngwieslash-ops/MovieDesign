package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/agent"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/generation"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type headerTransport struct {
	base          http.RoundTripper
	origin, token string
}

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Origin", h.origin)
	if h.token != "" {
		r.Header.Set("Authorization", "Bearer "+h.token)
	}
	return h.base.RoundTrip(r)
}

type fixture struct {
	server    *Server
	http      *httptest.Server
	db        *sql.DB
	scope     agent.ToolScope
	tasks     *tasks.Service
	providers *providers.Service
	client    *sdk.ClientSession
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db, e := store.Open(filepath.Join(t.TempDir(), "mcp.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	p, e := (store.Objects{DB: db}).CreateProject(ctx, "new-project", "P")
	if e != nil {
		t.Fatal(e)
	}
	ws, e := workspace.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ws.Close() })
	secrets, e := providers.OpenSecrets(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ps := providers.NewService(db, secrets)
	ps.Files = workspace.NewService(db, ws)
	cfg := providers.Config{ID: "local", Name: "Local", Kind: "image", Protocol: "openai-images", BaseURL: "http://127.0.0.1:11434", ModelID: "fixture", Enabled: true, Capability: providers.Capability{MaxPromptBytes: 65536, MaxCount: 2, MaxReferences: 2, Edit: true, Sizes: []string{"1024x1024"}}, Pricing: &providers.Pricing{Currency: "USD", Unit: "image", UnitAmount: 1, MinorUnitScale: 2, CreditNumerator: 1, CreditDenominator: 1, Source: "declared", Version: "v1"}}
	raw, _ := json.Marshal(map[string]any{"requestId": "provider", "action": "create", "config": cfg})
	if _, e = ps.Execute(ctx, raw); e != nil {
		t.Fatal(e)
	}
	ev := events.NewService(db)
	ts := tasks.NewService(db, ev, nil)
	t.Cleanup(func() { ts.Close(); ev.Close() })
	qs := questions.NewService(db, ts, ev)
	ts.BillingGate = qs.Billing
	turns := agent.NewTurns(db)
	scope, e := turns.Begin(ctx, p.ID, "session", "native")
	if e != nil {
		t.Fatal(e)
	}
	s := New("http://tauri.localhost", turns, Services{Objects: store.Objects{DB: db}, Generation: generation.Service{Providers: ps, Tasks: ts}, Tasks: ts})
	h := httptest.NewServer(s)
	t.Cleanup(h.Close)
	t.Cleanup(func() { s.CloseEpoch("") })
	f := &fixture{server: s, http: h, db: db, scope: scope, tasks: ts, providers: ps}
	f.client = f.connect(t, s.origin, s.token)
	t.Cleanup(func() { f.client.Close() })
	return f
}
func (f *fixture) connect(t *testing.T, origin, token string) *sdk.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: f.http.URL, HTTPClient: &http.Client{Transport: headerTransport{http.DefaultTransport, origin, token}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return session
}
func call(t *testing.T, c *sdk.ClientSession, name, raw string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, e := c.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: json.RawMessage(raw)})
	if e != nil {
		t.Fatal(e)
	}
	if r.IsError {
		t.Fatal("business result became retryable native tool error")
	}
	b, _ := json.Marshal(r.StructuredContent)
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		t.Fatal(string(b))
	}
	return v
}
func TestOfficialClientCatalogOriginTokenScopeAndForty(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for _, pair := range [][2]string{{"http://evil.invalid", f.server.token}, {f.server.origin, ""}} {
		client := sdk.NewClient(&sdk.Implementation{Name: "bad", Version: "1"}, nil)
		if _, e := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: f.http.URL, HTTPClient: &http.Client{Transport: headerTransport{http.DefaultTransport, pair[0], pair[1]}}}, nil); e == nil {
			t.Fatal("untrusted client initialized")
		}
	}
	listed, e := f.client.ListTools(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		raw, _ := json.Marshal(tool.InputSchema)
		if strings.Contains(string(raw), "session_id") || strings.Contains(string(raw), "project_id") || strings.Contains(string(raw), "frozen") {
			t.Fatal("trusted scope leaked", string(raw))
		}
	}
	want := []string{"design_analyse_media", "design_capability_get", "design_capability_list", "design_composition_edit", "design_composition_get", "design_composition_search", "design_deliver", "design_document_write", "design_execute", "design_image_search", "design_project_rules", "design_read", "design_resource_edit", "design_resource_get", "design_task_query", "design_task_resolve", "design_task_wait", "design_voice_search"}
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Fatal(names)
	}
	for _, raw := range []string{`{"unknown":true}`, `{"session_id":"forged"}`, `{"offset":null}`} {
		v := call(t, f.client, "design_capability_list", raw)
		if v["error"] == nil {
			t.Fatal("invalid accepted", raw)
		}
	}
	var count int
	f.db.QueryRow("SELECT call_count FROM chat_turns WHERE id=?", f.scope.TurnID).Scan(&count)
	aborted := 0
	f.server.Turns.Abort = func(context.Context, string) error { aborted++; return nil }
	for ; count < 40; count++ {
		if v := call(t, f.client, "design_capability_list", `{}`); v["error"] != nil {
			t.Fatal(v)
		}
	}
	v := call(t, f.client, "design_composition_edit", `{"requestId":"over-budget","action":"create","value":{"name":"Forbidden","type_id":"x"}}`)
	if v["error"].(map[string]any)["code"] != "capacity" || aborted != 1 {
		t.Fatal(v, aborted)
	}
	var n int
	f.db.QueryRow("SELECT count(*) FROM compositions").Scan(&n)
	if n != 0 {
		t.Fatal("41st caused effect")
	}
	old := f.scope
	if e = f.server.Turns.End(ctx, old, func(context.Context) error { return nil }, func(context.Context) error { return f.server.CloseEpoch(old.Epoch) }); e != nil {
		t.Fatal(e)
	}
	if _, e = f.server.Turns.Begin(ctx, old.ProjectID, "session", "native"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.server.Turns.Acquire(old); e == nil {
		t.Fatal("old connection scope survived")
	}
}

const output = `"output":{"resources":[{"key":"main","mode":"create","name":"Result"}],"composition":{"name":"Result","type":{"label":"Images"},"placements":[{"key":"main","role":"primary"}]}}`

func TestExecuteAdmissionBillingWaitCrossProjectAndReplay(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for _, raw := range []string{`{"requestId":"missing-output","capabilityId":"image:local","prompt":"x","params":{},"references":[]}`, `{"requestId":"forged","capabilityId":"image:local","prompt":"x","params":{},"references":[],"assets":[] ,` + output + `}`} {
		if v := call(t, f.client, "design_execute", raw); v["error"] == nil {
			t.Fatal("invalid admitted", v)
		}
	}
	other, e := (store.Objects{DB: f.db}).CreateProject(ctx, "other-project", "Other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec("INSERT INTO assets(id,path,size,mtime_ms,name,created_at,updated_at)VALUES('foreign','foreign.png',1,1,'Foreign',1,1)"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec("INSERT INTO asset_projects(asset_id,project_id)VALUES('foreign',?)", other.ID); e != nil {
		t.Fatal(e)
	}
	v := call(t, f.client, "design_execute", `{"requestId":"cross-project","capabilityId":"image:local","prompt":"x","params":{},"references":["foreign"],`+output+`}`)
	if v["error"] == nil {
		t.Fatal("foreign reference admitted")
	}
	var n int
	f.db.QueryRow("SELECT count(*) FROM tasks").Scan(&n)
	if n != 0 {
		t.Fatal("rejected input allocated task")
	}
	wire := `{"requestId":"execute","capabilityId":"image:local","prompt":" exact text ","params":{},"references":[],` + output + `}`
	result := make(chan map[string]any, 1)
	go func() { result <- call(t, f.client, "design_execute", wire) }()
	var id string
	deadline := time.Now().Add(4 * time.Second)
	for id == "" && time.Now().Before(deadline) {
		_ = f.db.QueryRow("SELECT id FROM tasks").Scan(&id)
		time.Sleep(10 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("task not submitted")
	}
	select {
	case <-result:
		t.Fatal("billing prematurely settled")
	default:
	}
	if r := call(t, f.client, "design_task_resolve", fmt.Sprintf(`{"requestId":"agent-retry","taskId":%q,"action":"retry","expected_revision":1}`, id)); r["error"].(map[string]any)["code"] != "waiting_user" {
		t.Fatal("agent retry bypassed human decision", r)
	}
	task, e := f.tasks.Query(ctx, f.scope.ProjectID, id)
	if e != nil || task.Status != tasks.WaitingUser {
		t.Fatal(task, e)
	}
	if r := call(t, f.client, "design_task_query", fmt.Sprintf(`{"taskId":%q}`, id)); r["status"] != "waiting_user" {
		t.Fatal(r)
	}
	if r := call(t, f.client, "design_task_resolve", fmt.Sprintf(`{"requestId":"cancel","taskId":%q,"action":"cancel","expected_revision":%d}`, id, task.Revision)); r["status"] != "cancelled" {
		t.Fatal(r)
	}
	select {
	case r := <-result:
		if r["status"] != "cancelled" {
			t.Fatal(r)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("execute did not settle")
	}
	if _, e = f.db.Exec("UPDATE provider_configs SET deleted=1 WHERE id='local'"); e != nil {
		t.Fatal(e)
	}
	if r := call(t, f.client, "design_execute", wire); r["id"] != id || r["status"] != "cancelled" {
		t.Fatal("frozen replay looked up disabled provider", r)
	}
	f.db.QueryRow("SELECT count(*) FROM tasks").Scan(&n)
	if n != 1 {
		t.Fatal("replay duplicated task")
	}
	var ledger int
	f.db.QueryRow("SELECT count(*) FROM agent_tool_calls WHERE request_id='execute'").Scan(&ledger)
	if ledger != 2 {
		t.Fatal("request replay bypassed budget", ledger)
	}
	if r := call(t, f.client, "design_execute", strings.Replace(wire, " exact text ", "different", 1)); r["error"] == nil {
		t.Fatal("mutated replay accepted")
	}
	// Unknown stays a successful structured MCP result, never an isError transport failure.
	_, e = f.db.Exec("UPDATE tasks SET status='unknown',view_json=json_set(view_json,'$.status','unknown') WHERE id=?", id)
	if e != nil {
		t.Fatal(e)
	}
	if r := call(t, f.client, "design_task_wait", fmt.Sprintf(`{"taskId":%q}`, id)); r["status"] != "unknown" {
		t.Fatal(r)
	}
}

func TestExecuteDisconnectOnlyStopsWaiting(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	wire := `{"requestId":"disconnect","capabilityId":"image:local","prompt":"x","params":{},"references":[],` + output + `}`
	go func() {
		_, e := f.client.CallTool(ctx, &sdk.CallToolParams{Name: "design_execute", Arguments: json.RawMessage(wire)})
		result <- e
	}()
	var id string
	deadline := time.Now().Add(3 * time.Second)
	for id == "" && time.Now().Before(deadline) {
		_ = f.db.QueryRow("SELECT id FROM tasks").Scan(&id)
		time.Sleep(10 * time.Millisecond)
	}
	if id == "" {
		cancel()
		t.Fatal("not submitted")
	}
	cancel()
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("wait not interrupted")
	}
	task, e := f.tasks.Query(context.Background(), f.scope.ProjectID, id)
	if e != nil || task.Status != tasks.WaitingUser {
		t.Fatal("disconnect cancelled durable generation", task, e)
	}
}

func TestMetadataObjectCreateUpdateReplayAndNestedValidation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	objects := store.Objects{DB: f.db}
	raw, e := objects.Mutate(ctx, f.scope.ProjectID, "composition_types", store.Mutation{RequestID: "type", Action: "create", Value: json.RawMessage(`{"label":"Metadata"}`)})
	if e != nil {
		t.Fatal(e)
	}
	var typ map[string]any
	json.Unmarshal(raw, &typ)
	listed, e := f.client.ListTools(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "design_composition_edit" || tool.Name == "design_resource_edit" {
			b, _ := json.Marshal(tool.InputSchema)
			var schema map[string]any
			json.Unmarshal(b, &schema)
			value := schema["properties"].(map[string]any)["value"].(map[string]any)
			if value["type"] != "object" {
				t.Errorf("%s value schema must describe object: %s", tool.Name, b)
			}
		}
	}
	composition := ""
	for _, kind := range []string{"composition", "resource"} {
		value := map[string]any{"name": "Initial", "type_id": typ["id"]}
		if kind == "resource" {
			value = map[string]any{"composition_id": composition, "description": "Initial"}
		}
		body := map[string]any{"requestId": kind + "-create", "action": "create", "value": value}
		wire, _ := json.Marshal(body)
		created := call(t, f.client, "design_"+kind+"_edit", string(wire))
		id, ok := created["id"].(string)
		if !ok {
			t.Fatalf("valid %s create rejected: %v", kind, created)
		}
		if kind == "composition" {
			composition = id
		}
		if replay := call(t, f.client, "design_"+kind+"_edit", string(wire)); replay["id"] != id {
			t.Fatal("create replay changed identity", replay)
		}
		field := "name"
		if kind == "resource" {
			field = "description"
		}
		body = map[string]any{"requestId": kind + "-update", "action": "update", "id": id, "value": map[string]any{field: "Updated"}}
		wire, _ = json.Marshal(body)
		updated := call(t, f.client, "design_"+kind+"_edit", string(wire))
		if updated[field] != "Updated" {
			t.Fatal("update failed", updated)
		}
		if replay := call(t, f.client, "design_"+kind+"_edit", string(wire)); replay[field] != "Updated" || replay["id"] != id {
			t.Fatal("update replay failed", replay)
		}
		body["value"] = map[string]any{field: "Changed"}
		wire, _ = json.Marshal(body)
		if r := call(t, f.client, "design_"+kind+"_edit", string(wire)); r["error"] == nil {
			t.Fatal("changed request identity replayed", r)
		}
		body["requestId"] = kind + "-invalid"
		body["value"] = map[string]any{field: "Bad", "unknown": true}
		wire, _ = json.Marshal(body)
		if r := call(t, f.client, "design_"+kind+"_edit", string(wire)); r["error"] == nil {
			t.Fatal("unknown nested field accepted", r)
		}
		got := call(t, f.client, "design_"+kind+"_get", fmt.Sprintf(`{"id":%q}`, id))
		if got[field] != "Updated" {
			t.Fatal("rejected input changed stored metadata", got)
		}
	}
}
