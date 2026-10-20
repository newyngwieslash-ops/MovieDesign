package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
)

func TestM73RecoveryMatrixAndCancellationRules(t *testing.T) {
	s, p, in := setup(t)
	ctx := context.Background()

	// 1. Approved-but-unsent attempt: Recover must NOT call Submit.
	submitCalls := 0
	s.Executor = &Executor{
		Submit: func(context.Context, Attempt, func(string) error) (Outcome, error) {
			submitCalls++
			return Outcome{Status: Completed}, nil
		},
	}
	unsent, err := s.Submit(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	if unsent.Billing.ChargeStatus != "awaiting_approval" || unsent.Attempt != 1 || unsent.AttemptState != "billing" {
		t.Fatalf("unexpected initial task view metadata: %+v", unsent)
	}
	if _, err := s.DB.Exec(`UPDATE task_attempts SET state='approved' WHERE task_id=? AND attempt=1`, unsent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE tasks SET status='accepted' WHERE id=?`, unsent.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(ctx); err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if submitCalls != 0 {
		t.Fatalf("Recover called Submit on approved-but-unsent task: %d", submitCalls)
	}

	// Cancel before send: actual_amount=0, charge_status="not_submitted", and duplicate cancel is idempotent.
	cancelledBeforeSend, err := s.Resolve(ctx, p, unsent.ID, ResolveInput{
		RequestID:        "cancel-before-send-1",
		ExpectedRevision: unsent.Revision,
		Action:           "cancel",
	})
	if err != nil {
		t.Fatalf("cancel before send failed: %v", err)
	}
	if cancelledBeforeSend.Status != Cancelled || cancelledBeforeSend.Billing.ChargeStatus != "not_submitted" || cancelledBeforeSend.Billing.ActualAmount == nil || *cancelledBeforeSend.Billing.ActualAmount != 0 {
		t.Fatalf("unexpected cancelled-before-send billing view: %+v", cancelledBeforeSend.Billing)
	}
	dupCancel, err := s.Resolve(ctx, p, unsent.ID, ResolveInput{
		RequestID:        "cancel-before-send-2",
		ExpectedRevision: cancelledBeforeSend.Revision,
		Action:           "cancel",
	})
	if err != nil || dupCancel.Status != Cancelled || dupCancel.Revision != cancelledBeforeSend.Revision {
		t.Fatalf("duplicate cancel should be idempotent without bumping revision: err=%v got=%+v", err, dupCancel)
	}

	// 2. Interrupted POST before upstream_id saved -> Unknown -> Query reconciles without calling Submit.
	in2 := in
	in2.RequestID = "req-unknown-post"
	in2.Output.Composition.Name = "ShotUnknown"
	in2.Provider.Config.Kind = "llm"
	taskUnknown, err := s.Submit(ctx, p, in2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE tasks SET status='running' WHERE id=?`, taskUnknown.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE task_attempts SET state='submitting', submit_started_at=100 WHERE task_id=? AND attempt=1`, taskUnknown.ID); err != nil {
		t.Fatal(err)
	}
	s.Executor = &Executor{
		Submit: func(context.Context, Attempt, func(string) error) (Outcome, error) {
			submitCalls++
			return Outcome{}, errors.New("Submit must not be called during Recover or Query")
		},
		Query: func(context.Context, Attempt) (Outcome, error) {
			return Outcome{Status: Unknown}, errors.New("still unreachable")
		},
	}
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	afterRecover, err := s.Query(ctx, p, taskUnknown.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterRecover.Status != Unknown || afterRecover.Problem == nil || afterRecover.Problem.Code != "PROVIDER_OUTCOME_UNKNOWN" {
		t.Fatalf("expected PROVIDER_OUTCOME_UNKNOWN after interrupted POST, got %+v", afterRecover)
	}
	for _, act := range afterRecover.Actions {
		if act == "retry" {
			t.Fatalf("Unknown state must never expose retry action: %v", afterRecover.Actions)
		}
	}
	queryCalls := 0
	s.Executor.Query = func(context.Context, Attempt) (Outcome, error) {
		queryCalls++
		return Outcome{
			Status:  Failed,
			Problem: &Problem{Source: "provider", Code: "UPSTREAM_503", Message: "Upstream confirmed failed", Retryable: true},
		}, nil
	}
	reconciled, err := s.Resolve(ctx, p, taskUnknown.ID, ResolveInput{
		RequestID:        "resolve-query-1",
		ExpectedRevision: afterRecover.Revision,
		Action:           "query",
	})
	if err != nil {
		t.Fatalf("Resolve(query) failed: %v", err)
	}
	if submitCalls != 0 || queryCalls != 1 || reconciled.Status != Failed {
		t.Fatalf("expected 0 Submit, 1 Query, status=failed; got submit=%d query=%d status=%s", submitCalls, queryCalls, reconciled.Status)
	}

	// 3. Stale attempt late-completion guard: attempt 1 finishing late after retry into attempt 2 must not corrupt attempt 2 or leave attempt 1 collecting.
	retried, err := s.Resolve(ctx, p, taskUnknown.ID, ResolveInput{
		RequestID:        "resolve-retry-attempt2",
		ExpectedRevision: reconciled.Revision,
		Action:           "retry",
	})
	if err != nil {
		t.Fatalf("Resolve(retry) failed: %v", err)
	}
	if retried.Attempt != 2 || retried.Status != WaitingUser {
		t.Fatalf("expected attempt 2 waiting_user, got %+v", retried)
	}
	staleAttempt1 := Attempt{TaskID: taskUnknown.ID, ProjectID: p, Number: 1}
	collectCalled := false
	s.Executor.Collect = func(context.Context, Attempt) (Outcome, error) {
		collectCalled = true
		return Outcome{Status: Completed}, nil
	}
	err = s.finishOrCollect(ctx, staleAttempt1, Outcome{
		Status:  Failed,
		Result:  []byte(`{"data":[{"b64_json":"AA=="}]}`),
		Problem: &Problem{Source: "delivery", Code: "RESULT_AWAITING_DELIVERY", Message: "late", Retryable: true},
	}, nil)
	if err == nil || collectCalled {
		t.Fatalf("expected stale attempt finishOrCollect to reject without calling Collect, err=%v collectCalled=%v", err, collectCalled)
	}
	var attempt1State string
	if err := s.DB.QueryRow(`SELECT state FROM task_attempts WHERE task_id=? AND attempt=1`, taskUnknown.ID).Scan(&attempt1State); err != nil {
		t.Fatal(err)
	}
	if attempt1State != "failed" {
		t.Fatalf("stale attempt 1 must remain terminal ('failed'), got %q", attempt1State)
	}

	// 4. Ark video cancellation matrix: queued (DELETE once) vs running (no DELETE + LateResult delivery card on late completion).
	var (
		mu          sync.Mutex
		postCalls   int
		getCalls    int
		deleteCalls int
		upstreamSt  = "queued"
		serverURL   string
	)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/contents/generations/tasks":
			postCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "cgt-m73-vid", "status": upstreamSt})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/contents/generations/tasks/cgt-m73-vid":
			getCalls++
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{"id": "cgt-m73-vid", "status": upstreamSt}
			if upstreamSt == "succeeded" {
				resp["content"] = map[string]any{"video_url": serverURL + "/cdn/late.mp4"}
			}
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v3/contents/generations/tasks/cgt-m73-vid":
			deleteCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer remote.Close()
	serverURL = remote.URL

	vidIn := in
	vidIn.RequestID = "req-vid-queued-cancel"
	vidIn.Output.Composition.Name = "ShotVidQueued"
	vidIn.Output.Composition.Type.Label = "VideoShots1"
	vidIn.CapabilityID = "video:ark-test"
	vidIn.Count = 1
	vidIn.References = nil
	vidIn.Provider.CredentialVersion = ""
	vidIn.Provider.Config.ID = "ark-test"
	vidIn.Provider.Config.Name = "Ark Video Test"
	vidIn.Provider.Config.Kind = "video"
	vidIn.Provider.Config.Protocol = "ark-video"
	vidIn.Provider.Config.BaseURL = remote.URL + "/api/v3"
	vidIn.Provider.Config.ModelID = "doubao-seedance-2-0"
	vidIn.Provider.Config.Enabled = true
	vidIn.Provider.Config.Capability.MaxCount = 1
	vidIn.Provider.Config.Capability.MaxPromptBytes = 16384
	vidIn.Provider.Config.Capability.MaxReferences = 4
	vidIn.Provider.Config.Capability.Sizes = []string{"1080x1920"}
	vidIn.Provider.Config.Capability.DurationSeconds = []int{5, 10, 15}
	vidIn.Provider.Config.Capability.Audio = true
	vidIn.Params = json.RawMessage(`{"duration":15,"aspect_ratio":"9:16","resolution":"1080p","size":"1080x1920","generate_audio":true,"audio_mode":"narration+music"}`)

	providerSvc := providers.NewService(s.DB, nil)
	s.Executor = ImageExecutor(providerSvc)

	// 4a. Cancel while queued -> calls DELETE once, sets TASK_CANCELLED_QUEUED and charge_status="remote_cancelled_queued".
	vidQueued, err := s.Submit(ctx, p, vidIn)
	if err != nil {
		t.Fatal(err)
	}
	if vidQueued.Provider == nil || len(vidQueued.Provider.RemoteCancelStages) != 1 || vidQueued.Provider.RemoteCancelStages[0] != "queued" {
		t.Fatalf("expected ark-video provider remote_cancel_stages=['queued'], got %+v", vidQueued.Provider)
	}
	if _, err := s.DB.Exec(`UPDATE tasks SET status='running' WHERE id=?`, vidQueued.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE task_attempts SET state='submitted', submit_started_at=1, upstream_id='cgt-m73-vid' WHERE task_id=? AND attempt=1`, vidQueued.ID); err != nil {
		t.Fatal(err)
	}
	cancelledQueued, err := s.Resolve(ctx, p, vidQueued.ID, ResolveInput{
		RequestID:        "cancel-vid-queued-1",
		ExpectedRevision: vidQueued.Revision,
		Action:           "cancel",
	})
	if err != nil {
		t.Fatalf("cancel queued video failed: %v", err)
	}
	mu.Lock()
	gotDeletes := deleteCalls
	mu.Unlock()
	if gotDeletes != 1 {
		t.Fatalf("expected exactly 1 DELETE for queued video task, got %d", gotDeletes)
	}
	if cancelledQueued.Problem == nil || cancelledQueued.Problem.Code != "TASK_CANCELLED_QUEUED" || cancelledQueued.Billing.ChargeStatus != "remote_cancelled_queued" {
		t.Fatalf("unexpected queued cancel result: problem=%+v billing=%+v", cancelledQueued.Problem, cancelledQueued.Billing)
	}
	// Duplicate cancel must not send a second DELETE.
	_, err = s.Resolve(ctx, p, vidQueued.ID, ResolveInput{
		RequestID:        "cancel-vid-queued-2",
		ExpectedRevision: cancelledQueued.Revision,
		Action:           "cancel",
	})
	if err != nil {
		t.Fatalf("duplicate queued cancel failed: %v", err)
	}
	mu.Lock()
	gotDeletesAfterDup := deleteCalls
	mu.Unlock()
	if gotDeletesAfterDup != 1 {
		t.Fatalf("duplicate cancel sent extra DELETE: %d", gotDeletesAfterDup)
	}

	// 4b. Cancel while running -> 0 additional DELETEs, RUNNING_UNCANCELLABLE, and late completion triggers LateResult callback.
	mu.Lock()
	upstreamSt = "running"
	mu.Unlock()
	vidIn2 := vidIn
	vidIn2.RequestID = "req-vid-running-cancel"
	vidIn2.Output.Composition.Name = "ShotVidRunning"
	vidIn2.Output.Composition.Type.Label = "VideoShots2"
	vidRunning, err := s.Submit(ctx, p, vidIn2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE tasks SET status='running' WHERE id=?`, vidRunning.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE task_attempts SET state='running', submit_started_at=1, upstream_id='cgt-m73-vid' WHERE task_id=? AND attempt=1`, vidRunning.ID); err != nil {
		t.Fatal(err)
	}
	lateResultCh := make(chan string, 1)
	s.LateResult = func(_ context.Context, _ *sql.Tx, _ string, v TaskView, _ int, _ string) error {
		select {
		case lateResultCh <- v.ID:
		default:
		}
		return nil
	}
	cancelledRunning, err := s.Resolve(ctx, p, vidRunning.ID, ResolveInput{
		RequestID:        "cancel-vid-running-1",
		ExpectedRevision: vidRunning.Revision,
		Action:           "cancel",
	})
	if err != nil {
		t.Fatalf("cancel running video failed: %v", err)
	}
	mu.Lock()
	gotDeletesRunning := deleteCalls
	mu.Unlock()
	if gotDeletesRunning != 1 {
		t.Fatalf("running video cancel must NOT call DELETE: deleteCalls=%d", gotDeletesRunning)
	}
	if cancelledRunning.Problem == nil || cancelledRunning.Problem.Code != "RUNNING_UNCANCELLABLE" || cancelledRunning.Billing.ChargeStatus != "local_cancelled_remote_may_charge" {
		t.Fatalf("unexpected running cancel result: problem=%+v billing=%+v", cancelledRunning.Problem, cancelledRunning.Billing)
	}
	// Transition upstream to succeeded; pollAcceptedAttempt must capture result_json and trigger LateResult without calling POST.
	mu.Lock()
	upstreamSt = "succeeded"
	mu.Unlock()
	select {
	case lateTaskID := <-lateResultCh:
		if lateTaskID != vidRunning.ID {
			t.Fatalf("unexpected lateTaskID %q", lateTaskID)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for LateResult callback on cancelled running video task")
	}
	mu.Lock()
	finalPostCalls := postCalls
	mu.Unlock()
	if finalPostCalls != 0 {
		t.Fatalf("expected 0 POST calls during cancel/poll recovery, got %d", finalPostCalls)
	}
}
