//go:build duckdb

package duckdb

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func TestDuckDB_Name(t *testing.T) {
	d := New()
	if d.Name() != "duckdb" {
		t.Errorf("Name() = %q, want duckdb", d.Name())
	}
}

func TestDuckDB_TypeMapping(t *testing.T) {
	d := New()
	tests := []struct {
		raw  string
		want dialect.LogicalBase
	}{
		{"VARCHAR", dialect.LBVarchar},
		{"TEXT", dialect.LBVarchar},
		{"INTEGER", dialect.LBInt},
		{"INT", dialect.LBInt},
		{"BIGINT", dialect.LBBigInt},
		{"HUGEINT", dialect.LBNumeric},
		{"FLOAT", dialect.LBFloat},
		{"DOUBLE", dialect.LBDouble},
		{"DECIMAL", dialect.LBNumeric},
		{"BOOLEAN", dialect.LBBoolean},
		{"DATE", dialect.LBDate},
		{"TIMESTAMP", dialect.LBTimestamp},
		{"BLOB", dialect.LBBLOB},
		{"JSON", dialect.LBJSON},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			lt := d.ToLogicalType(tt.raw, 0, 0, 0)
			if lt.Base != tt.want {
				t.Errorf("ToLogicalType(%q) base = %v, want %v", tt.raw, lt.Base, tt.want)
			}
		})
	}
}

func TestDuckDB_Features(t *testing.T) {
	d := New()
	if !d.SupportsTransactionalDDL() {
		t.Error("DuckDB should support transactional DDL")
	}
	if !d.SupportsIfNotExists() {
		t.Error("DuckDB should support IF NOT EXISTS")
	}
	if !d.SupportsJSONIndex() {
		t.Error("DuckDB should support JSON index")
	}
}

func TestDuckDB_DDLTable(t *testing.T) {
	d := New()
	tbl, _ := md.NewTableDef("main", "users")
	col, _ := md.NewColumnDef("main", "users", "id", 1, "INTEGER")
	col.Nullable = "NO"
	tbl.AddColumn(col)

	sql, err := d.BuildCreateTable(tbl, dialect.BuildOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "CREATE TABLE") {
		t.Error("DDL should contain CREATE TABLE")
	}
}

func TestDuckDB_DDLSequence(t *testing.T) {
	d := New()
	seq := &md.SequenceDef{
		SequenceSchema: "main", SequenceName: "my_seq",
		StartValue: 1, IncrementBy: 1, MinValue: 1, MaxValue: 999999, CacheSize: 1,
	}
	sql, err := d.BuildCreateSequence(seq, dialect.BuildOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "CREATE SEQUENCE") {
		t.Error("DDL should contain CREATE SEQUENCE")
	}
}

func TestDuckDB_Quoting(t *testing.T) {
	d := New()
	if q := d.Quote("t"); q != `"t"` {
		t.Errorf("Quote = %q", q)
	}
}
