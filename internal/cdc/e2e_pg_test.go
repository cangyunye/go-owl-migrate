//go:build e2e

package cdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/postgres"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

const pgE2EDSN = "host=127.0.0.1 port=5432 user=postgres password=postgres123 dbname=postgres_db sslmode=disable"

func openPG(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", pgE2EDSN)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("pg unreachable: %v", err)
	}
	return db
}

// applyStatements splits a CDC-generated DDL block into statements and executes
// them one by one (functions contain $$ bodies, so simple split on ";" is safe
// here since plpgsql bodies end with "$$ LANGUAGE plpgsql;").
func applyStatements(t *testing.T, db *sql.DB, block string) {
	t.Helper()
	for _, stmt := range splitDDL(block) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec DDL: %v\nstmt: %s", err, stmt)
		}
	}
}

func splitDDL(block string) []string {
	var out []string
	var cur strings.Builder
	var inDollar bool
	lines := strings.Split(block, "\n")
	for _, line := range lines {
		if strings.Contains(line, "$$") {
			inDollar = !inDollar
		}
		cur.WriteString(line + "\n")
		if !inDollar && strings.HasSuffix(strings.TrimSpace(line), ";") {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}

func makeEMPTableDef(schema string) *md.TableDef {
	t, _ := md.NewTableDef(schema, "emp")
	empno, _ := md.NewColumnDef(schema, "emp", "empno", 1, "INTEGER")
	empno.Nullable = "NO"
	ename, _ := md.NewColumnDef(schema, "emp", "ename", 2, "VARCHAR")
	ename.DataLength = 20
	t.AddColumn(empno)
	t.AddColumn(ename)
	t.AddPrimaryKey("pk_emp", "empno")
	return t
}

func TestE2E_PGTriggerCaptureAndReplay(t *testing.T) {
	db := openPG(t)
	schema := fmt.Sprintf("owlcdc_%d", time.Now().UnixNano()%100000)
	if _, err := db.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)) })

	// Source table
	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE "%s".emp (empno INT PRIMARY KEY, ename VARCHAR(20))`, schema)); err != nil {
		t.Fatalf("create source: %v", err)
	}

	builder := postgres.PGCDCBuilder{}
	opts := dialect.CDCOptions{SchemaMapping: map[string]string{schema: schema}}

	// Changelog table + triggers
	chgDDL, err := builder.BuildChangelogTable(makeEMPTableDef(schema), opts)
	if err != nil {
		t.Fatalf("changelog ddl: %v", err)
	}
	trgDDL, err := builder.BuildSyncTrigger(makeEMPTableDef(schema), opts)
	if err != nil {
		t.Fatalf("trigger ddl: %v", err)
	}
	applyStatements(t, db, chgDDL)
	applyStatements(t, db, trgDDL)

	// Target table
	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE "%s".emp_target (empno INT PRIMARY KEY, ename VARCHAR(20))`, schema)); err != nil {
		t.Fatalf("create target: %v", err)
	}

	// Source DML
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec %s: %v", q, err)
		}
	}
	mustExec(fmt.Sprintf(`INSERT INTO "%s".emp (empno, ename) VALUES ($1,$2)`, schema), 1, "ADAMS")
	mustExec(fmt.Sprintf(`INSERT INTO "%s".emp (empno, ename) VALUES ($1,$2)`, schema), 2, "BLAKE")
	mustExec(fmt.Sprintf(`UPDATE "%s".emp SET ename=$1 WHERE empno=$2`, schema), "BLAKE2", 2)
	mustExec(fmt.Sprintf(`DELETE FROM "%s".emp WHERE empno=$1`, schema), 1)

	// Poll changelog
	poller := &Poller{DB: db, Changelog: fmt.Sprintf(`"%s"."owl_chg_emp"`, schema), BatchSize: 100}
	ctx := context.Background()
	changes, maxID, err := poller.PollAfter(ctx, 0)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	t.Logf("polled %d changes, maxID=%d", len(changes), maxID)
	if len(changes) != 4 {
		t.Fatalf("expected 4 changes (I,I,U,D), got %d", len(changes))
	}

	// Verify operation sequence
	wantOps := []string{"I", "I", "U", "D"}
	for i, c := range changes {
		if c.OpType != wantOps[i] {
			t.Errorf("change %d op=%q, want %q", i, c.OpType, wantOps[i])
		}
	}

	// Replay to target
	tt := &TargetTable{
		Table:       fmt.Sprintf(`%s.emp_target`, schema),
		Columns:     []string{"empno", "ename"},
		KeyCols:     []string{"empno"},
		Quoter:      func(n string) string { return `"` + n + `"` },
		Placeholder: func(i int) string { return fmt.Sprintf("$%d", i) },
	}
	// Start a transaction so multiples can replay without savepoints.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for _, ch := range changes {
		stmt, args, err := BuildReplaySQL(tt, ch)
		if err != nil {
			tx.Rollback()
			t.Fatalf("build replay: %v", err)
		}
		if _, err := tx.Exec(stmt, args...); err != nil {
			tx.Rollback()
			t.Fatalf("exec replay %s: %v", stmt, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Verify target: row 1 deleted, row 2 updated
	var count int
	if err := db.QueryRow(fmt.Sprintf(`SELECT count(*) FROM "%s".emp_target`, schema)).Scan(&count); err != nil {
		t.Fatalf("count target: %v", err)
	}
	if count != 1 {
		t.Errorf("target count = %d, want 1 (empno=1 deleted, empno=2 kept)", count)
	}
	var ename string
	if err := db.QueryRow(fmt.Sprintf(`SELECT ename FROM "%s".emp_target WHERE empno=2`, schema)).Scan(&ename); err != nil {
		t.Fatalf("read target: %v", err)
	}
	if ename != "BLAKE2" {
		t.Errorf("target ename for empno=2 = %q, want BLAKE2", ename)
	}
}

func TestE2E_PGTruncateCaptured(t *testing.T) {
	db := openPG(t)
	schema := fmt.Sprintf("owltrc_%d", time.Now().UnixNano()%100000)
	if _, err := db.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)) })

	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE "%s".emp (empno INT PRIMARY KEY, ename VARCHAR(20))`, schema)); err != nil {
		t.Fatalf("create source: %v", err)
	}
	if _, err := db.Exec(fmt.Sprintf(`INSERT INTO "%s".emp VALUES (1,'A'),(2,'B')`, schema)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	builder := postgres.PGCDCBuilder{}
	opts := dialect.CDCOptions{SchemaMapping: map[string]string{schema: schema}}
	chgDDL, _ := builder.BuildChangelogTable(makeEMPTableDef(schema), opts)
	trgDDL, _ := builder.BuildSyncTrigger(makeEMPTableDef(schema), opts)
	applyStatements(t, db, chgDDL)
	applyStatements(t, db, trgDDL)

	if _, err := db.Exec(fmt.Sprintf(`TRUNCATE TABLE "%s".emp`, schema)); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	poller := &Poller{DB: db, Changelog: fmt.Sprintf(`"%s"."owl_chg_emp"`, schema), BatchSize: 100}
	changes, _, err := poller.PollAfter(context.Background(), 0)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(changes) != 1 || changes[0].OpType != "T" {
		t.Fatalf("expected 1 T change, got %+v", changes)
	}
}

func TestE2E_PGChangeJSONContainsColumns(t *testing.T) {
	db := openPG(t)
	schema := fmt.Sprintf("owljson_%d", time.Now().UnixNano()%100000)
	if _, err := db.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)) })

	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE "%s".emp (empno INT PRIMARY KEY, ename VARCHAR(20))`, schema)); err != nil {
		t.Fatalf("create source: %v", err)
	}
	builder := postgres.PGCDCBuilder{}
	opts := dialect.CDCOptions{SchemaMapping: map[string]string{schema: schema}}
	chgDDL, _ := builder.BuildChangelogTable(makeEMPTableDef(schema), opts)
	trgDDL, _ := builder.BuildSyncTrigger(makeEMPTableDef(schema), opts)
	applyStatements(t, db, chgDDL)
	applyStatements(t, db, trgDDL)

	if _, err := db.Exec(fmt.Sprintf(`INSERT INTO "%s".emp VALUES (7,'KING')`, schema)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	poller := &Poller{DB: db, Changelog: fmt.Sprintf(`"%s"."owl_chg_emp"`, schema), BatchSize: 100}
	changes, _, _ := poller.PollAfter(context.Background(), 0)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	var m map[string]any
	if err := json.Unmarshal(changes[0].NewData, &m); err != nil {
		t.Fatalf("unmarshal new_data: %v (%s)", err, changes[0].NewData)
	}
	if int(m["empno"].(float64)) != 7 || m["ename"] != "KING" {
		t.Errorf("new_data = %v, want empno=7 ename=KING", m)
	}
}

