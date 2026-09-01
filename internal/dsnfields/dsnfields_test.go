package dsnfields

import "testing"

func TestDecomposeString(t *testing.T) {
	cases := []struct {
		typ, dsn string
		want     Fields
	}{
		{"postgres", "postgres://user:pass@host:5432/dbname", Fields{Username: "user", Password: "pass", Host: "host", Port: "5432", Database: "dbname"}},
		{"postgres", "host=h port=5432 user=u password=s dbname=d sslmode=disable", Fields{Username: "u", Password: "s", Host: "h", Port: "5432", Database: "d", Extra: "sslmode=disable"}},
		{"opengaussdb", "postgres://u:p@127.0.0.1:5432/gauss", Fields{Username: "u", Password: "p", Host: "127.0.0.1", Port: "5432", Database: "gauss"}},
		{"mysql", "user:pass@tcp(host:3306)/dbname?charset=utf8mb4", Fields{Username: "user", Password: "pass", Host: "host", Port: "3306", Database: "dbname", Extra: "charset=utf8mb4"}},
		{"oceanbase", "root@test:pw@tcp(192.168.1.1:2881)/ob", Fields{Username: "root@test", Password: "pw", Host: "192.168.1.1", Port: "2881", Database: "ob"}},
		{"oracle", "oracle://user:pass@host:1521/service", Fields{Username: "user", Password: "pass", Host: "host", Port: "1521", Database: "service"}},
		{"oceanbase-oracle", "oceanbase-oracle://sys@tenant:pw@127.0.0.1:2883/db?cluster=obcluster", Fields{Username: "sys@tenant", Password: "pw", Host: "127.0.0.1", Port: "2883", Database: "db", Extra: "cluster=obcluster"}},
		{"sqlite3", "/tmp/a.db", Fields{Database: "/tmp/a.db"}},
		{"duckdb", "file.db", Fields{Database: "file.db"}},
	}
	for _, c := range cases {
		got, err := Decompose(c.typ, c.dsn)
		if err != nil {
			t.Fatalf("%s %q: %v", c.typ, c.dsn, err)
		}
		if *got != c.want {
			t.Errorf("%s %q: got %+v want %+v", c.typ, c.dsn, *got, c.want)
		}
	}
}

func TestBuildRoundTrip(t *testing.T) {
	cases := []struct {
		typ, dsn string
	}{
		{"postgres", "postgres://user:pass@host:5432/dbname"},
		{"postgres", "postgres://u:p@127.0.0.1:5432/gauss?sslmode=disable"},
		{"mysql", "user:pass@tcp(host:3306)/dbname?charset=utf8mb4"},
		{"oceanbase", "root@test:pw@tcp(192.168.1.1:2881)/ob"},
		{"oracle", "oracle://user:pass@host:1521/service"},
		{"oceanbase-oracle", "oceanbase-oracle://sys@tenant:pw@127.0.0.1:2883/db?cluster=obcluster"},
	}
	for _, c := range cases {
		f, err := Decompose(c.typ, c.dsn)
		if err != nil {
			t.Fatalf("%s %q: decompose %v", c.typ, c.dsn, err)
		}
		got, err := Build(c.typ, *f, "")
		if err != nil {
			t.Fatalf("%s %q: build %v", c.typ, c.dsn, err)
		}
		gotF, err := Decompose(c.typ, got)
		if err != nil {
			t.Fatalf("%s: re-decompose %q: %v", c.typ, got, err)
		}
		if *gotF != *f {
			t.Errorf("%s %q: round-trip %+v != %+v (%q)", c.typ, c.dsn, *gotF, *f, got)
		}
	}
}

func TestBuildPreservesPassword(t *testing.T) {
	old := "postgres://user:secret@host:5432/dbname"
	got, err := Build("postgres", Fields{Username: "user", Host: "host", Port: "5432", Database: "other"}, old)
	if err != nil {
		t.Fatal(err)
	}
	if got != "postgres://user:secret@host:5432/other" {
		t.Errorf("got %q", got)
	}
}
