//go:build e2e

package importer

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/lib/pq"
	"go.uber.org/zap"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

const pgDSN = "host=127.0.0.1 port=5432 user=postgres password=postgres123 dbname=postgres_db sslmode=disable"

func connectPG(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", pgDSN)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Skipf("PostgreSQL not reachable: %v", err)
	}
	return db
}

func setupSchema(t *testing.T, db *sql.DB) string {
	t.Helper()
	schema := fmt.Sprintf("imptest_%d", os.Getpid())
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		db.ExecContext(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema))
	})
	return schema
}

func writeCSV(t *testing.T, dir, schema, table string, rows []string) {
	t.Helper()
	filename := fmt.Sprintf("%s.%s.csv", schema, table)
	path := filepath.Join(dir, filename)
	content := ""
	for _, r := range rows {
		content += r + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
}

func countRows(t *testing.T, db *sql.DB, schema, table string) int {
	t.Helper()
	var count int
	err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s.%s", schema, table)).Scan(&count)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

func TestImporter_SkipRow_RollbackAndContinue(t *testing.T) {
	db := connectPG(t)
	schema := setupSchema(t, db)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, fmt.Sprintf(
		`CREATE TABLE %s.emp (id integer PRIMARY KEY, name text NOT NULL)`, schema))
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	dir := t.TempDir()
	writeCSV(t, dir, schema, "emp", []string{
		"id,name",
		"1,Alice",
		"2,Bob",
		"1,Duplicate",
		"3,Charlie",
		"4,Dave",
	})

	tbl := &md.TableDef{
		TableSchema: schema,
		TableName:   "emp",
	}

	logger, _ := zap.NewDevelopment()
	imp := New(db, Config{
		SourceDir:      dir,
		CommitInterval: 100,
		ErrorPolicy:    "skip_row",
		MaxWorkers:     1,
		TargetDBType:   "postgres",
		Logger:         logger,
	})

	results, err := imp.ImportTables(ctx, []*md.TableDef{tbl}, nil)
	if err != nil {
		t.Fatalf("ImportTables: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Err != nil {
		t.Fatalf("unexpected error: %v", r.Err)
	}
	// Rows 1,2 are rolled back when row 3 (dup PK) fails;
	// rows 4,5 succeed in the new transaction.
	if r.Actual != 2 {
		t.Errorf("inserted = %d, want 2 (rows after rollback)", r.Actual)
	}
	if r.Skipped != 1 {
		t.Errorf("skipped = %d, want 1 (duplicate PK)", r.Skipped)
	}

	got := countRows(t, db, schema, "emp")
	if got != 2 {
		t.Errorf("table has %d rows, want 2", got)
	}
}

func TestImporter_Stop_RollbackOnError(t *testing.T) {
	db := connectPG(t)
	schema := setupSchema(t, db)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, fmt.Sprintf(
		`CREATE TABLE %s.dept (id integer PRIMARY KEY, name text NOT NULL)`, schema))
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	dir := t.TempDir()
	writeCSV(t, dir, schema, "dept", []string{
		"id,name",
		"10,Engineering",
		"20,Sales",
		"10,Duplicate",
		"30,Marketing",
	})

	tbl := &md.TableDef{
		TableSchema: schema,
		TableName:   "dept",
	}

	logger, _ := zap.NewDevelopment()
	imp := New(db, Config{
		SourceDir:      dir,
		CommitInterval: 100,
		ErrorPolicy:    "stop",
		MaxWorkers:     1,
		TargetDBType:   "postgres",
		Logger:         logger,
	})

	results, err := imp.ImportTables(ctx, []*md.TableDef{tbl}, nil)
	if err != nil {
		t.Fatalf("ImportTables: %v", err)
	}

	r := results[0]
	if r.Err == nil {
		t.Fatal("expected error for stop policy, got nil")
	}
	// stop policy rolls back the entire transaction, so no rows are persisted
	if r.Actual != 0 {
		t.Errorf("inserted = %d, want 0 (stop policy rolls back all)", r.Actual)
	}

	got := countRows(t, db, schema, "dept")
	if got != 0 {
		t.Errorf("table has %d rows after rollback, want 0 (stop policy rolls back)", got)
	}
}

func TestImporter_CommitInterval(t *testing.T) {
	db := connectPG(t)
	schema := setupSchema(t, db)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, fmt.Sprintf(
		`CREATE TABLE %s.batch (id integer PRIMARY KEY, val text)`, schema))
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	dir := t.TempDir()
	rows := []string{"id,val"}
	for i := 1; i <= 10; i++ {
		rows = append(rows, fmt.Sprintf("%d,row%d", i, i))
	}
	writeCSV(t, dir, schema, "batch", rows)

	tbl := &md.TableDef{
		TableSchema: schema,
		TableName:   "batch",
	}

	logger, _ := zap.NewDevelopment()
	imp := New(db, Config{
		SourceDir:      dir,
		CommitInterval: 3,
		ErrorPolicy:    "skip_row",
		MaxWorkers:     1,
		TargetDBType:   "postgres",
		Logger:         logger,
	})

	results, err := imp.ImportTables(ctx, []*md.TableDef{tbl}, nil)
	if err != nil {
		t.Fatalf("ImportTables: %v", err)
	}

	r := results[0]
	if r.Err != nil {
		t.Fatalf("unexpected error: %v", r.Err)
	}
	if r.Actual != 10 {
		t.Errorf("inserted = %d, want 10", r.Actual)
	}

	got := countRows(t, db, schema, "batch")
	if got != 10 {
		t.Errorf("table has %d rows, want 10", got)
	}
}

