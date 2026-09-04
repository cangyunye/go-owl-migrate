package cmd

import (
	"reflect"
	"testing"
)

// recommendSchemaMapping：确定源/目标后默认推荐用户映射的 golden 用例。
func TestRecommendSchemaMapping(t *testing.T) {
	tests := []struct {
		name      string
		srcSchema string
		tgtSchema string
		tgtType   string
		want      map[string]string
	}{
		{"mysql→ob-oracle 同名推荐", "migsrc_mysql", "", "oceanbase-oracle",
			map[string]string{"migsrc_mysql": "migsrc_mysql"}},
		{"pg→ob-oracle 显式目标用户", "src_hr", "MIG_PG_HR", "oceanbase-oracle",
			map[string]string{"src_hr": "MIG_PG_HR"}},
		{"oracle→postgres 缺省 public 惯例由用户显式给", "SCOTT", "public", "postgres",
			map[string]string{"SCOTT": "public"}},
		{"无源 schema → 空", "", "", "oceanbase-oracle", nil},
		{"嵌入式目标 → 空", "main", "", "duckdb", nil},
		{"ob-mysql 目标同名", "migsrc_obm", "", "oceanbase-mysql",
			map[string]string{"migsrc_obm": "migsrc_obm"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recommendSchemaMapping(tt.srcSchema, tt.tgtSchema, tt.tgtType)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("recommendSchemaMapping(%q,%q,%q) = %v, want %v",
					tt.srcSchema, tt.tgtSchema, tt.tgtType, got, tt.want)
			}
		})
	}
}

// buildMigrateConfig：生成的 migrate 配置必须携带推荐映射 + 目标 schema。
func TestBuildMigrateConfigRecommendedMapping(t *testing.T) {
	cfg := buildMigrateConfig("postgres", "dsn-x", "src_hr", "oceanbase-oracle", "dsn-y", "")
	if cfg.Target.Schema != "src_hr" {
		t.Errorf("Target.Schema = %q, want src_hr (缺省推荐同名)", cfg.Target.Schema)
	}
	if cfg.DDL.SchemaMapping["src_hr"] != "src_hr" {
		t.Errorf("SchemaMapping = %v, want {src_hr: src_hr}", cfg.DDL.SchemaMapping)
	}
	if cfg.DDL.TargetDialect != "oceanbase-oracle" {
		t.Errorf("TargetDialect = %q", cfg.DDL.TargetDialect)
	}
}
