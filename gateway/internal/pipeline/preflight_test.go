package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/generation"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/pipeline"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
)

type preflightFixture struct {
	*approvalFixture
	ws        *workspace.Workspace
	files     *workspace.Service
	providers *providers.Service
	tasks     *tasks.Service
	gen       generation.Service
}

func setupPreflightFixture(t *testing.T) *preflightFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	db, e := store.Open(filepath.Join(root, "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	ws, e := workspace.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ws.Close() })
	files := workspace.NewService(db, ws)
	secrets, e := providers.OpenSecrets(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ps := providers.NewService(db, secrets)
	ps.Files = files

	imgCfg := providers.Config{
		ID:       "seedream",
		Name:     "Seedream Image",
		Kind:     "image",
		Protocol: "openai-images",
		BaseURL:  "https://api.example.com/v1",
		ModelID:  "seedream-3.0",
		Enabled:  true,
		Capability: providers.Capability{
			MaxPromptBytes: 4096,
			MaxCount:       2,
			MaxReferences:  2,
			Edit:           true,
			Sizes:          []string{"1024x1024", "1536x1024", "1024x1536"},
		},
		Pricing: &providers.Pricing{
			Currency:          "USD",
			Unit:              "image",
			UnitAmount:        5,
			MinorUnitScale:    2,
			CreditNumerator:   1,
			CreditDenominator: 1,
			Source:            "declared",
			Version:           "v1",
		},
	}
	rawImg, _ := json.Marshal(map[string]any{"requestId": "create-img", "action": "create", "config": imgCfg})
	if _, e := ps.Execute(ctx, rawImg); e != nil {
		t.Fatal(e)
	}

	vidCfg := providers.Config{
		ID:       "seedance",
		Name:     "Seedance Video",
		Kind:     "video",
		Protocol: "ark-video",
		BaseURL:  "https://api.example.com/v1",
		ModelID:  "seedance-2.0",
		Enabled:  true,
		Capability: providers.Capability{
			MaxPromptBytes:  8192,
			MaxCount:        1,
			MaxReferences:   2,
			Sizes:           []string{"1280x720", "1920x1080", "720x1280"},
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
	rawVid, _ := json.Marshal(map[string]any{"requestId": "create-vid", "action": "create", "config": vidCfg})
	if _, e := ps.Execute(ctx, rawVid); e != nil {
		t.Fatal(e)
	}

	vidSilentCfg := vidCfg
	vidSilentCfg.ID = "seedance-silent"
	vidSilentCfg.Name = "Seedance Silent"
	vidSilentCfg.Capability.Audio = false
	rawSilent, _ := json.Marshal(map[string]any{"requestId": "create-vid-silent", "action": "create", "config": vidSilentCfg})
	if _, e := ps.Execute(ctx, rawSilent); e != nil {
		t.Fatal(e)
	}

	p, e := (store.Objects{DB: db}).CreateProject(ctx, "preflight-project", "Preflight")
	if e != nil {
		t.Fatal(e)
	}
	ev := events.NewService(db)
	docs := delivery.NewService(db, ws, "user")
	ts := tasks.NewService(db, ev, nil)
	t.Cleanup(func() { ts.Close(); ev.Close() })
	qs := questions.NewService(db, ts, ev)
	ts.BillingGate = qs.Billing
	docs.CompleteTask = ts.CompleteDelivery

	af := &approvalFixture{t: t, db: db, docs: docs, q: qs, p: p.ID}
	return &preflightFixture{
		approvalFixture: af,
		ws:              ws,
		files:           files,
		providers:       ps,
		tasks:           ts,
		gen:             generation.Service{Providers: ps, Tasks: ts},
	}
}

func (f *preflightFixture) writePrompt(key string, rev int64, base, content string, draft *pipeline.ContractDraft) (delivery.Receipt, error) {
	d := pipeline.Document{
		Kind:             "prompt",
		PipelineID:       "film",
		ExpectedRevision: rev,
		Content:          content,
		Contract:         draft,
	}
	mode, outmode := "create", "create"
	if base != "" {
		mode, outmode = "replace", "new_version"
	}
	res := map[string]any{"key": "prompt", "mode": outmode, "name": "final_video_prompt.md"}
	if base != "" {
		res["baseVersionId"] = base
	}
	raw, _ := json.Marshal(map[string]any{
		"requestId":     key,
		"mode":          mode,
		"format":        "md",
		"typedDocument": d,
		"output": map[string]any{
			"resources": []any{res},
			"composition": map[string]any{
				"name":       "Film",
				"type":       map[string]any{"label": "video"},
				"placements": []any{map[string]any{"key": "prompt", "role": "prompt"}},
			},
		},
	})
	return f.docs.WriteDocument(context.Background(), f.p, raw)
}

func (f *preflightFixture) createRefAsset(key, name string) (string, string) {
	f.t.Helper()
	// 1x1 valid PNG
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
	stage, err := f.files.Stage(context.Background(), f.p, key, name+".png", strings.NewReader(string(pngBytes)))
	if err != nil {
		f.t.Fatal(err)
	}
	r, err := f.docs.Deliver(context.Background(), f.p, delivery.Request{
		RequestID: "deliver-" + key,
		Sources:   []delivery.Source{{Key: key, Path: stage}},
		Output: delivery.Output{
			Resources: []delivery.Resource{{Key: key, Mode: "create", Name: name}},
			Composition: delivery.Composition{
				Name:       "Refs",
				Type:       delivery.CompositionType{Label: "image"},
				Placements: []delivery.Placement{{Key: key, Role: "reference"}},
			},
		},
	})
	if err != nil || r.Delivery.Status != "completed" {
		f.t.Fatalf("deliver ref %s: %+v %v", key, r, err)
	}
	v := r.Delivery.Versions[0]
	return v.AssetID, v.VersionID
}

func TestPreflightAndPromptBytes(t *testing.T) {
	rawLF := "镜头一 [00:00-00:05] [近景]：雨夜慢推。\n镜头二：切特写。 "
	rawCRLF := "镜头一 [00:00-00:05] [近景]：雨夜慢推。\r\n镜头二：切特写。 "
	if pipeline.PromptDigest(rawLF) == pipeline.PromptDigest(rawCRLF) {
		t.Fatal("PromptDigest normalized LF and CRLF")
	}
	if pipeline.PromptDigest("镜头一") == pipeline.PromptDigest("镜头一 ") {
		t.Fatal("PromptDigest trimmed trailing space")
	}
	doc := pipeline.Document{Kind: "prompt", PipelineID: "film", Content: rawCRLF}
	if got := doc.Render(); got != rawCRLF {
		t.Fatalf("prompt Render mutated exact bytes: got %q want %q", got, rawCRLF)
	}

	f := setupPreflightFixture(t)
	f.upstream()
	if _, e := f.writePrompt("prompt-without-storyboard", 0, "", rawCRLF, nil); e == nil {
		t.Fatal("prompt write without approved storyboard committed")
	}
	sb := f.create("storyboard", "board-1", 0, "", "")
	if _, e := f.writePrompt("prompt-unapproved-storyboard", 0, "", rawCRLF, nil); e == nil {
		t.Fatal("prompt write with unapproved storyboard committed")
	}
	f.approve(sb)

	receipt, err := f.writePrompt("prompt-1", 0, "", rawCRLF, nil)
	if err != nil || receipt.Delivery.Status != "completed" {
		t.Fatalf("writePrompt failed: %+v %v", receipt, err)
	}
	promptVersionID := receipt.Delivery.Versions[0].VersionID
	view, err := pipeline.ReadPromptVersion(context.Background(), f.db, f.p, promptVersionID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Content != rawCRLF || view.PromptSHA256 != pipeline.PromptDigest(rawCRLF) || view.PromptBytes != len(rawCRLF) {
		t.Fatalf("stored prompt bytes mutated: %+v", view)
	}

	// Untyped replacement or patch on registered prompt Resource must be rejected.
	untypedRaw, _ := json.Marshal(map[string]any{
		"requestId": "untyped-prompt-replace",
		"mode":      "replace",
		"format":    "md",
		"content":   "tampered prompt",
		"output": map[string]any{
			"resources": []any{map[string]any{"key": "prompt", "mode": "new_version", "name": "final_video_prompt.md", "baseVersionId": promptVersionID}},
			"composition": map[string]any{
				"name":       "Film",
				"type":       map[string]any{"label": "video"},
				"placements": []any{map[string]any{"key": "prompt", "role": "prompt"}},
			},
		},
	})
	if r, e := f.docs.WriteDocument(context.Background(), f.p, untypedRaw); e == nil && r.Delivery.Status != "blocked" {
		t.Fatalf("untyped prompt replacement accepted: %+v", r)
	}
}

func TestPreflightAllSevenGates(t *testing.T) {
	ctx := context.Background()
	f := setupPreflightFixture(t)
	f.upstream()

	// Create a 15-second storyboard with oral narration + background music.
	board15 := pipeline.Storyboard{
		Panels: []pipeline.Panel{
			{ID: "p1", TimeMS: 0, Framing: "近景", Action: "主角开口讲述", Transition: "切"},
			{ID: "p2", TimeMS: 6000, Framing: "中景", Action: "转身走向窗前", Transition: "慢推"},
		},
		Shots: []pipeline.Shot{
			{ID: "s1", StartMS: 0, EndMS: 6000, PanelIDs: []string{"p1"}, NarrativeRole: "开场", StartState: "静立", EndState: "抬头"},
			{ID: "s2", StartMS: 6000, EndMS: 15000, PanelIDs: []string{"p2"}, NarrativeRole: "收束", StartState: "转身", EndState: "定格"},
		},
		Timeline: pipeline.Timeline{
			DurationMS:  15000,
			ShotIDs:     []string{"s1", "s2"},
			AudioIntent: "15s人声口播+背景配乐",
		},
	}
	rawBoard, _ := json.Marshal(map[string]any{
		"requestId":     "board-15s",
		"mode":          "create",
		"format":        "md",
		"typedDocument": pipeline.Document{Kind: "storyboard", PipelineID: "film", ExpectedRevision: 0, Storyboard: board15},
		"output": map[string]any{
			"resources":   []any{map[string]any{"key": "sb", "mode": "create", "name": "storyboard_review.md"}},
			"composition": map[string]any{"name": "Film", "type": map[string]any{"label": "video"}, "placements": []any{map[string]any{"key": "sb", "role": "storyboard"}}},
		},
	})
	sbReceipt, err := f.docs.WriteDocument(ctx, f.p, rawBoard)
	if err != nil || sbReceipt.Delivery.Status != "completed" {
		t.Fatalf("board-15s: %+v %v", sbReceipt, err)
	}
	sbBind, err := pipeline.BindingForVersion(ctx, f.db, f.p, sbReceipt.Delivery.Versions[0].VersionID)
	if err != nil {
		t.Fatal(err)
	}
	f.approve(sbBind)

	refA, _ := f.createRefAsset("ref-a", "Hero")
	refB, _ := f.createRefAsset("ref-b", "Scene")
	refC, _ := f.createRefAsset("ref-c", "Extra")

	// 1. Placeholder rejection
	placeholderReceipt, err := f.writePrompt("prompt-placeholder", 0, "", "镜头一 [近景]：{{hero_name}} 在雨夜说话，[待填背景细节]。\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:   "film",
		CapabilityID: "video:seedance",
		Params:       map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
	})
	if err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("expected unresolved placeholder error, got %v", err)
	}

	// Replace with clean prompt text (preserving [近景] and [00:00-00:15] normal brackets)
	exactPrompt := "镜头一 [00:00-00:06] [近景]：主角讲述核心台词。\r\n镜头二 [00:06-00:15] [中景]：配乐渐强推至窗前。 "
	cleanReceipt, err := f.writePrompt("prompt-clean", 1, placeholderReceipt.Delivery.Versions[0].VersionID, exactPrompt, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanVersionID := cleanReceipt.Delivery.Versions[0].VersionID

	// 2. Prompt bytes mismatch (\n vs \r\n or trimmed space)
	for _, mutated := range []string{
		strings.ReplaceAll(exactPrompt, "\r\n", "\n"),
		strings.TrimRight(exactPrompt, " "),
	} {
		if _, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
			PipelineID:   "film",
			Prompt:       mutated,
			CapabilityID: "video:seedance",
			Params:       map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
		}); err == nil {
			t.Fatal("mutated prompt bytes accepted by Preflight")
		}
	}

	// 3. Capability parameter rejection (undeclared param)
	if _, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:   "film",
		CapabilityID: "video:seedance",
		Params:       map[string]any{"duration": 15, "undeclared_flag": true, "audio_mode": "narration+music"},
	}); err == nil {
		t.Fatal("undeclared parameter accepted")
	}

	// 4. Reference count limit (3 > MaxReferences=2) & missing reference
	if _, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:   "film",
		CapabilityID: "video:seedance",
		References:   []string{refA, refB, refC},
		Params:       map[string]any{"duration": 15, "audio_mode": "narration+music"},
	}); err == nil {
		t.Fatal("excess reference count accepted")
	}
	if _, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:   "film",
		CapabilityID: "video:seedance",
		References:   []string{"missing-asset-id"},
		Params:       map[string]any{"duration": 15, "audio_mode": "narration+music"},
	}); err == nil {
		t.Fatal("missing reference asset accepted")
	}

	// 5. Duration and aspect ratio mismatch
	if _, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:   "film",
		CapabilityID: "video:seedance",
		Params:       map[string]any{"duration": 10, "audio_mode": "narration+music"},
	}); err == nil {
		t.Fatal("duration mismatch (10s vs 15s storyboard) accepted")
	}
	if _, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:   "film",
		CapabilityID: "video:seedance",
		Params:       map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "9:16", "audio_mode": "narration+music"},
	}); err == nil {
		t.Fatal("size/aspect_ratio mismatch accepted")
	}

	// 6. Audio / 15s oral narration + music checks
	if _, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:   "film",
		CapabilityID: "video:seedance-silent",
		Params:       map[string]any{"duration": 15},
	}); err == nil {
		t.Fatal("silent capability accepted for audio storyboard")
	}
	if _, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:   "film",
		CapabilityID: "video:seedance",
		Params:       map[string]any{"duration": 15, "generate_audio": true},
	}); err == nil {
		t.Fatal("generic generate_audio=true without explicit narration+music contract accepted for 15s oral+music storyboard")
	}
	if _, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:   "film",
		CapabilityID: "video:seedance",
		Params:       map[string]any{"duration": 15, "generate_audio": true, "audio_mode": "music"},
	}); err == nil {
		t.Fatal("music-only audio_mode accepted when narration+music is required")
	}

	// 7. Valid Compile locks exact bytes, references, and audio contract
	contract, report, err := pipeline.Compile(ctx, f.db, f.p, pipeline.CompileInput{
		RequestID:       "compile-15s",
		PipelineID:      "film",
		PromptVersionID: cleanVersionID,
		Prompt:          exactPrompt,
		CapabilityID:    "video:seedance",
		References:      []string{refA, refB},
		Params:          map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
	})
	if err != nil || !report.Passed || contract.Status != "ready" {
		t.Fatalf("Compile failed: %+v %+v %v", contract, report, err)
	}
	if len(contract.References) != 2 || contract.References[0].AssetID != refA || contract.References[1].AssetID != refB || !contract.Audio.RequiresNarration || !contract.Audio.RequiresMusic {
		t.Fatalf("compiled contract metadata mismatch: %+v", contract)
	}
}

