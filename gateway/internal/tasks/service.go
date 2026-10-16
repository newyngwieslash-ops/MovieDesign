package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

type Service struct {
	DB       *sql.DB
	Events   *events.Service
	Executor *Executor
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	stopping bool
	work     sync.WaitGroup
	active   atomic.Int64
}

func NewService(db *sql.DB, e *events.Service, executor *Executor) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{DB: db, Events: e, Executor: executor, ctx: ctx, cancel: cancel}
}
func (s *Service) begin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return unavailable()
	}
	s.work.Add(1)
	s.active.Add(1)
	return nil
}
func (s *Service) end()          { s.active.Add(-1); s.work.Done() }
func (s *Service) Stop()         { s.mu.Lock(); s.stopping = true; s.cancel(); s.mu.Unlock() }
func (s *Service) Close()        { s.Stop(); s.work.Wait() }
func (s *Service) Active() int64 { return s.active.Load() }
func scope(p, id string) error {
	if e := (store.Scope{ProjectID: p, Operation: "tasks", RequestID: id}).Validate(); e != nil || p == "_app" {
		return invalid("Invalid project or identifier")
	}
	return nil
}

type rowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func query(ctx context.Context, q rowQuery, p, id string) (v TaskView, attempt int, session string, err error) {
	var raw string
	err = q.QueryRowContext(ctx, "SELECT view_json,current_attempt,session_id FROM tasks WHERE project_id=? AND id=?", p, id).Scan(&raw, &attempt, &session)
	if errors.Is(err, sql.ErrNoRows) {
		err = &store.DomainError{Status: 404, Code: "not_found", Message: "Task not found in this project"}
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &v)
	}
	return
}
func (s *Service) Query(ctx context.Context, p, id string) (TaskView, error) {
	if e := scope(p, id); e != nil {
		return TaskView{}, e
	}
	v, _, _, e := query(ctx, s.DB, p, id)
	return v, e
}

