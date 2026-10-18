package store

import (
	"context"
	"database/sql"
)

// Frozen input/output dependencies remain protected even after task completion.
func checkTaskReference(ctx context.Context, tx *sql.Tx, kind, id string) error {
	var table int
	if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='tasks'").Scan(&table); e != nil || table == 0 {
		return e
	}
	var query string
	switch kind {
	case "projects":
		query = "SELECT count(*) FROM tasks WHERE project_id=?"
	case "assets":
		query = "SELECT (SELECT count(*) FROM generation_inputs WHERE asset_id=?)+(SELECT count(*) FROM task_outputs WHERE asset_id=?)"
	case "versions":
		query = `SELECT (SELECT count(*) FROM generation_inputs WHERE version_id=?)+(SELECT count(*) FROM task_outputs WHERE version_id=?)+(SELECT count(*) FROM tasks t,json_each(t.frozen_input_json,'$.output.resources') r WHERE json_extract(r.value,'$.baseVersionId')=?)`
	case "composition_types":
		query = "SELECT count(*) FROM task_outputs WHERE json_extract(source_json,'$.type_id')=?"
	case "compositions":
		query = "SELECT count(*) FROM task_outputs WHERE json_extract(source_json,'$.composition_id')=?"
	case "resources":
		query = "SELECT count(*) FROM task_outputs WHERE json_extract(source_json,'$.resource_id')=?"
	default:
		return nil
	}
	args := []any{id}
	if kind == "assets" {
		args = append(args, id)
	}
	if kind == "versions" {
		args = append(args, id, id)
	}
	var n int
	if e := tx.QueryRowContext(ctx, query, args...).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return protected()
	}
	return nil
}

