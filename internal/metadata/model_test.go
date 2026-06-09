package metadata

import "testing"

func TestNewColumnDef_RequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		schema  string
		table   string
		col     string
		pos     int
		dtype   string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "all required fields present",
			schema:  "SCOTT",
			table:   "EMP",
			col:     "EMPNO",
			pos:     1,
			dtype:   "NUMBER",
			wantErr: false,
		},
		{
			name:    "missing TABLE_SCHEMA",
			schema:  "",
			table:   "EMP",
			col:     "EMPNO",
			pos:     1,
			dtype:   "NUMBER",
			wantErr: true,
			errMsg:  "TABLE_SCHEMA is required",
		},
		{
			name:    "missing TABLE_NAME",
			schema:  "SCOTT",
			table:   "",
			col:     "EMPNO",
			pos:     1,
			dtype:   "NUMBER",
			wantErr: true,
			errMsg:  "TABLE_NAME is required",
		},
		{
			name:    "missing COLUMN_NAME",
			schema:  "SCOTT",
			table:   "EMP",
			col:     "",
			pos:     1,
			dtype:   "NUMBER",
			wantErr: true,
			errMsg:  "COLUMN_NAME is required",
		},
		{
			name:    "missing DATA_TYPE",
			schema:  "SCOTT",
			table:   "EMP",
			col:     "EMPNO",
			pos:     1,
			dtype:   "",
			wantErr: true,
			errMsg:  "DATA_TYPE is required",
		},
		{
			name:    "ORDINAL_POSITION is zero",
			schema:  "SCOTT",
			table:   "EMP",
			col:     "EMPNO",
			pos:     0,
			dtype:   "NUMBER",
			wantErr: true,
			errMsg:  "ORDINAL_POSITION must be >= 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			col, err := NewColumnDef(tt.schema, tt.table, tt.col, tt.pos, tt.dtype)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errMsg)
					return
				}
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if col.TableSchema != tt.schema {
				t.Errorf("TableSchema = %q, want %q", col.TableSchema, tt.schema)
			}
			if col.TableName != tt.table {
				t.Errorf("TableName = %q, want %q", col.TableName, tt.table)
			}
			if col.ColumnName != tt.col {
				t.Errorf("ColumnName = %q, want %q", col.ColumnName, tt.col)
			}
			if col.OrdinalPosition != tt.pos {
				t.Errorf("OrdinalPosition = %d, want %d", col.OrdinalPosition, tt.pos)
			}
			if col.DataType != tt.dtype {
				t.Errorf("DataType = %q, want %q", col.DataType, tt.dtype)
			}
		})
	}
}

func TestNewColumnDef_Defaults(t *testing.T) {
	col, err := NewColumnDef("SCOTT", "EMP", "ENAME", 2, "VARCHAR2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if col.Nullable != "YES" {
		t.Errorf("Nullable default = %q, want YES", col.Nullable)
	}
	if col.IsIdentity != "NO" {
		t.Errorf("IsIdentity default = %q, want NO", col.IsIdentity)
	}
	if col.DataLength != 0 {
		t.Errorf("DataLength default = %d, want 0", col.DataLength)
	}
}

// ── TableDef ──

func TestNewTableDef_RequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		schema  string
		table   string
		wantErr bool
		errMsg  string
	}{
		{name: "all required fields", schema: "SCOTT", table: "EMP", wantErr: false},
		{name: "missing TABLE_SCHEMA", schema: "", table: "EMP", wantErr: true, errMsg: "TABLE_SCHEMA is required"},
		{name: "missing TABLE_NAME", schema: "SCOTT", table: "", wantErr: true, errMsg: "TABLE_NAME is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tbl, err := NewTableDef(tt.schema, tt.table)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errMsg)
					return
				}
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tbl.TableSchema != tt.schema {
				t.Errorf("TableSchema = %q, want %q", tbl.TableSchema, tt.schema)
			}
			if tbl.TableName != tt.table {
				t.Errorf("TableName = %q, want %q", tbl.TableName, tt.table)
			}
		})
	}
}

