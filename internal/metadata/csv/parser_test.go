package csv

import (
	"strings"
	"testing"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func TestParseColumns_Basic(t *testing.T) {
	input := `TABLE_SCHEMA,TABLE_NAME,COLUMN_NAME,ORDINAL_POSITION,DATA_TYPE,DATA_LENGTH,DATA_PRECISION,DATA_SCALE,NULLABLE,DEFAULT_VALUE,COLUMN_COMMENT
SCOTT,EMP,EMPNO,1,NUMBER,22,4,0,NO,,
SCOTT,EMP,ENAME,2,VARCHAR2,10,,,YES,,
SCOTT,EMP,HIREDATE,3,DATE,,,,YES,,Hire date`

	cols, err := ParseColumns(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cols) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(cols))
	}

	if cols[0].ColumnName != "EMPNO" {
		t.Errorf("col[0] name = %q", cols[0].ColumnName)
	}
	if cols[0].OrdinalPosition != 1 {
		t.Errorf("col[0] pos = %d", cols[0].OrdinalPosition)
	}
	if cols[0].DataType != "NUMBER" {
		t.Errorf("col[0] type = %q", cols[0].DataType)
	}
	if cols[0].DataPrecision != 4 {
		t.Errorf("col[0] precision = %d", cols[0].DataPrecision)
	}
	if cols[0].Nullable != "NO" {
		t.Errorf("col[0] nullable = %q", cols[0].Nullable)
	}
	if cols[2].ColumnComment != "Hire date" {
		t.Errorf("col[2] comment = %q", cols[2].ColumnComment)
	}
}

func TestParseColumns_MissingRequiredField(t *testing.T) {
	input := `TABLE_SCHEMA,TABLE_NAME,COLUMN_NAME,ORDINAL_POSITION,DATA_TYPE
SCOTT,EMP,,1,NUMBER`

	_, err := ParseColumns(strings.NewReader(input))
	if err == nil {
		t.Error("expected error for missing COLUMN_NAME")
	}
}

func TestParseTables_Basic(t *testing.T) {
	input := `TABLE_SCHEMA,TABLE_NAME,TABLE_TYPE,TABLE_COMMENT
SCOTT,EMP,TABLE,Employee table
HR,DEPT,TABLE,Department table`

	tables, err := ParseTables(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(tables))
	}
	if tables[0].TableName != "EMP" {
		t.Errorf("table[0] = %q", tables[0].TableName)
	}
	if tables[1].TableSchema != "HR" {
		t.Errorf("table[1] schema = %q", tables[1].TableSchema)
	}
}