func TestImporter_SkipRow_MaxErrors(t *testing.T) {
	db := connectPG(t)
	schema := setupSchema(t, db)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, fmt.Sprintf(
		`CREATE TABLE %s.limited (id integer PRIMARY KEY, name text)`, schema))
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	dir := t.TempDir()
	writeCSV(t, dir, schema, "limited", []string{
		"id,name",
		"1,ok",
		"1,dup1",
		"1,dup2",
		"1,dup3",
		"2,ok2",
	})

	tbl := &md.TableDef{
		TableSchema: schema,
		TableName:   "limited",
	}

	logger, _ := zap.NewDevelopment()
	imp := New(db, Config{
		SourceDir:      dir,
		CommitInterval: 100,
		ErrorPolicy:    "skip_row",
		MaxErrors:      2,
		MaxWorkers:     1,
		TargetDBType:   "postgres",
		Logger:         logger,
	})

	results, err := imp.ImportTables(ctx, []*md.TableDef{tbl}, nil)
	if err != nil {
		t.Fatalf("ImportTables: %v", err)
	}

	r := results[0]
	if r.Err == nil {
		t.Fatal("expected max errors error, got nil")
	}
	if r.Errors < 2 {
		t.Errorf("errors = %d, want >= 2", r.Errors)
	}
}

func TestImporter_TruncateBefore(t *testing.T) {
	db := connectPG(t)
	schema := setupSchema(t, db)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, fmt.Sprintf(
		`CREATE TABLE %s.trunc (id integer PRIMARY KEY, name text)`, schema))
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s.trunc VALUES (99, 'old')`, schema))

	dir := t.TempDir()
	writeCSV(t, dir, schema, "trunc", []string{
		"id,name",
		"1,new1",
		"2,new2",
	})

	tbl := &md.TableDef{
		TableSchema: schema,
		TableName:   "trunc",
	}

	logger, _ := zap.NewDevelopment()
	imp := New(db, Config{
		SourceDir:      dir,
		CommitInterval: 100,
		TruncateBefore: true,
		ErrorPolicy:    "skip_row",
		MaxWorkers:     1,
		TargetDBType:   "postgres",
		Logger:         logger,
	})

	results, err := imp.ImportTables(ctx, []*md.TableDef{tbl}, nil)
	if err != nil {
		t.Fatalf("ImportTables: %v", err)
	}

	r := results[0]
	if r.Err != nil {
		t.Fatalf("unexpected error: %v", r.Err)
	}

	got := countRows(t, db, schema, "trunc")
	if got != 2 {
		t.Errorf("table has %d rows after truncate+import, want 2", got)
	}
}
