package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"sync/atomic"
	"testing"
	"time"
)

func queueOtherProject(t *testing.T, f *chatFixture) string {
	t.Helper()
	p, e := (store.Objects{DB: f.chat.DB}).CreateProject(context.Background(), "other", "Other")
	if e != nil {
		t.Fatal(e)
	}
	s, e := f.chat.Create(context.Background(), p.ID, []byte(`{"requestId":"other","title":"Other"}`))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.chat.DB.Exec("UPDATE chat_sessions SET native_session_id='ses_other',state='ready' WHERE id=?", s.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.chat.Send(context.Background(), p.ID, s.ID, f.input("next")); e != nil {
		t.Fatal(e)
	}
	return p.ID
}

func TestRestartQuestionPublishedBeforeLink(t *testing.T) {
	for _, answered := range []bool{false, true} {
		t.Run(fmt.Sprint("receipt_", answered), func(t *testing.T) {
			f := newChatFixture(t, 0, false)
			f.chat.Close()
			if _, e := f.chat.DB.Exec("UPDATE chat_sessions SET native_session_id='ses_native',state='ready' WHERE id=?", f.session); e != nil {
				t.Fatal(e)
			}
			s, e := f.chat.Turns.Begin(context.Background(), f.project, f.session, "ses_native")
			if e != nil {
				t.Fatal(e)
			}
			native := NativeQuestion{ID: "que_crash", SessionID: "ses_native", Questions: []questions.Info{{Header: "Choose", Question: "Continue?", Options: []questions.Option{{Label: "Yes", Description: "Proceed"}}}}}
			raw, _ := json.Marshal(native)
			if _, e = f.chat.DB.Exec("INSERT INTO native_questions(native_id,turn_id,native_json)VALUES(?,?,?)", native.ID, s.TurnID, string(raw)); e != nil {
				t.Fatal(e)
			}
			// Stop at the actual two-transaction boundary: Ask has committed its
			// card/event/write receipt, but the adapter has not linked question_id.
			card, e := f.chat.Turns.GateQuestion(context.Background(), s, native, f.chat.Questions, f.chat.Host.RejectQuestion)
			if e != nil {
				t.Fatal(e)
			}
			var link sql.NullString
			if e = f.chat.DB.QueryRow("SELECT question_id FROM native_questions WHERE native_id=?", native.ID).Scan(&link); e != nil || link.Valid {
				t.Fatal("not the NULL linkage crash boundary", e)
			}
			reply := questions.HumanReply{RequestID: "human", SessionID: f.session, ExpectedRevision: card.Revision, Answers: [][]string{{"Yes"}}}
			var receipt questions.Receipt
			if answered {
				receipt, e = f.chat.Questions.Reply(context.Background(), f.project, card.ID, reply)
				if e != nil {
					t.Fatal(e)
				}
			}
			turns := NewTurns(f.chat.DB)
			if e = turns.Recover(context.Background()); e != nil {
				t.Fatal(e)
			}
			next := NewChat(f.chat.DB, turns, f.chat.Events, f.chat.Questions)
			if e = next.Start(f.chat.Host, func(string) error { return nil }); e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			waitChat(t, func() bool {
				cards, e := next.Questions.List(context.Background(), f.project, f.session)
				return e == nil && len(cards) == 1 && cards[0].NativeSendState == "unknown" && cards[0].State != "pending"
			})
			next.Close()
			if e = next.DB.QueryRow("SELECT question_id FROM native_questions WHERE native_id=?", native.ID).Scan(&link); e != nil || link.String != card.ID {
				t.Fatal("existing card not recovered", e)
			}
			late := reply
			late.RequestID = "late"
			if _, e = next.Questions.Reply(context.Background(), f.project, card.ID, late); e == nil {
				t.Fatal("stale NULL-linked card accepted receipt")
			}
			if answered {
				got, e := next.Questions.Reply(context.Background(), f.project, card.ID, reply)
				if e != nil || got.ID != receipt.ID {
					t.Fatal("existing receipt changed", e)
				}
			}
			var cards, asked, receipts int
			_ = next.DB.QueryRow("SELECT count(*) FROM questions").Scan(&cards)
			_ = next.DB.QueryRow("SELECT count(*) FROM events WHERE type='question_asked'").Scan(&asked)
			_ = next.DB.QueryRow("SELECT count(*) FROM question_receipts").Scan(&receipts)
			want := 0
			if answered {
				want = 1
			}
			if cards != 1 || asked != 1 || receipts != want || f.prompts.Load() != 0 || f.replies.Load() != 0 {
				t.Fatal("recovery recreated or sent", cards, asked, receipts, f.prompts.Load(), f.replies.Load())
			}
		})
	}
}

func TestQuestionRecoveryDoesNotRetireForeignScope(t *testing.T) {
	for _, foreignProject := range []bool{false, true} {
		t.Run(fmt.Sprint("foreign_project_", foreignProject), func(t *testing.T) {
			f := newChatFixture(t, 0, false)
			f.chat.Close()
			_, e := f.chat.DB.Exec("UPDATE chat_sessions SET native_session_id='ses_native',state='ready' WHERE id=?", f.session)
			if e != nil {
				t.Fatal(e)
			}
			s, e := f.chat.Turns.Begin(context.Background(), f.project, f.session, "ses_native")
			if e != nil {
				t.Fatal(e)
			}
			p, session := f.project, "other_session"
			if foreignProject {
				other, e := (store.Objects{DB: f.chat.DB}).CreateProject(context.Background(), "foreign", "Other")
				if e != nil {
					t.Fatal(e)
				}
				p = other.ID
				session = f.session
			}
			card, e := f.chat.Questions.Ask(context.Background(), p, questions.Question{RequestID: "que_foreign", SessionID: session, Kind: "choice", Questions: []questions.Info{{Header: "Choose", Question: "Continue?", Options: []questions.Option{{Label: "Yes"}}}}})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.chat.DB.Exec("INSERT INTO native_questions(native_id,turn_id,native_json)VALUES('que_foreign',?,'{}')", s.TurnID); e != nil {
				t.Fatal(e)
			}
			if e = f.chat.retireMissingQuestions(context.Background(), s, map[string]bool{}); e != nil {
				t.Fatal(e)
			}
			var link sql.NullString
			var state string
			_ = f.chat.DB.QueryRow("SELECT question_id FROM native_questions WHERE native_id='que_foreign'").Scan(&link)
			_ = f.chat.DB.QueryRow("SELECT state FROM questions WHERE id=?", card.ID).Scan(&state)
			if link.Valid || state != "pending" {
				t.Fatal("foreign project/session card adopted", link, state)
			}
		})
	}
}

func TestRetiredQuestionRecoveryDoesNotWriteOrWake(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.chat.Close()
	_, e := f.chat.DB.Exec("UPDATE chat_sessions SET native_session_id='ses_native',state='ready' WHERE id=?", f.session)
	if e != nil {
		t.Fatal(e)
	}
	s, e := f.chat.Turns.Begin(context.Background(), f.project, f.session, "ses_native")
	if e != nil {
		t.Fatal(e)
	}
	card, e := f.chat.Questions.Ask(context.Background(), f.project, questions.Question{RequestID: "que_settled", SessionID: f.session, Kind: "choice", Questions: []questions.Info{{Header: "Choose", Question: "Continue?", Options: []questions.Option{{Label: "Yes"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.chat.DB.Exec("INSERT INTO native_questions(native_id,turn_id,question_id,native_json)VALUES('que_settled',?,?,'{}')", s.TurnID, card.ID); e != nil {
		t.Fatal(e)
	}
	if e = f.chat.retireMissingQuestions(context.Background(), s, map[string]bool{}); e != nil {
		t.Fatal(e)
	}
	if _, e = f.chat.DB.Exec("CREATE TRIGGER fail_settled_rewrite BEFORE UPDATE ON native_questions BEGIN SELECT RAISE(ABORT,'settled mapping rewritten'); END"); e != nil {
		t.Fatal(e)
	}
	wake, unsubscribe := f.chat.Events.Subscribe()
	defer unsubscribe()
	if e = f.chat.retireMissingQuestions(context.Background(), s, map[string]bool{}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-wake:
		t.Fatal("unchanged recovery notified subscribers")
	default:
	}
	if e = f.chat.Turns.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	var reads int
	f.control = func(path string) bool {
		if path == "/question" {
			reads++
		}
		return false
	}
	next := NewChat(f.chat.DB, NewTurns(f.chat.DB), f.chat.Events, f.chat.Questions)
	next.Host = f.chat.Host
	defer next.Close()
	next.reconcileInactiveQuestions()
	if reads != 0 {
		t.Fatal("settled history still read from native", reads)
	}
}

func TestUnlinkedUnknownQuestionCanRecoverLaterReceipt(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.chat.Close()
	_, e := f.chat.DB.Exec("UPDATE chat_sessions SET native_session_id='ses_native',state='ready' WHERE id=?", f.session)
	if e != nil {
		t.Fatal(e)
	}
	s, e := f.chat.Turns.Begin(context.Background(), f.project, f.session, "ses_native")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.chat.DB.Exec("INSERT INTO native_questions(native_id,turn_id,native_json)VALUES('que_later',?,'{}')", s.TurnID); e != nil {
		t.Fatal(e)
	}
	if e = f.chat.retireMissingQuestions(context.Background(), s, map[string]bool{}); e != nil {
		t.Fatal(e)
	}
	wake, unsubscribe := f.chat.Events.Subscribe()
	defer unsubscribe()
	if e = f.chat.retireMissingQuestions(context.Background(), s, map[string]bool{}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-wake:
		t.Fatal("unchanged NULL mapping notified subscribers")
	default:
	}
	card, e := f.chat.Questions.Ask(context.Background(), f.project, questions.Question{RequestID: "que_later", SessionID: f.session, Kind: "choice", Questions: []questions.Info{{Header: "Choose", Question: "Continue?", Options: []questions.Option{{Label: "Yes"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.chat.retireMissingQuestions(context.Background(), s, map[string]bool{}); e != nil {
		t.Fatal(e)
	}
	var link, state string
	if e = f.chat.DB.QueryRow("SELECT n.question_id,q.state FROM native_questions n JOIN questions q ON q.id=n.question_id WHERE n.native_id='que_later'").Scan(&link, &state); e != nil || link != card.ID || state != "superseded" {
		t.Fatal("unknown NULL mapping lost recoverable receipt", link, state, e)
	}
}
func assertOneTerminal(t *testing.T, f *chatFixture, id string) {
	t.Helper()
	frames, e := f.chat.Events.Read(context.Background(), f.project, f.session, 0)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, frame := range frames {
		if frame.Type == "chat_done" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("terminal count", count, id)
	}
}
func TestCleanupFailureRetainsAndRecoversEpoch(t *testing.T) {
	for _, stage := range []string{"idle", "disconnect", "epoch"} {
		t.Run(stage, func(t *testing.T) {
			f := newChatFixture(t, 0, false)
			failed := make(chan struct{}, 1)
			recoverControl := make(chan struct{})
			var armed, closed, earlyConnect atomic.Bool
			block := func() bool {
				if !armed.Load() {
					return false
				}
				select {
				case <-recoverControl:
					return false
				default:
				}
				select {
				case failed <- struct{}{}:
				default:
				}
				return true
			}
			f.control = func(path string) bool {
				if path == "/mcp/design/connect" && armed.Load() && !closed.Load() {
					earlyConnect.Store(true)
				}
				return (stage == "idle" && path == "/session/status" || stage == "disconnect" && path == "/mcp/design/disconnect") && block()
			}
			f.chat.CloseEpoch = func(epoch string) error {
				if epoch == "" {
					return nil
				}
				if stage == "epoch" && block() {
					return errors.New("fixture epoch close unavailable")
				}
				closed.Store(true)
				return nil
			}
			m, e := f.chat.Send(context.Background(), f.project, f.session, f.input("first"))
			if e != nil {
				t.Fatal(e)
			}
			old := f.chat.Turns.Current()
			p := queueOtherProject(t, f)
			armed.Store(true)
			f.mu.Lock()
			f.finished = true
			f.mu.Unlock()
			select {
			case <-failed:
			case <-time.After(3 * time.Second):
				t.Fatal("cleanup not attempted")
			}
			if f.prompts.Load() != 1 || closed.Load() || earlyConnect.Load() {
				t.Fatal("new scope opened before cleanup")
			}
			close(recoverControl)
			waitChat(t, func() bool { return f.prompts.Load() == 2 })
			if !closed.Load() || earlyConnect.Load() || f.chat.Turns.Current().ProjectID != p {
				t.Fatal("wrong epoch")
			}
			if release, e := f.chat.Turns.Acquire(old); e == nil {
				release()
				t.Fatal("old epoch acquired")
			}
			assertOneTerminal(t, f, m.ID)
		})
	}
}
func TestCancelLostAcceptanceRecoversFromExactSnapshot(t *testing.T) {
	for _, failAbort := range []bool{false, true} {
		t.Run(fmt.Sprint("abort_failure_", failAbort), func(t *testing.T) {
			f := newChatFixture(t, 0, false)
			f.loseAcceptance = true
			f.snapshotReads = make(chan string, 16)
			if failAbort {
				f.abortFailures.Store(1)
			}
			m, e := f.chat.Send(context.Background(), f.project, f.session, f.input("first"))
			if e != nil {
				t.Fatal(e)
			}
			waitChat(t, func() bool {
				var state string
				_ = f.chat.DB.QueryRow("SELECT state FROM chat_dispatch WHERE message_id=?", m.ID).Scan(&state)
				return state == "unknown"
			})
			old := f.chat.Turns.Current()
			p := queueOtherProject(t, f)
			if _, e = f.chat.Cancel(context.Background(), f.project, f.session, []byte(`{"requestId":"cancel"}`)); e != nil {
				t.Fatal(e)
			}
			waitChat(t, func() bool { return f.aborts.Load() > 0 })
			if f.prompts.Load() != 1 {
				t.Fatal("absence of native status released unknown turn")
			}
			f.mu.Lock()
			parent := f.parent
			f.parent = "different_parent"
			f.finished = true
			f.mu.Unlock()
			observed := false
			deadline := time.After(3 * time.Second)
			for !observed {
				select {
				case got := <-f.snapshotReads:
					observed = got == "different_parent"
				case <-deadline:
					t.Fatal("cancel stopped reading exact-parent evidence")
				}
			}

			if f.prompts.Load() != 1 {
				t.Fatal("foreign terminal released unknown turn")
			}
			f.mu.Lock()
			f.parent = parent
			f.mu.Unlock()
			f.chat.Notify()
			waitChat(t, func() bool { return f.prompts.Load() == 2 })
			if f.chat.Turns.Current().ProjectID != p || f.aborts.Load() < 2 {
				t.Fatal("owned abort/drain missing")
			}
			if release, e := f.chat.Turns.Acquire(old); e == nil {
				release()
				t.Fatal("old epoch acquired")
			}
			assertOneTerminal(t, f, m.ID)
		})
	}
}
func TestRestartMissingQuestionInvalidatesCardWithoutSending(t *testing.T) {
	for _, answered := range []bool{false, true} {
		t.Run(fmt.Sprint("receipt_", answered), func(t *testing.T) {
			f := newChatFixture(t, 0, false)
			f.mu.Lock()
			f.question = true
			f.mu.Unlock()
			_, e := f.chat.Send(context.Background(), f.project, f.session, f.input("first"))
			if e != nil {
				t.Fatal(e)
			}
			var card questions.Question
			waitChat(t, func() bool {
				cards, e := f.chat.Questions.List(context.Background(), f.project, f.session)
				if e == nil && len(cards) == 1 {
					card = cards[0]
					return true
				}
				return false
			})
			f.chat.Close()
			reply := questions.HumanReply{RequestID: "human", SessionID: f.session, ExpectedRevision: card.Revision, Answers: [][]string{{"Yes"}}}
			var receipt questions.Receipt
			if answered {
				receipt, e = f.chat.Questions.Reply(context.Background(), f.project, card.ID, reply)
				if e != nil {
					t.Fatal(e)
				}
			}
			f.mu.Lock()
			f.question = false
			f.mu.Unlock()
			turns := NewTurns(f.chat.DB)
			if e = turns.Recover(context.Background()); e != nil {
				t.Fatal(e)
			}
			next := NewChat(f.chat.DB, turns, f.chat.Events, f.chat.Questions)
			if e = next.Start(f.chat.Host, func(string) error { return nil }); e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			if _, e = next.Cancel(context.Background(), f.project, f.session, []byte(`{"requestId":"cancel-restarted"}`)); e != nil {
				t.Fatal(e)
			}
			waitChat(t, func() bool {
				cards, e := next.Questions.List(context.Background(), f.project, f.session)
				return e == nil && len(cards) == 1 && cards[0].NativeSendState == "unknown" && cards[0].State != "pending"
			})
			late := reply
			late.RequestID = "late"
			if _, e = next.Questions.Reply(context.Background(), f.project, card.ID, late); e == nil {
				t.Fatal("stale card accepted new receipt")
			}
			if answered {
				got, e := next.Questions.Reply(context.Background(), f.project, card.ID, reply)
				if e != nil || got.ID != receipt.ID {
					t.Fatal("saved human receipt changed", e)
				}
			}
			var count int
			_ = next.DB.QueryRow("SELECT count(*) FROM question_receipts WHERE question_id=?", card.ID).Scan(&count)
			want := 0
			if answered {
				want = 1
			}
			if count != want || f.replies.Load() != 0 || f.prompts.Load() != 1 {
				t.Fatal("restart sent or fabricated receipt", count, f.replies.Load(), f.prompts.Load())
			}
		})
	}
}
