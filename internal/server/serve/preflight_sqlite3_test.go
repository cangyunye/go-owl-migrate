//go:build sqlite3

package serve

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3" // sqlite3 方言的 CGo 驱动，dbconn/service.OpenDB 经方言系统选中

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

// web 预检 × 真实 sqlite3 端到端（make test/full 跑）：全绿路径、schema
// 映射告警进入 warnings、export.filters 条件 COUNT、sql-out 跳过目标与
// 条件 COUNT、目标不可达失败项。元数据 schema 用 "main"——sqlite 的默认
// schema 名，使 quoteSourceIdent 的 "main"."EMP" 与 plancheck 丢弃 schema
// 两条引用路径都命中同一张真表。

// seedSQLite3Source 建源库文件：EMP 带 2 行数据、DEPT 空表。
func seedSQLite3Source(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open sqlite3: %v", err)
	}
	for _, stmt := range []string{
		"CREATE TABLE EMP (id integer primary key, name text)",
		"CREATE TABLE DEPT (id integer primary key)",
		"INSERT INTO EMP (name) VALUES ('a'), ('b')",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return path
}

// preflightServer 组装一份 csv 元数据 + sqlite 源/目标的可预检服务。
func preflightServer(t *testing.T, mutate func(*config.Config)) (*Server, string, string) {
	t.Helper()
	src := seedSQLite3Source(t)
	tgt := filepath.Join(t.TempDir(), "tgt.db")
	srv := newTestServer(t)
	srv.cfg = &config.Config{
		Metadata: config.MetadataConfig{
			Type: "csv",
			CSV:  config.CSVConfig{Path: csvMetaDir(t, "main,EMP,TABLE,emp", "main,DEPT,TABLE,dept")},
		},
		Source: config.DBConfig{Type: "sqlite3", DSN: src, Schema: "main"},
		Target: config.DBConfig{Type: "sqlite3", DSN: tgt, Schema: "main"},
		DDL:    config.DDLConfig{TargetDialect: "postgres", SchemaMapping: map[string]string{"main": "main"}},
		Export: config.ExportConfig{Tables: config.TableListConfig{Include: []string{"main.*"}}},
	}
	if mutate != nil {
		mutate(srv.cfg)
	}
	return srv, src, tgt
}

