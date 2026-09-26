//go:build e2e && og

package cmd

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Oracle → openGauss 全兼容模式双通道对拍 ──
// 源 = Oracle 真库的 OWL_OGPARITY fixture（数值/变长/定长/日期/默认值）；
// export data / migrate / import 各以 native 与 agent 通道完整跑产品命令，
// 目标落库内容必须一致。覆盖三种 openGauss 兼容模式：
//
//	og_pg    → opengaussdb        （PostgreSQL 兼容）
//	og_ora   → opengaussdb-oracle （Oracle 兼容，A 模式）
//	og_mysql → opengaussdb-mysql  （MySQL 兼容，B 模式/dolphin，标识符折叠小写）
//
// 手工对拍固化的三个配置要点（缺一即失败，见 2026-09-26 对拍记录）：
//  1. migrate 必须带 export.csv.header: true——临时 CSV 无表头时 import 会把
//     首行数据当列名（报 column "1" does not exist 且悄悄丢一行）；
//  2. CSV 元数据导入必须显式 ddl.source_dialect: oracle——否则类型不转换，
//     Oracle 原始 DDL（SYSDATE 等）直发目标；
//  3. import.data_transforms.datetime_format: "yyyyMMddHHmmss"——oracle 族
//     导出的 14 位紧凑日期 og 的 timestamp 解析不了（MySQL 恰好能吃）。
//
// 环境变量（缺省 skip，不打 CI）：
//	OWL_E2E_ORACLE_DSN    源 oracle://user:pass@host:1521/SVC
//	OWL_E2E_OG_PG_DSN     opengaussdb 键值对 DSN（PG 兼容库）
//	OWL_E2E_OG_ORA_DSN    opengaussdb-oracle（Oracle 兼容库）
//	OWL_E2E_OG_MYSQL_DSN  opengaussdb-mysql（MySQL 兼容库）
//	OWL_E2E_JARS_DIR      驱动 jar 目录（默认 "../../"，需含 ojdbc*.jar 与
//	                      opengauss-jdbc-*.jar）
//	OWL_E2E_AGENT_JAR     owl-agent.jar（默认 "../../owljdbc/jvm/owl-agent/owl-agent.jar"）

const ogpTable = "OWL_OGPARITY"
const ogpSchema = "owl_e2e"

type ogpTarget struct {
	name   string // 子测试名
	dbType string // openGauss 变体类型
	dsnEnv string
	folded bool // B 模式：落库标识符折叠小写
}

var ogpTargets = []ogpTarget{
	{"og_pg", "opengaussdb", "OWL_E2E_OG_PG_DSN", false},
	{"og_ora", "opengaussdb-oracle", "OWL_E2E_OG_ORA_DSN", false},
	{"og_mysql", "opengaussdb-mysql", "OWL_E2E_OG_MYSQL_DSN", true},
}

func ogpEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ogpSource 准备 Oracle 源连接与 fixture，返回 (源库连接, schema, 清理函数)。
func ogpSource(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("OWL_E2E_ORACLE_DSN")
	if dsn == "" {
		t.Skip("OWL_E2E_ORACLE_DSN 未设置")
	}
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		t.Fatalf("open oracle: %v", err)
	}
	var schema string
	if err := db.QueryRow("SELECT USER FROM DUAL").Scan(&schema); err != nil {
		t.Fatalf("resolve oracle schema: %v", err)
	}
	for _, s := range []string{
		`BEGIN EXECUTE IMMEDIATE 'DROP TABLE ` + ogpTable + `'; EXCEPTION WHEN OTHERS THEN IF SQLCODE != -942 THEN RAISE; END IF; END;`,
		`CREATE TABLE ` + ogpTable + ` (
			id      NUMBER(10) PRIMARY KEY,
			name    VARCHAR2(100) NOT NULL,
			amount  NUMBER(12,2) DEFAULT 0,
			created DATE DEFAULT SYSDATE,
			status  CHAR(1)
		)`,
		`INSERT INTO ` + ogpTable + ` (id, name, amount, created, status)
		 SELECT ROWNUM, 'name-' || ROWNUM, ROWNUM * 1.5,
		        TO_DATE('2026-01-02 03:04:05', 'YYYY-MM-DD HH24:MI:SS') + ROWNUM / 24,
		        CASE WHEN MOD(ROWNUM, 2) = 0 THEN 'Y' ELSE 'N' END
		 FROM DUAL CONNECT BY ROWNUM <= 8`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	t.Cleanup(func() {
		db.Exec(`BEGIN EXECUTE IMMEDIATE 'DROP TABLE ` + ogpTable + `'; EXCEPTION WHEN OTHERS THEN NULL; END;`)
		db.Close()
	})
	return db, schema
}

