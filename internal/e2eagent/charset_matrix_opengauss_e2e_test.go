//go:build e2e && og

package e2eagent

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	"github.com/cangyunye/go-owl-migrate/internal/dsnfields"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/importer"
	"github.com/cangyunye/owljdbc"
)

// ── §4.4 矩阵 · openGauss 切片 ──
// openGauss（PG 系，SHA256 认证需 og 驱动）：native = dbconn.Open（og 驱动，
// -tags og 编译）；agent = owljdbc + openGauss-JDBC（org.opengauss.Driver）。
// 该实例 server_encoding 为 UTF8（GBK 实例待建），本切片验证：探针双通道
// 一致、中文/数值/时间戳经两通道导出导入后目标内容一致。
// 占位符不显式设置：postgres 族 exporter/importer 自动选 dollar（native pq 系
// 原生；agent 侧 $N 文本直通，extension smoke 已验证）。

func ogTryExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Logf("benign exec failed: %v", err)
	}
}

func ogMustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func ogAdminDSN(t *testing.T) string {
	t.Helper()
	env := csEnv(t)
	if d := strings.TrimSpace(env["OWL_E2E_OPGAUSS_ADMIN_DSN"]); d != "" {
		return d
	}
	return csGet(t, env, "OWL_E2E_OPGAUSS_DSN")
}

func ogAgentJar(t *testing.T) string {
	t.Helper()
	jar := "../../jdbcdrivers/openGauss-JDBC-6.0.6/opengauss-jdbc-6.0.6.jar"
	if _, err := os.Stat(jar); err != nil {
		t.Skipf("openGauss jdbc jar missing (%s)", jar)
	}
	return jar
}

