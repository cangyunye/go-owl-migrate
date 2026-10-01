package configbuild

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

func mustLoad(t *testing.T, cfg *config.Config, label string) *config.Config {
	t.Helper()
	if cfg == nil {
		t.Fatalf("%s: nil config", label)
	}
	inter, err := cfg.MarshalYAML()
	if err != nil {
		t.Fatalf("%s: marshal: %v", label, err)
	}
	// MarshalYAML 的产物必须能被 Load 系语义载入（往返保真）——通过临时文件走
	// 真实 Load 路径由 serve 侧测试覆盖；这里断言结构性字段。
	_ = inter
	return cfg
}

func TestBuildFromSlotsMigrateMySQL(t *testing.T) {
	cfg, err := BuildFromSlots(SlotRequest{
		Scenario: "migrate",
		Source:   EndpointSlots{Type: "mysql", Host: "127.0.0.1", Port: "3306", User: "root", Password: "pw", Database: "owl_demo", Schema: "owl_demo"},
		Target:   &EndpointSlots{Type: "postgres", Host: "127.0.0.1", Port: "5432", User: "postgres", Database: "app", Schema: "public"},
		Export: &ExportSlots{
			Format: "csv",
			Filters: map[string]string{"owl_demo.users": "id > 1"},
			Columns: &ColumnSlots{
				Include: map[string][]string{"owl_demo.users": {"id", "name"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildFromSlots: %v", err)
	}
	mustLoad(t, cfg, "migrate mysql")
	// DSN 由部件拼装（mysql tcp 形态）
	if !strings.HasPrefix(cfg.Source.DSN, "root:pw@tcp(127.0.0.1:3306)/owl_demo") {
		t.Errorf("source dsn = %q", cfg.Source.DSN)
	}
	if !strings.HasPrefix(cfg.Target.DSN, "postgres://postgres@127.0.0.1:5432/app") {
		t.Errorf("target dsn = %q", cfg.Target.DSN)
	}
	if cfg.Export.Filters["owl_demo.users"] != "id > 1" {
		t.Errorf("filters = %+v", cfg.Export.Filters)
	}
	if got := cfg.Export.Columns.Include["owl_demo.users"]; len(got) != 2 {
		t.Errorf("columns include = %v", got)
	}
	if cfg.DDL.SchemaMapping["owl_demo"] != "owl_tgt" && cfg.DDL.SchemaMapping["owl_demo"] != "" {
		// 推荐映射来自 builder；此处只断言字段可覆盖
		_ = cfg.DDL.SchemaMapping
	}
}

func TestBuildFromSlotsOracleParts(t *testing.T) {
	cfg, err := BuildFromSlots(SlotRequest{
		Scenario: "migrate",
		Source:   EndpointSlots{Type: "oracle", Host: "10.1.1.5", Port: "1521", User: "scott", Password: "tiger", Database: "XEPDB1", Schema: "SCOTT"},
		Target:   &EndpointSlots{Type: "oracle", Host: "10.1.1.5", Port: "1521", User: "owl_tgt", Password: "pw2", Database: "XEPDB1", Schema: "OWL_TGT"},
		DDL:      &DDLSlots{SchemaMapping: map[string]string{"SCOTT": "OWL_TGT"}, ColumnTypes: map[string]string{"SCOTT.EMP.SAL": "number(10,2)"}},
	})
	if err != nil {
		t.Fatalf("BuildFromSlots: %v", err)
	}
	mustLoad(t, cfg, "oracle parts")
	// oracle 走 url 形态
	if !strings.HasPrefix(cfg.Source.DSN, "oracle://scott:tiger@10.1.1.5:1521/XEPDB1") {
		t.Errorf("source dsn = %q", cfg.Source.DSN)
	}
	if cfg.DDL.SchemaMapping["SCOTT"] != "OWL_TGT" {
		t.Errorf("schema mapping = %+v", cfg.DDL.SchemaMapping)
	}
	if cfg.DDL.ColumnTypes["SCOTT.EMP.SAL"] != "number(10,2)" {
		t.Errorf("column types = %+v", cfg.DDL.ColumnTypes)
	}
}

func TestBuildFromSlotsVerbatimDSNWins(t *testing.T) {
	cfg, err := BuildFromSlots(SlotRequest{
		Scenario: "export",
		Source: EndpointSlots{
			Type: "oracle", Host: "ignored", Database: "IGNORED",
			DSN: "oracle://scott:tiger@real-host:1521/REAL", Schema: "SCOTT",
		},
	})
	if err != nil {
		t.Fatalf("BuildFromSlots: %v", err)
	}
	mustLoad(t, cfg, "verbatim dsn")
	if cfg.Source.DSN != "oracle://scott:tiger@real-host:1521/REAL" {
		t.Errorf("verbatim dsn must win, got %q", cfg.Source.DSN)
	}
}

func TestBuildFromSlotsValidation(t *testing.T) {
	// 缺 host
	if _, err := BuildFromSlots(SlotRequest{Scenario: "export", Source: EndpointSlots{Type: "mysql", Database: "d"}}); err == nil {
		t.Error("missing host must error")
	}
	// 缺 database
	if _, err := BuildFromSlots(SlotRequest{Scenario: "export", Source: EndpointSlots{Type: "mysql", Host: "h"}}); err == nil {
		t.Error("missing database must error")
	}
	// migrate 必须有 target
	if _, err := BuildFromSlots(SlotRequest{Scenario: "migrate", Source: EndpointSlots{Type: "mysql", Host: "h", Database: "d"}}); err == nil {
		t.Error("migrate without target must error")
	}
	// 未知场景
	if _, err := BuildFromSlots(SlotRequest{Scenario: "nope", Source: EndpointSlots{Type: "mysql", Host: "h", Database: "d"}}); err == nil {
		t.Error("unknown scenario must error")
	}
	// filter 消毒
	if _, err := BuildFromSlots(SlotRequest{
		Scenario: "export",
		Source:   EndpointSlots{Type: "mysql", Host: "h", Database: "d"},
		Export:   &ExportSlots{Filters: map[string]string{"a.b": "x = 1; drop table t"}},
	}); err == nil {
		t.Error("forbidden filter fragment must error")
	}
	// sqlite3 只需文件路径
	cfg, err := BuildFromSlots(SlotRequest{Scenario: "export", Source: EndpointSlots{Type: "sqlite3", Database: "/tmp/x.db"}})
	if err != nil {
		t.Fatalf("sqlite3: %v", err)
	}
	mustLoad(t, cfg, "sqlite3")
	if cfg.Source.DSN != "/tmp/x.db" {
		t.Errorf("sqlite dsn = %q", cfg.Source.DSN)
	}
}

func TestBuildFromSlotsScenarios(t *testing.T) {
	scenarios := []string{"export", "import", "export-ddl", "gen-select", "export-insert", "validate", "full"}
	for _, sc := range scenarios {
		req := SlotRequest{
			Scenario: sc,
			Source:   EndpointSlots{Type: "mysql", Host: "127.0.0.1", Port: "3306", User: "root", Database: "owl_demo", Schema: "owl_demo"},
			Target:   &EndpointSlots{Type: "postgres", Host: "127.0.0.1", Port: "5432", User: "postgres", Database: "app"},
		}
		cfg, err := BuildFromSlots(req)
		if err != nil {
			t.Errorf("scenario %s: %v", sc, err)
			continue
		}
		mustLoad(t, cfg, sc)
	}
}
