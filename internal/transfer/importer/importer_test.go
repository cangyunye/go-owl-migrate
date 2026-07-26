package importer

import (
	"testing"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func mustTable(t *testing.T, cols map[string]string) *md.TableDef {
	t.Helper()
	tbl, err := md.NewTableDef("SCOTT", "EMP")
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	i := 1
	for name, typ := range cols {
		col, err := md.NewColumnDef("SCOTT", "EMP", name, i, typ)
		if err != nil {
			t.Fatalf("new column: %v", err)
		}
		if err := tbl.AddColumn(col); err != nil {
			t.Fatalf("add column: %v", err)
		}
		i++
	}
	return tbl
}

func TestUnit_GetEncoding(t *testing.T) {
	tests := []struct {
		name    string
		wantNil bool
	}{
		{"GBK", false},
		{"gb2312", false},
		{"GB18030", false},
		{"LATIN1", false},
		{"ISO-8859-1", false},
		{"LATIN9", false},
		{"ISO-8859-15", false},
		{"WINDOWS-1252", false},
		{"", true},
		{"UTF-8", true},
		{"unknown", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getEncoding(tt.name)
			if (got == nil) != tt.wantNil {
				t.Errorf("getEncoding(%q) nil=%v, want nil=%v", tt.name, got == nil, tt.wantNil)
			}
		})
	}
}

