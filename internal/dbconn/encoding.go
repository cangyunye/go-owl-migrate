package dbconn

import (
	"fmt"
	"net/url"
	"strings"
)

// EncodingWarnHook, when set, receives charset warnings raised while opening
// a connection (e.g. a non-UTF-8 mysql DSN charset was overridden). cmd wires
// it to the logger so encoding decisions stay traceable.
var EncodingWarnHook func(dbType, message string)

// injectEncodingDefaults enforces the pipeline encoding invariant on native
// DSNs (stage-2 plan §2.5): in-process data is UTF-8, so the connection must
// speak a UTF-8 charset or the driver must convert (go-ora does; go-sql-driver
// does not — it hands raw connection-charset bytes to Go strings).
//
//	mysql wire DSNs   — charset must be utf8-family; missing leaves the driver
//	                    default (utf8mb4 handshake), non-UTF-8 overrides + warns
//	postgres family   — inject client_encoding=UTF8 when absent
//	oracle family     — untouched: go-ora/obconnector convert per server charset
func injectEncodingDefaults(dbType, driver, dsn string) string {
	switch driver {
	case "mysql":
		return ensureMySQLUTF8(dbType, dsn)
	case "postgres", "opengauss":
		return ensurePostgresClientEncoding(dbType, dsn)
	default:
		return dsn
	}
}

// ensureMySQLUTF8 validates the DSN charset parameter. go-sql-driver forwards
// connection-charset bytes verbatim into Go strings, so a non-UTF-8 charset
// would poison every exported value; override it rather than fail mid-migration.
func ensureMySQLUTF8(dbType, dsn string) string {
	prefix, query, found := strings.Cut(dsn, "?")
	if !found {
		return dsn // no params: driver default is the utf8mb4 handshake
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		warnEncoding(dbType, fmt.Sprintf("unparseable mysql dsn query %q left untouched", query))
		return dsn
	}
	charsets, ok := values["charset"]
	if !ok {
		return dsn
	}
	for _, cs := range charsets {
		for _, token := range strings.Split(cs, ",") {
			if !isMySQLUTF8Charset(token) {
				values.Set("charset", "utf8mb4")
				warnEncoding(dbType, fmt.Sprintf("mysql dsn charset %q is not UTF-8; overridden to utf8mb4 (non-UTF-8 bytes would reach Go strings verbatim)", cs))
				return prefix + "?" + values.Encode()
			}
		}
	}
	return dsn
}

// isMySQLUTF8Charset accepts utf8/utf8mb3/utf8mb4 tokens and collation
// spellings like "utf8mb4,utf8mb4_general_ci" (go-sql-driver charset lists).
func isMySQLUTF8Charset(cs string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(cs)), "utf8")
}

// ensurePostgresClientEncoding injects client_encoding=UTF8 (URL or libpq
// keyword form). lib/pq only sends client_encoding when the DSN carries it;
// without it a GBK server_encoding instance streams non-UTF-8 bytes.
func ensurePostgresClientEncoding(dbType, dsn string) string {
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return dsn
		}
		q := u.Query()
		if q.Get("client_encoding") != "" {
			return dsn
		}
		q.Set("client_encoding", "UTF8")
		u.RawQuery = q.Encode()
		return u.String()
	}
	for _, tok := range strings.Fields(dsn) {
		if strings.HasPrefix(strings.ToLower(tok), "client_encoding=") {
			return dsn
		}
	}
	return strings.TrimSpace(dsn) + " client_encoding=UTF8"
}

func warnEncoding(dbType, message string) {
	if EncodingWarnHook != nil {
		EncodingWarnHook(dbType, message)
	}
}
