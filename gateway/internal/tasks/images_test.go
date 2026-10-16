package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
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
