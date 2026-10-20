package pipeline_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/agent"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/generation"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/pipeline"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
)

type AcceptanceLineageManifest struct {
	ProjectID                  string   `json:"projectId"`
	PipelineID                 string   `json:"pipelineId"`
	IntentVersionID            string   `json:"intentVersionId"`
	ScriptVersionID            string   `json:"scriptVersionId"`
	StoryboardVersionID        string   `json:"storyboardVersionId"`
	PromptVersionID            string   `json:"promptVersionId"`
	ContractID                 string   `json:"contractId"`
	TaskID                     string   `json:"taskId"`
	TaskOutputKey              string   `json:"taskOutputKey"`
	VideoResourceID            string   `json:"videoResourceId"`
	VideoVersionV1ID           string   `json:"videoVersionV1Id"`
	VideoVersionV2ID           string   `json:"videoVersionV2Id"`
	VideoAssetV1ID             string   `json:"videoAssetV1Id"`
	VideoAssetV2ID             string   `json:"videoAssetV2Id"`
	OrderedReferenceAssetIDs   []string `json:"orderedReferenceAssetIds"`
	OrderedReferenceVersionIDs []string `json:"orderedReferenceVersionIds"`
	ExcludedProjectID          string   `json:"excludedProjectId"`
	ExcludedAssetID            string   `json:"excludedAssetId"`
	RedactedMarkers            []string `json:"redactedMarkers"`
}

