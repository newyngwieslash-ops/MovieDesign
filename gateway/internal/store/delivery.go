package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"strings"
)

func deliveryTables(ctx context.Context, tx *sql.Tx) (bool, error) {
	var n int
	e := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='deliveries'").Scan(&n)
	return n != 0, e
}

// CheckDeliveryPaths reserves recoverable source/archive paths against file work.
// It must execute in the transaction that prepares the competing journal.
func CheckDeliveryPaths(ctx context.Context, tx *sql.Tx, p, except string, paths ...string) error {
	exists, e := deliveryTables(ctx, tx)
	if e != nil || !exists {
		return e
	}
	rows, e := tx.QueryContext(ctx, `SELECT s.source_path,s.archive_path FROM delivery_sources s JOIN deliveries d USING(project_id,request_id) WHERE s.project_id=? AND s.request_id<>? AND d.status='unknown'`, p, except)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var source, target string
		if e = rows.Scan(&source, &target); e != nil {
			return e
		}
		for _, v := range paths {
			for _, x := range []string{source, target} {
				a, b := strings.ToLower(v), strings.ToLower(x)
				if a != "" && b != "" && (a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")) {
					return ErrRequestPending
				}
			}
		}
	}
	return rows.Err()
}
func checkDeliveryReference(ctx context.Context, tx *sql.Tx, kind, id string) error {
	exists, e := deliveryTables(ctx, tx)
	if e != nil || !exists {
		return e
	}
	var query string
	switch kind {
	case "projects":
		query = "SELECT count(*) FROM deliveries WHERE project_id=?"
	case "composition_types":
		query = "SELECT count(*) FROM deliveries WHERE json_extract(plan_json,'$.TypeID')=?"
	case "compositions":
		query = "SELECT count(*) FROM deliveries WHERE json_extract(plan_json,'$.CompositionID')=?"
	case "resources":
		query = "SELECT count(*) FROM delivery_sources WHERE resource_id=?"
	case "versions":
		query = "SELECT count(*) FROM delivery_sources WHERE version_id=? OR base_version_id=?"
	case "assets":
		query = "SELECT count(*) FROM delivery_sources WHERE asset_id=?"
	default:
		return nil
	}
	args := []any{id}
	if kind == "versions" {
		args = append(args, id)
	}
	var n int
	if e = tx.QueryRowContext(ctx, query, args...).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return protected()
	}
	return nil
}

// checkDeliveryRename protects only effective target name changes while an
// unknown delivery depends on them. Attributes and completed targets stay editable.
func checkDeliveryRename(ctx context.Context, tx *sql.Tx, kind, id, next string) error {
	if e := checkTaskRename(ctx, tx, kind, id, next); e != nil {
		return e
	}
	exists, e := deliveryTables(ctx, tx)
	if e != nil || !exists {
		return e
	}
	var current, field string
	switch kind {
	case "composition_types":
		field = "TypeID"
		e = tx.QueryRowContext(ctx, "SELECT label FROM composition_types WHERE id=?", id).Scan(&current)
	case "compositions":
		field = "CompositionID"
		e = tx.QueryRowContext(ctx, "SELECT name FROM compositions WHERE id=?", id).Scan(&current)
	default:
		return nil
	}
	if e != nil || current == next {
		return e
	}
	var n int
	e = tx.QueryRowContext(ctx, "SELECT count(*) FROM deliveries WHERE status='unknown' AND json_extract(plan_json,?)=?", "$."+field, id).Scan(&n)
	if e != nil {
		return e
	}
	if n > 0 {
		return ErrRequestPending
	}
	return nil
}

// DeliveryTarget is the stable reservation shared by task admission and delivery.
// New identities are allocated but not fabricated as rows.
type DeliveryTarget struct {
	CompositionID  string `json:"composition_id"`
	TypeID         string `json:"type_id"`
	TypeLabel      string `json:"type_label"`
	Name           string `json:"name"`
	NewType        bool   `json:"new_type"`
	NewComposition bool   `json:"new_composition"`
}

func ResolveDeliveryTarget(ctx context.Context, tx *sql.Tx, p, typeID, label, name string) (target DeliveryTarget, e error) {
	if e = CheckTaskTargetReservation(ctx, tx, p, typeID, label, name); e != nil {
		return
	}
	target.Name = name
	if typeID != "" {
		e = tx.QueryRowContext(ctx, "SELECT id,label FROM composition_types WHERE id=? AND project_id=?", typeID, p).Scan(&target.TypeID, &target.TypeLabel)
		if e != nil {
			return
		}
		if label != "" && label != target.TypeLabel {
			return target, invalid("Composition type id and label disagree")
		}
	} else {
		e = tx.QueryRowContext(ctx, "SELECT id,label FROM composition_types WHERE project_id=? AND label=?", p, label).Scan(&target.TypeID, &target.TypeLabel)
		if errors.Is(e, sql.ErrNoRows) {
			target.TypeID = uuid.NewString()
			target.TypeLabel = label
			target.NewType = true
			e = nil
		} else if e != nil {
			return
		}
	}
	rows, e := tx.QueryContext(ctx, "SELECT id FROM compositions WHERE project_id=? AND type_id=? AND name=?", p, target.TypeID, name)
	if e != nil {
		return
	}
	for rows.Next() {
		if target.CompositionID != "" {
			rows.Close()
			return target, invalid("Ambiguous composition target")
		}
		if e = rows.Scan(&target.CompositionID); e != nil {
			rows.Close()
			return
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return
	}
	if target.CompositionID == "" {
		target.CompositionID = uuid.NewString()
		target.NewComposition = true
	}
	return
}
