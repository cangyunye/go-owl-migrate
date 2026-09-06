//go:build e2e

package e2eagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"github.com/cangyunye/go-owl-migrate/internal/dsnfields"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/importer"
)

// ── §4.4 字符集矩阵 · MySQL 切片（P0 的 mysql 部分）──
// 同一 MySQL 服务器上三个库：owl_gbk_a / owl_gbk_b（CHARACTER SET gbk）、
// owl_utf8_t（utf8mb4）。三场景（S1 GBK→GBK、S2 UTF8→GBK、S3 GBK→UTF8）
// × 4 通道组合（源通道 × 目标通道 ∈ {native, agent}）：每组合走
// exporter（源通道读）→ CSV → importer（目标通道写），目标内容用固定
// native 连接回读，四组 dump 必须彼此一致。
//
// 说明：ProbeServerEncoding 是 server 级（该 MySQL 实例 character_set_server
// 为 utf8mb4）；本矩阵考察的是**库级** charset 下服务端的列级转换——入口
// 解码已由 §2.5 注入保证（连接一律 utf8 系），出口由目标库/列字符集转换。

const matrixFixtureSQL = `(
	id INT PRIMARY KEY,
	txt VARCHAR(200),
	amt DECIMAL(12,2),
	ts DATETIME
)`

var matrixRows = []struct {
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

// csEnv/csGet：自包含配置读取（parity 的 devEnv 在 e2e+ob 标签下，plain e2e
// 不可见；OS 环境变量优先，其次 testdata/db/.local-dev.env）。
func csEnv(t *testing.T) map[string]string {
	t.Helper()
	m := map[string]string{}
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "db", ".local-dev.env"))
	if err == nil {
		re := regexp.MustCompile(`(?m)^export\s+([A-Za-z0-9_]+)='(.*)'\s*$`)
		for _, mm := range re.FindAllStringSubmatch(string(data), -1) {
			m[mm[1]] = mm[2]
		}
	}
	for _, k := range []string{"OWL_E2E_MYSQL_DSN", "OWL_AGENT_JAR", "OWL_E2E_MYSQL_JAR"} {
		if v := os.Getenv(k); v != "" {
			m[k] = v
		}
	}
	return m
}

func csGet(t *testing.T, env map[string]string, key string) string {
	t.Helper()
	v := strings.TrimSpace(env[key])
	if v == "" {
		t.Skipf("%s 未配置（set env or testdata/db/.local-dev.env）", key)
	}
	return v
}

func csMatrixAdmin(t *testing.T) *sql.DB {
	t.Helper()
	env := csEnv(t)
	root := csGet(t, env, "OWL_E2E_MYSQL_DSN")
	admin, err := sql.Open("mysql", root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	return admin
}

func csMatrixCreateDB(t *testing.T, admin *sql.DB, name, charset string) {
	t.Helper()
	if _, err := admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name)); err != nil {
		t.Fatal(err)
	}
	q := fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET %s", name, charset)
	if _, err := admin.Exec(q); err != nil {
		t.Fatalf("create %s (%s): %v", name, charset, err)
	}
	// 确认库级字符集真的落成了 GBK/utf8mb4。
	var cs string
	if err := admin.QueryRow(
		"SELECT DEFAULT_CHARACTER_SET_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?",
		name).Scan(&cs); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(cs, charset) {
		t.Fatalf("database %s charset = %s, want %s (server may lack %s support)", name, cs, charset, charset)
	}
	if _, err := admin.Exec(fmt.Sprintf("CREATE TABLE `%s`.`t1` %s", name, matrixFixtureSQL)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name))
	})
}

func csMatrixSeed(t *testing.T, admin *sql.DB, db, charset string, includeSuperset bool) {
	t.Helper()
	for _, r := range matrixRows {
		txt := r.txt
		if !strings.EqualFold(charset, "gbk") && includeSuperset {
			txt = "emoji 🐉 supersets"
		}
		if _, err := admin.Exec(fmt.Sprintf("INSERT INTO `%s`.`t1` VALUES (?,?,?,?)", db),
			r.id, nullIfMarker(txt), r.amt, nullIfMarker(r.ts)); err != nil {
			t.Fatalf("seed %s row %d: %v", db, r.id, err)
		}
	}
}

