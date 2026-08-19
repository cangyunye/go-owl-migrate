//go:build e2e

package cdc

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "github.com/sijms/go-ora/v2"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/oracle"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

const oracleCDCDSN = "oracle://scott:tiger@127.0.0.1:1521/XEPDB1"

func openOracle(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("oracle", oracleCDCDSN)
	if err != nil {
		t.Fatalf("open oracle: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("oracle unreachable: %v", err)
	}
	return db
}

func oEmpTableDef(schema string) *md.TableDef {
	t, _ := md.NewTableDef(schema, "EMP")
	empno, _ := md.NewColumnDef(schema, "EMP", "EMPNO", 1, "NUMBER")
	empno.Nullable = "NO"
	ename, _ := md.NewColumnDef(schema, "EMP", "ENAME", 2, "VARCHAR2")
	ename.DataLength = 20
	t.AddColumn(empno)
	t.AddColumn(ename)
	t.AddPrimaryKey("pk_emp", "EMPNO")
	return t
}

func execOracleDDL(t *testing.T, db *sql.DB, stmts []string) {
	t.Helper()
	for _, stmt := range stmts {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("oracle exec: %v\nstmt: %s", err, stmt)
		}
	}
}

// splitOracleStatements splits on ";\n/" boundaries produced by the builder.
func splitOracleStatements(block string) []string {
	var out []string
	// Builder uses ";\n/" as statement terminator after each trigger/table.
	parts := strings.Split(block, "/\n")
	for _, p := range parts {
		out = append(out, p)
	}
	return out
}

