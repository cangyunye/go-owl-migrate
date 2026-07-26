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
