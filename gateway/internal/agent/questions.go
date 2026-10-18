package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
)

func (c *Chat) syncQuestions(ctx context.Context, s ToolScope) error {
	pending, e := c.Host.pendingQuestions(ctx)
	if e != nil {
		return e
	}
	ctx = context.WithoutCancel(ctx)
	present := map[string]bool{}
	for _, q := range pending {
		if q.SessionID != s.NativeSessionID {
			continue
		}
		present[q.ID] = true
		// Reserve native mapping before Ask's durable card publication. The local ID
		// can be recovered using the unique native requestId if interrupted after Ask.
		raw, _ := json.Marshal(q)
		if _, e = c.DB.ExecContext(ctx, "INSERT INTO native_questions(native_id,turn_id,native_json)VALUES(?,?,?) ON CONFLICT(native_id)DO NOTHING", q.ID, s.TurnID, string(raw)); e != nil {
			return e
		}
		var cardID sql.NullString
		var send string
		if e = c.DB.QueryRowContext(ctx, "SELECT question_id,send_state FROM native_questions WHERE native_id=? AND turn_id=?", q.ID, s.TurnID).Scan(&cardID, &send); e != nil {
			return e
		}
		if !cardID.Valid {
			card, e := c.Turns.GateQuestion(ctx, s, q, c.Questions, c.Host.RejectQuestion)
			if e != nil {
				return e
			}
			if _, e = c.DB.ExecContext(ctx, "UPDATE native_questions SET question_id=? WHERE native_id=?", card.ID, q.ID); e != nil {
				return e
			}
			cardID.String = card.ID
			cardID.Valid = true
		}
		if send != "pending" {
			continue
		}
		var receiptRaw string
		e = c.DB.QueryRowContext(ctx, "SELECT response_json FROM question_receipts WHERE question_id=? AND actor='human'", cardID.String).Scan(&receiptRaw)
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			return e
		}
		var receipt questions.Receipt
		if e = json.Unmarshal([]byte(receiptRaw), &receipt); e != nil {
			return e
		}
		result, e := c.DB.ExecContext(ctx, "UPDATE native_questions SET send_state='sending',receipt_id=? WHERE native_id=? AND send_state='pending'", receipt.ID, q.ID)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			continue
		}
		if receipt.State == "rejected" {
			e = c.Host.RejectQuestion(ctx, q.ID)
		} else {
			_, e = c.Host.request(ctx, "POST", "/question/"+url.PathEscape(q.ID)+"/reply", map[string]any{"answers": receipt.Answers})
		}
		state := "sent"
		if e != nil {
			state = "unknown"
		}
		if _, e = c.DB.ExecContext(context.Background(), "UPDATE native_questions SET send_state=? WHERE native_id=?", state, q.ID); e != nil {
			return e
		}
	}
	rows, e := c.DB.QueryContext(ctx, "SELECT native_id,send_state FROM native_questions WHERE turn_id=?", s.TurnID)
	if e != nil {
		return e
	}
	type record struct{ id, state string }
	var missing []record
	for rows.Next() {
		var r record
		if e = rows.Scan(&r.id, &r.state); e != nil {
			rows.Close()
			return e
		}
		if !present[r.id] && r.state != "sent" {
			missing = append(missing, r)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, r := range missing {
		// Absence in native memory is never proof of a human answer or successful send.
		if _, e = c.DB.ExecContext(ctx, "UPDATE native_questions SET send_state='unknown' WHERE native_id=?", r.id); e != nil {
			return e
		}
	}
	return nil
}
