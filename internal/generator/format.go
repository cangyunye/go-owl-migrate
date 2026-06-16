package generator

import "strings"

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
	if dialect == "oracle" && escaped == "" {
		return "NULL"
	}
	return "'" + escaped + "'"
}

// GetQuoter returns an identifier quoting function for the given dialect.
func GetQuoter(dialect string, noQuote bool) func(string) string {
	if noQuote {
		return func(s string) string { return s }
	}
	switch dialect {
	case "mysql":
		return func(s string) string { return "`" + s + "`" }
	default:
		return func(s string) string { return `"` + s + `"` }
	}
}