func TestContractAdmissionOmitBypassLineageAndWorkerRace(t *testing.T) {
	ctx := context.Background()
	f := setupPreflightFixture(t)
	intentBind, scriptBind := f.upstream()
	sbBind := f.create("storyboard", "board-img", 0, "", "")
	f.approve(sbBind)

	refA, refVerA := f.createRefAsset("ref-1", "RefOne")
	refB, refVerB := f.createRefAsset("ref-2", "RefTwo")

	exactPrompt := "镜头一：雨夜霓虹街道，主角驻足回望。\r\n保持冷色调光影。 "
	promptReceipt, err := f.writePrompt("prompt-img", 0, "", exactPrompt, &pipeline.ContractDraft{
		CapabilityID: "image:seedream",
		Params:       map[string]any{"size": "1024x1024", "quality": "high"},
		References:   []string{refA, refB},
	})
	if err != nil || promptReceipt.Delivery.Status != "completed" {
		t.Fatalf("writePrompt with contract draft: %+v %v", promptReceipt, err)
	}
	promptVersionID := promptReceipt.Delivery.Versions[0].VersionID
	promptView, err := pipeline.ReadPromptVersion(ctx, f.db, f.p, promptVersionID)
	if err != nil || promptView.Contract == nil || promptView.Contract.Status != "ready" {
		t.Fatalf("expected ready contract on prompt version: %+v %v", promptView, err)
	}
	contract := *promptView.Contract

	makeGenReq := func(reqID string, contractID *string, prompt string, refs []string, compName string) (generation.Request, []byte) {
		r := generation.Request{
			RequestID:    reqID,
			SessionID:    "human-session",
			CapabilityID: "image:seedream",
			Prompt:       prompt,
			Params:       map[string]any{"size": "1024x1024", "quality": "high"},
			References:   refs,
			ContractID:   contractID,
			Output: delivery.Output{
				Resources: []delivery.Resource{{Key: "shot", Mode: "create", Name: "ShotOutput"}},
				Composition: delivery.Composition{
					Name:       compName,
					Type:       delivery.CompositionType{Label: "video"},
					Placements: []delivery.Placement{{Key: "shot", Role: "keyframe"}},
				},
			},
		}
		raw, _ := json.Marshal(r)
		return r, raw
	}

	// 1. Omit-bypass prevention: omitting contract_id in a pipeline project is rejected even with a different composition name!
	reqOmit, rawOmit := makeGenReq("omit-bypass", nil, exactPrompt, []string{refA, refB}, "UnrelatedComp")
	if _, err := f.gen.Admit(ctx, f.p, reqOmit, rawOmit, false, ""); err == nil {
		t.Fatal("omitting contractId in pipeline project bypassed admission gate")
	}

	// 2. Modified prompt bytes (\n instead of \r\n) or reordered references ([refB, refA]) rejected!
	cid := contract.ID
	reqCRLF, rawCRLF := makeGenReq("bad-crlf", &cid, strings.ReplaceAll(exactPrompt, "\r\n", "\n"), []string{refA, refB}, "Film")
	if _, err := f.gen.Admit(ctx, f.p, reqCRLF, rawCRLF, false, ""); err == nil {
		t.Fatal("normalized LF prompt bytes accepted against CRLF contract")
	}
	reqOrder, rawOrder := makeGenReq("bad-order", &cid, exactPrompt, []string{refB, refA}, "Film")
	if _, err := f.gen.Admit(ctx, f.p, reqOrder, rawOrder, false, ""); err == nil {
		t.Fatal("reordered references accepted against locked contract")
	}

	// 3. Valid admission & worker claim race before provider submission
	reqRace, rawRace := makeGenReq("gen-race", &cid, exactPrompt, []string{refA, refB}, "Film")
	admittedRace, err := f.gen.Admit(ctx, f.p, reqRace, rawRace, false, "")
	if err != nil {
		t.Fatal(err)
	}
	// Approve billing first so task is Accepted ('approved' attempt state)
	var billingRaw string
	if err := f.db.QueryRow("SELECT question_json FROM questions WHERE task_id=? AND kind='billing'", admittedRace.Task.ID).Scan(&billingRaw); err != nil {
		t.Fatal(err)
	}
	var billingQ questions.Question
	if err := json.Unmarshal([]byte(billingRaw), &billingQ); err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.Reply(ctx, f.p, billingQ.ID, questions.HumanReply{
		SessionID:        "human-session",
		RequestID:        "approve-billing-race",
		ExpectedRevision: billingQ.Revision,
		Answers:          [][]string{{billingQ.Questions[0].Options[0].Label}},
	}); err != nil {
		t.Fatal(err)
	}

	// Now edit storyboard to revision 2 BEFORE worker claims the task!
	sbBind2 := f.create("storyboard", "board-img-rev2", 1, sbBind.DocumentVersionID, "panel")
	submitCalls := 0
	f.tasks.Executor = &tasks.Executor{
		Submit: func(ctx context.Context, a tasks.Attempt, cp func(string) error) (tasks.Outcome, error) {
			submitCalls++
			return tasks.Outcome{Status: tasks.Completed}, nil
		},
	}
	if err := f.tasks.Run(ctx, f.p, admittedRace.Task.ID); err != nil {
		t.Fatal(err)
	}
	if submitCalls != 0 {
		t.Fatalf("stale contract invoked provider Submit %d times", submitCalls)
	}
	staleTask, err := f.tasks.Query(ctx, f.p, admittedRace.Task.ID)
	if err != nil || staleTask.Status != tasks.Failed || staleTask.Problem == nil || staleTask.Problem.Code != "STALE_CREATIVE_APPROVAL" {
		t.Fatalf("expected Failed task with STALE_CREATIVE_APPROVAL, got %+v %v", staleTask, err)
	}

	// 4. Approve storyboard rev 2, write prompt rev 2, compile contract 2, run & deliver media output, and verify full SQL lineage + delete guards!
	f.approve(sbBind2)
	promptReceipt2, err := f.writePrompt("prompt-img-2", 1, promptVersionID, exactPrompt, &pipeline.ContractDraft{
		CapabilityID: "image:seedream",
		Params:       map[string]any{"size": "1024x1024", "quality": "high"},
		References:   []string{refA, refB},
	})
	if err != nil {
		t.Fatal(err)
	}
	promptVersionID2 := promptReceipt2.Delivery.Versions[0].VersionID
	promptView2, err := pipeline.ReadPromptVersion(ctx, f.db, f.p, promptVersionID2)
	if err != nil || promptView2.Contract == nil {
		t.Fatal(err)
	}
	cid2 := promptView2.Contract.ID
	reqGood, rawGood := makeGenReq("gen-good", &cid2, exactPrompt, []string{refA, refB}, "Film")
	admittedGood, err := f.gen.Admit(ctx, f.p, reqGood, rawGood, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow("SELECT question_json FROM questions WHERE task_id=? AND kind='billing'", admittedGood.Task.ID).Scan(&billingRaw); err != nil {
		t.Fatal(err)
	}
	var billingQ2 questions.Question
	if err := json.Unmarshal([]byte(billingRaw), &billingQ2); err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.Reply(ctx, f.p, billingQ2.ID, questions.HumanReply{
		SessionID:        "human-session",
		RequestID:        "approve-billing-good",
		ExpectedRevision: billingQ2.Revision,
		Answers:          [][]string{{billingQ2.Questions[0].Options[0].Label}},
	}); err != nil {
		t.Fatal(err)
	}

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
	f.tasks.Executor = &tasks.Executor{
		Submit: func(ctx context.Context, a tasks.Attempt, cp func(string) error) (tasks.Outcome, error) {
			submitCalls++
			if err := cp("upstream-job-1"); err != nil {
				return tasks.Outcome{}, err
			}
			// While in-flight ('submitted' contract), editing storyboard to rev 3 must NOT invalidate cid2!
			_ = f.create("storyboard", "board-img-rev3", 2, sbBind2.DocumentVersionID, "shot")
			return tasks.Outcome{
				Status:  tasks.Failed,
				Result:  []byte(`{"data":[{"b64_json":"ok"}]}`),
				Problem: &tasks.Problem{Source: "delivery", Code: "RESULT_AWAITING_DELIVERY", Message: "awaiting delivery", Retryable: true},
			}, nil
		},
		Collect: func(ctx context.Context, a tasks.Attempt) (tasks.Outcome, error) {
			var outKey, colKey string
			if err := f.db.QueryRow("SELECT task_output_key,json_extract(source_json,'$.collection_key') FROM task_outputs WHERE task_id=? AND attempt=1", a.TaskID).Scan(&outKey, &colKey); err != nil {
				return tasks.Outcome{}, err
			}
			stage, err := f.files.Stage(ctx, f.p, "out-stage", "shot.png", strings.NewReader(string(pngBytes)))
			if err != nil {
				return tasks.Outcome{}, err
			}
			if _, err := f.db.ExecContext(ctx, "UPDATE task_outputs SET path=?,state='collected' WHERE task_output_key=?", stage, outKey); err != nil {
				return tasks.Outcome{}, err
			}
			rec, err := f.docs.Deliver(ctx, f.p, delivery.Request{
				RequestID: "task-" + colKey,
				Sources:   []delivery.Source{{Key: "shot", TaskOutputKey: outKey}},
				Output:    a.Input.Output,
			})
			if err != nil || rec.Delivery.Status != "completed" {
				t.Fatalf("task delivery failed: %+v (%+v) %v", rec, rec.Delivery.Problem, err)
			}
			return tasks.Outcome{Status: tasks.Completed}, nil
		},
	}
	if err := f.tasks.Run(ctx, f.p, admittedGood.Task.ID); err != nil {
		t.Fatal(err)
	}
	doneTask, err := f.tasks.Query(ctx, f.p, admittedGood.Task.ID)
	if err != nil || doneTask.Status != tasks.Completed || len(doneTask.Outputs) != 1 || doneTask.Outputs[0].VersionID == nil {
		t.Fatalf("expected completed task with delivered output: %+v %v", doneTask, err)
	}
	mediaVersionID := *doneTask.Outputs[0].VersionID

	// Verify end-to-end SQL lineage from delivered media version all the way back to intent!
	lineage, err := pipeline.TraceLineage(ctx, f.db, f.p, mediaVersionID)
	if err != nil {
		t.Fatal(err)
	}
	if lineage.Intent == nil || lineage.Intent.DocumentVersionID != intentBind.DocumentVersionID ||
		lineage.Script == nil || lineage.Script.DocumentVersionID != scriptBind.DocumentVersionID ||
		lineage.Storyboard == nil || lineage.Storyboard.DocumentVersionID != sbBind2.DocumentVersionID ||
		lineage.Prompt == nil || lineage.Prompt.DocumentVersionID != promptVersionID2 ||
		lineage.Contract == nil || lineage.Contract.ID != cid2 || lineage.Contract.Status != "submitted" ||
		len(lineage.Outputs) != 1 || lineage.Outputs[0].VersionID != mediaVersionID || lineage.Outputs[0].SourceVersionID != promptVersionID2 {
		t.Fatalf("incomplete SQL lineage: %+v", lineage)
	}
	if lineage.Prompt.SourceVersionID != sbBind2.DocumentVersionID || lineage.Storyboard.SourceVersionID != scriptBind.DocumentVersionID || lineage.Script.SourceVersionID != intentBind.DocumentVersionID {
		t.Fatalf("broken versions.source_version_id chain: prompt=%+v sb=%+v script=%+v", lineage.Prompt, lineage.Storyboard, lineage.Script)
	}
	hasRef0, hasRef1 := false, false
	for _, dep := range lineage.Dependencies {
		if dep.VersionID == mediaVersionID && dep.SourceVersionID == refVerA && dep.Kind == "reference:0" {
			hasRef0 = true
		}
		if dep.VersionID == mediaVersionID && dep.SourceVersionID == refVerB && dep.Kind == "reference:1" {
			hasRef1 = true
		}
	}
	if !hasRef0 || !hasRef1 {
		t.Fatalf("missing ordered reference dependencies in version_dependencies: %+v", lineage.Dependencies)
	}

	// Verify delete guards return 409 Conflict (protected) instead of 500
	objs := store.Objects{DB: f.db}
	for _, tc := range []struct{ kind, id string }{
		{"versions", promptVersionID2},
		{"versions", sbBind2.DocumentVersionID},
		{"versions", scriptBind.DocumentVersionID},
		{"versions", intentBind.DocumentVersionID},
		{"resources", lineage.Prompt.ResourceID},
		{"assets", refA},
		{"projects", f.p},
	} {
		_, delErr := objs.Mutate(ctx, f.p, tc.kind, store.Mutation{RequestID: "del-" + tc.kind + "-" + tc.id, Action: "delete", ID: tc.id})
		var domErr *store.DomainError
		if !errors.As(delErr, &domErr) || domErr.Status != 409 {
			t.Fatalf("expected 409 Conflict deleting %s/%s, got %v", tc.kind, tc.id, delErr)
		}
	}
}
