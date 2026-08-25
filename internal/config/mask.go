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
	if dsn == "" {
		return dsn
	}
	if m := mysqlDSNPassword.FindStringSubmatch(dsn); m != nil && !strings.Contains(m[2], "/") {
		// m[2] containing "/" means we matched a scheme like "postgres://u",
		// not a native MySQL DSN; let url.Parse handle that below.
		return m[1] + ":******@" + dsn[len(m[0]):]
	}
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if _, has := u.User.Password(); has {
			// Splice the mask into the original string; re-serializing u
			// would percent-encode the asterisks.
			base := strings.Index(dsn, "://") + 3
			userinfo := dsn[base:]
			at := strings.LastIndex(userinfo, "@")
			colon := strings.Index(userinfo[:at], ":")
			if at > 0 && colon >= 0 {
				return dsn[:base+colon] + ":******" + dsn[base+at:]
			}
		}
	}
	return dsn
}
