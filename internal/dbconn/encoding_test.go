package dbconn

import (
	"strings"
	"testing"
)

func TestInjectEncodingDefaults(t *testing.T) {
	cases := []struct {
		name   string
		dbType string
		driver string
		dsn    string
		want   string
	}{
		{"mysql utf8mb4 untouched", "mysql", "mysql",
			"user:p@tcp(h:3306)/db?charset=utf8mb4&parseTime=true",
			"user:p@tcp(h:3306)/db?charset=utf8mb4&parseTime=true"},
		{"mysql charset list ok", "mysql", "mysql",
			"user:p@tcp(h:3306)/db?charset=utf8mb4,utf8mb4_general_ci",
			"user:p@tcp(h:3306)/db?charset=utf8mb4,utf8mb4_general_ci"},
		{"mysql gbk overridden", "mysql", "mysql",
			"user:p@tcp(h:3306)/db?charset=gbk",
			"user:p@tcp(h:3306)/db?charset=utf8mb4"},
		{"mysql latin1 overridden", "oceanbase-mysql", "mysql",
			"user:p@tcp(h:3306)/db?charset=LATIN1",
			"user:p@tcp(h:3306)/db?charset=utf8mb4"},
		{"mysql no charset untouched (driver default utf8mb4)", "mysql", "mysql",
			"user:p@tcp(h:3306)/db?parseTime=true",
			"user:p@tcp(h:3306)/db?parseTime=true"},
		{"postgres url injected", "postgres", "postgres",
			"postgres://u:p@h:5432/db?sslmode=disable",
			"postgres://u:p@h:5432/db?client_encoding=UTF8&sslmode=disable"},
		{"postgres url existing wins", "postgres", "postgres",
			"postgres://u:p@h:5432/db?client_encoding=GBK",
			"postgres://u:p@h:5432/db?client_encoding=GBK"},
		{"opengauss keyword injected", "opengaussdb", "opengauss",
			"host=h port=5432 user=u password=p dbname=db",
			"host=h port=5432 user=u password=p dbname=db client_encoding=UTF8"},
		{"opengauss keyword existing wins", "opengaussdb", "opengauss",
			"host=h user=u dbname=db client_encoding='GBK'",
			"host=h user=u dbname=db client_encoding='GBK'"},
		{"oracle untouched", "oceanbase-oracle", "oboracle",
			"oceanbase-oracle://u:p@h:2881/svc", "oceanbase-oracle://u:p@h:2881/svc"},
		{"oracle driver untouched", "oracle", "oracle",
			"oracle://scott:tiger@h:1521/ORCL", "oracle://scott:tiger@h:1521/ORCL"},
		{"sqlite3 untouched", "sqlite3", "sqlite3",
			"file:/tmp/x.db", "file:/tmp/x.db"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := injectEncodingDefaults(tc.dbType, tc.driver, tc.dsn); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInjectEncodingDefaultsWarns(t *testing.T) {
	var msgs []string
	EncodingWarnHook = func(dbType, message string) { msgs = append(msgs, dbType+": "+message) }
	defer func() { EncodingWarnHook = nil }()

	injectEncodingDefaults("mysql", "mysql", "u:p@tcp(h:3306)/db?charset=gbk")
	if len(msgs) != 1 || !strings.Contains(msgs[0], "overridden to utf8mb4") {
		t.Fatalf("warnings = %v, want one override warning", msgs)
	}

	msgs = nil
	injectEncodingDefaults("mysql", "mysql", "u:p@tcp(h:3306)/db?charset=utf8mb4")
	injectEncodingDefaults("oracle", "oboracle", "x")
	if len(msgs) != 0 {
		t.Fatalf("warnings = %v, want none", msgs)
	}
}
