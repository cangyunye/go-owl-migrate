//go:build e2e

// CLI 命令本体 × 真库冒烟（补"同函数经 pipeline 已验证、命令本体未跑"缺口）：
// export data / export ddl / gen-select / validate 各以真实命令行对真库执行，
// 断言退出码与落盘产物。DSN 走 env/.local-dev.env（凭据不入库）。
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI 以给定参数在进程内执行根命令（等价于 `owl-migrate ...`）。
func runCLI(t *testing.T, args ...string) error {
	t.Helper()
	rootCmd.SetArgs(args)
	return rootCmd.Execute()
}

// writeCLICfg 写临时 yaml 配置并返回路径。
func writeCLICfg(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cli-smoke.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// mysqlCLIYAML 生成 mysql 源（真库）的命令配置。
func mysqlCLIYAML(t *testing.T, dsn, schema string) string {
	t.Helper()
	return fmt.Sprintf(`general:
  log_level: info
metadata:
  type: database
source:
  type: mysql
  dsn: %q
  schema: %q
ddl:
  target_dialect: oceanbase-oracle
  schema_mapping:
    %s: MIG_MYSQL
  include_comments: true
  include_if_not_exists: true
export:
  output_dir: ./output/data/
  format: csv
  csv:
    delimiter: ","
    quote_char: "\""
    header: true
    null_representation: "\\N"
  batch:
    page_size: 5000
  parallel:
    enabled: true
    max_workers: 2
  tables:
    include: ["*"]
`, dsn, schema, schema)
}

// TestCLISmoke_MySQLCommands：mysql 真库上 export data/ddl、gen-select、validate 本体执行。
func TestCLISmoke_MySQLCommands(t *testing.T) {
	env := devEnv(t)
	mysqlRoot := devGet(t, env, "OWL_E2E_MYSQL_DSN")
	src := seedMySQLSource(t, "mysql", mysqlRoot, "migsrc_mysql")
	t.Cleanup(func() { src.Close() })

	dsn := mysqlRoot + "migsrc_mysql"
	cfgPath := writeCLICfg(t, mysqlCLIYAML(t, dsn, "migsrc_mysql"))

	t.Run("export_data", func(t *testing.T) {
		out := t.TempDir()
		if err := runCLI(t, "export", "data", "-c", cfgPath, "-o", out, "--format", "csv"); err != nil {
			t.Fatalf("export data: %v", err)
		}
		entries, _ := os.ReadDir(out)
		var csvs []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".csv") {
				csvs = append(csvs, e.Name())
			}
		}
		if len(csvs) < 2 {
			t.Fatalf("export data produced %d csv, want >= 2 (dept/emp): %v", len(csvs), csvs)
		}
		var deptOK bool
		for _, n := range csvs {
			b, err := os.ReadFile(filepath.Join(out, n))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "deptno") && strings.Count(string(b), "\n") >= 3 {
				deptOK = true
			}
		}
		if !deptOK {
			t.Errorf("no dept csv with header+2 rows among %v", csvs)
		}
	})

	t.Run("export_ddl", func(t *testing.T) {
		out := t.TempDir()
		if err := runCLI(t, "export", "ddl", "-c", cfgPath, "-o", out); err != nil {
			t.Fatalf("export ddl: %v", err)
		}
		entries, _ := os.ReadDir(out)
		if len(entries) == 0 {
			t.Fatal("export ddl produced no files")
		}
		var joined strings.Builder
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(out, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			joined.Write(b)
		}
		lw := strings.ToLower(joined.String())
		if !strings.Contains(lw, "create table") || !strings.Contains(lw, `"mig_mysql"."dept"`) {
			t.Errorf("ddl files missing mapped create table:\n%.600s", joined.String())
		}
	})

	t.Run("gen_select", func(t *testing.T) {
		out := t.TempDir()
		if err := runCLI(t, "gen-select", "-c", cfgPath, "-o", out, "--batch-method", "offset", "-n", "10"); err != nil {
			t.Fatalf("gen-select: %v", err)
		}
		entries, _ := os.ReadDir(out)
		if len(entries) != 2 {
			t.Fatalf("gen-select produced %d files, want 2 (dept/emp): %v", len(entries), namesOf(entries))
		}
		var selOK bool
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(out, e.Name()))
			if strings.Contains(strings.ToUpper(string(b)), "SELECT") {
				selOK = true
			}
		}
		if !selOK {
			t.Error("gen-select files missing SELECT content")
		}
	})

	t.Run("validate", func(t *testing.T) {
		if err := runCLI(t, "validate", "-c", cfgPath); err != nil {
			t.Fatalf("validate on live mysql model: %v", err)
		}
	})
}

// TestCLISmoke_OBOracleCommands：OB-Oracle 真库上 export ddl / gen-select 本体执行。
func TestCLISmoke_OBOracleCommands(t *testing.T) {
	env := devEnv(t)
	obDsn := devGet(t, env, "OWL_E2E_OB_ORACLE_DSN")
	pw := devGet(t, env, "OWL_E2E_DEV_PW")
	_, _, srcDSN := ensureMIGSRCWithObjects(t, obDsn, pw)

	cfgPath := writeCLICfg(t, fmt.Sprintf(`general:
  log_level: info
metadata:
  type: database
source:
  type: oceanbase-oracle
  dsn: %q
  schema: MIGSRC
ddl:
  target_dialect: oceanbase-oracle
  include_comments: true
  include_if_not_exists: true
export:
  tables:
    include: ["*"]
`, srcDSN))

	t.Run("export_ddl", func(t *testing.T) {
		out := t.TempDir()
		if err := runCLI(t, "export", "ddl", "-c", cfgPath, "-o", out); err != nil {
			t.Fatalf("export ddl (ob): %v", err)
		}
		entries, _ := os.ReadDir(out)
		if len(entries) == 0 {
			t.Fatal("export ddl (ob) produced no files")
		}
		var joined strings.Builder
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(out, e.Name()))
			joined.Write(b)
		}
		if !strings.Contains(joined.String(), `"MIGSRC"."EMP"`) {
			t.Errorf("ob ddl missing \"MIGSRC\".\"EMP\":\n%.600s", joined.String())
		}
	})

	t.Run("gen_select", func(t *testing.T) {
		out := t.TempDir()
		if err := runCLI(t, "gen-select", "-c", cfgPath, "-o", out, "--batch-method", "cursor", "-n", "10"); err != nil {
			t.Fatalf("gen-select (ob): %v", err)
		}
		entries, _ := os.ReadDir(out)
		if len(entries) != 2 {
			t.Fatalf("gen-select (ob) produced %d files, want 2: %v", len(entries), namesOf(entries))
		}
		var hasEMP bool
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(out, e.Name()))
			if strings.Contains(string(b), `"MIGSRC"."EMP"`) {
				hasEMP = true
			}
		}
		if !hasEMP {
			t.Error("ob gen-select missing \"MIGSRC\".\"EMP\"")
		}
	})
}

func namesOf(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
