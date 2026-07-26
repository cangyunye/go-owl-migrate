package generator

import (
	"os"
	"strings"
	"testing"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func testSelectTable(t *testing.T) *md.TableDef {
	t.Helper()
	tbl, _ := md.NewTableDef("scott", "emp")
	empno, _ := md.NewColumnDef("scott", "emp", "empno", 1, "NUMBER")
	ename, _ := md.NewColumnDef("scott", "emp", "ename", 2, "VARCHAR2")
	tbl.AddColumn(empno)
	tbl.AddColumn(ename)
	tbl.AddPrimaryKey("pk_emp", "empno")
	return tbl
}

func TestSelectGenerator_ExtraColumns(t *testing.T) {
	quote := func(s string) string { return `"` + s + `"` }

	t.Run("row number", func(t *testing.T) {
		sg := NewSelectGenerator("cursor", 100, t.TempDir(), quote, true, false, false)
		path, err := sg.generateForTable(testSelectTable(t))
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		content, _ := os.ReadFile(path)
		if !strings.Contains(string(content), `ROW_NUMBER() OVER (ORDER BY "empno") AS rn`) {
			t.Errorf("missing row number column:\n%s", content)
		}
	})
	t.Run("export columns", func(t *testing.T) {
		sg := NewSelectGenerator("cursor", 100, t.TempDir(), quote, false, true, false)
		path, _ := sg.generateForTable(testSelectTable(t))
		content, _ := os.ReadFile(path)
		if !strings.Contains(string(content), `'scott.emp' AS __export_source`) {
			t.Errorf("missing export source column:\n%s", content)
		}
	})
	t.Run("neither", func(t *testing.T) {
		sg := NewSelectGenerator("cursor", 100, t.TempDir(), quote, false, false, false)
		path, _ := sg.generateForTable(testSelectTable(t))
		content, _ := os.ReadFile(path)
		if strings.Contains(string(content), "ROW_NUMBER()") || strings.Contains(string(content), "__export_source") {
			t.Errorf("unexpected extra columns:\n%s", content)
		}
	})
}

func TestSelectGenerator_OracleRowNum(t *testing.T) {
	quote := func(s string) string { return `"` + s + `"` }
	sg := NewSelectGenerator("cursor", 100, t.TempDir(), quote, true, false, true)
	path, err := sg.generateForTable(testSelectTable(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "ROWNUM AS rn") {
		t.Errorf("expected ROWNUM for Oracle, got:\n%s", content)
	}
	if strings.Contains(string(content), "ROW_NUMBER()") {
		t.Errorf("Oracle should use ROWNUM not ROW_NUMBER(), got:\n%s", content)
	}
}