func TestTableDef_AddAndGetColumn(t *testing.T) {
	tbl, _ := NewTableDef("SCOTT", "EMP")

	col1, _ := NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	col2, _ := NewColumnDef("SCOTT", "EMP", "ENAME", 2, "VARCHAR2")

	tbl.AddColumn(col1)
	tbl.AddColumn(col2)

	if len(tbl.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(tbl.Columns))
	}

	got := tbl.GetColumn("EMPNO")
	if got == nil {
		t.Fatal("expected to find EMPNO column")
	}
	if got.ColumnName != "EMPNO" {
		t.Errorf("ColumnName = %q, want EMPNO", got.ColumnName)
	}

	got2 := tbl.GetColumn("ename") // case-insensitive default
	if got2 == nil {
		t.Fatal("expected to find ENAME column (case-insensitive)")
	}

	gotNil := tbl.GetColumn("SAL")
	if gotNil != nil {
		t.Errorf("expected nil for non-existent column, got %v", gotNil)
	}
}

func TestTableDef_GetColumns_OrderedByPosition(t *testing.T) {
	tbl, _ := NewTableDef("SCOTT", "EMP")
	col1, _ := NewColumnDef("SCOTT", "EMP", "HIREDATE", 10, "DATE")
	col2, _ := NewColumnDef("SCOTT", "EMP", "ENAME", 2, "VARCHAR2")
	col3, _ := NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")

	tbl.AddColumn(col1)
	tbl.AddColumn(col2)
	tbl.AddColumn(col3)

	cols := tbl.GetColumns()
	if len(cols) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(cols))
	}
	if cols[0].ColumnName != "EMPNO" || cols[1].ColumnName != "ENAME" || cols[2].ColumnName != "HIREDATE" {
		t.Errorf("columns not ordered by OrdinalPosition: got %q, %q, %q",
			cols[0].ColumnName, cols[1].ColumnName, cols[2].ColumnName)
	}
}

func TestTableDef_DuplicateColumn(t *testing.T) {
	tbl, _ := NewTableDef("SCOTT", "EMP")
	col1, _ := NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	col2, _ := NewColumnDef("SCOTT", "EMP", "EMPNO", 2, "NUMBER")

	if err := tbl.AddColumn(col1); err != nil {
		t.Fatalf("unexpected error adding first column: %v", err)
	}
	if err := tbl.AddColumn(col2); err == nil {
		t.Error("expected error for duplicate column name")
	}
}

func TestTableDef_PrimaryKeys(t *testing.T) {
	tbl, _ := NewTableDef("SCOTT", "EMP")
	tbl.AddPrimaryKey("PK_EMP", "EMPNO")
	tbl.AddPrimaryKey("PK_EMP", "DEPTNO")

	pks := tbl.GetPrimaryKeys()
	if len(pks) != 2 {
		t.Fatalf("expected 2 primary key columns, got %d", len(pks))
	}
	if pks[0].ColumnName != "EMPNO" || pks[1].ColumnName != "DEPTNO" {
		t.Errorf("pk columns wrong: %q, %q", pks[0].ColumnName, pks[1].ColumnName)
	}
	if pks[0].ConstraintName != "PK_EMP" {
		t.Errorf("constraint name = %q, want PK_EMP", pks[0].ConstraintName)
	}
}

func TestTableDef_Indexes(t *testing.T) {
	tbl, _ := NewTableDef("SCOTT", "EMP")
	idx := IndexDef{
		IndexName: "IDX_EMP_ENAME", TableSchema: "SCOTT", TableName: "EMP",
		ColumnName: "ENAME", OrdinalPosition: 1, IndexType: "BTREE", Uniqueness: "NONUNIQUE",
	}
	tbl.AddIndex(&idx)

	indexes := tbl.GetIndexes()
	if len(indexes) != 1 {
		t.Fatalf("expected 1 index, got %d", len(indexes))
	}
	if indexes[0].IndexName != "IDX_EMP_ENAME" {
		t.Errorf("index name = %q", indexes[0].IndexName)
	}
}

