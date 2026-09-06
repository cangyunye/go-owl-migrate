//go:build e2e

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── M3：产品级 CLI 双通道对拍 ──
// 同一份 MySQL fixture，export-metadata / export data 分别以 channel=native
// 与 channel=agent（owljdbc → JVM sidecar）完整跑一遍产品命令，产物逐字节
// 一致。这是「保底通道在产品命令上行为等价」的验收（阶段二计划 M3）。

// channelCLIYAML 生成带通道选择的 mysql 源命令配置。agent jar 路径相对
// internal/cmd（runCLI 进程内执行，cwd = 包目录）。
func channelCLIYAML(t *testing.T, dsn, schema, channel string) string {
	t.Helper()
	agentBlock := ""
	if channel == "agent" || channel == "auto" {
		agentBlock = `
agent:
  jars_dir: "../../"
  agent_jar: "../../jvm/owl-agent/owl-agent.jar"`
	}
	ch := ""
	if channel != "" {
		ch = "\n  channel: " + channel
	}
	return `general:
  log_level: info
metadata:
  type: database
ddl:
  target_dialect: oceanbase-oracle
  schema_mapping:
    ` + schema + `: MIG_CHAN
source:` + ch + `
  type: mysql
  dsn: "` + dsn + `"
  schema: "` + schema + `"` + agentBlock + `
export:
  format: csv
  csv:
    delimiter: ","
    quote_char: "\""
    header: true
    null_representation: "\\N"
  batch:
    page_size: 500
  tables:
    include: ["*"]
`
}

func csvDirContents(t *testing.T, dir string) map[string][]byte {
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

func assertCSVDirsEqual(t *testing.T, want, got map[string][]byte, label string) {
	t.Helper()
	if len(want) == 0 {
		t.Fatal("baseline csv dir is empty")
	}
	for name, bw := range want {
		bg, ok := got[name]
		if !ok {
			t.Fatalf("%s: missing csv %s", label, name)
		}
		if string(bw) != string(bg) {
			t.Fatalf("%s: csv bytes differ: %s (native %d B, %s %d B)", label, name, len(bw), label, len(bg))
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%s: csv file count %d, want %d", label, len(got), len(want))
	}
}

func TestE2E_CLI_ChannelParity(t *testing.T) {
	env := devEnv(t)
	mysqlRoot := devGet(t, env, "OWL_E2E_MYSQL_DSN")
	src := seedMySQLSource(t, "mysql", mysqlRoot, "migsrc_chan")
	t.Cleanup(func() { src.Close() })
	dsn := mysqlRoot + "migsrc_chan"

	nativeCfg := writeCLICfg(t, channelCLIYAML(t, dsn, "migsrc_chan", "native"))
	agentCfg := writeCLICfg(t, channelCLIYAML(t, dsn, "migsrc_chan", "agent"))

	t.Run("export_metadata", func(t *testing.T) {
		outN := t.TempDir()
		outA := t.TempDir()
		if err := runCLI(t, "export-metadata", "-c", nativeCfg, "--format", "csv", "--scope", "schema:migsrc_chan", "-o", outN); err != nil {
			t.Fatalf("native export-metadata: %v", err)
		}
		if err := runCLI(t, "export-metadata", "-c", agentCfg, "--format", "csv", "--scope", "schema:migsrc_chan", "-o", outA); err != nil {
			t.Fatalf("agent export-metadata: %v", err)
		}
		assertCSVDirsEqual(t, csvDirContents(t, outN), csvDirContents(t, outA), "agent export-metadata")
	})

	t.Run("export_data", func(t *testing.T) {
		outN := t.TempDir()
		outA := t.TempDir()
		if err := runCLI(t, "export", "data", "-c", nativeCfg, "-o", outN, "--format", "csv"); err != nil {
			t.Fatalf("native export data: %v", err)
		}
		if err := runCLI(t, "export", "data", "-c", agentCfg, "-o", outA, "--format", "csv"); err != nil {
			t.Fatalf("agent export data: %v", err)
		}
		assertCSVDirsEqual(t, csvDirContents(t, outN), csvDirContents(t, outA), "agent export data")
	})
}