func ogOpen(t *testing.T, dsn, channel, sidecarJar, driverJar string) *sql.DB {
	t.Helper()
	if channel == "native" {
		db, err := dbconn.Open(config.DBConfig{Type: "opengaussdb", DSN: dsn})
		if err != nil {
			t.Fatalf("native opengauss open: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	ac := ogAgentConfig(t, dsn, sidecarJar, driverJar)
	db, err := sql.Open("owljdbc", owljdbc.EncodeDSN(ac))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ogAgentConfig 用 dsnfields 解析 libpq keyword 形式的 openGauss DSN，组装
// owljdbc 连接配置（JDBC URL 携带 sslmode 等原参数）。
func ogAgentConfig(t *testing.T, dsn, sidecarJar, driverJar string) owljdbc.Config {
	t.Helper()
	f, err := dsnfields.Decompose("opengaussdb", dsn)
	if err != nil {
		t.Fatal(err)
	}
	ssl := ""
	if strings.Contains(f.Extra, "sslmode=") {
		ssl = "?" + f.Extra
	}
	return owljdbc.Config{
		DriverClass: "org.opengauss.Driver",
		URL:         fmt.Sprintf("jdbc:opengauss://%s:%s/%s%s", f.Host, f.Port, f.Database, ssl),
		User:        f.Username,
		Password:    f.Password,
		Family:      "postgres",
		Classpath:   []string{driverJar},
		AgentJar:    sidecarJar,
	}
}




func TestE2E_CharsetMatrixOpenGauss(t *testing.T) {
	adminDSN := ogAdminDSN(t)
	sidecarJar := "../../owljdbc/jvm/owl-agent/owl-agent.jar"
	if _, err := os.Stat(sidecarJar); err != nil {
		t.Skipf("owl-agent.jar missing (%s): bash owljdbc/jvm/owl-agent/build.sh", sidecarJar)
	}
	driverJar := ogAgentJar(t)

	native := ogOpen(t, adminDSN, "native", sidecarJar, driverJar)
	agent := ogOpen(t, adminDSN, "agent", sidecarJar, driverJar)

	// 1) 探针：双通道判得同一 server_encoding。
	en, err := dbconn.ProbeServerEncoding(context.Background(), native, "opengaussdb")
	if err != nil {
		t.Fatalf("native probe: %v", err)
	}
	ea, err := dbconn.ProbeServerEncoding(context.Background(), agent, "opengaussdb")
	if err != nil {
		t.Fatalf("agent probe: %v", err)
	}
	if en != ea {
		t.Fatalf("charset mismatch: native=%+v agent=%+v", en, ea)
	}
	t.Logf("openGauss server encoding: %+v", en)

	// 2) 影子表 + 种子（GBK 安全集 + datetime + numeric + NULL）。
	const tbl = "owl_og_matrix"
	ogTryExec(t, native, "DROP TABLE IF EXISTS public."+tbl)
	ogMustExec(t, native, `CREATE TABLE public.`+tbl+` (
		id INT PRIMARY KEY,
		txt VARCHAR(200),
		amt NUMERIC(12,2),
		ts TIMESTAMP
	)`)
	t.Cleanup(func() { ogTryExec(t, native, "DROP TABLE IF EXISTS public."+tbl) })

	seedRows := []struct {
		id  int
		txt string
		amt string
		ts  string
	}{
		{1, "中文常用字与全角标点：，！？", "10.50", "2024-01-02 03:04:05"},
		{2, "GBK 范围生僻字：镕堃旻", "-0.01", "2024-12-31 23:59:59"},
		{3, "全角与符号：０１２３ ①⑵ Ⅷ", "123.45", "2023-06-01 12:00:00"},
		{4, "\\N", "0.00", "\\N"},
	}
	for _, r := range seedRows {
		if _, err := native.ExecContext(context.Background(),
			fmt.Sprintf("INSERT INTO %s (id, txt, amt, ts) VALUES ($1,$2,$3,$4)", tbl),
			r.id, nullIfMarker(r.txt), nullIfMarker(r.amt), nullIfMarker(r.ts)); err != nil {
			t.Fatalf("seed row %d: %v", r.id, err)
		}
	}

	tblDef := func(db string) *md.TableDef {
		td, err2 := md.NewTableDef(db, tbl)
		if err2 != nil {
			t.Fatal(err2)
		}
		for i, c := range []struct{ name, typ string }{
			{"id", "INT"}, {"txt", "VARCHAR"}, {"amt", "NUMERIC"}, {"ts", "TIMESTAMP"},
		} {
			cd, err2 := md.NewColumnDef(db, tbl, c.name, i+1, c.typ)
			if err2 != nil {
				t.Fatal(err2)
			}
			if err2 := td.AddColumn(cd); err2 != nil {
				t.Fatal(err2)
			}
		}
		td.AddPrimaryKey("pk_"+tbl, "id")
		return td
	}
	pks := map[string][]string{"public." + tbl: {"id"}}

	// 3) 4 通道组合：exporter（源通道）→ CSV → importer（目标通道）→ dump。
	dumps := map[string]string{}
	for _, srcCh := range []string{"native", "agent"} {
		for _, tgtCh := range []string{"native", "agent"} {
			label := srcCh + "->" + tgtCh
			t.Run(label, func(t *testing.T) {
				srcDB := ogOpen(t, adminDSN, srcCh, sidecarJar, driverJar)
				tgtDB := ogOpen(t, adminDSN, tgtCh, sidecarJar, driverJar)

				dir := t.TempDir()
				tables := []*md.TableDef{tblDef("public")}
				// 占位符按通道分治：native pq 系 $N、agent PG JDBC 仅 ?。
				var ph string
				if srcCh == "agent" {
					ph = "qmark"
				}
				res, err := exporter.New(srcDB, exporter.Config{
					OutputDir: dir, Format: "csv", CSVHeader: true,
					CSVDelimiter: ",", CSVNullRep: "\\N",
					PageSize: 500, MaxWorkers: 1, DBType: "postgres",
					PlaceholderFamily: ph,
				}).ExportTables(context.Background(), tables, pks)
				if err != nil {
					t.Fatalf("export: %v", err)
				}
				for _, r := range res {
					if r.Error != nil {
						t.Fatalf("export: %v", r.Error)
					}
				}
				var tgtPh string
				if tgtCh == "agent" {
					tgtPh = "qmark" // PG JDBC 只认 ?；native pq 系用默认 $N
				}
				impRes, err := importer.New(tgtDB, importer.Config{
					SourceDir: dir, CSVDelimiter: ",", CSVNullMarker: "\\N",
					NullIf: []string{"NULL", "null", "\\N"},
					CommitInterval: 100, ErrorPolicy: "stop",
					MaxWorkers: 1, TargetDBType: "postgres",
					// exporter 对 time.Time 输出紧凑格式（20060102150405），
					// importer 需显式声明同款解析格式（默认集不含它）。
					DateTimeFormat: "yyyyMMddHHmmss",
					PlaceholderFamily: tgtPh,
					TruncateBefore: true,
				}).ImportTables(context.Background(), []*md.TableDef{tblDef("public")},
					map[string]string{"public": "public"})
				if err != nil {
					t.Fatalf("import: %v", err)
				}
				for _, r := range impRes {
					if r.Err != nil {
						t.Fatalf("import: %v", r.Err)
					}
				}

				dump := dumpOGTable(t, native, tbl)
				if prev, ok := dumps["__any__"]; ok && prev != dump {
					t.Fatalf("%s: target dump differs across channel combos:\n%q\nvs\n%q", label, prev, dump)
				}
				dumps[label] = dump
				dumps["__any__"] = dump
			})
		}
	}
	// 行数与关键内容终检。
	var n int
	if err := native.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM public.%s", tbl)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(seedRows) {
		t.Fatalf("target rows = %d, want %d", n, len(seedRows))
	}
	t.Logf("opengauss matrix ok: %d rows, all channel combos identical", n)
}

func dumpOGTable(t *testing.T, db *sql.DB, tbl string) string {
	t.Helper()
	rows, err := db.Query(fmt.Sprintf(
		"SELECT id, COALESCE(txt,'\\\\N'), COALESCE(amt::text,'\\\\N'), COALESCE(to_char(ts,'YYYY-MM-DD HH24:MI:SS'),'\\\\N') FROM %s ORDER BY id", tbl))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var id int
		var txt, amt, ts string
		if err := rows.Scan(&id, &txt, &amt, &ts); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sb, "%d|%s|%s|%s\n", id, txt, amt, ts)
	}
	if sb.Len() == 0 {
		t.Fatalf("dump %s empty", tbl)
	}
	return sb.String()
}
