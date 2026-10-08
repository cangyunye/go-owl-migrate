//go:build sqlite3

package cmd

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3" // sqlite3 方言的 CGo 驱动，dbconn.Open 经方言系统选中
)

// preflight × 真实 sqlite3 端到端：CSV 元数据描述的表在真库上可读、
// --skip-target 生效、schema_mapping 未覆盖告警。默认 tag 下 sqlite3 驱动
// 未链入，plancheck 逻辑分支由 plancheck 包测试经缝隙覆盖；本文件锁定
// dbconn.Open → 方言引用 → 真实探针的完整链路（make test/full 跑）。

// seedSQLite3File 建一个种好表的 sqlite3 文件库并返回路径。
func seedSQLite3File(t *testing.T, tables ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open sqlite3: %v", err)
	}
	for _, tbl := range tables {
		if _, err := db.Exec("CREATE TABLE " + tbl + " (id integer primary key)"); err != nil {
			t.Fatalf("create %s: %v", tbl, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return path
}

// TestPreflightHappyPathSQLite3：导出场景全绿——csv 元数据两张表在真库上
// 全部可读，无目标侧检查，预检通过。
func TestPreflightHappyPathSQLite3(t *testing.T) {
	csvDir := writePreflightCSV(t, "SCOTT,EMP,TABLE,emp", "SCOTT,DEPT,TABLE,dept")
	src := seedSQLite3File(t, "EMP", "DEPT")
	outDir := filepath.Join(t.TempDir(), "out")

	yaml := `general:
  log_level: error
metadata:
  type: csv
  csv:
    path: ` + csvDir + `
source:
  type: sqlite3
  dsn: ` + src + `
ddl:
  target_dialect: postgres
export:
  output_dir: ` + outDir + `
  format: csv
  tables:
    include: ["SCOTT.*"]
`
	out, err := runPreflight(t, writePreflightYAML(t, yaml))
	if err != nil {
		t.Fatalf("preflight should pass, got %v, stdout:\n%s", err, out)
	}
	for _, want := range []string{"✓", "元数据↔源库一致性", "2 张表", "Preflight passed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got:\n%s", want, out)
		}
	}
}

// TestPreflightSkipTargetFlag：同一份 migrate 形态的配置，不带旗标时目标
// 建表探针运行；带 --skip-target 时被跳过——旗标的效果必须有对照地锁定。
func TestPreflightSkipTargetFlag(t *testing.T) {
	csvDir := writePreflightCSV(t, "SCOTT,EMP,TABLE,emp")
	src := seedSQLite3File(t, "EMP")
	tgt := seedSQLite3File(t)

	yaml := `general:
  log_level: error
metadata:
  type: csv
  csv:
    path: ` + csvDir + `
source:
  type: sqlite3
  dsn: ` + src + `
target:
  type: sqlite3
  dsn: ` + tgt + `
  schema: main
ddl:
  target_dialect: postgres
  schema_mapping:
    SCOTT: main
export:
  output_dir: ` + filepath.Join(t.TempDir(), "out") + `
  tables:
    include: ["SCOTT.*"]
`
	cfgPath := writePreflightYAML(t, yaml)

	out, err := runPreflight(t, cfgPath)
	if err != nil || !strings.Contains(out, "目标建表权限") {
		t.Errorf("without the flag the target probe must run and pass, err=%v out:\n%s", err, out)
	}

	out, err = runPreflight(t, cfgPath, "--skip-target")
	if err != nil {
		t.Fatalf("--skip-target must still pass, got %v, stdout:\n%s", err, out)
	}
	if strings.Contains(out, "目标建表权限") {
		t.Errorf("--skip-target must omit the target probe, got:\n%s", out)
	}
}

// TestPreflightMappingCoverageWarnSQLite3：元数据 schema 未出现在
// schema_mapping 中 → ⚠ 不阻断，但 detail 给出可粘贴的映射建议。
func TestPreflightMappingCoverageWarnSQLite3(t *testing.T) {
	csvDir := writePreflightCSV(t, "SCOTT,EMP,TABLE,emp")
	src := seedSQLite3File(t, "EMP")
	tgt := seedSQLite3File(t)

	yaml := `general:
  log_level: error
metadata:
  type: csv
  csv:
    path: ` + csvDir + `
source:
  type: sqlite3
  dsn: ` + src + `
target:
  type: sqlite3
  dsn: ` + tgt + `
  schema: main
ddl:
  target_dialect: postgres
export:
  output_dir: ` + filepath.Join(t.TempDir(), "out") + `
  tables:
    include: ["SCOTT.*"]
`
	out, err := runPreflight(t, writePreflightYAML(t, yaml))
	if err != nil {
		t.Fatalf("mapping warn must not block, got %v, stdout:\n%s", err, out)
	}
	if !strings.Contains(out, "⚠ Schema 映射覆盖") || !strings.Contains(out, "schema_mapping: {SCOTT:") {
		t.Errorf("mapping coverage warning with suggestion expected, got:\n%s", out)
	}
	if !strings.Contains(out, "Preflight passed") {
		t.Errorf("warn-level report should still pass, got:\n%s", out)
	}
}
