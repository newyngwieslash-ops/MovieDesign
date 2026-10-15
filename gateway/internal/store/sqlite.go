package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/migrations"
	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(abs)}
	q := u.Query()
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	// ponytail: one connection serializes local writes; increase only with measured contention.
	db.SetMaxOpenConns(1)
	if err = migrateDB(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrateDB(db *sql.DB) error {
	if err := db.Ping(); err != nil {
		return err
	}
	source, err := iofs.New(migrations.Files, ".")
	if err != nil {
		return err
	}
	defer source.Close()
	driver, err := migratesqlite.WithInstance(db, &migratesqlite.Config{})
	if err != nil {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		return err
	}
	// The caller owns db; m.Close would close it too.
	if err = m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}