func TestParseTables_DefaultType(t *testing.T) {
	input := `TABLE_SCHEMA,TABLE_NAME
SCOTT,EMP`

	tables, err := ParseTables(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tables[0].TableType != "TABLE" {
		t.Errorf("default TableType = %q, want TABLE", tables[0].TableType)
	}
}

func TestParsePrimaryKeys(t *testing.T) {
	input := `TABLE_SCHEMA,TABLE_NAME,CONSTRAINT_NAME,COLUMN_NAME,ORDINAL_POSITION
SCOTT,EMP,PK_EMP,EMPNO,1
SCOTT,EMP,PK_EMP,DEPTNO,2`

	pks, err := ParsePrimaryKeys(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pks) != 2 {
		t.Fatalf("expected 2 PK entries, got %d", len(pks))
	}
	if pks[0].ConstraintName != "PK_EMP" {
		t.Errorf("PK constraint = %q", pks[0].ConstraintName)
	}
}

func TestParseIndexes(t *testing.T) {
	input := `TABLE_SCHEMA,TABLE_NAME,INDEX_NAME,COLUMN_NAME,ORDINAL_POSITION,INDEX_TYPE,UNIQUENESS
SCOTT,EMP,IDX_EMP_ENAME,ENAME,1,BTREE,NONUNIQUE`

	indexes, err := ParseIndexes(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(indexes) != 1 {
		t.Fatalf("expected 1 index, got %d", len(indexes))
	}
}

func TestParseForeignKeys(t *testing.T) {
	input := `CONSTRAINT_NAME,TABLE_SCHEMA,TABLE_NAME,COLUMN_NAME,REF_SCHEMA,REF_TABLE,REF_COLUMN,DELETE_RULE
FK_EMP_DEPT,SCOTT,EMP,DEPTNO,SCOTT,DEPT,DEPTNO,CASCADE`

	fks, err := ParseForeignKeys(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fks) != 1 {
		t.Fatalf("expected 1 FK, got %d", len(fks))
	}
	if fks[0].DeleteRule != "CASCADE" {
		t.Errorf("DeleteRule = %q", fks[0].DeleteRule)
	}
}

func TestParse_ExtraColumnsIgnored(t *testing.T) {
	input := `TABLE_SCHEMA,TABLE_NAME,EXTRA_COL1,EXTRA_COL2
SCOTT,EMP,foo,bar`

	tables, err := ParseTables(strings.NewReader(input))
	if err != nil {
		t.Fatalf("extra columns should be ignored: %v", err)
	}
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
}

func TestParse_EmptyLinesAndComments(t *testing.T) {
	input := `TABLE_SCHEMA,TABLE_NAME
# This is a comment
SCOTT,EMP

HR,DEPT
# Another comment`

	tables, err := ParseTables(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(tables))
	}
}

func TestParse_NullMarker(t *testing.T) {
	input := `TABLE_SCHEMA,TABLE_NAME,TABLE_COMMENT
SCOTT,EMP,\N
SCOTT,DEPT,`

	tables, err := ParseTables(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tables[0].TableComment != "" {
		t.Errorf("\\N should become empty: got %q", tables[0].TableComment)
	}
	if tables[1].TableComment != "" {
		t.Errorf("empty field should stay empty: got %q", tables[1].TableComment)
	}
}

func TestParse_Views(t *testing.T) {
	input := `VIEW_SCHEMA,VIEW_NAME,VIEW_DEFINITION
SCOTT,EMP_VIEW,SELECT * FROM SCOTT.EMP WHERE deptno = 10`

	views, err := ParseViews(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if views[0].ViewDefinition != "SELECT * FROM SCOTT.EMP WHERE deptno = 10" {
		t.Errorf("ViewDefinition = %q", views[0].ViewDefinition)
	}
}

func TestLoader_Load(t *testing.T) {
	loader := NewLoader()
	loader.AddReader("tables.csv", strings.NewReader(
		`TABLE_SCHEMA,TABLE_NAME,TABLE_COMMENT
SCOTT,EMP,Employees
SCOTT,DEPT,Departments`))
	loader.AddReader("columns.csv", strings.NewReader(
		`TABLE_SCHEMA,TABLE_NAME,COLUMN_NAME,ORDINAL_POSITION,DATA_TYPE
SCOTT,EMP,EMPNO,1,NUMBER
SCOTT,EMP,ENAME,2,VARCHAR2
SCOTT,DEPT,DEPTNO,1,NUMBER`))

	sm, err := loader.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sm.HasTable("SCOTT", "EMP") {
		t.Error("expected SCOTT.EMP")
	}
	if !sm.HasTable("SCOTT", "DEPT") {
		t.Error("expected SCOTT.DEPT")
	}
	cols := sm.GetColumns("SCOTT", "EMP")
	if len(cols) != 2 {
		t.Fatalf("expected 2 columns for EMP, got %d", len(cols))
	}
}

func TestLoader_Load_MissingTablesCSV(t *testing.T) {
	loader := NewLoader()
	// Only provide columns, no tables
	loader.AddReader("columns.csv", strings.NewReader(
		`TABLE_SCHEMA,TABLE_NAME,COLUMN_NAME,ORDINAL_POSITION,DATA_TYPE
SCOTT,EMP,EMPNO,1,NUMBER`))

	_, err := loader.Load()
	if err == nil {
		t.Error("expected error when tables.csv is missing")
	}
}

func TestLoader_Load_EmptyCSV(t *testing.T) {
	loader := NewLoader()
	loader.AddReader("tables.csv", strings.NewReader(`TABLE_SCHEMA,TABLE_NAME`)) // header only
	loader.AddReader("columns.csv", strings.NewReader(`TABLE_SCHEMA,TABLE_NAME,COLUMN_NAME,ORDINAL_POSITION,DATA_TYPE`))

	sm, err := loader.Load()
	if err != nil {
		t.Fatalf("empty CSV should not error: %v", err)
	}
	if len(sm.GetTables()) != 0 {
		t.Errorf("expected 0 tables, got %d", len(sm.GetTables()))
	}
}

// ── Validator ──

func TestValidator_AllTablesHaveColumns(t *testing.T) {
	sm := md.NewSchemaModel()
	tbl, _ := md.NewTableDef("SCOTT", "EMP")
	col, _ := md.NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	tbl.AddColumn(col)
	sm.AddTable(tbl)

	// Add a table with no columns
	tbl2, _ := md.NewTableDef("SCOTT", "EMPTY_TABLE")
	sm.AddTable(tbl2)

	errs := Validate(sm)
	found := false
	for _, e := range errs {
		if e.Severity == "ERROR" && strings.Contains(e.Error(), "EMPTY_TABLE") && strings.Contains(e.Error(), "no columns") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for table with no columns")
	}
}

func TestValidator_PrimaryKeyColumnExists(t *testing.T) {
	sm := md.NewSchemaModel()
	tbl, _ := md.NewTableDef("SCOTT", "EMP")
	col, _ := md.NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	tbl.AddColumn(col)
	tbl.AddPrimaryKey("PK_EMP", "EMPNO")
	// Reference non-existent column in PK
	tbl.AddPrimaryKey("PK_EMP", "BOGUS_COL")
	sm.AddTable(tbl)

	errs := Validate(sm)
	found := false
	for _, e := range errs {
		if e.Severity == "ERROR" && strings.Contains(e.Error(), "BOGUS_COL") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for PK referencing non-existent column")
	}
}

func TestValidator_IndexColumnExists(t *testing.T) {
	sm := md.NewSchemaModel()
	tbl, _ := md.NewTableDef("SCOTT", "EMP")
	col, _ := md.NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	tbl.AddColumn(col)
	sm.AddTable(tbl)

	idx := &md.IndexDef{
		TableSchema: "SCOTT", TableName: "EMP",
		IndexName: "IDX_BOGUS", ColumnName: "BOGUS_COL", OrdinalPosition: 1,
	}
	sm.AddIndex(idx) // This will add to the table via the model

	errs := Validate(sm)
	found := false
	for _, e := range errs {
		if e.Severity == "ERROR" && strings.Contains(e.Error(), "BOGUS_COL") && strings.Contains(e.Error(), "IDX_BOGUS") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for index referencing non-existent column")
	}
}

func TestValidator_NonPKIdentityWarning(t *testing.T) {
	sm := md.NewSchemaModel()
	tbl, _ := md.NewTableDef("SCOTT", "EMP")
	col, _ := md.NewColumnDef("SCOTT", "EMP", "SAL", 1, "NUMBER")
	col.IsIdentity = "YES"
	tbl.AddColumn(col)
	sm.AddTable(tbl)

	errs := Validate(sm)
	found := false
	for _, e := range errs {
		if e.Severity == "WARNING" && strings.Contains(e.Error(), "SAL") && strings.Contains(e.Error(), "IDENTITY") {
			found = true
		}
	}
	if !found {
		t.Error("expected WARNING for non-PK identity column")
	}
}
