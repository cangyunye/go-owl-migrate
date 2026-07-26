package oracle

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func TestOracle_ToLogicalType(t *testing.T) {
	d := New()
	tests := []struct {
		name      string
		rawType   string
		length    int
		precision int
		scale     int
		wantBase  dialect.LogicalBase
	}{
		{"varchar2", "VARCHAR2", 100, 0, 0, dialect.LBVarchar},
		{"nvarchar2", "NVARCHAR2", 50, 0, 0, dialect.LBVarchar},
		{"char", "CHAR", 10, 0, 0, dialect.LBChar},
		{"number smallint", "NUMBER", 0, 3, 0, dialect.LBSmallInt},
		{"number int", "NUMBER", 0, 9, 0, dialect.LBInt},
		{"number bigint", "NUMBER", 0, 18, 0, dialect.LBBigInt},
		{"number numeric", "NUMBER", 0, 10, 2, dialect.LBNumeric},
		{"binary_float", "BINARY_FLOAT", 0, 0, 0, dialect.LBFloat},
		{"binary_double", "BINARY_DOUBLE", 0, 0, 0, dialect.LBDouble},
		{"date is datetime", "DATE", 0, 0, 0, dialect.LBDatetime},
		{"timestamp", "TIMESTAMP", 0, 0, 0, dialect.LBTimestamp},
		{"timestamp tz", "TIMESTAMP WITH TIME ZONE", 0, 0, 0, dialect.LBTimestampTZ},
		{"clob", "CLOB", 0, 0, 0, dialect.LBCLOB},
		{"blob", "BLOB", 0, 0, 0, dialect.LBBLOB},
		{"raw", "RAW", 100, 0, 0, dialect.LBVarBinary},
		{"xmltype", "XMLTYPE", 0, 0, 0, dialect.LBXML},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lt := d.ToLogicalType(tt.rawType, tt.length, tt.precision, tt.scale)
			if lt.Base != tt.wantBase {
				t.Errorf("ToLogicalType(%q,%d,%d,%d) base = %v, want %v",
					tt.rawType, tt.length, tt.precision, tt.scale, lt.Base, tt.wantBase)
			}
		})
	}
}

func TestOracle_FromLogicalType(t *testing.T) {
	d := New()
	tests := []struct {
		name string
		lt   dialect.LogicalType
		want string
	}{
		{"varchar", dialect.LogicalType{Base: dialect.LBVarchar, Length: 100}, "VARCHAR2(100)"},
		{"varchar over 4000 to clob", dialect.LogicalType{Base: dialect.LBVarchar, Length: 5000}, "CLOB"},
		{"char", dialect.LogicalType{Base: dialect.LBChar, Length: 10}, "CHAR(10)"},
		{"smallint", dialect.LogicalType{Base: dialect.LBSmallInt, Precision: 4}, "NUMBER(4,0)"},
		{"int", dialect.LogicalType{Base: dialect.LBInt, Precision: 9}, "NUMBER(9,0)"},
		{"bigint", dialect.LogicalType{Base: dialect.LBBigInt, Precision: 18}, "NUMBER(18,0)"},
		{"numeric", dialect.LogicalType{Base: dialect.LBNumeric, Precision: 10, Scale: 2}, "NUMBER(10,2)"},
		{"numeric bare", dialect.LogicalType{Base: dialect.LBNumeric}, "NUMBER"},
		{"float", dialect.LogicalType{Base: dialect.LBFloat}, "BINARY_FLOAT"},
		{"double", dialect.LogicalType{Base: dialect.LBDouble}, "BINARY_DOUBLE"},
		{"datetime", dialect.LogicalType{Base: dialect.LBDatetime}, "DATE"},
		{"timestamp", dialect.LogicalType{Base: dialect.LBTimestamp}, "TIMESTAMP"},
		{"timestamptz", dialect.LogicalType{Base: dialect.LBTimestampTZ}, "TIMESTAMP WITH TIME ZONE"},
		{"clob", dialect.LogicalType{Base: dialect.LBCLOB}, "CLOB"},
		{"blob", dialect.LogicalType{Base: dialect.LBBLOB}, "BLOB"},
		{"varbinary", dialect.LogicalType{Base: dialect.LBVarBinary, Length: 100}, "RAW(100)"},
		{"json to clob", dialect.LogicalType{Base: dialect.LBJSON}, "CLOB"},
		{"xml", dialect.LogicalType{Base: dialect.LBXML}, "XMLTYPE"},
		{"boolean", dialect.LogicalType{Base: dialect.LBBoolean}, "NUMBER(1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.FromLogicalType(tt.lt); got != tt.want {
				t.Errorf("FromLogicalType(%v) = %q, want %q", tt.lt.Base, got, tt.want)
			}
		})
	}
}

func TestOracle_Quote(t *testing.T) {
	d := New()
	if got := d.Quote("emp"); got != `"EMP"` {
		t.Errorf("Quote(emp) = %q, want %q", got, `"EMP"`)
	}
}

func TestOracle_Features(t *testing.T) {
	d := New()
	if d.SupportsTransactionalDDL() {
		t.Error("Oracle should not support transactional DDL")
	}
	if d.SupportsIfNotExists() {
		t.Error("Oracle should not support IF NOT EXISTS")
	}
	if d.MaxIdentifierLength() != 128 {
		t.Errorf("MaxIdentifierLength = %d, want 128", d.MaxIdentifierLength())
	}
	if d.TruncateIsTransactional() {
		t.Error("Oracle TRUNCATE should not be transactional")
	}
}

func TestOracle_BuildCreateTable_AddRowIDColumn(t *testing.T) {
	d := New()
	newTbl := func() *md.TableDef {
		tbl, _ := md.NewTableDef("SCOTT", "EMP")
		empno, _ := md.NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
		empno.Nullable = "NO"
		tbl.AddColumn(empno)
		ename, _ := md.NewColumnDef("SCOTT", "EMP", "ENAME", 2, "VARCHAR2")
		tbl.AddColumn(ename)
		return tbl
	}

	t.Run("adds rowid column", func(t *testing.T) {
		ddl, err := d.BuildCreateTable(newTbl(), dialect.BuildOptions{AddRowIDColumn: true})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !strings.Contains(ddl, `"ORIG_ROWID" ROWID`) {
			t.Errorf("expected ORIG_ROWID ROWID column, got:\n%s", ddl)
		}
		if !strings.Contains(ddl, `"ENAME" VARCHAR2,`) {
			t.Errorf("expected trailing comma after last data column, got:\n%s", ddl)
		}
	})
	t.Run("disabled no rowid", func(t *testing.T) {
		ddl, _ := d.BuildCreateTable(newTbl(), dialect.BuildOptions{AddRowIDColumn: false})
		if strings.Contains(ddl, "ORIG_ROWID") {
			t.Errorf("did not expect ORIG_ROWID when disabled, got:\n%s", ddl)
		}
	})
}
