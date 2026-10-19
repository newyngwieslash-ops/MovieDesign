package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func validVideoConfig(id, baseURL string) Config {
	return Config{
		ID:       id,
		Name:     "Ark Video " + id,
		Kind:     "video",
		Protocol: "ark-video",
		BaseURL:  baseURL,
		ModelID:  "doubao-seedance-2-0",
		Enabled:  true,
		Headers:  Headers{},
		Capability: Capability{
			MaxPromptBytes:  16384,
			MaxCount:        1,
			MaxReferences:   4,
			Sizes:           []string{"1280x720", "720x1280", "1920x1080", "1080x1920"},
			DurationSeconds: []int{5, 10, 15},
			Audio:           true,
			ReferenceRoles:  []string{"first_frame", "reference_image"},
		},
		Pricing: &Pricing{
			Currency:          "CNY",
			Unit:              "second",
			UnitAmount:        20,
			MinorUnitScale:    2,
			CreditNumerator:   1,
			CreditDenominator: 1,
			Source:            "ark-official",
			Version:           "2026-10",
			EffectiveAt:       1700000000,
		},
	}
}

func TestVideoCapabilityAndPricing(t *testing.T) {
	cfg := validVideoConfig("ark1", "http://127.0.0.1:18080/api/v3")
	cfg.Capability.ReferenceRoles = nil
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("ValidateConfig failed: %v", err)
	}
	entry := VideoCapability(cfg)
	if entry.ID != "video:ark1" || entry.Operation != "video" || entry.Protocol != "ark-video" || entry.KnowledgeCard != "knowledge/providers/ark-video.md" {
		t.Fatalf("unexpected capability entry: %#v", entry)
	}
	if !strings.Contains(VideoKnowledgeCard, "contents/generations/tasks") {
		t.Fatalf("unexpected VideoKnowledgeCard: %s", VideoKnowledgeCard)
	}

	params := map[string]any{
		"duration":       json.Number("15"),
		"aspect_ratio":   "9:16",
		"resolution":     "1080p",
		"size":           "1080x1920",
		"generate_audio": true,
		"audio_mode":     "narration+music",
		"fps":            json.Number("24"),
	}
	in := GenerationInput{
		ProjectID:  "proj-1",
		Prompt:     "15s oral narration prompt",
		Count:      1,
		Params:     params,
		References: []string{},
		Output:     json.RawMessage(`{"mode":"new_resource"}`),
	}
	if err := ValidateVideo(entry, in); err != nil {
		t.Fatalf("ValidateVideo failed: %v", err)
	}

	// Reject aspect_ratio / size mismatch
	badIn := in
	badIn.Params = map[string]any{
		"duration":     json.Number("15"),
		"aspect_ratio": "16:9",
		"size":         "1080x1920",
	}
	if err := ValidateVideo(entry, badIn); err == nil {
		t.Fatal("expected aspect_ratio/size mismatch rejection")
	}

	// Pricing by second: 20 * 15s * 1 = 300
	frozen := FrozenProvider{Config: cfg, CredentialVersion: "vid-key-v1"}
	quoteSec, err := Estimate(in, frozen)
	if err != nil {
		t.Fatalf("Estimate (second) failed: %v", err)
	}
	if quoteSec.EstimatedAmount != 300 || quoteSec.EstimatedCredits != 300 {
		t.Fatalf("expected 300 minor units for 15s video, got %#v", quoteSec)
	}

	// Pricing by video: 500 * 1 = 500
	cfgPerVideo := cfg
	pCopy := *cfg.Pricing
	pCopy.Unit = "video"
	pCopy.UnitAmount = 500
	cfgPerVideo.Pricing = &pCopy
	if err := ValidateConfig(cfgPerVideo); err != nil {
		t.Fatalf("ValidateConfig (unit=video) failed: %v", err)
	}
	quoteVid, err := Estimate(in, FrozenProvider{Config: cfgPerVideo, CredentialVersion: "vid-key-v1"})
	if err != nil {
		t.Fatalf("Estimate (video) failed: %v", err)
	}
	if quoteVid.EstimatedAmount != 500 || quoteVid.EstimatedCredits != 500 {
		t.Fatalf("expected 500 minor units for per-video quote, got %#v", quoteVid)
	}
}

