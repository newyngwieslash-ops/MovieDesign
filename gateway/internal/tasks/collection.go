package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
)

// One atomic Delivery batch owns all slots for an attempt. Its request identity
// derives from the first opaque output (itself task+attempt+slot unique).
func collectImages(s *providers.Service, d *delivery.Service) func(context.Context, Attempt) (Outcome, error) {
	return collectOutputs(s, d, func(ctx context.Context, a Attempt, keys []string) ([]string, error) {
		var result providers.ProviderResult
		if e := json.Unmarshal(a.Result, &result); e != nil {
			return nil, e
		}
		return s.CollectImages(ctx, a.ProjectID, result, a.Provider, keys)
	})
}

func collectVideos(s *providers.Service, d *delivery.Service) func(context.Context, Attempt) (Outcome, error) {
	return collectOutputs(s, d, func(ctx context.Context, a Attempt, keys []string) ([]string, error) {
		var result providers.ProviderResult
		if e := json.Unmarshal(a.Result, &result); e != nil {
			return nil, e
		}
		input, e := GenerationInput(a.ProjectID, a.Input)
		if e != nil {
			return nil, e
		}
		return s.CollectVideos(ctx, a.ProjectID, input, result, a.Provider, keys)
	})
}
func collectOutputs(s *providers.Service, d *delivery.Service, collect func(context.Context, Attempt, []string) ([]string, error)) func(context.Context, Attempt) (Outcome, error) {
	return func(ctx context.Context, a Attempt) (Outcome, error) {
		d := d
		if a.Input.Actor == "agent" {
			agentDelivery := delivery.NewService(s.DB, s.Files.Workspace, "agent")
			agentDelivery.CompleteTask = d.CompleteTask
			agentDelivery.Notify = d.Notify
			d = agentDelivery
		}
		failed := func(e error) (Outcome, error) {
			problem := &Problem{Source: "delivery", Code: "COLLECTION_IO", Message: "Saved result collection was interrupted; retry collection", Retryable: true}
			var collection *providers.CollectionError
			if errors.As(e, &collection) {
				problem.Code, problem.Message, problem.Retryable = collection.Code, collection.Message, collection.Retryable
			}
			return Outcome{Status: Failed, Problem: problem}, nil
		}
		request := delivery.Request{Output: a.Input.Output}
		keys := make([]string, len(a.Input.Output.Resources))
		for i, r := range a.Input.Output.Resources {
			var key, collection string
			if e := s.DB.QueryRowContext(ctx, "SELECT task_output_key,json_extract(source_json,'$.collection_key') FROM task_outputs WHERE task_id=? AND attempt=? AND output_key=?", a.TaskID, a.Number, r.Key).Scan(&key, &collection); e != nil {
				return failed(e)
			}
			keys[i] = collection
			request.Sources = append(request.Sources, delivery.Source{Key: r.Key, TaskOutputKey: key})
		}
		request.RequestID = "task-" + keys[0]
		var exists int
		if e := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM deliveries WHERE project_id=? AND request_id=?", a.ProjectID, request.RequestID).Scan(&exists); e != nil {
			return failed(e)
		}
		var receipt delivery.Receipt
		var e error
		if exists > 0 {
			receipt, e = d.Recover(ctx, a.ProjectID, request.RequestID)
		} else {
			paths, err := collect(ctx, a, keys)
			if err != nil {
				return failed(err)
			}
			tx, err := s.DB.BeginTx(ctx, nil)
			if err != nil {
				return failed(err)
			}
			defer tx.Rollback()
			for i, src := range request.Sources {
				// Persist file evidence even if cancellation won while downloading.
				res, err := tx.ExecContext(ctx, "UPDATE task_outputs SET path=?,state='collected' WHERE task_output_key=? AND (path IS NULL OR path=?) AND state<>'delivered'", paths[i], src.TaskOutputKey, paths[i])
				if err != nil {
					return failed(err)
				}
				n, _ := res.RowsAffected()
				if n != 1 {
					return failed(fmt.Errorf("collection identity changed"))
				}
			}
			if err = tx.Commit(); err != nil {
				return failed(err)
			}
			receipt, e = d.Deliver(ctx, a.ProjectID, request)
		}
		if e != nil {
			return failed(e)
		}
		if receipt.Delivery.Status != "completed" {
			code, message, retry := "DELIVERY_PENDING", "Archive receipt is pending; retry the original delivery", true
			if receipt.Delivery.Problem != nil {
				code = receipt.Delivery.Problem.Code
				message = receipt.Delivery.Problem.Message
			}
			if receipt.Delivery.Status == "blocked" {
				retry = false
			}
			return Outcome{Status: Failed, Problem: &Problem{Source: "delivery", Code: code, Message: message, Retryable: retry}}, nil
		}
		return Outcome{Status: Completed}, nil
	}
}
