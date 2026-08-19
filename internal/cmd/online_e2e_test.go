//go:build e2e

package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

const onlinePGE2EDSN = "host=127.0.0.1 port=5432 user=postgres password=postgres123 dbname=postgres_db sslmode=disable"

func openOnlinePG(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", onlinePGE2EDSN)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Skipf("pg unreachable: %v", err)
	}
	return db
}

type onlineDirs struct {
	pending, done, failed string
}

func writeFileBatchAdapter(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "adapter.yaml")
	yaml := `adapter:
  name: pgfile
  mode: file-batch
  quote: "\""
  placeholder: "$%d"
  client:
    command: cat
    args_template: "{file}"
    transaction:
      begin: "BEGIN;"
      commit: "COMMIT;"
      wrap: true
  column_map:
    EMP: { EMPNO: "empno", ENAME: "ename" }
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("write adapter: %v", err)
	}
	return path
}

func TestE2E_OnlineInitApplyAndSyncFileBatch(t *testing.T) {
	db := openOnlinePG(t)
	schema := fmt.Sprintf("owlc_%d", time.Now().UnixNano()%100000)
	if _, err := db.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)) })

	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE "%s".emp (empno INT PRIMARY KEY, ename VARCHAR(20))`, schema)); err != nil {
		t.Fatalf("create source: %v", err)
	}

	dirs := &onlineDirs{
		pending: filepath.Join(t.TempDir(), "pending"),
		done:    filepath.Join(t.TempDir(), "done"),
		failed:  filepath.Join(t.TempDir(), "failed"),
	}
	adapterPath := writeFileBatchAdapter(t)

	cfg := &config.Config{
		Metadata: config.MetadataConfig{Type: "database"},
		Source:   config.DBConfig{Type: "postgres", DSN: onlinePGE2EDSN, Schema: schema},
		Target:   config.DBConfig{Type: "postgres", Adapter: adapterPath},
		DDL: config.DDLConfig{
			TargetDialect: "postgres",
			SourceDialect: "postgres",
			SchemaMapping: map[string]string{schema: schema},
		},
		Online: config.OnlineConfig{
			CDC:   config.OnlineCDCConfig{ChangelogPrefix: "owl_chg_", Apply: true},
			Sync:  config.OnlineSyncConfig{PollInterval: "500ms", BatchSize: 100, OnError: "skip"},
			Files: config.OnlineFilesConfig{Pending: dirs.pending, Done: dirs.done, Failed: dirs.failed},
			State: config.OnlineStateConfig{DB: filepath.Join(t.TempDir(), "online.db")},
		},
	}

	// online init --apply on live PG.
	if err := runOnlineInit(context.Background(), cfg); err != nil {
		t.Fatalf("online init apply: %v", err)
	}
	var chgCount int
	if err := db.QueryRow(fmt.Sprintf(`SELECT count(*) FROM information_schema.tables WHERE table_schema='%s' AND table_name='owl_chg_emp'`, schema)).Scan(&chgCount); err != nil {
		t.Fatalf("changelog table check: %v", err)
	}
	if chgCount != 1 {
		t.Fatalf("changelog table not created (count=%d)", chgCount)
	}

	// Seed source rows AFTER install → captured.
	if _, err := db.Exec(fmt.Sprintf(`INSERT INTO "%s".emp VALUES ($1,$2)`, schema), 1, "AA"); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if _, err := db.Exec(fmt.Sprintf(`INSERT INTO "%s".emp VALUES ($1,$2)`, schema), 2, "BB"); err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	e, err := loadSyncEngine(cfg)
	if err != nil {
		t.Fatalf("loadSyncEngine: %v", err)
	}
	defer e.store.Close()
	if err := e.syncOnce(context.Background()); err != nil {
		t.Fatalf("syncOnce: %v", err)
	}

	entries, err := os.ReadDir(dirs.pending)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected pending batch file, err=%v entries=%d", err, len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(dirs.pending, entries[0].Name()))
	if !strings.Contains(string(data), "INSERT INTO") {
		t.Errorf("batch file missing INSERT:\n%s", data)
	}

	cps, _ := e.store.LoadCheckpoints()
	cp, ok := cps["emp"]
	if !ok || cp.FiledChgID < 1 {
		t.Errorf("checkpoint not advanced for emp: %+v ok=%v", cp, ok)
	}
}
