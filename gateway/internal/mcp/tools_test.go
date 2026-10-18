package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestRealCatalogMetadataAndToolRefusals(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.server.services.Files = f.providers.Files
	var imageBytes bytes.Buffer
	png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 11, 9)))
	stage, e := f.providers.Files.Stage(ctx, f.scope.ProjectID, "image", "picture.png", &imageBytes)
	if e != nil {
		t.Fatal(e)
	}
	asset, e := f.providers.Files.Commit(ctx, f.scope.ProjectID, "image-commit", stage)
	if e != nil {
		t.Fatal(e)
	}
	r := call(t, f.client, "design_analyse_media", fmt.Sprintf(`{"references":[%q],"type":"metadata"}`, asset.ID))
	if r["error"] != nil || r["items"].([]any)[0].(map[string]any)["metadata"].(map[string]any)["width"] != float64(11) {
		t.Fatal(r)
	}
	r = call(t, f.client, "design_image_search", `{"query":""}`)
	if r["coverage"].(map[string]any)["total"] != float64(1) {
		t.Fatal(r)
	}
	stage, e = f.providers.Files.Stage(ctx, f.scope.ProjectID, "voice", "voice-catalog.json", strings.NewReader(`[{"voice_id":"21m00Tcm4TlvDq8ikWAM","provider":"user-registered-elevenlabs","language":"en","description":"User registered catalog entry; not network verified"}]`))
	if e != nil {
		t.Fatal(e)
	}
	asset, e = f.providers.Files.Commit(ctx, f.scope.ProjectID, "voice-commit", stage)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec("UPDATE assets SET name='voice-catalog.json' WHERE id=?", asset.ID); e != nil {
		t.Fatal(e)
	}
	r = call(t, f.client, "design_voice_search", `{"query":"en"}`)
	if r["coverage"].(map[string]any)["total"] != float64(1) {
		t.Fatal(r)
	}
	for _, name := range []string{"design_capability_get", "design_composition_get", "design_resource_get"} {
		if r := call(t, f.client, name, `{"id":"missing"}`); r["error"] == nil {
			t.Fatal(name, r)
		}
	}
	for _, name := range []string{"design_task_query", "design_task_wait"} {
		if r := call(t, f.client, name, `{"taskId":"missing"}`); r["error"] == nil {
			t.Fatal(name, r)
		}
	}
	if r := call(t, f.client, "design_composition_search", `{"offset":-1}`); r["error"] == nil {
		t.Fatal(r)
	}
	if r := call(t, f.client, "design_project_rules", `{"action":"approve"}`); r["error"] == nil {
		t.Fatal(r)
	}
	if r := call(t, f.client, "design_document_write", `{"requestId":"bad","mode":"create","format":"pdf"}`); r["error"] == nil {
		t.Fatal(r)
	}
}

func TestConfiguredTextCapabilityAndKnowledge(t *testing.T) {
	f := setup(t)
	f.server.services.Generation.TextProvider = func(context.Context) (providers.FrozenProvider, error) {
		return providers.TextProvider([]byte(`{"protocol":"openai-compatible","base_url":"http://127.0.0.1:11434/v1","model_id":"local","context":32000,"output":4096}`), "")
	}
	r := call(t, f.client, "design_capability_get", `{"id":"text:configured-llm"}`)
	if r["error"] != nil || r["capability"].(map[string]any)["operation"] != "text" {
		t.Fatal(r)
	}
	r = call(t, f.client, "design_read", `{"file_path":"knowledge/providers/text.md"}`)
	if r["error"] != nil || r["content"] == "" {
		t.Fatal(r)
	}
}

func TestDocumentReadRulesAndCatalogTools(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.server.services.Files = f.providers.Files
	f.server.services.Delivery = delivery.NewService(f.db, f.providers.Files.Workspace, "agent")
	f.server.services.Questions = questions.NewService(f.db, f.tasks, f.tasks.Events)
	raw := `{"requestId":"doc","mode":"create","format":"md","content":"hello\nworld",` + output + `}`
	result := call(t, f.client, "design_document_write", raw)
	receipt := result["delivery"].(map[string]any)
	if receipt["status"] != "completed" {
		t.Fatal(result)
	}
	version := receipt["versions"].([]any)[0].(map[string]any)["versionId"].(string)
	got := call(t, f.client, "design_read", fmt.Sprintf(`{"versionId":%q,"offset":1,"limit":1}`, version))
	if got["content"] != "world" {
		t.Fatal(got)
	}
	if r := call(t, f.client, "design_read", `{"file_path":"C:/private.txt"}`); r["error"] == nil {
		t.Fatal(r)
	}
	if r := call(t, f.client, "design_analyse_media", `{"references":["unavailable"],"type":"semantic"}`); r["error"] == nil {
		t.Fatal(r)
	}
	for _, name := range []string{"design_image_search", "design_voice_search"} {
		r := call(t, f.client, name, `{"query":"missing"}`)
		if r["error"] != nil || r["coverage"].(map[string]any)["total"] != float64(0) {
			t.Fatal(r)
		}
		if r := call(t, f.client, name, `{"query":"x","offset":-1}`); r["error"] == nil {
			t.Fatal(r)
		}
	}
	r := call(t, f.client, "design_project_rules", `{"action":"save","requestId":"rules","content":"中文","expectedRevision":null}`)
	if r["kind"] != "project_rules" {
		t.Fatal(r)
	}
	before, e := f.server.services.Questions.ReadRules(ctx, f.scope.ProjectID)
	if e != nil || before.Content != "" {
		t.Fatal(before, e)
	}
	_, e = f.server.services.Questions.Reply(ctx, f.scope.ProjectID, r["id"].(string), questions.HumanReply{RequestID: "approve-rules", SessionID: f.scope.SessionID, ExpectedRevision: 1, Answers: [][]string{{"保存项目规则"}}})
	if e != nil {
		t.Fatal(e)
	}
	if r := call(t, f.client, "design_project_rules", `{"action":"read"}`); r["content"] != "中文" {
		t.Fatal(r)
	}
	replay := call(t, f.client, "design_document_write", raw)
	a, _ := json.Marshal(replay)
	b, _ := json.Marshal(result)
	if string(a) != string(b) {
		t.Fatal("replay changed", replay)
	}
	stage, e := f.providers.Files.Stage(ctx, f.scope.ProjectID, "direct-delivery", "direct.txt", strings.NewReader("direct source"))
	if e != nil {
		t.Fatal(e)
	}
	wire := fmt.Sprintf(`{"requestId":"deliver-tool","sources":[{"key":"main","path":%q}],%s}`, stage, output)
	direct := call(t, f.client, "design_deliver", wire)
	if direct["delivery"].(map[string]any)["status"] != "completed" {
		t.Fatal(direct)
	}
	first, _ := json.Marshal(direct)
	again, _ := json.Marshal(call(t, f.client, "design_deliver", wire))
	if string(first) != string(again) {
		t.Fatal("direct delivery replay changed")
	}
	if r := call(t, f.client, "design_deliver", `{"requestId":"bad-deliver","sources":[],"output":{}}`); r["error"] == nil {
		t.Fatal(r)
	}
}
