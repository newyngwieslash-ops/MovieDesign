package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestImageAdapterPersistsResultWithoutDeliveryOrResubmit(t *testing.T) {
	for _, response := range []string{`{"data":[{"b64_json":"invalid-media-but-saved"}]}`, `{"data":[]}`} {
		t.Run(response, func(t *testing.T) {
			s, p, in := setup(t)
			var posts atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts.Add(1); fmt.Fprint(w, response) }))
			defer remote.Close()
			in.Count = 1
			in.Provider.CredentialVersion = ""
			in.Provider.Config.BaseURL = remote.URL
			in.Provider.Config.ModelID = "configured-custom"
			in.Provider.Config.Kind = "image"
			in.Provider.Config.Protocol = "openai-images"
			in.Provider.Config.Name = "Configured"
			in.Provider.Config.Capability.MaxCount = 1
			in.Provider.Config.Capability.MaxPromptBytes = 65536
			in.Provider.Config.Capability.MaxReferences = 0
			in.References = nil
			provider := providers.NewService(s.DB, nil)
			s.Executor = ImageExecutor(provider)
			v, e := s.Submit(context.Background(), p, in)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Run(context.Background(), p, v.ID); e != nil || posts.Load() != 0 {
				t.Fatal("billing gate bypass", e)
			}
			// Test-only approval state; production M4.4 owns the human receipt transition.
			if _, e = s.DB.Exec("UPDATE task_attempts SET state='approved' WHERE task_id=?", v.ID); e != nil {
				t.Fatal(e)
			}
			if e = s.Run(context.Background(), p, v.ID); e != nil {
				t.Fatal(e)
			}
			var saved string
			var outputs int
			if e = s.DB.QueryRow("SELECT coalesce(result_json,'') FROM task_attempts WHERE task_id=?", v.ID).Scan(&saved); e != nil {
				t.Fatal(e)
			}
			if saved == "" || !json.Valid([]byte(saved)) {
				t.Fatal("provider evidence lost", saved)
			}
			v, e = s.Query(context.Background(), p, v.ID)
			if e != nil || v.Status != Failed || v.Problem.Source != "delivery" {
				t.Fatal(v, e)
			}
			s.DB.QueryRow("SELECT count(*) FROM task_outputs WHERE task_id=? AND (asset_id IS NOT NULL OR version_id IS NOT NULL)", v.ID).Scan(&outputs)
			if outputs != 0 {
				t.Fatal("fake delivered identities")
			}
			if e = s.Recover(context.Background()); e != nil {
				t.Fatal(e)
			}
			if e = s.Run(context.Background(), p, v.ID); e != nil || posts.Load() != 1 {
				t.Fatal("saved result was resubmitted", e, posts.Load())
			}
		})
	}
}
func TestUnknownRetainsSanitizedUpstreamEvidence(t *testing.T) {
	s, p, in := setup(t)
	ctx := context.Background()
	v, e := s.Submit(ctx, p, in)
	if e != nil {
		t.Fatal(e)
	}
	s.Executor = &Executor{Submit: func(context.Context, Attempt, func(string) error) (Outcome, error) {
		return Outcome{Problem: &Problem{Source: "provider", Code: "HTTP_500", Upstream: &Upstream{Code: "temporary_error", Message: "HTTP 500: retry later"}}}, fmt.Errorf("transport result uncertain")
	}}
	if _, e = s.DB.Exec("UPDATE task_attempts SET state='approved' WHERE task_id=?", v.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Run(ctx, p, v.ID); e != nil {
		t.Fatal(e)
	}
	v, e = s.Query(ctx, p, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if v.Status != Unknown || v.Problem.Upstream == nil || v.Problem.Upstream.Code != "temporary_error" {
		t.Fatal("safe upstream evidence lost", v)
	}
}

func TestImageExecutorPreservesJSONNumbers(t *testing.T) {
	for _, token := range []string{"1", "1.0", "1e0", "1.0000000000000001", "100.0000000000000001"} {
		t.Run(token, func(t *testing.T) {
			s, p, in := setup(t)
			calls := 0
			wire := ""
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]json.RawMessage
				json.NewDecoder(r.Body).Decode(&body)
				wire = string(body["output_compression"])
				fmt.Fprint(w, `{"data":[]}`)
			}))
			defer remote.Close()
			in.Count = 1
			in.References = nil
			in.Provider.CredentialVersion = ""
			in.Provider.Config.BaseURL = remote.URL
			in.Provider.Config.Name = "Exact"
			in.Provider.Config.ModelID = "exact-model"
			in.Provider.Config.Kind = "image"
			in.Provider.Config.Protocol = "openai-images"
			in.Provider.Config.Capability.MaxCount = 1
			in.Provider.Config.Capability.MaxPromptBytes = 65536
			in.Params = json.RawMessage(`{"output_format":"jpeg","output_compression":` + token + `}`)
			executor := ImageExecutor(providers.NewService(s.DB, nil))
			_, e := executor.Submit(context.Background(), Attempt{ProjectID: p, Input: in, Provider: in.Provider}, func(string) error { return nil })
			if strings.Contains(token, "0000000000000001") {
				if e == nil || calls != 0 {
					t.Fatal("rounded invalid number submitted", calls, e)
				}
			} else if calls != 1 || wire != token {
				t.Fatal("numeric wire token changed", token, wire, calls, e)
			}
		})
	}
}

