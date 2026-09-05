//go:build e2e
// +build e2e

// Extension smoke tests: the agent channel against standalone MySQL and
// PostgreSQL (beyond the OceanBase dual-tenant parity scope). Proves the
// channel is family-generic — jar + family + URL, no channel changes:
//
//	mysql:    ? binds pass through (caching_sha2 needs allowPublicKeyRetrieval)
//	postgres: $N binds must pass through unmangled (family "postgres" is not
//	          rewritten on either side; only oracle :N → ? is rewritten)
//
// Run:
//
//	go test -tags e2e ./internal/e2eagent/ -run TestE2E_Ext -v
//
// Credentials come from the OS environment or testdata/db/.local-dev.env
// (OS env wins). Driver jars default to the repo root (gitignored; the
// caller downloads them) and can be overridden via OWL_E2E_MYSQL_JAR /
// OWL_E2E_PG_JAR. Missing jars/DSNs/unreachable DBs skip cleanly.
//
// Helpers are ext-prefixed and self-contained (parity_e2e_test.go carries an
// extra `ob` build tag, so shared symbols there are invisible under plain
// `-tags e2e` — and re-declaring its names would collide under `-tags "e2e ob"`).
package e2eagent

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/agent"
)

// ── config assembly (OB-free: driver jars for the extension targets) ──

// extJar resolves a driver jar: env override > repo-root default; skip if absent.
func extJar(t *testing.T, envKey, repoRootDefault string) string {
	t.Helper()
	jar := extLookup(nil, envKey)
	if jar == "" {
		jar = filepath.Join("..", "..", repoRootDefault)
	}
	if _, err := os.Stat(jar); err != nil {
		t.Skipf("%s missing (%s)", envKey, jar)
	}
	return jar
}

// extAgentJar resolves the channel sidecar jar (same default as the parity harness).
func extAgentJar(t *testing.T) string {
	t.Helper()
	agentJar := extLookup(nil, "OWL_AGENT_JAR")
	if agentJar == "" {
		agentJar = filepath.Join("..", "..", "jvm", "owl-agent", "owl-agent.jar")
	}
	if _, err := os.Stat(agentJar); err != nil {
		t.Skipf("agent jar missing (%s): run bash jvm/owl-agent/build.sh", agentJar)
	}
	return agentJar
}

// extCred holds credentials parsed from a dev-env DSN (same shape as the
// parity harness's jdbcCred; renamed for tag-combination compatibility).
type extCred struct {
	User     string
	Password string
	Host     string
	Port     int
}

// parsePostgresURLDSN parses
//
//	postgres://superme:AA%401122%23@host:5432/test?connect_timeout=5&sslmode=disable
//
// The password is percent-encoded; url.Userinfo only exposes the decoded
// form, so the RAW userinfo slice is extracted and PathUnescaped here (a
// literal '%40' inside the cleartext password would survive a double
// unescape of the decoded value).
func parsePostgresURLDSN(t *testing.T, dsn string) extCred {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse pg dsn: %v", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		t.Fatalf("unexpected pg scheme %q", u.Scheme)
	}
	cred := extCred{}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("bad pg port: %v", err)
	}
	cred.Port = port
	cred.Host = u.Hostname()
	if u.User == nil {
		t.Fatalf("pg dsn missing userinfo")
	}
	cred.User = u.User.Username()
	rest := strings.TrimPrefix(dsn, u.Scheme+"://")
	at := strings.LastIndex(rest, "@") // 真实分隔符：口令里的 @ 已编码为 %40
	if at < 0 {
		t.Fatalf("pg dsn missing @ separator")
	}
	userinfo := rest[:at]
	colon := strings.Index(userinfo, ":")
	if colon < 0 {
		t.Fatalf("pg dsn missing password")
	}
	pass, err := url.PathUnescape(userinfo[colon+1:])
	if err != nil {
		t.Fatalf("unescape pg password: %v", err)
	}
	cred.Password = pass
	return cred
}

// pgJDBCURL builds the JDBC URL from the parsed DSN, keeping the DSN's own
// query params (connect_timeout / sslmode=disable) when present.
func pgJDBCURL(cred extCred, dbname string, raw *url.URL) string {
	q := raw.RawQuery
	if q == "" {
		q = "sslmode=disable"
	}
	return fmt.Sprintf("jdbc:postgresql://%s:%d/%s?%s", cred.Host, cred.Port, dbname, q)
}

