package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/plancheck"
)

// preflight CLI 的进程内测试：锁定只读预检的对外契约——退出码语义
// （阻断检查失败 → RunE 返回 error → 主程序退出码 1）、export 场景自动
// 跳过目标侧、TRUNCATE 破坏性警告透传、三态渲染。真实 sqlite3 端到端
// 链路见 preflight_sqlite3_test.go（-tags sqlite3）。

// captureStdout 捕获直写 os.Stdout 的输出。preflight 的 ✓/⚠/✗ 渲染走
// fmt.Printf 而非 cobra 的 SetOut，必须换流才能断言。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	w.Close()
	os.Stdout = old
	out := <-done
	r.Close()
	return out
}

// runPreflight 在进程内执行 preflight 子命令并返回（stdout, error）。
// cfgFile 直接注入包变量而非走 -c 旗标——preflightCmd 单独构造，不挂
// root 的持久旗标，旗标解析路径由 root 级冒烟覆盖。
func runPreflight(t *testing.T, cfgPath string, args ...string) (string, error) {
	t.Helper()
	old := cfgFile
	cfgFile = cfgPath
	t.Cleanup(func() { cfgFile = old })

	var (
		out     string
		execErr error
	)
	out = captureStdout(t, func() {
		cmd := preflightCmd()
		cmd.SetArgs(args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		execErr = cmd.Execute()
	})
	return out, execErr
}

// writePreflightYAML 写临时 yaml 配置并返回路径。
func writePreflightYAML(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "preflight.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// writePreflightCSV 写一个最小 csv 元数据目录（tables.csv + columns.csv，
// 与 testdata/csv 同构）并返回目录路径。
func writePreflightCSV(t *testing.T, rows ...string) string {
	t.Helper()
	dir := t.TempDir()
	tables := "TABLE_SCHEMA,TABLE_NAME,TABLE_TYPE,TABLE_COMMENT\n" + strings.Join(rows, "\n") + "\n"
	cols := "TABLE_SCHEMA,TABLE_NAME,COLUMN_NAME,ORDINAL_POSITION,DATA_TYPE,NULLABLE\n"
	for _, r := range rows {
		parts := strings.Split(r, ",")
		cols += parts[0] + "," + parts[1] + ",ID,1,NUMBER,YES\n"
	}
	for name, content := range map[string]string{"tables.csv": tables, "columns.csv": cols} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// unreachableMySQLYAML: mysql 源指向必拒端口（127.0.0.1:1）——mysql 驱动
// 恒被链入（基础方言），"连接被拒"在默认与 sqlite3 两种 tag 下行为一致。
func unreachableMySQLYAML(csvDir string) string {
	return `general:
  log_level: error
metadata:
  type: csv
  csv:
    path: ` + csvDir + `
source:
  type: mysql
  dsn: "root:pw@tcp(127.0.0.1:1)/none"
  schema: SCOTT
ddl:
  target_dialect: postgres
export:
  output_dir: ./output/data/
  format: csv
  tables:
    include: ["SCOTT.*"]
`
}

// 元数据目录缺失必须在进入任何检查前报错，且错误链带上 metadata: 前缀。
func TestPreflightMetadataLoadFailure(t *testing.T) {
	yaml := `general:
  log_level: error
metadata:
  type: csv
  csv:
    path: ` + filepath.Join(t.TempDir(), "no-such-dir") + `
source:
  type: mysql
  dsn: "root:pw@tcp(127.0.0.1:1)/none"
  schema: SCOTT
ddl:
  target_dialect: postgres
`
	out, err := runPreflight(t, writePreflightYAML(t, yaml))
	if err == nil {
		t.Fatalf("missing metadata dir must error, stdout:\n%s", out)
	}
	if !strings.Contains(err.Error(), "metadata:") {
		t.Errorf("error should carry the metadata: prefix, got: %v", err)
	}
}

// 源不可达（元数据描述的表一张都探不到）→ 阻断失败：RunE 返回包含
// "plan preflight failed" 的 error（主程序据此退出码 1），stdout 渲染 ✗。
func TestPreflightSourceUnreachableFails(t *testing.T) {
	csvDir := writePreflightCSV(t, "SCOTT,EMP,TABLE,emp", "SCOTT,DEPT,TABLE,dept")
	out, err := runPreflight(t, writePreflightYAML(t, unreachableMySQLYAML(csvDir)))
	if err == nil {
		t.Fatalf("unreachable source must fail the preflight, stdout:\n%s", out)
	}
	if !strings.Contains(err.Error(), "plan preflight failed") {
		t.Errorf("error should carry the blocking message, got: %v", err)
	}
	if !strings.Contains(out, "✗") || !strings.Contains(out, "元数据↔源库一致性") {
		t.Errorf("stdout should render the failed check, got:\n%s", out)
	}
}

// export 场景（无目标库）自动跳过目标侧检查——输出里不能出现"目标建表权限"，
// 这是 --skip-target 之外的隐式语义，防导出用户被无关告警打扰。
func TestPreflightExportScenarioSkipsTarget(t *testing.T) {
	csvDir := writePreflightCSV(t, "SCOTT,EMP,TABLE,emp")
	out, _ := runPreflight(t, writePreflightYAML(t, unreachableMySQLYAML(csvDir)))
	if strings.Contains(out, "目标建表权限") {
		t.Errorf("export scenario must skip target-side checks, got:\n%s", out)
	}
	if !strings.Contains(out, "元数据↔源库一致性") {
		t.Errorf("source-side check should still run, got:\n%s", out)
	}
}

// import.target.truncate_before 是破坏性操作，预检必须原样透传 TRUNCATE
// 警告（与 migrate Step 2、web 预检同一份文案）。
func TestPreflightTruncateWarning(t *testing.T) {
	yaml := `general:
  log_level: error
metadata:
  type: csv
  csv:
    path: ` + writePreflightCSV(t, "SCOTT,EMP,TABLE,emp") + `
source:
  type: mysql
  dsn: "root:pw@tcp(127.0.0.1:1)/none"
  schema: SCOTT
target:
  type: sqlite3
  dsn: ` + filepath.Join(t.TempDir(), "target.db") + `
import:
  target:
    truncate_before: true
`
	out, err := runPreflight(t, writePreflightYAML(t, yaml))
	if err == nil {
		t.Fatalf("source is unreachable, preflight must fail, stdout:\n%s", out)
	}
	if !strings.Contains(out, "TRUNCATE") {
		t.Errorf("truncate warning must be rendered, got:\n%s", out)
	}
}

// printPlanChecks 三态渲染：✗ / ⚠ / ✓ 与 Warns 逐行原样输出——这是 CLI
// 用户判读预检结果的唯一界面。
func TestPrintPlanChecksRendersThreeStates(t *testing.T) {
	report := &plancheck.Report{
		Checks: []plancheck.Check{
			{Name: "检查A", Status: plancheck.StatusFail, Detail: "坏了"},
			{Name: "检查B", Status: plancheck.StatusWarn, Detail: "提醒"},
			{Name: "检查C", Status: plancheck.StatusPass, Detail: "好的"},
		},
		Warns: []string{"额外警告"},
	}
	out := captureStdout(t, func() { printPlanChecks(report) })
	for _, want := range []string{
		"✗ 检查A: 坏了",
		"⚠ 检查B: 提醒",
		"✓ 检查C: 好的",
		"⚠ 额外警告",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got:\n%s", want, out)
		}
	}
	// 三种状态各自成行，fail 不吞掉后续检查的渲染。
	if got := strings.Count(out, "\n"); got < 4 {
		t.Errorf("each check and warn should render on its own line, got %d lines:\n%s", got, out)
	}
}