// ogpJars 校验 agent 通道 jar 齐备，返回 jars_dir 与 agent_jar。
func ogpJars(t *testing.T) (string, string) {
	t.Helper()
	jarsDir := ogpEnv("OWL_E2E_JARS_DIR", "../../")
	agentJar := ogpEnv("OWL_E2E_AGENT_JAR", "../../owljdbc/jvm/owl-agent/owl-agent.jar")
	for _, g := range []string{"ojdbc*.jar", "opengauss-jdbc-*.jar"} {
		if m, _ := filepath.Glob(filepath.Join(jarsDir, g)); len(m) == 0 {
			t.Skipf("%s 缺失于 %s（agent 通道需要）", g, jarsDir)
		}
	}
	if _, err := os.Stat(agentJar); err != nil {
		t.Skipf("owl-agent.jar 缺失（先跑 owljdbc/jvm/owl-agent/build.sh）: %v", err)
	}
	return jarsDir, agentJar
}

func ogpTargetDB(t *testing.T, tg ogpTarget) *sql.DB {
	t.Helper()
	dsn := os.Getenv(tg.dsnEnv)
	if dsn == "" {
		t.Skipf("%s 未设置", tg.dsnEnv)
	}
	db, err := sql.Open("opengauss", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", tg.dbType, err)
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %q`, ogpSchema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	ogpDropTarget(t, db, tg)
	t.Cleanup(func() { ogpDropTarget(t, db, tg); db.Close() })
	return db
}

func ogpDropTarget(t *testing.T, db *sql.DB, tg ogpTarget) {
	t.Helper()
	for _, n := range []string{strings.ToLower(ogpTable), ogpTable} {
		db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %q.%q`, ogpSchema, n))
	}
}

// ogpDump 把目标表内容规范成行集合（通道无关），ORDER BY 第一列。
func ogpDump(t *testing.T, db *sql.DB, tg ogpTarget) string {
	t.Helper()
	name := ogpTable
	if tg.folded {
		name = strings.ToLower(ogpTable)
	}
	rows, err := db.Query(fmt.Sprintf(`SELECT * FROM %q.%q ORDER BY 1`, ogpSchema, name))
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	sb.WriteString(strings.Join(cols, "\t"))
	sb.WriteString("\n")
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			if v == nil {
				parts[i] = `\N`
			} else if b, ok := v.([]byte); ok {
				parts[i] = string(b)
			} else {
				parts[i] = fmt.Sprintf("%v", v)
			}
		}
		sb.WriteString(strings.Join(parts, "\t"))
		sb.WriteString("\n")
	}
	return sb.String()
}

