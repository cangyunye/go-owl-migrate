package exporter

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	_ "github.com/lib/pq"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// connectPostgres opens a connection to the running PostgreSQL test container.
func connectPostgres(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", "host=127.0.0.1 port=5432 user=postgres password=postgres123 dbname=postgres_db sslmode=disable")
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping pg: %v", err)
	}
	return db
}

// TestExportTables_ContinueOnError verifies that when one table fails to export,
// remaining tables are still processed and their results are returned.
func TestExportTables_ContinueOnError(t *testing.T) {
	db := connectPostgres(t)
	ctx := context.Background()

	// Create a temporary schema for this test
	schema := fmt.Sprintf("exptest_%d", os.Getpid())
	_, err := db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema))
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		db.ExecContext(context.Background(), fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
	})

	// Create a table that will succeed
	_, err = db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s.good (
		id integer PRIMARY KEY,
		name text NOT NULL
	)`, schema))
	if err != nil {
		t.Fatalf("create good table: %v", err)
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s.good VALUES (1,'ok'), (2,'fine')`, schema))
	if err != nil {
		t.Fatalf("insert good data: %v", err)
	}

	// Build metadata: two tables, one exists (good), one doesn't (bad)
	goodTable := &md.TableDef{
		TableSchema: schema,
		TableName:   "good",
	}
	badTable := &md.TableDef{
		TableSchema: schema,
		TableName:   "does_not_exist",
	}
	tables := []*md.TableDef{goodTable, badTable}

	// Run export
	tmpDir, _ := os.MkdirTemp("", "exporter-test-*")
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	exp := New(db, Config{
		OutputDir: tmpDir,
		PageSize:  100,
		DBType:    "postgres",
		CSVHeader: true,
	})

	results, err := exp.ExportTables(ctx, tables, nil)
	if err != nil {
		t.Fatalf("ExportTables returned fatal error (should collect per-table errors): %v", err)
	}

	// Verify both tables have results
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Good table: should have data, no error
	goodResult := results[0]
	if goodResult.Table != "good" {
		t.Fatalf("expected first result for 'good', got %s", goodResult.Table)
	}
	if goodResult.Error != nil {
		t.Errorf("good table should not have error, got: %v", goodResult.Error)
	}
	if goodResult.Rows != 2 {
		t.Errorf("good table expected 2 rows, got %d", goodResult.Rows)
	}

	// Bad table: should have an error
	badResult := results[1]
	if badResult.Table != "does_not_exist" {
		t.Fatalf("expected second result for 'does_not_exist', got %s", badResult.Table)
	}
	if badResult.Error == nil {
		t.Error("bad table should have an error, got nil")
	}
}
