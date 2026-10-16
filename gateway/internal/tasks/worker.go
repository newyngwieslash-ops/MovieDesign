package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"time"
)

type Attempt struct {
	TaskID     string
	ProjectID  string
	Number     int
	UpstreamID string
	Input      Input
	Result     json.RawMessage
	Provider   providers.FrozenProvider
}
type Outcome struct {
	Status      Status
	Problem     *Problem
	Result      []byte
	NotAccepted bool
}

// Submit must checkpoint the upstream identity as soon as obtained. Errors alone
// never prove rejection. Query is read-only and must never submit generation.
type Executor struct {
	Submit  func(context.Context, Attempt, func(string) error) (Outcome, error)
	Query   func(context.Context, Attempt) (Outcome, error)
	Collect func(context.Context, Attempt) (Outcome, error)
}

func loadAttempt(ctx context.Context, q rowQuery, p, id string) (a Attempt, state string, started bool, e error) {
	var input, provider, result string
	var stamp sql.NullInt64
	a.TaskID = id
	a.ProjectID = p
	e = q.QueryRowContext(ctx, "SELECT t.current_attempt,t.frozen_input_json,t.provider_json,a.state,coalesce(a.upstream_id,''),a.submit_started_at,coalesce(a.result_json,'null') FROM tasks t JOIN task_attempts a ON a.task_id=t.id AND a.attempt=t.current_attempt WHERE t.id=? AND t.project_id=?", id, p).Scan(&a.Number, &input, &provider, &state, &a.UpstreamID, &stamp, &result)
	if e != nil {
		return
	}
	started = stamp.Valid
	a.Result = json.RawMessage(result)
	if e = json.Unmarshal([]byte(input), &a.Input); e == nil {
		e = json.Unmarshal([]byte(provider), &a.Provider)
	}
	a.Input.Provider = a.Provider
	return
}

// Dispatch registers work before returning to the HTTP caller so shutdown cannot
// miss a scheduled goroutine. Run's SQL claim permits only approved, unsent work.
func (s *Service) Dispatch(p, id string) error {
	if e := s.begin(); e != nil {
		return e
	}
	go func() { defer s.end(); _ = s.Run(context.Background(), p, id) }()
	return nil
}
func (s *Service) Run(_ context.Context, p, id string) error {
	if s.Executor == nil {
		return nil
	}
	if e := s.begin(); e != nil {
		return e
	}
	defer s.end()
	ctx := s.ctx
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	v, attempt, session, e := query(ctx, tx, p, id)
	if e != nil {
		return e
	}
	a, state, started, e := loadAttempt(ctx, tx, p, id)
	if e != nil {
		return e
	}
	collect := state == "collection_pending" && s.Executor.Collect != nil
	if Terminal(v.Status) || !collect && (state != "approved" || started || s.Executor.Submit == nil) {
		return nil
	}
	var res sql.Result
	if collect {
		res, e = tx.ExecContext(ctx, "UPDATE task_attempts SET state='collecting' WHERE task_id=? AND attempt=? AND state='collection_pending'", id, attempt)
	} else {
		res, e = tx.ExecContext(ctx, "UPDATE task_attempts SET state='submitting',submit_started_at=? WHERE task_id=? AND attempt=? AND state='approved' AND submit_started_at IS NULL", time.Now().UnixMilli(), id, attempt)
	}
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil || n != 1 {
		return e
	}
	v.Status = Running
	v.Problem = nil
	v.Actions = []string{"cancel"}
	v.Progress = Progress{Phase: "active"}
	if e = s.save(ctx, tx, p, session, &v, attempt); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.Events.Notify()
	if collect {
		outcome, collectErr := s.Executor.Collect(ctx, a)
		return s.finish(a, outcome, collectErr)
	}
	outcome, submitErr := s.Executor.Submit(ctx, a, func(upstream string) error { return s.checkpoint(a, upstream) })
	return s.finish(a, outcome, submitErr)
}
func (s *Service) checkpoint(a Attempt, upstream string) error {
	if upstream == "" || len(upstream) > 2048 {
		return invalid("Invalid upstream identity")
	}
	ctx := context.Background()
	res, e := s.DB.ExecContext(ctx, "UPDATE task_attempts SET upstream_id=?,accepted_at=?,state='submitted' WHERE task_id=? AND attempt=? AND (upstream_id IS NULL OR upstream_id=?) AND submit_started_at IS NOT NULL", upstream, time.Now().UnixMilli(), a.TaskID, a.Number, upstream)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return conflict()
	}
	return nil
}
func (s *Service) finish(a Attempt, out Outcome, callErr error) error {
	ctx := context.Background()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	v, attempt, session, e := query(ctx, tx, a.ProjectID, a.TaskID)
	if e != nil {
		return e
	}
	if attempt != a.Number {
		return conflict()
	}
	persisted, state, _, e := loadAttempt(ctx, tx, a.ProjectID, a.TaskID)
	if e != nil {
		return e
	}
	status := out.Status
	if callErr != nil || status == "" {
		status = Unknown
		if state == "collecting" && string(persisted.Result) != "null" {
			status = Failed
			out.Problem = &Problem{Source: "delivery", Code: "COLLECTION_INTERRUPTED", Message: "Saved result collection was interrupted; retry collection", Retryable: true}
		} else if out.NotAccepted && persisted.UpstreamID == "" {
			status = Failed
		}
	}
	if status != Running && status != WaitingUser && status != Unknown && status != Failed && status != Completed {
		return invalid("Invalid executor outcome")
	}
	if len(out.Result) > 0 && !json.Valid(out.Result) {
		return invalid("Invalid executor result")
	}
	problem := out.Problem
	if status == Unknown {
		problem = &Problem{Source: "provider", Code: "PROVIDER_OUTCOME_UNKNOWN", Message: "Submission outcome is unknown; do not resubmit"}
		if out.Problem != nil {
			problem.Upstream = out.Problem.Upstream
		}
	} else if status == Failed && problem == nil {
		problem = &Problem{Source: "provider", Code: "PROVIDER_REJECTED", Message: "Provider did not accept generation", Retryable: out.NotAccepted}
	}
	if status == WaitingUser && problem == nil {
		return invalid("Waiting outcome requires a problem")
	}
	if status == Completed {
		var pending int
		if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM task_outputs WHERE task_id=? AND attempt=? AND (state<>'delivered' OR asset_id IS NULL OR version_id IS NULL)", a.TaskID, a.Number).Scan(&pending); e != nil {
			return e
		}
		if pending > 0 {
			return invalid("Task outputs have not been delivered")
		}
	}
	var problemJSON any
	if problem != nil {
		encoded, _ := json.Marshal(problem)
		problemJSON = string(encoded)
	}
	var result any
	if len(out.Result) > 0 {
		result = string(out.Result)
	}
	// Keep the first immutable error/result evidence. Cancellation only stops local
	// work; late upstream facts are still recorded on the same attempt.
	if _, e = tx.ExecContext(ctx, "UPDATE task_attempts SET state=?,error_json=coalesce(error_json,?),result_json=coalesce(result_json,?) WHERE task_id=? AND attempt=?", status, problemJSON, result, a.TaskID, a.Number); e != nil {
		return e
	}
	if !Terminal(v.Status) {
		if v.Outputs, e = readOutputs(ctx, tx, a.TaskID, a.Number); e != nil {
			return e
		}
		v.Status = status
		v.Problem = problem
		v.Actions = []string{}
		v.Progress = Progress{Phase: "paused"}
		if !Terminal(status) {
			v.Actions = []string{"cancel"}
		}
		if status == Failed && problem != nil && problem.Retryable {
			v.Actions = []string{"retry"}
		}
		if status == Running {
			v.Progress.Phase = "active"
		}
		if status == Completed {
			percent := 100
			v.Progress = Progress{Phase: "done", Percent: &percent}
		}
		if e = s.save(ctx, tx, a.ProjectID, session, &v, attempt); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e == nil {
		s.Events.Notify()
	}
	return e
}