func ogpMetadataExport(t *testing.T, srcDSN, srcSchema string) string {
	t.Helper()
	metaDir := filepath.Join(t.TempDir(), "meta")
	cfg := writeCLICfg(t, fmt.Sprintf(`general:
  log_level: error
metadata:
  type: database
ddl:
  target_dialect: oracle
agent:
  jars_dir: %s
  agent_jar: %s
source:
  type: oracle
  dsn: '%s'
  schema: "%s"
`, ogpEnv("OWL_E2E_JARS_DIR", "../../"), ogpEnv("OWL_E2E_AGENT_JAR", "../../owljdbc/jvm/owl-agent/owl-agent.jar"), srcDSN, srcSchema))
	if err := runCLI(t, "export-metadata", "-c", cfg, "--format", "csv", "--scope", "table:"+ogpTable, "-o", metaDir); err != nil {
		t.Fatalf("export-metadata: %v", err)
	}
	return metaDir
}

func ogpExportData(t *testing.T, metaDir, srcDSN, srcSchema, channel string) string {
	t.Helper()
	jarsDir, agentJar := ogpJars(t)
	dataDir := filepath.Join(t.TempDir(), "data_"+channel)
	cfg := writeCLICfg(t, fmt.Sprintf(`general:
  log_level: error
metadata:
  type: csv
  csv:
    path: %s
    has_header: true
ddl:
  target_dialect: oracle
agent:
  jars_dir: %s
  agent_jar: %s
source:
  type: oracle
  dsn: '%s'
  schema: "%s"
  channel: %s
export:
  format: csv
  csv:
    delimiter: ","
    header: true
    null_representation: "\\N"
  batch:
    page_size: 500
`, metaDir, jarsDir, agentJar, srcDSN, srcSchema, channel))
	if err := runCLI(t, "export", "data", "-c", cfg, "-o", dataDir); err != nil {
		t.Fatalf("export data channel=%s: %v", channel, err)
	}
	return dataDir
}

func ogpMigrateYAML(t *testing.T, tg ogpTarget, metaDir, srcDSN, srcSchema, channel string) string {
	t.Helper()
	jarsDir, agentJar := ogpJars(t)
	return writeCLICfg(t, fmt.Sprintf(`general:
  log_level: error
metadata:
  type: csv
  csv:
    path: %s
    has_header: true
ddl:
  source_dialect: oracle
  target_dialect: %s
  schema_mapping:
    %s: %s
agent:
  jars_dir: %s
  agent_jar: %s
export:
  format: csv
  csv:
    delimiter: ","
    header: true
    null_representation: "\\N"
  batch:
    page_size: 500
source:
  type: oracle
  dsn: '%s'
  schema: "%s"
  channel: %s
target:
  type: %s
  dsn: '%s'
  channel: %s
import:
  data_transforms:
    datetime_format: "yyyyMMddHHmmss"
  csv:
    delimiter: ","
    null_marker: "\\N"
  target:
    truncate_before: true
  batch:
    commit_interval: 100
    error_policy: stop
`, metaDir, tg.dbType, srcSchema, ogpSchema, jarsDir, agentJar, srcDSN, srcSchema, channel, tg.dbType, os.Getenv(tg.dsnEnv), channel))
}

func ogpImportYAML(t *testing.T, tg ogpTarget, metaDir, dataDir, srcSchema, channel string) string {
	t.Helper()
	jarsDir, agentJar := ogpJars(t)
	return writeCLICfg(t, fmt.Sprintf(`general:
  log_level: error
metadata:
  type: csv
  csv:
    path: %s
    has_header: true
ddl:
  source_dialect: oracle
  target_dialect: %s
  schema_mapping:
    %s: %s
agent:
  jars_dir: %s
  agent_jar: %s
import:
  data_transforms:
    datetime_format: "yyyyMMddHHmmss"
  source_dir: %s
  format: csv
  csv:
    delimiter: ","
    null_marker: "\\N"
  target:
    truncate_before: true
target:
  type: %s
  dsn: '%s'
  channel: %s
`, metaDir, tg.dbType, srcSchema, ogpSchema, jarsDir, agentJar, dataDir, tg.dbType, os.Getenv(tg.dsnEnv), channel))
}

