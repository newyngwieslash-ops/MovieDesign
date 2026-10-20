package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
)

func videoOutcomeFromObservation(obs providers.VideoTaskObservation, callErr error) (Outcome, error) {
	out := Outcome{}
	if obs.Status == "succeeded" && len(obs.Result.Data) > 0 {
		out.Result, _ = json.Marshal(obs.Result)
		out.Status = Failed
		out.Problem = &Problem{Source: "delivery", Code: "RESULT_AWAITING_DELIVERY", Message: "Provider response saved; output delivery is not installed", Retryable: true}
		return out, nil
	}
	if obs.Status == "queued" || obs.Status == "running" {
		out.Status = Running
		if obs.Failure != nil {
			out.Problem = &Problem{Source: "provider", Code: obs.Failure.Code, Message: obs.Failure.Message, Upstream: &Upstream{Code: obs.Failure.Code, Message: obs.Failure.Message}, Retryable: false}
		}
		return out, nil
	}
	if obs.Status == "cancelled" {
		out.Status = Cancelled
		if obs.Failure != nil {
			out.NotAccepted = obs.Failure.NotAccepted
			out.Problem = &Problem{Source: "provider", Code: obs.Failure.Code, Message: obs.Failure.Message, Upstream: &Upstream{Code: obs.Failure.Code, Message: obs.Failure.Message}, Retryable: false}
		}
		return out, nil
	}
	if obs.Status == "failed" || obs.Status == "expired" {
		out.Status = Failed
		if obs.Failure != nil {
			out.NotAccepted = obs.Failure.NotAccepted
			out.Problem = &Problem{Source: "provider", Code: obs.Failure.Code, Message: obs.Failure.Message, Upstream: &Upstream{Code: obs.Failure.Code, Message: obs.Failure.Message}, Retryable: obs.Failure.NotAccepted}
		}
		return out, nil
	}
	if obs.Failure != nil {
		out.NotAccepted = obs.Failure.NotAccepted
		out.Problem = &Problem{Source: "provider", Code: obs.Failure.Code, Message: obs.Failure.Message, Upstream: &Upstream{Code: obs.Failure.Code, Message: obs.Failure.Message}, Retryable: obs.Failure.NotAccepted}
	}
	if callErr == nil {
		callErr = errors.New("video task outcome unknown")
	}
	return out, callErr
}

// VideoExecutor adapts Ark asynchronous video tasks (create -> checkpoint upstream_id -> poll -> collect -> deliver).
func VideoExecutor(s *providers.Service, deliveries ...*delivery.Service) *Executor {
	isRemoteCancelled := func(a Attempt) bool {
		if s == nil || s.DB == nil {
			return false
		}
		var state string
		if err := s.DB.QueryRow("SELECT state FROM task_attempts WHERE task_id=? AND attempt=?", a.TaskID, a.Number).Scan(&state); err == nil {
			return state == "cancelled"
		}
		return false
	}
	executor := &Executor{
		Submit: func(ctx context.Context, a Attempt, checkpoint func(string) error) (Outcome, error) {
			input, e := GenerationInput(a.ProjectID, a.Input)
			if e != nil {
				return Outcome{NotAccepted: true}, e
			}
			upstreamID := a.UpstreamID
			if upstreamID == "" {
				upstreamID, e = s.CreateVideoTask(ctx, input, a.Provider)
				if e != nil {
					out := Outcome{}
					var failure *providers.ImageError
					if errors.As(e, &failure) {
						out.NotAccepted = failure.NotAccepted
						out.Problem = &Problem{Source: "provider", Code: failure.Code, Message: failure.Message, Upstream: &Upstream{Code: failure.Code, Message: failure.Message}, Retryable: failure.NotAccepted}
					}
					return out, e
				}
				if e := checkpoint(upstreamID); e != nil {
					return Outcome{}, e
				}
			}
			obs, pollErr := s.PollVideoTask(ctx, a.Provider, upstreamID, func() bool { return isRemoteCancelled(a) })
			return videoOutcomeFromObservation(obs, pollErr)
		},
		Query: func(ctx context.Context, a Attempt) (Outcome, error) {
			obs, e := s.QueryVideoTask(ctx, a.Provider, a.UpstreamID)
			return videoOutcomeFromObservation(obs, e)
		},
		Cancel: func(ctx context.Context, a Attempt) (Outcome, error) {
			if a.UpstreamID == "" {
				return Outcome{Status: Cancelled}, nil
			}
			res, e := s.CancelVideoTask(ctx, a.Provider, a.UpstreamID)
			if res.Result != nil && len(res.Result.Data) > 0 {
				raw, _ := json.Marshal(*res.Result)
				return Outcome{
					Status:  Failed,
					Result:  raw,
					Problem: &Problem{Source: "delivery", Code: "RESULT_AWAITING_DELIVERY", Message: res.Message, Retryable: true},
				}, nil
			}
			if res.RemoteCancelled {
				return Outcome{
					Status:  Cancelled,
					Problem: &Problem{Source: "provider", Code: "TASK_CANCELLED_QUEUED", Message: res.Message, Upstream: &Upstream{Code: "cancelled", Message: res.Message}, Retryable: false},
				}, nil
			}
			code := "RUNNING_UNCANCELLABLE"
			if res.Status == "unknown" {
				code = "CANCEL_OUTCOME_UNKNOWN"
			}
			return Outcome{
				Status:  Running,
				Problem: &Problem{Source: "provider", Code: code, Message: res.Message, Upstream: &Upstream{Code: res.Status, Message: res.Message}, Retryable: false},
			}, e
		},
	}
	if len(deliveries) > 0 && deliveries[0] != nil {
		executor.Collect = collectVideos(s, deliveries[0])
	}
	return executor
}