func TestUnit_TransformValue(t *testing.T) {
	tests := []struct {
		name  string
		cfg   Config
		input string
		want  any
	}{
		{"datetime14", Config{DateTimeFormat: "yyyyMMddHHmmss"}, "19801217000000", "1980-12-17 00:00:00"},
		{"datetime14 non-digit passthrough", Config{DateTimeFormat: "yyyyMMddHHmmss"}, "1980121700000X", "1980121700000X"},
		{"datetime14 wrong length", Config{DateTimeFormat: "yyyyMMddHHmmss"}, "19801217", "19801217"},
		{"datetime8", Config{DateTimeFormat: "yyyyMMdd"}, "19801217", "1980-12-17"},
		{"trim", Config{TrimStrings: true}, "  foo  ", "foo"},
		{"trim then datetime", Config{TrimStrings: true, DateTimeFormat: "yyyyMMddHHmmss"}, " 19801217000000 ", "1980-12-17 00:00:00"},
		{"passthrough", Config{}, "hello", "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := New(nil, tt.cfg)
			if got := imp.transformValue(tt.input); got != tt.want {
				t.Errorf("transformValue(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestUnit_TransformValue_Encoding(t *testing.T) {
	imp := New(nil, Config{SourceEncoding: "LATIN1"})
	if imp.dec == nil {
		t.Fatal("expected decoder for LATIN1")
	}
	if got := imp.transformValue("\xe9"); got != "é" {
		t.Errorf("LATIN1 decode = %q, want %q", got, "é")
	}
}

func TestUnit_ConvertCompactDatetime(t *testing.T) {
	tests := []struct {
		format string
		in     string
		want   string
		ok     bool
	}{
		{"yyyyMMddHHmmss", "19801217000000", "1980-12-17 00:00:00", true},
		{"yyyyMMddHHmmss", "19801217", "", false},
		{"yyyyMMdd", "19801217", "1980-12-17", true},
		{"yyyyMMdd", "19801217000000", "", false},
		{"yyyyMMddHHmmss", "1980121700000X", "", false},
		{"unknownFormat", "19801217000000", "", false},
	}
	for _, tt := range tests {
		got, ok := convertCompactDatetime(tt.format, tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("convertCompactDatetime(%q,%q) = (%q,%v), want (%q,%v)", tt.format, tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestUnit_TransformValue_DatetimeFallback(t *testing.T) {
	tests := []struct {
		name  string
		cfg   Config
		input string
		want  any
	}{
		{"primary precedence", Config{DateTimeFormat: "yyyyMMddHHmmss", DateTimeFormatFallback: []string{"yyyyMMdd"}}, "19801217000000", "1980-12-17 00:00:00"},
		{"fallback when primary misses", Config{DateTimeFormat: "yyyyMMddHHmmss", DateTimeFormatFallback: []string{"yyyyMMdd"}}, "19801217", "1980-12-17"},
		{"fallback without primary", Config{DateTimeFormatFallback: []string{"yyyyMMdd"}}, "19801217", "1980-12-17"},
		{"second fallback matches", Config{DateTimeFormatFallback: []string{"yyyyMMddHHmmss", "yyyyMMdd"}}, "19801217", "1980-12-17"},
		{"no match passthrough", Config{DateTimeFormat: "yyyyMMddHHmmss"}, "19801217", "19801217"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := New(nil, tt.cfg)
			if got := imp.transformValue(tt.input); got != tt.want {
				t.Errorf("transformValue(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestUnit_NumericBooleanValue(t *testing.T) {
	tests := []struct {
		in   any
		want any
	}{
		{"true", "1"},
		{"TRUE", "1"},
		{"yes", "1"},
		{"y", "1"},
		{"t", "1"},
		{"false", "0"},
		{"No", "0"},
		{"n", "0"},
		{"f", "0"},
		{"maybe", "maybe"},
		{"", ""},
		{5, 5},
	}
	for _, tt := range tests {
		if got := numericBooleanValue(tt.in); got != tt.want {
			t.Errorf("numericBooleanValue(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestUnit_NeedsNumericBoolean(t *testing.T) {
	tbl := mustTable(t, map[string]string{"ACTIVE": "BOOLEAN", "NAME": "VARCHAR"})
	tests := []struct {
		name   string
		target string
		col    string
		want   bool
	}{
		{"mysql boolean", "mysql", "ACTIVE", true},
		{"oracle boolean", "oracle", "ACTIVE", true},
		{"postgres boolean ignored", "postgres", "ACTIVE", false},
		{"mysql non-boolean", "mysql", "NAME", false},
		{"mysql missing col", "mysql", "NOPE", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := New(nil, Config{TargetDBType: tt.target})
			if got := imp.needsNumericBoolean(tbl, tt.col); got != tt.want {
				t.Errorf("needsNumericBoolean(%q) = %v, want %v", tt.col, got, tt.want)
			}
		})
	}
}

func TestUnit_IsBinaryColumn(t *testing.T) {
	tbl := mustTable(t, map[string]string{"DATA": "BLOB", "RAW_COL": "RAW", "NAME": "VARCHAR"})
	imp := New(nil, Config{})
	for _, col := range []string{"DATA", "RAW_COL"} {
		if !imp.isBinaryColumn(tbl, col) {
			t.Errorf("isBinaryColumn(%q) = false, want true", col)
		}
	}
	if imp.isBinaryColumn(tbl, "NAME") {
		t.Error("isBinaryColumn(NAME) = true, want false")
	}
}

func TestUnit_QuoteIdent(t *testing.T) {
	tests := []struct {
		name  string
		cfg   Config
		input string
		want  string
	}{
		{"mysql backtick", Config{TargetDBType: "mysql"}, "emp", "`emp`"},
		{"postgres double quote", Config{TargetDBType: "postgres"}, "emp", `"emp"`},
		{"oracle double quote", Config{TargetDBType: "oracle"}, "emp", `"emp"`},
		{"no quote", Config{TargetDBType: "mysql", NoQuoteIdentifiers: true}, "emp", "emp"},
		{"mysql escape backtick", Config{TargetDBType: "mysql"}, "a`b", "`a``b`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := New(nil, tt.cfg)
			if got := imp.quoteIdent(tt.input); got != tt.want {
				t.Errorf("quoteIdent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestUnit_IsAllDigits(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"123", true},
		{"0", true},
		{"12a3", false},
		{" 123", false},
		{"", true},
	}
	for _, tt := range tests {
		if got := isAllDigits(tt.in); got != tt.want {
			t.Errorf("isAllDigits(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestUnit_IsNullValue(t *testing.T) {
	imp := New(nil, Config{CSVNullMarker: "\\N", NullIf: []string{"NULL", "null", ""}})
	tests := []struct {
		in   string
		want bool
	}{
		{"\\N", true},
		{"NULL", true},
		{"null", true},
		{"", true},
		{"NULLL", false},
		{"value", false},
		{"Null", false},
	}
	for _, tt := range tests {
		if got := imp.isNullValue(tt.in); got != tt.want {
			t.Errorf("isNullValue(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func fkRef(childSchema, child, parentSchema, parent string) *md.ForeignKeyDef {
	return &md.ForeignKeyDef{TableSchema: childSchema, TableName: child, RefSchema: parentSchema, RefTable: parent}
}

func TestUnit_SortByForeignKeys(t *testing.T) {
	t.Run("parent before child", func(t *testing.T) {
		emp := &md.TableDef{TableSchema: "SCOTT", TableName: "EMP"}
		emp.AddForeignKey(fkRef("SCOTT", "EMP", "SCOTT", "DEPT"))
		dept := &md.TableDef{TableSchema: "SCOTT", TableName: "DEPT"}
		sorted := sortByForeignKeys([]*md.TableDef{emp, dept})
		if sorted[0].TableName != "DEPT" || sorted[1].TableName != "EMP" {
			t.Errorf("got [%s,%s], want [DEPT,EMP]", sorted[0].TableName, sorted[1].TableName)
		}
	})
	t.Run("chain", func(t *testing.T) {
		a := &md.TableDef{TableSchema: "S", TableName: "A"}
		b := &md.TableDef{TableSchema: "S", TableName: "B"}
		b.AddForeignKey(fkRef("S", "B", "S", "A"))
		c := &md.TableDef{TableSchema: "S", TableName: "C"}
		c.AddForeignKey(fkRef("S", "C", "S", "B"))
		sorted := sortByForeignKeys([]*md.TableDef{c, b, a})
		if sorted[0].TableName != "A" || sorted[1].TableName != "B" || sorted[2].TableName != "C" {
			t.Errorf("got [%s,%s,%s], want [A,B,C]", sorted[0].TableName, sorted[1].TableName, sorted[2].TableName)
		}
	})
	t.Run("no fk preserves order", func(t *testing.T) {
		x := &md.TableDef{TableSchema: "S", TableName: "X"}
		y := &md.TableDef{TableSchema: "S", TableName: "Y"}
		sorted := sortByForeignKeys([]*md.TableDef{x, y})
		if sorted[0].TableName != "X" || sorted[1].TableName != "Y" {
			t.Errorf("order not preserved: [%s,%s]", sorted[0].TableName, sorted[1].TableName)
		}
	})
	t.Run("external ref ignored", func(t *testing.T) {
		emp := &md.TableDef{TableSchema: "SCOTT", TableName: "EMP"}
		emp.AddForeignKey(fkRef("SCOTT", "EMP", "OTHER", "EXT"))
		dept := &md.TableDef{TableSchema: "SCOTT", TableName: "DEPT"}
		sorted := sortByForeignKeys([]*md.TableDef{emp, dept})
		if sorted[0].TableName != "EMP" || sorted[1].TableName != "DEPT" {
			t.Errorf("got [%s,%s], want [EMP,DEPT]", sorted[0].TableName, sorted[1].TableName)
		}
	})
	t.Run("cycle does not hang", func(t *testing.T) {
		a := &md.TableDef{TableSchema: "S", TableName: "A"}
		a.AddForeignKey(fkRef("S", "A", "S", "B"))
		b := &md.TableDef{TableSchema: "S", TableName: "B"}
		b.AddForeignKey(fkRef("S", "B", "S", "A"))
		sorted := sortByForeignKeys([]*md.TableDef{a, b})
		if len(sorted) != 2 {
			t.Errorf("expected 2 tables, got %d", len(sorted))
		}
	})
}
