//go:build ob

package dbconn

import (
	"context"
	"net/url"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	obconnector "github.com/helingjun/obconnector-go"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

func tContext() context.Context { return context.Background() }

type errString string

func (e errString) Error() string { return string(e) }

func TestResolveOceanBaseOracleDriver(t *testing.T) {
	t.Run("oracle url uses go-ora", func(t *testing.T) {
		driver, dsn, err := resolveOceanBaseOracleDriver(config.DBConfig{
			Type: "oceanbase-oracle",
			DSN:  "oracle://user:pw@obproxy:2883/ORCL",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if driver != "oracle" {
			t.Errorf("driver = %q, want oracle", driver)
		}
		u, _ := url.Parse(dsn)
		if u.Query().Get("LOB FETCH") != "POST" {
			t.Errorf("go-ora DSN should get LOB FETCH injected: %s", dsn)
		}
	})

	t.Run("mysql-wire url uses oboracle", func(t *testing.T) {
		driver, dsn, err := resolveOceanBaseOracleDriver(config.DBConfig{
			Type: "oceanbase-oracle",
			DSN:  "oceanbase-oracle://sys:pw@127.0.0.1:2881/oracle_tenant",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if driver != "oboracle" {
			t.Errorf("driver = %q, want oboracle", driver)
		}
		u, _ := url.Parse(dsn)
		if u.Scheme != "oboracle" {
			t.Errorf("scheme = %q, want oboracle", u.Scheme)
		}
		if u.Query().Get("preset") != "oboracle" {
			t.Errorf("preset missing: %s", dsn)
		}
	})

	t.Run("mysql-style dsn rewritten to oboracle", func(t *testing.T) {
		driver, dsn, err := resolveOceanBaseOracleDriver(config.DBConfig{
			Type: "oceanbase-oracle",
			DSN:  "mysql://sys:pw@127.0.0.1:2881/oracle_tenant",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if driver != "oboracle" || !strings.HasPrefix(dsn, "oboracle://") {
			t.Errorf("got driver=%q dsn=%q", driver, dsn)
		}
	})

	t.Run("invalid dsn errors", func(t *testing.T) {
		if _, _, err := resolveOceanBaseOracleDriver(config.DBConfig{Type: "oceanbase-oracle", DSN: "not a url"}); err == nil {
			t.Error("expected error for malformed DSN")
		}
	})

	t.Run("cluster folded into username on OBProxy port", func(t *testing.T) {
		driver, dsn, err := resolveOceanBaseOracleDriver(config.DBConfig{
			Type: "oceanbase-oracle",
			DSN:  "oceanbase-oracle://sys@oracle_tenant:pw@127.0.0.1:2883/oracle_tenant?cluster=obcluster",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if driver != "oboracle" {
			t.Errorf("driver = %q, want oboracle", driver)
		}
		// obconnector-go parses userinfo with QueryUnescape, so the generated
		// DSN must round-trip to user `sys@oracle_tenant#obcluster`.
		cfg, err := obconnector.ParseDSN(dsn)
		if err != nil {
			t.Fatalf("obconnector parse: %v", err)
		}
		if cfg.User != "sys@oracle_tenant#obcluster" {
			t.Errorf("user = %q, want sys@oracle_tenant#obcluster (dsn %s)", cfg.User, dsn)
		}
		if cfg.Password != "pw" {
			t.Errorf("password = %q, want pw", cfg.Password)
		}
		if strings.Contains(dsn, "cluster=") {
			t.Errorf("cluster query param should be consumed, got: %s", dsn)
		}
	})

	t.Run("cluster dropped on direct OBServer port", func(t *testing.T) {
		// 2881 = direct OBServer: no cluster needed; folding one in may be
		// rejected by the server, so the query param is dropped.
		driver, dsn, err := resolveOceanBaseOracleDriver(config.DBConfig{
			Type: "oceanbase-oracle",
			DSN:  "oceanbase-oracle://sys@oracle_tenant:pw@127.0.0.1:2881/oracle_tenant?cluster=obcluster",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if driver != "oboracle" {
			t.Errorf("driver = %q, want oboracle", driver)
		}
		cfg, err := obconnector.ParseDSN(dsn)
		if err != nil {
			t.Fatalf("obconnector parse: %v", err)
		}
		if cfg.User != "sys@oracle_tenant" {
			t.Errorf("user = %q, want sys@oracle_tenant (dsn %s)", cfg.User, dsn)
		}
		if strings.Contains(dsn, "cluster=") || strings.Contains(cfg.User, "#") {
			t.Errorf("cluster must be dropped on 2881: %s", dsn)
		}
	})

	t.Run("cluster folded on TNS path", func(t *testing.T) {
		driver, dsn, err := resolveOceanBaseOracleDriver(config.DBConfig{
			Type: "oceanbase-oracle",
			DSN:  "oracle://sys@oracle_tenant:pw@obproxy:2883/ORCL?cluster=obcluster",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if driver != "oracle" {
			t.Errorf("driver = %q, want oracle", driver)
		}
		u, _ := url.Parse(dsn)
		if u.User.Username() != "sys@oracle_tenant#obcluster" {
			t.Errorf("username = %q, want sys@oracle_tenant#obcluster (dsn %s)", u.User.Username(), dsn)
		}
		if u.Query().Get("LOB FETCH") != "POST" {
			t.Errorf("go-ora DSN should keep LOB FETCH injection: %s", dsn)
		}
	})

	t.Run("existing #cluster in username wins over query", func(t *testing.T) {
		_, dsn, err := resolveOceanBaseOracleDriver(config.DBConfig{
			Type: "oceanbase-oracle",
			DSN:  "oboracle://sys@oracle_tenant%23c1:pw@127.0.0.1:2883/db?cluster=c2",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfg, err := obconnector.ParseDSN(dsn)
		if err != nil {
			t.Fatalf("obconnector parse: %v", err)
		}
		if cfg.User != "sys@oracle_tenant#c1" {
			t.Errorf("user = %q, want sys@oracle_tenant#c1", cfg.User)
		}
	})
}

func TestOceanBaseOracleUsesMySQLWire(t *testing.T) {
	tests := []struct {
		cfg  config.DBConfig
		want bool
	}{
		{config.DBConfig{Type: "oceanbase-oracle", DSN: "oceanbase-oracle://u:p@h:2881/db"}, true},
		{config.DBConfig{Type: "oceanbase-oracle", DSN: "oracle://u:p@h:2883/svc"}, false},
		{config.DBConfig{Type: "oceanbase-mysql", DSN: "mysql://u:p@h:2881/db"}, false},
		{config.DBConfig{Type: "oracle", DSN: "oracle://u:p@h:1521/svc"}, false},
	}
	for _, tt := range tests {
		if got := OceanBaseOracleUsesMySQLWire(tt.cfg); got != tt.want {
			t.Errorf("OceanBaseOracleUsesMySQLWire(%+v) = %v, want %v", tt.cfg, got, tt.want)
		}
	}
}

func TestProbeOceanBaseCompatMode(t *testing.T) {
	t.Run("oracle tenant reports oracle", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		defer db.Close()
		mock.ExpectQuery("SHOW VARIABLES LIKE").WillReturnRows(
			sqlmock.NewRows([]string{"Variable_name", "Value"}).
				AddRow("ob_compatibility_mode", "1"))
		if got := ProbeOceanBaseCompatMode(tContext(), db); got != "oracle" {
			t.Errorf("mode = %q, want oracle", got)
		}
	})

	t.Run("mysql tenant reports mysql", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		defer db.Close()
		mock.ExpectQuery("SHOW VARIABLES LIKE").WillReturnRows(
			sqlmock.NewRows([]string{"Variable_name", "Value"}).
				AddRow("ob_compatibility_mode", "0"))
		if got := ProbeOceanBaseCompatMode(tContext(), db); got != "mysql" {
			t.Errorf("mode = %q, want mysql", got)
		}
	})

	t.Run("non-oceanbase reports empty", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		defer db.Close()
		mock.ExpectQuery("SHOW VARIABLES LIKE").WillReturnRows(
			sqlmock.NewRows([]string{"Variable_name", "Value"}))
		if got := ProbeOceanBaseCompatMode(tContext(), db); got != "" {
			t.Errorf("mode = %q, want empty", got)
		}
	})
}

func TestVerifyOceanBaseCompatMode_Mismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	mock.ExpectPing()
	mock.ExpectQuery("SHOW VARIABLES LIKE").WillReturnRows(
		sqlmock.NewRows([]string{"Variable_name", "Value"}).
			AddRow("ob_compatibility_mode", "1"))

	err = verifyOceanBaseCompatMode(db, config.DBConfig{Type: "oceanbase"})
	if err == nil {
		t.Fatal("expected error for Oracle tenant with mysql type")
	}
	if !strings.Contains(err.Error(), "oceanbase-oracle") {
		t.Errorf("error should guide to oceanbase-oracle: %v", err)
	}
}

func TestVerifyOceanBaseCompatMode_Match(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	mock.ExpectPing()
	mock.ExpectQuery("SHOW VARIABLES LIKE").WillReturnRows(
		sqlmock.NewRows([]string{"Variable_name", "Value"}).
			AddRow("ob_compatibility_mode", "0"))

	if err := verifyOceanBaseCompatMode(db, config.DBConfig{Type: "oceanbase"}); err != nil {
		t.Errorf("mysql tenant should pass: %v", err)
	}
}

func TestAnnotateOceanBaseError(t *testing.T) {
	err := annotateOceanBaseError(errString("OceanBase error 1235: Oracle tenant for current client driver is not supported"))
	if err == nil || !strings.Contains(err.Error(), "oceanbase-oracle") {
		t.Errorf("expected hint in error, got: %v", err)
	}
}