func nullIfMarker(s string) any {
	if s == "\\N" {
		return nil
	}
	return s
}

// csMatrixOpen 打开某库的某通道连接。
func csMatrixOpen(t *testing.T, env map[string]string, db, channel string) *sql.DB {
	t.Helper()
	root := csGet(t, env, "OWL_E2E_MYSQL_DSN")
	if channel == "native" {
		db2, err := sql.Open("mysql", root+db)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db2.Close() })
		return db2
	}
	f, err := dsnfields.Decompose("mysql", root)
	if err != nil {
		t.Fatal(err)
	}
	driverJar := env["OWL_E2E_MYSQL_JAR"]
	if driverJar == "" {
		matches, _ := filepath.Glob("../../mysql-connector-j-*.jar")
		if len(matches) == 0 {
			t.Skipf("mysql connector jar missing at repo root (mysql-connector-j-*.jar)")
		}
		driverJar = matches[0]
	}
	cfg := map[string]any{
		"driverClass": "com.mysql.cj.jdbc.Driver",
		"url": fmt.Sprintf("jdbc:mysql://%s:%s/%s?useSSL=false&allowPublicKeyRetrieval=true&characterEncoding=UTF-8",
			f.Host, f.Port, db),
		"user":      f.Username,
		"password":  f.Password,
		"family":    "mysql",
		"classpath": []string{driverJar},
		"agentJar":  "../../jvm/owl-agent/owl-agent.jar",
	}
	b, _ := json.Marshal(cfg)
	db2, err := sql.Open("owljdbc", string(b))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	return db2
}

