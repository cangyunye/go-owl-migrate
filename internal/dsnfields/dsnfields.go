// Package dsnfields decomposes database DSNs into structured connection fields
// (username, password, host, port, database, extra parameters) and rebuilds a
// DSN from those fields. It never sends a password to the browser: callers use
// Decompose to fill a form, then Build (with a blank password) to keep the
// already-stored secret on edit.
package dsnfields

import (
	"fmt"
	"net/url"
	"strings"
)

// Fields is the structured, password-bearing form of a DSN. The password is
// intentionally omitted by API read endpoints; only Build sees it.
type Fields struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	Database string `json:"database"`
	Extra    string `json:"extra"`
}

// familyFor maps a database type to a DSN grammar. It is kept separate from
// dbconn.Family so the grammar matches the documented DSN examples rather than
// the wire-protocol family.
func familyFor(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch {
	case t == "sqlite3" || t == "duckdb":
		return "file"
	case t == "oracle" || t == "goldendb-oracle" || t == "oceanbase-oracle":
		return "oracle"
	case t == "mysql" || t == "goldendb" || t == "goldendb-mysql" || t == "oceanbase" || t == "oceanbase-mysql":
		return "mysql"
	default:
		return "postgres"
	}
}

// Decompose parses a DSN into structured fields. Values already URL-encoded in
// the DSN are returned un-escaped; extra parameters are normalized to URL query
// form.
func Decompose(dbType, dsn string) (*Fields, error) {
	family := familyFor(dbType)
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return &Fields{}, nil
	}
	switch family {
	case "file":
		return &Fields{Database: dsn}, nil
	case "mysql":
		return decomposeMySQL(dsn)
	case "oracle":
		return decomposeURL(dsn)
	default:
		return decomposePostgres(dsn)
	}
}

// Build reconstructs a DSN for dbType from structured fields. When Password is
// blank it is recovered from oldDSN, so an edit that leaves the password field
// empty keeps the previously-stored secret.
func Build(dbType string, f Fields, oldDSN string) (string, error) {
	family := familyFor(dbType)
	if f.Password == "" && oldDSN != "" {
		if of, err := Decompose(dbType, oldDSN); err == nil && of != nil && of.Password != "" {
			f.Password = of.Password
		}
	}
	switch family {
	case "file":
		if f.Database == "" {
			return "", fmt.Errorf("database path is required")
		}
		return f.Database, nil
	case "mysql":
		return buildMySQL(f)
	case "oracle":
		return buildOracle(dbType, f)
	default:
		return buildPostgres(f)
	}
}

func userinfo(u, p string) string {
	if p == "" {
		return u
	}
	return u + ":" + p
}

func buildMySQL(f Fields) (string, error) {
	if f.Username == "" || f.Host == "" || f.Database == "" {
		return "", fmt.Errorf("username, host and database are required")
	}
	s := userinfo(f.Username, f.Password) + "@tcp(" + f.Host + ":" + f.Port + ")/" + f.Database
	if f.Extra != "" {
		s += "?" + f.Extra
	}
	return s, nil
}

func buildOracle(dbType string, f Fields) (string, error) {
	if f.Username == "" || f.Host == "" || f.Database == "" {
		return "", fmt.Errorf("username, host and database (service name) are required")
	}
	scheme := "oracle"
	if strings.EqualFold(strings.TrimSpace(dbType), "oceanbase-oracle") {
		scheme = "oceanbase-oracle"
	}
	s := scheme + "://" + userinfo(f.Username, f.Password) + "@" + f.Host + ":" + f.Port + "/" + f.Database
	if f.Extra != "" {
		s += "?" + f.Extra
	}
	return s, nil
}

func buildPostgres(f Fields) (string, error) {
	if f.Host == "" || f.Database == "" {
		return "", fmt.Errorf("host and database are required")
	}
	s := "postgres://" + userinfo(f.Username, f.Password) + "@" + f.Host + ":" + f.Port + "/" + f.Database
	if f.Extra != "" {
		s += "?" + f.Extra
	}
	return s, nil
}

// splitUserPass splits "user:pass" on the first colon. A username may itself
// contain '@' (e.g. OceanBase "user@tenant"), which is unaffected here.
func splitUserPass(s string) (user, pass string) {
	if i := strings.Index(s, ":"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func decomposeMySQL(dsn string) (*Fields, error) {
	if strings.Contains(dsn, "://") {
		return decomposeURL(dsn)
	}
	at := strings.LastIndex(dsn, "@")
	if at < 0 {
		return &Fields{}, nil
	}
	user, pass := splitUserPass(dsn[:at])
	return decomposeMySQLRest(dsn[at+1:], user, pass)
}

func decomposeMySQLRest(rest, user, pass string) (*Fields, error) {
	f := &Fields{Username: user, Password: pass}
	extra := ""
	if i := strings.Index(rest, "?"); i >= 0 {
		extra = rest[i+1:]
		rest = rest[:i]
	}
	db := ""
	if i := strings.Index(rest, "/"); i >= 0 {
		db = rest[i+1:]
		rest = rest[:i]
	}
	f.Database = db
	inner := rest
	if strings.HasPrefix(rest, "tcp(") && strings.HasSuffix(rest, ")") {
		inner = rest[len("tcp(") : len(rest)-1]
	}
	if strings.HasPrefix(inner, "unix(") && strings.HasSuffix(inner, ")") {
		f.Host = inner
		f.Database = db
		return f, nil
	}
	host, port := "", ""
	if i := strings.LastIndex(inner, ":"); i >= 0 {
		host, port = inner[:i], inner[i+1:]
	} else {
		host = inner
	}
	f.Host, f.Port = host, port
	f.Extra = extra
	return f, nil
}

// decomposeURL parses "scheme://user[:pass]@host:port/db[?query]". It is shared
// by the URL-form mysql/oracle/postgres DSNs and correctly splits an OceanBase
// username like "sys@oracle_tenant" (net/url splits the authority at the last
// '@').
func decomposeURL(dsn string) (*Fields, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid DSN: %w", err)
	}
	f := &Fields{}
	if u.User != nil {
		f.Username = u.User.Username()
		if p, ok := u.User.Password(); ok {
			f.Password = p
		}
	}
	f.Host = u.Hostname()
	f.Port = u.Port()
	f.Database = strings.TrimPrefix(u.Path, "/")
	f.Extra = u.RawQuery
	return f, nil
}

// decomposePostgres handles both the URL and libpq keyword ("host=h port=5432
// user=u dbname=d") forms; the latter is normalized to a URL query in Extra so
// Build can emit a canonical postgres:// DSN.
func decomposePostgres(dsn string) (*Fields, error) {
	if strings.Contains(dsn, "://") {
		return decomposeURL(dsn)
	}
	next := make(url.Values)
	f := &Fields{}
	for _, tok := range strings.Fields(dsn) {
		if !strings.Contains(tok, "=") {
			continue
		}
		k, v := tok[:strings.Index(tok, "=")], tok[strings.Index(tok, "=")+1:]
		v = strings.Trim(v, "'\"")
		switch strings.ToLower(k) {
		case "user":
			f.Username = v
		case "password":
			f.Password = v
		case "host":
			f.Host = v
		case "port":
			f.Port = v
		case "dbname", "database":
			f.Database = v
		default:
			next.Set(k, v)
		}
	}
	f.Extra = next.Encode()
	return f, nil
}