func scrubProviderSnapshotJSON(raw string) string {
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return "{}"
	}
	delete(doc, "headers")
	delete(doc, "apiKey")
	delete(doc, "key")
	delete(doc, "credential_version")
	delete(doc, "credentialVersion")
	if cfg, ok := doc["config"].(map[string]any); ok {
		delete(cfg, "headers")
		delete(cfg, "apiKey")
		delete(cfg, "key")
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func exportScopedProjectAcceptanceDB(ctx context.Context, srcDB *sql.DB, projectID, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dstPath)

	var seq int
	var name, srcPath string
	if err := srcDB.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &name, &srcPath); err != nil {
		return fmt.Errorf("lookup source db path: %w", err)
	}

	dstDB, err := store.Open(dstPath)
	if err != nil {
		return fmt.Errorf("open destination db: %w", err)
	}
	defer dstDB.Close()

	if _, err := dstDB.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	if _, err := dstDB.ExecContext(ctx, `ATTACH DATABASE ? AS src`, srcPath); err != nil {
		return fmt.Errorf("attach source db: %w", err)
	}
	defer dstDB.ExecContext(ctx, `DETACH DATABASE src`)

	tx, err := dstDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO main.projects SELECT * FROM src.projects WHERE id = ?`, []any{projectID}},
		{`INSERT INTO main.composition_types SELECT * FROM src.composition_types WHERE project_id = ?`, []any{projectID}},
		{`INSERT INTO main.compositions SELECT * FROM src.compositions WHERE project_id = ?`, []any{projectID}},
		{`INSERT INTO main.resources SELECT r.* FROM src.resources r JOIN src.compositions c ON c.id = r.composition_id WHERE c.project_id = ?`, []any{projectID}},
		{`INSERT INTO main.assets SELECT a.* FROM src.assets a JOIN src.asset_projects ap ON ap.asset_id = a.id WHERE ap.project_id = ?`, []any{projectID}},
		{`INSERT INTO main.asset_projects SELECT * FROM src.asset_projects WHERE project_id = ?`, []any{projectID}},
		{`INSERT INTO main.versions SELECT v.* FROM src.versions v JOIN src.resources r ON r.id = v.resource_id JOIN src.compositions c ON c.id = r.composition_id WHERE c.project_id = ? ORDER BY v.created_at, v.id`, []any{projectID}},
		{`INSERT INTO main.composition_members SELECT m.* FROM src.composition_members m JOIN src.compositions c ON c.id = m.composition_id WHERE c.project_id = ?`, []any{projectID}},
		{`INSERT INTO main.deliveries SELECT * FROM src.deliveries WHERE project_id = ?`, []any{projectID}},
		{`INSERT INTO main.delivery_sources SELECT * FROM src.delivery_sources WHERE project_id = ?`, []any{projectID}},
		{`INSERT INTO main.price_quotes SELECT * FROM src.price_quotes WHERE project_id = ?`, []any{projectID}},
		{`INSERT INTO main.questions SELECT * FROM src.questions WHERE project_id = ?`, []any{projectID}},
		{`INSERT INTO main.question_receipts SELECT qr.* FROM src.question_receipts qr JOIN src.questions q ON q.id = qr.question_id WHERE q.project_id = ?`, []any{projectID}},
		{`INSERT INTO main.pipelines SELECT * FROM src.pipelines WHERE project_id = ?`, []any{projectID}},
		{`INSERT INTO main.creative_artifacts SELECT ca.* FROM src.creative_artifacts ca JOIN src.pipelines p ON p.id = ca.pipeline_id WHERE p.project_id = ?`, []any{projectID}},
		{`INSERT INTO main.storyboard_revisions SELECT sr.* FROM src.storyboard_revisions sr JOIN src.pipelines p ON p.id = sr.pipeline_id WHERE p.project_id = ?`, []any{projectID}},
		{`INSERT INTO main.approvals SELECT a.* FROM src.approvals a JOIN src.pipelines p ON p.id = a.pipeline_id WHERE p.project_id = ?`, []any{projectID}},
		{`INSERT INTO main.prompt_revisions SELECT pr.* FROM src.prompt_revisions pr JOIN src.pipelines p ON p.id = pr.pipeline_id WHERE p.project_id = ?`, []any{projectID}},
		{`INSERT INTO main.prompt_contracts SELECT pc.* FROM src.prompt_contracts pc JOIN src.pipelines p ON p.id = pc.pipeline_id WHERE p.project_id = ?`, []any{projectID}},
		{`INSERT INTO main.version_dependencies SELECT vd.* FROM src.version_dependencies vd JOIN src.versions v ON v.id = vd.version_id JOIN src.resources r ON r.id = v.resource_id JOIN src.compositions c ON c.id = r.composition_id WHERE c.project_id = ?`, []any{projectID}},
	}
	for _, st := range statements {
		if _, err := tx.ExecContext(ctx, st.sql, st.args...); err != nil {
			return fmt.Errorf("exec %q: %w", st.sql, err)
		}
	}

	taskRows, err := srcDB.QueryContext(ctx, `SELECT id, project_id, session_id, request_id, status, revision, current_attempt, frozen_input_json, provider_json, view_json, created_at, updated_at FROM tasks WHERE project_id = ?`, projectID)
	if err != nil {
		return err
	}
	defer taskRows.Close()
	for taskRows.Next() {
		var (
			id, pid, sid, reqID, status, frozenJSON, providerJSON, viewJSON string
			rev, curAttempt, createdAt, updatedAt                           int64
		)
		if err := taskRows.Scan(&id, &pid, &sid, &reqID, &status, &rev, &curAttempt, &frozenJSON, &providerJSON, &viewJSON, &createdAt, &updatedAt); err != nil {
			return err
		}
		cleanProviderJSON := scrubProviderSnapshotJSON(providerJSON)
		if _, err := tx.ExecContext(ctx, `INSERT INTO main.tasks(id, project_id, session_id, request_id, status, revision, current_attempt, frozen_input_json, provider_json, view_json, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, pid, sid, reqID, status, rev, curAttempt, frozenJSON, cleanProviderJSON, viewJSON, createdAt, updatedAt); err != nil {
			return err
		}
	}
	if err := taskRows.Err(); err != nil {
		return err
	}

	postTaskStatements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO main.task_attempts SELECT ta.* FROM src.task_attempts ta JOIN src.tasks t ON t.id = ta.task_id WHERE t.project_id = ?`, []any{projectID}},
		{`INSERT INTO main.task_outputs SELECT to1.* FROM src.task_outputs to1 JOIN src.tasks t ON t.id = to1.task_id WHERE t.project_id = ?`, []any{projectID}},
		{`INSERT INTO main.generation_inputs SELECT gi.* FROM src.generation_inputs gi JOIN src.tasks t ON t.id = gi.task_id WHERE t.project_id = ?`, []any{projectID}},
	}
	for _, st := range postTaskStatements {
		if _, err := tx.ExecContext(ctx, st.sql, st.args...); err != nil {
			return fmt.Errorf("exec %q: %w", st.sql, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := dstDB.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return err
	}
	var fkTable string
	var fkRowID int64
	var fkParent string
	var fkIdx int
	if err := dstDB.QueryRowContext(ctx, `PRAGMA foreign_key_check`).Scan(&fkTable, &fkRowID, &fkParent, &fkIdx); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("foreign_key_check query failed: %w", err)
	} else if err == nil {
		return fmt.Errorf("foreign_key_check violation on table %s row %d -> %s (%d)", fkTable, fkRowID, fkParent, fkIdx)
	}
	return nil
}

func TestAcceptanceProviderSaveSwitchRestartAndLocalFixture(t *testing.T) {
	ctx := context.Background()
	dbDir := t.TempDir()
	secDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "providers-acceptance.sqlite")

	var probeCalls int
	localFixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" && r.Method == http.MethodGet {
			probeCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []any{
					map[string]any{"id": "local-sdxl-1"},
					map[string]any{"id": "local-seedance-15s"},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer localFixture.Close()

	db1, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	sec1, err := providers.OpenSecrets(secDir)
	if err != nil {
		db1.Close()
		t.Fatal(err)
	}
	ps1 := providers.NewService(db1, sec1)

	// 1. Save local image & video configs and cloud image & video configs, then select defaults.
	localImg := providers.Config{
		ID:         "local-img",
		Name:       "Local Images Fixture",
		Kind:       "image",
		Protocol:   "openai-images",
		BaseURL:    localFixture.URL + "/v1",
		ModelID:    "local-sdxl-1",
		Enabled:    true,
		Capability: providers.Capability{MaxPromptBytes: 4096, MaxCount: 2, MaxReferences: 2, Edit: true, Sizes: []string{"1024x1024"}},
		Pricing:    &providers.Pricing{Currency: "USD", Unit: "image", UnitAmount: 0, MinorUnitScale: 2, CreditNumerator: 1, CreditDenominator: 1, Source: "local_fixture", Version: "v1"},
	}
	cloudImg := providers.Config{
		ID:         "cloud-img",
		Name:       "Cloud OpenAI Images",
		Kind:       "image",
		Protocol:   "openai-images",
		BaseURL:    "https://api.example.com/v1",
		ModelID:    "gpt-image-1",
		Enabled:    true,
		Headers:    providers.Headers{Organization: "org-alpha", Project: "proj-alpha"},
		Capability: providers.Capability{MaxPromptBytes: 4096, MaxCount: 2, MaxReferences: 2, Edit: true, Sizes: []string{"1024x1024"}},
		Pricing:    &providers.Pricing{Currency: "USD", Unit: "image", UnitAmount: 4, MinorUnitScale: 2, CreditNumerator: 1, CreditDenominator: 1, Source: "declared", Version: "v1"},
	}
	localVid := providers.Config{
		ID:         "local-vid",
		Name:       "Local Video Fixture",
		Kind:       "video",
		Protocol:   "ark-video",
		BaseURL:    localFixture.URL + "/v1",
		ModelID:    "local-seedance-15s",
		Enabled:    true,
		Capability: providers.Capability{MaxPromptBytes: 8192, MaxCount: 1, MaxReferences: 2, Sizes: []string{"1280x720"}, DurationSeconds: []int{5, 10, 15}, Audio: true},
		Pricing:    &providers.Pricing{Currency: "USD", Unit: "second", UnitAmount: 0, MinorUnitScale: 2, CreditNumerator: 1, CreditDenominator: 1, Source: "local_fixture", Version: "v1"},
	}
	cloudVid := providers.Config{
		ID:         "cloud-vid",
		Name:       "Cloud Ark Video",
		Kind:       "video",
		Protocol:   "ark-video",
		BaseURL:    "https://ark.example.com/api/v3",
		ModelID:    "doubao-seedance-1-0-pro",
		Enabled:    true,
		Capability: providers.Capability{MaxPromptBytes: 8192, MaxCount: 1, MaxReferences: 2, Sizes: []string{"1280x720"}, DurationSeconds: []int{5, 10, 15}, Audio: true},
		Pricing:    &providers.Pricing{Currency: "CNY", Unit: "second", UnitAmount: 20, MinorUnitScale: 2, CreditNumerator: 1, CreditDenominator: 1, Source: "declared", Version: "v1"},
	}

	for i, item := range []struct {
		cfg providers.Config
		key string
	}{
		{localImg, "sk-local-img-secret-111"},
		{cloudImg, "sk-cloud-img-secret-222"},
		{localVid, "sk-local-vid-secret-333"},
		{cloudVid, "sk-cloud-vid-secret-444"},
	} {
		raw, _ := json.Marshal(map[string]any{
			"requestId": fmt.Sprintf("save-cfg-%d", i+1),
			"action":    "create",
			"config":    item.cfg,
			"key":       item.key,
		})
		if _, err := ps1.Execute(ctx, raw); err != nil {
			db1.Close()
			t.Fatalf("create config %s: %v", item.cfg.ID, err)
		}
	}

	// Select local defaults first, then switch defaults to cloud
	for i, id := range []string{"local-img", "local-vid", "cloud-img", "cloud-vid"} {
		rawSelect, _ := json.Marshal(map[string]any{
			"requestId": fmt.Sprintf("select-cfg-%d", i+1),
			"action":    "select",
			"id":        id,
		})
		if _, err := ps1.Execute(ctx, rawSelect); err != nil {
			db1.Close()
			t.Fatalf("select %s: %v", id, err)
		}
	}

	// Probe local HTTP fixture (read-only /v1/models check; confirms HTTP path without claiming real local model compatibility)
	rawProbe, _ := json.Marshal(map[string]any{"requestId": "probe-local-img", "action": "probe", "id": "local-img"})
	probeRec, err := ps1.Execute(ctx, rawProbe)
	if err != nil || probeRec.Probe == nil || !probeRec.Probe.LocalValid || !probeRec.Probe.RemoteTested || !probeRec.Probe.ModelsListed || !probeRec.Probe.ModelFound || probeCalls != 1 {
		db1.Close()
		t.Fatalf("expected local fixture probe to succeed with 1 GET /v1/models call: %+v err=%v calls=%d", probeRec, err, probeCalls)
	}
	db1.Close()

	// 2. Reopen SQLite and DPAPI store to verify restart recovery, default switching, and secret redaction.
	db2, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	sec2, err := providers.OpenSecrets(secDir)
	if err != nil {
		t.Fatal(err)
	}
	ps2 := providers.NewService(db2, sec2)
	list, err := ps2.List(ctx)
	if err != nil || len(list) != 4 {
		t.Fatalf("expected 4 providers after restart, got %+v err=%v", list, err)
	}
	defaults := map[string]string{}
	for _, item := range list {
		if !item.HasKey {
			t.Fatalf("expected HasKey=true after restart for %s", item.Config.ID)
		}
		if item.DefaultFor != "" {
			defaults[item.DefaultFor] = item.Config.ID
		}
	}
	if defaults["image"] != "cloud-img" || defaults["video"] != "cloud-vid" {
		t.Fatalf("expected switched defaults cloud-img/cloud-vid after restart, got %+v", defaults)
	}
	rawListBytes, _ := json.Marshal(list)
	for _, secret := range []string{"sk-local-img-secret-111", "sk-cloud-img-secret-222", "sk-local-vid-secret-333", "sk-cloud-vid-secret-444"} {
		if bytes.Contains(rawListBytes, []byte(secret)) {
			t.Fatalf("provider list leaked secret %q", secret)
		}
	}
}

func TestAcceptanceEndToEndNarratedVideoAndLineageExport(t *testing.T) {
	ctx := context.Background()
	f := setupPreflightFixture(t)
	f.tasks.Executor = tasks.ImageExecutor(f.providers, f.docs)

	// Verify the two required methodology skills (口播视频 and 图片标注修复) install, match trigger phrases, and preserve negative boundaries.
	skills, err := agent.InstallSkills(t.TempDir())
	if err != nil {
		t.Fatalf("InstallSkills: %v", err)
	}
	defer skills.Close()
	matchedSkill, err := skills.Match("用这张图做个15秒口播视频配轻快音乐")
	if err != nil || matchedSkill != "口播视频" {
		t.Fatalf("expected trigger phrase to match 口播视频, got %q err=%v", matchedSkill, err)
	}
	oralSkill, err := skills.Get("口播视频")
	if err != nil || !strings.Contains(oralSkill.Body, "## 不用于") || !strings.Contains(oralSkill.Body, "## 确认门禁序列") || !strings.Contains(oralSkill.Body, "## 失败回流") {
		t.Fatalf("unexpected 口播视频 skill structure: %+v err=%v", oralSkill, err)
	}
	repairSkill, err := skills.Get("图片标注修复")
	if err != nil || !strings.Contains(repairSkill.Body, "## 不用于") || !strings.Contains(repairSkill.Body, "## 确认门禁序列") || !strings.Contains(repairSkill.Body, "## 失败回流") {
		t.Fatalf("unexpected 图片标注修复 skill structure: %+v err=%v", repairSkill, err)
	}

	// Create a second isolated project with private assets and a sensitive provider header/key to verify export isolation.
	otherProj, err := (store.Objects{DB: f.db}).CreateProject(ctx, "create-other-private-project", "other-private-project")
	if err != nil {
		t.Fatal(err)
	}
	otherStage, err := f.files.Stage(ctx, otherProj.ID, "stage-other-secret", "other-secret-asset.png", strings.NewReader(string([]byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0x60, 0x60, 0x60, 0x60,
		0x00, 0x00, 0x00, 0x05, 0x00, 0x01, 0xa5, 0xf6,
		0x45, 0x40, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
		0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	})))
	if err != nil {
		t.Fatal(err)
	}
	otherDel, err := f.docs.Deliver(ctx, otherProj.ID, delivery.Request{
		RequestID: "deliver-other-secret",
		Sources:   []delivery.Source{{Key: "secret", Path: otherStage}},
		Output: delivery.Output{
			Resources: []delivery.Resource{{Key: "secret", Mode: "create", Name: "PrivateSecretAsset"}},
			Composition: delivery.Composition{
				Name:       "PrivateComp",
				Type:       delivery.CompositionType{Label: "image"},
				Placements: []delivery.Placement{{Key: "secret", Role: "reference"}},
			},
		},
	})
	if err != nil || otherDel.Delivery.Status != "completed" {
		t.Fatalf("deliver private project asset: %+v %v", otherDel, err)
	}
	excludedAssetID := otherDel.Delivery.Versions[0].AssetID

	// Configure a sensitive OpenAI header and DPAPI key to prove export redaction.
	secretKeyMarker := "sk-secret-do-not-export-9999"
	secretHeaderMarker := "org-secret-header-do-not-leak"
	imgCfgWithHeader := providers.Config{
		ID:         "seedream-secret",
		Name:       "Seedream Secret Header",
		Kind:       "image",
		Protocol:   "openai-images",
		BaseURL:    "https://api.example.com/v1",
		ModelID:    "seedream-3.0",
		Enabled:    true,
		Headers:    providers.Headers{Organization: secretHeaderMarker},
		Capability: providers.Capability{MaxPromptBytes: 4096, MaxCount: 2, MaxReferences: 2, Edit: true, Sizes: []string{"1024x1024"}},
		Pricing:    &providers.Pricing{Currency: "USD", Unit: "image", UnitAmount: 5, MinorUnitScale: 2, CreditNumerator: 1, CreditDenominator: 1, Source: "declared", Version: "v1"},
	}
	rawSecImg, _ := json.Marshal(map[string]any{"requestId": "create-sec-img", "action": "create", "config": imgCfgWithHeader, "key": secretKeyMarker})
	if _, err := f.providers.Execute(ctx, rawSecImg); err != nil {
		t.Fatal(err)
	}

	mp4V1 := providers.BuildTestMP4(1280, 720, 15, true)
	mp4V2 := providers.BuildTestMP4(1280, 720, 15, true)
	var serverURL string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/contents/generations/tasks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "cgt-accept-15s-001", "status": "queued"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/contents/generations/tasks/cgt-accept-15s-001":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "cgt-accept-15s-001",
				"status":  "succeeded",
				"content": map[string]any{"video_url": serverURL + "/cdn/accept-15s.mp4"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/cdn/accept-15s.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(mp4V1)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer remote.Close()
	serverURL = remote.URL

	vidCfg := providers.Config{
		ID:       "seedance",
		Name:     "Seedance Video",
		Kind:     "video",
		Protocol: "ark-video",
		BaseURL:  remote.URL + "/v1",
		ModelID:  "seedance-2.0",
		Enabled:  true,
		Capability: providers.Capability{
			MaxPromptBytes:  8192,
			MaxCount:        1,
			MaxReferences:   2,
			Sizes:           []string{"1280x720"},
			DurationSeconds: []int{5, 10, 15},
			Audio:           true,
		},
		Pricing: &providers.Pricing{
			Currency:          "USD",
			Unit:              "second",
			UnitAmount:        10,
			MinorUnitScale:    2,
			CreditNumerator:   1,
			CreditDenominator: 1,
			Source:            "declared",
			Version:           "v1",
		},
	}
	rawUpd, _ := json.Marshal(map[string]any{"requestId": "upd-seedance-accept", "action": "update", "config": vidCfg, "key": secretKeyMarker})
	if _, err := f.providers.Execute(ctx, rawUpd); err != nil {
		t.Fatal(err)
	}

	// Upload two ordered reference images to the acceptance project and walk through intent -> script -> storyboard -> prompt -> contract -> billing -> 15s MP4 -> revised v2.
	ref1Asset, ref1Ver := f.createRefAsset("accept-ref-1", "HeroProductImage")
	ref2Asset, ref2Ver := f.createRefAsset("accept-ref-2", "DetailTextureImage")

	intentBind, scriptBind := f.upstream()
	sbBind := create15sNarratedStoryboard(f, "accept-board-1")
	f.approve(sbBind)

	exactPrompt := "镜头一 [00:00-00:07] [近景]：用这张图做个15秒口播视频配轻快音乐，主讲人自然展示产品核心卖点。\r\n镜头二 [00:07-00:15] [中景]：镜头慢推特写纹理细节，口播收尾，轻快音乐收束。 "
	promptRec, err := f.writePrompt("accept-prompt-1", 0, "", exactPrompt, &pipeline.ContractDraft{
		CapabilityID: "video:seedance",
		Params:       map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "resolution": "720p", "generate_audio": true, "audio_mode": "narration+music"},
		References:   []string{ref1Asset, ref2Asset},
	})
	if err != nil {
		t.Fatal(err)
	}
	promptVersionID := promptRec.Delivery.Versions[0].VersionID
	promptView, err := pipeline.ReadPromptVersion(ctx, f.db, f.p, promptVersionID)
	if err != nil || promptView.Contract == nil || promptView.Contract.Status != "ready" {
		t.Fatalf("ReadPromptVersion: %+v %v", promptView, err)
	}
	contractID := promptView.Contract.ID

	count := 1
	admitReq := generation.Request{
		RequestID:    "accept-gen-video-1",
		SessionID:    "human-session",
		CapabilityID: "video:seedance",
		ContractID:   &contractID,
		Prompt:       exactPrompt,
		Params:       map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "resolution": "720p", "generate_audio": true, "audio_mode": "narration+music"},
		References:   []string{ref1Asset, ref2Asset},
		Count:        &count,
		Output: delivery.Output{
			Resources: []delivery.Resource{{Key: "oral_video", Mode: "create", Name: "15秒口播配乐成片"}},
			Composition: delivery.Composition{
				Name:       "Film",
				Type:       delivery.CompositionType{Label: "video"},
				Placements: []delivery.Placement{{Key: "oral_video", Role: "video"}},
			},
		},
	}
	admitRaw, _ := json.Marshal(admitReq)
	admitted, err := f.gen.Admit(ctx, f.p, admitReq, admitRaw, false, "")
	if err != nil {
		t.Fatal(err)
	}
	taskID := admitted.Task.ID

	var billRaw string
	if err := f.db.QueryRowContext(ctx, `SELECT question_json FROM questions WHERE task_id=? AND kind='billing' ORDER BY created_at DESC LIMIT 1`, taskID).Scan(&billRaw); err != nil {
		t.Fatal(err)
	}
	var billQ questions.Question
	_ = json.Unmarshal([]byte(billRaw), &billQ)
	if _, err := f.q.Reply(ctx, f.p, billQ.ID, questions.HumanReply{
		SessionID:        "human-session",
		RequestID:        "accept-approve-billing-1",
		ExpectedRevision: billQ.Revision,
		Answers:          [][]string{{billQ.Questions[0].Options[0].Label}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Run(ctx, f.p, taskID); err != nil {
		t.Fatal(err)
	}
	doneTask, err := f.tasks.Query(ctx, f.p, taskID)
	if err != nil || doneTask.Status != tasks.Completed || len(doneTask.Outputs) != 1 || doneTask.Outputs[0].VersionID == nil || doneTask.Outputs[0].AssetID == nil {
		t.Fatalf("expected completed 15s video task, got %+v %v", doneTask, err)
	}
	videoV1 := *doneTask.Outputs[0].VersionID
	videoAssetV1 := *doneTask.Outputs[0].AssetID
	videoResourceID := doneTask.Outputs[0].ResourceID
	taskOutputKey := doneTask.Outputs[0].TaskOutputKey

	// Deliver a revised video version v2 on the same video Resource preserving source_version_id and lineage.
	stageV2, err := f.files.Stage(ctx, f.p, "accept-stage-v2", "revised-15s.mp4", bytes.NewReader(mp4V2))
	if err != nil {
		t.Fatal(err)
	}
	delRes2, err := f.docs.Deliver(ctx, f.p, delivery.Request{
		RequestID: "accept-deliver-v2",
		Sources:   []delivery.Source{{Key: "oral_video", Path: stageV2}},
		Output: delivery.Output{
			Resources: []delivery.Resource{{Key: "oral_video", Mode: "new_version", Name: "15秒口播配乐成片", BaseVersionID: videoV1}},
			Composition: delivery.Composition{
				Name:       "Film",
				Type:       delivery.CompositionType{Label: "video"},
				Placements: []delivery.Placement{{Key: "oral_video", Role: "video"}},
			},
		},
	})
	if err != nil || delRes2.Delivery.Status != "completed" {
		t.Fatalf("deliver revised video v2: %+v %v", delRes2, err)
	}
	videoV2 := delRes2.Delivery.Versions[0].VersionID
	videoAssetV2 := delRes2.Delivery.Versions[0].AssetID

	// Export scoped acceptance DB and verify SQL lineage and redaction.
	exportDir := os.Getenv("MOVIEDESIGN_ACCEPTANCE_EXPORT_DIR")
	if exportDir == "" {
		exportDir = filepath.Join(t.TempDir(), "acceptance-lineage")
	}
	exportDBPath := filepath.Join(exportDir, "acceptance-lineage.sqlite")
	if err := exportScopedProjectAcceptanceDB(ctx, f.db, f.p, exportDBPath); err != nil {
		t.Fatalf("exportScopedProjectAcceptanceDB: %v", err)
	}
	manifest := AcceptanceLineageManifest{
		ProjectID:                  f.p,
		PipelineID:                 "film",
		IntentVersionID:            intentBind.DocumentVersionID,
		ScriptVersionID:            scriptBind.DocumentVersionID,
		StoryboardVersionID:        sbBind.DocumentVersionID,
		PromptVersionID:            promptVersionID,
		ContractID:                 contractID,
		TaskID:                     taskID,
		TaskOutputKey:              taskOutputKey,
		VideoResourceID:            videoResourceID,
		VideoVersionV1ID:           videoV1,
		VideoVersionV2ID:           videoV2,
		VideoAssetV1ID:             videoAssetV1,
		VideoAssetV2ID:             videoAssetV2,
		OrderedReferenceAssetIDs:   []string{ref1Asset, ref2Asset},
		OrderedReferenceVersionIDs: []string{ref1Ver, ref2Ver},
		ExcludedProjectID:          otherProj.ID,
		ExcludedAssetID:            excludedAssetID,
		RedactedMarkers:            []string{secretKeyMarker, secretHeaderMarker, "other-private-project", "other-secret-asset.png"},
	}
	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(exportDir, "acceptance-lineage-manifest.json"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Open exported DB and verify lineage + redaction in Go as well.
	exportedDB, err := store.Open(exportDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer exportedDB.Close()

	lineage, err := pipeline.TraceLineage(ctx, exportedDB, f.p, videoV2)
	if err != nil {
		t.Fatalf("TraceLineage on exported DB failed: %v", err)
	}
	if lineage.Storyboard == nil || lineage.Storyboard.DocumentVersionID != sbBind.DocumentVersionID ||
		lineage.Prompt == nil || lineage.Prompt.DocumentVersionID != promptVersionID ||
		lineage.Contract == nil || lineage.Contract.ID != contractID || len(lineage.Contract.References) != 2 {
		t.Fatalf("unexpected exported lineage: %+v", lineage)
	}
	if lineage.Contract.References[0].AssetID != ref1Asset || lineage.Contract.References[1].AssetID != ref2Asset {
		t.Fatalf("ordered reference asset IDs mismatch in exported lineage: %+v", lineage.Contract.References)
	}

	// Verify generation_inputs ordered rows in exported DB and version_dependencies reference:0 / reference:1
	rows, err := exportedDB.QueryContext(ctx, `SELECT ordinal, asset_id, COALESCE(version_id,'') FROM generation_inputs WHERE task_id = ? ORDER BY ordinal ASC`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var gotAssets []string
	for rows.Next() {
		var ord int
		var aid, vid string
		if err := rows.Scan(&ord, &aid, &vid); err != nil {
			t.Fatal(err)
		}
		gotAssets = append(gotAssets, aid)
	}
	if len(gotAssets) != 2 || gotAssets[0] != ref1Asset || gotAssets[1] != ref2Asset {
		t.Fatalf("generation_inputs ordered references mismatch: assets=%v", gotAssets)
	}
	var depRef0, depRef1 string
	if err := exportedDB.QueryRowContext(ctx, `SELECT source_version_id FROM version_dependencies WHERE version_id=? AND kind='reference:0'`, videoV1).Scan(&depRef0); err != nil || depRef0 != ref1Ver {
		t.Fatalf("expected reference:0 dependency %s on %s, got %q err=%v", ref1Ver, videoV1, depRef0, err)
	}
	if err := exportedDB.QueryRowContext(ctx, `SELECT source_version_id FROM version_dependencies WHERE version_id=? AND kind='reference:1'`, videoV1).Scan(&depRef1); err != nil || depRef1 != ref2Ver {
		t.Fatalf("expected reference:1 dependency %s on %s, got %q err=%v", ref2Ver, videoV1, depRef1, err)
	}

	// Verify zero rows from other-private-project or provider_configs/llm_settings in exported DB
	var leakedCount int
	if err := exportedDB.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM projects WHERE id != ?) + (SELECT COUNT(*) FROM provider_configs) + (SELECT COUNT(*) FROM llm_settings) + (SELECT COUNT(*) FROM assets WHERE id = ?)`, f.p, excludedAssetID).Scan(&leakedCount); err != nil {
		t.Fatal(err)
	}
	if leakedCount != 0 {
		t.Fatalf("expected 0 leaked rows in exported DB, got %d", leakedCount)
	}
	rawDBBytes, err := os.ReadFile(exportDBPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range manifest.RedactedMarkers {
		if bytes.Contains(rawDBBytes, []byte(marker)) {
			t.Fatalf("exported acceptance SQLite file leaked forbidden marker %q", marker)
		}
	}
}