// TestE2E_CharsetMatrixMySQL：3 场景 × 4 通道组合，目标回读四组彼此一致。
func TestE2E_CharsetMatrixMySQL(t *testing.T) {
	env := csEnv(t)
	admin := csMatrixAdmin(t)

	csMatrixCreateDB(t, admin, "owl_gbk_a", "gbk")
	csMatrixCreateDB(t, admin, "owl_gbk_b", "gbk")
	csMatrixCreateDB(t, admin, "owl_utf8_t", "utf8mb4")
	// 三库都是 GBK 安全集：S2 考察「utf8 库中的 GBK 安全数据 → gbk 目标」；
	// emoji 等超集字符的失败语义归 UnsupportedRune 专测（两通道错误文案因驱
	// 动而异——native 3988 collation 拒绝 / agent 1366 incorrect string——但都
	// 失败，不静默替换）。
	csMatrixSeed(t, admin, "owl_gbk_a", "gbk", false)
	csMatrixSeed(t, admin, "owl_gbk_b", "gbk", false)
	csMatrixSeed(t, admin, "owl_utf8_t", "utf8mb4", false)

	scenarios := []struct {
		name       string
		srcDB      string
		tgtDB      string
		tgtCharset string
	}{
		{"S1_gbk_to_gbk", "owl_gbk_a", "owl_gbk_b", "gbk"},
		{"S2_utf8_to_gbk", "owl_utf8_t", "owl_gbk_b", "gbk"},
		{"S3_gbk_to_utf8", "owl_gbk_a", "owl_utf8_t", "utf8mb4"},
	}
	channels := []string{"native", "agent"}

	tblDef := func(db string) *md.TableDef {
		tbl, err := md.NewTableDef(db, "t1")
		if err != nil {
			t.Fatal(err)
		}
		specs := []struct {
			name, typ string
		}{
			{"id", "INT"}, {"txt", "VARCHAR"}, {"amt", "DECIMAL"}, {"ts", "DATETIME"},
		}
		for i, c := range specs {
			cd, err := md.NewColumnDef(db, "t1", c.name, i+1, c.typ)
			if err != nil {
				t.Fatal(err)
			}
			if err := tbl.AddColumn(cd); err != nil {
				t.Fatal(err)
			}
		}
		tbl.AddPrimaryKey("pk_t1", "id")
		return tbl
	}

	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			baselines := map[string]string{}
			for _, srcCh := range channels {
				for _, tgtCh := range channels {
					label := srcCh + "->" + tgtCh
					t.Run(label, func(t *testing.T) {
						srcDB := csMatrixOpen(t, env, sc.srcDB, srcCh)
						tgtDB := csMatrixOpen(t, env, sc.tgtDB, tgtCh)

						dir := t.TempDir()
						tables := []*md.TableDef{tblDef(sc.srcDB)}
						pks := map[string][]string{sc.srcDB + ".t1": {"id"}}
						res, err := exporter.New(srcDB, exporter.Config{
							OutputDir: dir, Format: "csv", CSVHeader: true,
							CSVDelimiter: ",", CSVNullRep: "\\N",
							PageSize: 500, MaxWorkers: 1, DBType: "mysql",
							PlaceholderFamily: "qmark",
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
							MaxWorkers: 1, TargetDBType: "mysql",
							PlaceholderFamily: "qmark",
							TruncateBefore:    true,
						}).ImportTables(context.Background(),
							[]*md.TableDef{tblDef(sc.srcDB)},
							map[string]string{sc.srcDB: sc.tgtDB})
						if err != nil {
							t.Fatalf("import: %v", err)
						}
						for _, r := range impRes {
							if r.Err != nil {
								t.Fatalf("import: %v", r.Err)
							}
						}

						// 固定 native 读取器回读目标（通道无关比对）。
						dump := dumpMatrixTable(t, admin, sc.tgtDB)
						if prev, ok := baselines[srcCh]; ok && prev != dump {
							t.Fatalf("target dump differs from %s baseline", srcCh)
						}
						if prev, ok := baselines["__any__"]; ok && prev != dump {
							t.Fatalf("target dump differs across channel combos:\n%q\nvs\n%q", prev, dump)
						}
						baselines[label] = dump
						baselines["__any__"] = dump
					})
				}
			}
			// 行数与内容抽查：GBK 场景下 emoji 行在 S2 必然语义不同，见
			// TestE2E_CharsetMatrixMySQLUnsupportedRune；这里断言行数一致即可。
			var n int
			if err := admin.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`.`t1`", sc.tgtDB)).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				t.Fatalf("%s: target empty after matrix", sc.tgtDB)
			}
		})
	}
}

func dumpMatrixTable(t *testing.T, admin *sql.DB, db string) string {
	t.Helper()
	rows, err := admin.Query(fmt.Sprintf("SELECT id, IFNULL(txt,'\\\\N'), IFNULL(amt,'\\\\N'), IFNULL(ts,'\\\\N') FROM `%s`.`t1` ORDER BY id", db))
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
		t.Fatalf("dump %s empty", db)
	}
	return sb.String()
}

// TestE2E_CharsetMatrixMySQLUnsupportedRune：UTF8 超集字符（emoji）写 GBK 目标
// 的失败语义——服务端拒绝，importer 报错；native 与 agent 都必须失败（不允许
// 静默替换）。
func TestE2E_CharsetMatrixMySQLUnsupportedRune(t *testing.T) {
	env := csEnv(t)
	admin := csMatrixAdmin(t)
	csMatrixCreateDB(t, admin, "owl_gbk_sup", "gbk")
	csMatrixCreateDB(t, admin, "owl_utf8_sup", "utf8mb4")
	if _, err := admin.Exec("INSERT INTO `owl_utf8_sup`.`t1` VALUES (1, 'emoji 🐉 here', 1.00, '2024-01-01 00:00:00')"); err != nil {
		t.Fatal(err)
	}

	for _, ch := range []string{"native", "agent"} {
		t.Run(ch, func(t *testing.T) {
			srcDB := csMatrixOpen(t, env, "owl_utf8_sup", "native") // 源固定 native 读
			tgtDB := csMatrixOpen(t, env, "owl_gbk_sup", ch)        // 目标通道轮换

			dir := t.TempDir()
			tbl := func(db string) *md.TableDef {
				td, err := md.NewTableDef(db, "t1")
				if err != nil {
					t.Fatal(err)
				}
				for i, c := range []string{"id", "txt", "amt", "ts"} {
					cd, err := md.NewColumnDef(db, "t1", c, i+1, "VARCHAR")
					if err != nil {
						t.Fatal(err)
					}
					_ = td.AddColumn(cd)
				}
				return td
			}
			tables := []*md.TableDef{tbl("owl_utf8_sup")}
			pks := map[string][]string{"owl_utf8_sup.t1": {"id"}}
			if _, err := exporter.New(srcDB, exporter.Config{
				OutputDir: dir, Format: "csv", CSVHeader: true,
				CSVDelimiter: ",", PageSize: 500, MaxWorkers: 1,
				DBType: "mysql", PlaceholderFamily: "qmark",
			}).ExportTables(context.Background(), tables, pks); err != nil {
				t.Fatalf("export: %v", err)
			}
			impRes, err := importer.New(tgtDB, importer.Config{
				SourceDir: dir, CSVDelimiter: ",", CSVNullMarker: "\\N",
				NullIf: []string{"NULL", "null", "\\N"},
				CommitInterval: 100, ErrorPolicy: "stop",
				MaxWorkers: 1, TargetDBType: "mysql",
				PlaceholderFamily: "qmark",
				TruncateBefore:    true,
			}).ImportTables(context.Background(), []*md.TableDef{tbl("owl_utf8_sup")},
				map[string]string{"owl_utf8_sup": "owl_gbk_sup"})
			// 与 exporter 同构：单表错误只落在 impRes[0].Err，顶层 error 为 nil。
			if err == nil && (len(impRes) != 1 || impRes[0].Err == nil) {
				t.Fatalf("%s: emoji write to gbk target must fail (row-level error), got err=%v impRes=%+v", ch, err, impRes)
			}
			t.Logf("%s rejected as expected: %v", ch, impRes[0].Err)
		})
	}
}

// ── §4.4 矩阵 · oceanbase-mysql 切片 ──
// 探针实测：OB MySQL 模式支持租户内按库指定 gbk（无需 GBK 租户）。结构与
// MySQL 切片一致：三库（gbk/gbk/utf8mb4）× S1/S2/S3 × 4 通道组合。native =
// go-sql-driver（OB MySQL wire），agent = owljdbc + oceanbase-client jar。

func csOBMatrixEnv(t *testing.T) (map[string]string, *sql.DB, string) {
	t.Helper()
	env := csEnv(t)
	root := csGet(t, env, "OWL_E2E_OB_MYSQL_DSN")
	admin, err := sql.Open("mysql", root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	return env, admin, root
}

func TestE2E_CharsetMatrixOceanBaseMySQL(t *testing.T) {
	env, admin, rootDSN := csOBMatrixEnv(t)

	for _, spec := range []struct{ name, charset string }{
		{"owl_ob_gbk_a", "gbk"}, {"owl_ob_gbk_b", "gbk"}, {"owl_ob_utf8_t", "utf8mb4"},
	} {
		csMatrixCreateDB(t, admin, spec.name, spec.charset)
	}
	// 三库同一份 GBK 安全集（emoji 失败语义已在 MySQL 切片专测覆盖）。
	csMatrixSeed(t, admin, "owl_ob_gbk_a", "gbk", false)
	csMatrixSeed(t, admin, "owl_ob_gbk_b", "gbk", false)
	csMatrixSeed(t, admin, "owl_ob_utf8_t", "utf8mb4", false)

	f, err := dsnfields.Decompose("oceanbase-mysql", rootDSN)
	if err != nil {
		t.Fatal(err)
	}
	obJar := env["OWL_E2E_OB_JAR"]
	if obJar == "" {
		obJar = "../../oceanbase-client-2.4.1.jar"
	}
	if _, err := os.Stat(obJar); err != nil {
		t.Skipf("oceanbase client jar missing (%s)", obJar)
	}

	openOB := func(db, channel string) *sql.DB {
		if channel == "native" {
			d, err := sql.Open("mysql", rootDSN+db)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = d.Close() })
			return d
		}
		cfg := map[string]any{
			"driverClass": "com.oceanbase.jdbc.Driver",
			"url": fmt.Sprintf("jdbc:oceanbase://%s:%s/%s?useSSL=false&characterEncoding=UTF-8",
				f.Host, f.Port, db),
			"user":      f.Username,
			"password":  f.Password,
			"family":    "mysql",
			"classpath": []string{obJar},
			"agentJar":  env["OWL_AGENT_JAR"],
		}
		delete(cfg, "agentJar")
		if v := env["OWL_AGENT_JAR"]; v != "" {
			cfg["agentJar"] = v
		} else {
			cfg["agentJar"] = "../../jvm/owl-agent/owl-agent.jar"
		}
		b, _ := json.Marshal(cfg)
		d, err := sql.Open("owljdbc", string(b))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Close() })
		return d
	}

	tblDef := func(db string) *md.TableDef {
		tbl, err := md.NewTableDef(db, "t1")
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range []string{"id", "txt", "amt", "ts"} {
			cd, err := md.NewColumnDef(db, "t1", c, i+1, map[string]string{"id": "INT", "txt": "VARCHAR", "amt": "DECIMAL", "ts": "DATETIME"}[c])
			if err != nil {
				t.Fatal(err)
			}
			if err := tbl.AddColumn(cd); err != nil {
				t.Fatal(err)
			}
		}
		tbl.AddPrimaryKey("pk_t1", "id")
		return tbl
	}

	scenarios := []struct{ name, srcDB, tgtDB string }{
		{"S1_gbk_to_gbk", "owl_ob_gbk_a", "owl_ob_gbk_b"},
		{"S2_utf8_to_gbk", "owl_ob_utf8_t", "owl_ob_gbk_b"},
		{"S3_gbk_to_utf8", "owl_ob_gbk_a", "owl_ob_utf8_t"},
	}
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			dumps := map[string]string{}
			for _, srcCh := range []string{"native", "agent"} {
				for _, tgtCh := range []string{"native", "agent"} {
					label := srcCh + "->" + tgtCh
					t.Run(label, func(t *testing.T) {
						srcDB := openOB(sc.srcDB, srcCh)
						tgtDB := openOB(sc.tgtDB, tgtCh)

						dir := t.TempDir()
						tables := []*md.TableDef{tblDef(sc.srcDB)}
						pks := map[string][]string{sc.srcDB + ".t1": {"id"}}
						res, err := exporter.New(srcDB, exporter.Config{
							OutputDir: dir, Format: "csv", CSVHeader: true,
							CSVDelimiter: ",", CSVNullRep: "\\N",
							PageSize: 500, MaxWorkers: 1, DBType: "mysql",
							PlaceholderFamily: "qmark",
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
							MaxWorkers: 1, TargetDBType: "mysql",
							PlaceholderFamily: "qmark",
							TruncateBefore:    true,
						}).ImportTables(context.Background(),
							[]*md.TableDef{tblDef(sc.srcDB)},
							map[string]string{sc.srcDB: sc.tgtDB})
						if err != nil {
							t.Fatalf("import: %v", err)
						}
						for _, r := range impRes {
							if r.Err != nil {
								t.Fatalf("import: %v", r.Err)
							}
						}
						dump := dumpMatrixTable(t, admin, sc.tgtDB)
						if prev, ok := dumps["__any__"]; ok && prev != dump {
							t.Fatalf("%s: target dump differs across channel combos:\n%q\nvs\n%q", label, prev, dump)
						}
						dumps[label] = dump
						dumps["__any__"] = dump
					})
				}
			}
		})
	}
}
