package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type chatFixture struct {
	chat             *Chat
	autoApprove      atomic.Bool
	project, session string
	prompts, aborts  atomic.Int32
	mu               sync.Mutex
	parent, text     string
	promptParts      []map[string]string
	finished         bool
	delay            time.Duration
	reject           bool
	disconnect       bool
	question         bool
	replies          atomic.Int32
	accept           chan struct{}
	failureEvent     bool
	loseAcceptance   bool
	abortFailures    atomic.Int32
	control          func(string) bool
	snapshotReads    chan string
}

func newChatFixture(t *testing.T, delay time.Duration, reject bool) *chatFixture {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "create", "P")
	if e != nil {
		t.Fatal(e)
	}
	ev := events.NewService(db)
	turns := NewTurns(db)
	q := questions.NewService(db, nil, ev)
	c := NewChat(db, turns, ev, q)
	c.Grace = 40 * time.Millisecond
	f := &chatFixture{chat: c, project: p.ID, delay: delay, reject: reject}
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); !ok || user != "opencode" || password != "private" {
			w.WriteHeader(401)
			return
		}
		if f.control != nil && f.control(r.URL.Path) {
			w.WriteHeader(503)
			return
		}
		switch {
		case r.URL.Path == "/global/health":
			fmt.Fprint(w, `{"healthy":true,"version":"1.19.0"}`)
		case r.URL.Path == "/agent":
			fmt.Fprint(w, `[{"name":"build","permission":[{"permission":"*","pattern":"*","action":"deny"},{"permission":"question","pattern":"*","action":"allow"}]}]`)
		case r.URL.Path == "/session" && r.Method == "POST":
			fmt.Fprint(w, `{"id":"ses_native"}`)
		case strings.HasSuffix(r.URL.Path, "/prompt_async"):
			f.prompts.Add(1)
			var body struct {
				MessageID string              `json:"messageID"`
				Parts     []map[string]string `json:"parts"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.parent = body.MessageID
			f.promptParts = body.Parts
			f.mu.Unlock()
			if f.accept != nil {
				select {
				case <-f.accept:
				case <-r.Context().Done():
					return
				}
			}
			select {
			case <-time.After(f.delay):
			case <-r.Context().Done():
				return
			}
			if f.loseAcceptance {
				conn, _, e := w.(http.Hijacker).Hijack()
				if e == nil {
					conn.Close()
				}
				return
			}
			if f.reject {
				w.WriteHeader(400)
			} else {
				w.WriteHeader(204)
			}
		case strings.HasSuffix(r.URL.Path, "/abort"):
			f.aborts.Add(1)
			if f.abortFailures.CompareAndSwap(1, 0) {
				w.WriteHeader(503)
				return
			}
			fmt.Fprint(w, "true")
		case strings.HasSuffix(r.URL.Path, "/message"):
			f.mu.Lock()
			defer f.mu.Unlock()
			select {
			case f.snapshotReads <- f.parent:
			default:
			}
			if f.parent == "" {
				fmt.Fprint(w, "[]")
				return
			}
			completed := 0
			finish := ""
			if f.finished {
				completed = 1
				finish = "stop"
			}
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"info": map[string]any{"id": "msg_assistant", "sessionID": "ses_native", "role": "assistant", "parentID": f.parent, "finish": finish, "time": map[string]int{"completed": completed}}, "parts": []any{map[string]any{"id": "part_text", "sessionID": "ses_native", "messageID": "msg_assistant", "type": "text", "text": f.text}}}})
		case r.URL.Path == "/question":
			f.mu.Lock()
			pending := f.question
			f.mu.Unlock()
			if pending {
				fmt.Fprint(w, `[{"id":"que_fixture","sessionID":"ses_native","questions":[{"header":"Choose","question":"Continue?","options":[{"label":"Yes","description":"Proceed"}]}],"tool":{"messageID":"msg_assistant","callID":"call_question"}}]`)
			} else {
				fmt.Fprint(w, "[]")
			}
		case r.URL.Path == "/question/que_fixture/reply":
			f.replies.Add(1)
			f.mu.Lock()
			f.question = false
			f.mu.Unlock()
			conn, _, e := w.(http.Hijacker).Hijack()
			if e == nil {
				conn.Close()
			}
		case r.URL.Path == "/session/status":
			fmt.Fprint(w, "{}")
		case r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"server.connected\"}\n\n")
			if f.failureEvent {
				fmt.Fprint(w, "data: {\"type\":\"session.error\",\"properties\":{\"sessionID\":\"ses_native\",\"error\":{\"name\":\"UnknownError\",\"data\":{\"message\":\"synthetic upstream internal error\"}}}}\n\n")
			}
			w.(http.Flusher).Flush()
			if !f.disconnect {
				<-r.Context().Done()
			}
		default:
			fmt.Fprint(w, "true")
		}
	}))
	t.Cleanup(native.Close)
	config := chatFixtureConfig(LLMConfig{Protocol: "openai-compatible", BaseURL: "http://127.0.0.1:12345", ModelID: "fixture"})
	h := &Host{base: native.URL, password: "private", client: native.Client(), active: View{Config: &config}, Relay: &Relay{DB: db, config: config}}
	if e = c.Start(h, func(string) error { return nil }); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(c.Close)
	s, e := c.Create(context.Background(), p.ID, []byte(`{"requestId":"session","title":"Chat"}`))
	if e != nil {
		t.Fatal(e)
	}
	f.session = s.ID
	f.autoApprove.Store(true)
	done := make(chan struct{})
	stopped := make(chan struct{})
	t.Cleanup(func() { close(done); <-stopped })
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if !f.autoApprove.Load() {
					continue
				}
				rows, err := db.Query("SELECT question_json FROM questions WHERE kind='billing' AND state='pending'")
				if err != nil {
					continue
				}
				var cards []questions.Question
				for rows.Next() {
					var raw string
					var card questions.Question
					if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &card) == nil {
						cards = append(cards, card)
					}
				}
				rows.Close()
				for _, card := range cards {
					_, _ = q.Reply(context.Background(), card.ProjectID, card.ID, questions.HumanReply{RequestID: "fixture-human-" + card.ID, SessionID: card.SessionID, ExpectedRevision: card.Revision, Answers: [][]string{{chatApprove}}})
				}
			}
		}
	}()
	return f
}

func TestNativeSessionErrorProducesOneSanitizedTerminal(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.failureEvent = true
	m, e := f.chat.Send(context.Background(), f.project, f.session, f.input("error"))
	if e != nil {
		t.Fatal(e)
	}
	waitChat(t, func() bool {
		var terminal int
		_ = f.chat.DB.QueryRow("SELECT terminal FROM chat_dispatch WHERE message_id=?", m.ID).Scan(&terminal)
		return terminal == 1
	})
	frames, e := f.chat.Events.Read(context.Background(), f.project, f.session, 0)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, frame := range frames {
		if frame.Type == "chat_done" {
			count++
			if strings.Contains(string(frame.Payload), "internal error") {
				t.Fatal("raw native error leaked")
			}
		}
	}
	if count != 1 {
		t.Fatal("terminal count", count)
	}
}

func TestSupplierUncertaintySurvivesNativeError(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.failureEvent = true
	f.accept = make(chan struct{})
	m, e := f.chat.Send(context.Background(), f.project, f.session, f.input("error"))
	if e != nil {
		t.Fatal(e)
	}
	waitChat(t, func() bool { return f.chat.Turns.Current().TurnID != "" })
	waitChat(t, func() bool { return f.prompts.Load() == 1 })
	s := f.chat.Turns.Current()
	_, e = f.chat.DB.Exec("INSERT INTO inference_requests(turn_id,step,fingerprint,state,created_at)VALUES(?,1,'fixture','unknown',1)", s.TurnID)
	if e != nil {
		t.Fatal(e)
	}
	close(f.accept)
	waitChat(t, func() bool {
		var terminal int
		_ = f.chat.DB.QueryRow("SELECT terminal FROM chat_dispatch WHERE message_id=?", m.ID).Scan(&terminal)
		return terminal == 1
	})
	var state string
	_ = f.chat.DB.QueryRow("SELECT state FROM chat_dispatch WHERE message_id=?", m.ID).Scan(&state)
	if state != "unknown" {
		t.Fatal("supplier uncertainty lost", state)
	}
}
func (f *chatFixture) input(id string) []byte {
	return []byte(fmt.Sprintf(`{"requestId":%q,"document":{"type":"doc","content":[]},"text":"hello","references":[],"annotations":[]}`, id))
}
func waitChat(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("chat condition timed out")
}
func TestChatAcceptanceWindowReplayAndLateCompletion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		delay  time.Duration
		reject bool
	}{{"immediate-reject", 0, true}, {"within-grace", 30 * time.Millisecond, false}, {"late-success", 100 * time.Millisecond, false}, {"late-reject", 100 * time.Millisecond, true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newChatFixture(t, tc.delay, tc.reject)
			if tc.name == "immediate-reject" {
				f.chat.Grace = 500 * time.Millisecond
			}
			if strings.HasPrefix(tc.name, "late-") {
				f.accept = make(chan struct{})
			}
			m, e := f.chat.Send(context.Background(), f.project, f.session, f.input("send"))
			if tc.reject && tc.delay == 0 {
				if e == nil {
					t.Fatal("immediate rejection accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if f.accept != nil {
				if m.State != "submitting" && m.State != "queued" {
					t.Fatal("grace did not preserve unresolved submission", m.State)
				}
				close(f.accept)
			}
			_, e = f.chat.Send(context.Background(), f.project, f.session, f.input("send"))
			if e != nil && !(tc.reject) {
				t.Fatal(e)
			}
			waitChat(t, func() bool { return f.prompts.Load() == 1 })
			if _, e = f.chat.Send(context.Background(), f.project, f.session, []byte(strings.Replace(string(f.input("send")), "hello", "changed", 1))); e == nil {
				t.Fatal("mutated replay accepted")
			}
			if !tc.reject {
				f.mu.Lock()
				f.text = "你你"
				f.finished = true
				f.mu.Unlock()
			}
			waitChat(t, func() bool {
				var terminal int
				_ = f.chat.DB.QueryRow("SELECT terminal FROM chat_dispatch WHERE message_id=?", m.ID).Scan(&terminal)
				return terminal == 1
			})
			if f.prompts.Load() != 1 {
				t.Fatal("native prompt resubmitted")
			}
		})
	}
}
func TestChatSnapshotReplayPreservesIdenticalTextAndSingleTerminal(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.disconnect = true
	m, e := f.chat.Send(context.Background(), f.project, f.session, f.input("send"))
	if e != nil {
		t.Fatal(e)
	}
	for _, text := range []string{"哈", "哈哈", "哈哈"} {
		f.mu.Lock()
		f.text = text
		f.mu.Unlock()
		time.Sleep(170 * time.Millisecond)
	}
	f.mu.Lock()
	f.finished = true
	f.mu.Unlock()
	waitChat(t, func() bool {
		var terminal int
		_ = f.chat.DB.QueryRow("SELECT terminal FROM chat_dispatch WHERE message_id=?", m.ID).Scan(&terminal)
		return terminal == 1
	})
	frames, e := f.chat.Events.Read(context.Background(), f.project, f.session, 0)
	if e != nil {
		t.Fatal(e)
	}
	text := ""
	done := 0
	for _, frame := range frames {
		if frame.Type == "chat_done" {
			done++
		}
		if frame.Type == "chat_message" {
			var p ChatMessageEnvelope
			_ = json.Unmarshal(frame.Payload, &p)
			text += p.ContentDelta
		}
	}
	if text != "哈哈" || done != 1 {
		t.Fatalf("snapshot replay %q terminals %d", text, done)
	}
	messages, e := f.chat.Messages(context.Background(), f.project, f.session, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	if len(messages) != 2 || messages[1].Role != "assistant" || !strings.Contains(string(messages[1].Content), "哈哈") {
		t.Fatal("assistant history missing from read route", messages)
	}
}

func TestChatCancelledQueueEmitsTerminalAndDoesNotSubmit(t *testing.T) {
	f := newChatFixture(t, 500*time.Millisecond, false)
	_, e := f.chat.Send(context.Background(), f.project, f.session, f.input("first"))
	if e != nil {
		t.Fatal(e)
	}
	waitChat(t, func() bool { return f.prompts.Load() == 1 })
	second, e := f.chat.Send(context.Background(), f.project, f.session, f.input("second"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.chat.Cancel(context.Background(), f.project, f.session, []byte(`{"requestId":"cancel"}`))
	if e != nil {
		t.Fatal(e)
	}
	frames, e := f.chat.Events.Read(context.Background(), f.project, f.session, 0)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, frame := range frames {
		var p ChatDonePayload
		_ = json.Unmarshal(frame.Payload, &p)
		if frame.Type == "chat_done" && p.MessageID == second.ID && p.Status == "cancelled" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("queued cancellation did not emit one durable terminal")
	}
	if f.prompts.Load() != 1 {
		t.Fatal("queued message submitted")
	}
}
func TestChatCancelDuringUnansweredAcceptanceAbortsOwnedSession(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.accept = make(chan struct{})
	_, e := f.chat.Send(context.Background(), f.project, f.session, f.input("send"))
	if e != nil {
		t.Fatal(e)
	}
	waitChat(t, func() bool { return f.prompts.Load() == 1 })
	if _, e = f.chat.Cancel(context.Background(), f.project, f.session, []byte(`{"requestId":"cancel"}`)); e != nil {
		t.Fatal(e)
	}
	// Acceptance is still gated: observing Abort proves cancellation does not
	// wait for the prompt HTTP response, regardless of CPU/SQLite scheduling.
	waitChat(t, func() bool { return f.aborts.Load() == 1 })
	close(f.accept)
}

func TestCancelWaitsForLateAcceptanceBeforeNextProjectEpoch(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.accept = make(chan struct{})
	_, e := f.chat.Send(context.Background(), f.project, f.session, f.input("first"))
	if e != nil {
		t.Fatal(e)
	}
	waitChat(t, func() bool { return f.prompts.Load() == 1 })
	old := f.chat.Turns.Current()
	p, e := (store.Objects{DB: f.chat.DB}).CreateProject(context.Background(), "other", "Other")
	if e != nil {
		t.Fatal(e)
	}
	// The fixture models two already-created native sessions and records which scope
	// is permitted while the old prompt's HTTP response remains unresolved.
	other, e := f.chat.Create(context.Background(), p.ID, []byte(`{"requestId":"other","title":"Other"}`))
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.chat.DB.Exec("UPDATE chat_sessions SET native_session_id='ses_other',state='ready' WHERE id=?", other.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.chat.Send(context.Background(), p.ID, other.ID, f.input("next"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.chat.Cancel(context.Background(), f.project, f.session, []byte(`{"requestId":"cancel"}`))
	if e != nil {
		t.Fatal(e)
	}
	waitChat(t, func() bool { return f.aborts.Load() == 1 })
	if f.chat.Turns.Current() != old || f.prompts.Load() != 1 {
		t.Fatal("old acceptance escaped into new epoch")
	}
	close(f.accept)
	waitChat(t, func() bool { return f.prompts.Load() == 2 })
	if f.chat.Turns.Current().ProjectID != p.ID {
		t.Fatal("queued project did not activate after drain")
	}
	if _, e = f.chat.Turns.Acquire(old); e == nil {
		t.Fatal("old transport scope still acquired")
	}
}

func TestNativeQuestionHumanReceiptUnknownNeverResends(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.mu.Lock()
	f.question = true
	f.mu.Unlock()
	_, e := f.chat.Send(context.Background(), f.project, f.session, f.input("send"))
	if e != nil {
		t.Fatal(e)
	}
	var card questions.Question
	waitChat(t, func() bool {
		cards, e := f.chat.Questions.List(context.Background(), f.project, f.session)
		if e == nil {
			for _, candidate := range cards {
				if candidate.Kind == "choice" {
					card = candidate
					return true
				}
			}
		}
		return false
	})
	if f.replies.Load() != 0 {
		t.Fatal("agent question treated as authorization")
	}
	receipt, e := f.chat.Questions.Reply(context.Background(), f.project, card.ID, questions.HumanReply{RequestID: "human", SessionID: f.session, ExpectedRevision: card.Revision, Answers: [][]string{{"Yes"}}})
	if e != nil {
		t.Fatal(e)
	}
	waitChat(t, func() bool {
		var state string
		_ = f.chat.DB.QueryRow("SELECT send_state FROM native_questions WHERE question_id=?", card.ID).Scan(&state)
		return state == "unknown"
	})
	replay, e := f.chat.Questions.Reply(context.Background(), f.project, card.ID, questions.HumanReply{RequestID: "human", SessionID: f.session, ExpectedRevision: card.Revision, Answers: [][]string{{"Yes"}}})
	if e != nil || replay.ID != receipt.ID {
		t.Fatal("human receipt replay changed", e)
	}
	time.Sleep(300 * time.Millisecond)
	if f.replies.Load() != 1 {
		t.Fatal("unknown native reply automatically resent")
	}
	var count int
	_ = f.chat.DB.QueryRow("SELECT count(*) FROM question_receipts WHERE question_id=?", card.ID).Scan(&count)
	if count != 1 {
		t.Fatal("human receipt duplicated")
	}
}

func TestProjectWithChatReturnsReferencedConflict(t *testing.T) {
	f := newChatFixture(t, 0, false)
	_, e := (store.Objects{DB: f.chat.DB}).Mutate(context.Background(), f.project, "projects", store.Mutation{RequestID: "delete", Action: "delete", ID: f.project})
	var domain *store.DomainError
	if !errors.As(e, &domain) || domain.Status != 409 {
		t.Fatal("chat project delete not protected", e)
	}
}

func TestRestartReconcilesWithoutResendingUnknownAndQueuedWithdrawal(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.autoApprove.Store(false)
	first, e := f.chat.Send(context.Background(), f.project, f.session, f.input("first"))
	if e != nil {
		t.Fatal(e)
	}
	approveChatFixture(t, f.chat, f.project, f.session, first.BillingQuestionID, false)
	var beforeState string
	waitChat(t, func() bool {
		_ = f.chat.DB.QueryRow("SELECT state FROM chat_dispatch WHERE message_id=?", first.ID).Scan(&beforeState)
		return f.prompts.Load() == 1 && beforeState == "accepted"
	})
	before := f.prompts.Load()
	t.Logf("before restart: POSTs=%d state=%s", before, beforeState)
	second, e := f.chat.Send(context.Background(), f.project, f.session, f.input("second"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.chat.Cancel(context.Background(), f.project, f.session, []byte(fmt.Sprintf(`{"requestId":"withdraw","message_id":%q}`, second.ID)))
	if e != nil {
		t.Fatal(e)
	}
	if f.aborts.Load() != 0 {
		t.Fatal("withdrawing queue aborted active turn")
	}
	old := f.chat
	old.Close()
	f.snapshotReads = make(chan string, 16)
	turns := NewTurns(old.DB)
	if e = turns.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	next := NewChat(old.DB, turns, old.Events, old.Questions)
	next.Grace = 20 * time.Millisecond
	if e = next.Start(old.Host, func(string) error { return nil }); e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	_, e = next.Send(context.Background(), f.project, f.session, f.input("first"))
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-f.snapshotReads:
	case <-time.After(3 * time.Second):
		t.Fatal("restart did not reconcile native snapshot")
	}
	var afterState string
	if e = old.DB.QueryRow("SELECT state FROM chat_dispatch WHERE message_id=?", first.ID).Scan(&afterState); e != nil {
		t.Fatal(e)
	}
	t.Logf("after restart: POSTs=%d state=%s", f.prompts.Load(), afterState)
	if f.prompts.Load() != before || afterState != "unknown" {
		t.Fatalf("restart changed uncertain dispatch: before POSTs=%d state=%s; after POSTs=%d state=%s", before, beforeState, f.prompts.Load(), afterState)
	}
	f.mu.Lock()
	f.text = "recovered"
	f.finished = true
	f.mu.Unlock()
	waitChat(t, func() bool {
		var terminal int
		_ = old.DB.QueryRow("SELECT terminal FROM chat_dispatch WHERE message_id=?", first.ID).Scan(&terminal)
		return terminal == 1
	})
	if f.prompts.Load() != before {
		t.Fatalf("reconciliation submitted new user message: before POSTs=%d state=%s; after POSTs=%d prior state=%s", before, beforeState, f.prompts.Load(), afterState)
	}
}