func TestAcceptanceThreeInterruptionDrillsAndIdempotentDelivery(t *testing.T) {
	ctx := context.Background()
	f := setupPreflightFixture(t)
	intentBind, scriptBind := f.upstream()
	sbBind := create15sNarratedStoryboard(f, "drill-board-1")
	f.approve(sbBind)
	ref1, _ := f.createRefAsset("drill-ref-1", "DrillRef1")

	exactPrompt := "镜头一 [00:00-00:07] [近景]：讲述者微笑向镜头介绍产品。\n镜头二 [00:07-00:15] [中景]：随手势拉远展示全景，配乐收束。 "
	promptRec, err := f.writePrompt("drill-prompt-1", 0, "", exactPrompt, &pipeline.ContractDraft{
		CapabilityID: "video:seedance",
		Params:       map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
		References:   []string{ref1},
	})
	if err != nil {
		t.Fatal(err)
	}
	promptV1 := promptRec.Delivery.Versions[0].VersionID
	view1, err := pipeline.ReadPromptVersion(ctx, f.db, f.p, promptV1)
	if err != nil || view1.Contract == nil {
		t.Fatal(err)
	}
	contract1 := view1.Contract.ID

	var (
		mu          sync.Mutex
		postCalls   int
		deleteCalls int
		mode        = "cancel_queued"
		serverURL   string
	)
	validMP4 := providers.BuildTestMP4(1280, 720, 15, true)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		curMode := mode
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/contents/generations/tasks":
			postCalls++
			callNum := postCalls
			mu.Unlock()
			if curMode == "fail_once" && callNum == 2 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{"code": "rate_limit_exceeded", "message": "HTTP 429 rate limit"},
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("cgt-drill-%d", callNum), "status": "queued"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/contents/generations/tasks/"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/contents/generations/tasks/")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if curMode == "cancel_queued" {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "status": "queued"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      id,
				"status":  "succeeded",
				"content": map[string]any{"video_url": serverURL + "/cdn/drill.mp4"},
			})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/contents/generations/tasks/"):
			deleteCalls++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/cdn/drill.mp4":
			mu.Unlock()
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(validMP4)
		default:
			mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer remote.Close()
	serverURL = remote.URL

	vidCfg := providers.Config{
		ID:       "seedance",
		Name:     "Seedance Video",
		Kind:     "video",
		Protocol: "ark-video",
		BaseURL:  remote.URL + "/v1",
		ModelID:  "seedance-2.0",
		Enabled:  true,
		Capability: providers.Capability{
			MaxPromptBytes:  8192,
			MaxCount:        1,
			MaxReferences:   2,
			Sizes:           []string{"1280x720"},
			DurationSeconds: []int{5, 10, 15},
			Audio:           true,
		},
		Pricing: &providers.Pricing{
			Currency:          "USD",
			Unit:              "second",
			UnitAmount:        10,
			MinorUnitScale:    2,
			CreditNumerator:   1,
			CreditDenominator: 1,
			Source:            "declared",
			Version:           "v1",
		},
	}
	rawUpd, _ := json.Marshal(map[string]any{"requestId": "upd-seedance-drill", "action": "update", "config": vidCfg, "key": "sk-drill-key"})
	if _, err := f.providers.Execute(ctx, rawUpd); err != nil {
		t.Fatal(err)
	}
	f.tasks.Executor = tasks.ImageExecutor(f.providers, f.docs)

	admitAndApprove := func(reqID, cid string) string {
		t.Helper()
		count := 1
		req := generation.Request{
			RequestID:    reqID,
			SessionID:    "human-session",
			CapabilityID: "video:seedance",
			ContractID:   &cid,
			Prompt:       exactPrompt,
			Params:       map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
			References:   []string{ref1},
			Count:        &count,
			Output: delivery.Output{
				Resources: []delivery.Resource{{Key: "vid_" + reqID, Mode: "create", Name: "Video-" + reqID}},
				Composition: delivery.Composition{
					Name:       "Film",
					Type:       delivery.CompositionType{Label: "video"},
					Placements: []delivery.Placement{{Key: "vid_" + reqID, Role: "video"}},
				},
			},
		}
		raw, _ := json.Marshal(req)
		adm, err := f.gen.Admit(ctx, f.p, req, raw, false, "")
		if err != nil {
			t.Fatalf("Admit(%s): %v", reqID, err)
		}
		var bRaw string
		if err := f.db.QueryRowContext(ctx, `SELECT question_json FROM questions WHERE task_id=? AND kind='billing' AND state='pending' ORDER BY created_at DESC LIMIT 1`, adm.Task.ID).Scan(&bRaw); err != nil {
			t.Fatal(err)
		}
		var bQ questions.Question
		_ = json.Unmarshal([]byte(bRaw), &bQ)
		if _, err := f.q.Reply(ctx, f.p, bQ.ID, questions.HumanReply{
			SessionID:        "human-session",
			RequestID:        "bill-" + reqID,
			ExpectedRevision: bQ.Revision,
			Answers:          [][]string{{bQ.Questions[0].Options[0].Label}},
		}); err != nil {
			t.Fatal(err)
		}
		return adm.Task.ID
	}

	// Drill 1: Cancel during generation (while upstream is queued -> calls DELETE once and transitions to cancelled)
	taskCancelID := admitAndApprove("drill-cancel-1", contract1)
	runDone := make(chan error, 1)
	go func() {
		runDone <- f.tasks.Run(ctx, f.p, taskCancelID)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var upID sql.NullString
		_ = f.db.QueryRowContext(ctx, `SELECT upstream_id FROM task_attempts WHERE task_id=? AND attempt=1`, taskCancelID).Scan(&upID)
		if upID.Valid && upID.String != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	curCancelView, err := f.tasks.Query(ctx, f.p, taskCancelID)
	if err != nil {
		t.Fatal(err)
	}
	cancelledView, err := f.tasks.Resolve(ctx, f.p, taskCancelID, tasks.ResolveInput{
		RequestID:        "cancel-drill-1",
		Action:           "cancel",
		ExpectedRevision: curCancelView.Revision,
	})
	if err != nil {
		t.Fatalf("Resolve(cancel) failed: %v", err)
	}
	if cancelledView.Status != tasks.Cancelled || cancelledView.Billing.ChargeStatus != "remote_cancelled_queued" {
		t.Fatalf("expected cancelled task with remote_cancelled_queued, got %+v", cancelledView)
	}
	<-runDone
	mu.Lock()
	if deleteCalls != 1 {
		mu.Unlock()
		t.Fatalf("expected 1 DELETE call on queued cancel, got %d", deleteCalls)
	}
	mu.Unlock()

	// Drill 2: User retry after failure (no automatic retry; human Resolve(retry) + new billing card succeeds)
	contractRetry, _, err := pipeline.Compile(ctx, f.db, f.p, pipeline.CompileInput{
		RequestID:       "compile-drill-retry",
		PipelineID:      "film",
		PromptVersionID: promptV1,
		Prompt:          exactPrompt,
		CapabilityID:    "video:seedance",
		References:      []string{ref1},
		Params:          map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	mode = "fail_once"
	mu.Unlock()
	taskRetryID := admitAndApprove("drill-retry-1", contractRetry.ID)
	if err := f.tasks.Run(ctx, f.p, taskRetryID); err != nil {
		t.Fatal(err)
	}
	failedView, err := f.tasks.Wait(ctx, f.p, taskRetryID)
	if err != nil || failedView.Status != tasks.Failed {
		t.Fatalf("expected failed task after HTTP 429, got %+v %v", failedView, err)
	}
	mu.Lock()
	if postCalls != 2 {
		mu.Unlock()
		t.Fatalf("expected exactly 1 POST for attempt 1 (total 2), got %d", postCalls)
	}
	mode = "succeed"
	mu.Unlock()

	retryView, err := f.tasks.Resolve(ctx, f.p, taskRetryID, tasks.ResolveInput{
		RequestID:        "user-retry-drill-1",
		Action:           "retry",
		ExpectedRevision: failedView.Revision,
	})
	if err != nil || retryView.Status != tasks.WaitingUser || retryView.Problem == nil || retryView.Problem.Source != "billing" {
		t.Fatalf("expected retry to enter waiting_user billing, got %+v %v", retryView, err)
	}
	var retryBillRaw string
	if err := f.db.QueryRowContext(ctx, `SELECT question_json FROM questions WHERE task_id=? AND kind='billing' AND state='pending' ORDER BY created_at DESC LIMIT 1`, taskRetryID).Scan(&retryBillRaw); err != nil {
		t.Fatal(err)
	}
	var retryBillQ questions.Question
	_ = json.Unmarshal([]byte(retryBillRaw), &retryBillQ)
	if _, err := f.q.Reply(ctx, f.p, retryBillQ.ID, questions.HumanReply{
		SessionID:        "human-session",
		RequestID:        "approve-retry-drill-bill",
		ExpectedRevision: retryBillQ.Revision,
		Answers:          [][]string{{retryBillQ.Questions[0].Options[0].Label}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Run(ctx, f.p, taskRetryID); err != nil {
		t.Fatal(err)
	}
	completedRetry, err := f.tasks.Wait(ctx, f.p, taskRetryID)
	if err != nil || completedRetry.Status != tasks.Completed {
		t.Fatalf("expected completed task on attempt 2 after user retry, got %+v %v", completedRetry, err)
	}

	// Drill 3: Storyboard edit after approval re-gates downstream while preserving upstream intent/script approvals
	contractBeforeEdit, _, err := pipeline.Compile(ctx, f.db, f.p, pipeline.CompileInput{
		RequestID:       "compile-before-sb-edit",
		PipelineID:      "film",
		PromptVersionID: promptV1,
		Prompt:          exactPrompt,
		CapabilityID:    "video:seedance",
		References:      []string{ref1},
		Params:          map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sbRev2 := f.create("storyboard", "drill-board-rev2", 1, sbBind.DocumentVersionID, "panel")
	if sbRev2.Revision != 2 {
		t.Fatalf("expected storyboard revision 2, got %d", sbRev2.Revision)
	}
	var contractStatus string
	if err := f.db.QueryRowContext(ctx, `SELECT status FROM prompt_contracts WHERE id=?`, contractBeforeEdit.ID).Scan(&contractStatus); err != nil || contractStatus != "invalidated" {
		t.Fatalf("expected unsubmitted contract to be invalidated after storyboard edit, got status=%q err=%v", contractStatus, err)
	}
	var validIntent, validScript, validSB1 int
	_ = f.db.QueryRowContext(ctx, `SELECT valid FROM approvals WHERE pipeline_id='film' AND gate='intent' AND document_version_id=?`, intentBind.DocumentVersionID).Scan(&validIntent)
	_ = f.db.QueryRowContext(ctx, `SELECT valid FROM approvals WHERE pipeline_id='film' AND gate='script' AND document_version_id=?`, scriptBind.DocumentVersionID).Scan(&validScript)
	_ = f.db.QueryRowContext(ctx, `SELECT valid FROM approvals WHERE pipeline_id='film' AND gate='storyboard' AND document_version_id=?`, sbBind.DocumentVersionID).Scan(&validSB1)
	if validIntent != 1 || validScript != 1 || validSB1 != 0 {
		t.Fatalf("directional invalidation mismatch: intent=%d script=%d storyboard_rev1=%d", validIntent, validScript, validSB1)
	}
	if rep, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:      "film",
		PromptVersionID: promptV1,
		CapabilityID:    "video:seedance",
		Params:          map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
		References:      []string{ref1},
	}); err == nil || rep.Passed {
		t.Fatalf("expected preflight to fail before storyboard rev 2 is approved: %+v", rep)
	}

	// Concurrent duplicate Deliver with same requestId -> only ONE Version set; same key different input -> 409; unknown crash replay -> recovers!
	pngBytes := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0x60, 0x60, 0x60, 0x60,
		0x00, 0x00, 0x00, 0x05, 0x00, 0x01, 0xa5, 0xf6,
		0x45, 0x40, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
		0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
	stageCon, err := f.files.Stage(ctx, f.p, "stage-concurrent-del", "concurrent.png", bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	conReq := delivery.Request{
		RequestID: "concurrent-deliver-req-1",
		Sources:   []delivery.Source{{Key: "con_img", Path: stageCon}},
		Output: delivery.Output{
			Resources: []delivery.Resource{{Key: "con_img", Mode: "create", Name: "ConcurrentResource"}},
			Composition: delivery.Composition{
				Name:       "ConcurrentComp",
				Type:       delivery.CompositionType{Label: "image"},
				Placements: []delivery.Placement{{Key: "con_img", Role: "main"}},
			},
		},
	}
	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	versionIDs := make(chan string, workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			res, err := f.docs.Deliver(ctx, f.p, conReq)
			if err == nil && res.Delivery.Status == "completed" && len(res.Delivery.Versions) == 1 {
				versionIDs <- res.Delivery.Versions[0].VersionID
			}
		}()
	}
	wg.Wait()
	close(versionIDs)
	var firstVersionID string
	for vid := range versionIDs {
		if firstVersionID == "" {
			firstVersionID = vid
		} else if vid != firstVersionID {
			t.Fatalf("concurrent Deliver produced mismatched VersionIDs: %s vs %s", firstVersionID, vid)
		}
	}
	if firstVersionID == "" {
		res, err := f.docs.Deliver(ctx, f.p, conReq)
		if err != nil || res.Delivery.Status != "completed" {
			t.Fatalf("replay after concurrent Deliver failed: %+v %v", res, err)
		}
		firstVersionID = res.Delivery.Versions[0].VersionID
	}
	var countVersions int
	if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM versions WHERE resource_id = (SELECT resource_id FROM versions WHERE id = ?)`, firstVersionID).Scan(&countVersions); err != nil || countVersions != 1 {
		t.Fatalf("expected exactly 1 version row after concurrent duplicate Deliver, got %d (err=%v)", countVersions, err)
	}

	// Same requestId with different input -> ErrRequestConflict (mapped to HTTP 409 invalid_input by server)
	conflictReq := conReq
	conflictReq.Output.Resources = []delivery.Resource{{Key: "con_img", Mode: "create", Name: "MutatedResourceName"}}
	if _, err := f.docs.Deliver(ctx, f.p, conflictReq); !errors.Is(err, store.ErrRequestConflict) {
		t.Fatalf("expected ErrRequestConflict (HTTP 409 invalid_input) on idempotency conflict, got %v", err)
	}

	// Unknown crash recovery: simulate interrupted delivery in 'archived' stage with status='unknown' and receipt=NULL, then replay same requestId
	stageCrash, err := f.files.Stage(ctx, f.p, "stage-unknown-crash", "crash.png", bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	crashReq := delivery.Request{
		RequestID: "unknown-crash-deliver-1",
		Sources:   []delivery.Source{{Key: "crash_img", Path: stageCrash}},
		Output: delivery.Output{
			Resources: []delivery.Resource{{Key: "crash_img", Mode: "create", Name: "CrashRecoveryResource"}},
			Composition: delivery.Composition{
				Name:       "ConcurrentComp",
				Type:       delivery.CompositionType{Label: "image"},
				Placements: []delivery.Placement{{Key: "crash_img", Role: "crash"}},
			},
		},
	}
	crashRes1, err := f.docs.Deliver(ctx, f.p, crashReq)
	if err != nil || crashRes1.Delivery.Status != "completed" {
		t.Fatal(err)
	}
	crashVer := crashRes1.Delivery.Versions[0]
	// Rewind DB state to the exact pre-commit 'archived' / 'unknown' crash boundary (preserving reserved IDs in deliveries/delivery_sources)
	for _, stmt := range []struct {
		q    string
		args []any
	}{
		{`DELETE FROM composition_members WHERE resource_id=?`, []any{crashVer.ResourceID}},
		{`DELETE FROM versions WHERE id=?`, []any{crashVer.VersionID}},
		{`DELETE FROM resources WHERE id=?`, []any{crashVer.ResourceID}},
		{`DELETE FROM asset_projects WHERE asset_id=?`, []any{crashVer.AssetID}},
		{`DELETE FROM assets WHERE id=?`, []any{crashVer.AssetID}},
		{`UPDATE deliveries SET status='unknown', stage='archived', receipt=NULL WHERE project_id=? AND request_id=?`, []any{f.p, crashReq.RequestID}},
	} {
		if _, err := f.db.ExecContext(ctx, stmt.q, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	crashReplay, err := f.docs.Deliver(ctx, f.p, crashReq)
	if err != nil || crashReplay.Delivery.Status != "completed" || crashReplay.Delivery.Versions[0].VersionID != crashVer.VersionID {
		t.Fatalf("expected unknown crash replay with same requestId to recover %s, got %+v %v", crashVer.VersionID, crashReplay, err)
	}
}

func TestAcceptanceSecurityBoundaries(t *testing.T) {
	ctx := context.Background()
	f := setupPreflightFixture(t)
	f.upstream()
	sbBind := create15sNarratedStoryboard(f, "sec-board-1")
	f.approve(sbBind)
	ref1, _ := f.createRefAsset("sec-ref-1", "SecRef1")

	// 1. Path traversal & Windows junction escape rejected by workspace
	if _, err := f.ws.OpenRegular(f.p, "../outside.png"); err == nil {
		t.Fatal("expected ../outside.png path traversal to be rejected")
	}
	if _, err := f.ws.OpenRegular(f.p, `..\outside.png`); err == nil {
		t.Fatal("expected ..\\outside.png path traversal to be rejected")
	}
	if runtime.GOOS == "windows" {
		wsRoot := t.TempDir()
		w2, err := workspace.Open(wsRoot)
		if err != nil {
			t.Fatal(err)
		}
		defer w2.Close()
		s2 := workspace.NewService(f.db, w2)
		outsideDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("outside-secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		projFilesDir := filepath.Join(wsRoot, "projects", f.p, "files")
		if err := os.MkdirAll(projFilesDir, 0o755); err != nil {
			t.Fatal(err)
		}
		junctionPath := filepath.Join(projFilesDir, "junction-escape")
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", junctionPath, outsideDir).CombinedOutput(); err == nil {
			defer os.Remove(junctionPath)
			if _, err := s2.OpenContent(ctx, f.p, "files/junction-escape/secret.txt"); err == nil {
				t.Fatalf("expected junction escape OpenContent to be rejected (mklink output: %s)", string(out))
			}
		}
	}

	// 2. Cross-project asset reference & undeclared capability parameter rejected by Preflight
	otherProj, err := (store.Objects{DB: f.db}).CreateProject(ctx, "sec-other-proj", "SecOther")
	if err != nil {
		t.Fatal(err)
	}
	otherStage, err := f.files.Stage(ctx, otherProj.ID, "sec-other-stage", "other.png", strings.NewReader(string([]byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0x60, 0x60, 0x60, 0x60,
		0x00, 0x00, 0x00, 0x05, 0x00, 0x01, 0xa5, 0xf6,
		0x45, 0x40, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
		0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	})))
	if err != nil {
		t.Fatal(err)
	}
	otherDel, err := f.docs.Deliver(ctx, otherProj.ID, delivery.Request{
		RequestID: "sec-deliver-other",
		Sources:   []delivery.Source{{Key: "other", Path: otherStage}},
		Output: delivery.Output{
			Resources: []delivery.Resource{{Key: "other", Mode: "create", Name: "OtherAsset"}},
			Composition: delivery.Composition{
				Name:       "OtherComp",
				Type:       delivery.CompositionType{Label: "image"},
				Placements: []delivery.Placement{{Key: "other", Role: "reference"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	otherAssetID := otherDel.Delivery.Versions[0].AssetID

	promptRec, err := f.writePrompt("sec-prompt-1", 0, "", "镜头一 [00:00-00:07] [近景]：测试。\n镜头二 [00:07-00:15] [中景]：收束。", nil)
	if err != nil {
		t.Fatal(err)
	}
	promptVerID := promptRec.Delivery.Versions[0].VersionID

	// Cross-project reference rejected
	if rep, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:      "film",
		PromptVersionID: promptVerID,
		CapabilityID:    "video:seedance",
		Params:          map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
		References:      []string{otherAssetID},
	}); err == nil || rep.Passed {
		t.Fatalf("expected cross-project reference %s to be rejected by Preflight: %+v", otherAssetID, rep)
	}

	// Undeclared parameter rejected
	if rep, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:      "film",
		PromptVersionID: promptVerID,
		CapabilityID:    "video:seedance",
		Params:          map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music", "undeclared_flag": "evil"},
		References:      []string{ref1},
	}); err == nil || rep.Passed {
		t.Fatalf("expected undeclared parameter to be rejected by Preflight: %+v", rep)
	}

	// 3. Verify opencode configuration & BuildConfig permission rules deny native shell/file tools and only allow design_design_* + question
	repoRoot := filepath.Clean(filepath.Join("..", "..", ".."))
	tmplBytes, err := os.ReadFile(filepath.Join(repoRoot, "agent", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tmplDoc map[string]any
	if err := json.Unmarshal(tmplBytes, &tmplDoc); err != nil {
		t.Fatalf("invalid opencode.json: %v", err)
	}
	perm, _ := tmplDoc["permission"].(map[string]any)
	if perm["*"] != "deny" || perm["question"] != "allow" {
		t.Fatalf("expected opencode.json permission[*]==deny and question==allow, got %+v", perm)
	}
	if _, err := agent.BuildConfig(agent.LLMConfig{}, agent.MCPConnection{
		URL:    "http://127.0.0.1:8001/mcp",
		Origin: "http://tauri.localhost",
		Token:  "test-mcp-token",
		Tools:  []string{"bash"},
	}); err == nil {
		t.Fatal("expected BuildConfig to reject non-design_design_* tool permission 'bash'")
	}
	builtCfgBytes, err := agent.BuildConfig(agent.LLMConfig{}, agent.MCPConnection{
		URL:    "http://127.0.0.1:8001/mcp",
		Origin: "http://tauri.localhost",
		Token:  "test-mcp-token",
		Tools:  []string{"design_design_execute", "design_design_deliver"},
	})
	if err != nil {
		t.Fatalf("BuildConfig with valid design tools failed: %v", err)
	}
	var builtCfg map[string]any
	_ = json.Unmarshal(builtCfgBytes, &builtCfg)
	builtPerm, _ := builtCfg["permission"].(map[string]any)
	if builtPerm["*"] != "deny" || builtPerm["skill"] != "deny" || builtPerm["question"] != "allow" || builtPerm["design_design_execute"] != "allow" {
		t.Fatalf("unexpected built opencode permission map: %+v", builtPerm)
	}

	// 4. Spawn real open-design-gateway process to verify Origin (missing, null, foreign, OPTIONS, MCP) and MCP token enforcement.
	// Note: curl on localhost can spoof Origin headers, so Origin checks prove browser CSRF defense rather than local malicious process isolation.
	gatewayBin := filepath.Join(repoRoot, "desktop", "src-tauri", "binaries", "open-design-gateway-x86_64-pc-windows-msvc.exe")
	if _, err := os.Stat(gatewayBin); err != nil {
		gatewayBin = filepath.Join(t.TempDir(), "open-design-gateway.exe")
		buildCmd := exec.CommandContext(ctx, "go", "build", "-o", gatewayBin, "./cmd/open-design-gateway")
		buildCmd.Dir = filepath.Join(repoRoot, "gateway")
		if out, err := buildCmd.CombinedOutput(); err != nil {
			t.Fatalf("build gateway for security test: %v (%s)", err, string(out))
		}
	}

	cmd := exec.CommandContext(ctx, gatewayBin, "--data-dir", t.TempDir(), "--port", "0", "--origin", "http://tauri.localhost")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":99,"method":"gateway.shutdown"}`+"\n")
		_ = stdin.Close()
		_ = cmd.Wait()
	}()

	reader := bufio.NewReader(stdout)
	if _, err := io.WriteString(stdin, `{"jsonrpc":"2.0","id":1,"method":"gateway.info"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read start response: %v", err)
	}
	var startResp struct {
		Result struct {
			Port int `json:"port"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &startResp); err != nil || startResp.Result.Port <= 0 {
		t.Fatalf("unexpected start response %q: %v", line, err)
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", startResp.Result.Port)

	originCases := []struct {
		name   string
		method string
		path   string
		origin string
	}{
		{"missing_origin_get", http.MethodGet, "/api/health", ""},
		{"null_origin_get", http.MethodGet, "/api/health", "null"},
		{"foreign_origin_post", http.MethodPost, "/api/projects", "https://evil.example"},
		{"foreign_origin_options", http.MethodOptions, "/api/projects", "https://evil.example"},
		{"null_origin_options", http.MethodOptions, "/api/projects", "null"},
		{"missing_origin_mcp", http.MethodPost, "/mcp", ""},
		{"foreign_origin_mcp", http.MethodPost, "/mcp", "https://evil.example"},
	}
	for _, tc := range originCases {
		req, _ := http.NewRequest(tc.method, baseURL+tc.path, strings.NewReader(`{}`))
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: expected 403 Forbidden, got %d", tc.name, resp.StatusCode)
		}
	}

	// Valid Origin on /mcp WITHOUT MCP token must be rejected
	mcpReq, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	mcpReq.Header.Set("Origin", "http://tauri.localhost")
	mcpReq.Header.Set("Content-Type", "application/json")
	mcpResp, err := http.DefaultClient.Do(mcpReq)
	if err != nil {
		t.Fatal(err)
	}
	mcpResp.Body.Close()
	if mcpResp.StatusCode != http.StatusUnauthorized && mcpResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected /mcp with valid Origin but missing MCP token to return 401/403, got %d", mcpResp.StatusCode)
	}
}
