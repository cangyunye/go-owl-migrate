package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"gopkg.in/yaml.v3"
)

// TestInitFullTemplateAdvancedOptions 验证 init 生成的配置：
//  1. filters_check 作为真默认值出现在 export 段（键存在，注释附着）；
//  2. 文尾高级选项块覆盖 filters/columns/column_types/column_datetime_formats；
//  3. 生成文件能被 config.Load 完整载入（模板改动不得破坏解析）。
func TestInitFullTemplateAdvancedOptions(t *testing.T) {
	cfg := buildFullConfig("database", "oracle", "oracle://u:p@h:1521/SVC", "SCOTT",
		"postgres", "host=h port=5432 user=u dbname=d", "public", "", "")
	out := filepath.Join(t.TempDir(), "migrate.yaml")
	if err := writeConfig(cfg, out); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(data)

	if !strings.Contains(text, "filters_check: count") {
		t.Error("模板缺 filters_check 默认值")
	}
	for _, frag := range []string{
		"#   filters:", "SCOTT.EMP", "deptno = 20",
		"#   columns:", "include:", "rename:", "SAL: salary",
		"column_types:", "column_datetime_formats:",
		"docs/filtered-export.md",
	} {
		if !strings.Contains(text, frag) {
			t.Errorf("高级选项块缺 %q", frag)
		}
	}
	// 高级选项块全部是注释行——绝不能引入顶级键重复或解析错误
	if _, err := config.Load(out); err != nil {
		t.Fatalf("生成的配置必须能被 config.Load 载入: %v", err)
	}
	// 默认不带 filters（示例仅存在于注释中），迁移行为不被 init 改变
	loaded, err := config.Load(out)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Export.Filters) != 0 {
		t.Errorf("init 不得默认启用 filters: %+v", loaded.Export.Filters)
	}
	if loaded.Export.FiltersCheck != "count" {
		t.Errorf("filters_check = %q, want count", loaded.Export.FiltersCheck)
	}
}

// TestConfigRoundTripNewFields：新字段 marshal → unmarshal 全保真。
func TestConfigRoundTripNewFields(t *testing.T) {
	src := config.Config{
		Export: config.ExportConfig{
			Filters:      map[string]string{"SCOTT.EMP": "deptno = 20", "*.LOG_*": "created >= DATE '2026-01-01'"},
			FiltersCheck: "off",
			Columns: config.ExportColumnsConfig{
				Include: map[string][]string{"SCOTT.EMP": {"empno", "sal", "ename"}},
				Rename:  map[string]map[string]string{"SCOTT.EMP": {"SAL": "salary"}},
			},
		},
		DDL: config.DDLConfig{
			ColumnTypes: map[string]string{"SCOTT.EMP.SAL": "number(10,2)"},
		},
	}
	intermediate, err := src.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	raw, err := yaml.Marshal(intermediate)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	var back config.Config
	if err := yaml.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Export.Filters["SCOTT.EMP"] != "deptno = 20" || back.Export.FiltersCheck != "off" {
		t.Errorf("filters round trip: %+v", back.Export)
	}
	if len(back.Export.Columns.Include["SCOTT.EMP"]) != 3 ||
		back.Export.Columns.Rename["SCOTT.EMP"]["SAL"] != "salary" {
		t.Errorf("columns round trip: %+v", back.Export.Columns)
	}
	if back.DDL.ColumnTypes["SCOTT.EMP.SAL"] != "number(10,2)" {
		t.Errorf("column_types round trip: %+v", back.DDL.ColumnTypes)
	}
}

// TestSplitWhereList：CLI --where 语法与消毒。
func TestSplitWhereList(t *testing.T) {
	got, err := splitWhereList("SCOTT.EMP: deptno = 20, *.LOG_*: created >= DATE '2026-01-01'")
	if err != nil {
		t.Fatalf("splitWhereList: %v", err)
	}
	if got["SCOTT.EMP"] != "deptno = 20" {
		t.Errorf("exact entry: %q", got["SCOTT.EMP"])
	}
	// 片段内部的冒号不截断（日期时间等）
	if got["*.LOG_*"] != "created >= DATE '2026-01-01'" {
		t.Errorf("colon inside fragment: %q", got["*.LOG_*"])
	}
	for _, bad := range []string{
		"no-colon-entry",
		"SCOTT.EMP: ",
		": fragment",
		"SCOTT.EMP: a = 1; drop table x", // 消毒拒绝
	} {
		if _, err := splitWhereList(bad); err == nil {
			t.Errorf("must reject %q", bad)
		}
	}
}
