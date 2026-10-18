package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

type taskBinding struct {
	store.DeliveryTarget
	ResourceID    string `json:"resource_id"`
	VersionID     string `json:"version_id"`
	BaseVersionID string `json:"base_version_id"`
	Ordinal       int64  `json:"ordinal"`
	CollectionKey string `json:"collection_key"`
	TaskID        string
	Attempt       int
	Path          string
}

type taskQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// The opaque key is looked up, never decoded. Both admission and final SQL
// commit enforce the current attempt and authorization, after filesystem I/O.
func taskSource(ctx context.Context, q taskQuery, p, key string) (taskBinding, Output, error) {
	var b taskBinding
	var raw, input, status, state string
	var current int
	e := q.QueryRowContext(ctx, `SELECT o.source_json,t.frozen_input_json,t.status,a.state,t.current_attempt,o.task_id,o.attempt,coalesce(o.path,'') FROM task_outputs o JOIN tasks t ON t.id=o.task_id JOIN task_attempts a ON a.task_id=o.task_id AND a.attempt=o.attempt WHERE o.task_output_key=? AND t.project_id=?`, key, p).Scan(&raw, &input, &status, &state, &current, &b.TaskID, &b.Attempt, &b.Path)
	if errors.Is(e, sql.ErrNoRows) {
		return b, Output{}, missing("Task output not found in project")
	}
	if e != nil {
		return b, Output{}, e
	}
	if e = json.Unmarshal([]byte(raw), &b); e != nil {
		return b, Output{}, e
	}
	var frozen struct {
		Output Output `json:"output"`
	}
	if e = json.Unmarshal([]byte(input), &frozen); e != nil {
		return b, Output{}, e
	}
	authorized := status == "running"
	if status == "cancelled" {
		var receipts int
		e = q.QueryRowContext(ctx, `SELECT count(*) FROM question_receipts r JOIN questions q ON q.id=r.question_id WHERE q.task_id=? AND q.kind='delivery' AND q.state='approved' AND q.input_hash=? AND r.actor='human'`, b.TaskID, fmt.Sprintf("%s:%d", b.TaskID, b.Attempt)).Scan(&receipts)
		if e != nil {
			return b, frozen.Output, e
		}
		authorized = receipts > 0
	}
	if current != b.Attempt || !authorized || state != "collecting" || b.Path == "" || b.Ordinal < 1 {
		return b, frozen.Output, invalid("Task output is not authorized for delivery; local cancellation does not cancel upstream cost")
	}
	return b, frozen.Output, nil
}

func (s *Service) taskSources(ctx context.Context, p string, in Request) ([]Source, map[string]taskBinding, error) {
	sources := append([]Source(nil), in.Sources...)
	bindings := map[string]taskBinding{}
	for i, src := range sources {
		if src.TaskOutputKey == "" {
			continue
		}
		b, output, e := taskSource(ctx, s.db, p, src.TaskOutputKey)
		if e != nil {
			return nil, nil, e
		}
		if i == 0 && in.RequestID != "task-"+b.CollectionKey {
			return nil, nil, invalid("Task delivery requires its owned request identity")
		}
		if i >= len(output.Resources) || output.Resources[i].Key != src.Key {
			return nil, nil, invalid("Task delivery output order changed")
		}
		want, _ := json.Marshal(output)
		got, _ := json.Marshal(in.Output)
		if string(want) != string(got) {
			return nil, nil, invalid("Task delivery must preserve its frozen output declaration")
		}
		var logical string
		if e = s.db.QueryRowContext(ctx, "SELECT output_key FROM task_outputs WHERE task_output_key=?", src.TaskOutputKey).Scan(&logical); e != nil {
			return nil, nil, e
		}
		if logical != src.Key {
			return nil, nil, invalid("Task output key does not match logical slot")
		}
		if len(bindings) > 0 {
			for _, first := range bindings {
				if b.TaskID != first.TaskID || b.Attempt != first.Attempt {
					return nil, nil, invalid("Task delivery cannot mix attempts")
				}
				break
			}
		}
		sources[i].Path = b.Path
		bindings[src.Key] = b
	}
	if len(bindings) > 0 && len(bindings) != len(sources) {
		return nil, nil, invalid("Task delivery cannot mix file sources")
	}
	return sources, bindings, nil
}
