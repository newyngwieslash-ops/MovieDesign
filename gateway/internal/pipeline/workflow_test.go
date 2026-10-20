package pipeline_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
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

func TestClassifyFailureTableDriven(t *testing.T) {
	cases := []struct {
		name        string
		problem     pipeline.FailureProblem
		hasPipeline bool
		wantStage   pipeline.FailureStage
		wantRec     string
		wantActions []string
	}{
		{
			name:        "parameter_error_code",
			problem:     pipeline.FailureProblem{Status: "failed", Source: "provider", Code: "INVALID_PARAMETER", Message: "duration out of range", Retryable: true},
			hasPipeline: true,
			wantStage:   pipeline.FailureParameter,
			wantRec:     "modify_params",
			wantActions: []string{"modify_params", "modify_storyboard"},
		},
		{
			name:        "parameter_upstream_code",
			problem:     pipeline.FailureProblem{Status: "failed", Source: "provider", Code: "HTTP_400", UpstreamCode: "unsupported_resolution", Message: "HTTP 400: unsupported resolution", Retryable: true},
			hasPipeline: true,
			wantStage:   pipeline.FailureParameter,
			wantRec:     "modify_params",
			wantActions: []string{"modify_params", "modify_storyboard"},
		},
		{
			name:        "semantic_stale_approval",
			problem:     pipeline.FailureProblem{Status: "failed", Source: "pipeline", Code: "STALE_CREATIVE_APPROVAL", Message: "storyboard revision changed", Retryable: false},
			hasPipeline: true,
			wantStage:   pipeline.FailureSemantic,
			wantRec:     "modify_storyboard",
			wantActions: []string{"modify_storyboard"},
		},
		{
			name:        "semantic_content_policy",
			problem:     pipeline.FailureProblem{Status: "failed", Source: "provider", Code: "HTTP_400", UpstreamCode: "content_policy_violation", Message: "HTTP 400: rejected by safety policy", Retryable: true},
			hasPipeline: true,
			wantStage:   pipeline.FailureSemantic,
			wantRec:     "modify_storyboard",
			wantActions: []string{"modify_storyboard"},
		},
		{
			name:        "upstream_temp_failure_pipeline",
			problem:     pipeline.FailureProblem{Status: "failed", Source: "provider", Code: "PROVIDER_RATE_LIMITED", UpstreamCode: "rate_limit_exceeded", Message: "HTTP 429: rate limit", Retryable: true},
			hasPipeline: true,
			wantStage:   pipeline.FailureUpstream,
			wantRec:     "retry",
			wantActions: []string{"retry", "modify_params", "modify_storyboard"},
		},
		{
			name:        "upstream_delivery_collection",
			problem:     pipeline.FailureProblem{Status: "failed", Source: "delivery", Code: "COLLECTION_INTERRUPTED", Message: "retry collection", Retryable: true},
			hasPipeline: true,
			wantStage:   pipeline.FailureUpstream,
			wantRec:     "retry",
			wantActions: []string{"retry"},
		},
		{
			name:        "unknown_outcome",
			problem:     pipeline.FailureProblem{Status: "unknown", Source: "provider", Code: "PROVIDER_OUTCOME_UNKNOWN", Message: "Submission outcome is unknown; do not resubmit", Retryable: false},
			hasPipeline: true,
			wantStage:   pipeline.FailureUnknown,
			wantRec:     "query",
			wantActions: []string{"query", "cancel"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStage := pipeline.ClassifyFailure(tc.problem)
			if gotStage != tc.wantStage {
				t.Fatalf("ClassifyFailure(%+v) = %q, want %q", tc.problem, gotStage, tc.wantStage)
			}
			retryable := tc.problem.Retryable && gotStage == pipeline.FailureUpstream
			gotRec := pipeline.RecommendedAction(gotStage, retryable)
			if gotRec != tc.wantRec {
				t.Fatalf("RecommendedAction(%q, %v) = %q, want %q", gotStage, retryable, gotRec, tc.wantRec)
			}
			gotActions := pipeline.RecoveryActions(gotStage, tc.hasPipeline, tc.problem.Source, retryable)
			if !slices.Equal(gotActions, tc.wantActions) {
				t.Fatalf("RecoveryActions = %v, want %v", gotActions, tc.wantActions)
			}
		})
	}
}

