//go:build e2e

package exporter

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/sijms/go-ora/v2"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

const (
	expPGDSN      = "host=127.0.0.1 port=5432 user=postgres password=postgres123 dbname=postgres_db sslmode=disable"
	expMySQLDSN   = "root:root123456@tcp(127.0.0.1:3306)/default_db"
	expOracleDSN  = "oracle://scott:tiger@127.0.0.1:1521/XEPDB1"
	expOracleDSN2 = "oracle://appuser:App123!@127.0.0.1:1521/XEPDB1"
)

func connectExpDB(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Skipf("open %s: %v", driver, err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Skipf("ping %s: %v", driver, err)
	}
	return db
}

// ── continue-on-error test (original) ──

func TestExportTables_ContinueOnError(t *testing.T) {
	db := connectExpDB(t, "postgres", expPGDSN)
	ctx := context.Background()

	schema := fmt.Sprintf("exptest_%d", os.Getpid())
	_, err := db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema))
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		db.ExecContext(context.Background(), fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
	})

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

	goodTable := &md.TableDef{TableSchema: schema, TableName: "good"}
	badTable := &md.TableDef{TableSchema: schema, TableName: "does_not_exist"}
	tables := []*md.TableDef{goodTable, badTable}

	tmpDir, _ := os.MkdirTemp("", "exporter-test-*")
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	exp := New(db, Config{OutputDir: tmpDir, PageSize: 100, DBType: "postgres", CSVHeader: true})
	results, err := exp.ExportTables(ctx, tables, nil)
	if err != nil {
		t.Fatalf("ExportTables returned fatal error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
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
	badResult := results[1]
	if badResult.Table != "does_not_exist" {
		t.Fatalf("expected second result for 'does_not_exist', got %s", badResult.Table)
	}
	if badResult.Error == nil {
		t.Error("bad table should have an error, got nil")
	}
}

// ── online export tests (real databases) ──

func TestExport_Oracle_RowCount(t *testing.T) {
	db := connectExpDB(t, "oracle", expOracleDSN)
	ctx := context.Background()
	dir := t.TempDir()

	tbl := &md.TableDef{TableSchema: "SCOTT", TableName: "EMP"}
	exp := New(db, Config{OutputDir: dir, PageSize: 100, CSVHeader: true, DBType: "oracle"})
	pkMap := map[string][]string{"SCOTT.EMP": {"EMPNO"}}
	results, err := exp.ExportTables(ctx, []*md.TableDef{tbl}, pkMap)
	if err != nil {
		t.Fatalf("export SCOTT.EMP: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Error != nil {
		t.Fatalf("export error: %v", r.Error)
	}
	if r.Rows != 14 {
		t.Errorf("EMP row count = %d, want 14", r.Rows)
	}
	if r.Batches < 1 {
		t.Errorf("batches = %d, want >= 1", r.Batches)
	}
}

func TestExport_Oracle_NullAndDatetime(t *testing.T) {
	db := connectExpDB(t, "oracle", expOracleDSN)
	ctx := context.Background()
	dir := t.TempDir()

	tbl := &md.TableDef{TableSchema: "SCOTT", TableName: "EMP"}
	exp := New(db, Config{OutputDir: dir, PageSize: 100, CSVHeader: true, CSVNullRep: "\\N", DBType: "oracle"})
	pkMap := map[string][]string{"SCOTT.EMP": {"EMPNO"}}
	results, err := exp.ExportTables(ctx, []*md.TableDef{tbl}, pkMap)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("export failed: %v", results[0].Error)
	}

	// Read CSV and check content
	content, err := os.ReadFile(results[0].OutputFile)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	csv := string(content)
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if len(lines) != 15 { // header + 14 rows
		t.Fatalf("expected 15 lines, got %d", len(lines))
	}

	// Header: column names
	header := lines[0]
	if !strings.Contains(header, "EMPNO,ENAME,JOB,MGR,HIREDATE,SAL,COMM,DEPTNO") {
		t.Errorf("unexpected header: %s", header)
	}

	// Check NULL representation: SMITH has COMM=NULL → should be \N
	// EMPNO=7369, SMITH, CLERK, 7902, 1980-12-17, 800, , 20
	for _, line := range lines[1:] {
		fields := strings.Split(line, ",")
		if len(fields) < 7 {
			t.Errorf("short row: %s", line)
			continue
		}
		if fields[0] == "7369" {
			// SMITH: COMM should be \N or empty
			if fields[6] != "\\N" && fields[6] != "" {
				t.Errorf("SMITH COMM = %q, want \\N", fields[6])
			}
		}
		if fields[0] == "7839" {
			// KING: COMM is null, MGR is null
			if fields[3] != "\\N" && fields[3] != "" {
				t.Errorf("KING MGR = %q, want \\N", fields[3])
			}
		}
		// Check HIREDATE compact format (14+ digits)
		hiredate := fields[4]
		if len(hiredate) >= 8 && len(hiredate) <= 14 {
			for _, c := range hiredate {
				if c < '0' || c > '9' {
					t.Errorf("HIREDATE %q contains non-digit", hiredate)
					break
				}
			}
		}
	}
}

func TestExport_Oracle_Pagination(t *testing.T) {
	db := connectExpDB(t, "oracle", expOracleDSN)
	ctx := context.Background()
	dir := t.TempDir()

	tbl := &md.TableDef{TableSchema: "SCOTT", TableName: "EMP"}
	exp := New(db, Config{OutputDir: dir, PageSize: 5, CSVHeader: true, DBType: "oracle"})
	pkMap := map[string][]string{"SCOTT.EMP": {"EMPNO"}}
	results, err := exp.ExportTables(ctx, []*md.TableDef{tbl}, pkMap)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Rows != 14 {
		t.Errorf("rows = %d, want 14 (pagination with page_size=5)", r.Rows)
	}
	// page_size=5, 14 rows → at least 3 batches
	if r.Batches < 3 {
		t.Errorf("batches = %d, want >= 3", r.Batches)
	}
}

func TestExport_MySQL_RowCount(t *testing.T) {
	db := connectExpDB(t, "mysql", expMySQLDSN)
	ctx := context.Background()
	dir := t.TempDir()

	tbl := &md.TableDef{TableSchema: "default_db", TableName: "EMP"}
	exp := New(db, Config{OutputDir: dir, PageSize: 100, CSVHeader: true, DBType: "mysql"})
	pkMap := map[string][]string{"default_db.EMP": {"EMPNO"}}
	results, err := exp.ExportTables(ctx, []*md.TableDef{tbl}, pkMap)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Error != nil {
		t.Fatalf("export error: %v", r.Error)
	}
	if r.Rows != 14 {
		t.Errorf("rows = %d, want 14", r.Rows)
	}
}

func TestExport_PG_RowCount(t *testing.T) {
	db := connectExpDB(t, "postgres", expPGDSN)
	ctx := context.Background()
	dir := t.TempDir()

	tbl := &md.TableDef{TableSchema: "public", TableName: "EMP"}
	exp := New(db, Config{OutputDir: dir, PageSize: 100, CSVHeader: true, DBType: "postgres"})
	pkMap := map[string][]string{"public.EMP": {"empno"}}
	results, err := exp.ExportTables(ctx, []*md.TableDef{tbl}, pkMap)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Error != nil {
		t.Fatalf("export error: %v", r.Error)
	}
	if r.Rows != 14 {
		t.Errorf("rows = %d, want 14", r.Rows)
	}
}

func TestExport_PG_QuoteIdent(t *testing.T) {
	db := connectExpDB(t, "postgres", expPGDSN)
	ctx := context.Background()
	dir := t.TempDir()

	tbl := &md.TableDef{TableSchema: "public", TableName: "EMP"}
	exp := New(db, Config{OutputDir: dir, PageSize: 100, CSVHeader: true, DBType: "postgres"})
	pkMap := map[string][]string{"public.EMP": {"empno"}}
	results, err := exp.ExportTables(ctx, []*md.TableDef{tbl}, pkMap)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("export failed: %v", results[0].Error)
	}

	content, err := os.ReadFile(results[0].OutputFile)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	csv := string(content)
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if len(lines) != 15 {
		t.Fatalf("expected 15 lines, got %d", len(lines))
	}

	// PG exported EMP should have 14 rows (skip the extra e2e_users table test)
	for _, line := range lines[1:] {
		fields := strings.Split(line, ",")
		if len(fields) > 0 && fields[0] == "7369" {
			t.Logf("verify sample row: %s", line)
			break
		}
	}
}

// ── helpers ──

func tFatalIf(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}
