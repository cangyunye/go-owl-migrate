//go:build e2e && ob
// +build e2e,ob

// Package e2eagent is the dual-tenant native-vs-agent parity harness: the
// acceptance gate for the JDBC agent channel. It drives the same OceanBase
// Oracle tenant (MIGSRC schema) through two transports —
//
//	native: dbconn.Open("oceanbase-oracle", DSN)  → obconnector-go, MySQL wire
//	agent:  sql.Open("owljdbc", agent.EncodeDSN(cfg)) → Java sidecar over JDBC
//
// — and asserts the observable results match: extracted metadata, exported
// CSV bytes, imported rows, and export throughput (agent ≥ native/10).
//
// The native OB driver only links with `-tags ob`; when the binary was built
// without it, dbconn.Open returns an error mentioning "-tags" and the tests
// skip instead of failing.
//
// Run:
//
//	go test -tags "e2e ob" ./internal/e2eagent/ -v
//
// Credentials come from the OS environment or testdata/db/.local-dev.env
// (`export KEY='value'` lines; OS env wins). Nothing is hardcoded. Missing
// jars/DSNs skip cleanly.
package e2eagent

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/agent"
	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/importer"
)

// parityExtractDBType is used for extractor.Extract on BOTH transports so the
// metadata diff isolates transport differences, not querier differences. The
// OB-Oracle-wire querier binds with "?" — the only placeholder style the
// native obconnector-go (MySQL wire) connection accepts, and the agent's JDBC
// PreparedStatement binds it natively.
const parityExtractDBType = "oceanbase-oracle-wire"

// ── dev-env loader (OS env > testdata/db/.local-dev.env) ──
// Test scaffolding duplicated from internal/agent/integration_e2e_test.go by
// convention (same-package e2e harnesses stay self-contained).

func devEnv(t *testing.T) map[string]string {
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

func lookup(m map[string]string, key string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return m[key]
}

// ── DSN parsing (Oracle URL form / MySQL wire form → JDBC credentials) ──

type jdbcCred struct {
	User     string
	Password string
	Host     string
	Port     int
}

// parseOracleURLDSN parses
// oceanbase-oracle://MIGSRC@oratest:PASS%40WORD@127.0.0.1:2881/ — the password
// is percent-encoded, so the LAST '@' separates userinfo from host.
func parseOracleURLDSN(t *testing.T, dsn string) jdbcCred {
	t.Helper()
	rest := strings.TrimPrefix(strings.TrimPrefix(dsn, "oceanbase-oracle://"), "oracle://")
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		t.Fatalf("bad oracle dsn")
	}
	userinfo, hostport := rest[:at], rest[at+1:]
	colon := strings.Index(userinfo, ":")
	if colon < 0 {
		t.Fatalf("bad oracle userinfo")
	}
	pass, err := url.PathUnescape(userinfo[colon+1:])
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}
	host, port := splitHostPort(t, hostport)
	return jdbcCred{User: userinfo[:colon], Password: pass, Host: host, Port: port}
}

// parseMysqlWireDSN parses root@obmysql:PASS@WORD@tcp(127.0.0.1:2881)/
func parseMysqlWireDSN(t *testing.T, dsn string) jdbcCred {
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
	host, port := splitHostPort(t, strings.Split(inside, "/")[0])
	return jdbcCred{User: userinfo[:colon], Password: userinfo[colon+1:], Host: host, Port: port}
}

func splitHostPort(t *testing.T, hp string) (string, int) {
	t.Helper()
	hp = strings.TrimSuffix(hp, "/")
	i := strings.LastIndex(hp, ":")
	if i < 0 {
		t.Fatalf("bad host:port %q", hp)
	}
	var p int
	fmt.Sscanf(hp[i+1:], "%d", &p)
	return hp[:i], p
}

// ── connection helpers ──

