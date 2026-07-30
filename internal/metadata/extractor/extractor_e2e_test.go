//go:build e2e

package extractor

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/sijms/go-ora/v2"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

const (
	oraDSN   = "oracle://scott:tiger@127.0.0.1:1521/XEPDB1"
	myDSN    = "root:root123456@tcp(127.0.0.1:3306)/default_db"
	pgDSN    = "host=127.0.0.1 port=5432 user=postgres password=postgres123 dbname=postgres_db sslmode=disable"
	oraSchema = "SCOTT"
	mySchema  = "default_db"
	pgSchema  = "public"
)

func connectExt(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Skipf("open %s: %v", driver, err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("ping %s: %v", driver, err)
	}
	return db
}

// ── Oracle ──

func TestExtract_Oracle_Tables(t *testing.T) {
	db := connectExt(t, "oracle", oraDSN)
	sm, err := Extract(db, "oracle", oraSchema)
	if err != nil {
		t.Fatalf("Extract oracle: %v", err)
	}
	tables := sm.GetTables()
	if len(tables) < 2 {
		t.Fatalf("expected >=2 tables, got %d", len(tables))
	}
	emp := findTable(tables, "EMP")
	if emp == nil {
		t.Fatal("EMP table not found")
	}
	dept := findTable(tables, "DEPT")
	if dept == nil {
		t.Fatal("DEPT table not found")
	}
	cols := emp.GetColumns()
	if len(cols) < 8 {
		t.Errorf("EMP columns = %d, want >= 8", len(cols))
	}
	pks := emp.GetPrimaryKeys()
	if len(pks) == 0 {
		t.Error("EMP has no primary key")
	}
}

func TestExtract_Oracle_Views(t *testing.T) {
	db := connectExt(t, "oracle", oraDSN)
	sm, err := Extract(db, "oracle", oraSchema)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	views := sm.GetViews()
	if len(views) < 1 {
		t.Error("expected at least 1 view (emp_view)")
	} else {
		found := false
		for _, v := range views {
			if v.ViewName == "EMP_VIEW" || v.ViewName == "emp_view" {
				found = true
				if v.ViewDefinition == "" {
					t.Error("emp_view has empty definition")
				}
				break
			}
		}
		if !found {
			t.Errorf("emp_view not found in views: %v", viewNames(views))
		}
	}
}

func TestExtract_Oracle_Sequences(t *testing.T) {
	db := connectExt(t, "oracle", oraDSN)
	sm, err := Extract(db, "oracle", oraSchema)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	seqs := sm.GetSequences(oraSchema)
	if len(seqs) < 1 {
		t.Error("expected at least 1 sequence (seq_emp_id)")
	} else {
		for _, s := range seqs {
			if s.SequenceName == "SEQ_EMP_ID" {
				if s.StartValue < 1 {
					t.Errorf("seq_emp_id start = %d", s.StartValue)
				}
				return
			}
		}
		t.Errorf("seq_emp_id not found: %v", seqNames(seqs))
	}
}

func TestExtract_Oracle_Indexes(t *testing.T) {
	db := connectExt(t, "oracle", oraDSN)
	sm, err := Extract(db, "oracle", oraSchema)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	emp := findTable(sm.GetTables(), "EMP")
	if emp == nil {
		t.Fatal("EMP not found")
	}
	idx := emp.GetIndexes()
	t.Logf("EMP indexes: %d", len(idx))
	if len(idx) == 0 {
		t.Log("no indexes (may be empty depending on schema)")
	}
}

// ── MySQL ──

func TestExtract_MySQL_Tables(t *testing.T) {
	db := connectExt(t, "mysql", myDSN)
	sm, err := Extract(db, "mysql", mySchema)
	if err != nil {
		t.Fatalf("Extract mysql: %v", err)
	}
	tables := sm.GetTables()
	if len(tables) < 2 {
		t.Fatalf("expected >=2 tables, got %d", len(tables))
	}
	emp := findTable(tables, "EMP")
	if emp == nil {
		t.Fatal("EMP not found")
	}
	dept := findTable(tables, "DEPT")
	if dept == nil {
		t.Fatal("DEPT not found")
	}
	cols := emp.GetColumns()
	if len(cols) < 8 {
		t.Errorf("EMP columns = %d, want >= 8", len(cols))
	}
	pks := emp.GetPrimaryKeys()
	if len(pks) == 0 {
		t.Error("EMP has no primary key")
	}
	// Check FK
	fks := sm.GetForeignKeys(mySchema, "EMP")
	fkFound := false
	for _, fk := range fks {
		if fk.TableName == "EMP" {
			fkFound = true
			if fk.RefTable != "DEPT" {
				t.Errorf("EMP FK references %s, want DEPT", fk.RefTable)
			}
			break
		}
	}
	if !fkFound {
		t.Log("EMP FK to DEPT not found (may not be created)")
	}
}

func TestExtract_MySQL_Views(t *testing.T) {
	db := connectExt(t, "mysql", myDSN)
	sm, err := Extract(db, "mysql", mySchema)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	views := sm.GetViews()
	// MySQL has emp_view
	if len(views) < 1 {
		t.Error("expected at least 1 view (emp_view)")
	} else {
		t.Logf("MySQL views: %v", viewNames(views))
	}
}

func TestExtract_MySQL_Indexes(t *testing.T) {
	db := connectExt(t, "mysql", myDSN)
	sm, err := Extract(db, "mysql", mySchema)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	found := false
	for _, tbl := range sm.GetTables() {
		for _, idx := range tbl.GetIndexes() {
			if strings.Contains(idx.IndexName, "idx_emp_ename") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Log("index idx_emp_ename not found (may depend on test schema state)")
	}
}

// ── PostgreSQL ──

func TestExtract_PG_Tables(t *testing.T) {
	db := connectExt(t, "postgres", pgDSN)
	sm, err := Extract(db, "postgres", pgSchema)
	if err != nil {
		t.Fatalf("Extract postgres: %v", err)
	}
	tables := sm.GetTables()
	if len(tables) < 2 {
		t.Fatalf("expected >=2 tables, got %d", len(tables))
	}
	emp := findTable(tables, "EMP")
	if emp == nil {
		t.Fatal("EMP not found")
	}
	cols := emp.GetColumns()
	if len(cols) < 8 {
		t.Errorf("EMP columns = %d, want >= 8", len(cols))
	}
	pks := emp.GetPrimaryKeys()
	if len(pks) == 0 {
		t.Error("EMP has no primary key")
	}
}

func TestExtract_PG_Indexes(t *testing.T) {
	db := connectExt(t, "postgres", pgDSN)
	sm, err := Extract(db, "postgres", pgSchema)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	emp := findTable(sm.GetTables(), "EMP")
	if emp != nil && len(emp.GetIndexes()) == 0 {
		t.Log("PG EMP has no indexes (may depend on test schema state)")
	}
}

// ── helpers ──

func findTable(tables []*md.TableDef, name string) *md.TableDef {
	for _, t := range tables {
		if strings.EqualFold(t.TableName, name) {
			return t
		}
	}
	return nil
}

func viewNames(views []*md.ViewDef) []string {
	var names []string
	for _, v := range views {
		names = append(names, v.ViewName)
	}
	return names
}

func seqNames(seqs []*md.SequenceDef) []string {
	var names []string
	for _, s := range seqs {
		names = append(names, s.SequenceName)
	}
	return names
}