func TestVideoWireOrderedReferencesAndLifecycle(t *testing.T) {
	s, p := imageService(t)
	ctx := context.Background()

	ref1Bytes := tinyPNG(t)
	stage1, err := s.Files.Stage(ctx, p, "vid-ref-stage-1", "ref1.png", bytes.NewReader(ref1Bytes))
	if err != nil {
		t.Fatal(err)
	}
	asset1, err := s.Files.Commit(ctx, p, "vid-ref-commit-1", stage1)
	if err != nil {
		t.Fatal(err)
	}

	stage2, err := s.Files.Stage(ctx, p, "vid-ref-stage-2", "ref2.png", bytes.NewReader(ref1Bytes))
	if err != nil {
		t.Fatal(err)
	}
	asset2, err := s.Files.Commit(ctx, p, "vid-ref-commit-2", stage2)
	if err != nil {
		t.Fatal(err)
	}

	mp4Bytes := BuildTestMP4(1080, 1920, 15, true)

	var (
		mu        sync.Mutex
		postCalls int
		getCalls  int
		dlCalls   int
		serverURL string
	)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/contents/generations/tasks":
			postCalls++
			if r.Header.Get("Authorization") != "Bearer synthetic-ark-secret" {
				t.Errorf("unexpected auth header: %q", r.Header.Get("Authorization"))
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			if body["model"] != "doubao-seedance-2-0" {
				t.Errorf("unexpected model: %v", body["model"])
			}
			if body["duration"] != float64(15) || body["aspect_ratio"] != "9:16" || body["resolution"] != "1080p" || body["size"] != "1080x1920" || body["generate_audio"] != true || body["audio_mode"] != "narration+music" {
				t.Errorf("unexpected top-level params: %#v", body)
			}
			content, ok := body["content"].([]any)
			if !ok || len(content) != 3 {
				t.Fatalf("expected 3 content items (1 text + 2 ordered image_url), got %#v", body["content"])
			}
			first := content[0].(map[string]any)
			if first["type"] != "text" || first["text"] != " 精确锁定的15秒口播提示词\r\n第二行 " {
				t.Errorf("prompt text mutated: %#v", first)
			}
			second := content[1].(map[string]any)
			secondURL := second["image_url"].(map[string]any)["url"].(string)
			wantDataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(ref1Bytes)
			if second["type"] != "image_url" || secondURL != wantDataURL || second["role"] != "first_frame" {
				t.Errorf("unexpected second content item: %#v", second)
			}
			third := content[2].(map[string]any)
			if third["type"] != "image_url" || third["role"] != "reference_image" {
				t.Errorf("unexpected third content item: %#v", third)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "cgt-ark-task-001",
				"status": "queued",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/contents/generations/tasks/cgt-ark-task-001":
			getCalls++
			status := "queued"
			var content map[string]any
			if getCalls == 2 {
				status = "running"
			} else if getCalls >= 3 {
				status = "succeeded"
				content = map[string]any{"video_url": serverURL + "/cdn/output-001.mp4"}
			}
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{"id": "cgt-ark-task-001", "status": status}
			if content != nil {
				resp["content"] = content
			}
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodGet && r.URL.Path == "/cdn/output-001.mp4":
			dlCalls++
			if r.Header.Get("Authorization") != "" {
				t.Errorf("download request leaked Authorization header")
			}
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(mp4Bytes)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer remote.Close()
	serverURL = remote.URL

	cfg := validVideoConfig("ark1", remote.URL+"/api/v3")
	frozen := FrozenProvider{Config: cfg, CredentialVersion: "ark-vid-cred-1"}
	if err := s.Secrets.Put(frozen.CredentialVersion, "synthetic-ark-secret"); err != nil {
		t.Fatal(err)
	}

	cap := VideoCapability(cfg)
	in := GenerationInput{
		ProjectID:  p,
		Prompt:     " 精确锁定的15秒口播提示词\r\n第二行 ",
		Count:      1,
		References: []string{asset1.ID, asset2.ID},
		Params: map[string]any{
			"duration":       json.Number("15"),
			"aspect_ratio":   "9:16",
			"resolution":     "1080p",
			"size":           "1080x1920",
			"generate_audio": true,
			"audio_mode":     "narration+music",
		},
	}
	frozenAssets, err := s.FreezeReferences(ctx, p, in, frozen)
	if err != nil {
		t.Fatalf("FreezeReferences failed: %v", err)
	}
	if len(frozenAssets) != 2 {
		t.Fatalf("expected 2 resolved references, got %d", len(frozenAssets))
	}
	in.Assets = frozenAssets

	upstreamID, err := s.CreateVideoTask(ctx, in, frozen)
	if err != nil {
		t.Fatalf("CreateVideoTask failed: %v", err)
	}
	if upstreamID != "cgt-ark-task-001" {
		t.Fatalf("unexpected upstreamID: %q", upstreamID)
	}

	finalObs, err := s.PollVideoTask(ctx, frozen, upstreamID, nil)
	if err != nil {
		t.Fatalf("PollVideoTask failed: %v", err)
	}
	if finalObs.Status != "succeeded" || finalObs.VideoURL == "" {
		t.Fatalf("unexpected final poll state: %#v", finalObs)
	}
	if postCalls != 1 || getCalls < 3 {
		t.Fatalf("expected 1 POST and >=3 GETs, got post=%d get=%d", postCalls, getCalls)
	}

	paths, err := s.CollectVideos(ctx, p, in, finalObs.Result, frozen, []string{"vid-col-key-1"})
	if err != nil {
		t.Fatalf("CollectVideos failed: %v", err)
	}
	if len(paths) != 1 || dlCalls != 1 {
		t.Fatalf("expected 1 staged path and 1 download, got paths=%v dlCalls=%d", paths, dlCalls)
	}

	// Replay CollectVideos must reuse staged file without re-downloading
	pathsReplay, err := s.CollectVideos(ctx, p, in, finalObs.Result, frozen, []string{"vid-col-key-1"})
	if err != nil || len(pathsReplay) != 1 || pathsReplay[0] != paths[0] || dlCalls != 1 {
		t.Fatalf("CollectVideos replay failed or re-downloaded: err=%v paths=%v dlCalls=%d", err, pathsReplay, dlCalls)
	}

	// Commit staged MP4 through workspace.Service.Commit and verify DB columns width, height, duration_ms, metadata
	committedAsset, err := s.Files.Commit(ctx, p, "commit-video-asset-1", paths[0])
	if err != nil {
		t.Fatalf("Files.Commit failed: %v", err)
	}
	if committedAsset.Width == nil || *committedAsset.Width != 1080 || committedAsset.Height == nil || *committedAsset.Height != 1920 {
		t.Fatalf("unexpected committed video dimensions: width=%v height=%v", committedAsset.Width, committedAsset.Height)
	}
	if committedAsset.DurationMS == nil || *committedAsset.DurationMS != 15000 {
		t.Fatalf("unexpected committed video duration_ms: %v", committedAsset.DurationMS)
	}
	if committedAsset.Metadata == nil || !strings.Contains(*committedAsset.Metadata, `"codec"`) || !strings.Contains(*committedAsset.Metadata, `"timebase"`) {
		t.Fatalf("unexpected committed video metadata: %v", committedAsset.Metadata)
	}

	// Verify audio track enforcement: if generate_audio=true but MP4 has no audio track, validateVideoBytes must reject
	silentMP4 := BuildTestMP4(1080, 1920, 15, false)
	if _, err := validateVideoBytes(ctx, silentMP4, in, cap); err == nil {
		t.Fatal("expected validateVideoBytes to reject silent MP4 when generate_audio=true")
	}
	// Verify duration enforcement: if 5s MP4 is returned for 15s request, validateVideoBytes must reject
	shortMP4 := BuildTestMP4(1080, 1920, 5, true)
	if _, err := validateVideoBytes(ctx, shortMP4, in, cap); err == nil {
		t.Fatal("expected validateVideoBytes to reject 5s MP4 when duration=15")
	}
}

func TestVideoCancelQueuedVsRunningVsTerminal(t *testing.T) {
	s, _ := imageService(t)
	ctx := context.Background()

	var (
		mu            sync.Mutex
		upstreamState = "queued"
		deleteCalls   int
	)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{"id": "cgt-cancel-test", "status": upstreamState}
			if upstreamState == "succeeded" {
				resp["content"] = map[string]any{"video_url": "https://example.com/done.mp4"}
			}
			_ = json.NewEncoder(w).Encode(resp)
		case http.MethodDelete:
			deleteCalls++
			upstreamState = "cancelled"
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer remote.Close()

	cfg := validVideoConfig("ark-cancel", remote.URL+"/api/v3")
	frozen := FrozenProvider{Config: cfg, CredentialVersion: "ark-cancel-key"}
	if err := s.Secrets.Put(frozen.CredentialVersion, "synthetic-cancel-secret"); err != nil {
		t.Fatal(err)
	}

	// 1. When upstream is running, CancelVideoTask must NOT call DELETE and must return RemoteCancelled=false
	mu.Lock()
	upstreamState = "running"
	mu.Unlock()
	stRunning, err := s.CancelVideoTask(ctx, frozen, "cgt-cancel-test")
	if err != nil {
		t.Fatalf("CancelVideoTask (running) returned unexpected error: %v", err)
	}
	if stRunning.RemoteCancelled || stRunning.Status != "running" || deleteCalls != 0 {
		t.Fatalf("expected RemoteCancelled=false, status=running, deleteCalls=0, got %#v deleteCalls=%d", stRunning, deleteCalls)
	}

	// 2. When upstream is succeeded, CancelVideoTask must NOT call DELETE and must preserve the succeeded result
	mu.Lock()
	upstreamState = "succeeded"
	mu.Unlock()
	stSucceeded, err := s.CancelVideoTask(ctx, frozen, "cgt-cancel-test")
	if err != nil {
		t.Fatalf("CancelVideoTask (succeeded) returned unexpected error: %v", err)
	}
	if stSucceeded.Status != "succeeded" || stSucceeded.Result == nil || len(stSucceeded.Result.Data) != 1 || stSucceeded.Result.Data[0].URL != "https://example.com/done.mp4" || deleteCalls != 0 {
		t.Fatalf("unexpected succeeded state or deleteCalls: %#v deleteCalls=%d", stSucceeded, deleteCalls)
	}

	// 3. When upstream is queued, CancelVideoTask calls DELETE and returns RemoteCancelled=true
	mu.Lock()
	upstreamState = "queued"
	mu.Unlock()
	stQueued, err := s.CancelVideoTask(ctx, frozen, "cgt-cancel-test")
	if err != nil {
		t.Fatalf("CancelVideoTask on queued task failed: %v", err)
	}
	if !stQueued.RemoteCancelled || stQueued.Status != "cancelled" || deleteCalls != 1 {
		t.Fatalf("expected RemoteCancelled=true and deleteCalls=1, got %#v deleteCalls=%d", stQueued, deleteCalls)
	}
}
