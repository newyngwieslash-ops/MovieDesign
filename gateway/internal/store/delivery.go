package store

import (
	"context"
	"database/sql"
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
