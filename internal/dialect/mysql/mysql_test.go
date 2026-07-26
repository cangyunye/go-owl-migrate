package mysql

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func TestMySQL_ToLogicalType(t *testing.T) {
	d := New()
	tests := []struct {
		name     string
		rawType  string
		length   int
		wantBase dialect.LogicalBase
	}{
		{"varchar", "VARCHAR", 100, dialect.LBVarchar},
		{"char", "CHAR", 10, dialect.LBChar},
		{"text", "TEXT", 0, dialect.LBCLOB},
		{"longtext", "LONGTEXT", 0, dialect.LBCLOB},
		{"tinyint", "TINYINT", 0, dialect.LBSmallInt},
		{"int", "INT", 0, dialect.LBInt},
		{"bigint", "BIGINT", 0, dialect.LBBigInt},
		{"decimal", "DECIMAL", 0, dialect.LBNumeric},
		{"boolean", "BOOLEAN", 0, dialect.LBBoolean},
		{"date", "DATE", 0, dialect.LBDate},
		{"datetime", "DATETIME", 0, dialect.LBDatetime},
		{"timestamp", "TIMESTAMP", 0, dialect.LBTimestamp},
		{"blob", "BLOB", 0, dialect.LBBLOB},
		{"json", "JSON", 0, dialect.LBJSON},
		{"enum", "ENUM", 0, dialect.LBEnum},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lt := d.ToLogicalType(tt.rawType, tt.length, 0, 0)
			if lt.Base != tt.wantBase {
				t.Errorf("ToLogicalType(%q) base = %v, want %v", tt.rawType, lt.Base, tt.wantBase)
			}
		})
	}
}

func TestMySQL_FromLogicalType(t *testing.T) {
	d := New()
	tests := []struct {
		name string
		lt   dialect.LogicalType
		want string
	}{
		{"varchar", dialect.LogicalType{Base: dialect.LBVarchar, Length: 100}, "VARCHAR(100)"},
		{"varchar default", dialect.LogicalType{Base: dialect.LBVarchar}, "VARCHAR(255)"},
		{"varchar over 65535 to longtext", dialect.LogicalType{Base: dialect.LBVarchar, Length: 70000}, "LONGTEXT"},
		{"char", dialect.LogicalType{Base: dialect.LBChar, Length: 10}, "CHAR(10)"},
		{"smallint", dialect.LogicalType{Base: dialect.LBSmallInt}, "SMALLINT"},
		{"int", dialect.LogicalType{Base: dialect.LBInt}, "INT"},
		{"bigint", dialect.LogicalType{Base: dialect.LBBigInt}, "BIGINT"},
		{"numeric", dialect.LogicalType{Base: dialect.LBNumeric, Precision: 10, Scale: 2}, "DECIMAL(10,2)"},
		{"float", dialect.LogicalType{Base: dialect.LBFloat}, "FLOAT"},
		{"double", dialect.LogicalType{Base: dialect.LBDouble}, "DOUBLE"},
		{"clob to longtext", dialect.LogicalType{Base: dialect.LBCLOB}, "LONGTEXT"},
		{"blob to longblob", dialect.LogicalType{Base: dialect.LBBLOB}, "LONGBLOB"},
		{"boolean to tinyint", dialect.LogicalType{Base: dialect.LBBoolean}, "TINYINT(1)"},
		{"json", dialect.LogicalType{Base: dialect.LBJSON}, "JSON"},
		{"datetime", dialect.LogicalType{Base: dialect.LBDatetime}, "DATETIME"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.FromLogicalType(tt.lt); got != tt.want {
				t.Errorf("FromLogicalType(%v) = %q, want %q", tt.lt.Base, got, tt.want)
			}
		})
	}
}

func TestMySQL_Quote(t *testing.T) {
	d := New()
	if got := d.Quote("emp"); got != "`emp`" {
		t.Errorf("Quote(emp) = %q, want `emp`", got)
	}
}

func TestMySQL_Features(t *testing.T) {
	d := New()
	if d.SupportsTransactionalDDL() {
		t.Error("MySQL should not support transactional DDL")
	}
	if !d.SupportsIfNotExists() {
		t.Error("MySQL should support IF NOT EXISTS")
	}
	if d.MaxIdentifierLength() != 64 {
		t.Errorf("MaxIdentifierLength = %d, want 64", d.MaxIdentifierLength())
	}
	if d.TruncateIsTransactional() {
		t.Error("MySQL TRUNCATE should not be transactional")
	}
}

func TestMySQL_BuildCreateTable_Partition(t *testing.T) {
	d := New()
	newTbl := func(info string) *md.TableDef {
		tbl, _ := md.NewTableDef("db", "t")
		col, _ := md.NewColumnDef("db", "t", "id", 1, "INT")
		tbl.AddColumn(col)
		tbl.Partitioned = "YES"
		tbl.PartitionInfo = info
		return tbl
	}
	const partClause = "PARTITION BY HASH(id) PARTITIONS 4"

	t.Run("appends partition when enabled", func(t *testing.T) {
		ddl, _ := d.BuildCreateTable(newTbl(partClause), dialect.BuildOptions{})
		if !strings.Contains(ddl, partClause) {
			t.Errorf("expected partition clause, got:\n%s", ddl)
		}
	})
	t.Run("skips when SkipPartitions", func(t *testing.T) {
		ddl, _ := d.BuildCreateTable(newTbl(partClause), dialect.BuildOptions{SkipPartitions: true})
		if strings.Contains(ddl, "PARTITION BY") {
			t.Errorf("did not expect partition clause, got:\n%s", ddl)
		}
	})
	t.Run("ignores raw metadata without PARTITION BY", func(t *testing.T) {
		ddl, _ := d.BuildCreateTable(newTbl("partitioned"), dialect.BuildOptions{})
		if strings.Contains(ddl, "partitioned") {
			t.Errorf("raw create_options should not be appended, got:\n%s", ddl)
		}
	})
}

func TestMySQL_BuildCreateTable_BooleanMapping(t *testing.T) {
	d := New()
	tbl, _ := md.NewTableDef("db", "t")
	col, _ := md.NewColumnDef("db", "t", "flag", 1, "TINYINT(1)")
	col.DefaultValue = "Y"
	tbl.AddColumn(col)
	ddl, _ := d.BuildCreateTable(tbl, dialect.BuildOptions{BooleanMapping: map[string]bool{"Y": true}})
	if !strings.Contains(ddl, "DEFAULT 1") {
		t.Errorf("expected DEFAULT 1 for MySQL numeric boolean, got:\n%s", ddl)
	}
}
