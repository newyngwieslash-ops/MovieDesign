package tasks

import (
	"context"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"strings"
)

type TextResult struct {
	Data []TextContent `json:"data"`
}
type TextContent struct {
	Text string `json:"text"`
}

// InstallText injects native execution without an agent/tasks import cycle.
func InstallText(base *Executor, s *providers.Service, d *delivery.Service, submit func(context.Context, Attempt) (Outcome, error), query func(context.Context, Attempt) (Outcome, error)) *Executor {
	collect := collectOutputs(s, d, func(ctx context.Context, a Attempt, keys []string) ([]string, error) {
		var result TextResult
		if json.Unmarshal(a.Result, &result) != nil || len(result.Data) != 1 || len(keys) != 1 {
			return nil, invalid("Invalid saved text result")
		}
		v, e := s.Files.Stage(ctx, a.ProjectID, "text-"+keys[0], "document.md", strings.NewReader(result.Data[0].Text))
		return []string{v}, e
	})
	return &Executor{Submit: func(ctx context.Context, a Attempt, checkpoint func(string) error) (Outcome, error) {
		if a.Provider.Config.Kind == "llm" {
			return submit(ctx, a)
		}
		return base.Submit(ctx, a, checkpoint)
	}, Collect: func(ctx context.Context, a Attempt) (Outcome, error) {
		if a.Provider.Config.Kind == "llm" {
			return collect(ctx, a)
		}
		return base.Collect(ctx, a)
	}, Query: func(ctx context.Context, a Attempt) (Outcome, error) {
		if a.Provider.Config.Kind == "llm" {
			return query(ctx, a)
		}
		if base.Query != nil {
			return base.Query(ctx, a)
		}
		return Outcome{Status: Unknown}, nil
	}, Cancel: func(ctx context.Context, a Attempt) (Outcome, error) {
		if a.Provider.Config.Kind != "llm" && base.Cancel != nil {
			return base.Cancel(ctx, a)
		}
		return Outcome{Status: Cancelled}, nil
	}}
}