// ImageExecutor only submits an already human-approved attempt. M4.4 owns that
// atomic transition. Saved media is collected/delivered by M4.5, never re-POSTed.
// Video attempts on the shared executor automatically route through VideoExecutor.
func ImageExecutor(s *providers.Service, deliveries ...*delivery.Service) *Executor {
	videoExec := VideoExecutor(s, deliveries...)
	executor := &Executor{
		Submit: func(ctx context.Context, a Attempt, checkpoint func(string) error) (Outcome, error) {
			if a.Provider.Config.Kind == "video" {
				return videoExec.Submit(ctx, a, checkpoint)
			}
			input, e := GenerationInput(a.ProjectID, a.Input)
			if e != nil {
				return Outcome{NotAccepted: true}, e
			}
			result, e := s.GenerateImage(ctx, input, a.Provider)
			out := Outcome{}
			if result.Data != nil {
				out.Result, _ = json.Marshal(result)
			}
			if e != nil {
				var failure *providers.ImageError
				if errors.As(e, &failure) {
					out.NotAccepted = failure.NotAccepted
					out.Problem = &Problem{Source: "provider", Code: failure.Code, Message: failure.Message, Upstream: &Upstream{Code: failure.Code, Message: failure.Message}, Retryable: failure.NotAccepted}
					if failure.HTTPStatus == 200 && len(out.Result) > 0 {
						out.Status = Failed
						out.Problem.Source = "delivery"
						return out, nil
					}
				}
				return out, e
			}
			out.Status = Failed
			out.Problem = &Problem{Source: "delivery", Code: "RESULT_AWAITING_DELIVERY", Message: "Provider response saved; output delivery is not installed", Retryable: true}
			return out, nil
		},
		Query: func(ctx context.Context, a Attempt) (Outcome, error) {
			if a.Provider.Config.Kind == "video" {
				return videoExec.Query(ctx, a)
			}
			return Outcome{Status: Unknown}, errors.New("image provider does not support upstream task query")
		},
		Cancel: func(ctx context.Context, a Attempt) (Outcome, error) {
			if a.Provider.Config.Kind == "video" {
				return videoExec.Cancel(ctx, a)
			}
			return Outcome{Status: Cancelled}, nil
		},
	}
	if len(deliveries) > 0 && deliveries[0] != nil {
		imgCollect := collectImages(s, deliveries[0])
		executor.Collect = func(ctx context.Context, a Attempt) (Outcome, error) {
			if a.Provider.Config.Kind == "video" {
				return videoExec.Collect(ctx, a)
			}
			return imgCollect(ctx, a)
		}
	}
	return executor
}

// GenerationInput preserves the exact same frozen input for quote and submission.
func GenerationInput(project string, i Input) (providers.GenerationInput, error) {
	var params map[string]any
	decoder := json.NewDecoder(bytes.NewReader(i.Params))
	decoder.UseNumber()
	if e := decoder.Decode(&params); e != nil {
		return providers.GenerationInput{}, e
	}
	output, e := json.Marshal(i.Output)
	if e != nil {
		return providers.GenerationInput{}, e
	}
	refs := make([]string, len(i.References))
	for i, r := range i.References {
		refs[i] = r.AssetID
	}
	return providers.GenerationInput{ProjectID: project, Prompt: i.Prompt, Params: params, References: refs, Count: i.Count, Output: output, Assets: i.Assets}, nil
}
