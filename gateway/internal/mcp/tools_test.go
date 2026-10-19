package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/testsupport"
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

func TestStoryboardMCP(t *testing.T) {
	f := setup(t)
	f.server.services.Questions = questions.NewService(f.db, f.tasks, f.tasks.Events)
	f.server.services.Delivery = delivery.NewService(f.db, f.providers.Files.Workspace, "agent")
	testsupport.CreativeUpstream(t, f.db, f.scope.ProjectID, "film", func(raw []byte) error {
		r, e := f.server.services.Delivery.WriteDocument(context.Background(), f.scope.ProjectID, raw)
		if e != nil {
			return e
		}
		if r.Delivery.Status != "completed" {
			return fmt.Errorf("%+v", r)
		}
		return nil
	})
	raw := `{"requestId":"storyboard","mode":"create","format":"md","typedDocument":{"kind":"storyboard","pipelineId":"film","expectedRevision":0,"storyboard":{"panels":[{"id":"p","timeMs":0,"framing":"close","action":"speak","transition":"continuous"}],"shots":[{"id":"s","startMs":0,"endMs":2000,"panelIds":["p"],"narrativeRole":"intro","startState":"start","endState":"end"}],"timeline":{"durationMs":2000,"shotIds":["s"],"audioIntent":"none"}}},` + output + `}`
	r := call(t, f.client, "design_document_write", raw)
	if r["error"] != nil || r["document"].(map[string]any)["delivery"].(map[string]any)["status"] != "completed" {
		t.Fatal(r)
	}
	var n int
	if e := f.db.QueryRow("SELECT count(*) FROM storyboard_revisions").Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	for _, bad := range []string{strings.Replace(raw, `"expectedRevision":0,`, "", 1), strings.Replace(raw, `"timeMs":0`, `"timeMs":0,"extra":true`, 1), strings.Replace(raw, `"durationMs":2000`, `"durationMs":2.5`, 1)} {
		v, e := f.client.CallTool(context.Background(), &sdk.CallToolParams{Name: "design_document_write", Arguments: json.RawMessage(bad)})
		if e == nil && !v.IsError {
			if got := call(t, f.client, "design_document_write", bad); got["error"] == nil {
				t.Fatal("strict schema accepted", got)
			}
		}
	}
}