// ReplaySubmission lets capability admission replay an original public request
// before looking up mutable providers/capabilities. A miss authorizes no work.
func (s *Service) ReplaySubmission(ctx context.Context, p, key string, wire []byte) (TaskView, bool, error) {
	var v TaskView
	if e := scope(p, key); e != nil {
		return v, false, e
	}
	canonical, e := store.CanonicalJSON(wire)
	if e != nil {
		return v, false, invalid("Invalid original wire input")
	}
	var input, state, receipt string
	e = s.DB.QueryRowContext(ctx, "SELECT input_json,state,coalesce(response_json,'') FROM write_requests WHERE project_id=? AND operation='tasks.submit' AND request_id=?", p, key).Scan(&input, &state, &receipt)
	if errors.Is(e, sql.ErrNoRows) {
		return v, false, nil
	}
	if e != nil {
		return v, false, e
	}
	if input != string(canonical) {
		return v, true, store.ErrRequestConflict
	}
	if state != "completed" {
		return v, true, store.ErrRequestPending
	}
	e = json.Unmarshal([]byte(receipt), &v)
	return v, true, e
}
func (s *Service) Wait(ctx context.Context, p, id string) (TaskView, error) {
	ch, done := s.Events.Subscribe()
	defer done()
	for {
		v, e := s.Query(ctx, p, id)
		if e != nil {
			return v, e
		}
		source := ""
		if v.Problem != nil {
			source = v.Problem.Source
		}
		if SettlesAgentWait(v.Status, source) {
			return v, nil
		}
		select {
		case <-ctx.Done():
			return TaskView{}, ctx.Err()
		case _, ok := <-ch:
			if !ok {
				return TaskView{}, unavailable()
			}
		case <-s.ctx.Done():
			return TaskView{}, unavailable()
		}
	}
}
func (s *Service) Changed(ctx context.Context, p, id string, after int64) (TaskView, error) {
	if after < 1 {
		return TaskView{}, invalid("Invalid revision")
	}
	ch, done := s.Events.Subscribe()
	defer done()
	for {
		v, e := s.Query(ctx, p, id)
		if e != nil {
			return v, e
		}
		if after > v.Revision {
			return v, invalid("Revision is in the future")
		}
		if after < v.Revision || Terminal(v.Status) {
			return v, nil
		}
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case _, ok := <-ch:
			if !ok {
				return v, unavailable()
			}
		case <-s.ctx.Done():
			return v, unavailable()
		}
	}
}
func validateInput(i Input) error {
	if i.CapabilityID == "" || len(i.CapabilityID) > 128 || len(i.Prompt) > 65536 || !utf8.ValidString(i.Prompt) || len(i.References) > 100 || len(i.SessionID) > 128 {
		return invalid("Invalid task input")
	}
	if i.SessionID != "" {
		if e := scope("session", i.SessionID); e != nil {
			return e
		}
	}
	if _, e := store.CanonicalJSON(i.Params); e != nil {
		return invalid("params must be an object")
	}
	if i.Provider.Config.ID == "" || !i.Provider.Config.Enabled {
		return invalid("A frozen enabled provider is required")
	}
	o := i.Output
	if len(o.Resources) < 1 || len(o.Resources) > 50 || len(o.Composition.Placements) != len(o.Resources) || strings.TrimSpace(o.Composition.Name) == "" || len(o.Composition.Name) > 256 || (o.Composition.Type.ID == "") == (o.Composition.Type.Label == "") {
		return invalid("Invalid declared output")
	}
	keys := map[string]bool{}
	for _, r := range o.Resources {
		if scope("output", r.Key) != nil || keys[r.Key] || strings.TrimSpace(r.Name) == "" || len(r.Name) > 256 || r.Mode != "create" && r.Mode != "new_version" || (r.Mode == "new_version") != (r.BaseVersionID != "") {
			return invalid("Invalid output resource")
		}
		keys[r.Key] = true
	}
	for _, p := range o.Composition.Placements {
		if !keys[p.Key] || strings.TrimSpace(p.Role) == "" || len(p.Role) > 128 {
			return invalid("Invalid output placement")
		}
		delete(keys, p.Key)
	}
	for _, r := range i.References {
		if scope("input", r.AssetID) != nil || r.VersionID != "" && scope("input", r.VersionID) != nil || r.Role == "" || len(r.Role) > 128 {
			return invalid("Invalid reference")
		}
	}
	return nil
}
func validateOwnership(ctx context.Context, tx *sql.Tx, p string, i Input) error {
	var id string
	if e := tx.QueryRowContext(ctx, "SELECT id FROM projects WHERE id=?", p).Scan(&id); e != nil {
		return invalid("Project does not exist")
	}
	for _, r := range i.References {
		if e := store.CheckAssetFilePending(ctx, tx, r.AssetID); e != nil {
			return e
		}
		var n int
		e := tx.QueryRowContext(ctx, "SELECT count(*) FROM assets a JOIN asset_projects ap ON ap.asset_id=a.id WHERE a.id=? AND ap.project_id=? AND a.status='active'", r.AssetID, p).Scan(&n)
		if e != nil {
			return e
		}
		if n != 1 {
			return invalid("Reference asset not available in this project")
		}
		if r.VersionID != "" {
			e = tx.QueryRowContext(ctx, "SELECT count(*) FROM versions v JOIN resources r ON r.id=v.resource_id JOIN compositions c ON c.id=r.composition_id WHERE v.id=? AND v.asset_id=? AND c.project_id=?", r.VersionID, r.AssetID, p).Scan(&n)
			if e != nil {
				return e
			}
			if n != 1 {
				return invalid("Reference version does not own asset in this project")
			}
		}
	}
	if i.Output.Composition.Type.ID != "" {
		if e := tx.QueryRowContext(ctx, "SELECT id FROM composition_types WHERE id=? AND project_id=?", i.Output.Composition.Type.ID, p).Scan(&id); e != nil {
			return invalid("Output type not found in this project")
		}
	}
	for _, r := range i.Output.Resources {
		if r.BaseVersionID != "" {
			if e := tx.QueryRowContext(ctx, "SELECT v.id FROM versions v JOIN resources r ON r.id=v.resource_id JOIN compositions c ON c.id=r.composition_id WHERE v.id=? AND c.project_id=?", r.BaseVersionID, p).Scan(&id); e != nil {
				return invalid("Output base version not found in this project")
			}
		}
	}
	return nil
}
func declarations(i Input) []Output {
	out := []Output{}
	roles := map[string]string{}
	for _, p := range i.Output.Composition.Placements {
		roles[p.Key] = p.Role
	}
	for _, r := range i.Output.Resources {
		out = append(out, Output{TaskOutputKey: uuid.NewString(), OutputKey: r.Key, Role: roles[r.Key], State: "declared"})
	}
	return out
}
func insertAttempt(ctx context.Context, tx *sql.Tx, v TaskView, attempt int) error {
	if _, e := tx.ExecContext(ctx, "INSERT INTO task_attempts(task_id,attempt,state,created_at)VALUES(?,?,'billing',?)", v.ID, attempt, time.Now().UnixMilli()); e != nil {
		return e
	}
	for _, o := range v.Outputs {
		if _, e := tx.ExecContext(ctx, "INSERT INTO task_outputs(task_output_key,task_id,attempt,output_key,role,state)VALUES(?,?,?,?,?,?)", o.TaskOutputKey, v.ID, attempt, o.OutputKey, o.Role, o.State); e != nil {
			return e
		}
	}
	return nil
}
func readOutputs(ctx context.Context, tx *sql.Tx, id string, attempt int) ([]Output, error) {
	rows, e := tx.QueryContext(ctx, "SELECT task_output_key,output_key,role,state,asset_id,version_id FROM task_outputs WHERE task_id=? AND attempt=? ORDER BY json_extract(source_json,'$.ordinal'),output_key", id, attempt)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Output{}
	for rows.Next() {
		var o Output
		if e = rows.Scan(&o.TaskOutputKey, &o.OutputKey, &o.Role, &o.State, &o.AssetID, &o.VersionID); e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func billing(v *TaskView) {
	v.Status = WaitingUser
	v.Problem = &Problem{Source: "billing", Code: "BILLING_APPROVAL_REQUIRED", Message: "Human billing approval is required"}
	v.Progress = Progress{Phase: "paused"}
	v.Actions = []string{"cancel"}
	v.Billing = Billing{}
}
func (s *Service) Submit(ctx context.Context, p string, i Input) (TaskView, error) {
	var v TaskView
	if e := s.begin(); e != nil {
		return v, e
	}
	defer s.end()
	ctx = context.WithoutCancel(ctx)
	if e := scope(p, i.RequestID); e != nil {
		return v, e
	}
	raw, e := inputJSON(i)
	if e != nil {
		return v, e
	}
	identity := raw
	if i.OriginalWire != nil {
		identity, e = store.CanonicalJSON(i.OriginalWire)
		if e != nil {
			return v, invalid("Invalid original wire input")
		}
	}
	result, e := store.WriteRequest(ctx, s.DB, store.Scope{ProjectID: p, Operation: "tasks.submit", RequestID: i.RequestID}, identity, func(tx *sql.Tx) ([]byte, error) {
		if e := validateInput(i); e != nil {
			return nil, e
		}
		if e := validateOwnership(ctx, tx, p, i); e != nil {
			return nil, e
		}
		target, e := store.ResolveDeliveryTarget(ctx, tx, p, i.Output.Composition.Type.ID, i.Output.Composition.Type.Label, i.Output.Composition.Name)
		if e != nil {
			return nil, e
		}
		v = TaskView{ID: uuid.NewString(), CapabilityID: i.CapabilityID, Prompt: i.Prompt, Revision: 1, Outputs: declarations(i)}
		billing(&v)
		view, _ := json.Marshal(v)
		provider, _ := json.Marshal(i.Provider)
		now := time.Now().UnixMilli()
		if _, e := tx.ExecContext(ctx, "INSERT INTO tasks(id,project_id,session_id,request_id,status,revision,current_attempt,frozen_input_json,provider_json,view_json,created_at,updated_at)VALUES(?,?,?,?,?,1,1,?,?,?,?,?)", v.ID, p, i.SessionID, i.RequestID, v.Status, string(raw), string(provider), string(view), now, now); e != nil {
			return nil, e
		}
		if e := insertAttempt(ctx, tx, v, 1); e != nil {
			return nil, e
		}
		targetJSON, _ := json.Marshal(target)
		for _, r := range i.Output.Resources {
			var binding map[string]any
			json.Unmarshal(targetJSON, &binding)
			resourceID := uuid.NewString()
			if r.BaseVersionID != "" {
				var cid string
				if e := tx.QueryRowContext(ctx, "SELECT r.composition_id,r.id FROM versions v JOIN resources r ON r.id=v.resource_id WHERE v.id=?", r.BaseVersionID).Scan(&cid, &resourceID); e != nil {
					return nil, e
				}
				if cid != target.CompositionID {
					return nil, invalid("Output base version is not in declared composition")
				}
			}
			binding["resource_id"], binding["base_version_id"] = resourceID, r.BaseVersionID
			binding["version_id"] = uuid.NewString()
			// Reserve per-resource history order once; retries retain this slot.
			var ordinal int64
			if e = tx.QueryRowContext(ctx, `SELECT coalesce(max(ordinal),0)+1 FROM (SELECT ordinal FROM canvas_version_state WHERE resource_id=? UNION ALL SELECT CAST(json_extract(source_json,'$.ordinal') AS INTEGER) FROM task_outputs WHERE json_extract(source_json,'$.resource_id')=?)`, resourceID, resourceID).Scan(&ordinal); e != nil {
				return nil, e
			}
			binding["ordinal"] = ordinal
			// Existing version inserts use this same high-water mark. The eventual
			// task delivery assigns the reserved ordinal inside its commit transaction.
			if _, e = tx.ExecContext(ctx, "UPDATE canvas_resource_state SET next_ordinal=max(next_ordinal,?) WHERE resource_id=?", ordinal+1, resourceID); e != nil {
				return nil, e
			}
			boundJSON, _ := json.Marshal(binding)
			if _, e := tx.ExecContext(ctx, "UPDATE task_outputs SET source_json=? WHERE task_id=? AND attempt=1 AND output_key=?", string(boundJSON), v.ID, r.Key); e != nil {
				return nil, e
			}
		}
		for n, r := range i.References {
			var version any
			if r.VersionID != "" {
				version = r.VersionID
			}
			if _, e := tx.ExecContext(ctx, "INSERT INTO generation_inputs(task_id,ordinal,asset_id,version_id,role)VALUES(?,?,?,?,?)", v.ID, n, r.AssetID, version, r.Role); e != nil {
				return nil, e
			}
		}
		if _, e := tx.ExecContext(ctx, "UPDATE canvas_composition_state SET revision=revision+1 WHERE composition_id=?", target.CompositionID); e != nil {
			return nil, e
		}
		if e := events.Append(ctx, tx, p, i.SessionID, v.ID, "task_changed", view); e != nil {
			return nil, e
		}
		return view, nil
	})
	if e == nil {
		e = json.Unmarshal(result, &v)
		s.Events.Notify()
	}
	return v, e
}
func (s *Service) save(ctx context.Context, tx *sql.Tx, p, session string, v *TaskView, attempt int) error {
	old := v.Revision
	v.Revision++
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	res, e := tx.ExecContext(ctx, "UPDATE tasks SET status=?,revision=revision+1,current_attempt=?,view_json=?,updated_at=? WHERE id=? AND project_id=? AND revision=?", v.Status, attempt, string(b), time.Now().UnixMilli(), v.ID, p, old)
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
	if _, e = tx.ExecContext(ctx, `UPDATE canvas_composition_state SET revision=revision+1 WHERE composition_id IN (SELECT json_extract(source_json,'$.composition_id') FROM task_outputs WHERE task_id=?)`, v.ID); e != nil {
		return e
	}
	return events.Append(ctx, tx, p, session, v.ID, "task_changed", b)
}
func (s *Service) Resolve(ctx context.Context, p, id string, i ResolveInput) (TaskView, error) {
	var v TaskView
	if e := s.begin(); e != nil {
		return v, e
	}
	defer s.end()
	ctx = context.WithoutCancel(ctx)
	if e := scope(p, id); e != nil {
		return v, e
	}
	if e := scope(p, i.RequestID); e != nil {
		return v, e
	}
	if i.ExpectedRevision < 1 || i.ExpectedRevision >= 9007199254740991 {
		return v, invalid("Invalid expected_revision")
	}
	raw, _ := json.Marshal(i)
	if i.raw != nil {
		var original ResolveInput
		if e := json.Unmarshal(i.raw, &original); e != nil {
			return v, e
		}
		b, _ := json.Marshal(original)
		if string(b) != string(raw) {
			return v, invalid("Decoded resolve input was modified")
		}
		raw = i.raw
	}
	result, e := store.WriteRequest(ctx, s.DB, store.Scope{ProjectID: p, Operation: "tasks.resolve." + id, RequestID: i.RequestID}, raw, func(tx *sql.Tx) ([]byte, error) {
		view, attempt, session, e := query(ctx, tx, p, id)
		if e != nil {
			return nil, e
		}
		v = view
		if v.Revision != i.ExpectedRevision {
			return nil, conflict()
		}
		allowed := false
		for _, a := range v.Actions {
			if a == i.Action {
				allowed = true
			}
		}
		if !allowed {
			return nil, conflict()
		}
		switch i.Action {
		case "cancel":
			if _, e = tx.ExecContext(ctx, "UPDATE task_attempts SET state='cancelled' WHERE task_id=? AND attempt=? AND (submit_started_at IS NULL OR state='collection_pending')", id, attempt); e != nil {
				return nil, e
			}
			v.Status = Cancelled
			v.Problem = nil
			v.Actions = []string{}
			v.Progress = Progress{Phase: "stopped"}
		case "retry":
			if v.Problem != nil && v.Problem.Source == "delivery" {
				var result sql.NullString
				if e = tx.QueryRowContext(ctx, "SELECT result_json FROM task_attempts WHERE task_id=? AND attempt=?", id, attempt).Scan(&result); e != nil {
					return nil, e
				}
				if !result.Valid {
					return nil, conflict()
				}
				if _, e = tx.ExecContext(ctx, "UPDATE task_attempts SET state='collection_pending' WHERE task_id=? AND attempt=?", id, attempt); e != nil {
					return nil, e
				}
				v.Status = Accepted
				v.Problem = nil
				v.Actions = []string{"cancel"}
				v.Progress = Progress{Phase: "pending"}
				break
			}
			var frozen string
			if e = tx.QueryRowContext(ctx, "SELECT frozen_input_json FROM tasks WHERE id=?", id).Scan(&frozen); e != nil {
				return nil, e
			}
			var input Input
			if e = json.Unmarshal([]byte(frozen), &input); e != nil {
				return nil, e
			}
			v.Outputs = declarations(input)
			attempt++
			billing(&v)
			if e = insertAttempt(ctx, tx, v, attempt); e != nil {
				return nil, e
			}
			if _, e = tx.ExecContext(ctx, "UPDATE task_outputs SET source_json=(SELECT previous.source_json FROM task_outputs previous WHERE previous.task_id=task_outputs.task_id AND previous.attempt=? AND previous.output_key=task_outputs.output_key) WHERE task_id=? AND attempt=?", attempt-1, id, attempt); e != nil {
				return nil, e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE task_outputs SET (state,asset_id,version_id)=(SELECT previous.state,previous.asset_id,previous.version_id FROM task_outputs previous WHERE previous.task_id=task_outputs.task_id AND previous.attempt=? AND previous.output_key=task_outputs.output_key) WHERE task_id=? AND attempt=? AND EXISTS(SELECT 1 FROM task_outputs previous WHERE previous.task_id=task_outputs.task_id AND previous.attempt=? AND previous.output_key=task_outputs.output_key AND previous.state='delivered')`, attempt-1, id, attempt, attempt-1); e != nil {
				return nil, e
			}
			if v.Outputs, e = readOutputs(ctx, tx, id, attempt); e != nil {
				return nil, e
			}
		default:
			return nil, invalid("Unsupported task action")
		}
		if e = s.save(ctx, tx, p, session, &v, attempt); e != nil {
			return nil, e
		}
		return json.Marshal(v)
	})
	if e == nil {
		e = json.Unmarshal(result, &v)
		s.Events.Notify()
	}
	return v, e
}
