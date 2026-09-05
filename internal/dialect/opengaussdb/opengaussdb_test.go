package opengaussdb

import (
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
)

func TestOpenGaussDB_Name(t *testing.T) {
	if New().Name() != "opengaussdb" {
		t.Errorf("Name() = %q, want opengaussdb", New().Name())
	}
}

func TestOpenGaussDB_TypeMapping(t *testing.T) {
	d := New()
	tests := []struct {
		raw  string
		want dialect.LogicalBase
	}{
		{"INTEGER", dialect.LBInt},
		{"BIGINT", dialect.LBBigInt},
		{"TEXT", dialect.LBCLOB},
		{"BOOLEAN", dialect.LBBoolean},
		{"JSONB", dialect.LBJSON},
	}
	for _, tt := range tests {
		if lt := d.ToLogicalType(tt.raw, 0, 0, 0); lt.Base != tt.want {
			t.Errorf("ToLogicalType(%q) = %v, want %v", tt.raw, lt.Base, tt.want)
		}
	}
	if got := d.FromLogicalType(dialect.LogicalType{Base: dialect.LBBoolean}); got != "BOOLEAN" {
		t.Errorf("FromLogicalType(BOOLEAN) = %q, want BOOLEAN", got)
	}
}

func TestOpenGaussDB_QuotingAndFeatures(t *testing.T) {
	d := New()
	if q := d.Quote("EMP"); q != `"emp"` {
		t.Errorf("Quote = %q, want \"emp\"", q)
	}
	if !d.SupportsTransactionalDDL() || !d.SupportsIfNotExists() {
		t.Error("opengaussdb should support transactional DDL and IF NOT EXISTS (PG features)")
	}
	if d.MaxIdentifierLength() != 63 {
		t.Errorf("MaxIdentifierLength = %d, want 63", d.MaxIdentifierLength())
	}
}

func TestOpenGaussDBMySQL_Name(t *testing.T) {
	if NewMySQL().Name() != "opengaussdb-mysql" {
		t.Errorf("Name() = %q, want opengaussdb-mysql", NewMySQL().Name())
	}
}

func TestOpenGaussDBMySQL_InheritsMySQLTypeMapping(t *testing.T) {
	d := NewMySQL()
	tests := []struct {
		raw  string
		want dialect.LogicalBase
	}{
		{"VARCHAR", dialect.LBVarchar},
		{"INT", dialect.LBInt},
		{"BIGINT", dialect.LBBigInt},
		{"DECIMAL", dialect.LBNumeric},
		{"DATETIME", dialect.LBDatetime},
		{"TEXT", dialect.LBCLOB},
		{"JSON", dialect.LBJSON},
	}
	for _, tt := range tests {
		if lt := d.ToLogicalType(tt.raw, 0, 10, 2); lt.Base != tt.want {
			t.Errorf("ToLogicalType(%q) = %v, want %v", tt.raw, lt.Base, tt.want)
		}
	}
	// Backtick quoting (MySQL) but PG features (transactional DDL).
	if q := d.Quote("emp"); q != "`emp`" {
		t.Errorf("Quote = %q, want `emp`", q)
	}
	if !d.SupportsTransactionalDDL() {
		t.Error("opengaussdb-mysql should support transactional DDL (PG feature)")
	}
}

func TestOpenGaussDBOracle_Name(t *testing.T) {
	if NewOracle().Name() != "opengaussdb-oracle" {
		t.Errorf("Name() = %q, want opengaussdb-oracle", NewOracle().Name())
	}
}

func TestOpenGaussDBOracle_InheritsOracleTypeMapping(t *testing.T) {
	d := NewOracle()
	tests := []struct {
		raw      string
		prec     int
		scale    int
		want     dialect.LogicalBase
	}{
		{"VARCHAR2", 0, 0, dialect.LBVarchar},
		{"NUMBER", 4, 0, dialect.LBSmallInt},
		{"NUMBER", 10, 2, dialect.LBNumeric},
		{"DATE", 0, 0, dialect.LBDatetime},
		{"CLOB", 0, 0, dialect.LBCLOB},
	}
	for _, tt := range tests {
		if lt := d.ToLogicalType(tt.raw, 0, tt.prec, tt.scale); lt.Base != tt.want {
			t.Errorf("ToLogicalType(%q,%d,%d) = %v, want %v", tt.raw, tt.prec, tt.scale, lt.Base, tt.want)
		}
	}
	// Oracle UPPER double-quote quoting but PG features.
	if q := d.Quote("emp"); q != `"EMP"` {
		t.Errorf("Quote = %q, want \"EMP\"", q)
	}
	if !d.SupportsTransactionalDDL() {
		t.Error("opengaussdb-oracle should support transactional DDL (PG feature)")
	}
}
