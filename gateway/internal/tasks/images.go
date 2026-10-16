package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
)

// ImageExecutor only submits an already human-approved attempt. M4.4 owns that
// atomic transition. Saved media is collected/delivered by M4.5, never re-POSTed.
func ImageExecutor(s *providers.Service) *Executor {
	return &Executor{Submit: func(ctx context.Context, a Attempt, _ func(string) error) (Outcome, error) {
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
	}}
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
