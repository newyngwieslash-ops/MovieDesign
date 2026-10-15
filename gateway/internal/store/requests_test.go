package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestRequestPersistenceReplayAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	scope := Scope{ProjectID: "p", Operation: "create", RequestID: "r"}
	calls := 0
	mutate := func(tx *sql.Tx) ([]byte, error) {
		calls++
		_, err := tx.Exec(`INSERT INTO projects VALUES('p','Project',1)`)
		return []byte(`{"id":"p"}`), err
	}
	got, err := WriteRequest(ctx, db, scope, []byte(`{"a":1}`), mutate)
	if err != nil || string(got) != `{"id":"p"}` {
		t.Fatalf("write: %s %v", got, err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err = WriteRequest(ctx, db, scope, []byte(`{"a":1}`), mutate)
	if err != nil || calls != 1 || string(got) != `{"id":"p"}` {
		t.Fatalf("replay %s %v calls %d", got, err, calls)
	}
	_, err = WriteRequest(ctx, db, scope, []byte(`{"a":2}`), mutate)
	if !errors.Is(err, ErrRequestConflict) || calls != 1 {
		t.Fatalf("conflict %v", err)
	}
	scope.RequestID = "rollback"
	_, err = WriteRequest(ctx, db, scope, []byte(`{}`), func(tx *sql.Tx) ([]byte, error) {
		_, err := tx.Exec(`INSERT INTO projects VALUES('rollback','Rollback',1)`)
		if err != nil {
			return nil, err
		}
		return nil, errors.New("abort")
	})
	if err == nil {
		t.Fatal("missing failure")
	}
	for _, q := range []string{`SELECT count(*) FROM projects WHERE id='rollback'`, `SELECT count(*) FROM write_requests WHERE request_id='rollback'`} {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil || n != 0 {
			t.Fatalf("rollback lost: %d %v", n, err)
		}
	}
	for _, table := range []string{"projects", "composition_types", "asset_projects", "write_requests", "file_operations"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s %d %v", table, n, err)
		}
	}
}
func TestRequestCanonicalInput(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "canonical.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	scope := Scope{ProjectID: "p", Operation: "update", RequestID: "r"}
	calls := 0
	mutate := func(*sql.Tx) ([]byte, error) { calls++; return []byte(`{"ok":true}`), nil }
	if _, err = WriteRequest(context.Background(), db, scope, []byte(`{"b":2,"a":1}`), mutate); err != nil {
		t.Fatal(err)
	}
	if _, err = WriteRequest(context.Background(), db, scope, []byte(`{ "a":1, "b":2 }`), mutate); err != nil || calls != 1 {
		t.Fatalf("canonical replay: %v calls %d", err, calls)
	}
	scope.RequestID = "invalid"
	if _, err = WriteRequest(context.Background(), db, scope, []byte(`{"a":1,"a":2}`), mutate); err == nil {
		t.Fatal("duplicate input accepted")
	}
}

func TestRequestPendingAndScope(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "pending.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO write_requests VALUES('p','op','r','{}','pending',NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	mutate := func(*sql.Tx) ([]byte, error) { calls++; return []byte(`{}`), nil }
	scope := Scope{ProjectID: "p", Operation: "op", RequestID: "r"}
	if _, err := WriteRequest(context.Background(), db, scope, []byte(`{}`), mutate); !errors.Is(err, ErrRequestPending) || calls != 0 {
		t.Fatalf("pending was re-executed: %v", err)
	}
	scope.Operation = "other"
	if _, err := WriteRequest(context.Background(), db, scope, []byte(`{}`), mutate); err != nil || calls != 1 {
		t.Fatalf("operation scopes collided: %v", err)
	}
	scope.ProjectID = "../p"
	if _, err := WriteRequest(context.Background(), db, scope, []byte(`{}`), mutate); err == nil || calls != 1 {
		t.Fatal("invalid scope accepted")
	}
}
