// Package dbconn centralizes database connection handling: driver selection,
// DSN post-processing and connection pool configuration.
package dbconn

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "github.com/helingjun/obconnector-go"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

// Family classifies a database type into its wire-protocol family.
// PanWeiDB always uses the PostgreSQL wire protocol regardless of SQL mode.
func Family(dbType string) string {
	t := strings.ToLower(strings.TrimSpace(dbType))
	t = registry.Normalize(t)
	switch {
	case t == "panweidb" || strings.HasPrefix(t, "panweidb-"):
		return "postgres"
	case t == "mysql" || t == "mariadb" || strings.HasSuffix(t, "-mysql"):
		return "mysql"
	case t == "oracle" || strings.HasSuffix(t, "-oracle"):
		return "oracle"
	case t == "sqlite3":
		return "sqlite3"
	case t == "duckdb":
		return "duckdb"
	default:
		return "postgres"
	}
}

var knownTypes = map[string]bool{
	"mysql": true, "mariadb": true, "postgres": true, "postgresql": true,
	"oracle": true, "sqlite3": true, "duckdb": true,
	"opengaussdb": true, "panweidb": true, "panweidb-mysql": true, "panweidb-oracle": true,
	"goldendb-mysql": true, "goldendb-oracle": true,
	"oceanbase-mysql": true, "oceanbase-oracle": true,
}

// driverName maps a database type to the registered database/sql driver.
func driverName(dbType string) (string, error) {
	t := registry.Normalize(strings.ToLower(strings.TrimSpace(dbType)))
	if !knownTypes[t] {
		return "", fmt.Errorf("unsupported database type: %s", dbType)
	}
	switch Family(t) {
	case "mysql":
		return "mysql", nil
	case "oracle":
		return "oracle", nil
	case "sqlite3":
		return "sqlite3", nil
	case "duckdb":
		return "duckdb", nil
	default:
		return "postgres", nil
	}
}

// Open opens a database connection for the configured type and applies pool
// settings. Oracle-family DSNs are post-processed for LOB-friendly streaming.
func Open(cfg config.DBConfig) (*sql.DB, error) {
	name := registry.Normalize(strings.ToLower(strings.TrimSpace(cfg.Type)))

	var (
		driver string
		dsn    string
		err    error
	)
	if name == "oceanbase-oracle" {
		driver, dsn, err = resolveOceanBaseOracleDriver(cfg)
		if err != nil {
			return nil, err
		}
	} else {
		driver, err = driverName(cfg.Type)
		if err != nil {
			return nil, err
		}
		dsn = cfg.DSN
		if Family(name) == "oracle" {
			dsn = InjectOracleParams(dsn)
		}
	}

	if Family(name) == "postgres" && strings.TrimSpace(cfg.Schema) != "" {
		dsn = InjectPGSearchPath(dsn, cfg.Schema)
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	ConfigurePool(db, cfg)

	if name == "oceanbase-mysql" {
		if verr := verifyOceanBaseCompatMode(db, cfg); verr != nil {
			db.Close()
			return nil, verr
		}
	}
	return db, nil
}

// InjectOracleParams sets go-ora streaming defaults required for memory-bounded
// export of tables containing LOBs:
//
//	PREFETCH_ROWS=25 — go-ora materializes every LOB of a prefetched batch, so a
//	large prefetch retains many BLOBs before rows are scanned.
//	LOB FETCH=POST  — lazy LOB loading keeps large LOB columns from dominating
//	row fetches.
//
// Values already present in the DSN (any spelling/case) are left untouched.
func InjectOracleParams(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return dsn
	}
	q := u.Query()
	has := func(names ...string) bool {
		for k := range q {
			up := strings.ToUpper(k)
			for _, n := range names {
				if up == n {
					return true
				}
			}
		}
		return false
	}
	if !has("PREFETCH_ROWS", "PREFETCH ROWS") {
		q.Set("PREFETCH_ROWS", "25")
	}
	if !has("LOB FETCH", "LOB_FETCH", "LOBFETCH") {
		q.Set("LOB FETCH", "POST")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// InjectPGSearchPath bakes search_path into a PostgreSQL-family DSN. A plain
// "SET search_path" only affects one pooled connection, so it must be part of
// the startup parameters. URL and keyword/value DSN formats are both handled;
// an existing search_path setting always wins.
func InjectPGSearchPath(dsn, schema string) string {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return dsn
	}
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return dsn
		}
		q := u.Query()
		for k := range q {
			if strings.EqualFold(k, "search_path") {
				return dsn
			}
		}
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		return u.String()
	}
	for _, kv := range strings.Fields(dsn) {
		if strings.HasPrefix(strings.ToLower(kv), "search_path=") {
			return dsn
		}
	}
	return dsn + " search_path=" + pgQuoteLiteral(schema)
}

func pgQuoteLiteral(s string) string {
	simple := true
	for _, r := range s {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			simple = false
			break
		}
	}
	if simple {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// OracleSupportsRowLimiting reports whether the Oracle server understands the
// 12c row limiting syntax (OFFSET ... FETCH NEXT). Oracle 11g requires ROWNUM
// wrappers instead.
func OracleSupportsRowLimiting(ctx context.Context, db *sql.DB) bool {
	var one int
	err := db.QueryRowContext(ctx, "SELECT 1 FROM DUAL OFFSET 0 ROWS FETCH NEXT 1 ROWS ONLY").Scan(&one)
	return err == nil && one == 1
}

// ConfigurePool applies connection pool settings from config with sensible
// defaults.
func ConfigurePool(db *sql.DB, cfg config.DBConfig) {
	maxOpen := cfg.Pool.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 10
	}
	db.SetMaxOpenConns(maxOpen)

	maxIdle := cfg.Pool.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 5
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	db.SetMaxIdleConns(maxIdle)

	if d, err := ParseDuration(cfg.Pool.ConnMaxLifetime, 30*time.Minute); err == nil {
		db.SetConnMaxLifetime(d)
	}
	if d, err := ParseDuration(cfg.Pool.ConnMaxIdleTime, 5*time.Minute); err == nil {
		db.SetConnMaxIdleTime(d)
	}
}

// ParseDuration parses a duration string, returning fallback if empty.
func ParseDuration(s string, fallback time.Duration) (time.Duration, error) {
	if s == "" {
		return fallback, nil
	}
	return time.ParseDuration(s)
}