// CheckTaskAssetPath is also used at the file journal boundary so deleting or
// replacing an unversioned generation input cannot bypass object deletion guards.
func CheckTaskAssetPath(ctx context.Context, tx *sql.Tx, p, path string) error {
	var exists int
	if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='generation_inputs'").Scan(&exists); e != nil || exists == 0 {
		return e
	}
	var n int
	e := tx.QueryRowContext(ctx, `SELECT count(*) FROM assets a JOIN asset_projects ap ON ap.asset_id=a.id WHERE ap.project_id=? AND (lower(a.path)=lower(?) OR substr(lower(a.path),1,length(?)+1)=lower(?)||'/') AND (EXISTS(SELECT 1 FROM generation_inputs g WHERE g.asset_id=a.id) OR EXISTS(SELECT 1 FROM task_outputs o WHERE o.asset_id=a.id))`, p, path, path, path).Scan(&n)
	if e != nil {
		return e
	}
	if n == 0 {
		e = tx.QueryRowContext(ctx, `SELECT count(*) FROM task_outputs o JOIN tasks t ON t.id=o.task_id WHERE t.project_id=? AND (lower(o.path)=lower(?) OR substr(lower(o.path),1,length(?)+1)=lower(?)||'/')`, p, path, path, path).Scan(&n)
		if e != nil {
			return e
		}
	}
	if n > 0 {
		return protected()
	}
	return nil
}
func checkTaskOutputPairs(ctx context.Context, tx *sql.Tx, p string, v any) error {
	switch x := v.(type) {
	case map[string]any:
		if task, ok := x["taskId"].(string); ok {
			if output, ok := x["outputKey"].(string); ok {
				var n int
				e := tx.QueryRowContext(ctx, "SELECT count(*) FROM task_outputs o JOIN tasks t ON t.id=o.task_id WHERE t.project_id=? AND t.id=? AND o.output_key=? AND o.attempt=t.current_attempt", p, task, output).Scan(&n)
				if e != nil {
					return e
				}
				if n != 1 {
					return canvasInvalid("Task output not found in this project")
				}
			}
		}
		for key, value := range x {
			if key != "params" {
				if e := checkTaskOutputPairs(ctx, tx, p, value); e != nil {
					return e
				}
			}
		}
	case []any:
		for _, value := range x {
			if e := checkTaskOutputPairs(ctx, tx, p, value); e != nil {
				return e
			}
		}
	}
	return nil
}
func taskOutputContent(ctx context.Context, tx *sql.Tx, p, cid string) ([]any, error) {
	out := []any{}
	var exists int
	if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='task_outputs'").Scan(&exists); e != nil || exists == 0 {
		return out, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT t.id,o.output_key,json_extract(o.source_json,'$.ordinal'),r.id,v.id,o.state,t.status FROM task_outputs o JOIN tasks t ON t.id=o.task_id LEFT JOIN resources r ON r.id=json_extract(o.source_json,'$.resource_id') AND r.composition_id=? LEFT JOIN versions v ON v.id=o.version_id AND v.resource_id=r.id WHERE t.project_id=? AND o.attempt=t.current_attempt AND json_extract(o.source_json,'$.composition_id')=? ORDER BY t.created_at,t.id,3`, cid, p, cid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var task, key, state, status string
		var ordinal int
		var resource, version *string
		if e = rows.Scan(&task, &key, &ordinal, &resource, &version, &state, &status); e != nil {
			return nil, e
		}
		projected := "generating"
		if state == "delivered" && version != nil {
			projected = "ready"
		} else if status == "failed" || status == "cancelled" {
			projected = "failed"
		}
		out = append(out, map[string]any{"taskId": task, "outputKey": key, "ordinal": ordinal, "resourceId": resource, "versionId": version, "state": projected})
	}
	return out, rows.Err()
}

// CheckTaskTargetReservation prevents a name-based writer from taking a pending
// task's allocated identity. The task-output delivery resolver must consume the
// exact saved identity, not call the name-based resolver again.
func CheckTaskTargetReservation(ctx context.Context, tx *sql.Tx, p, typeID, label, name string) error {
	var exists int
	if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='task_outputs'").Scan(&exists); e != nil || exists == 0 {
		return e
	}
	var n int
	e := tx.QueryRowContext(ctx, `SELECT count(*) FROM task_outputs o JOIN tasks t ON t.id=o.task_id WHERE t.project_id=? AND t.status IN ('accepted','running','waiting_user','unknown') AND ((?<>'' AND json_extract(o.source_json,'$.new_type')=1 AND json_extract(o.source_json,'$.type_label')=?) OR (?<>'' AND json_extract(o.source_json,'$.new_composition')=1 AND json_extract(o.source_json,'$.name')=? AND (json_extract(o.source_json,'$.type_id')=? OR (?<>'' AND json_extract(o.source_json,'$.type_label')=?))))`, p, label, label, name, name, typeID, label, label).Scan(&n)
	if e != nil {
		return e
	}
	if n > 0 {
		return ErrRequestPending
	}
	return nil
}
func checkTaskRename(ctx context.Context, tx *sql.Tx, kind, id, next string) error {
	var field, current string
	switch kind {
	case "compositions":
		field = "composition_id"
		if e := tx.QueryRowContext(ctx, "SELECT name FROM compositions WHERE id=?", id).Scan(&current); e != nil {
			return e
		}
	case "composition_types":
		field = "type_id"
		if e := tx.QueryRowContext(ctx, "SELECT label FROM composition_types WHERE id=?", id).Scan(&current); e != nil {
			return e
		}
	default:
		return nil
	}
	if current == next {
		return nil
	}
	var exists int
	if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='task_outputs'").Scan(&exists); e != nil || exists == 0 {
		return e
	}
	var n int
	e := tx.QueryRowContext(ctx, "SELECT count(*) FROM task_outputs o JOIN tasks t ON t.id=o.task_id WHERE t.status IN ('accepted','running','waiting_user','unknown') AND json_extract(o.source_json,?)=?", "$."+field, id).Scan(&n)
	if e != nil {
		return e
	}
	if n > 0 {
		return ErrRequestPending
	}
	return nil
}
