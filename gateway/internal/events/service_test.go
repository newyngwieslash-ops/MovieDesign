package events

import (
	"context"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"path/filepath"
	"testing"
)

func TestReplayScopeAndBoundedWake(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "e.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	p, e := (store.Objects{DB: db}).CreateProject(ctx, "p", "p")
	if e != nil {
		t.Fatal(e)
	}
	q, e := (store.Objects{DB: db}).CreateProject(ctx, "q", "q")
	if e != nil {
		t.Fatal(e)
	}
	for _, project := range []any{p.ID, q.ID, nil} {
		if _, e = db.Exec("INSERT INTO events(project_id,session_id,type,payload_json,created_at)VALUES(?,'s','fixture','{}',1)", project); e != nil {
			t.Fatal(e)
		}
	}
	s := NewService(db)
	ch, unsubscribe := s.Subscribe()
	defer unsubscribe()
	for i := 0; i < 1000; i++ {
		s.Notify()
	}
	if len(ch) != 1 {
		t.Fatal("wake memory must remain bounded")
	}
	v, e := s.Read(ctx, p.ID, "s", 0)
	if e != nil || len(v) != 1 || v[0].ProjectID != p.ID {
		t.Fatalf("scoped durable replay: %v %v", v, e)
	}
	v, e = s.Read(ctx, p.ID, "s", v[0].ID)
	if e != nil || len(v) != 0 {
		t.Fatal("cursor duplicated event", e)
	}
	if _, e = s.Read(ctx, p.ID, "s", 9999); e == nil {
		t.Fatal("future cursor accepted")
	}
	s.Close()
	<-ch
	if _, open := <-ch; open {
		t.Fatal("shutdown retained watcher")
	}
}
