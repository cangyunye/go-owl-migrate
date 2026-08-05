package generator

import (
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

// IsNumeric returns true if s represents a numeric value (integer or float).
func IsNumeric(s string) bool {
	if s == "" {
		return false
	}
	dots := 0
	start := 0
	if s[0] == '-' {
		start = 1
	}
	if start >= len(s) {
		return false
	}
	for i := start; i < len(s); i++ {
		if s[i] == '.' {
			dots++
			if dots > 1 {
				return false
			}
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// FormatSQLValue formats a CSV value as a SQL literal.
// nullMarker is the CSV null representation (e.g. "\\N").
func FormatSQLValue(v, nullMarker, dialect string) string {
	if v == nullMarker {
		return "NULL"
	}
	if IsNumeric(v) {
		return v
	}
	escaped := strings.ReplaceAll(v, "'", "''")
	if isOracleFamily(dialect) && escaped == "" {
		return "NULL"
	}
	return "'" + escaped + "'"
}

// GetQuoter returns an identifier quoting function for the given dialect.
func GetQuoter(dialect string, noQuote bool) func(string) string {
	if noQuote {
		return func(s string) string { return s }
	}
	if isMySQLFamily(dialect) {
		return func(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }
	}
	return func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
}

// isMySQLFamily reports whether the dialect uses backtick quoting.
func isMySQLFamily(dialect string) bool {
	t := strings.ToLower(strings.TrimSpace(dialect))
	if t == "mysql" || t == "mariadb" || t == "goldendb" || strings.HasSuffix(t, "-mysql") {
		return true
	}
	if d, err := registry.Get(t); err == nil {
		return strings.Contains(strings.ToLower(d.Name()), "mysql")
	}
	return false
}

// isOracleFamily reports whether the dialect treats empty string as NULL.
func isOracleFamily(dialect string) bool {
	t := strings.ToLower(strings.TrimSpace(dialect))
	return t == "oracle" || strings.HasSuffix(t, "-oracle")
}
