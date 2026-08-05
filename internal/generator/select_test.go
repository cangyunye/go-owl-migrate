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

func testCompositePKTable(t *testing.T) *md.TableDef {
	t.Helper()
	tbl, _ := md.NewTableDef("scott", "order_item")
	a, _ := md.NewColumnDef("scott", "order_item", "order_id", 1, "NUMBER")
	b, _ := md.NewColumnDef("scott", "order_item", "line_no", 2, "NUMBER")
	tbl.AddColumn(a)
	tbl.AddColumn(b)
	tbl.AddPrimaryKey("pk_order_item", "order_id")
	tbl.AddPrimaryKey("pk_order_item", "line_no")
	return tbl
}

func TestSelectGenerator_CompositePKCursorUsesRowValue(t *testing.T) {
	quote := func(s string) string { return `"` + s + `"` }
	sg := NewSelectGenerator("cursor", 100, t.TempDir(), quote, false, false, false)
	path, err := sg.generateForTable(testCompositePKTable(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	content, _ := os.ReadFile(path)
	s := string(content)
	if !strings.Contains(s, `WHERE ("order_id", "line_no") > ($LAST_ORDER_ID, $LAST_LINE_NO)`) {
		t.Errorf("composite PK cursor must use row-value comparison, got:\n%s", s)
	}
	if !strings.Contains(s, `ORDER BY "order_id", "line_no"`) {
		t.Errorf("cursor pagination must be ordered, got:\n%s", s)
	}
}

func TestSelectGenerator_OffsetPaginationDialect(t *testing.T) {
	quote := func(s string) string { return `"` + s + `"` }
	tests := []struct {
		name       string
		pagination func(pageSize, offset int) string
		want       string
	}{
		{
			name:       "postgres",
			pagination: func(n, off int) string { return "LIMIT 100 OFFSET 0" },
			want:       "LIMIT 100 OFFSET 0",
		},
		{
			name:       "oracle",
			pagination: func(n, off int) string { return "OFFSET 0 ROWS FETCH NEXT 100 ROWS ONLY" },
			want:       "OFFSET 0 ROWS FETCH NEXT 100 ROWS ONLY",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sg := NewSelectGenerator("offset", 100, t.TempDir(), quote, false, false, false).
				WithPagination(tt.pagination)
			path, err := sg.generateForTable(testSelectTable(t))
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			content, _ := os.ReadFile(path)
			s := string(content)
			if !strings.Contains(s, tt.want) {
				t.Errorf("offset pagination must render dialect SQL %q, got:\n%s", tt.want, s)
			}
			if strings.Contains(s, "$PAGE_SIZE") || strings.Contains(s, "$OFFSET") {
				t.Errorf("offset pagination must not leave placeholders, got:\n%s", s)
			}
		})
	}
}
