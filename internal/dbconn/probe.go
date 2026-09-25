package dbconn

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ServerEncoding reports the server-side character set discovered by
// ProbeServerEncoding.
type ServerEncoding struct {
	Charset string // raw server value: AL32UTF8, UTF8, ZHS16GBK, utf8mb4, UTF8…
	Unicode bool   // true when the charset round-trips UTF-8 losslessly
}

// probeFamily maps a database type to the probe dialect. It mirrors Family
// plus the owljdbc catalog types (dm/timesten/goldendb-oracle are
// oracle-dialect servers even though Family defaults them to postgres).
func probeFamily(dbType string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(dbType))
	switch t {
	case "oracle", "oceanbase-oracle", "timesten", "dm", "goldendb-oracle":
		return "oracle", nil
	case "mysql", "mariadb", "oceanbase-mysql", "goldendb-mysql":
		return "mysql", nil
	case "postgres", "opengaussdb", "opengaussdb-mysql", "opengaussdb-oracle",
		"panweidb", "panweidb-mysql", "panweidb-oracle", "kingbase":
		return "postgres", nil
	default:
		return "", fmt.Errorf("server encoding probe unsupported for database type %q", dbType)
	}
}

// ProbeServerEncoding issues a single round-trip query and interprets the
// server character set for the dialect. The probe is plain SQL text, so it
// works identically over native and owljdbc (agent) connections — that is the
// basis of the charset matrix (stage-2 plan §4.4) and of the UTF8-tenant
// confirmation that stands in for the deferred OB GBK tenant.
func ProbeServerEncoding(ctx context.Context, db *sql.DB, dbType string) (ServerEncoding, error) {
	family, err := probeFamily(dbType)
	if err != nil {
		return ServerEncoding{}, err
	}
	var charset string
	switch family {
	case "oracle":
		const q = "SELECT VALUE FROM NLS_DATABASE_PARAMETERS WHERE PARAMETER = 'NLS_CHARACTERSET'"
		if err := db.QueryRowContext(ctx, q).Scan(&charset); err != nil {
			return ServerEncoding{}, fmt.Errorf("probe oracle charset: %w", err)
		}
		return ServerEncoding{Charset: charset, Unicode: unicodeOracleCharset(charset)}, nil
	case "mysql":
		if err := db.QueryRowContext(ctx, "SELECT @@character_set_server").Scan(&charset); err != nil {
			return ServerEncoding{}, fmt.Errorf("probe mysql charset: %w", err)
		}
		return ServerEncoding{Charset: charset, Unicode: unicodeMySQLCharset(charset)}, nil
	default: // postgres
		if err := db.QueryRowContext(ctx, "SHOW server_encoding").Scan(&charset); err != nil {
			return ServerEncoding{}, fmt.Errorf("probe postgres charset: %w", err)
		}
		return ServerEncoding{Charset: charset, Unicode: unicodePostgresEncoding(charset)}, nil
	}
}

// unicodeOracleCharset: AL32UTF8 and UTF8 are the Oracle database charsets
// that map losslessly to UTF-8; ZHS16GBK/US7ASCII and friends do not.
func unicodeOracleCharset(charset string) bool {
	cs := strings.ToUpper(strings.TrimSpace(charset))
	return cs == "AL32UTF8" || cs == "UTF8"
}

// unicodeMySQLCharset: utf8/utf8mb3/utf8mb4 (with any collation suffix).
func unicodeMySQLCharset(charset string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(charset)), "utf8")
}

// unicodePostgresEncoding: UTF8 (and its EUC-equivalent alias UNICODE).
func unicodePostgresEncoding(charset string) bool {
	cs := strings.ToUpper(strings.TrimSpace(charset))
	return cs == "UTF8" || cs == "UNICODE"
}
