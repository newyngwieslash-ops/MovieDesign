package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"sync/atomic"
	"testing"
	"time"
)

func TestTextQueueCancellationAndRestartNeverResubmit(t *testing.T) {
	s, p, in := setup(t)
	ctx := context.Background()
	f, e := providers.TextProvider(json.RawMessage(`{"protocol":"openai-compatible","base_url":"http://127.0.0.1:11434","model_id":"fixture","context":8192,"output":512}`), "")
	if e != nil {
		t.Fatal(e)
	}
	in.Provider = f
	in.CapabilityID = "text:configured-llm"
	first, e := s.Submit(ctx, p, in)
	if e != nil {
		t.Fatal(e)
	}
	in.RequestID = "second"
	in.Output.Composition.Name = "Second"
	in.Output.Composition.Type.Label = "Second"
	second, e := s.Submit(ctx, p, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("UPDATE task_attempts SET state='approved'"); e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var posts atomic.Int32
	s.Executor = &Executor{Submit: func(context.Context, Attempt, func(string) error) (Outcome, error) {
		posts.Add(1)
		entered <- struct{}{}
		<-release
		return Outcome{}, errors.New("lost acceptance")
	}, Query: func(context.Context, Attempt) (Outcome, error) { return Outcome{Status: Unknown}, nil }}
	s.Dispatch(p, first.ID)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first did not start")
	}
	s.Dispatch(p, second.ID)
	time.Sleep(100 * time.Millisecond)
	var started int
	s.DB.QueryRow("SELECT count(*) FROM task_attempts WHERE submit_started_at IS NOT NULL").Scan(&started)
	if started != 1 {
		t.Fatal("concurrent text execution", started)
	}
	second, e = s.Query(ctx, p, second.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Resolve(ctx, p, second.ID, ResolveInput{RequestID: "cancel-second", Action: "cancel", ExpectedRevision: second.Revision}); e != nil {
		t.Fatal(e)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for s.Active() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Active() != 0 || posts.Load() != 1 {
		t.Fatal("queue did not drain", s.Active(), posts.Load())
	}
	if e = s.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	v, e := s.Query(ctx, p, first.ID)
	if e != nil || v.Status != Unknown || posts.Load() != 1 {
		t.Fatal(v, e, posts.Load())
	}
}
