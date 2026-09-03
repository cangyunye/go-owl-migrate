package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

// P1-DDL: service.GenerateDDL — CLI 与 serve 共用的单一实现。

func ddlFixture(t *testing.T) *md.SchemaModel {
	t.Helper()
	sm := md.NewSchemaModel()
	add := func(schema, name string, cols [][2]string) {
		tbl, err := md.NewTableDef(schema, name)
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range cols {
			col, err := md.NewColumnDef(schema, name, c[0], i+1, c[1])
			if err != nil {
				t.Fatal(err)
			}
			tbl.AddColumn(col)
		}
		if err := sm.AddTable(tbl); err != nil {
			t.Fatal(err)
		}
	}
	// SCOTT.EMP: NUMBER(4,0) + VARCHAR2(10)
	add("SCOTT", "EMP", [][2]string{{"EMPNO", "NUMBER"}, {"ENAME", "VARCHAR2"}})
	tbl := sm.GetTable("SCOTT", "EMP")
	tbl.Columns[0].DataPrecision = 4
	tbl.Columns[1].DataLength = 10
	// HR.EMP 跨 owner 同名
	add("HR", "EMP", [][2]string{{"ID", "NUMBER"}})
	hr := sm.GetTable("HR", "EMP")
	hr.Columns[0].DataPrecision = 9
	// 两个 owner 各自一条序列，验证按 owner 分组生成
	sm.AddSequence(&md.SequenceDef{SequenceSchema: "SCOTT", SequenceName: "SEQ_EMP", StartValue: 1, IncrementBy: 1, MaxValue: 999, CacheSize: 10})
	sm.AddSequence(&md.SequenceDef{SequenceSchema: "HR", SequenceName: "SEQ_HR", StartValue: 1, IncrementBy: 1, MaxValue: 999, CacheSize: 10})
	return sm
}

func ddlCfg(sourceType string) *config.Config {
	return &config.Config{
		Source: config.DBConfig{Type: sourceType, Schema: "SCOTT"},
		DDL:    config.DDLConfig{TargetDialect: "postgres"},
	}
}

func fileNames(files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = filepath.Base(f)
	}
	return out
}

func readAll(t *testing.T, files []string) string {
	t.Helper()
	var sb strings.Builder
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
	}
	return sb.String()
}