// Recover runs once during process startup after adapters have been installed.
// It never calls Submit, including for approved-but-unsent tasks.
func (s *Service) Recover(_ context.Context) error {
	if e := s.begin(); e != nil {
		return e
	}
	defer s.end()
	ctx := s.ctx
	rows, e := s.DB.QueryContext(ctx, "SELECT t.id,t.project_id FROM tasks t JOIN task_attempts a ON a.task_id=t.id AND a.attempt=t.current_attempt WHERE t.status IN ('running','unknown') OR (t.status='cancelled' AND a.state IN ('submitting','submitted','running','unknown','collection_pending','collecting'))")
	if e != nil {
		return e
	}
	var ids [][2]string
	for rows.Next() {
		var pair [2]string
		if e = rows.Scan(&pair[0], &pair[1]); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, pair)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, pair := range ids {
		a, _, started, e := loadAttempt(ctx, s.DB, pair[1], pair[0])
		if e != nil {
			return e
		}
		// Saved output is collection work, regardless of whether the provider uses
		// an upstream ID. Startup exposes an explicit retry and performs no I/O.
		// finish also drains interrupted cancelled collection without reviving it.
		if string(a.Result) != "null" {
			if e = s.finish(a, Outcome{Status: Failed, Problem: &Problem{Source: "delivery", Code: "COLLECTION_INTERRUPTED", Message: "Saved result collection was interrupted; retry collection", Retryable: true}}, nil); e != nil {
				return e
			}
			continue
		}
		if !started {
			continue
		}
		if a.UpstreamID != "" && s.Executor != nil && s.Executor.Query != nil {
			out, queryErr := s.Executor.Query(ctx, a)
			if e = s.finish(a, out, queryErr); e != nil {
				return e
			}
		} else {
			if e = s.finish(a, Outcome{Status: Unknown}, errors.New("interrupted submission")); e != nil {
				return e
			}
		}
	}
	return nil
}
