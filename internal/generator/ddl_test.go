package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	csvpkg "github.com/cangyunye/go-owl-migrate/internal/metadata/csv"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

// loadTestModel builds a SchemaModel from inline CSV strings.
func loadTestModel(t *testing.T) *md.SchemaModel {
	t.Helper()

	loader := csvpkg.NewLoader()
	loader.AddReader("tables.csv", strings.NewReader(tablesCSV))
	loader.AddReader("columns.csv", strings.NewReader(columnsCSV))
	loader.AddReader("primary_keys.csv", strings.NewReader(pksCSV))
	loader.AddReader("indexes.csv", strings.NewReader(indexesCSV))
	loader.AddReader("views.csv", strings.NewReader(viewsCSV))
	loader.AddReader("sequences.csv", strings.NewReader(sequencesCSV))
	loader.AddReader("synonyms.csv", strings.NewReader(synonymsCSV))
	loader.AddReader("mviews.csv", strings.NewReader(mviewsCSV))
	loader.AddReader("triggers.csv", strings.NewReader(triggersCSV))
	loader.AddReader("functions.csv", strings.NewReader(functionsCSV))
	loader.AddReader("packages.csv", strings.NewReader(packagesCSV))
	loader.AddReader("package_bodies.csv", strings.NewReader(packageBodiesCSV))

	sm, err := loader.Load()
	if err != nil {
		t.Fatalf("load test model: %v", err)
	}
	return sm
}

// dialectCase holds a dialect name and its DDL generator for table-driven tests.
type dialectCase struct {
	name     string
	dialect  dialect.Dialect
	opts     dialect.BuildOptions
	noQuote  bool
}

func newDialectCase(name string, noQuote bool) dialectCase {
	d, err := registry.Get(name)
	if err != nil {
		panic(err)
	}
	return dialectCase{
		name:    name,
		dialect: d,
		opts: dialect.BuildOptions{
			TargetDialect: name,
			SchemaMapping: map[string]string{"SCOTT": "public"},
		},
		noQuote: noQuote,
	}
}

func (dc dialectCase) generator(t *testing.T, outputDir string) *DDLGenerator {
	t.Helper()
	opts := dc.opts
	if dc.noQuote {
		opts.NoQuoteIdentifiers = true
	}
	return NewDDLGenerator(dc.dialect, opts, outputDir)
}

// ── Test: GenerateIndexes should produce files ──

func TestGenerateIndexes_ExpectedCount(t *testing.T) {
	sm := loadTestModel(t)

	for _, dc := range []dialectCase{
		newDialectCase("mysql", false),
		newDialectCase("oracle", false),
		newDialectCase("postgres", false),
	} {
		t.Run(dc.name, func(t *testing.T) {
			dir := t.TempDir()
			gen := dc.generator(t, dir)
			files, err := gen.GenerateIndexes(sm)
			if err != nil {
				t.Fatalf("GenerateIndexes: %v", err)
			}
			// Expect 4 index groups: IDX_EMP_ENAME, IDX_EMP_DEPTNO,
			// IDX_EMP_NAME_JOB (composite), IDX_EMP_UNIQUE_MGR
			want := 4
			if got := len(files); got != want {
				t.Errorf("GenerateIndexes returned %d files, want %d", got, want)
			}
		})
	}
}