func parityAgentCfg(t *testing.T, family, user, pass, host string, port int) agent.Config {
	t.Helper()
	agentJar := lookup(nil, "OWL_AGENT_JAR")
	if agentJar == "" {
		agentJar = filepath.Join("..", "..", "jvm", "owl-agent", "owl-agent.jar")
	}
	obJar := lookup(nil, "OWL_E2E_OB_JAR")
	if obJar == "" {
		obJar = filepath.Join("..", "..", "oceanbase-client-2.4.1.jar")
	}
	if _, err := os.Stat(agentJar); err != nil {
		t.Skipf("agent jar missing (%s): run bash jvm/owl-agent/build.sh", agentJar)
	}
	if _, err := os.Stat(obJar); err != nil {
		t.Skipf("OB jar missing (%s)", obJar)
	}
	return agent.Config{
		DriverClass: "com.oceanbase.jdbc.Driver",
		URL:         fmt.Sprintf("jdbc:oceanbase://%s:%d?useSSL=false", host, port),
		User:        user,
		Password:    pass,
		Family:      family,
		Classpath:   []string{obJar},
		AgentJar:    agentJar,
	}
}

func openAgentDB(t *testing.T, cfg agent.Config) *sql.DB {
	t.Helper()
	db, err := sql.Open("owljdbc", agent.EncodeDSN(cfg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func openNativeOB(t *testing.T, dbType, dsn string) *sql.DB {
	t.Helper()
	db, err := dbconn.Open(config.DBConfig{Type: dbType, DSN: dsn})
	if err != nil {
		if strings.Contains(err.Error(), "-tags") {
			t.Skipf("native OB driver not compiled: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// paritySource resolves the shared OB-Oracle MIGSRC fixture: native + agent
// connections and the schema to compare. Skips cleanly on missing DSN, missing
// jars, or a binary built without the native OB driver.
func paritySource(t *testing.T) (native, ag *sql.DB, schema string) {
	t.Helper()
	env := devEnv(t)
	dsn := lookup(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_OB_ORACLE_MIGSRC_DSN (or testdata/db/.local-dev.env)")
	}
	cred := parseOracleURLDSN(t, dsn)
	schema = lookup(env, "OWL_E2E_OB_ORACLE_SCHEMA")
	if schema == "" {
		// OB Oracle usernames travel as user@tenant inside the DSN; the Oracle
		// schema (table owner) is the bare user part.
		schema = strings.SplitN(cred.User, "@", 2)[0]
	}
	native = openNativeOB(t, "oceanbase-oracle", dsn)
	ag = openAgentDB(t, parityAgentCfg(t, "oracle", cred.User, cred.Password, cred.Host, cred.Port))
	return native, ag, schema
}

func pkCols(tbl *md.TableDef) []string {
	pks := tbl.GetPrimaryKeys()
	out := make([]string, 0, len(pks))
	for _, pk := range pks {
		out = append(out, pk.ColumnName)
	}
	return out
}

// ── 1) metadata parity ──

func TestE2E_Parity_Metadata(t *testing.T) {
	native, ag, schema := paritySource(t)

	ns, err := extractor.Extract(native, parityExtractDBType, schema)
	if err != nil {
		t.Fatalf("native extract: %v", err)
	}
	as, err := extractor.Extract(ag, parityExtractDBType, schema)
	if err != nil {
		t.Fatalf("agent extract: %v", err)
	}
	nt, at := ns.GetTables(), as.GetTables() // both sorted by schema.name
	if len(nt) != len(at) {
		t.Fatalf("table count native=%d agent=%d", len(nt), len(at))
	}
	for i := range nt {
		n, a := nt[i], at[i]
		if !strings.EqualFold(n.TableName, a.TableName) {
			t.Errorf("table %d: %s vs %s", i, n.TableName, a.TableName)
			continue
		}
		nc, ac := n.GetColumns(), a.GetColumns() // both sorted by ordinal position
		if len(nc) != len(ac) {
			t.Errorf("table %s: cols native=%d agent=%d", n.TableName, len(nc), len(ac))
			continue
		}
		for j := range nc {
			if !strings.EqualFold(nc[j].ColumnName, ac[j].ColumnName) || !strings.EqualFold(nc[j].DataType, ac[j].DataType) {
				t.Errorf("col %s.%s: native=%s/%s agent=%s/%s",
					n.TableName, nc[j].ColumnName, nc[j].ColumnName, nc[j].DataType, ac[j].ColumnName, ac[j].DataType)
			}
		}
		npk, apk := pkCols(n), pkCols(a)
		if strings.Join(npk, ",") != strings.Join(apk, ",") {
			t.Errorf("table %s: pk native=%v agent=%v", n.TableName, npk, apk)
		}
	}
	t.Logf("metadata parity ok: %d tables", len(nt))
}

// ── 2) export parity (CSV byte-diff) ──

func TestE2E_Parity_Export(t *testing.T) {
	native, ag, schema := paritySource(t)

	model, err := extractor.Extract(native, parityExtractDBType, schema)
	if err != nil {
		t.Fatal(err)
	}
	tables := model.GetTables()
	if len(tables) == 0 {
		t.Skip("no tables in source schema")
	}
	pks := map[string][]string{}
	for _, tbl := range tables {
		pks[tbl.TableSchema+"."+tbl.TableName] = pkCols(tbl)
	}

	dirN := t.TempDir()
	dirA := t.TempDir()
	expCfg := func(dir string) exporter.Config {
		return exporter.Config{
			OutputDir: dir, Format: "csv", CSVHeader: true,
			CSVDelimiter: ",", CSVNullRep: "\\N",
			PageSize: 500, MaxWorkers: 1, DBType: "oracle",
			// Both transports bind "?": the native side is the obconnector-go
			// MySQL wire (":N" is not supported), the agent side binds through
			// JDBC PreparedStatements.
			PlaceholderFamily: "qmark",
		}
	}
	if _, err := exporter.New(native, expCfg(dirN)).ExportTables(context.Background(), tables, pks); err != nil {
		t.Fatalf("native export: %v", err)
	}
	if _, err := exporter.New(ag, expCfg(dirA)).ExportTables(context.Background(), tables, pks); err != nil {
		t.Fatalf("agent export: %v", err)
	}

	filesN := csvMap(t, dirN)
	filesA := csvMap(t, dirA)
	if len(filesN) != len(filesA) {
		t.Fatalf("csv file count native=%d agent=%d", len(filesN), len(filesA))
	}
	diffs := 0
	for name, bn := range filesN {
		ba, ok := filesA[name]
		if !ok {
			t.Errorf("missing agent csv for %s", name)
			continue
		}
		if !bytes.Equal(bn, ba) {
			diffs++
			t.Errorf("csv bytes differ: %s (native %d B, agent %d B)", name, len(bn), len(ba))
		}
	}
	if diffs == 0 {
		t.Logf("export parity ok: %d csv files byte-identical", len(filesN))
	}
}

func csvMap(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".csv") {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			out[e.Name()] = b
		}
	}
	return out
}

// ── 3) import parity (self-contained: hand-written CSV + hand-built TableDef
// → shadow tables A/B → row count + content spot-check). Independent of the
// exporter file layout and of cross-schema privileges: two shadow tables are
// created inside the source schema, native imports A, agent imports B.

func TestE2E_Parity_Import(t *testing.T) {
	native, ag, schema := paritySource(t)

	const ddl = `CREATE TABLE %s (
		ID NUMBER(10) NOT NULL,
		NAME VARCHAR2(100),
		AMT NUMBER(12,2),
		TS DATE,
		CONSTRAINT %s PRIMARY KEY (ID)
	)`
	for _, tgt := range []string{"OWL_PARITY_A", "OWL_PARITY_B"} {
		tryExec(t, native, "DROP TABLE "+tgt) // absent on first run
		mustExec(t, native, fmt.Sprintf(ddl, tgt, "PK_"+tgt))
	}
	t.Cleanup(func() {
		tryExec(t, native, "DROP TABLE OWL_PARITY_A")
		tryExec(t, native, "DROP TABLE OWL_PARITY_B")
	})

	dir := t.TempDir()
	// Importer/Exporter CSV naming: {lower(schema)}.{lower(table)}.csv; row 1
	// is the header. Covers: plain value, NULL, quoted value containing a
	// comma, negative decimal, and datetime.
	csv := "ID,NAME,AMT,TS\n" +
		"1,alpha,10.5,2024-01-02 03:04:05\n" +
		"2,\\N,-0.01,\\N\n" +
		"3,\"x,\",123.45,2024-12-31 23:59:59\n"
	writeFile(t, filepath.Join(dir, "migsrc.owl_parity_a.csv"), csv)
	writeFile(t, filepath.Join(dir, "migsrc.owl_parity_b.csv"), csv)

	tblA := parityTableDef(schema, "OWL_PARITY_A")
	tblB := parityTableDef(schema, "OWL_PARITY_B")

	impCfg := func() importer.Config {
		return importer.Config{
			SourceDir: dir, CSVDelimiter: ",", CSVNullMarker: "\\N",
			NullIf:         []string{"NULL", "null", "\\N"},
			CommitInterval: 500, ErrorPolicy: "stop",
			MaxWorkers: 1, TargetDBType: "oracle",
			PlaceholderFamily: "qmark",
		}
	}
	mapping := map[string]string{schema: schema}
	resA, err := importer.New(native, impCfg()).ImportTables(context.Background(),
		[]*md.TableDef{tblA}, mapping)
	if err != nil {
		t.Fatalf("native import: %v", err)
	}
	resB, err := importer.New(ag, impCfg()).ImportTables(context.Background(),
		[]*md.TableDef{tblB}, mapping)
	if err != nil {
		t.Fatalf("agent import: %v", err)
	}
	if resA[0].Err != nil {
		t.Fatalf("native import: %v", resA[0].Err)
	}
	if resB[0].Err != nil {
		t.Fatalf("agent import: %v", resB[0].Err)
	}
	var ca, cb int
	mustScan(t, native, "SELECT COUNT(*) FROM OWL_PARITY_A", &ca)
	mustScan(t, native, "SELECT COUNT(*) FROM OWL_PARITY_B", &cb)
	if ca != cb {
		t.Errorf("row count mismatch: native=%d agent=%d", ca, cb)
	}
	if ca != 3 {
		t.Errorf("row count native=%d agent=%d, want 3", ca, cb)
	}
	var amtA, amtB string
	mustScan(t, native, "SELECT TO_CHAR(AMT) FROM OWL_PARITY_A WHERE ID=2", &amtA)
	mustScan(t, native, "SELECT TO_CHAR(AMT) FROM OWL_PARITY_B WHERE ID=2", &amtB)
	if amtA != amtB {
		t.Errorf("amt null-row mismatch: native=%q agent=%q", amtA, amtB)
	}
	var nameA, nameB string
	mustScan(t, native, "SELECT NAME FROM OWL_PARITY_A WHERE ID=3", &nameA)
	mustScan(t, native, "SELECT NAME FROM OWL_PARITY_B WHERE ID=3", &nameB)
	if nameA != nameB {
		t.Errorf("comma name mismatch: native=%q agent=%q", nameA, nameB)
	}
	if nameA != "x," {
		t.Errorf("comma name = %q, want \"x,\"", nameA)
	}
	var tsA, tsB string
	mustScan(t, native, "SELECT TO_CHAR(TS, 'YYYY-MM-DD HH24:MI:SS') FROM OWL_PARITY_A WHERE ID=1", &tsA)
	mustScan(t, native, "SELECT TO_CHAR(TS, 'YYYY-MM-DD HH24:MI:SS') FROM OWL_PARITY_B WHERE ID=1", &tsB)
	if tsA != tsB {
		t.Errorf("ts mismatch: native=%s agent=%s", tsA, tsB)
	}
	t.Logf("import parity ok: native=%d rows (expected %d), agent=%d rows (expected %d)",
		ca, resA[0].Expected, cb, resB[0].Expected)
}

// parityTableDef builds a TableDef matching the shadow-table DDL/CSV column
// order.
func parityTableDef(schema, table string) *md.TableDef {
	tbl, err := md.NewTableDef(schema, table)
	if err != nil {
		panic(err) // fixed non-empty arguments
	}
	cols := []struct {
		name        string
		typ         string
		prec, scale int
		nullable    string
	}{
		{"ID", "NUMBER", 10, 0, "NO"},
		{"NAME", "VARCHAR2", 100, 0, "YES"},
		{"AMT", "NUMBER", 12, 2, "YES"},
		{"TS", "DATE", 0, 0, "YES"},
	}
	for i, c := range cols {
		cd, err := md.NewColumnDef(schema, table, c.name, i+1, c.typ)
		if err != nil {
			panic(err)
		}
		cd.DataPrecision = c.prec
		cd.DataScale = c.scale
		cd.Nullable = c.nullable
		if err := tbl.AddColumn(cd); err != nil {
			panic(err)
		}
	}
	tbl.AddPrimaryKey("PK_"+table, "ID")
	return tbl
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustScan(t *testing.T, db *sql.DB, q string, dest any) {
	t.Helper()
	if err := db.QueryRow(q).Scan(dest); err != nil {
		t.Fatalf("scan %q: %v", q, err)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// tryExec runs DDL whose failure is expected/benign (e.g. dropping a shadow
// table that does not exist yet).
func tryExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	_, _ = db.Exec(q)
}

// ── 4) perf baseline (env-gated) ──

func TestE2E_Perf_Baseline(t *testing.T) {
	env := devEnv(t)
	dsn := lookup(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	tableName := lookup(env, "OWL_AGENT_PERF_TABLE")
	if dsn == "" || tableName == "" {
		t.Skip("set OWL_E2E_OB_ORACLE_MIGSRC_DSN and OWL_AGENT_PERF_TABLE to run perf baseline")
	}
	native, ag, schema := paritySource(t)

	model, err := extractor.Extract(native, parityExtractDBType, schema)
	if err != nil {
		t.Fatal(err)
	}
	var tbl *md.TableDef
	for _, c := range model.GetTables() {
		if strings.EqualFold(c.TableName, tableName) {
			tbl = c
			break
		}
	}
	if tbl == nil {
		t.Fatalf("OWL_AGENT_PERF_TABLE %q not found in schema %s", tableName, schema)
	}
	pk := map[string][]string{tbl.TableSchema + "." + tbl.TableName: pkCols(tbl)}

	expCfg := func(dir string) exporter.Config {
		return exporter.Config{
			OutputDir: dir, Format: "csv", CSVHeader: true,
			CSVDelimiter: ",", CSVNullRep: "\\N",
			PageSize: 5000, MaxWorkers: 1, DBType: "oracle",
			PlaceholderFamily: "qmark",
		}
	}
	dirN, dirA := t.TempDir(), t.TempDir()
	resN, err := exporter.New(native, expCfg(dirN)).ExportTables(context.Background(), []*md.TableDef{tbl}, pk)
	if err != nil {
		t.Fatalf("native export: %v", err)
	}
	resA, err := exporter.New(ag, expCfg(dirA)).ExportTables(context.Background(), []*md.TableDef{tbl}, pk)
	if err != nil {
		t.Fatalf("agent export: %v", err)
	}
	if len(resN) != 1 || len(resA) != 1 {
		t.Fatalf("expected 1 result each, got native=%d agent=%d", len(resN), len(resA))
	}
	if resN[0].Error != nil || resA[0].Error != nil {
		t.Fatalf("export errors: native=%v agent=%v", resN[0].Error, resA[0].Error)
	}

	thr := func(r exporter.TableResult) (rowsPerSec, mbPerSec float64) {
		secs := r.Duration.Seconds()
		if secs <= 0 {
			secs = 1e-9
		}
		rowsPerSec = float64(r.Rows) / secs
		if fi, err := os.Stat(r.OutputFile); err == nil {
			mbPerSec = float64(fi.Size()) / (1 << 20) / secs
		}
		return rowsPerSec, mbPerSec
	}
	nRps, nMps := thr(resN[0])
	aRps, aMps := thr(resA[0])
	ratio := 0.0
	if nRps > 0 {
		ratio = aRps / nRps
	}
	t.Logf("perf table %s: native %d rows in %s (%.0f rows/s, %.2f MB/s) | agent %d rows in %s (%.0f rows/s, %.2f MB/s) | agent/native=%.3f (1/3 target, ≥0.1 hard floor)",
		tbl.TableName, resN[0].Rows, resN[0].Duration, nRps, nMps,
		resA[0].Rows, resA[0].Duration, aRps, aMps, ratio)
	if aRps < nRps/10 {
		t.Fatalf("agent export throughput %.0f rows/s is below native/10 (%.0f rows/s; ratio=%.3f)", aRps, nRps, ratio)
	}
}

// ── 5) fail-fast on a broken agent jar path ──
func TestE2E_AgentBadJarFailsFast(t *testing.T) {
	env := devEnv(t)
	dsn := lookup(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_OB_ORACLE_MIGSRC_DSN (or testdata/db/.local-dev.env)")
	}
	cred := parseOracleURLDSN(t, dsn)
	cfg := parityAgentCfg(t, "oracle", cred.User, cred.Password, cred.Host, cred.Port)
	cfg.AgentJar = "/nonexistent/owl-agent.jar"
	db, err := sql.Open("owljdbc", agent.EncodeDSN(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := db.QueryContext(ctx, "SELECT 1 FROM DUAL"); err == nil {
		t.Fatal("expected error for missing agent jar")
	}
}
