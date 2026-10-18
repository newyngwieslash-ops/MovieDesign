package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
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
	return c.retireMissingQuestions(ctx, s, present)
}

// Reconciliation of old mappings is read-only toward native: no GateQuestion,
// receipt claim, reply or reject can run for an interrupted/closed epoch.
func (c *Chat) reconcileInactiveQuestions() {
	rows, e := c.DB.Query(`SELECT DISTINCT t.id,t.epoch,s.project_id,s.id,s.native_session_id FROM native_questions n JOIN chat_turns t ON t.id=n.turn_id JOIN chat_sessions s ON s.id=t.session_id WHERE t.state!='active' AND n.send_state!='sent'`)
	if e != nil {
		return
	}
	var scopes []ToolScope
	for rows.Next() {
		var s ToolScope
		if rows.Scan(&s.TurnID, &s.Epoch, &s.ProjectID, &s.SessionID, &s.NativeSessionID) == nil {
			scopes = append(scopes, s)
		}
	}
	rows.Close()
	if len(scopes) == 0 {
		return
	}
	pending, e := c.Host.pendingQuestions(c.ctx)
	if e != nil {
		return
	}
	for _, s := range scopes {
		present := map[string]bool{}
		for _, q := range pending {
			if q.SessionID == s.NativeSessionID {
				present[q.ID] = true
			}
		}
		_ = c.retireMissingQuestions(context.WithoutCancel(c.ctx), s, present)
	}
}
func (c *Chat) retireMissingQuestions(ctx context.Context, s ToolScope, present map[string]bool) error {
	rows, e := c.DB.QueryContext(ctx, "SELECT native_id FROM native_questions WHERE turn_id=? AND send_state!='sent'", s.TurnID)
	if e != nil {
		return e
	}
	var missing []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		if !present[id] {
			missing = append(missing, id)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range missing {
		tx, e := c.DB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		e = func() error {
			defer tx.Rollback()
			if _, e := tx.ExecContext(ctx, "UPDATE native_questions SET send_state='unknown' WHERE native_id=?", id); e != nil {
				return e
			}
			result, e := tx.ExecContext(ctx, `UPDATE questions SET state='superseded',revision=revision+1,question_json=json_set(question_json,'$.state','superseded','$.revision',revision+1,'$.reason','Native question unavailable; delivery unknown') WHERE id=(SELECT question_id FROM native_questions WHERE native_id=?) AND state='pending'`, id)
			if e != nil {
				return e
			}
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if n > 0 {
				var raw string
				if e = tx.QueryRowContext(ctx, "SELECT question_json FROM questions WHERE id=(SELECT question_id FROM native_questions WHERE native_id=?)", id).Scan(&raw); e != nil {
					return e
				}
				if e = events.Append(ctx, tx, s.ProjectID, s.SessionID, "", "question_resolved", []byte(raw)); e != nil {
					return e
				}
			}
			return tx.Commit()
		}()
		if e != nil {
			return e
		}
	}
	if len(missing) > 0 {
		c.Events.Notify()
	}
	return nil
}
