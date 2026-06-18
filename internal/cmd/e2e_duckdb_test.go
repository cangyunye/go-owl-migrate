package cmd

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/marcboeker/go-duckdb"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

func TestDuckDB_E2E_DDLGeneration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE dept (deptno INTEGER PRIMARY KEY, dname VARCHAR, loc VARCHAR);
		CREATE TABLE emp (
			empno INTEGER PRIMARY KEY,
			ename VARCHAR NOT NULL,
			job VARCHAR,
			sal DOUBLE,
			deptno INTEGER REFERENCES dept(deptno)
		);
		CREATE INDEX idx_emp_ename ON emp(ename);
		CREATE INDEX idx_emp_deptno ON emp(deptno);
		CREATE VIEW emp_view AS SELECT empno, ename FROM emp WHERE sal > 1000;
		CREATE SEQUENCE seq_emp_id START 1000;
	`)
	if err != nil {
		t.Fatalf("create test objects: %v", err)
	}

	sm, err := extractor.Extract(db, "duckdb", "main")
	if err != nil {
		t.Fatalf("extract metadata: %v", err)
	}

	d, err := registry.Get("duckdb")
	if err != nil {
		t.Fatalf("get duckdb dialect: %v", err)
	}

	opts := dialect.BuildOptions{
		TargetDialect: "duckdb",
		SchemaMapping: map[string]string{"main": "main"},
	}
	outputDir := filepath.Join(dir, "ddl")
	gen := generator.NewDDLGenerator(d, opts, outputDir)

	tableFiles, _ := gen.GenerateTables(sm)
	if len(tableFiles) != 2 {
		t.Errorf("expected 2 table files, got %d", len(tableFiles))
	}

	idxFiles, _ := gen.GenerateIndexes(sm)
	if len(idxFiles) < 2 {
		t.Errorf("expected at least 2 index files, got %d", len(idxFiles))
	}

	viewFiles, _ := gen.GenerateViews(sm)
	if len(viewFiles) != 1 {
		t.Errorf("expected 1 view file, got %d", len(viewFiles))
	}

	seqFiles, _ := gen.GenerateSequences(sm, "main")
	if len(seqFiles) != 1 {
		t.Errorf("expected 1 sequence file, got %d", len(seqFiles))
	}

	for _, f := range idxFiles {
		content, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if strings.Contains(string(content), "idx_emp_ename") {
			if !strings.Contains(string(content), `"ename"`) {
				t.Errorf("index should reference ename column: %s", content)
			}
		}
	}
}
