package plancheck

import "testing"

// TestQuoteIdent 守护方言引用契约：MySQL 线协议用反引号、其余一律双引号，
// 内嵌引号字符必须翻倍而不是剥除——探针必须命中导出会碰到的同一个对象，
// 包括含特殊字符的标识符（历史教训：静默大小写折叠会让探针查到导出查不到的表）。
func TestQuoteIdent(t *testing.T) {
	tests := []struct {
		name    string
		dialect string
		in      string
		want    string
	}{
		{"mysql", "mysql", "EMP", "`EMP`"},
		{"oceanbase", "oceanbase", "t1", "`t1`"},
		{"goldendb", "goldendb", "t1", "`t1`"},
		{"composite mysql tenant", "oceanbase-mysql", "t1", "`t1`"},
		{"oracle", "oracle", "EMP", `"EMP"`},
		{"dm", "dm", "EMP", `"EMP"`},
		{"timesten", "timesten", "EMP", `"EMP"`},
		{"postgres", "postgres", "emp", `"emp"`},
		{"opengaussdb", "opengaussdb", "emp", `"emp"`},
		{"panweidb", "panweidb", "emp", `"emp"`},
		{"kingbase", "kingbase", "emp", `"emp"`},
		{"sqlite3", "sqlite3", "emp", `"emp"`},
		{"duckdb", "duckdb", "emp", `"emp"`},
		{"mysql backtick doubled", "mysql", "a`b", "`a``b`"},
		{"postgres double quote doubled", "postgres", `a"b`, `"a""b"`},
		{"name used verbatim no case folding", "oracle", "MixedCase", `"MixedCase"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteIdent(tc.dialect, tc.in); got != tc.want {
				t.Errorf("quoteIdent(%q, %q) = %q, want %q", tc.dialect, tc.in, got, tc.want)
			}
		})
	}
}

// TestQuoteQualified 守护 "schema.table" 的组合规则：嵌入式库没有 schema
// 命名空间必须丢弃 schema 部分；未知方言不引用、让探针在真实库上大声失败
// （而不是包一层引号后失败原因变得不可读）。
func TestQuoteQualified(t *testing.T) {
	tests := []struct {
		name    string
		dialect string
		schema  string
		table   string
		want    string
	}{
		{"mysql qualified", "mysql", "scott", "emp", "`scott`.`emp`"},
		{"oracle qualified", "oracle", "SCOTT", "EMP", `"SCOTT"."EMP"`},
		{"sqlite3 drops schema", "sqlite3", "SCOTT", "emp", `"emp"`},
		{"duckdb drops schema", "duckdb", "main", "t", `"t"`},
		{"empty schema keeps table only", "postgres", "", "emp", `"emp"`},
		{"unknown dialect unquoted", "fancydb", "scott", "emp", "scott.emp"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteQualified(tc.dialect, tc.schema, tc.table); got != tc.want {
				t.Errorf("quoteQualified(%q, %q, %q) = %q, want %q",
					tc.dialect, tc.schema, tc.table, got, tc.want)
			}
		})
	}
}

// TestDialectFamilies 锁定方言→产品族分类表。它决定探针 SQL 的引用形态，
// 与 configbuild.PasswordSentinelFor 同构——两边漂移意味着预检查的库和
// 实际迁移的库引用方式不同。
func TestDialectFamilies(t *testing.T) {
	tests := []struct {
		name      string
		dialect   string
		mysqlFam  bool
		oracleFam bool
		quoteable bool
	}{
		{"mysql", "mysql", true, false, true},
		{"mysql case insensitive", "MySQL", true, false, true},
		{"mysql padded", " mysql ", true, false, true},
		{"oceanbase", "oceanbase", true, false, true},
		{"goldendb", "goldendb", true, false, true},
		{"ob mysql tenant", "oceanbase-mysql", true, false, true},
		{"oracle", "oracle", false, true, true},
		{"dm", "dm", false, true, true},
		{"timesten", "timesten", false, true, true},
		{"ob oracle tenant", "oceanbase-oracle", false, true, true},
		{"postgres", "postgres", false, false, true},
		{"greenplum contains postgres", "greenplum-postgres", false, false, true},
		{"opengaussdb", "opengaussdb", false, false, true},
		{"panweidb mysql", "panweidb-mysql", true, false, true},
		{"panweidb oracle", "panweidb-oracle", false, true, true},
		{"kingbase", "kingbase", false, false, true},
		{"sqlite3", "sqlite3", false, false, true},
		{"duckdb", "duckdb", false, false, true},
		{"unknown dialect", "fancydb", false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMySQLFamily(tc.dialect); got != tc.mysqlFam {
				t.Errorf("isMySQLFamily(%q) = %v, want %v", tc.dialect, got, tc.mysqlFam)
			}
			if got := isOracleFamily(tc.dialect); got != tc.oracleFam {
				t.Errorf("isOracleFamily(%q) = %v, want %v", tc.dialect, got, tc.oracleFam)
			}
			if got := isQuoteable(tc.dialect); got != tc.quoteable {
				t.Errorf("isQuoteable(%q) = %v, want %v", tc.dialect, got, tc.quoteable)
			}
		})
	}
}
