package postgres

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func TestPostgres_ToLogicalType(t *testing.T) {
	d := New()
	tests := []struct {
		name     string
		rawType  string
		length   int
		wantBase dialect.LogicalBase
	}{
		{"varchar", "VARCHAR", 100, dialect.LBVarchar},
		{"text", "TEXT", 0, dialect.LBCLOB},
		{"integer", "INTEGER", 0, dialect.LBInt},
		{"int4", "INT4", 0, dialect.LBInt},
		{"bigint", "BIGINT", 0, dialect.LBBigInt},
		{"smallint", "SMALLINT", 0, dialect.LBSmallInt},
		{"numeric", "NUMERIC", 0, dialect.LBNumeric},
		{"boolean", "BOOLEAN", 0, dialect.LBBoolean},
		{"date", "DATE", 0, dialect.LBDate},
		{"timestamp", "TIMESTAMP", 0, dialect.LBTimestamp},
		{"timestamptz", "TIMESTAMPTZ", 0, dialect.LBTimestampTZ},
		{"bytea", "BYTEA", 0, dialect.LBBLOB},
		{"jsonb", "JSONB", 0, dialect.LBJSON},
		{"uuid to varchar", "UUID", 0, dialect.LBVarchar},
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

func TestPostgres_FromLogicalType(t *testing.T) {
	d := New()
	tests := []struct {
		name string
		lt   dialect.LogicalType
		want string
	}{
		{"varchar", dialect.LogicalType{Base: dialect.LBVarchar, Length: 100}, "VARCHAR(100)"},
		{"varchar bare", dialect.LogicalType{Base: dialect.LBVarchar}, "VARCHAR"},
		{"char", dialect.LogicalType{Base: dialect.LBChar, Length: 10}, "CHAR(10)"},
		{"smallint", dialect.LogicalType{Base: dialect.LBSmallInt}, "SMALLINT"},
		{"int", dialect.LogicalType{Base: dialect.LBInt}, "INTEGER"},
		{"bigint", dialect.LogicalType{Base: dialect.LBBigInt}, "BIGINT"},
		{"numeric", dialect.LogicalType{Base: dialect.LBNumeric, Precision: 10, Scale: 2}, "NUMERIC(10,2)"},
		{"numeric precision only", dialect.LogicalType{Base: dialect.LBNumeric, Precision: 10}, "NUMERIC(10)"},
		{"numeric bare", dialect.LogicalType{Base: dialect.LBNumeric}, "NUMERIC"},
		{"float", dialect.LogicalType{Base: dialect.LBFloat}, "REAL"},
		{"double", dialect.LogicalType{Base: dialect.LBDouble}, "DOUBLE PRECISION"},
		{"clob to text", dialect.LogicalType{Base: dialect.LBCLOB}, "TEXT"},
		{"blob to bytea", dialect.LogicalType{Base: dialect.LBBLOB}, "BYTEA"},
		{"boolean", dialect.LogicalType{Base: dialect.LBBoolean}, "BOOLEAN"},
		{"json to jsonb", dialect.LogicalType{Base: dialect.LBJSON}, "JSONB"},
		{"timestamptz", dialect.LogicalType{Base: dialect.LBTimestampTZ}, "TIMESTAMPTZ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.FromLogicalType(tt.lt); got != tt.want {
				t.Errorf("FromLogicalType(%v) = %q, want %q", tt.lt.Base, got, tt.want)
			}
		})
	}
}

func TestPostgres_Quote(t *testing.T) {
	d := New()
	if got := d.Quote("EMP"); got != `"emp"` {
		t.Errorf("Quote(EMP) = %q, want %q", got, `"emp"`)
	}
}

func TestPostgres_Features(t *testing.T) {
	d := New()
	if !d.SupportsTransactionalDDL() {
		t.Error("PostgreSQL should support transactional DDL")
	}
	if !d.SupportsIfNotExists() {
		t.Error("PostgreSQL should support IF NOT EXISTS")
	}
	if d.MaxIdentifierLength() != 63 {
		t.Errorf("MaxIdentifierLength = %d, want 63", d.MaxIdentifierLength())
	}
	if !d.TruncateIsTransactional() {
		t.Error("PostgreSQL TRUNCATE should be transactional")
	}
}

