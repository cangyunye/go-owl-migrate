package extractor

import (
	"strings"
	"testing"
)

func TestNormalizeDBType(t *testing.T) {
	tests := []struct{ in, want string }{
		{"goldendb-mysql", "mysql"},
		{"goldendb-oracle", "oracle"},
		{"oceanbase-mysql", "mysql"},
		{"oceanbase-oracle", "oracle"},
		{"oceanbase-oracle-wire", "oracle"},
		{"goldendb", "mysql"},
		{"oceanbase", "mysql"},
		{"panweidb", "postgres"},
		{"opengaussdb", "postgres"},
		{"postgres", "postgres"},
		{"mysql", "mysql"},
		{"oracle", "oracle"},
	}
	for _, tt := range tests {
		if got := normalizeDBType(tt.in); got != tt.want {
			t.Errorf("normalizeDBType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGet(t *testing.T) {
	for _, dbType := range []string{"postgres", "mysql", "oracle"} {
		if _, err := Get(dbType); err != nil {
			t.Errorf("Get(%q) unexpected error: %v", dbType, err)
		}
	}
	if _, err := Get("nonexistent"); err == nil {
		t.Error("Get(nonexistent) expected error")
	}
}

func TestGetQuerySQL(t *testing.T) {
	for _, dbType := range []string{"postgres", "mysql", "oracle", "oceanbase-oracle", "oceanbase-oracle-wire"} {
		for _, obj := range []string{"tables", "columns", "pk", "indexes", "views"} {
			if sql := GetQuerySQL(dbType, obj); sql == "" {
				t.Errorf("GetQuerySQL(%q, %q) = empty", dbType, obj)
			}
		}
	}
	if sql := GetQuerySQL("postgres", "bogus"); sql != "" {
		t.Errorf("GetQuerySQL(postgres, bogus) = %q, want empty", sql)
	}
	// OceanBase Oracle tenant columns query must omit collation and identity.
	if sql := GetQuerySQL("oceanbase-oracle-wire", "columns"); strings.Contains(sql, "collation") || strings.Contains(sql, "identity") {
		t.Errorf("oceanbase-oracle-wire columns SQL must omit collation/identity, got: %s", sql)
	}
	if sql := GetQuerySQL("oracle", "columns"); !strings.Contains(sql, "collation") {
		t.Errorf("native oracle columns SQL should keep collation, got: %s", sql)
	}
}

func TestIsOceanBaseOracle(t *testing.T) {
	for _, in := range []string{"oceanbase-oracle", "oceanbase-oracle-wire"} {
		if !isOceanBaseOracle(in) {
			t.Errorf("isOceanBaseOracle(%q) = false, want true", in)
		}
	}
	for _, in := range []string{"oracle", "oceanbase", "oceanbase-mysql", "postgres"} {
		if isOceanBaseOracle(in) {
			t.Errorf("isOceanBaseOracle(%q) = true, want false", in)
		}
	}
}

func TestOracleBindPlaceholderRewrite(t *testing.T) {
	src := "WHERE owner = UPPER(:1) OR table_owner = UPPER(:1)"

	native := OracleMetadataQuerier{}
	if got := native.bind(src); got != src {
		t.Errorf("native bind should keep :N, got %q", got)
	}

	wire := OracleMetadataQuerier{Placeholder: "?"}
	got := wire.bind(src)
	want := "WHERE owner = UPPER(?) OR table_owner = UPPER(?)"
	if got != want {
		t.Errorf("wire bind = %q, want %q", got, want)
	}
}

func TestOceanBaseOracleWireQuerierRegistered(t *testing.T) {
	q, err := Get("oceanbase-oracle-wire")
	if err != nil {
		t.Fatalf("Get(oceanbase-oracle-wire): %v", err)
	}
	if q.Type() != "oceanbase-oracle-wire" {
		t.Errorf("querier type = %q", q.Type())
	}
	// TNS-style oceanbase-oracle still routes to the native ":N" querier.
	if _, err := Get(normalizeDBType("oceanbase-oracle")); err != nil {
		t.Fatalf("normalized oceanbase-oracle lookup: %v", err)
	}
}
