package cmd

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

func TestSQLite3_E2E_DDLGeneration(t *testing.T) {
	// Create a temporary file-based SQLite3 database
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite3: %v", err)
	}
	defer db.Close()

	// Create test objects
	_, err = db.Exec(`
		CREATE TABLE dept (
			deptno INTEGER PRIMARY KEY,
			dname TEXT NOT NULL,
			loc TEXT
		);
		CREATE TABLE emp (
			empno INTEGER PRIMARY KEY,
			ename TEXT NOT NULL,
			job TEXT,
			sal REAL,
			deptno INTEGER,
			FOREIGN KEY (deptno) REFERENCES dept(deptno)
		);
		CREATE INDEX idx_emp_ename ON emp(ename);
		CREATE INDEX idx_emp_deptno ON emp(deptno);
		CREATE INDEX idx_emp_name_job ON emp(ename, job);
		CREATE VIEW emp_view AS SELECT empno, ename, job FROM emp WHERE sal > 1000;
		CREATE TRIGGER trg_emp_sal BEFORE INSERT ON emp
		FOR EACH ROW BEGIN SELECT 1; END;
	`)
	if err != nil {
		t.Fatalf("create test objects: %v", err)
	}

	// Verify connection
	var count int
	db.QueryRow("SELECT COUNT(*) FROM dept").Scan(&count)

	// Extract metadata
	sm, err := extractor.Extract(db, "sqlite3", "main")
	if err != nil {
		t.Fatalf("extract metadata: %v", err)
	}

	// Verify extracted objects
	tables := sm.GetTables()
	if len(tables) != 2 {
		t.Errorf("expected 2 tables, got %d", len(tables))
	}

	// Get the EMP table to access its indexes
	emp := sm.GetTable("main", "emp")
	if emp == nil {
		t.Fatal("emp table not found")
	}
	idxCount := len(emp.GetIndexes())
	if idxCount < 3 {
		t.Errorf("emp should have at least 3 indexes, got %d", idxCount)
	}

	views := sm.GetViews()
	if len(views) != 1 {
		t.Errorf("expected 1 view, got %d", len(views))
	}

	triggers := sm.GetTriggers("main", "emp")
	if len(triggers) != 1 {
		t.Errorf("expected 1 trigger, got %d", len(triggers))
	}

	// Generate DDL
	d, err := registry.Get("sqlite3")
	if err != nil {
		t.Fatalf("get sqlite3 dialect: %v", err)
	}

	opts := dialect.BuildOptions{
		TargetDialect: "sqlite3",
		SchemaMapping: map[string]string{"main": "main"},
	}
	outputDir := filepath.Join(dir, "ddl")
	gen := generator.NewDDLGenerator(d, opts, outputDir)

	tableFiles, err := gen.GenerateTables(sm)
	if err != nil {
		t.Fatalf("GenerateTables: %v", err)
	}
	if len(tableFiles) != 2 {
		t.Errorf("expected 2 table files, got %d", len(tableFiles))
	}

	idxFiles, err := gen.GenerateIndexes(sm)
	if err != nil {
		t.Fatalf("GenerateIndexes: %v", err)
	}
	// 3 explicit indexes + possibly PK implicit index
	if len(idxFiles) < 3 {
		t.Errorf("expected at least 3 index files, got %d", len(idxFiles))
	}

	viewFiles, err := gen.GenerateViews(sm)
	if err != nil {
		t.Fatalf("GenerateViews: %v", err)
	}
	if len(viewFiles) != 1 {
		t.Errorf("expected 1 view file, got %d", len(viewFiles))
	}

	trgFiles, err := gen.GenerateTriggers(sm)
	if err != nil {
		t.Fatalf("GenerateTriggers: %v", err)
	}
	if len(trgFiles) != 1 {
		t.Errorf("expected 1 trigger file, got %d", len(trgFiles))
	}

	// Verify file contents
	for _, f := range tableFiles {
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if !strings.Contains(string(content), "CREATE TABLE") {
			t.Errorf("table file should contain CREATE TABLE: %s", f)
		}
	}
	for _, f := range idxFiles {
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if !strings.Contains(string(content), "CREATE INDEX") {
			t.Errorf("index file should contain CREATE INDEX: %s", f)
		}
	}
	// Verify composite index
	for _, f := range idxFiles {
		content, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if strings.Contains(string(content), "idx_emp_name_job") {
			if !strings.Contains(string(content), `("ename", "job")`) {
				t.Errorf("composite index should have both columns: %s", content)
			}
		}
	}
}