func TestGenerateIndexes_ContentMySQL(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("mysql", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateIndexes(sm)
	if err != nil {
		t.Fatalf("GenerateIndexes: %v", err)
	}

	// Build a map: filename → content
	got := make(map[string]string)
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		got[filepath.Base(f)] = string(content)
	}

	tests := []struct {
		file     string // filename pattern (uses original schema)
		wantPart string // expected SQL fragment (uses mapped schema)
	}{
		{"scott.idx_emp_ename.index.sql", "CREATE INDEX `IDX_EMP_ENAME` ON `public`.`EMP` (`ENAME`)"},
		{"scott.idx_emp_deptno.index.sql", "CREATE INDEX `IDX_EMP_DEPTNO` ON `public`.`EMP` (`DEPTNO`)"},
		{"scott.idx_emp_name_job.index.sql", "CREATE INDEX `IDX_EMP_NAME_JOB` ON `public`.`EMP` (`ENAME`, `JOB`)"},
		{"scott.idx_emp_unique_mgr.index.sql", "CREATE UNIQUE INDEX `IDX_EMP_UNIQUE_MGR` ON `public`.`EMP` (`MGR`)"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			content, ok := got[tt.file]
			if !ok {
				t.Errorf("missing file %s", tt.file)
				return
			}
			if !strings.Contains(content, tt.wantPart) {
				t.Errorf("expected content to contain %q\n  got: %s", tt.wantPart, content)
			}
		})
	}
}

func TestGenerateIndexes_ContentOracle(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("oracle", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateIndexes(sm)
	if err != nil {
		t.Fatalf("GenerateIndexes: %v", err)
	}

	got := make(map[string]string)
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		got[filepath.Base(f)] = string(content)
	}

	tests := []struct {
		file     string
		wantPart string
	}{
		{"scott.idx_emp_ename.index.sql", `CREATE INDEX "IDX_EMP_ENAME" ON "PUBLIC"."EMP" ("ENAME")`},
		{"scott.idx_emp_deptno.index.sql", `CREATE INDEX "IDX_EMP_DEPTNO" ON "PUBLIC"."EMP" ("DEPTNO")`},
		{"scott.idx_emp_name_job.index.sql", `CREATE INDEX "IDX_EMP_NAME_JOB" ON "PUBLIC"."EMP" ("ENAME", "JOB")`},
		{"scott.idx_emp_unique_mgr.index.sql", `CREATE UNIQUE INDEX "IDX_EMP_UNIQUE_MGR" ON "PUBLIC"."EMP" ("MGR")`},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			content, ok := got[tt.file]
			if !ok {
				t.Errorf("missing file %s", tt.file)
				return
			}
			if !strings.Contains(content, tt.wantPart) {
				t.Errorf("expected content to contain %q\n  got: %s", tt.wantPart, content)
			}
		})
	}
}

func TestGenerateIndexes_ContentPostgres(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("postgres", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateIndexes(sm)
	if err != nil {
		t.Fatalf("GenerateIndexes: %v", err)
	}

	got := make(map[string]string)
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		got[filepath.Base(f)] = string(content)
	}

	tests := []struct {
		file     string
		wantPart string
	}{
		{"scott.idx_emp_ename.index.sql", `CREATE INDEX "IDX_EMP_ENAME" ON "public"."EMP" ("ENAME")`},
		{"scott.idx_emp_deptno.index.sql", `CREATE INDEX "IDX_EMP_DEPTNO" ON "public"."EMP" ("DEPTNO")`},
		{"scott.idx_emp_name_job.index.sql", `CREATE INDEX "IDX_EMP_NAME_JOB" ON "public"."EMP" ("ENAME", "JOB")`},
		{"scott.idx_emp_unique_mgr.index.sql", `CREATE UNIQUE INDEX "IDX_EMP_UNIQUE_MGR" ON "public"."EMP" ("MGR")`},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			content, ok := got[tt.file]
			if !ok {
				t.Errorf("missing file %s", tt.file)
				return
			}
			if !strings.Contains(content, tt.wantPart) {
				t.Errorf("expected content to contain %q\n  got: %s", tt.wantPart, content)
			}
		})
	}
}

func TestGenerateIndexes_NoQuote(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("postgres", true) // no-quote
	gen := dc.generator(t, dir)

	files, err := gen.GenerateIndexes(sm)
	if err != nil {
		t.Fatalf("GenerateIndexes: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one index file")
	}
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if strings.Contains(string(content), `"`) {
			t.Errorf("no-quote mode should not contain quotes, got: %s", content)
		}
	}
}

