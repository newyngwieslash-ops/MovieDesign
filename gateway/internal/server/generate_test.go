package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateAdmissionQuoteReplayAndBillingGate(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "generate.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ws, e := workspace.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer ws.Close()
	files := workspace.NewService(db, ws)
	secrets, e := providers.OpenSecrets(filepath.Join(t.TempDir(), "secrets"))
	if e != nil {
		t.Fatal(e)
	}
	ps := providers.NewService(db, secrets)
	posts := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		t.Error("billing gate leaked POST")
		w.WriteHeader(500)
	}))
	defer remote.Close()
	cfg := providers.Config{ID: "local", Name: "Local", Kind: "image", Protocol: "openai-images", BaseURL: remote.URL, ModelID: "custom", Enabled: true, Capability: providers.Capability{MaxPromptBytes: 65536, MaxCount: 2, MaxReferences: 2, Edit: true, Sizes: []string{"1024x1024"}}, Pricing: &providers.Pricing{Currency: "USD", Unit: "image", UnitAmount: 7, MinorUnitScale: 2, CreditNumerator: 2, CreditDenominator: 3, Source: "declared", Version: "v1"}}
	save := func(id string) {
		b, _ := json.Marshal(map[string]any{"requestId": id, "action": "create", "config": cfg})
		if _, e := ps.Execute(context.Background(), b); e != nil {
			t.Fatal(e)
		}
	}
	save("provider-create")
	p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "generate-project", "Generate")
	if e != nil {
		t.Fatal(e)
	}
	h := newHandler("http://tauri.localhost", db, files, ps).(*Server)
	defer h.Close()
	call := func(method, route string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, route, bytes.NewReader(body))
		r.Header.Set("Origin", "http://tauri.localhost")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	input := map[string]any{"requestId": "estimate-one", "capability_id": "image:local", "prompt": " 原文\n ", "params": map[string]any{}, "references": []string{}, "count": 1, "output": map[string]any{"resources": []any{map[string]any{"key": "main", "mode": "create", "name": "Result"}}, "composition": map[string]any{"name": "Result", "type": map[string]any{"label": "Images"}, "placements": []any{map[string]any{"key": "main", "role": "primary"}}}}}
	t.Run("output-label-boundary", func(t *testing.T) {
		output := input["output"].(map[string]any)["composition"].(map[string]any)["type"].(map[string]any)
		output["label"] = strings.Repeat("x", 257)
		for _, route := range []string{"estimate", "image"} {
			input["requestId"] = "overlong-" + route
			b, _ := json.Marshal(input)
			got := call("POST", "/api/generate/"+route+"?project_id="+p.ID, b)
			if got.Code != 400 {
				t.Errorf("overlong output admitted by %s: %d %s", route, got.Code, got.Body.String())
			}
		}
		for _, table := range []string{"tasks", "questions", "task_outputs", "price_quotes"} {
			var n int
			if e := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
				t.Errorf("invalid output allocated %s: %d %v", table, n, e)
			}
		}
		if posts != 0 {
			t.Fatal("invalid output submitted provider")
		}
		output["label"] = strings.Repeat("x", 256)
		input["requestId"] = "boundary-estimate"
		b, _ := json.Marshal(input)
		if got := call("POST", "/api/generate/estimate?project_id="+p.ID, b); got.Code != 200 {
			t.Fatal("valid boundary estimate", got.Code, got.Body.String())
		}
		input["requestId"] = "boundary-generate"
		b, _ = json.Marshal(input)
		if got := call("POST", "/api/generate/image?project_id="+p.ID, b); got.Code != 202 {
			t.Fatal("valid boundary generation", got.Code, got.Body.String())
		}
		output["label"] = "Images"
		input["requestId"] = "estimate-one"
	})
	raw, _ := json.Marshal(input)
	estimate := call("POST", "/api/generate/estimate?project_id="+p.ID, raw)
	if estimate.Code != 200 {
		t.Fatalf("estimate: %d %s", estimate.Code, estimate.Body.String())
	}
	var quote providers.Quote
	json.Unmarshal(estimate.Body.Bytes(), &quote)
	if quote.EstimatedCredits != 5 || quote.EstimatedAmount != 7 {
		t.Fatal(quote)
	}
	input["requestId"] = "generate-one"
	input["quote_id"] = quote.QuoteID
	raw, _ = json.Marshal(input)
	created := call("POST", "/api/generate/image?project_id="+p.ID, raw)
	if created.Code != 202 {
		t.Fatalf("generate: %d %s", created.Code, created.Body.String())
	}
	var accepted struct {
		TaskID string `json:"task_id"`
	}
	json.Unmarshal(created.Body.Bytes(), &accepted)
	if accepted.TaskID == "" || strings.Contains(created.Body.String(), "prompt") {
		t.Fatal("expected task_id-only acceptance", created.Body.String())
	}
	task, e := h.tasks.Query(context.Background(), p.ID, accepted.TaskID)
	if e != nil {
		t.Fatal(e)
	}
	if task.Status != tasks.WaitingUser || task.Problem.Source != "billing" || task.Prompt != " 原文\n " {
		t.Fatal(task)
	}
	if e = h.tasks.Run(context.Background(), p.ID, task.ID); e != nil {
		t.Fatal(e)
	}
	if posts != 0 {
		t.Fatal("unapproved network")
	}
	if _, e = db.Exec("UPDATE provider_configs SET deleted=1"); e != nil {
		t.Fatal(e)
	}
	replay := call("POST", "/api/generate/image?project_id="+p.ID, raw)
	if replay.Code != 202 || replay.Body.String() != created.Body.String() {
		t.Fatal("replay resolved changed provider", replay.Body.String())
	}
	input["count"] = nil
	changed, _ := json.Marshal(input)
	if got := call("POST", "/api/generate/image?project_id="+p.ID, changed); got.Code != 409 {
		t.Fatal("full original wire lost", got.Code, got.Body.String())
	}
	if _, e = db.Exec("UPDATE provider_configs SET deleted=0"); e != nil {
		t.Fatal(e)
	}
	input["requestId"] = "changed-config-generate"
	input["count"] = 1
	if _, e = db.Exec("UPDATE provider_configs SET config_json=json_set(config_json,'$.pricing.unit_amount',9)"); e != nil {
		t.Fatal(e)
	}
	changedPrice, _ := json.Marshal(input)
	if got := call("POST", "/api/generate/image?project_id="+p.ID, changedPrice); got.Code != 409 {
		t.Fatal("stale quote accepted", got.Code, got.Body.String())
	}
	delete(input, "quote_id")
	delete(input, "params")
	delete(input, "references")
	delete(input, "count")
	input["requestId"] = "optional-estimate"
	optionalWire, _ := json.Marshal(input)
	optional := call("POST", "/api/generate/estimate?project_id="+p.ID, optionalWire)
	if optional.Code != 200 {
		t.Fatal("optional params rejected", optional.Code, optional.Body.String())
	}
	input["params"] = map[string]any{}
	explicit, _ := json.Marshal(input)
	if got := call("POST", "/api/generate/estimate?project_id="+p.ID, explicit); got.Code != 409 {
		t.Fatal("omitted vs explicit params receipt collapsed", got.Code)
	}
	input["requestId"] = "bad-null-params"
	input["params"] = nil
	explicit, _ = json.Marshal(input)
	if got := call("POST", "/api/generate/estimate?project_id="+p.ID, explicit); got.Code != 400 {
		t.Fatal("null params accepted", got.Code)
	}
	input["params"] = map[string]any{}
	input["count"] = 2
	input["requestId"] = "bad-count"
	explicit, _ = json.Marshal(input)
	if got := call("POST", "/api/generate/estimate?project_id="+p.ID, explicit); got.Code != 400 {
		t.Fatal("count/slots mismatch accepted", got.Code)
	}
	input["count"] = 1
	for _, field := range []string{"assets", "provider", "project_id", "original_wire"} {
		input["requestId"] = "bad-" + field
		input[field] = nil
		b, _ := json.Marshal(input)
		if got := call("POST", "/api/generate/estimate?project_id="+p.ID, b); got.Code != 400 {
			t.Fatal("private input accepted", field, got.Code)
		}
		delete(input, field)
	}
	input["count"] = 1
	delete(input, "quote_id")
	for _, ref := range []string{"https://example.com/a.png", "missing-asset"} {
		input["requestId"] = "bad-reference"
		input["references"] = []string{ref}
		b, _ := json.Marshal(input)
		if got := call("POST", "/api/generate/estimate?project_id="+p.ID, b); got.Code != 400 {
			t.Fatal("bad reference", got.Code, got.Body.String())
		}
	}
	catalog := call("GET", "/api/v3/capabilities?operation=image&offset=0&limit=10", nil)
	if catalog.Code != 200 || !strings.Contains(catalog.Body.String(), "image:local") {
		t.Fatal(catalog.Code, catalog.Body.String())
	}
	card := call("GET", "/api/knowledge/providers/openai-images.md", nil)
	if card.Code != 200 {
		t.Fatal("knowledge card missing", card.Code)
	}
	b, _ := io.ReadAll(card.Body)
	if !strings.Contains(string(b), "mask") {
		t.Fatal("missing mask contract")
	}
}