func TestGenerateDDLCrossDialectConvertsTypes(t *testing.T) {
	sm := ddlFixture(t)
	dir := t.TempDir()
	// 源 oracle → 目标 postgres（跨类型族）：NUMBER/VARCHAR2 必须经 IR 转换，
	// 不能原样透传（serve 曾缺失该步导致 DDL 出现 NUMBER/VARCHAR2）。
	files, err := GenerateDDL(sm, ddlCfg("oracle"), nil, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	content := readAll(t, files)
	for _, want := range []string{"CREATE TABLE", "SMALLINT", "VARCHAR(10)", "CREATE SEQUENCE"} {
		if !strings.Contains(content, want) {
			t.Errorf("generated DDL missing %q:\n%s", want, content)
		}
	}
}

func TestGenerateDDLSameFamilyQualifies(t *testing.T) {
	sm := ddlFixture(t)
	dir := t.TempDir()
	// 源 postgres → 目标 postgres：不跨族，只补长度/精度限定，类型原样。
	files, err := GenerateDDL(sm, ddlCfg("postgres"), nil, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	content := readAll(t, files)
	if !strings.Contains(content, "VARCHAR2(10)") {
		t.Errorf("same-family DDL should qualify VARCHAR2(10):\n%s", content)
	}
}

func TestGenerateDDLPerOwnerAndInclude(t *testing.T) {
	sm := ddlFixture(t)
	dir := t.TempDir()
	files, err := GenerateDDL(sm, ddlCfg("oracle"), nil, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	names := fileNames(files)
	want := map[string]bool{
		"scott.emp.table.sql":        true,
		"hr.emp.table.sql":           true,
		"scott.seq_emp.sequence.sql": true,
		"hr.seq_hr.sequence.sql":     true,
	}
	if len(names) != len(want) {
		t.Fatalf("files = %v, want %d files", names, len(want))
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected file %s (have %v)", n, names)
		}
	}

	// include 只取 HR.EMP：表及其 owner 的序列
	dir2 := t.TempDir()
	files2, err := GenerateDDL(sm, ddlCfg("oracle"), []string{"HR.EMP"}, nil, dir2)
	if err != nil {
		t.Fatal(err)
	}
	names2 := fileNames(files2)
	if len(names2) != 2 {
		t.Fatalf("include HR.EMP files = %v, want 2 (table + sequence)", names2)
	}
}

func TestGenerateDDLNoQuote(t *testing.T) {
	sm := ddlFixture(t)
	dir := t.TempDir()
	files, err := GenerateDDL(sm, ddlCfg("oracle"), []string{"SCOTT.EMP"}, boolPtr(true), dir)
	if err != nil {
		t.Fatal(err)
	}
	content := readAll(t, files)
	if strings.Contains(content, `"`) {
		t.Errorf("no_quote_identifiers should omit quotes:\n%s", content)
	}
}

func boolPtr(b bool) *bool { return &b }

func mustCol(t *testing.T, schema, table, name string, pos int, dt string) *md.ColumnDef {
	t.Helper()
	col, err := md.NewColumnDef(schema, table, name, pos, dt)
	if err != nil {
		t.Fatalf("new column %s: %v", name, err)
	}
	return col
}

// ── 从 cmd/tableddl_test.go 随迁移一同搬入的类型边界测试 ──

func TestQualifyColumnType(t *testing.T) {
	tests := []struct {
		name string
		col  func() *md.ColumnDef
		want string
	}{
		{"varchar with length", func() *md.ColumnDef {
			c := mustCol(t, "s", "t", "c", 1, "VARCHAR2")
			c.DataLength = 10
			return c
		}, "VARCHAR2(10)"},
		{"number with precision only", func() *md.ColumnDef {
			c := mustCol(t, "s", "t", "c", 1, "NUMBER")
			c.DataPrecision = 4
			return c
		}, "NUMBER(4)"},
		{"number with precision and scale", func() *md.ColumnDef {
			c := mustCol(t, "s", "t", "c", 1, "DECIMAL")
			c.DataPrecision, c.DataScale = 7, 2
			return c
		}, "DECIMAL(7,2)"},
		{"type already qualified passes through", func() *md.ColumnDef {
			return mustCol(t, "s", "t", "c", 1, "VARCHAR(100)")
		}, "VARCHAR(100)"},
		{"bare type without length stays bare", func() *md.ColumnDef {
			return mustCol(t, "s", "t", "c", 1, "BLOB")
		}, "BLOB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := qualifyColumnType(tt.col(), dialect.BuildOptions{}); got != tt.want {
				t.Errorf("qualifyColumnType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConvertSchemaModelForDDL(t *testing.T) {
	// Build a live-extraction style model: bare data_type with length/precision
	// in separate fields (what information_schema returns).
	tbl, _ := md.NewTableDef("test", "t")
	name, _ := md.NewColumnDef("test", "t", "name", 1, "VARCHAR")
	name.DataLength = 50
	tbl.AddColumn(name)
	sal, _ := md.NewColumnDef("test", "t", "sal", 2, "DECIMAL")
	sal.DataPrecision, sal.DataScale = 12, 2
	tbl.AddColumn(sal)
	txt, _ := md.NewColumnDef("test", "t", "note", 3, "TEXT")
	tbl.AddColumn(txt)
	sm := md.NewSchemaModel()
	_ = sm.AddTable(tbl)

	t.Run("same dialect qualifies", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Source.Type = "oceanbase-mysql"
		cfg.DDL.TargetDialect = "oceanbase-mysql"
		tgt, _ := registry.Get("oceanbase-mysql")
		out := ConvertSchemaModelForDDL(sm, cfg, tgt, dialect.BuildOptions{})
		if out == sm {
			t.Fatal("expected a converted model")
		}
		got := out.GetTables()[0].GetColumns()
		if got[0].DataType != "VARCHAR(50)" {
			t.Errorf("name type = %q, want VARCHAR(50)", got[0].DataType)
		}
		if got[1].DataType != "DECIMAL(12,2)" {
			t.Errorf("sal type = %q, want DECIMAL(12,2)", got[1].DataType)
		}
		if got[2].DataType != "TEXT" {
			t.Errorf("note type = %q, want TEXT", got[2].DataType)
		}
	})

	t.Run("cross dialect converts via LogicalType IR", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Source.Type = "mysql"
		cfg.DDL.TargetDialect = "postgres"
		tgt, _ := registry.Get("postgres")
		out := ConvertSchemaModelForDDL(sm, cfg, tgt, dialect.BuildOptions{})
		if out == sm {
			t.Fatal("expected a converted model")
		}
		got := out.GetTables()[0].GetColumns()
		if got[0].DataType != "VARCHAR(50)" {
			t.Errorf("name type = %q, want VARCHAR(50)", got[0].DataType)
		}
		if got[1].DataType != "NUMERIC(12,2)" {
			t.Errorf("sal type = %q, want NUMERIC(12,2)", got[1].DataType)
		}
	})

	t.Run("no source dialect leaves model untouched", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.DDL.TargetDialect = "postgres"
		tgt, _ := registry.Get("postgres")
		if out := ConvertSchemaModelForDDL(sm, cfg, tgt, dialect.BuildOptions{}); out != sm {
			t.Error("model should be returned unchanged when no source dialect is known")
		}
	})
}