// ── Test: GenerateViews should produce files ──

func TestGenerateViews_ExpectedCount(t *testing.T) {
	sm := loadTestModel(t)

	for _, dc := range []dialectCase{
		newDialectCase("mysql", false),
		newDialectCase("oracle", false),
		newDialectCase("postgres", false),
	} {
		t.Run(dc.name, func(t *testing.T) {
			dir := t.TempDir()
			gen := dc.generator(t, dir)
			files, err := gen.GenerateViews(sm)
			if err != nil {
				t.Fatalf("GenerateViews: %v", err)
			}
			// Expect 1 view: EMP_VIEW
			if want := 1; len(files) != want {
				t.Errorf("GenerateViews returned %d files, want %d", len(files), want)
			}
		})
	}
}

func TestGenerateViews_ContentMySQL(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("mysql", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateViews(sm)
	if err != nil {
		t.Fatalf("GenerateViews: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one view file")
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	wantPart := "CREATE VIEW `public`.`EMP_VIEW` AS"
	if !strings.Contains(string(content), wantPart) {
		t.Errorf("expected %q\n  got: %s", wantPart, content)
	}
}

func TestGenerateViews_ContentOracle(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("oracle", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateViews(sm)
	if err != nil {
		t.Fatalf("GenerateViews: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one view file")
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	wantPart := `CREATE VIEW "PUBLIC"."EMP_VIEW" AS`
	if !strings.Contains(string(content), wantPart) {
		t.Errorf("expected %q\n  got: %s", wantPart, content)
	}
}

func TestGenerateViews_ContentPostgres(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("postgres", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateViews(sm)
	if err != nil {
		t.Fatalf("GenerateViews: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one view file")
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	wantPart := `CREATE VIEW "public"."EMP_VIEW" AS`
	if !strings.Contains(string(content), wantPart) {
		t.Errorf("expected %q\n  got: %s", wantPart, content)
	}
}

func TestGenerateViews_NoQuote(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("postgres", true)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateViews(sm)
	if err != nil {
		t.Fatalf("GenerateViews: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one view file")
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	if strings.Contains(string(content), `"`) {
		t.Errorf("no-quote mode should not contain quotes, got: %s", content)
	}
}

// ── Test: GenerateSequences should produce files ──

func TestGenerateSequences_ExpectedCount(t *testing.T) {
	sm := loadTestModel(t)

	for _, dc := range []dialectCase{
		newDialectCase("oracle", false),
		newDialectCase("postgres", false),
	} {
		t.Run(dc.name, func(t *testing.T) {
			dir := t.TempDir()
			gen := dc.generator(t, dir)
			files, err := gen.GenerateSequences(sm, "SCOTT")
			if err != nil {
				t.Fatalf("GenerateSequences: %v", err)
			}
			// Expect 1 sequence: SEQ_EMP_ID
			if want := 1; len(files) != want {
				t.Errorf("GenerateSequences returned %d files, want %d", len(files), want)
			}
		})
	}
}

func TestGenerateSequences_MysqlStub(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("mysql", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateSequences(sm, "SCOTT")
	if err != nil {
		t.Fatalf("GenerateSequences: %v", err)
	}
	// MySQL doesn't support native sequences, expect 0 files
	if want := 0; len(files) != want {
		t.Errorf("MySQL GenerateSequences returned %d files, want %d", len(files), want)
	}
}

func TestGenerateSequences_ContentOracle(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("oracle", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateSequences(sm, "SCOTT")
	if err != nil {
		t.Fatalf("GenerateSequences: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one sequence file")
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	wantPart := `CREATE SEQUENCE "PUBLIC"."SEQ_EMP_ID" START WITH 1`
	if !strings.Contains(string(content), wantPart) {
		t.Errorf("expected %q\n  got: %s", wantPart, content)
	}
}

func TestGenerateSequences_ContentPostgres(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("postgres", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateSequences(sm, "SCOTT")
	if err != nil {
		t.Fatalf("GenerateSequences: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one sequence file")
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	wantPart := `CREATE SEQUENCE "public"."SEQ_EMP_ID" START WITH 1`
	if !strings.Contains(string(content), wantPart) {
		t.Errorf("expected %q\n  got: %s", wantPart, content)
	}
}

func TestGenerateSequences_NoQuote(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("oracle", true)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateSequences(sm, "SCOTT")
	if err != nil {
		t.Fatalf("GenerateSequences: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one sequence file")
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	if strings.Contains(string(content), `"`) {
		t.Errorf("no-quote mode should not contain quotes, got: %s", content)
	}
}

// ── Test: GenerateMViews should produce files ──

func TestGenerateMViews_Oracle(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("oracle", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateMViews(sm)
	if err != nil {
		t.Fatalf("GenerateMViews: %v", err)
	}
	if want := 1; len(files) != want {
		t.Errorf("GenerateMViews returned %d files, want %d", len(files), want)
	}
	if len(files) == 0 {
		return
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	wantPart := `CREATE MATERIALIZED VIEW "PUBLIC"."EMP_MV" AS`
	if !strings.Contains(string(content), wantPart) {
		t.Errorf("expected %q\n  got: %s", wantPart, content)
	}
}

func TestGenerateMViews_Postgres(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("postgres", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateMViews(sm)
	if err != nil {
		t.Fatalf("GenerateMViews: %v", err)
	}
	if want := 1; len(files) != want {
		t.Errorf("GenerateMViews returned %d files, want %d", len(files), want)
	}
	if len(files) == 0 {
		return
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	wantPart := `CREATE MATERIALIZED VIEW "public"."EMP_MV" AS`
	if !strings.Contains(string(content), wantPart) {
		t.Errorf("expected %q\n  got: %s", wantPart, content)
	}
}

func TestGenerateMViews_MysqlStub(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("mysql", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateMViews(sm)
	if err != nil {
		t.Fatalf("GenerateMViews: %v", err)
	}
	if want := 0; len(files) != want {
		t.Errorf("MySQL GenerateMViews returned %d files, want %d", len(files), want)
	}
}

func TestGenerateSynonyms_OracleOnly(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("oracle", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateSynonyms(sm, "SCOTT")
	if err != nil {
		t.Fatalf("GenerateSynonyms: %v", err)
	}
	if want := 1; len(files) != want {
		t.Errorf("GenerateSynonyms returned %d files, want %d", len(files), want)
	}
	if len(files) == 0 {
		return
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	wantPart := `CREATE SYNONYM "PUBLIC"."EMP_SYN" FOR "PUBLIC"."EMP"`
	if !strings.Contains(string(content), wantPart) {
		t.Errorf("expected %q\n  got: %s", wantPart, content)
	}
}

// ── Test: GenerateTriggers ──

func TestGenerateTriggers_Oracle(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("oracle", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateTriggers(sm)
	if err != nil {
		t.Fatalf("GenerateTriggers: %v", err)
	}
	if want := 1; len(files) != want {
		t.Errorf("GenerateTriggers returned %d files, want %d", len(files), want)
	}
}

func TestGenerateTriggers_ContentMySQL(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("mysql", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateTriggers(sm)
	if err != nil {
		t.Fatalf("GenerateTriggers: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one trigger file")
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	wantPart := "CREATE TRIGGER"
	if !strings.Contains(string(content), wantPart) {
		t.Errorf("expected %q\n  got: %s", wantPart, content)
	}
}

// ── Test: GenerateFunctions ──

func TestGenerateFunctions_Oracle(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("oracle", false)
	gen := dc.generator(t, dir)

	files, err := gen.GenerateFunctions(sm, "SCOTT")
	if err != nil {
		t.Fatalf("GenerateFunctions: %v", err)
	}
	if want := 1; len(files) != want {
		t.Errorf("GenerateFunctions returned %d files, want %d", len(files), want)
	}
}

// ── Test: GeneratePackages ──

func TestGeneratePackages_OracleOnly(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("oracle", false)
	gen := dc.generator(t, dir)

	files, err := gen.GeneratePackages(sm, "SCOTT")
	if err != nil {
		t.Fatalf("GeneratePackages: %v", err)
	}
	if want := 1; len(files) != want {
		t.Errorf("GeneratePackages returned %d files, want %d", len(files), want)
	}
}

func TestGeneratePackageBodies_OracleOnly(t *testing.T) {
	sm := loadTestModel(t)
	dir := t.TempDir()
	dc := newDialectCase("oracle", false)
	gen := dc.generator(t, dir)

	files, err := gen.GeneratePackageBodies(sm, "SCOTT")
	if err != nil {
		t.Fatalf("GeneratePackageBodies: %v", err)
	}
	if want := 1; len(files) != want {
		t.Errorf("GeneratePackageBodies returned %d files, want %d", len(files), want)
	}
}

// ── Inline CSV test data ──

const tablesCSV = `TABLE_SCHEMA,TABLE_NAME,TABLE_TYPE,TABLE_COMMENT
SCOTT,EMP,TABLE,Employee table
SCOTT,DEPT,TABLE,Department table
SCOTT,BONUS,TABLE,Bonus table
`

const columnsCSV = `TABLE_SCHEMA,TABLE_NAME,COLUMN_NAME,ORDINAL_POSITION,DATA_TYPE,DATA_LENGTH,DATA_PRECISION,DATA_SCALE,NULLABLE,DEFAULT_VALUE,COLUMN_COMMENT
SCOTT,EMP,EMPNO,1,NUMBER,22,4,0,NO,,Employee number
SCOTT,EMP,ENAME,2,VARCHAR2,10,,,YES,,Employee name
SCOTT,EMP,JOB,3,VARCHAR2,9,,,YES,,Job title
SCOTT,EMP,MGR,4,NUMBER,22,4,0,YES,,Manager ID
SCOTT,EMP,HIREDATE,5,DATE,,,,YES,,Hire date
SCOTT,EMP,SAL,6,NUMBER,22,7,2,YES,,Salary
SCOTT,EMP,COMM,7,NUMBER,22,7,2,YES,,Commission
SCOTT,EMP,DEPTNO,8,NUMBER,22,2,0,NO,,Department number
SCOTT,DEPT,DEPTNO,1,NUMBER,22,2,0,NO,,Department number
SCOTT,DEPT,DNAME,2,VARCHAR2,14,,,YES,,Department name
SCOTT,DEPT,LOC,3,VARCHAR2,13,,,YES,,Location
SCOTT,BONUS,ENAME,1,VARCHAR2,10,,,YES,,Employee name
SCOTT,BONUS,JOB,2,VARCHAR2,9,,,YES,,Job
SCOTT,BONUS,SAL,3,NUMBER,22,7,2,YES,,Salary
SCOTT,BONUS,COMM,4,NUMBER,22,7,2,YES,,Commission
`

const pksCSV = `TABLE_SCHEMA,TABLE_NAME,CONSTRAINT_NAME,COLUMN_NAME,ORDINAL_POSITION
SCOTT,EMP,PK_EMP,EMPNO,1
SCOTT,DEPT,PK_DEPT,DEPTNO,1
`

const indexesCSV = `TABLE_SCHEMA,TABLE_NAME,INDEX_NAME,COLUMN_NAME,ORDINAL_POSITION,INDEX_TYPE,UNIQUENESS
SCOTT,EMP,IDX_EMP_ENAME,ENAME,1,BTREE,NONUNIQUE
SCOTT,EMP,IDX_EMP_DEPTNO,DEPTNO,1,BTREE,NONUNIQUE
SCOTT,EMP,IDX_EMP_NAME_JOB,ENAME,1,BTREE,NONUNIQUE
SCOTT,EMP,IDX_EMP_NAME_JOB,JOB,2,BTREE,NONUNIQUE
SCOTT,EMP,IDX_EMP_UNIQUE_MGR,MGR,1,BTREE,UNIQUE
`

const viewsCSV = `VIEW_SCHEMA,VIEW_NAME,VIEW_DEFINITION,VIEW_COMMENT,IS_UPDATABLE,CHECK_OPTION,OWNER
SCOTT,EMP_VIEW,SELECT e.empno e.ename e.job e.sal d.dname FROM scott.emp e JOIN scott.dept d ON e.deptno = d.deptno WHERE e.sal > 1000,High salary employees,YES,NONE,SCOTT
`

const sequencesCSV = `SEQUENCE_SCHEMA,SEQUENCE_NAME,START_VALUE,INCREMENT_BY,MIN_VALUE,MAX_VALUE,CYCLE,CACHE_SIZE,ORDER_FLAG,CURRENT_VALUE,DATA_TYPE
SCOTT,SEQ_EMP_ID,1,1,1,999999999,NO,20,N,1,NUMBER
`

const synonymsCSV = `SYNONYM_NAME,SYNONYM_SCHEMA,TARGET_SCHEMA,TARGET_NAME,IS_PUBLIC,TARGET_TYPE
EMP_SYN,SCOTT,SCOTT,EMP,NO,TABLE
`

const mviewsCSV = `MVIEW_SCHEMA,MVIEW_NAME,MVIEW_QUERY,REFRESH_METHOD,REFRESH_MODE,REFRESH_INTERVAL,BUILD_MODE,MVIEW_COMMENT
SCOTT,EMP_MV,SELECT e.empno e.ename e.job e.sal d.dname FROM scott.emp e JOIN scott.dept d ON e.deptno = d.deptno,COMPLETE,DEMAND,NULL,IMMEDIATE,Employee materialized view
`

const triggersCSV = `TRIGGER_SCHEMA,TRIGGER_NAME,TABLE_SCHEMA,TABLE_NAME,TRIGGER_TYPE,TRIGGER_EVENT,TRIGGER_BODY,STATUS,FOR_EACH,WHEN_CLAUSE,REFERENCING,DESCRIPTION,LANGUAGE
SCOTT,TRG_EMP_SAL,SCOTT,EMP,BEFORE,INSERT,"BEGIN IF :NEW.SAL < 0 THEN :NEW.SAL := 0; END IF; END;",ENABLED,ROW,,,,Salary validation trigger,PLSQL
`

const functionsCSV = `FUNCTION_SCHEMA,FUNCTION_NAME,FUNCTION_TYPE,RETURN_TYPE,FUNCTION_BODY,LANGUAGE,STATUS,ARGUMENTS,AUTH_ID,DETERMINISTIC,PARALLEL
SCOTT,GET_EMP_COUNT,FUNCTION,NUMBER,"BEGIN RETURN 0; END;",PLSQL,ENABLED,,DEFINER,NO,NO
`

const packagesCSV = `PACKAGE_SCHEMA,PACKAGE_NAME,PACKAGE_SPEC,STATUS,AUTH_ID,DESCRIPTION
SCOTT,PKG_EMP,"PROCEDURE get_emp(p_id IN NUMBER); FUNCTION get_count RETURN NUMBER;",ENABLED,DEFINER,Employee package
`

const packageBodiesCSV = `PACKAGE_SCHEMA,PACKAGE_NAME,PACKAGE_BODY,STATUS
SCOTT,PKG_EMP,"PROCEDURE get_emp(p_id IN NUMBER) IS BEGIN NULL; END; FUNCTION get_count RETURN NUMBER IS BEGIN RETURN 0; END;",ENABLED
`