func TestE2E_PGFileBatchSpillAndApply(t *testing.T) {
	db := openPG(t)
	schema := fmt.Sprintf("owlfb_%d", time.Now().UnixNano()%100000)
	if _, err := db.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)) })

	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE "%s".emp (empno INT PRIMARY KEY, ename VARCHAR(20))`, schema)); err != nil {
		t.Fatalf("create source: %v", err)
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE "%s".emp_target (empno INT PRIMARY KEY, ename VARCHAR(20))`, schema)); err != nil {
		t.Fatalf("create target: %v", err)
	}

	builder := postgres.PGCDCBuilder{}
	opts := dialect.CDCOptions{SchemaMapping: map[string]string{schema: schema}}
	chgDDL, err := builder.BuildChangelogTable(makeEMPTableDef(schema), opts)
	if err != nil {
		t.Fatalf("changelog: %v", err)
	}
	trgDDL, err := builder.BuildSyncTrigger(makeEMPTableDef(schema), opts)
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	applyStatements(t, db, chgDDL)
	applyStatements(t, db, trgDDL)

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec %s: %v", q, err)
		}
	}
	mustExec(fmt.Sprintf(`INSERT INTO "%s".emp (empno, ename) VALUES ($1,$2)`, schema), 1, "AA")
	mustExec(fmt.Sprintf(`INSERT INTO "%s".emp (empno, ename) VALUES ($1,$2)`, schema), 2, "BB")
	mustExec(fmt.Sprintf(`UPDATE "%s".emp SET ename=$1 WHERE empno=$2`, schema), "BB2", 2)
	mustExec(fmt.Sprintf(`DELETE FROM "%s".emp WHERE empno=$1`, schema), 1)

	// Poll and spill to a batch file via BatchWriter.
	polled, _, err := (&Poller{DB: db, Changelog: fmt.Sprintf(`"%s"."owl_chg_emp"`, schema), BatchSize: 100}).PollAfter(context.Background(), 0)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(polled) != 4 {
		t.Fatalf("expected 4 changes, got %d", len(polled))
	}

	bw := &BatchWriter{
		PendingDir: filepath.Join(t.TempDir(), "pending"),
		Begin:      "BEGIN;",
		Commit:     "COMMIT;",
		Wrap:       true,
	}
	pb, err := bw.Write("emp", polled, &TargetTable{
		Table:       fmt.Sprintf(`%s.emp_target`, schema),
		Columns:     []string{"empno", "ename"},
		KeyCols:     []string{"empno"},
		Quoter:      func(n string) string { return `"` + n + `"` },
		Placeholder: func(i int) string { return "$" },
	}, time.Now())
	if err != nil {
		t.Fatalf("batch write: %v", err)
	}

	// Simulate the runner: execute each DML statement (lib/pq can't run the
	// whole multi-statement file in one Exec, so apply statement-by-statement).
	content, err := os.ReadFile(pb.Path)
	if err != nil {
		t.Fatalf("read batch: %v", err)
	}
	lines := strings.Split(string(content), "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || ln == "BEGIN;" || ln == "COMMIT;" {
			continue
		}
		if _, err := db.Exec(ln); err != nil {
			t.Fatalf("apply batch statement %q: %v", ln, err)
		}
	}

	var count int
	if err := db.QueryRow(fmt.Sprintf(`SELECT count(*) FROM "%s".emp_target`, schema)).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("target count = %d, want 1", count)
	}
	var ename string
	if err := db.QueryRow(fmt.Sprintf(`SELECT ename FROM "%s".emp_target WHERE empno=2`, schema)).Scan(&ename); err != nil {
		t.Fatalf("read: %v", err)
	}
	if ename != "BB2" {
		t.Errorf("ename = %q, want BB2", ename)
	}
}
