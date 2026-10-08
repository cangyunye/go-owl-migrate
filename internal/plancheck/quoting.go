package plancheck

import (
	"database/sql"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
)

// openDB opens a plain connection for probing. Channel and agent-jars
// settings from the config are honored, so agent-channel dialects probe
// exactly like they would run. Package-level var purely as a test seam so
// unit tests can inject an in-process database; production never reassigns it.
var openDB = func(cfg config.DBConfig) (*sql.DB, error) { return dbconn.Open(cfg) }

// isMySQLFamily classifies a dialect for quoting. Names follow the registry's
// product families (see configbuild.PasswordSentinelFor for the same shape).
func isMySQLFamily(t string) bool {
	t = strings.ToLower(strings.TrimSpace(t))
	return t == "mysql" || t == "oceanbase" || t == "goldendb" || strings.HasSuffix(t, "-mysql")
}

func isOracleFamily(t string) bool {
	t = strings.ToLower(strings.TrimSpace(t))
	return t == "oracle" || t == "dm" || t == "timesten" || strings.HasSuffix(t, "-oracle")
}

func isQuoteable(t string) bool {
	return isMySQLFamily(t) || isOracleFamily(t) ||
		strings.Contains(t, "postgres") || strings.HasPrefix(t, "opengauss") ||
		strings.HasPrefix(t, "panwei") || t == "kingbase" ||
		t == "sqlite3" || t == "duckdb"
}

// quoteIdent quotes one identifier for the dialect family: backticks for the
// MySQL wire, double quotes elsewhere (Oracle/PG/SQLite/DuckDB). Names are
// used verbatim from the metadata — no case folding — so the probe hits the
// same object the export would.
func quoteIdent(dialect, name string) string {
	if isMySQLFamily(dialect) {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// quoteQualified quotes "schema.table"; embedded databases have no schema
// namespace, so their part is dropped.
func quoteQualified(dialect, schema, table string) string {
	if !isQuoteable(dialect) {
		return schema + "." + table // unknown dialect: unquoted, probe will fail loudly
	}
	if schema == "" || strings.EqualFold(dialect, "sqlite3") || strings.EqualFold(dialect, "duckdb") {
		return quoteIdent(dialect, table)
	}
	return quoteIdent(dialect, schema) + "." + quoteIdent(dialect, table)
}
