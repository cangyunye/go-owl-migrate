package extractor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
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
		{"panweidb-mysql", "postgres"},
		{"panweidb-oracle", "postgres"},
		{"PanWeiDB-MySQL", "postgres"},
		{"opengaussdb", "postgres"},
		{"opengaussdb-mysql", "postgres"},
		{"opengaussdb-oracle", "postgres"},
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

// TestResolveQuerierPGWireFamily pins the openGauss-family routing rule:
// openGaussDB and PanWeiDB speak the PG wire protocol in every compatibility
// mode, so their "-mysql"/"-oracle" suffixes pick the DDL/type dialect only and
// must never select the MySQL or Oracle querier. Those queriers bind with
// "?"/":N", which the server parses as ordinary tokens and rejects — the
// reported symptom being `pq: syntax error at or near "AND"`.
func TestResolveQuerierPGWireFamily(t *testing.T) {
	for _, dbType := range []string{
		"panweidb", "panweidb-mysql", "panweidb-oracle",
		"opengaussdb", "opengaussdb-mysql", "opengaussdb-oracle",
		"PanWeiDB-MySQL",
	} {
		q, err := resolveQuerier(dbType)
		if err != nil {
			t.Fatalf("resolveQuerier(%q): %v", dbType, err)
		}
		if q.Type() != "postgres" {
			t.Errorf("resolveQuerier(%q).Type() = %q, want postgres", dbType, q.Type())
		}
	}

	// Non-PG-wire families must not be captured by the rule above. Their exact
	// registration wins when compiled in (oceanbase-mysql has its own querier
	// under -tags ob), so only the family is asserted here.
	for _, dbType := range []string{
		"mysql", "oracle", "postgres",
		"goldendb-mysql", "oceanbase-mysql", "oceanbase-oracle",
	} {
		q, err := resolveQuerier(dbType)
		if err != nil {
			t.Fatalf("resolveQuerier(%q): %v", dbType, err)
		}
		if dbType != "postgres" && q.Type() == "postgres" {
			t.Errorf("resolveQuerier(%q).Type() = postgres, want its own querier", dbType)
		}
	}
	for dbType, want := range map[string]string{"mysql": "mysql", "oracle": "oracle", "postgres": "postgres"} {
		q, err := resolveQuerier(dbType)
		if err != nil {
			t.Fatalf("resolveQuerier(%q): %v", dbType, err)
		}
		if q.Type() != want {
			t.Errorf("resolveQuerier(%q).Type() = %q, want %q", dbType, q.Type(), want)
		}
	}
}

// TestQueryTablesPanWeiDBMySQLUsesPGBinds is the regression test for the
// reported "extract metadata from panweidb-mysql: query tables: pq syntax error
// at or near AND": the tables query for a PanWeiDB/openGauss B-mode source must
// bind with $1. A MySQL-style "?" travels to the server verbatim, and its
// parser fails at the token following it.
func TestQueryTablesPanWeiDBMySQLUsesPGBinds(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	q, err := resolveQuerier("panweidb-mysql")
	if err != nil {
		t.Fatalf("resolveQuerier: %v", err)
	}

	rows := sqlmock.NewRows([]string{"table_name", "table_type", "table_comment", "partitioned"}).
		AddRow("EMP", "BASE TABLE", "", "NO")
	mock.ExpectQuery(`table_schema = \$1`).WithArgs("public").WillReturnRows(rows)

	tables, err := q.QueryTables(db, "public")
	if err != nil {
		t.Fatalf("QueryTables: %v", err)
	}
	if len(tables) != 1 || tables[0].TableName != "EMP" {
		t.Fatalf("tables = %+v, want single EMP", tables)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
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

func TestParseOracleVersion(t *testing.T) {
	cases := []struct {
		banner string
		want   string // "M.m.p"; "" means unparseable
	}{
		{"OceanBase 4.3.3.0 (r100000192024040922) (Built Apr 9 2024)", "4.3.3"},
		{"OceanBase 3.2.4.0 (r1000001920230101)", "3.2.4"},
		{"Oracle Database 19c Enterprise Edition Release 19.0.0.0.0 - Production", "19.0.0"},
		{"OceanBase 4.4.2.0 (r1000001920241107)", "4.4.2"},
		{"not a version banner", ""},
	}
	for _, c := range cases {
		got, ok := parseOracleVersion(c.banner)
		if c.want == "" {
			if ok {
				t.Errorf("parseOracleVersion(%q) = %v, %v; want not-ok", c.banner, got, ok)
			}
			continue
		}
		if !ok {
			t.Fatalf("parseOracleVersion(%q) not ok", c.banner)
		}
		want := fmt.Sprintf("%d.%d.%d", got.major, got.minor, got.patch)
		if want != c.want {
			t.Errorf("parseOracleVersion(%q) = %q, want %q", c.banner, want, c.want)
		}
	}
}

func TestOracleVersionAtLeast(t *testing.T) {
	cases := []struct {
		v, cmp string
		want   bool
	}{
		{"4.3.3", "4.3.3", true},
		{"4.3.4", "4.3.3", true},
		{"4.4.0", "4.3.3", true},
		{"4.3.2", "4.3.3", false},
		{"3.2.4", "4.3.3", false},
		{"4.2.9", "4.3.3", false},
	}
	for _, c := range cases {
		v, _ := parseOracleVersion("OceanBase " + c.v + ".0")
		got := v.atLeast(4, 3, 3)
		if got != c.want {
			t.Errorf("atLeast(%s, 4.3.3) = %v, want %v", c.v, got, c.want)
		}
	}
}
