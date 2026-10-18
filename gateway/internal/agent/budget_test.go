package agent

import (
	"context"
	"errors"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFortyCalls(t *testing.T) {
	b := NewBudget(40)
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("call-%d", i)
		if e := b.Admit(id); e != nil {
			t.Fatal(e)
		}
		if e := b.Admit(id); e != nil {
			t.Fatal("event replay counted twice")
		}
	}
	if b.Admit("call-40") == nil {
		t.Fatal("41st admitted")
	}
}
func TestDurableParallelBudgetQuestionReplayAndEpoch(t *testing.T) {
	ctx := context.Background()
	db, e := store.Open(filepath.Join(t.TempDir(), "budget.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p, e := (store.Objects{DB: db}).CreateProject(ctx, "create", "P")
	if e != nil {
		t.Fatal(e)
	}
	turns := NewTurns(db)
	scope, e := turns.Begin(ctx, p.ID, "session", "native")
	if e != nil {
		t.Fatal(e)
	}
	if _, e := turns.Begin(ctx, p.ID, "other", "other"); e == nil {
		t.Fatal("second active turn")
	}
	var wg sync.WaitGroup
	for i := 0; i < 39; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, e := turns.Admit(ctx, scope, fmt.Sprint(i), "fixture", "", []byte(`{}`)); e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	ev := events.NewService(db)
	defer ev.Close()
	qs := questions.NewService(db, nil, ev)
	q := NativeQuestion{ID: "question1", SessionID: "native", Questions: []questions.Info{{Header: "Choose", Question: "Continue?", Options: []questions.Option{{Label: "Yes"}}}}}
	rejects, aborts := 0, 0
	turns.Abort = func(context.Context, string) error { aborts++; return nil }
	reject := func(context.Context, string) error { rejects++; return nil }
	first, e := turns.GateQuestion(ctx, scope, q, qs, reject)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := turns.GateQuestion(ctx, scope, q, qs, reject)
	if e != nil || first.ID != replay.ID {
		t.Fatal("question replay", e)
	}
	q.ID = "question2"
	if _, e = turns.GateQuestion(ctx, scope, q, qs, reject); !errors.Is(e, ErrBudget) {
		t.Fatal("41st question", e)
	}
	var count, cards, receipts int
	db.QueryRow("SELECT call_count FROM chat_turns WHERE id=?", scope.TurnID).Scan(&count)
	db.QueryRow("SELECT count(*) FROM questions").Scan(&cards)
	db.QueryRow("SELECT count(*) FROM question_receipts").Scan(&receipts)
	if count != 40 || cards != 1 || receipts != 0 || aborts != 1 || rejects != 1 {
		t.Fatal(count, cards, receipts, aborts, rejects)
	}
	idle, closed := false, false
	if e = turns.End(ctx, scope, func(context.Context) error { idle = true; return nil }, func(context.Context) error {
		if !idle {
			t.Fatal("close before idle")
		}
		closed = true
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if !closed {
		t.Fatal("transport not closed")
	}
	next, e := turns.Begin(ctx, p.ID, "session", "native")
	if e != nil {
		t.Fatal(e)
	}
	if next.Epoch == scope.Epoch {
		t.Fatal("epoch reused")
	}
	if _, e = turns.Acquire(scope); e == nil {
		t.Fatal("old epoch accepted")
	}
}

func TestQueuedTurnWaitsForDrainAndRecovery(t *testing.T) {
	ctx := context.Background()
	db, e := store.Open(filepath.Join(t.TempDir(), "queue.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p, e := (store.Objects{DB: db}).CreateProject(ctx, "create", "P")
	if e != nil {
		t.Fatal(e)
	}
	turns := NewTurns(db)
	first, e := turns.Begin(ctx, p.ID, "s1", "n1")
	if e != nil {
		t.Fatal(e)
	}
	done, e := turns.Acquire(first)
	if e != nil {
		t.Fatal(e)
	}
	queued := make(chan ToolScope, 1)
	go func() {
		next, e := turns.BeginQueued(ctx, p.ID, "s2", "n2")
		if e != nil {
			t.Error(e)
		}
		queued <- next
	}()
	ended := make(chan error, 1)
	closed := false
	go func() {
		ended <- turns.End(ctx, first, func(context.Context) error { return nil }, func(context.Context) error { closed = true; return nil })
	}()
	select {
	case <-queued:
		t.Fatal("queue bypassed pending tool")
	case <-time.After(20 * time.Millisecond):
	}
	done()
	if e = <-ended; e != nil {
		t.Fatal(e)
	}
	next := <-queued
	if next.TurnID == first.TurnID || !closed {
		t.Fatal("epoch handoff")
	}
	restarted := NewTurns(db)
	if e = restarted.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = restarted.Begin(ctx, p.ID, "s3", "n3"); e != nil {
		t.Fatal("stale persisted active turn blocked restart", e)
	}
}
