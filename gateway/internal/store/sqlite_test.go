package store

import (
	"path/filepath"
	"testing"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/migrations"
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
	if err = db.QueryRow("SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != 16 || dirty {
		t.Fatalf("migration state %d/%v: %v", version, dirty, err)
	}
	// Exercise the actual support DDL down/up and migration of an existing v14
	// database, preserving its business rows.
	down16, err := migrations.Files.ReadFile("000016_chat_billing.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(down16)); err != nil {
		t.Fatal(err)
	}
	down, err := migrations.Files.ReadFile("000015_inference_stream.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE schema_migrations SET version=14"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var records int
	if err = db.QueryRow("SELECT count(*) FROM inference_stream").Scan(&records); err != nil || records != 0 {
		t.Fatal("v14 journal upgrade", records, err)
	}
	if err = db.QueryRow("SELECT name FROM compositions WHERE id='c'").Scan(&name); err != nil || name != "宣传片" {
		t.Fatal("v14 upgrade lost business row", name, err)
	}
}
