//go:build e2e

package e2eagent

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	"github.com/cangyunye/go-owl-migrate/internal/dsnfields"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/importer"
	"github.com/cangyunye/owljdbc"
)

// ── §4.4 矩阵 · PostgreSQL 切片（UTF8 实例）──
// 该 PG 实例 server_encoding 为 UTF8（GBK 库需 GBK locale 初始化实例，待建），
// 本切片验证：① 探针双通道一致；② §2.5 的 client_encoding=UTF8 注入生效
// （native 走 dbconn.Open 注入，DSN 原本没有该参数）；③ 中文/数值/时间戳经
// 两通道导出导入后目标内容一致。占位符按通道分治：native pq 系 $N、agent
// PG JDBC 仅 ?。

func pgEnvDSN(t *testing.T) string {
	t.Helper()
	env := csEnv(t)
	return csGet(t, env, "OWL_E2E_PG_DSN")
}

func pgFields(t *testing.T, dsn string) *dsnfields.Fields {
	t.Helper()
	f, err := dsnfields.Decompose("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func pgOpen(t *testing.T, dsn, channel string) *sql.DB {
	t.Helper()
	if channel == "native" {
		// 走 dbconn.Open：验证 §2.5 的 client_encoding=UTF8 注入真实生效。
		db, err := dbconn.Open(config.DBConfig{Type: "postgres", DSN: dsn})
		if err != nil {
			t.Fatalf("native pg open: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	f := pgFields(t, dsn)
	ac, err := owljdbc.BuildConfig("postgres", owljdbc.Endpoint{
		Host: f.Host, Port: f.Port, User: f.Username, Password: f.Password, Database: f.Database,
	}, "", "../..", "../../owljdbc/jvm/owl-agent/owl-agent.jar", "")
	if err != nil {
		t.Fatalf("agent pg build config: %v", err)
	}
	db, err := sql.Open("owljdbc", owljdbc.EncodeDSN(ac))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestE2E_CharsetMatrixPostgres(t *testing.T) {
	pgDSN := pgEnvDSN(t)
	native := pgOpen(t, pgDSN, "native")
	agent := pgOpen(t, pgDSN, "agent")

	// 1) 探针：双通道判得同一 server_encoding（UTF8 实例）。
	en, err := dbconn.ProbeServerEncoding(context.Background(), native, "postgres")
	if err != nil {
		t.Fatalf("native probe: %v", err)
	}
	ea, err := dbconn.ProbeServerEncoding(context.Background(), agent, "postgres")
	if err != nil {
		t.Fatalf("agent probe: %v", err)
	}
	if en != ea {
		t.Fatalf("charset mismatch: native=%+v agent=%+v", en, ea)
	}
	if !en.Unicode {
		t.Fatalf("expected Unicode server encoding, got %+v", en)
	}
	t.Logf("postgres server encoding: %+v (client_encoding=UTF8 injected on native)", en)

	// 2) 影子表 + 种子。
	const tbl = "owl_pg_matrix"
	tryExecPG(t, native, "DROP TABLE IF EXISTS "+tbl)
	mustExecPG(t, native, `CREATE TABLE `+tbl+` (
		id INT PRIMARY KEY,
		txt VARCHAR(200),
		amt NUMERIC(12,2),
		ts TIMESTAMP
	)`)
	t.Cleanup(func() { tryExecPG(t, native, "DROP TABLE IF EXISTS "+tbl) })

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

	tblDef := func() *md.TableDef {
		td, err2 := md.NewTableDef("public", tbl)
		if err2 != nil {
			t.Fatal(err2)
		}
		for i, c := range []struct{ name, typ string }{
			{"id", "INT"}, {"txt", "VARCHAR"}, {"amt", "NUMERIC"}, {"ts", "TIMESTAMP"},
		} {
			cd, err2 := md.NewColumnDef("public", tbl, c.name, i+1, c.typ)
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

	// 3) 4 通道组合（占位符分治：native $N / agent ?）。
	dumps := map[string]string{}
	for _, srcCh := range []string{"native", "agent"} {
		for _, tgtCh := range []string{"native", "agent"} {
			label := srcCh + "->" + tgtCh
			t.Run(label, func(t *testing.T) {
				srcDB := pgOpen(t, pgDSN, srcCh)
				tgtDB := pgOpen(t, pgDSN, tgtCh)

				var srcPH, tgtPH string
				if srcCh == "agent" {
					srcPH = "qmark"
				}
				if tgtCh == "agent" {
					tgtPH = "qmark"
				}

				dir := t.TempDir()
				tables := []*md.TableDef{tblDef()}
				res, err := exporter.New(srcDB, exporter.Config{
					OutputDir: dir, Format: "csv", CSVHeader: true,
					CSVDelimiter: ",", CSVNullRep: "\\N",
					PageSize: 500, MaxWorkers: 1, DBType: "postgres",
					PlaceholderFamily: srcPH,
				}).ExportTables(context.Background(), tables, pks)
				if err != nil {
					t.Fatalf("export: %v", err)
				}
				for _, r := range res {
					if r.Error != nil {
						t.Fatalf("export: %v", r.Error)
					}
				}
				impRes, err := importer.New(tgtDB, importer.Config{
					SourceDir: dir, CSVDelimiter: ",", CSVNullMarker: "\\N",
					NullIf: []string{"NULL", "null", "\\N"},
					CommitInterval: 100, ErrorPolicy: "stop",
					MaxWorkers: 1, TargetDBType: "postgres",
					DateTimeFormat: "yyyyMMddHHmmss",
					PlaceholderFamily: tgtPH,
					TruncateBefore: true,
				}).ImportTables(context.Background(), tables,
					map[string]string{"public": "public"})
				if err != nil {
					t.Fatalf("import: %v", err)
				}
				for _, r := range impRes {
					if r.Err != nil {
						t.Fatalf("import: %v", r.Err)
					}
				}

				dump := dumpPGTable(t, native, tbl)
				if prev, ok := dumps["__any__"]; ok && prev != dump {
					t.Fatalf("%s: target dump differs across channel combos:\n%q\nvs\n%q", label, prev, dump)
				}
				dumps[label] = dump
				dumps["__any__"] = dump
			})
		}
	}
	var n int
	if err := native.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tbl)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(seedRows) {
		t.Fatalf("target rows = %d, want %d", n, len(seedRows))
	}
	t.Logf("postgres matrix ok: %d rows, all channel combos identical", n)
}

func dumpPGTable(t *testing.T, db *sql.DB, tbl string) string {
	t.Helper()
	rows, err := db.Query(fmt.Sprintf(
		"SELECT id, COALESCE(txt,'\\N'), COALESCE(amt::text,'\\N'), COALESCE(to_char(ts,'YYYY-MM-DD HH24:MI:SS'),'\\N') FROM %s ORDER BY id", tbl))
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

func tryExecPG(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Logf("benign exec failed: %v", err)
	}
}

func mustExecPG(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}