// ── SchemaModel ──

func TestSchemaModel_AddAndGetTable(t *testing.T) {
	sm := NewSchemaModel()
	tbl, _ := NewTableDef("SCOTT", "EMP")
	col, _ := NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	tbl.AddColumn(col)

	if err := sm.AddTable(tbl); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := sm.GetTable("SCOTT", "EMP")
	if got == nil {
		t.Fatal("expected to find SCOTT.EMP")
	}
	if got.TableName != "EMP" {
		t.Errorf("TableName = %q, want EMP", got.TableName)
	}
	if len(got.Columns) != 1 {
		t.Errorf("expected 1 column, got %d", len(got.Columns))
	}
}

func TestSchemaModel_HasTable(t *testing.T) {
	sm := NewSchemaModel()
	tbl, _ := NewTableDef("SCOTT", "EMP")
	sm.AddTable(tbl)

	if !sm.HasTable("SCOTT", "EMP") {
		t.Error("expected HasTable true")
	}
	if sm.HasTable("SCOTT", "DEPT") {
		t.Error("expected HasTable false")
	}
}

func TestSchemaModel_AddTable_Duplicate(t *testing.T) {
	sm := NewSchemaModel()
	t1, _ := NewTableDef("SCOTT", "EMP")
	t2, _ := NewTableDef("SCOTT", "EMP")
	sm.AddTable(t1)
	if err := sm.AddTable(t2); err == nil {
		t.Error("expected error for duplicate table")
	}
}

func TestSchemaModel_GetColumns(t *testing.T) {
	sm := NewSchemaModel()
	tbl, _ := NewTableDef("SCOTT", "EMP")
	c1, _ := NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	c2, _ := NewColumnDef("SCOTT", "EMP", "ENAME", 2, "VARCHAR2")
	tbl.AddColumn(c1)
	tbl.AddColumn(c2)
	sm.AddTable(tbl)

	cols := sm.GetColumns("SCOTT", "EMP")
	if len(cols) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(cols))
	}
	if cols[0].ColumnName != "EMPNO" || cols[1].ColumnName != "ENAME" {
		t.Errorf("wrong column order")
	}

	// Non-existent table
	if cols := sm.GetColumns("SCOTT", "BOGUS"); cols != nil {
		t.Error("expected nil for non-existent table")
	}
}

func TestSchemaModel_GetTables(t *testing.T) {
	sm := NewSchemaModel()
	t1, _ := NewTableDef("SCOTT", "EMP")
	t2, _ := NewTableDef("HR", "DEPT")
	sm.AddTable(t1)
	sm.AddTable(t2)

	tables := sm.GetTables()
	if len(tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(tables))
	}
	// Sorted: HR.DEPT then SCOTT.EMP
	if tables[0].TableSchema != "HR" || tables[1].TableSchema != "SCOTT" {
		t.Errorf("tables not sorted: %s.%s, %s.%s",
			tables[0].TableSchema, tables[0].TableName,
			tables[1].TableSchema, tables[1].TableName)
	}
}

// ── SchemaModel.Validate ──

func TestSchemaModel_Validate_Success(t *testing.T) {
	sm := NewSchemaModel()
	tbl, _ := NewTableDef("SCOTT", "EMP")
	c1, _ := NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	c2, _ := NewColumnDef("SCOTT", "EMP", "DEPTNO", 2, "NUMBER")
	tbl.AddColumn(c1)
	tbl.AddColumn(c2)
	tbl.AddPrimaryKey("PK_EMP", "EMPNO")
	sm.AddTable(tbl)

	dept, _ := NewTableDef("SCOTT", "DEPT")
	dc1, _ := NewColumnDef("SCOTT", "DEPT", "DEPTNO", 1, "NUMBER")
	dept.AddColumn(dc1)
	dept.AddPrimaryKey("PK_DEPT", "DEPTNO")
	sm.AddTable(dept)

	fk := &ForeignKeyDef{
		ConstraintName: "FK_EMP_DEPT",
		TableSchema:    "SCOTT", TableName: "EMP", ColumnName: "DEPTNO",
		RefSchema: "SCOTT", RefTable: "DEPT", RefColumn: "DEPTNO",
	}
	sm.AddForeignKey(fk)

	errs := sm.Validate()
	if len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("unexpected validation error: %v", e)
		}
	}
}