// 全绿路径：配置、元数据、plancheck 两项、目标连通、目标建表权限全部通过，
// 无警告。
func TestMigratePreflightGreenWithLiveSQLite(t *testing.T) {
	srv, _, _ := preflightServer(t, nil)

	w := doJSON(t, srv, http.MethodPost, "/api/v1/migrate/preflight", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	resp := decodePreflight(t, w.Body.Bytes())
	if !resp.Ok {
		t.Fatalf("preflight should pass, checks: %+v", resp.Checks)
	}
	for _, name := range []string{"配置", "源库/元数据", "元数据↔源库一致性", "Schema 映射覆盖", "目标库", "目标建表权限"} {
		if c, found := checkByName(resp, name); !found || !c.Ok {
			t.Errorf("check %q should exist and pass, got found=%v: %+v", name, found, resp.Checks)
		}
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("green path must carry no warnings, got %v", resp.Warnings)
	}
}

// schema_mapping 未覆盖元数据 schema → 端点整体仍 ok，但警告必须进入
// warnings 数组（前端气泡的数据源）。
func TestMigratePreflightMappingWarnSurfacesInWarnings(t *testing.T) {
	srv, _, _ := preflightServer(t, func(cfg *config.Config) {
		cfg.DDL.SchemaMapping = nil
	})

	w := doJSON(t, srv, http.MethodPost, "/api/v1/migrate/preflight", "")
	resp := decodePreflight(t, w.Body.Bytes())
	if !resp.Ok {
		t.Fatalf("mapping warn must not block, checks: %+v", resp.Checks)
	}
	c, found := checkByName(resp, "Schema 映射覆盖")
	// 端点契约：checks[].ok 的语义是"非阻断"（warn 也是 ok=true），warn 级
	// 的完整信息经 warnings 数组与 detail 呈现。
	if !found || !c.Ok {
		t.Fatalf("mapping warn is non-blocking so ok=true, got found=%v: %+v", found, c)
	}
	if !strings.Contains(c.Detail, "main 未出现在 ddl.schema_mapping") {
		t.Errorf("detail should explain the unmapped schema, got: %s", c.Detail)
	}
	found = false
	for _, warn := range resp.Warnings {
		if strings.Contains(warn, "Schema 映射覆盖") {
			found = true
		}
	}
	if !found {
		t.Errorf("mapping warning must appear in warnings, got %v", resp.Warnings)
	}
}

// export.filters 配置时对每张命中表跑条件 COUNT——语法/列名/权限错误在
// 预检阶段暴露，并给出源侧预期行数。
func TestMigratePreflightFiltersConditionalCount(t *testing.T) {
	srv, _, _ := preflightServer(t, func(cfg *config.Config) {
		cfg.Export.Filters = map[string]string{"main.EMP": "id > 0"}
	})

	w := doJSON(t, srv, http.MethodPost, "/api/v1/migrate/preflight", "")
	resp := decodePreflight(t, w.Body.Bytes())
	if !resp.Ok {
		t.Fatalf("filter count should pass on live data, checks: %+v", resp.Checks)
	}
	c, found := checkByName(resp, "条件导出预检")
	if !found || !c.Ok {
		t.Fatalf("expected passing 条件导出预检 check, got found=%v checks=%+v", found, resp.Checks)
	}
	if !strings.Contains(c.Detail, "main.EMP: 2 行") {
		t.Errorf("detail should report the source-side row count, got: %s", c.Detail)
	}
}

// 过滤条件引用不存在的列 → 条件导出预检失败（把 Step 4 才爆的错提前）。
func TestMigratePreflightFiltersBadColumnFails(t *testing.T) {
	srv, _, _ := preflightServer(t, func(cfg *config.Config) {
		cfg.Export.Filters = map[string]string{"main.EMP": "no_such_col > 0"}
	})

	w := doJSON(t, srv, http.MethodPost, "/api/v1/migrate/preflight", "")
	resp := decodePreflight(t, w.Body.Bytes())
	if resp.Ok {
		t.Fatal("a filter referencing a missing column must fail the preflight")
	}
	if c, found := checkByName(resp, "条件导出预检"); !found || c.Ok {
		t.Errorf("expected failed 条件导出预检 check, got %+v", resp.Checks)
	}
}

// mode=sql-out：不连目标库、跳过条件 COUNT，两者都以警告/说明项呈现且
// 整体 ok——SQL 输出模式没有目标库是常态而非异常。
func TestMigratePreflightSqlOutSkipsTargetAndCount(t *testing.T) {
	srv, _, _ := preflightServer(t, func(cfg *config.Config) {
		cfg.Export.Filters = map[string]string{"main.EMP": "id > 0"}
	})

	w := doJSON(t, srv, http.MethodPost, "/api/v1/migrate/preflight?mode=sql-out", "")
	resp := decodePreflight(t, w.Body.Bytes())
	if !resp.Ok {
		t.Fatalf("sql-out mode should pass without a target, checks: %+v", resp.Checks)
	}
	if c, found := checkByName(resp, "条件导出预检"); found {
		t.Errorf("sql-out must skip the conditional COUNT, got %+v", c)
	}
	c, found := checkByName(resp, "目标库")
	if !found || !c.Ok || !strings.Contains(c.Detail, "跳过") {
		t.Errorf("target check should report a skip, got found=%v: %+v", found, c)
	}
	sawCountSkip := false
	for _, warn := range resp.Warnings {
		if strings.Contains(warn, "条件 COUNT") {
			sawCountSkip = true
		}
	}
	if !sawCountSkip {
		t.Errorf("sql-out should warn about the skipped COUNT, warnings: %v", resp.Warnings)
	}
}

// 目标不可达 → "目标库" 失败项，detail 带驱动错误；源侧不受影响。
func TestMigratePreflightTargetUnreachable(t *testing.T) {
	srv, _, _ := preflightServer(t, func(cfg *config.Config) {
		cfg.Target = config.DBConfig{Type: "mysql", DSN: "root:pw@tcp(127.0.0.1:1)/none", Schema: "main"}
	})

	w := doJSON(t, srv, http.MethodPost, "/api/v1/migrate/preflight", "")
	resp := decodePreflight(t, w.Body.Bytes())
	if resp.Ok {
		t.Fatal("unreachable target must fail the preflight")
	}
	c, found := checkByName(resp, "目标库")
	if !found || c.Ok {
		t.Fatalf("expected failed 目标库 check, got %+v", resp.Checks)
	}
	if !strings.Contains(c.Detail, "ping") {
		t.Errorf("detail should carry the ping error, got: %s", c.Detail)
	}
}
