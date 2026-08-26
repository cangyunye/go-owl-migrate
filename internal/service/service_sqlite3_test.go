//go:build sqlite3

package service

import (
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

func TestOpenDB_SQLite3(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	db, err := OpenDB(config.DBConfig{Type: "sqlite3", DSN: dbPath})
	if err != nil {
		t.Fatalf("OpenDB(sqlite3): %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