func TestPostgres_BuildCreateTable_IdentityToSerial(t *testing.T) {
	d := New()
	newTbl := func() *md.TableDef {
		tbl, _ := md.NewTableDef("public", "users")
		id, _ := md.NewColumnDef("public", "users", "id", 1, "INTEGER")
		id.IsIdentity = "YES"
		id.Nullable = "NO"
		tbl.AddColumn(id)
		name, _ := md.NewColumnDef("public", "users", "name", 2, "VARCHAR")
		tbl.AddColumn(name)
		return tbl
	}

	t.Run("identity becomes serial", func(t *testing.T) {
		ddl, err := d.BuildCreateTable(newTbl(), dialect.BuildOptions{IdentityToSerial: true})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !strings.Contains(ddl, `"id" SERIAL NOT NULL`) {
			t.Errorf("expected `\"id\" SERIAL NOT NULL`, got:\n%s", ddl)
		}
	})
	t.Run("bigint identity becomes bigserial", func(t *testing.T) {
		tbl, _ := md.NewTableDef("public", "t")
		id, _ := md.NewColumnDef("public", "t", "id", 1, "BIGINT")
		id.IsIdentity = "YES"
		tbl.AddColumn(id)
		ddl, _ := d.BuildCreateTable(tbl, dialect.BuildOptions{IdentityToSerial: true})
		if !strings.Contains(ddl, `"id" BIGSERIAL`) {
			t.Errorf("expected BIGSERIAL, got:\n%s", ddl)
		}
	})
	t.Run("disabled keeps raw type", func(t *testing.T) {
		ddl, _ := d.BuildCreateTable(newTbl(), dialect.BuildOptions{IdentityToSerial: false})
		if !strings.Contains(ddl, `"id" INTEGER`) {
			t.Errorf("expected raw INTEGER when disabled, got:\n%s", ddl)
		}
	})
}

func TestPostgres_BuildCreateTable_TypeOptions(t *testing.T) {
	d := New()
	newTbl := func() *md.TableDef {
		tbl, _ := md.NewTableDef("public", "t")
		c, _ := md.NewColumnDef("public", "t", "code", 1, "VARCHAR2")
		c.DataLength = 20
		tbl.AddColumn(c)
		return tbl
	}

	t.Run("type_overrides", func(t *testing.T) {
		ddl, _ := d.BuildCreateTable(newTbl(), dialect.BuildOptions{TypeOverrides: map[string]string{"VARCHAR2": "VARCHAR(%l)"}})
		if !strings.Contains(ddl, `"code" VARCHAR(20)`) {
			t.Errorf("expected VARCHAR(20) override, got:\n%s", ddl)
		}
	})
	t.Run("empty_string_to_null", func(t *testing.T) {
		tbl := newTbl()
		tbl.GetColumn("code").DefaultValue = "''"
		ddl, _ := d.BuildCreateTable(tbl, dialect.BuildOptions{EmptyStringToNull: true})
		if !strings.Contains(ddl, "DEFAULT NULL") {
			t.Errorf("expected DEFAULT NULL, got:\n%s", ddl)
		}
	})
	t.Run("boolean_mapping", func(t *testing.T) {
		tbl, _ := md.NewTableDef("public", "t")
		flag, _ := md.NewColumnDef("public", "t", "active", 1, "BOOLEAN")
		flag.DefaultValue = "Y"
		tbl.AddColumn(flag)
		ddl, _ := d.BuildCreateTable(tbl, dialect.BuildOptions{BooleanMapping: map[string]bool{"Y": true}})
		if !strings.Contains(ddl, "DEFAULT TRUE") {
			t.Errorf("expected DEFAULT TRUE, got:\n%s", ddl)
		}
	})
}
