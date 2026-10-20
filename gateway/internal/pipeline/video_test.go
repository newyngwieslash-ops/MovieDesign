package pipeline_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/generation"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/pipeline"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
)

func create15sNarratedStoryboard(f *preflightFixture, key string) pipeline.Binding {
	f.t.Helper()
	board15 := pipeline.Storyboard{
		Panels: []pipeline.Panel{
			{ID: "p1", TimeMS: 0, Framing: "近景", Action: "主角向镜头口播产品核心卖点", Transition: "切"},
			{ID: "p2", TimeMS: 7000, Framing: "中景", Action: "慢推展示细节与背景音乐收束", Transition: "慢推"},
		},
		Shots: []pipeline.Shot{
			{ID: "s1", StartMS: 0, EndMS: 7000, PanelIDs: []string{"p1"}, NarrativeRole: "开场口播", StartState: "静立", EndState: "手势展开"},
			{ID: "s2", StartMS: 7000, EndMS: 15000, PanelIDs: []string{"p2"}, NarrativeRole: "痛点收束", StartState: "手势展开", EndState: "微笑定格"},
		},
		Timeline: pipeline.Timeline{
			DurationMS:  15000,
			ShotIDs:     []string{"s1", "s2"},
			AudioIntent: "15s人声口播+背景配乐",
		},
	}
	rawBoard, _ := json.Marshal(map[string]any{
		"requestId":     key,
		"mode":          "create",
		"format":        "md",
		"typedDocument": pipeline.Document{Kind: "storyboard", PipelineID: "film", ExpectedRevision: 0, Storyboard: board15},
		"output": map[string]any{
			"resources": []any{map[string]any{"key": "storyboard", "mode": "create", "name": "storyboard"}},
			"composition": map[string]any{
				"name":       "Film",
				"type":       map[string]any{"label": "video"},
				"placements": []any{map[string]any{"key": "storyboard", "role": "storyboard"}},
			},
		},
	})
	r, err := f.docs.WriteDocument(context.Background(), f.p, rawBoard)
	if err != nil || r.Delivery.Status != "completed" {
		f.t.Fatalf("write15sStoryboard(%s): %+v %v", key, r, err)
	}
	b, err := pipeline.BindingForVersion(context.Background(), f.db, f.p, r.Delivery.Versions[0].VersionID)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func TestVideoPreflightGatesBeforeBilling(t *testing.T) {
	ctx := context.Background()
	f := setupPreflightFixture(t)
	f.upstream()
	sb := create15sNarratedStoryboard(f, "vid-board-1")
	f.approve(sb)
	ref1, _ := f.createRefAsset("vid-ref-1", "Ref1")
	ref2, _ := f.createRefAsset("vid-ref-2", "Ref2")
	ref3, _ := f.createRefAsset("vid-ref-3", "Ref3")

	// Register a short-duration video provider (max 10s, no 15s)
	shortVidCfg := providers.Config{
		ID:       "seedance-10s",
		Name:     "Seedance 10s Only",
		Kind:     "video",
		Protocol: "ark-video",
		BaseURL:  "https://api.example.com/v1",
		ModelID:  "seedance-10s",
		Enabled:  true,
		Capability: providers.Capability{
			MaxPromptBytes:  8192,
			MaxCount:        1,
			MaxReferences:   2,
			Sizes:           []string{"1280x720"},
			DurationSeconds: []int{5, 10},
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
	rawShort, _ := json.Marshal(map[string]any{"requestId": "create-vid-short", "action": "create", "config": shortVidCfg})
	if _, err := f.providers.Execute(ctx, rawShort); err != nil {
		t.Fatal(err)
	}

	promptText := "镜头一 [00:00-00:07] [近景]：讲述者微笑向镜头介绍产品。\n镜头二 [00:07-00:15] [中景]：随手势拉远展示全景，配乐收束。 "
	rec, err := f.writePrompt("vid-prompt-1", 0, "", promptText, nil)
	if err != nil {
		t.Fatal(err)
	}
	promptV1 := rec.Delivery.Versions[0].VersionID

	// 1. Silent capability rejected for 15s narration+music storyboard
	repSilent, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:      "film",
		PromptVersionID: promptV1,
		CapabilityID:    "video:seedance-silent",
		Params:          map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
		References:      []string{ref1},
	})
	if err == nil || repSilent.Passed {
		t.Fatalf("expected silent video capability to fail preflight for narration+music storyboard: %+v", repSilent)
	}

	// 2. Max duration < 15s rejected before billing
	repShort, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:      "film",
		PromptVersionID: promptV1,
		CapabilityID:    "video:seedance-10s",
		Params:          map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
		References:      []string{ref1},
	})
	if err == nil || repShort.Passed {
		t.Fatalf("expected 10s-max video capability to fail 15s preflight: %+v", repShort)
	}

	// 3. Reference count > MaxReferences (3 > 2) rejected before billing
	repRefs, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:      "film",
		PromptVersionID: promptV1,
		CapabilityID:    "video:seedance",
		Params:          map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
		References:      []string{ref1, ref2, ref3},
	})
	if err == nil || repRefs.Passed {
		t.Fatalf("expected 3 references on max_references=2 capability to fail preflight: %+v", repRefs)
	}

	// 4. Incompatible audio_mode ("narration" only when storyboard requires narration+music) rejected
	repMode, err := pipeline.Preflight(ctx, f.db, f.p, pipeline.CompileInput{
		PipelineID:      "film",
		PromptVersionID: promptV1,
		CapabilityID:    "video:seedance",
		Params:          map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration"},
		References:      []string{ref1},
	})
	if err == nil || repMode.Passed {
		t.Fatalf("expected audio_mode=narration to fail when storyboard requires narration+music: %+v", repMode)
	}
}