func TestVideoExecutorRestartPollResumptionAndLateResult(t *testing.T) {
	s, p, in := setup(t)
	ctx := context.Background()

	var (
		mu          sync.Mutex
		postCalls   int
		getCalls    int
		deleteCalls int
		status      = "running"
		serverURL   string
	)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/contents/generations/tasks":
			postCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "cgt-restart-001", "status": "queued"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/contents/generations/tasks/cgt-restart-001":
			getCalls++
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{"id": "cgt-restart-001", "status": status}
			if status == "succeeded" {
				resp["content"] = map[string]any{"video_url": serverURL + "/cdn/restart.mp4"}
			}
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodDelete:
			deleteCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer remote.Close()
	serverURL = remote.URL

	in.CapabilityID = "video:ark-test"
	in.Count = 1
	in.References = nil
	in.Provider.CredentialVersion = ""
	in.Provider.Config.ID = "ark-test"
	in.Provider.Config.Name = "Ark Video Test"
	in.Provider.Config.Kind = "video"
	in.Provider.Config.Protocol = "ark-video"
	in.Provider.Config.BaseURL = remote.URL + "/api/v3"
	in.Provider.Config.ModelID = "doubao-seedance-2-0"
	in.Provider.Config.Enabled = true
	in.Provider.Config.Capability.MaxCount = 1
	in.Provider.Config.Capability.MaxPromptBytes = 16384
	in.Provider.Config.Capability.MaxReferences = 4
	in.Provider.Config.Capability.Sizes = []string{"1080x1920"}
	in.Provider.Config.Capability.DurationSeconds = []int{5, 10, 15}
	in.Provider.Config.Capability.Audio = true
	in.Params = json.RawMessage(`{"duration":15,"aspect_ratio":"9:16","resolution":"1080p","size":"1080x1920","generate_audio":true,"audio_mode":"narration+music"}`)

	providerSvc := providers.NewService(s.DB, nil)
	s.Executor = ImageExecutor(providerSvc)

	v, err := s.Submit(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an interrupted running attempt that had already saved upstream_task_id="cgt-restart-001"
	if _, err := s.DB.Exec(`UPDATE tasks SET status='running', updated_at=1 WHERE id=?`, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE task_attempts SET state='running', submit_started_at=1, upstream_id='cgt-restart-001' WHERE task_id=? AND attempt=1`, v.ID); err != nil {
		t.Fatal(err)
	}

	// Recover while upstream is still "running": must call GET (never POST) and keep task Running while spawning pollAcceptedAttempt
	if err := s.Recover(ctx); err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	mu.Lock()
	if postCalls != 0 || getCalls == 0 {
		mu.Unlock()
		t.Fatalf("expected postCalls=0 and getCalls>0 during Recover, got post=%d get=%d", postCalls, getCalls)
	}
	// Now transition upstream to succeeded and verify background poll settles the task without ever calling POST
	status = "succeeded"
	mu.Unlock()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		cur, qErr := s.Query(ctx, p, v.ID)
		if qErr != nil {
			t.Fatal(qErr)
		}
		if cur.Status != Running {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	finalPosts := postCalls
	finalDeletes := deleteCalls
	mu.Unlock()
	if finalPosts != 0 || finalDeletes != 0 {
		t.Fatalf("expected 0 POSTs and 0 DELETEs on restart poll resumption, got post=%d delete=%d", finalPosts, finalDeletes)
	}
	var savedResult string
	if err := s.DB.QueryRow(`SELECT coalesce(result_json,'') FROM task_attempts WHERE task_id=? AND attempt=1`, v.ID).Scan(&savedResult); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(savedResult, "/cdn/restart.mp4") {
		t.Fatalf("expected saved video_url in result_json after poll resumption, got %s", savedResult)
	}
}