func extAgentCfg(t *testing.T, family, driverClass, jdbcURL string, driverJar string, user, pass string) agent.Config {
	t.Helper()
	return agent.Config{
		DriverClass: driverClass,
		URL:         jdbcURL,
		User:        user,
		Password:    pass,
		Family:      family,
		Classpath:   []string{driverJar},
		AgentJar:    extAgentJar(t),
	}
}

// ── 1) standalone MySQL smoke ──

func TestE2E_Ext_MySQLSmoke(t *testing.T) {
	env := extDevEnv(t)
	dsn := extLookup(env, "OWL_E2E_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_MYSQL_DSN (or testdata/db/.local-dev.env)")
	}
	cred := extParseMysqlWireDSN(t, dsn)
	jar := extJar(t, "OWL_E2E_MYSQL_JAR", "mysql-connector-j-8.0.33.jar")
	cfg := extAgentCfg(t, "mysql", "com.mysql.cj.jdbc.Driver",
		fmt.Sprintf("jdbc:mysql://%s:%d/?useSSL=false&allowPublicKeyRetrieval=true", cred.Host, cred.Port),
		jar, cred.User, cred.Password)
	db := extOpenAgentDB(t, cfg)
	ctx := context.Background()

	// server version round-trips and is non-empty
	var v string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&v); err != nil {
		t.Fatalf("SELECT VERSION(): %v", err)
	}
	if strings.TrimSpace(v) == "" {
		t.Fatalf("VERSION() empty")
	}
	t.Logf("mysql server version: %s", v)

	// comma literal → JSON parser's string-protection path
	if err := db.QueryRowContext(ctx, "SELECT 'a,b'").Scan(&v); err != nil {
		t.Fatalf("comma query: %v", err)
	}
	if v != "a,b" {
		t.Fatalf("comma got %q", v)
	}

	// bound multi-row result set (streaming across row boundaries)
	rows, err := db.QueryContext(ctx, "SELECT ? UNION ALL SELECT ?", int64(1), int64(2))
	if err != nil {
		t.Fatalf("multi-row bind: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var got int64
		if err := rows.Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != int64(n+1) {
			t.Fatalf("row %d = %d", n, got)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}

	// information_schema unbound (DATABASE() inline; no default schema → 0..3 rows)
	info, err := db.QueryContext(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() LIMIT 3")
	if err != nil {
		t.Fatalf("information_schema query: %v", err)
	}
	defer info.Close()
	for info.Next() {
	}
	if err := info.Err(); err != nil {
		t.Fatalf("information_schema rows: %v", err)
	}

	// information_schema bound: count tables in the session schema; with no
	// default schema selected, fall back to the always-present "mysql" schema
	// so the count assertion stays meaningful.
	var schemaVal any
	if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schemaVal); err != nil {
		t.Fatal(err)
	}
	schema, _ := schemaVal.(string) // no default schema → DATABASE() is NULL
	if strings.TrimSpace(schema) == "" {
		schema = "mysql"
	}
	var cnt int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ?", schema).Scan(&cnt); err != nil {
		t.Fatalf("information_schema bind: %v", err)
	}
	if cnt <= 0 {
		t.Fatalf("table count for schema %q = %d, want > 0", schema, cnt)
	}
}

// ── 2) standalone PostgreSQL smoke ──

