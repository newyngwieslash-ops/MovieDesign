package tasks

import (
	"context"
	"database/sql"
	"fmt"
)

// Adoption authorizes only collection of the same saved attempt. The task stays
// cancelled; no new provider attempt or billing approval is created.
func (s *Service) AdoptQuestion(ctx context.Context, tx *sql.Tx, p, session, id string, revision int64, binding string, reject bool) error {
	v, attempt, owned, e := query(ctx, tx, p, id)
	if e != nil {
		return e
	}
	if v.Status != Cancelled || v.Revision != revision || owned != session || binding != fmt.Sprintf("%s:%d", id, attempt) {
		return conflict()
	}
	if reject {
		return nil
	}
	var result sql.NullString
	if e = tx.QueryRowContext(ctx, "SELECT result_json FROM task_attempts WHERE task_id=? AND attempt=?", id, attempt).Scan(&result); e != nil {
		return e
	}
	if !result.Valid {
		return conflict()
	}
	_, e = tx.ExecContext(ctx, "UPDATE task_attempts SET state='adoption_pending' WHERE task_id=? AND attempt=?", id, attempt)
	return e
}