func ogpCompareDirs(t *testing.T, a, b string) {
	t.Helper()
	ea, err := os.ReadDir(a)
	if err != nil {
		t.Fatal(err)
	}
	eb, err := os.ReadDir(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(ea) != len(eb) {
		t.Fatalf("文件数不一致: %d vs %d", len(ea), len(eb))
	}
	for _, f := range ea {
		ca, err := os.ReadFile(filepath.Join(a, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		cb, err := os.ReadFile(filepath.Join(b, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(ca, cb) {
			t.Errorf("%s 两通道产物不一致", f.Name())
		}
	}
}

// TestE2E_OGParity_ExportData：oracle 源导出数据，native 与 agent 逐字节一致。
func TestE2E_OGParity_ExportData(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过 e2e")
	}
	_, srcSchema := ogpSource(t)
	srcDSN := os.Getenv("OWL_E2E_ORACLE_DSN")
	metaDir := ogpMetadataExport(t, srcDSN, srcSchema)
	dirN := ogpExportData(t, metaDir, srcDSN, srcSchema, "native")
	dirA := ogpExportData(t, metaDir, srcDSN, srcSchema, "agent")
	ogpCompareDirs(t, dirN, dirA)
}

// TestE2E_OGParity_Migrate：migrate 全流程（DDL+数据）双通道，目标落库一致。
func TestE2E_OGParity_Migrate(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过 e2e")
	}
	_, srcSchema := ogpSource(t)
	srcDSN := os.Getenv("OWL_E2E_ORACLE_DSN")
	metaDir := ogpMetadataExport(t, srcDSN, srcSchema)
	for _, tg := range ogpTargets {
		t.Run(tg.name, func(t *testing.T) {
			admin := ogpTargetDB(t, tg)
			dumps := map[string]string{}
			for _, ch := range []string{"native", "agent"} {
				ogpDropTarget(t, admin, tg)
				cfg := ogpMigrateYAML(t, tg, metaDir, srcDSN, srcSchema, ch)
				if err := runCLI(t, "migrate", "-c", cfg, "--temp-dir", t.TempDir()); err != nil {
					t.Fatalf("migrate channel=%s: %v", ch, err)
				}
				dumps[ch] = ogpDump(t, admin, tg)
			}
			if dumps["native"] != dumps["agent"] {
				t.Errorf("migrate 双通道落库不一致:\n native: %s\n agent:  %s", dumps["native"], dumps["agent"])
			}
			if got := strings.Count(dumps["native"], "\n") - 1; got != 8 {
				t.Errorf("落库行数 = %d, want 8", got)
			}
		})
	}
}

// TestE2E_OGParity_Import：CSV→目标双通道，目标落库一致。
func TestE2E_OGParity_Import(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过 e2e")
	}
	_, srcSchema := ogpSource(t)
	srcDSN := os.Getenv("OWL_E2E_ORACLE_DSN")
	metaDir := ogpMetadataExport(t, srcDSN, srcSchema)
	dataDir := ogpExportData(t, metaDir, srcDSN, srcSchema, "native")
	for _, tg := range ogpTargets {
		t.Run(tg.name, func(t *testing.T) {
			admin := ogpTargetDB(t, tg)
			dumps := map[string]string{}
			for _, ch := range []string{"native", "agent"} {
				ogpDropTarget(t, admin, tg)
				cfg := ogpImportYAML(t, tg, metaDir, dataDir, srcSchema, ch)
				if err := runCLI(t, "import", "-c", cfg); err != nil {
					t.Fatalf("import channel=%s: %v", ch, err)
				}
				dumps[ch] = ogpDump(t, admin, tg)
			}
			if dumps["native"] != dumps["agent"] {
				t.Errorf("import 双通道落库不一致:\n native: %s\n agent:  %s", dumps["native"], dumps["agent"])
			}
			if got := strings.Count(dumps["native"], "\n") - 1; got != 8 {
				t.Errorf("落库行数 = %d, want 8", got)
			}
		})
	}
}