func TestFailureReturnPathsPreserveRevisionsAndReceipts(t *testing.T) {
	ctx := context.Background()
	f := setupPreflightFixture(t)
	intentBind, scriptBind := f.upstream()
	sbBind := f.create("storyboard", "board-rec-1", 0, "", "")
	f.approve(sbBind)
	refA, _ := f.createRefAsset("ref-rec-a", "RefRecA")

	exactPrompt := "镜头一 [00:00-00:07] [近景]：讲述者微笑展示产品。\n镜头二 [00:07-00:15] [中景]：随手势拉远收束。 "
	promptRec, err := f.writePrompt("prompt-rec-1", 0, "", exactPrompt, &pipeline.ContractDraft{
		CapabilityID: "image:seedream",
		Params:       map[string]any{"size": "1024x1024", "quality": "high"},
		References:   []string{refA},
	})
	if err != nil {
		t.Fatal(err)
	}
	promptVersionID := promptRec.Delivery.Versions[0].VersionID
	promptView, err := pipeline.ReadPromptVersion(ctx, f.db, f.p, promptVersionID)
	if err != nil || promptView.Contract == nil {
		t.Fatal(err)
	}
	contract1 := *promptView.Contract

	submitAndApproveBilling := func(reqID string, cid string, params map[string]any) tasks.TaskView {
		t.Helper()
		r := generation.Request{
			RequestID:    reqID,
			SessionID:    "human-session",
			CapabilityID: "image:seedream",
			Prompt:       exactPrompt,
			Params:       params,
			References:   []string{refA},
			ContractID:   &cid,
			Output: delivery.Output{
				Resources: []delivery.Resource{{Key: "shot", Mode: "create", Name: "Shot-" + reqID}},
				Composition: delivery.Composition{
					Name:       "Film",
					Type:       delivery.CompositionType{Label: "video"},
					Placements: []delivery.Placement{{Key: "shot", Role: "keyframe"}},
				},
			},
		}
		raw, _ := json.Marshal(r)
		admitted, err := f.gen.Admit(ctx, f.p, r, raw, false, "")
		if err != nil {
			t.Fatalf("Admit(%s): %v", reqID, err)
		}
		var billingRaw string
		if err := f.db.QueryRow("SELECT question_json FROM questions WHERE task_id=? AND kind='billing' ORDER BY created_at DESC LIMIT 1", admitted.Task.ID).Scan(&billingRaw); err != nil {
			t.Fatal(err)
		}
		var billingQ questions.Question
		if err := json.Unmarshal([]byte(billingRaw), &billingQ); err != nil {
			t.Fatal(err)
		}
		if _, err := f.q.Reply(ctx, f.p, billingQ.ID, questions.HumanReply{
			SessionID:        "human-session",
			RequestID:        "bill-" + reqID,
			ExpectedRevision: billingQ.Revision,
			Answers:          [][]string{{billingQ.Questions[0].Options[0].Label}},
		}); err != nil {
			t.Fatal(err)
		}
		tv, err := f.tasks.Query(ctx, f.p, admitted.Task.ID)
		if err != nil {
			t.Fatal(err)
		}
		return tv
	}

	// 1. Parameter failure: does NOT change storyboard revision or invalidate storyboard approval;
	// human chooses modify_params -> compiles a new PromptContract on the same storyboard revision 1!
	taskParam := submitAndApproveBilling("task-param", contract1.ID, map[string]any{"size": "1024x1024", "quality": "high"})
	paramPosts := 0
	f.tasks.Executor = &tasks.Executor{
		Submit: func(ctx context.Context, a tasks.Attempt, cp func(string) error) (tasks.Outcome, error) {
			paramPosts++
			return tasks.Outcome{
				Status:      tasks.Failed,
				NotAccepted: true,
				Problem: &tasks.Problem{
					Source:    "provider",
					Code:      "INVALID_PARAMETER",
					Message:   "HTTP 400: quality high rejected for current tier",
					Upstream:  &tasks.Upstream{Code: "invalid_parameter", Message: "quality high rejected"},
					Retryable: true,
				},
			}, nil
		},
	}
	if err := f.tasks.Run(ctx, f.p, taskParam.ID); err != nil {
		t.Fatal(err)
	}
	if paramPosts != 1 {
		t.Fatalf("expected exactly 1 submit call without auto-retry, got %d", paramPosts)
	}
	failedParam, err := f.tasks.Query(ctx, f.p, taskParam.ID)
	if err != nil || failedParam.Status != tasks.Failed || failedParam.Recovery == nil || failedParam.Recovery.Stage != pipeline.FailureParameter {
		t.Fatalf("expected parameter recovery stage, got %+v %v", failedParam, err)
	}
	if !slices.Equal(failedParam.Actions, []string{"modify_params", "modify_storyboard"}) {
		t.Fatalf("expected actions [modify_params modify_storyboard], got %v", failedParam.Actions)
	}
	// Attempting verbatim retry on a parameter failure is rejected
	if _, err := f.tasks.Resolve(ctx, f.p, failedParam.ID, tasks.ResolveInput{
		RequestID:        "illegal-retry-param",
		Action:           "retry",
		ExpectedRevision: failedParam.Revision,
	}); err == nil {
		t.Fatal("expected verbatim retry to be rejected for parameter failure")
	}

	resolvedParam, err := f.tasks.Resolve(ctx, f.p, failedParam.ID, tasks.ResolveInput{
		RequestID:        "resolve-param-1",
		Action:           "modify_params",
		ExpectedRevision: failedParam.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolvedParam.Recovery == nil || resolvedParam.Recovery.SelectedAction != "modify_params" || resolvedParam.Recovery.RequestID != "resolve-param-1" || resolvedParam.Recovery.FromTaskID != taskParam.ID || resolvedParam.Recovery.FromContractID != contract1.ID {
		t.Fatalf("missing recovery receipt metadata on resolvedParam: %+v", resolvedParam.Recovery)
	}
	// Verify storyboard is still at revision 1 and its approval is still valid=1!
	sbViewAfterParam, err := pipeline.ReadVersion(ctx, f.db, f.p, sbBind.DocumentVersionID)
	if err != nil || sbViewAfterParam.Revision != 1 || sbViewAfterParam.CurrentRevision != 1 || sbViewAfterParam.ApprovalStatus != "approved" {
		t.Fatalf("parameter failure recovery must preserve storyboard revision 1 and approval: %+v %v", sbViewAfterParam, err)
	}
	// Re-compile a new PromptContract with adjusted parameter (quality: medium) on the same storyboard revision 1
	contract2, report2, err := pipeline.Compile(ctx, f.db, f.p, pipeline.CompileInput{
		RequestID:       "compile-after-param",
		PipelineID:      "film",
		PromptVersionID: promptVersionID,
		Prompt:          exactPrompt,
		CapabilityID:    "image:seedream",
		References:      []string{refA},
		Params:          map[string]any{"size": "1024x1024", "quality": "medium"},
	})
	if err != nil || !report2.Passed || contract2.ID == contract1.ID || contract2.StoryboardRevision != 1 || contract2.ApprovalID != contract1.ApprovalID || contract2.QuestionReceiptID != contract1.QuestionReceiptID {
		t.Fatalf("expected new contract on same storyboard rev 1 & approval, got %+v %v", contract2, err)
	}
	if contract2.Recovery == nil || contract2.Recovery.FromTaskID != taskParam.ID || contract2.Recovery.FromContractID != contract1.ID || contract2.Recovery.RequestID != "compile-after-param" {
		t.Fatalf("expected new contract to preserve recovery link to old task/contract, got %+v", contract2.Recovery)
	}

	// 2. Upstream temporary failure: never auto-retries; human "retry" replays exact frozen input and requires new billing confirmation!
	taskUpstream := submitAndApproveBilling("task-upstream", contract2.ID, map[string]any{"size": "1024x1024", "quality": "medium"})
	upstreamPosts := 0
	f.tasks.Executor = &tasks.Executor{
		Submit: func(ctx context.Context, a tasks.Attempt, cp func(string) error) (tasks.Outcome, error) {
			upstreamPosts++
			return tasks.Outcome{
				Status:      tasks.Failed,
				NotAccepted: true,
				Problem: &tasks.Problem{
					Source:    "provider",
					Code:      "PROVIDER_RATE_LIMITED",
					Message:   "HTTP 429: rate limit exceeded",
					Upstream:  &tasks.Upstream{Code: "rate_limit_exceeded", Message: "rate limit"},
					Retryable: true,
				},
			}, nil
		},
	}
	if err := f.tasks.Run(ctx, f.p, taskUpstream.ID); err != nil {
		t.Fatal(err)
	}
	if upstreamPosts != 1 {
		t.Fatalf("upstream failure must not auto-retry, got %d posts", upstreamPosts)
	}
	failedUpstream, err := f.tasks.Query(ctx, f.p, taskUpstream.ID)
	if err != nil || failedUpstream.Status != tasks.Failed || failedUpstream.Recovery == nil || failedUpstream.Recovery.Stage != pipeline.FailureUpstream {
		t.Fatalf("expected upstream recovery stage, got %+v %v", failedUpstream, err)
	}
	if !slices.Equal(failedUpstream.Actions, []string{"retry", "modify_params", "modify_storyboard"}) {
		t.Fatalf("expected [retry modify_params modify_storyboard], got %v", failedUpstream.Actions)
	}
	retriedUpstream, err := f.tasks.Resolve(ctx, f.p, failedUpstream.ID, tasks.ResolveInput{
		RequestID:        "retry-upstream-1",
		Action:           "retry",
		ExpectedRevision: failedUpstream.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if retriedUpstream.Status != tasks.WaitingUser || retriedUpstream.Problem == nil || retriedUpstream.Problem.Source != "billing" || retriedUpstream.Prompt != exactPrompt || retriedUpstream.Recovery == nil || retriedUpstream.Recovery.SelectedAction != "retry" {
		t.Fatalf("expected retry to enter waiting_user billing with frozen prompt and recovery receipt, got %+v", retriedUpstream)
	}

	// 3. Unknown acceptance: never enters generator; rejects retry; "query" reconciles read-only!
	var retryBillRaw string
	if err := f.db.QueryRow("SELECT question_json FROM questions WHERE task_id=? AND kind='billing' AND state='pending' ORDER BY created_at DESC LIMIT 1", retriedUpstream.ID).Scan(&retryBillRaw); err != nil {
		t.Fatal(err)
	}
	var retryBillQ questions.Question
	_ = json.Unmarshal([]byte(retryBillRaw), &retryBillQ)
	if _, err := f.q.Reply(ctx, f.p, retryBillQ.ID, questions.HumanReply{
		SessionID:        "human-session",
		RequestID:        "approve-retry-bill",
		ExpectedRevision: retryBillQ.Revision,
		Answers:          [][]string{{retryBillQ.Questions[0].Options[0].Label}},
	}); err != nil {
		t.Fatal(err)
	}
	queryCalls := 0
	f.tasks.Executor = &tasks.Executor{
		Submit: func(ctx context.Context, a tasks.Attempt, cp func(string) error) (tasks.Outcome, error) {
			upstreamPosts++
			if err := cp("upstream-job-unknown-1"); err != nil {
				return tasks.Outcome{}, err
			}
			return tasks.Outcome{}, errors.New("network dropped after checkpoint")
		},
		Query: func(ctx context.Context, a tasks.Attempt) (tasks.Outcome, error) {
			queryCalls++
			return tasks.Outcome{
				Status: tasks.Failed,
				Problem: &tasks.Problem{
					Source:    "provider",
					Code:      "CONTENT_POLICY_REJECTED",
					Message:   "Storyboard narrative rejected by content policy",
					Upstream:  &tasks.Upstream{Code: "content_policy_violation", Message: "rejected"},
					Retryable: false,
				},
			}, nil
		},
	}
	if err := f.tasks.Run(ctx, f.p, retriedUpstream.ID); err != nil {
		t.Fatal(err)
	}
	unknownTask, err := f.tasks.Query(ctx, f.p, retriedUpstream.ID)
	if err != nil || unknownTask.Status != tasks.Unknown || unknownTask.Recovery == nil || unknownTask.Recovery.Stage != pipeline.FailureUnknown {
		t.Fatalf("expected unknown task recovery stage, got %+v %v", unknownTask, err)
	}
	if !slices.Equal(unknownTask.Actions, []string{"query", "cancel"}) {
		t.Fatalf("expected [query cancel] for unknown task, got %v", unknownTask.Actions)
	}
	if _, err := f.tasks.Resolve(ctx, f.p, unknownTask.ID, tasks.ResolveInput{
		RequestID:        "illegal-retry-unknown",
		Action:           "retry",
		ExpectedRevision: unknownTask.Revision,
	}); err == nil {
		t.Fatal("unknown task must reject retry")
	}
	postsBeforeQuery := upstreamPosts
	reconciledTask, err := f.tasks.Resolve(ctx, f.p, unknownTask.ID, tasks.ResolveInput{
		RequestID:        "query-unknown-1",
		Action:           "query",
		ExpectedRevision: unknownTask.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if upstreamPosts != postsBeforeQuery || queryCalls != 1 {
		t.Fatalf("query must never call Submit (posts=%d->%d, queryCalls=%d)", postsBeforeQuery, upstreamPosts, queryCalls)
	}
	if reconciledTask.Status != tasks.Failed || reconciledTask.Recovery == nil || reconciledTask.Recovery.Stage != pipeline.FailureSemantic || !slices.Equal(reconciledTask.Actions, []string{"modify_storyboard"}) {
		t.Fatalf("expected reconciled semantic failure with [modify_storyboard], got %+v", reconciledTask)
	}

	// 4. Semantic failure: human chooses modify_storyboard -> edits storyboard to revision 2 -> invalidates storyboard & prompt approvals while keeping intent & script valid!
	resolvedSemantic, err := f.tasks.Resolve(ctx, f.p, reconciledTask.ID, tasks.ResolveInput{
		RequestID:        "resolve-semantic-1",
		Action:           "modify_storyboard",
		ExpectedRevision: reconciledTask.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolvedSemantic.Recovery == nil || resolvedSemantic.Recovery.SelectedAction != "modify_storyboard" || resolvedSemantic.Recovery.TargetStage != "semantic" {
		t.Fatalf("expected semantic recovery state, got %+v", resolvedSemantic.Recovery)
	}
	sbViewBeforeEdit, err := pipeline.ReadVersion(ctx, f.db, f.p, sbBind.DocumentVersionID)
	if err != nil || sbViewBeforeEdit.Recovery == nil || sbViewBeforeEdit.Recovery.SelectedAction != "modify_storyboard" {
		t.Fatalf("expected ReadVersion to surface active semantic recovery state, got %+v %v", sbViewBeforeEdit, err)
	}

	sbBind2 := f.create("storyboard", "board-rec-2", 1, sbBind.DocumentVersionID, "panel")
	if sbBind2.Revision != 2 {
		t.Fatalf("expected storyboard revision 2, got %d", sbBind2.Revision)
	}
	lineageAfterEdit, err := pipeline.TraceLineage(ctx, f.db, f.p, sbBind2.DocumentVersionID)
	if err != nil {
		t.Fatal(err)
	}
	if lineageAfterEdit.Intent == nil || !lineageAfterEdit.Intent.Valid || lineageAfterEdit.Intent.DocumentVersionID != intentBind.DocumentVersionID ||
		lineageAfterEdit.Script == nil || !lineageAfterEdit.Script.Valid || lineageAfterEdit.Script.DocumentVersionID != scriptBind.DocumentVersionID ||
		lineageAfterEdit.Storyboard == nil || lineageAfterEdit.Storyboard.Valid {
		t.Fatalf("modifying storyboard must keep intent/script valid while invalidating storyboard: %+v", lineageAfterEdit)
	}
}

// TestOneSentenceWorkflowVideoFixtureRestartAndEditorLineage verifies:
// 1. One-sentence request -> intent confirmation -> script confirmation -> storyboard document node -> human approval -> exact Prompt -> quote confirmation -> task -> delivered media Version -> Canvas.
// 2. Test-injected video executor (explicitly marked as test-only fixture; real ark-video is M7.1) + mid-stream exit and restart recovery.
// 3. Post-approval storyboard edit blocking old PromptContract while keeping already-submitted task frozen.
// 4. Delivered media Version entering M3 editor (mask repair new_version on the same media Resource) while preserving complete SQL lineage.
func TestOneSentenceWorkflowVideoFixtureRestartAndEditorLineage(t *testing.T) {
	ctx := context.Background()
	f := setupPreflightFixture(t)

	// Step 1: Intent & Script human confirmations
	intentBind, scriptBind := f.upstream()
	// Step 2: Storyboard revision 1 + human approval (15s oral narration + music)
	board15 := pipeline.Storyboard{
		Panels: []pipeline.Panel{
			{ID: "p1", TimeMS: 0, Framing: "近景", Action: "讲述者微笑向镜头介绍产品亮点", Transition: "切"},
			{ID: "p2", TimeMS: 6000, Framing: "中景", Action: "随手势拉远展示全景", Transition: "慢推"},
		},
		Shots: []pipeline.Shot{
			{ID: "s1", StartMS: 0, EndMS: 6000, PanelIDs: []string{"p1"}, NarrativeRole: "开场", StartState: "静立", EndState: "开口"},
			{ID: "s2", StartMS: 6000, EndMS: 15000, PanelIDs: []string{"p2"}, NarrativeRole: "收束", StartState: "讲述", EndState: "定格"},
		},
		Timeline: pipeline.Timeline{
			DurationMS:  15000,
			ShotIDs:     []string{"s1", "s2"},
			AudioIntent: "15s人声口播+背景配乐",
		},
	}
	rawBoard, _ := json.Marshal(map[string]any{
		"requestId":     "wf-board-1",
		"mode":          "create",
		"format":        "md",
		"typedDocument": pipeline.Document{Kind: "storyboard", PipelineID: "film", ExpectedRevision: 0, Storyboard: board15},
		"output": map[string]any{
			"resources":   []any{map[string]any{"key": "storyboard", "mode": "create", "name": "storyboard_review.md"}},
			"composition": map[string]any{"name": "Film", "type": map[string]any{"label": "video"}, "placements": []any{map[string]any{"key": "storyboard", "role": "storyboard"}}},
		},
	})
	sbReceipt, err := f.docs.WriteDocument(ctx, f.p, rawBoard)
	if err != nil || sbReceipt.Delivery.Status != "completed" {
		t.Fatalf("wf-board-1: %+v %v", sbReceipt, err)
	}
	sbBind, err := pipeline.BindingForVersion(ctx, f.db, f.p, sbReceipt.Delivery.Versions[0].VersionID)
	if err != nil {
		t.Fatal(err)
	}
	f.approve(sbBind)
	refA, refVerA := f.createRefAsset("wf-ref-a", "KeyframeRef")

	// Step 3: Exact final_video_prompt.md + video:seedance PromptContract (15s oral narration + music)
	videoPrompt := "镜头一 [00:00-00:06] [近景]：讲述者微笑向镜头介绍产品亮点。\r\n镜头二 [00:06-00:15] [中景]：随手势拉远展示全景，背景轻快配乐自然收尾。 "
	videoPromptRec, err := f.writePrompt("wf-prompt-video", 0, "", videoPrompt, &pipeline.ContractDraft{
		CapabilityID: "video:seedance",
		Params:       map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
		References:   []string{refA},
	})
	if err != nil {
		t.Fatal(err)
	}
	videoPromptVersionID := videoPromptRec.Delivery.Versions[0].VersionID
	videoPromptView, err := pipeline.ReadPromptVersion(ctx, f.db, f.p, videoPromptVersionID)
	if err != nil || videoPromptView.Contract == nil || videoPromptView.Contract.Status != "ready" {
		t.Fatalf("expected ready video PromptContract, got %+v %v", videoPromptView, err)
	}
	videoContract := *videoPromptView.Contract

	// Submit video task bound to videoContract (using test-injected video executor fixture)
	videoParamsRaw, _ := json.Marshal(videoContract.Params)
	videoTaskIn := tasks.Input{
		RequestID:    "wf-video-task-1",
		SessionID:    "human-session",
		CapabilityID: "video:seedance",
		ContractID:   videoContract.ID,
		Prompt:       videoPrompt,
		Params:       videoParamsRaw,
		References:   []tasks.Reference{{AssetID: refA, VersionID: refVerA, Role: "reference"}},
		Provider: providers.FrozenProvider{
			Config: providers.Config{
				ID:       "seedance",
				Name:     "Seedance Test Fixture (Not Real Video Acceptance)",
				Kind:     "video",
				Protocol: "ark-video",
				BaseURL:  "https://api.example.com/v1",
				ModelID:  "seedance-2.0-fixture",
				Enabled:  true,
			},
			CredentialVersion: "test-video-cred-v1",
		},
		Output: delivery.Output{
			Resources: []delivery.Resource{{Key: "video_main", Mode: "create", Name: "口播成片"}},
			Composition: delivery.Composition{
				Name:       "Film",
				Type:       delivery.CompositionType{Label: "video"},
				Placements: []delivery.Placement{{Key: "video_main", Role: "video"}},
			},
		},
	}
	// Use test-scoped billing gate for the synthetic video fixture
	origBillingGate := f.tasks.BillingGate
	f.tasks.BillingGate = func(ctx context.Context, tx *sql.Tx, p string, v *tasks.TaskView, i tasks.Input, fresh bool) error {
		if strings.HasPrefix(i.CapabilityID, "video:") {
			credits := int64(150)
			v.Billing.EstimatedCredits = &credits
			return nil
		}
		return origBillingGate(ctx, tx, p, v, i, fresh)
	}
	videoTask, err := f.tasks.Submit(ctx, f.p, videoTaskIn)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate human billing approval for the test-injected video task
	if _, err := f.db.Exec(`UPDATE task_attempts SET state='approved' WHERE task_id=? AND attempt=1`, videoTask.ID); err != nil {
		t.Fatal(err)
	}

	// Test-injected video executor (explicitly marked test fixture)
	mp4Fixture := append([]byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0x00, 0x00, 0x00, 0x00, 'm', 'p', '4', '2', 'i', 's', 'o', 'm'}, make([]byte, 128)...)
	f.tasks.Executor = &tasks.Executor{
		Submit: func(ctx context.Context, a tasks.Attempt, cp func(string) error) (tasks.Outcome, error) {
			if err := cp("fixture-video-job-1"); err != nil {
				return tasks.Outcome{}, err
			}
			// Post-approval storyboard edit while video task is already submitted/in-flight:
			// Old contract must not be retroactively invalidated, while any new unapproved execution is blocked!
			_ = f.create("storyboard", "wf-board-2", 1, sbBind.DocumentVersionID, "panel")
			return tasks.Outcome{
				Status:  tasks.Failed,
				Result:  []byte(`{"fixture":"synthetic_video_not_real_acceptance","upstream_id":"fixture-video-job-1"}`),
				Problem: &tasks.Problem{Source: "delivery", Code: "RESULT_AWAITING_DELIVERY", Message: "awaiting delivery", Retryable: true},
			}, nil
		},
		Collect: func(ctx context.Context, a tasks.Attempt) (tasks.Outcome, error) {
			var outKey, colKey string
			if err := f.db.QueryRow("SELECT task_output_key,json_extract(source_json,'$.collection_key') FROM task_outputs WHERE task_id=? AND attempt=1", a.TaskID).Scan(&outKey, &colKey); err != nil {
				return tasks.Outcome{}, err
			}
			stage, err := f.files.Stage(ctx, f.p, "video-stage", "oral_video.mp4", strings.NewReader(string(mp4Fixture)))
			if err != nil {
				return tasks.Outcome{}, err
			}
			if _, err := f.db.ExecContext(ctx, "UPDATE task_outputs SET path=?,state='collected' WHERE task_output_key=?", stage, outKey); err != nil {
				return tasks.Outcome{}, err
			}
			rec, err := f.docs.Deliver(ctx, f.p, delivery.Request{
				RequestID: "task-" + colKey,
				Sources:   []delivery.Source{{Key: "video_main", TaskOutputKey: outKey}},
				Output:    a.Input.Output,
			})
			if err != nil || rec.Delivery.Status != "completed" {
				t.Fatalf("video fixture delivery failed: %+v %v", rec, err)
			}
			return tasks.Outcome{Status: tasks.Completed}, nil
		},
	}
	if err := f.tasks.Run(ctx, f.p, videoTask.ID); err != nil {
		t.Fatal(err)
	}
	doneVideo, err := f.tasks.Query(ctx, f.p, videoTask.ID)
	if err != nil || doneVideo.Status != tasks.Completed || len(doneVideo.Outputs) != 1 || doneVideo.Outputs[0].VersionID == nil {
		t.Fatalf("expected completed video fixture task, got %+v %v", doneVideo, err)
	}
	videoVersionID := *doneVideo.Outputs[0].VersionID

	// Verify that because storyboard was edited to revision 2, compiling or executing against old storyboard revision 1 is now blocked!
	if _, _, err := pipeline.Compile(ctx, f.db, f.p, pipeline.CompileInput{
		RequestID:       "stale-compile-rev1",
		PipelineID:      "film",
		PromptVersionID: videoPromptVersionID,
		Prompt:          videoPrompt,
		CapabilityID:    "video:seedance",
		References:      []string{refA},
		Params:          map[string]any{"duration": 15, "size": "1280x720", "aspect_ratio": "16:9", "generate_audio": true, "audio_mode": "narration+music"},
	}); err == nil {
		t.Fatal("expected compiling against stale storyboard revision 1 after edit to fail")
	}

	// Step 4: Approve storyboard revision 2, compile image keyframe contract, execute, and then edit the delivered media Version in M3 editor (new_version with mask) while preserving full SQL lineage!
	var sb2VersionID, sb2Hash string
	if err := f.db.QueryRow("SELECT document_version_id,semantic_hash FROM storyboard_revisions WHERE pipeline_id='film' AND revision=2").Scan(&sb2VersionID, &sb2Hash); err != nil {
		t.Fatal(err)
	}
	f.approve(pipeline.Binding{PipelineID: "film", Gate: "storyboard", Revision: 2, DocumentVersionID: sb2VersionID, SemanticHash: sb2Hash})

	imgPrompt := "镜头一 [00:00-00:06] [近景]：讲述者微笑展示产品关键帧。 "
	imgPromptRec, err := f.writePrompt("wf-prompt-img", 1, videoPromptVersionID, imgPrompt, &pipeline.ContractDraft{
		CapabilityID: "image:seedream",
		Params:       map[string]any{"size": "1024x1024", "quality": "high"},
		References:   []string{refA},
	})
	if err != nil {
		t.Fatal(err)
	}
	imgPromptVersionID := imgPromptRec.Delivery.Versions[0].VersionID
	imgPromptView, err := pipeline.ReadPromptVersion(ctx, f.db, f.p, imgPromptVersionID)
	if err != nil || imgPromptView.Contract == nil {
		t.Fatal(err)
	}
	imgContract := *imgPromptView.Contract

	// Admit and approve billing for keyframe v1
	imgReq := generation.Request{
		RequestID:    "wf-img-v1",
		SessionID:    "human-session",
		CapabilityID: "image:seedream",
		Prompt:       imgPrompt,
		Params:       map[string]any{"size": "1024x1024", "quality": "high"},
		References:   []string{refA},
		ContractID:   &imgContract.ID,
		Output: delivery.Output{
			Resources: []delivery.Resource{{Key: "keyframe", Mode: "create", Name: "分镜关键帧"}},
			Composition: delivery.Composition{
				Name:       "Film",
				Type:       delivery.CompositionType{Label: "video"},
				Placements: []delivery.Placement{{Key: "keyframe", Role: "keyframe"}},
			},
		},
	}
	imgRaw, _ := json.Marshal(imgReq)
	admittedImg, err := f.gen.Admit(ctx, f.p, imgReq, imgRaw, false, "")
	if err != nil {
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
	deliverPNG := func(a tasks.Attempt, key string) (tasks.Outcome, error) {
		var outKey, colKey string
		if err := f.db.QueryRow("SELECT task_output_key,json_extract(source_json,'$.collection_key') FROM task_outputs WHERE task_id=? AND attempt=?", a.TaskID, a.Number).Scan(&outKey, &colKey); err != nil {
			return tasks.Outcome{}, err
		}
		stage, err := f.files.Stage(ctx, f.p, "img-stage-"+a.TaskID, "frame.png", strings.NewReader(string(pngBytes)))
		if err != nil {
			return tasks.Outcome{}, err
		}
		if _, err := f.db.ExecContext(ctx, "UPDATE task_outputs SET path=?,state='collected' WHERE task_output_key=?", stage, outKey); err != nil {
			return tasks.Outcome{}, err
		}
		rec, err := f.docs.Deliver(ctx, f.p, delivery.Request{
			RequestID: "task-" + colKey,
			Sources:   []delivery.Source{{Key: key, TaskOutputKey: outKey}},
			Output:    a.Input.Output,
		})
		if err != nil || rec.Delivery.Status != "completed" {
			t.Fatalf("image delivery failed: %+v %v", rec, err)
		}
		return tasks.Outcome{Status: tasks.Completed}, nil
	}
	f.tasks.Executor = &tasks.Executor{
		Submit: func(ctx context.Context, a tasks.Attempt, cp func(string) error) (tasks.Outcome, error) {
			_ = cp("img-job-" + a.TaskID)
			return tasks.Outcome{
				Status:  tasks.Failed,
				Result:  []byte(`{"data":[{"b64_json":"ok"}]}`),
				Problem: &tasks.Problem{Source: "delivery", Code: "RESULT_AWAITING_DELIVERY", Message: "awaiting delivery", Retryable: true},
			}, nil
		},
		Collect: func(ctx context.Context, a tasks.Attempt) (tasks.Outcome, error) {
			return deliverPNG(a, a.Input.Output.Resources[0].Key)
		},
	}

	var billRaw string
	if err := f.db.QueryRow("SELECT question_json FROM questions WHERE task_id=? AND kind='billing'", admittedImg.Task.ID).Scan(&billRaw); err != nil {
		t.Fatal(err)
	}
	var billQ questions.Question
	_ = json.Unmarshal([]byte(billRaw), &billQ)
	if _, err := f.q.Reply(ctx, f.p, billQ.ID, questions.HumanReply{
		SessionID:        "human-session",
		RequestID:        "approve-wf-img-v1",
		ExpectedRevision: billQ.Revision,
		Answers:          [][]string{{billQ.Questions[0].Options[0].Label}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Run(ctx, f.p, admittedImg.Task.ID); err != nil {
		t.Fatal(err)
	}
	doneImg1, err := f.tasks.Wait(ctx, f.p, admittedImg.Task.ID)
	if err != nil || doneImg1.Status != tasks.Completed || doneImg1.Outputs[0].VersionID == nil || doneImg1.Outputs[0].AssetID == nil {
		t.Fatalf("expected completed keyframe v1, got %+v %v", doneImg1, err)
	}
	mediaV1 := *doneImg1.Outputs[0].VersionID
	mediaAsset1 := *doneImg1.Outputs[0].AssetID
	mediaResourceID := doneImg1.Outputs[0].ResourceID

	// Step 5: Simulate M3 ImageEditor mask repair on mediaV1 (new_version on mediaResourceID without needing a new storyboard contract)
	maskAsset, err := f.providers.CreateMask(ctx, f.p, providers.MaskInput{
		RequestID: "wf-mask-1",
		AssetID:   mediaAsset1,
		Bounds:    []providers.MaskBounds{{X: 0, Y: 0, Width: 1, Height: 1}},
	})
	if err != nil {
		t.Fatalf("CreateMask: %v", err)
	}
	var compTypeID string
	if err := f.db.QueryRow("SELECT c.type_id FROM resources r JOIN compositions c ON c.id=r.composition_id WHERE r.id=?", mediaResourceID).Scan(&compTypeID); err != nil {
		t.Fatal(err)
	}
	repairReq := generation.Request{
		RequestID:    "wf-img-v2-repair",
		SessionID:    "human-session",
		CapabilityID: "image:seedream",
		Prompt:       "修复局部色彩与边缘细节",
		Params:       map[string]any{"size": "1024x1024"},
		References:   []string{mediaAsset1, maskAsset.ID},
		Output: delivery.Output{
			Resources: []delivery.Resource{{Key: "keyframe_repair", Mode: "new_version", Name: "分镜关键帧 v2", BaseVersionID: mediaV1}},
			Composition: delivery.Composition{
				Name:       "Film",
				Type:       delivery.CompositionType{ID: compTypeID},
				Placements: []delivery.Placement{{Key: "keyframe_repair", Role: "keyframe"}},
			},
		},
	}
	repairRaw, _ := json.Marshal(repairReq)
	admittedRepair, err := f.gen.Admit(ctx, f.p, repairReq, repairRaw, false, "")
	if err != nil {
		t.Fatalf("M3 editor new_version repair admission failed: %v", err)
	}
	if err := f.db.QueryRow("SELECT question_json FROM questions WHERE task_id=? AND kind='billing'", admittedRepair.Task.ID).Scan(&billRaw); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(billRaw), &billQ)
	if _, err := f.q.Reply(ctx, f.p, billQ.ID, questions.HumanReply{
		SessionID:        "human-session",
		RequestID:        "approve-wf-img-v2",
		ExpectedRevision: billQ.Revision,
		Answers:          [][]string{{billQ.Questions[0].Options[0].Label}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Run(ctx, f.p, admittedRepair.Task.ID); err != nil {
		t.Fatal(err)
	}
	doneImg2, err := f.tasks.Wait(ctx, f.p, admittedRepair.Task.ID)
	if err != nil || doneImg2.Status != tasks.Completed || doneImg2.Outputs[0].VersionID == nil {
		t.Fatalf("expected completed M3 repair v2, got %+v %v", doneImg2, err)
	}
	mediaV2 := *doneImg2.Outputs[0].VersionID
	if doneImg2.Outputs[0].ResourceID != mediaResourceID || mediaV2 == mediaV1 {
		t.Fatalf("M3 editor repair must create a new Version on the same media Resource: v1=%s v2=%s res1=%s res2=%s", mediaV1, mediaV2, mediaResourceID, doneImg2.Outputs[0].ResourceID)
	}

	// Step 6: Simulate physical database reopen + service recovery and verify full SQL lineage from mediaV2!
	var seq int
	var dbName, dbPath string
	if err := f.db.QueryRow("PRAGMA database_list").Scan(&seq, &dbName, &dbPath); err != nil {
		t.Fatal(err)
	}
	reopenedDB, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedDB.Close()
	reopenedEv := events.NewService(reopenedDB)
	defer reopenedEv.Close()
	reopenedTasks := tasks.NewService(reopenedDB, reopenedEv, f.tasks.Executor)
	defer reopenedTasks.Close()
	if err := reopenedTasks.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	wReopened, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer wReopened.Close()

	lineageV2, err := pipeline.TraceLineage(ctx, reopenedDB, f.p, mediaV2)
	if err != nil {
		t.Fatalf("TraceLineage(mediaV2) after reopen failed: %v", err)
	}
	if lineageV2.Intent == nil || lineageV2.Intent.DocumentVersionID != intentBind.DocumentVersionID ||
		lineageV2.Script == nil || lineageV2.Script.DocumentVersionID != scriptBind.DocumentVersionID ||
		lineageV2.Storyboard == nil || lineageV2.Storyboard.DocumentVersionID != sb2VersionID ||
		lineageV2.Prompt == nil || lineageV2.Prompt.DocumentVersionID != imgPromptVersionID ||
		lineageV2.Contract == nil || lineageV2.Contract.ID != imgContract.ID {
		t.Fatalf("M3 editor mediaV2 lost upstream creative lineage: %+v", lineageV2)
	}
	hasV1, hasV2, hasVideo, hasMediaRevDep := false, false, false, false
	for _, out := range lineageV2.Outputs {
		if out.VersionID == mediaV1 {
			hasV1 = true
		}
		if out.VersionID == mediaV2 && out.ResourceID == mediaResourceID && out.SourceVersionID == mediaV1 {
			hasV2 = true
		}
		if out.VersionID == videoVersionID {
			hasVideo = true
		}
	}
	for _, dep := range lineageV2.Dependencies {
		if dep.VersionID == mediaV2 && dep.SourceVersionID == mediaV1 && dep.Kind == "media_revision" {
			hasMediaRevDep = true
		}
	}
	if !hasV1 || !hasV2 || !hasVideo || !hasMediaRevDep {
		t.Fatalf("expected outputs to contain video (%v), mediaV1 (%v), mediaV2 (%v) and media_revision dependency (%v): outputs=%+v deps=%+v",
			hasVideo, hasV1, hasV2, hasMediaRevDep, lineageV2.Outputs, lineageV2.Dependencies)
	}
}
