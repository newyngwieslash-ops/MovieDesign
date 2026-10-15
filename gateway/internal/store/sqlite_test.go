package store

import (
	"path/filepath"
	"testing"
)

func TestMigrationsAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "项目 #1.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"assets", "compositions", "resources", "versions", "composition_members"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s: %d %v", table, count, err)
		}
	}
	if _, err = db.Exec("INSERT INTO compositions(id,project_id,type_id,name) VALUES('c','p','video','宣传片')"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO versions(id,resource_id,asset_id,created_at) VALUES('v','r','missing',1)"); err == nil {
		t.Fatal("foreign key not enforced")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err = db.QueryRow("SELECT name FROM compositions WHERE id='c'").Scan(&name); err != nil || name != "宣传片" {
		t.Fatalf("lost persistent state: %q %v", name, err)
	}
	var version int
	var dirty bool
	if err = db.QueryRow("SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != 3 || dirty {
		t.Fatalf("migration state %d/%v: %v", version, dirty, err)
	}
}