func TestE2E_Ext_PostgresSmoke(t *testing.T) {
	env := extDevEnv(t)
	dsn := extLookup(env, "OWL_E2E_PG_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_PG_DSN (or testdata/db/.local-dev.env)")
	}
	raw, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse pg dsn: %v", err)
	}
	cred := parsePostgresURLDSN(t, dsn)
	jar := extJar(t, "OWL_E2E_PG_JAR", "postgresql-42.7.13.jar")
	cfg := extAgentCfg(t, "postgres", "org.postgresql.Driver",
		pgJDBCURL(cred, strings.TrimPrefix(raw.Path, "/"), raw), jar, cred.User, cred.Password)
	db := extOpenAgentDB(t, cfg)
	ctx := context.Background()

	// server version round-trips and is non-empty
	var v string
	if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&v); err != nil {
		t.Fatalf("SELECT version(): %v", err)
	}
	if strings.TrimSpace(v) == "" {
		t.Fatalf("version() empty")
	}
	t.Logf("pg server version: %s", v)

	// comma literal → JSON parser's string-protection path
	if err := db.QueryRowContext(ctx, "SELECT 'a,b'").Scan(&v); err != nil {
		t.Fatalf("comma query: %v", err)
	}
	if v != "a,b" {
		t.Fatalf("comma got %q", v)
	}

	// q-mark bind (PG JDBC PreparedStatement accepts ? natively)
	var flag any
	if err := db.QueryRowContext(ctx, "SELECT CAST(? AS int) = 41", int64(41)).Scan(&flag); err != nil {
		t.Fatalf("q-mark bind: %v", err)
	}
	switch f := flag.(type) {
	case bool:
		if !f {
			t.Fatalf("q-mark bind comparison = false")
		}
	case string:
		if f != "t" && f != "true" {
			t.Fatalf("q-mark bind comparison = %q", f)
		}
	default:
		t.Fatalf("q-mark bind comparison unexpected type %T (%v)", flag, flag)
	}

	// CRITICAL: $N must reach the server unmangled — family "postgres" is not
	// rewritten on either side of the channel. PgJDBC does not accept $N as a
	// client-side bind marker (verified against the jar directly: preparing
	// "SELECT $1::int" and binding index 1 fails with "column index is out of
	// range: 1, number of columns: 0"), so server-side $N semantics are
	// exercised via PREPARE/EXECUTE on one pinned session. A channel-side
	// rewrite of $1 → ? would make PREPARE fail with a syntax error, and
	// EXECUTE returning 41 proves the bound value flows through the plan.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PREPARE exte2e_dollar(int) AS SELECT $1::int"); err != nil {
		t.Fatalf("PREPARE with $1: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DEALLOCATE exte2e_dollar")
	})
	var n int64
	if err := conn.QueryRowContext(ctx, "EXECUTE exte2e_dollar(41)").Scan(&n); err != nil {
		t.Fatalf("EXECUTE $1 plan: %v", err)
	}
	if n != 41 {
		t.Fatalf("EXECUTE $1 plan got %d, want 41", n)
	}

	// information_schema bound query
	var cnt int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ?", "public").Scan(&cnt); err != nil {
		t.Fatalf("information_schema bind: %v", err)
	}
	if cnt <= 0 {
		t.Fatalf("table count for schema %q = %d, want > 0", "public", cnt)
	}
}

// ── self-contained scaffolding (dev-env loader / DSN parsing / connection) ──
// Duplicated from parity_e2e_test.go under distinct ext* names: that file
// builds only with `-tags "e2e ob"`, so its symbols are invisible to plain
// `-tags e2e`, and same-named redeclarations would collide when both tags are on.

// extDevEnv loads testdata/db/.local-dev.env (`export KEY='value'` lines).
func extDevEnv(t *testing.T) map[string]string {
	t.Helper()
	m := map[string]string{}
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "db", ".local-dev.env"))
	if err == nil {
		re := regexp.MustCompile(`(?m)^export\s+([A-Za-z0-9_]+)='(.*)'\s*$`)
		for _, mm := range re.FindAllStringSubmatch(string(data), -1) {
			m[mm[1]] = mm[2]
		}
	}
	return m
}

// extLookup resolves a key: OS env wins over the dev-env file.
func extLookup(m map[string]string, key string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return m[key]
}

// extParseMysqlWireDSN parses root:PASS@tcp(172.20.208.1:3306)/
func extParseMysqlWireDSN(t *testing.T, dsn string) extCred {
	t.Helper()
	i := strings.Index(dsn, "@tcp(")
	if i < 0 {
		t.Fatalf("bad mysql dsn")
	}
	userinfo := dsn[:i]
	colon := strings.Index(userinfo, ":")
	if colon < 0 {
		t.Fatalf("bad mysql userinfo")
	}
	inside := dsn[i+len("@tcp("):]
	if j := strings.Index(inside, ")"); j >= 0 {
		inside = inside[:j]
	}
	inside = strings.Split(inside, "/")[0]
	j := strings.LastIndex(inside, ":")
	if j < 0 {
		t.Fatalf("bad mysql host:port")
	}
	var port int
	fmt.Sscanf(inside[j+1:], "%d", &port)
	return extCred{User: userinfo[:colon], Password: userinfo[colon+1:], Host: inside[:j], Port: port}
}

// extOpenAgentDB opens the owljdbc channel over the given config.
func extOpenAgentDB(t *testing.T, cfg agent.Config) *sql.DB {
	t.Helper()
	db, err := sql.Open("owljdbc", agent.EncodeDSN(cfg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