func TestE2E_OracleTriggerCaptureToPG(t *testing.T) {
	ora := openOracle(t)

	// Isolated table under SCOTT (the only writable test schema).
	tableName := fmt.Sprintf("EMPTEST_%d", time.Now().UnixNano()%1000000)
	chgName := "OWL_CHG_" + tableName

	tbl := oEmpTableDef("SCOTT")
	tbl.TableName = tableName

	if _, err := ora.Exec(fmt.Sprintf(`CREATE TABLE "SCOTT"."%s" (EMPNO NUMBER PRIMARY KEY, ENAME VARCHAR2(20))`, tableName)); err != nil {
		t.Fatalf("create oracle table: %v", err)
	}
	t.Cleanup(func() {
		ora.Exec(fmt.Sprintf(`DROP TABLE "SCOTT"."%s" PURGE`, tableName))
		ora.Exec(fmt.Sprintf(`DROP TRIGGER "SCOTT"."OWL_SYNC_ROW_%s"`, tableName))
		ora.Exec(fmt.Sprintf(`DROP TRIGGER "SCOTT"."OWL_SYNC_TRUNC_%s"`, tableName))
		ora.Exec(fmt.Sprintf(`DROP TABLE "SCOTT"."%s" PURGE`, chgName))
	})

	builder := oracle.OracleCDCBuilder{}
	opts := dialect.CDCOptions{SchemaMapping: map[string]string{"SCOTT": "SCOTT"}}

	chgDDL, err := builder.BuildChangelogTable(tbl, opts)
	if err != nil {
		t.Fatalf("changelog: %v", err)
	}
	trgDDL, err := builder.BuildSyncTrigger(tbl, opts)
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}

	execOracleDDL(t, ora, splitOracleStatements(chgDDL))
	execOracleDDL(t, ora, splitOracleStatements(trgDDL))

	// Source DML
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := ora.Exec(q, args...); err != nil {
			t.Fatalf("oracle dml %s: %v", q, err)
		}
	}
	must(fmt.Sprintf(`INSERT INTO "SCOTT"."%s" (EMPNO, ENAME) VALUES (:1, :2)`, tableName), 1, "KING")
	must(fmt.Sprintf(`INSERT INTO "SCOTT"."%s" (EMPNO, ENAME) VALUES (:1, :2)`, tableName), 2, "SCOTT")
	must(fmt.Sprintf(`UPDATE "SCOTT"."%s" SET ENAME = :1 WHERE EMPNO = :2`, tableName), "SCOTT2", 2)
	must(fmt.Sprintf(`DELETE FROM "SCOTT"."%s" WHERE EMPNO = :1`, tableName), 1)

	// Poll changelog (Oracle syntax: no LIMIT, no $ placeholders)
	rows, err := ora.Query(fmt.Sprintf(
		`SELECT CHG_ID, OP_TYPE, NEW_DATA, OLD_DATA FROM "SCOTT"."%s" ORDER BY CHG_ID`, chgName))
	if err != nil {
		t.Fatalf("query oracle changelog: %v", err)
	}
	defer rows.Close()

	type oChange struct {
		chgID   int64
		op      string
		newData sql.NullString
		oldData sql.NullString
	}
	var changes []oChange
	for rows.Next() {
		var c oChange
		if err := rows.Scan(&c.chgID, &c.op, &c.newData, &c.oldData); err != nil {
			t.Fatalf("scan oracle change: %v", err)
		}
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate oracle changes: %v", err)
	}
	if len(changes) != 4 {
		t.Fatalf("expected 4 oracle changes (I,I,U,D), got %d", len(changes))
	}
	wantOps := []string{"I", "I", "U", "D"}
	for i, c := range changes {
		if strings.TrimSpace(c.op) != wantOps[i] {
			t.Errorf("oracle change %d op=%q, want %q", i, c.op, wantOps[i])
		}
	}
	// Verify new_data JSON for the insert contains columns.
	if !strings.Contains(changes[0].newData.String, "KING") || !strings.Contains(changes[0].newData.String, "EMPNO") {
		t.Errorf("oracle insert new_data = %q, expected JSON with EMPNO/ENAME", changes[0].newData.String)
	}

	// Replay to a PG target.
	pgt := openPG(t)
	pgSchema := fmt.Sprintf("owlora_%d", time.Now().UnixNano()%100000)
	if _, err := pgt.Exec(fmt.Sprintf("CREATE SCHEMA %s", pgSchema)); err != nil {
		t.Fatalf("create pg schema: %v", err)
	}
	t.Cleanup(func() { pgt.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pgSchema)) })
	if _, err := pgt.Exec(fmt.Sprintf(`CREATE TABLE "%s".emp (empno INT PRIMARY KEY, ename VARCHAR(20))`, pgSchema)); err != nil {
		t.Fatalf("create pg target: %v", err)
	}

	tt := &TargetTable{
		Table:       fmt.Sprintf(`%s.emp`, pgSchema),
		Columns:     []string{"EMPNO", "ENAME"},
		KeyCols:     []string{"EMPNO"},
		ColumnMap:   map[string]string{"EMPNO": "empno", "ENAME": "ename"},
		Quoter:      func(n string) string { return `"` + n + `"` },
		Placeholder: func(i int) string { return fmt.Sprintf("$%d", i) },
	}
	tx, _ := pgt.BeginTx(context.Background(), nil)
	for _, c := range changes {
		ch := Change{OpType: strings.TrimSpace(c.op)}
		if c.newData.Valid {
			ch.NewData = []byte(c.newData.String)
		}
		if c.oldData.Valid {
			ch.OldData = []byte(c.oldData.String)
		}
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

	var count int
	if err := pgt.QueryRow(fmt.Sprintf(`SELECT count(*) FROM "%s".emp`, pgSchema)).Scan(&count); err != nil {
		t.Fatalf("count pg: %v", err)
	}
	if count != 1 {
		t.Errorf("pg target count = %d, want 1 (empno=1 deleted, empno=2 kept)", count)
	}
	var ename string
	if err := pgt.QueryRow(fmt.Sprintf(`SELECT ename FROM "%s".emp WHERE empno=2`, pgSchema)).Scan(&ename); err != nil {
		t.Fatalf("read pg: %v", err)
	}
	if ename != "SCOTT2" {
		t.Errorf("pg ename = %q, want SCOTT2", ename)
	}
}