func TestSchemaModel_Validate_ColumnRefsMissingTable(t *testing.T) {
	sm := NewSchemaModel()
	// Add EMP with columns, but no table for DEPT
	tbl, _ := NewTableDef("SCOTT", "EMP")
	c1, _ := NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	tbl.AddColumn(c1)
	sm.AddTable(tbl)

	// Add FK referencing non-existent DEPT
	fk := &ForeignKeyDef{
		ConstraintName: "FK_EMP_DEPT",
		TableSchema:    "SCOTT", TableName: "EMP", ColumnName: "DEPTNO",
		RefSchema: "SCOTT", RefTable: "DEPT", RefColumn: "DEPTNO",
	}
	sm.AddForeignKey(fk)

	errs := sm.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation errors")
	}
	found := false
	for _, e := range errs {
		if contains(e.Error(), "SCOTT.DEPT") {
			found = true
		}
	}
	if !found {
		t.Error("expected an error about missing table SCOTT.DEPT")
	}
}

func TestSchemaModel_Validate_FKRefsMissingColumn(t *testing.T) {
	sm := NewSchemaModel()
	dept, _ := NewTableDef("SCOTT", "DEPT")
	dc1, _ := NewColumnDef("SCOTT", "DEPT", "DEPTNO", 1, "NUMBER")
	dept.AddColumn(dc1)
	sm.AddTable(dept)

	emp, _ := NewTableDef("SCOTT", "EMP")
	c1, _ := NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	emp.AddColumn(c1)
	sm.AddTable(emp)

	// FK references DEPT.MGRNO which does not exist
	fk := &ForeignKeyDef{
		ConstraintName: "FK_EMP_DEPT",
		TableSchema:    "SCOTT", TableName: "EMP", ColumnName: "MGRNO",
		RefSchema: "SCOTT", RefTable: "DEPT", RefColumn: "MGRNO",
	}
	sm.AddForeignKey(fk)

	errs := sm.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation errors for missing FK column")
	}
}

func TestSchemaModel_Validate_TriggerRefsMissingTable(t *testing.T) {
	sm := NewSchemaModel()
	trg := &TriggerDef{
		TriggerSchema: "SCOTT", TriggerName: "TRG_EMP",
		TableSchema: "SCOTT", TableName: "BOGUS",
		TriggerType: "BEFORE", TriggerEvent: "INSERT", TriggerBody: "BEGIN NULL; END;",
	}
	sm.AddTrigger(trg)

	errs := sm.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error for trigger referencing missing table")
	}
}

func TestSchemaModel_Validate_MultipleErrors(t *testing.T) {
	sm := NewSchemaModel()
	// Only EMP table, but FK to DEPT and trigger on BOGUS
	tbl, _ := NewTableDef("SCOTT", "EMP")
	c1, _ := NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	tbl.AddColumn(c1)
	sm.AddTable(tbl)

	fk := &ForeignKeyDef{
		ConstraintName: "FK_EMP_DEPT",
		TableSchema:    "SCOTT", TableName: "EMP", ColumnName: "DEPTNO",
		RefSchema: "SCOTT", RefTable: "DEPT", RefColumn: "DEPTNO",
	}
	sm.AddForeignKey(fk)

	trg := &TriggerDef{
		TriggerSchema: "SCOTT", TriggerName: "TRG_BOGUS",
		TableSchema: "SCOTT", TableName: "BOGUS",
		TriggerType: "BEFORE", TriggerEvent: "INSERT", TriggerBody: "BEGIN NULL; END;",
	}
	sm.AddTrigger(trg)

	errs := sm.Validate()
	if len(errs) < 2 {
		t.Fatalf("expected at least 2 errors, got %d", len(errs))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
