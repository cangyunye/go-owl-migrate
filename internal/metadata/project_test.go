package metadata

import "testing"

func sampleTable() *TableDef {
	return &TableDef{
		TableSchema: "OWL_SRC",
		TableName:   "EMP",
		Columns: []*ColumnDef{
			{ColumnName: "EMPNO", DataType: "NUMBER"},
			{ColumnName: "ENAME", DataType: "VARCHAR2"},
			{ColumnName: "SAL", DataType: "NUMBER"},
			{ColumnName: "HIREDATE", DataType: "DATE"},
		},
		PrimaryKeys: []*PrimaryKeyDef{{ColumnName: "EMPNO"}},
		Indexes:     []*IndexDef{{IndexName: "IX_SAL", ColumnName: "SAL"}},
		ForeignKeys: []*ForeignKeyDef{{ConstraintName: "FK_D", ColumnName: "DEPTNO"}},
	}
}

func TestProjectTableOrderAndRename(t *testing.T) {
	projected, err := ProjectTable(sampleTable(),
		[]string{"empno", "sal", "ename"}, // 顺序由配置决定
		map[string]string{"SAL": "salary"})
	if err != nil {
		t.Fatalf("ProjectTable: %v", err)
	}
	got := make([]string, 0, len(projected.Columns))
	for _, c := range projected.Columns {
		got = append(got, c.ColumnName)
	}
	want := []string{"EMPNO", "salary", "ENAME"} // 输出顺序=include 顺序，rename 就位
	if len(got) != len(want) {
		t.Fatalf("columns = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("column order = %v, want %v", got, want)
		}
	}
	if projected.PrimaryKeys[0].ColumnName != "EMPNO" {
		t.Errorf("pk = %s", projected.PrimaryKeys[0].ColumnName)
	}
	if projected.Indexes[0].ColumnName != "salary" {
		t.Errorf("index column must follow rename: %s", projected.Indexes[0].ColumnName)
	}
	// 原表不被改动
	if sampleTable().Columns[2].ColumnName != "SAL" {
		t.Error("input table mutated")
	}
}

func TestProjectTableRenameOnly(t *testing.T) {
	projected, err := ProjectTable(sampleTable(), nil, map[string]string{"hiredate": "hire_ts"})
	if err != nil {
		t.Fatalf("ProjectTable: %v", err)
	}
	if len(projected.Columns) != 4 {
		t.Fatalf("columns = %d", len(projected.Columns))
	}
	if projected.Columns[3].ColumnName != "hire_ts" {
		t.Errorf("last column = %s", projected.Columns[3].ColumnName)
	}
}

func TestProjectTableErrors(t *testing.T) {
	// 未知列
	if _, err := ProjectTable(sampleTable(), []string{"empno", "nope"}, nil); err == nil {
		t.Error("unknown column must error")
	}
	// 丢 PK
	if _, err := ProjectTable(sampleTable(), []string{"ename", "sal"}, nil); err == nil {
		t.Error("dropping PK must error")
	}
}

func TestProjectTablePKRename(t *testing.T) {
	projected, err := ProjectTable(sampleTable(), []string{"empno", "ename"}, map[string]string{"empno": "emp_id"})
	if err != nil {
		t.Fatalf("ProjectTable: %v", err)
	}
	if projected.PrimaryKeys[0].ColumnName != "emp_id" {
		t.Errorf("renamed pk = %s", projected.PrimaryKeys[0].ColumnName)
	}
}
