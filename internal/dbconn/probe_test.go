package dbconn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

// The charset stub serves one canned row per query so ProbeServerEncoding's
// SQL selection, scanning and classification run against a real database/sql
// handle without a live server. Tests run sequentially, so the canned state
// stays package-level.
var (
	stubMu        sync.Mutex
	stubValue     string
	stubLastQuery string
)

type charsetStubDriver struct{}

func (d *charsetStubDriver) Open(string) (driver.Conn, error) { return &charsetStubConn{}, nil }

type charsetStubConn struct{}

func (c *charsetStubConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *charsetStubConn) Close() error                        { return nil }
func (c *charsetStubConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }

func (c *charsetStubConn) Query(query string, _ []driver.NamedValue) (driver.Rows, error) {
	return c.QueryContext(context.Background(), query, nil)
}

func (c *charsetStubConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	stubMu.Lock()
	stubLastQuery = query
	stubMu.Unlock()
	return &charsetStubRows{}, nil
}

type charsetStubRows struct{ done bool }

func (r *charsetStubRows) Columns() []string { return []string{"value"} }
func (r *charsetStubRows) Close() error      { return nil }
func (r *charsetStubRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	stubMu.Lock()
	defer stubMu.Unlock()
	dest[0] = stubValue
	return nil
}

func newCharsetStubDB(t *testing.T, serverCharset string) *sql.DB {
	t.Helper()
	stubMu.Lock()
	stubValue = serverCharset
	stubLastQuery = ""
	stubMu.Unlock()
	name := "charsetStub_" + strings.ReplaceAll(t.Name(), "/", "_")
	sql.Register(name, &charsetStubDriver{})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestProbeServerEncoding(t *testing.T) {
	cases := []struct {
		name        string
		dbType      string
		serverValue string
		wantUnicode bool
	}{
		{"ob-oracle utf8 tenant", "oceanbase-oracle", "AL32UTF8", true},
		{"oracle legacy utf8", "oracle", "UTF8", true},
		{"oracle gbk not unicode", "oracle", "ZHS16GBK", false},
		{"mysql utf8mb4", "mysql", "utf8mb4", true},
		{"mysql gbk not unicode", "oceanbase-mysql", "gbk", false},
		{"postgres utf8", "postgres", "UTF8", true},
		{"opengauss gbk not unicode", "opengaussdb", "GBK", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newCharsetStubDB(t, tc.serverValue)
			got, err := ProbeServerEncoding(context.Background(), db, tc.dbType)
			if err != nil {
				t.Fatalf("ProbeServerEncoding: %v", err)
			}
			if got.Charset != tc.serverValue || got.Unicode != tc.wantUnicode {
				t.Fatalf("encoding = %+v, want {%s %v}", got, tc.serverValue, tc.wantUnicode)
			}
		})
	}
}

func TestProbeServerEncodingUsesDialectSQL(t *testing.T) {
	db := newCharsetStubDB(t, "AL32UTF8")
	if _, err := ProbeServerEncoding(context.Background(), db, "oceanbase-oracle"); err != nil {
		t.Fatalf("probe: %v", err)
	}
	stubMu.Lock()
	q := stubLastQuery
	stubMu.Unlock()
	if !strings.Contains(q, "NLS_CHARACTERSET") {
		t.Fatalf("oracle probe sql = %q, want NLS_CHARACTERSET query", q)
	}
}

func TestProbeServerEncodingUnsupportedFamily(t *testing.T) {
	db := newCharsetStubDB(t, "x")
	if _, err := ProbeServerEncoding(context.Background(), db, "duckdb"); err == nil {
		t.Fatalf("duckdb probe must be unsupported")
	}
}

func TestUnicodeClassification(t *testing.T) {
	if !unicodeOracleCharset("al32utf8") || unicodeOracleCharset("ZHS16GBK") || unicodeOracleCharset("US7ASCII") {
		t.Fatalf("oracle classification wrong")
	}
	if !unicodeMySQLCharset("UTF8MB4") || !unicodeMySQLCharset(" utf8 ") || unicodeMySQLCharset("gbk") || unicodeMySQLCharset("latin1") {
		t.Fatalf("mysql classification wrong")
	}
	if !unicodePostgresEncoding("utf8") || !unicodePostgresEncoding(" UNICODE ") || unicodePostgresEncoding("GB18030") {
		t.Fatalf("postgres classification wrong")
	}
}

func TestProbeFamily(t *testing.T) {
	for _, dt := range []string{"oracle", "oceanbase-oracle", "timesten", "dm", "goldendb-oracle"} {
		if fam, err := probeFamily(dt); err != nil || fam != "oracle" {
			t.Fatalf("probeFamily(%s) = %v, %v", dt, fam, err)
		}
	}
	for _, dt := range []string{"mysql", "mariadb", "oceanbase-mysql", "goldendb-mysql"} {
		if fam, err := probeFamily(dt); err != nil || fam != "mysql" {
			t.Fatalf("probeFamily(%s) = %v, %v", dt, fam, err)
		}
	}
	for _, dt := range []string{"postgres", "opengaussdb-mysql", "opengaussdb-oracle", "panweidb", "kingbase"} {
		if fam, err := probeFamily(dt); err != nil || fam != "postgres" {
			t.Fatalf("probeFamily(%s) = %v, %v", dt, fam, err)
		}
	}
	if _, err := probeFamily("duckdb"); err == nil {
		t.Fatalf("duckdb must be unsupported")
	}
}
