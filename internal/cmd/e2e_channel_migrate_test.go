//go:build e2e

package cmd

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// ── M3：migrate / import 的产品级双通道对拍 ──
// 同一份 MySQL fixture，migrate（源→目标全流程）与 import（CSV→目标）各以
// native 与 agent 通道完整跑一遍产品命令，两个目标库的落库内容必须一致。
// 目标表由 migrate 内建 ensureTablesForMigrate 创建；import 用 CREATE TABLE
// ... LIKE 预建。清理策略：每轮独立目标库，t.Cleanup 整库 DROP。

func channelMigrateYAML(t *testing.T, srcDSN, srcSchema, tgtDSN, tgtDB, channel string) string {
	t.Helper()
	return fmt.Sprintf(`general:
  log_level: info
metadata:
  type: database
ddl:
  target_dialect: mysql
  schema_mapping:
    %s: %s
agent:
  jars_dir: "../../"
  agent_jar: "../../jvm/owl-agent/owl-agent.jar"
export:
  format: csv
  csv:
    delimiter: ","
    header: true
    null_representation: "\\N"
  batch:
    page_size: 500
source:
  channel: %s
  type: mysql
  dsn: "%s"
  schema: "%s"
target:
  channel: %s
  type: mysql
  dsn: "%s"
`, srcSchema, tgtDB, channel, srcDSN, srcSchema, channel, tgtDSN)
}

func channelImportYAML(t *testing.T, srcDSN, tgtDSN, tgtDB, csvDir, channel string) string {
	t.Helper()
	return fmt.Sprintf(`general:
  log_level: info
metadata:
  type: database
ddl:
  target_dialect: mysql
  schema_mapping:
    migsrc_chan: %s
agent:
  jars_dir: "../../"
  agent_jar: "../../jvm/owl-agent/owl-agent.jar"
source:
  type: mysql
  dsn: "%s"
  schema: "migsrc_chan"
import:
  source_dir: "%s"
  format: csv
  csv:
    delimiter: ","
    null_marker: "\\N"
target:
  channel: %s
  type: mysql
  dsn: "%s"
`, tgtDB, srcDSN, csvDir, channel, tgtDSN)
}

// dumpTargetTable 用固定 native 连接把目标表内容规范成行集合，通道无关。
func dumpTargetTable(t *testing.T, admin *sql.DB, db, table string) string {
	t.Helper()
	rows, err := admin.Query(fmt.Sprintf("SELECT * FROM `%s`.`%s` ORDER BY 1", db, table))
	if err != nil {
		t.Fatalf("dump %s.%s: %v", db, table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
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
				parts[i] = "\\N"
			} else if b, ok := v.([]byte); ok {
				parts[i] = string(b)
			} else {
				parts[i] = fmt.Sprintf("%v", v)
			}
		}
		sb.WriteString(strings.Join(parts, "|"))
		sb.WriteString("\n")
	}
	return sb.String()
}

func TestE2E_CLI_MigrateChannelParity(t *testing.T) {
	env := devEnv(t)
	mysqlRoot := devGet(t, env, "OWL_E2E_MYSQL_DSN")
	src := seedMySQLSource(t, "mysql", mysqlRoot, "migsrc_chan")
	t.Cleanup(func() { src.Close() })
	srcDSN := mysqlRoot + "migsrc_chan"

	admin := connectE2E(t, "mysql", mysqlRoot)
	t.Cleanup(func() { admin.Close() })
	for _, db := range []string{"migtgt_native", "migtgt_agent"} {
		if _, err := admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", db)); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(fmt.Sprintf("CREATE DATABASE `%s`", db)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", db))
		})
	}

	tgtDSN := strings.TrimSuffix(mysqlRoot, "/") + "/" // go-sql-driver 要求 /dbname 形式
	runMigrate := func(channel, tgtDB string) {
		cfgPath := writeCLICfg(t, channelMigrateYAML(t, srcDSN, "migsrc_chan", tgtDSN, tgtDB, channel))
		tmpDir := t.TempDir()
		if err := runCLI(t, "migrate", "-c", cfgPath, "--temp-dir", tmpDir,
			"-r", filepath.Join(tmpDir, "report.json")); err != nil {
			t.Fatalf("migrate channel=%s: %v", channel, err)
		}
	}
	runMigrate("native", "migtgt_native")
	runMigrate("agent", "migtgt_agent")

	for _, table := range []string{"dept", "emp"} {
		dn := dumpTargetTable(t, admin, "migtgt_native", table)
		da := dumpTargetTable(t, admin, "migtgt_agent", table)
		if dn == "" {
			t.Fatalf("%s: native target empty", table)
		}
		if dn != da {
			t.Fatalf("%s: target content differs between channels\nnative: %q\nagent: %q", table, dn, da)
		}
		t.Logf("%s parity ok (%d bytes)", table, len(dn))
	}
}

func TestE2E_CLI_ImportChannelParity(t *testing.T) {
	env := devEnv(t)
	mysqlRoot := devGet(t, env, "OWL_E2E_MYSQL_DSN")
	src := seedMySQLSource(t, "mysql", mysqlRoot, "migsrc_chan")
	t.Cleanup(func() { src.Close() })
	srcDSN := mysqlRoot + "migsrc_chan"

	// 产品级 native 导出产出 CSV（import 的输入）。
	csvDir := t.TempDir()
	nativeCfg := writeCLICfg(t, channelCLIYAML(t, srcDSN, "migsrc_chan", "native"))
	if err := runCLI(t, "export", "data", "-c", nativeCfg, "-o", csvDir, "--format", "csv"); err != nil {
		t.Fatalf("baseline export: %v", err)
	}

	admin := connectE2E(t, "mysql", mysqlRoot)
	t.Cleanup(func() { admin.Close() })
	for _, db := range []string{"migimp_native", "migimp_agent"} {
		if _, err := admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", db)); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(fmt.Sprintf("CREATE DATABASE `%s`", db)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", db))
		})
		for _, table := range []string{"dept", "emp"} {
			if _, err := admin.Exec(fmt.Sprintf("CREATE TABLE `%s`.`%s` LIKE `migsrc_chan`.`%s`", db, table, table)); err != nil {
				t.Fatal(err)
			}
		}
	}

	tgtDSN := strings.TrimSuffix(mysqlRoot, "/") + "/" // go-sql-driver 要求 /dbname 形式
	runImport := func(channel, tgtDB string) {
		cfgPath := writeCLICfg(t, channelImportYAML(t, srcDSN, tgtDSN, tgtDB, csvDir, channel))
		if err := runCLI(t, "import", "-c", cfgPath); err != nil {
			t.Fatalf("import channel=%s: %v", channel, err)
		}
	}
	runImport("native", "migimp_native")
	runImport("agent", "migimp_agent")

	for _, table := range []string{"dept", "emp"} {
		dn := dumpTargetTable(t, admin, "migimp_native", table)
		da := dumpTargetTable(t, admin, "migimp_agent", table)
		if dn == "" {
			t.Fatalf("%s: native target empty", table)
		}
		if dn != da {
			t.Fatalf("%s: target content differs between channels\nnative: %q\nagent: %q", table, dn, da)
		}
		t.Logf("%s parity ok (%d bytes)", table, len(dn))
	}
}
