//go:build e2e

package e2edev

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/helingjun/obconnector-go" // registers "oboracle"/"oceanbase"
	_ "github.com/lib/pq"
	_ "github.com/sijms/go-ora/v2"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
)

// connect 用仓库统一 dbconn.Open 建连；ping 失败 → skip（实库缺席不炸套件）。
func connect(t *testing.T, dbType, dsn string) *sql.DB {
	t.Helper()
	if strings.TrimSpace(dsn) == "" {
		t.Skipf("%s: no DSN configured", t.Name())
	}
	cfg := config.DBConfig{Type: dbType, DSN: dsn, ConnectTimeout: "15s"}
	db, err := dbconn.Open(cfg)
	if err != nil {
		t.Skipf("open %s: %v", dbType, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.Skipf("ping %s unreachable: %v", dbType, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// oracleBanner 返回 OB/Oracle 的 v$version banner 首行。
func oracleBanner(t *testing.T, db *sql.DB) string {
	t.Helper()
	var banner string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// OB Oracle 与原生 Oracle 都支持 v$version（OB 亦接受 ROWNUM）。
	if err := db.QueryRowContext(ctx, "SELECT banner FROM v$version WHERE ROWNUM <= 1").Scan(&banner); err != nil {
		t.Skipf("v$version: %v", err)
	}
	return banner
}

// execAll 依序执行 DDL/DML；每条失败即返回（引导失败必须暴露，不做静默吞错）。
func execAll(t *testing.T, db *sql.DB, stmts ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	for _, s := range stmts {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("exec %q: %v", truncateSQL(s), err)
		}
	}
}

func truncateSQL(s string) string {
	const n = 160
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