func TestVideoDownloadValidationAndCollectionRetryWithoutResubmit(t *testing.T) {
	ctx := context.Background()
	f := setupPreflightFixture(t)
	f.tasks.Executor = tasks.ImageExecutor(f.providers, f.docs)

	var (
		mu           sync.Mutex
		postCalls    int
		getCalls     int
		dlCalls      int
		downloadMode = "interrupt"
		serverURL    string
	)
	validMP4 := providers.BuildTestMP4(1280, 720, 15, true)
	silentMP4 := providers.BuildTestMP4(1280, 720, 15, false)

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		mode := downloadMode
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/contents/generations/tasks":
			postCalls++
			id := "cgt-dl-test"
			if postCalls > 1 {
				id = "cgt-dl-test-2"
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "status": "queued"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/contents/generations/tasks/"):
			getCalls++
			id := strings.TrimPrefix(r.URL.Path, "/v1/contents/generations/tasks/")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      id,
				"status":  "succeeded",
				"content": map[string]any{"video_url": serverURL + "/cdn/out.mp4"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/cdn/out.mp4":
			dlCalls++
			mu.Unlock()
			switch mode {
			case "interrupt":
				w.Header().Set("Content-Type", "video/mp4")
				w.Header().Set("Content-Length", "999999")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(validMP4[:64])
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				if hj, ok := w.(http.Hijacker); ok {
					conn, _, err := hj.Hijack()
					if err == nil {
						conn.Close()
						return
					}
				}
			case "silent":
				w.Header().Set("Content-Type", "video/mp4")
				_, _ = w.Write(silentMP4)
			case "playlist":
				w.Header().Set("Content-Type", "video/mp4")
				_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\nhttps://evil.example/seg.ts\n"))
			default:
				w.Header().Set("Content-Type", "video/mp4")
				_, _ = w.Write(validMP4)
			}
		default:
			mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer remote.Close()
	serverURL = remote.URL

	// Update seedance provider BaseURL to point to local httptest server
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
	rawUpd, _ := json.Marshal(map[string]any{"requestId": "upd-seedance-local", "action": "update", "config": vidCfg, "key": "synthetic-ark-vid-key"})
	if _, err := f.providers.Execute(ctx, rawUpd); err != nil {
		t.Fatal(err)
	}

	f.upstream()
	sb := create15sNarratedStoryboard(f, "vid-dl-board")
	f.approve(sb)
	ref1, _ := f.createRefAsset("vid-dl-ref", "RefDL")

	promptText := "镜头一 [00:00-00:07] [近景]：讲述者微笑向镜头介绍产品。\n镜头二 [00:07-00:15] [中景]：随手势拉远展示全景，配乐收束。 "
	rec, err := f.writePrompt("vid-dl-prompt", 0, "", promptText, &pipeline.ContractDraft{
		CapabilityID: "video:seedance",
		Params:       map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
		References:   []string{ref1},
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := pipeline.ReadPromptVersion(ctx, f.db, f.p, rec.Delivery.Versions[0].VersionID)
	if err != nil || view.Contract == nil {
		t.Fatalf("ReadPromptVersion failed: %+v %v", view, err)
	}

	// Admit and approve video task
	count := 1
	contractID := view.Contract.ID
	admitReq := generation.Request{
		RequestID:    "gen-vid-dl-1",
		SessionID:    "human-session",
		CapabilityID: "video:seedance",
		ContractID:   &contractID,
		Prompt:       promptText,
		Params:       map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
		References:   []string{ref1},
		Count:        &count,
		Output: delivery.Output{
			Resources: []delivery.Resource{{Key: "main_video", Mode: "create", Name: "口播成片"}},
			Composition: delivery.Composition{
				Name:       "Film",
				Type:       delivery.CompositionType{Label: "video"},
				Placements: []delivery.Placement{{Key: "main_video", Role: "video"}},
			},
		},
	}
	admitRaw, _ := json.Marshal(admitReq)
	admitted, err := f.gen.Admit(ctx, f.p, admitReq, admitRaw, false, "")
	if err != nil {
		t.Fatalf("Admit video failed: %v", err)
	}
	taskID := admitted.Task.ID

	// Approve billing card and run task (download will interrupt mid-stream)
	var billingRaw string
	if err := f.db.QueryRow("SELECT question_json FROM questions WHERE task_id=? AND kind='billing' ORDER BY created_at DESC LIMIT 1", taskID).Scan(&billingRaw); err != nil {
		t.Fatal(err)
	}
	var billQ questions.Question
	if err := json.Unmarshal([]byte(billingRaw), &billQ); err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.Reply(ctx, f.p, billQ.ID, questions.HumanReply{
		SessionID:        "human-session",
		RequestID:        "approve-bill-dl-1",
		ExpectedRevision: billQ.Revision,
		Answers:          [][]string{{billQ.Questions[0].Options[0].Label}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Run(ctx, f.p, taskID); err != nil {
		t.Fatal(err)
	}

	tv, err := f.tasks.Query(ctx, f.p, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if tv.Status != tasks.Failed || tv.Problem == nil || tv.Problem.Source != "delivery" || !tv.Problem.Retryable {
		t.Fatalf("expected retryable delivery failure on interrupted download, got %+v", tv)
	}
	mu.Lock()
	if postCalls != 1 {
		mu.Unlock()
		t.Fatalf("expected 1 POST call, got %d", postCalls)
	}
	downloadMode = "ok"
	mu.Unlock()

	// Retry collection on the SAME attempt without re-POSTing generation!
	tvRetry, err := f.tasks.Resolve(ctx, f.p, taskID, tasks.ResolveInput{
		RequestID:        "retry-vid-dl-1",
		Action:           "retry",
		ExpectedRevision: tv.Revision,
	})
	if err != nil {
		t.Fatalf("Resolve(retry) failed: %v", err)
	}
	if err := f.tasks.Run(ctx, f.p, taskID); err != nil {
		t.Fatalf("Run collection retry failed: %v", err)
	}
	tvDone, err := f.tasks.Query(ctx, f.p, taskID)
	if err != nil || tvDone.Status != tasks.Completed {
		t.Fatalf("expected Completed after collection retry, got %+v (retry view=%+v) err=%v", tvDone, tvRetry, err)
	}
	mu.Lock()
	finalPosts := postCalls
	mu.Unlock()
	if finalPosts != 1 {
		t.Fatalf("collection retry must NEVER re-POST generation: postCalls=%d", finalPosts)
	}

	// Verify silent MP4 and playlist demuxer rejection in CollectVideos
	frozen, err := f.providers.Resolve("seedance")
	if err != nil {
		t.Fatal(err)
	}
	genIn := providers.GenerationInput{
		ProjectID: f.p,
		Prompt:    promptText,
		Count:     1,
		Params:    map[string]any{"duration": json.Number("15"), "generate_audio": true, "audio_mode": "narration+music"},
	}
	mu.Lock()
	downloadMode = "silent"
	mu.Unlock()
	if _, err := f.providers.CollectVideos(ctx, f.p, genIn, providers.ProviderResult{Data: []providers.ProviderImage{{URL: serverURL + "/cdn/out.mp4"}}}, frozen, []string{"silent-col"}); err == nil {
		t.Fatal("expected CollectVideos to reject silent MP4 when generate_audio=true")
	}
	mu.Lock()
	downloadMode = "playlist"
	mu.Unlock()
	if _, err := f.providers.CollectVideos(ctx, f.p, genIn, providers.ProviderResult{Data: []providers.ProviderImage{{URL: serverURL + "/cdn/out.mp4"}}}, frozen, []string{"playlist-col"}); err == nil {
		t.Fatal("expected CollectVideos to reject #EXTM3U playlist")
	}
}

func TestVideoNarrated15sEndToEndDeliveryAndRevisionLineage(t *testing.T) {
	ctx := context.Background()
	f := setupPreflightFixture(t)
	f.tasks.Executor = tasks.ImageExecutor(f.providers, f.docs)

	mp4V1 := providers.BuildTestMP4(1280, 720, 15, true)
	mp4V2 := providers.BuildTestMP4(1280, 720, 15, true)

	var (
		mu        sync.Mutex
		postCalls int
		serverURL string
	)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/contents/generations/tasks":
			postCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "cgt-e2e-001", "status": "queued"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/contents/generations/tasks/cgt-e2e-001":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "cgt-e2e-001",
				"status":  "succeeded",
				"content": map[string]any{"video_url": serverURL + "/cdn/e2e-15s.mp4"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/cdn/e2e-15s.mp4":
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
	rawUpd, _ := json.Marshal(map[string]any{"requestId": "upd-seedance-e2e", "action": "update", "config": vidCfg, "key": "synthetic-e2e-key"})
	if _, err := f.providers.Execute(ctx, rawUpd); err != nil {
		t.Fatal(err)
	}

	intentBind, scriptBind := f.upstream()
	sbBind := create15sNarratedStoryboard(f, "vid-e2e-board")
	f.approve(sbBind)
	ref1, refVer1 := f.createRefAsset("vid-e2e-ref1", "ProductHero")
	ref2, refVer2 := f.createRefAsset("vid-e2e-ref2", "ProductDetail")

	promptText := "镜头一 [00:00-00:07] [近景]：讲述者手持产品向镜头口播核心卖点，轻快背景音乐渐入。\n镜头二 [00:07-00:15] [中景]：镜头慢推展示产品细节，口播收束，音乐自然尾奏。 "
	rec, err := f.writePrompt("vid-e2e-prompt", 0, "", promptText, &pipeline.ContractDraft{
		CapabilityID: "video:seedance",
		Params:       map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "resolution": "720p", "generate_audio": true, "audio_mode": "narration+music"},
		References:   []string{ref1, ref2},
	})
	if err != nil {
		t.Fatal(err)
	}
	promptV1 := rec.Delivery.Versions[0].VersionID
	view, err := pipeline.ReadPromptVersion(ctx, f.db, f.p, promptV1)
	if err != nil || view.Contract == nil {
		t.Fatal(err)
	}

	count := 1
	contractID := view.Contract.ID
	admitReq := generation.Request{
		RequestID:    "gen-vid-e2e-1",
		SessionID:    "human-session",
		CapabilityID: "video:seedance",
		ContractID:   &contractID,
		Prompt:       promptText,
		Params:       map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "resolution": "720p", "generate_audio": true, "audio_mode": "narration+music"},
		References:   []string{ref1, ref2},
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
	var billingRaw string
	if err := f.db.QueryRow("SELECT question_json FROM questions WHERE task_id=? AND kind='billing' ORDER BY created_at DESC LIMIT 1", taskID).Scan(&billingRaw); err != nil {
		t.Fatal(err)
	}
	var billQ questions.Question
	if err := json.Unmarshal([]byte(billingRaw), &billQ); err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.Reply(ctx, f.p, billQ.ID, questions.HumanReply{
		SessionID:        "human-session",
		RequestID:        "approve-bill-e2e",
		ExpectedRevision: billQ.Revision,
		Answers:          [][]string{{billQ.Questions[0].Options[0].Label}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Run(ctx, f.p, taskID); err != nil {
		t.Fatal(err)
	}

	tv, err := f.tasks.Query(ctx, f.p, taskID)
	if err != nil || tv.Status != tasks.Completed || len(tv.Outputs) != 1 || tv.Outputs[0].VersionID == nil || tv.Outputs[0].AssetID == nil {
		t.Fatalf("expected Completed video task with delivered output, got %+v err=%v", tv, err)
	}
	videoV1 := *tv.Outputs[0].VersionID
	videoAsset1 := *tv.Outputs[0].AssetID
	videoResource := tv.Outputs[0].ResourceID

	// Verify asset row stores duration_ms=15000, width=1280, height=720, and metadata with codec/timebase/audioTracks
	var width, height int
	var durationMS int64
	var metadata string
	if err := f.db.QueryRowContext(ctx, `SELECT width, height, duration_ms, metadata FROM assets WHERE id=?`, videoAsset1).Scan(&width, &height, &durationMS, &metadata); err != nil {
		t.Fatal(err)
	}
	if width != 1280 || height != 720 || durationMS != 15000 || !strings.Contains(metadata, `"audioTracks":1`) {
		t.Fatalf("unexpected delivered video asset metadata: %dx%d %dms %s", width, height, durationMS, metadata)
	}

	// Stage and deliver a revised video version (new_version) on the same video Resource, plus test duplicate delivery idempotency
	stageV2, err := f.files.Stage(ctx, f.p, "stage-vid-v2", "revised.mp4", bytes.NewReader(mp4V2))
	if err != nil {
		t.Fatal(err)
	}
	delReq := delivery.Request{
		RequestID: "deliver-vid-v2",
		Sources:   []delivery.Source{{Key: "oral_video", Path: stageV2}},
		Output: delivery.Output{
			Resources: []delivery.Resource{{Key: "oral_video", Mode: "new_version", Name: "15秒口播配乐成片", BaseVersionID: videoV1}},
			Composition: delivery.Composition{
				Name:       "Film",
				Type:       delivery.CompositionType{Label: "video"},
				Placements: []delivery.Placement{{Key: "oral_video", Role: "video"}},
			},
		},
	}
	delRes1, err := f.docs.Deliver(ctx, f.p, delReq)
	if err != nil || delRes1.Delivery.Status != "completed" {
		t.Fatalf("Deliver v2 failed: %+v %v", delRes1, err)
	}
	videoV2 := delRes1.Delivery.Versions[0].VersionID
	if delRes1.Delivery.Versions[0].ResourceID != videoResource || videoV2 == videoV1 {
		t.Fatalf("expected new Version on same video Resource, got %+v", delRes1.Delivery.Versions[0])
	}

	// Replay identical requestId -> returns identical Version without creating a third version
	delResReplay, err := f.docs.Deliver(ctx, f.p, delReq)
	if err != nil || delResReplay.Delivery.Versions[0].VersionID != videoV2 {
		t.Fatalf("expected idempotent replay to return %s, got %+v %v", videoV2, delResReplay, err)
	}
	// Same requestId with different input -> 409 Conflict
	conflictReq := delReq
	conflictReq.Output.Resources = []delivery.Resource{{Key: "oral_video", Mode: "new_version", Name: "改名冲突", BaseVersionID: videoV1}}
	if _, err := f.docs.Deliver(ctx, f.p, conflictReq); err == nil {
		t.Fatal("expected same requestId with modified input to conflict")
	}

	// Trace full SQL lineage from videoV2 all the way back to intent/script/storyboard/prompt/contract + ordered references
	lineage, err := pipeline.TraceLineage(ctx, f.db, f.p, videoV2)
	if err != nil {
		t.Fatalf("TraceLineage failed: %v", err)
	}
	if lineage.Intent == nil || lineage.Intent.DocumentVersionID != intentBind.DocumentVersionID {
		t.Fatalf("missing intent in lineage: %+v", lineage.Intent)
	}
	if lineage.Script == nil || lineage.Script.DocumentVersionID != scriptBind.DocumentVersionID {
		t.Fatalf("missing script in lineage: %+v", lineage.Script)
	}
	if lineage.Storyboard == nil || lineage.Storyboard.DocumentVersionID != sbBind.DocumentVersionID {
		t.Fatalf("missing storyboard in lineage: %+v", lineage.Storyboard)
	}
	if lineage.Prompt == nil || lineage.Prompt.DocumentVersionID != promptV1 {
		t.Fatalf("missing prompt in lineage: %+v", lineage.Prompt)
	}
	if lineage.Contract == nil || lineage.Contract.ID != view.Contract.ID || len(lineage.Contract.References) != 2 {
		t.Fatalf("missing contract or ordered references in lineage: %+v", lineage.Contract)
	}
	if lineage.Contract.References[0].AssetID != ref1 || lineage.Contract.References[0].VersionID != refVer1 || lineage.Contract.References[1].AssetID != ref2 || lineage.Contract.References[1].VersionID != refVer2 {
		t.Fatalf("reference ordering mismatch in lineage: %+v", lineage.Contract.References)
	}
	if len(lineage.Outputs) < 2 {
		t.Fatalf("expected both v1 and v2 media versions in lineage outputs, got %+v", lineage.Outputs)
	}
}
