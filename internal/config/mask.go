package config

import (
	"net/url"
	"regexp"
	"strings"
)

var mysqlDSNPassword = regexp.MustCompile(`^([^:@/]+):([^@]*)@`)

// MaskDSN replaces the password embedded in a DSN with asterisks. URL-form
// DSNs are parsed; MySQL native-form DSNs (user:pass@tcp(host)/db) are
// handled by pattern. Unrecognized forms are returned unchanged so masking
// never destroys an opaque DSN.
func MaskDSN(dsn string) string {
	return ReplaceDSNPassword(dsn, "******")
}

// ReplaceDSNPassword swaps the password embedded in a DSN for an arbitrary
// replacement (mask for display, credential sentinel for the plan protocol).
// Unrecognized DSN shapes are returned unchanged.
func ReplaceDSNPassword(dsn, repl string) string {
	if dsn == "" {
		return dsn
	}
	if m := mysqlDSNPassword.FindStringSubmatch(dsn); m != nil && !strings.HasPrefix(m[2], "//") {
		// m[2] starting with "//" means we matched a scheme like
		// "postgres://u", not a native MySQL DSN; let url.Parse handle that below.
		return m[1] + ":" + repl + "@" + dsn[len(m[0]):]
	}
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if _, has := u.User.Password(); has {
			// Splice the replacement into the original string; re-serializing u
			// would percent-encode it.
			base := strings.Index(dsn, "://") + 3
			userinfo := dsn[base:]
			at := strings.LastIndex(userinfo, "@")
			colon := strings.Index(userinfo[:at], ":")
			if at > 0 && colon >= 0 {
				return dsn[:base+colon] + ":" + repl + dsn[base+at:]
			}
		}
	}
	return dsn
}
