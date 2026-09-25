package cmd

import (
	"context"
	"database/sql"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// targetTypeFamily 委托 service（单一实现；import/migrate 的建表判定共用）。
func targetTypeFamily(dbType string) string { return service.TargetTypeFamily(dbType) }

// placeholderFamilyFor returns the bind placeholder override required by the
// connection, e.g. "?" for OceanBase Oracle tenants reached over MySQL wire.
func placeholderFamilyFor(cfg config.DBConfig) string {
	if dbconn.OceanBaseOracleUsesMySQLWire(cfg) {
		return "qmark"
	}
	// agent 通道经 JDBC：postgres 族的 $N 是服务端 PREPARE 语义，PG JDBC
	// 只认 ?，强制 qmark（native pq 系维持 $N）。mysql 族本就 ?；oracle 族
	// 由 sidecar BindRewriter 承接，不受影响。通道结论复用 dbconn 的
	// native-first 决策（auto 回退也会被正确解析），flag 覆盖在此生效。
	cfg2 := cfg
	if channelFlag != "" {
		cfg2.Channel = channelFlag
	}
	if ch, err := dbconn.ResolveChannel(cfg2); err == nil && ch == dbconn.ChannelAgent &&
		dbconn.Family(cfg.Type) == "postgres" {
		return "qmark"
	}
	return ""
}

// tableExists reports whether the target table already exists. wireQmark
// selects "?" binds for Oracle-family targets reached over the MySQL wire.
func tableExists(ctx context.Context, db *sql.DB, dbType, schema, table string, wireQmark bool) (bool, error) {
	var query string
	var args []any
	switch targetTypeFamily(dbType) {
	case "mysql":
		query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?"
		args = []any{schema, table}
	case "oracle":
		if wireQmark {
			query = "SELECT COUNT(*) FROM all_tables WHERE owner = UPPER(?) AND table_name = UPPER(?)"
		} else {
			query = "SELECT COUNT(*) FROM all_tables WHERE owner = UPPER(:1) AND table_name = UPPER(:2)"
		}
		args = []any{schema, table}
	case "sqlite3":
		query = "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?"
		args = []any{table}
	case "duckdb":
		query = "SELECT COUNT(*) FROM duckdb_tables() WHERE schema_name = ? AND table_name = ?"
		args = []any{schema, table}
	default:
		query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2"
		args = []any{schema, table}
	}
	var count int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// buildCreateTableViaDialect renders CREATE TABLE via the dialect system
// （类型转换/限定逻辑已迁至 service，import/migrate 建表路径共用）。
func buildCreateTableViaDialect(tbl *md.TableDef, cfg *config.Config) (string, error) {
	return service.BuildCreateTableViaDialect(tbl, cfg)
}
