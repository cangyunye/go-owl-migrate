package dbconn

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

// MetadataSourceType returns the database type string to use for metadata
// extraction. OceanBase Oracle tenants reached over the MySQL wire need the
// "?"-placeholder querier instead of the default ":N" Oracle one.
func MetadataSourceType(cfg config.DBConfig) string {
	if OceanBaseOracleUsesMySQLWire(cfg) {
		return "oceanbase-oracle-wire"
	}
	return cfg.Type
}

// OceanBaseOracleUsesMySQLWire reports whether an oceanbase-oracle connection
// goes through the OceanBase MySQL wire protocol (obconnector-go) instead of
// Oracle TNS (go-ora). The MySQL wire uses "?" placeholders even though SQL
// syntax stays Oracle-style.
func OceanBaseOracleUsesMySQLWire(cfg config.DBConfig) bool {
	if registry.Normalize(strings.ToLower(strings.TrimSpace(cfg.Type))) != "oceanbase-oracle" {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(cfg.DSN))
	if err != nil {
		return false
	}
	return u.Scheme != "oracle" && u.Scheme != ""
}

// resolveOceanBaseOracleDriver picks the driver and rewrites the DSN for an
// oceanbase-oracle connection: oracle:// DSNs use go-ora (TNS listener, e.g.
// OBProxy Oracle endpoint); any other scheme uses obconnector-go over the
// OceanBase MySQL wire protocol.
func resolveOceanBaseOracleDriver(cfg config.DBConfig) (string, string, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", fmt.Errorf("target DSN must be a URL like oceanbase-oracle://user:pass@host:2881/db or oracle://user:pass@host:port/service: %q", cfg.DSN)
	}
	if u.Scheme == "oracle" {
		return "oracle", InjectOracleParams(dsn), nil
	}

	if u.Scheme != "oboracle" {
		u.Scheme = "oboracle"
	}
	q := u.Query()
	if q.Get("preset") == "" {
		q.Set("preset", "oboracle")
	}
	if q.Get("timeout") == "" {
		if d, perr := ParseDuration(cfg.ConnectTimeout, 30*time.Second); perr == nil && d > 0 {
			q.Set("timeout", d.String())
		}
	}
	u.RawQuery = q.Encode()
	return "oboracle", u.String(), nil
}

// ProbeOceanBaseCompatMode detects the tenant compatibility mode through a
// MySQL-wire connection. Returns "mysql", "oracle", or "" when the server is
// not OceanBase or the mode cannot be determined.
func ProbeOceanBaseCompatMode(ctx context.Context, db *sql.DB) string {
	rows, err := db.QueryContext(ctx, "SHOW VARIABLES LIKE 'ob_compatibility_mode'")
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var name, value string
		if rows.Scan(&name, &value) != nil {
			continue
		}
		if !strings.EqualFold(name, "ob_compatibility_mode") {
			continue
		}
		switch strings.TrimSpace(value) {
		case "0":
			return "mysql"
		case "1":
			return "oracle"
		default:
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

// verifyOceanBaseCompatMode enforces the configured tenant mode against the
// live connection.
func verifyOceanBaseCompatMode(db *sql.DB, cfg config.DBConfig) error {
	timeout, _ := ParseDuration(cfg.ConnectTimeout, 30*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping OceanBase: %w", annotateOceanBaseError(err))
	}
	detected := ProbeOceanBaseCompatMode(ctx, db)
	declared := strings.ToLower(strings.TrimSpace(cfg.CompatMode))

	switch declared {
	case "", "mysql":
		if detected == "oracle" {
			return fmt.Errorf("OceanBase tenant runs in Oracle compatibility mode but type %q connects in MySQL mode; "+
				"change the type to \"oceanbase-oracle\" (a MySQL-wire DSN uses the oboracle driver, an oracle:// DSN uses TNS)", cfg.Type)
		}
	case "oracle":
		return fmt.Errorf("compat_mode oracle requires type \"oceanbase-oracle\", got %q", cfg.Type)
	}
	return nil
}

// annotateOceanBaseError adds actionable hints to common OceanBase connection
// failures.
func annotateOceanBaseError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "1235") && strings.Contains(msg, "oracle") {
		return fmt.Errorf("%w (the tenant is Oracle-compatible; use type \"oceanbase-oracle\")", err)
	}
	return err
}
