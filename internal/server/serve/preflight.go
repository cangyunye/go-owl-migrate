package serve

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/plancheck"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// handleMigratePreflight runs the cheap, read-only checks a user needs before
// committing to a migration: config present, metadata extractable, table
// selection matching something, and (outside sql-out mode) the target
// reachable. It never writes and never spawns the worker.
func (s *Server) handleMigratePreflight(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	checks := make([]map[string]any, 0, 4)
	add := func(name string, ok bool, detail string) {
		checks = append(checks, map[string]any{"name": name, "ok": ok, "detail": detail})
	}
	finish := func(ok bool, warnings []string) {
		if warnings == nil {
			warnings = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "checks": checks, "warnings": warnings})
	}

	var warnings []string
	if cfg == nil || cfg.Metadata.Type == "" {
		add("配置", false, "尚未加载任何配置，请先在「配置」页保存")
		finish(false, warnings)
		return
	}
	add("配置", true, "场景 "+service.DetectScenario(cfg)+"，元数据来源 "+cfg.Metadata.Type)
	if cfg.Import.Target.TruncateBefore {
		warnings = append(warnings, "目标表将在导入前执行 TRUNCATE（import.target.truncate_before）")
	}

	// Metadata extractable — for a database source this also proves the source
	// is reachable; for csv/xlsx it proves the files are readable.
	sm, err := service.LoadMetadata(cfg)
	if err != nil {
		add("源库/元数据", false, err.Error())
		finish(false, warnings)
		return
	}
	allTables := sm.GetTables()
	matched := metadata.FilterTablesByInclude(allTables, cfg.Export.Tables.Include)
	if len(matched) == 0 {
		add("表清单", false, fmt.Sprintf("表清单 %v 没有匹配到任何表（源里共 %d 张）", cfg.Export.Tables.Include, len(allTables)))
		finish(false, warnings)
		return
	}
	add("源库/元数据", true, fmt.Sprintf("可读取，表清单命中 %d 张（源里共 %d 张）", len(matched), len(allTables)))

	// 计划级共享检查（与 `owl-migrate preflight` / migrate Step 2 同一套实现）：
	// 离线元数据的表在源库上是否可读、schema_mapping 覆盖率。
	for _, c := range []plancheck.Check{
		plancheck.CheckSourceTables(r.Context(), cfg, matched),
		plancheck.CheckMappingCoverage(cfg, matched),
	} {
		add(c.Name, c.OK(), c.Detail)
		if c.Status == plancheck.StatusWarn {
			warnings = append(warnings, c.Name+"："+c.Detail)
		}
		if !c.OK() {
			finish(false, warnings)
			return
		}
	}

	// 条件导出预检：filters 非空时对每张命中表跑条件 COUNT——把语法/列名/
	// 权限错误在预检阶段暴露（与 worker 侧门禁同一判定），并给出源侧预期行数。
	if len(cfg.Export.Filters) > 0 {
		if r.URL.Query().Get("mode") != "sql-out" {
			srcDB, err := s.openSourceDB(cfg.Source)
			if err != nil {
				add("条件导出预检", false, "connect source: "+err.Error())
				finish(false, warnings)
				return
			}
			defer srcDB.Close()
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
			defer cancel()
			okAll := true
			var details []string
			for _, tbl := range matched {
				frag, rerr := resolveFilterForTable(cfg.Export.Filters, tbl.TableSchema, tbl.TableName)
				if rerr != nil {
					details = append(details, rerr.Error())
					okAll = false
					continue
				}
				if frag == "" {
					continue
				}
				qualified := quoteSourceIdent(cfg.Source.Type, tbl.TableSchema) + "." + quoteSourceIdent(cfg.Source.Type, tbl.TableName)
				n, cerr := countTableRowsWithFilter(ctx, srcDB, qualified, frag, 2*time.Minute)
				if cerr != nil {
					details = append(details, fmt.Sprintf("%s.%s filter %q: %v", tbl.TableSchema, tbl.TableName, frag, cerr))
					okAll = false
					continue
				}
				details = append(details, fmt.Sprintf("%s.%s: %d 行（filter %q）", tbl.TableSchema, tbl.TableName, n, frag))
			}
			add("条件导出预检", okAll, strings.Join(details, "；"))
			if !okAll {
				finish(false, warnings)
				return
			}
		} else {
			warnings = append(warnings, "SQL 输出模式跳过条件 COUNT 预检")
		}
	}

	// Target reachable — sql-out mode never connects to a target, so skip it.
	if r.URL.Query().Get("mode") == "sql-out" {
		add("目标库", true, "SQL 输出模式不连接目标库，已跳过")
		finish(true, warnings)
		return
	}
	if cfg.Target.Type == "" || cfg.Target.DSN == "" {
		add("目标库", false, "未配置目标数据库")
		finish(false, warnings)
		return
	}
	timeout := 15 * time.Second
	if d, perr := time.ParseDuration(cfg.Target.ConnectTimeout); perr == nil && d > 0 {
		timeout = d
	}
	db, err := service.OpenDB(cfg.Target)
	if err != nil {
		add("目标库", false, "connect: "+err.Error())
		finish(false, warnings)
		return
	}
	defer db.Close()
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		add("目标库", false, "ping: "+err.Error())
		finish(false, warnings)
		return
	}
	add("目标库", true, fmt.Sprintf("%s 连接正常（%d ms）", cfg.Target.Type, time.Since(start).Milliseconds()))

	// 目标建表权限探针（与 CLI 同一实现；失败是警告级，不阻塞预检但会点名）。
	ddl := plancheck.CheckTargetDDL(r.Context(), cfg, db)
	add(ddl.Name, ddl.OK(), ddl.Detail)
	if ddl.Status == plancheck.StatusWarn {
		warnings = append(warnings, ddl.Name+"："+ddl.Detail)
	}

	finish(true, warnings)
}

// resolveFilterForTable picks the WHERE fragment for one table using the same
// semantics as the worker-side exporter gate (exact key wins over glob; two
// globs hitting one table is an error). Exported rules live in the exporter;
// this copy keeps serve preflight dependency-light.
func resolveFilterForTable(filters map[string]string, schema, table string) (string, error) {
	if len(filters) == 0 {
		return "", nil
	}
	for k, f := range filters {
		if strings.EqualFold(strings.TrimSpace(k), schema+"."+table) {
			return f, nil
		}
	}
	var matched []string
	var frag string
	for k, f := range filters {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		if strings.Contains(k, ".") {
			if ok, _ := filepath.Match(k, strings.ToLower(schema+"."+table)); ok {
				matched = append(matched, k)
				frag = f
			}
			continue
		}
		if ok, _ := filepath.Match(k, strings.ToLower(table)); ok {
			matched = append(matched, k)
			frag = f
		}
	}
	if len(matched) == 0 {
		return "", nil
	}
	if len(matched) > 1 {
		return "", fmt.Errorf("%s.%s 命中多个 export.filters 键（%s），请让模式互斥", schema, table, strings.Join(matched, ", "))
	}
	return frag, nil
}

// countTableRowsWithFilter runs SELECT COUNT(*) … WHERE <filter> with a
// per-table timeout, mirroring the worker-side filter gate.
func countTableRowsWithFilter(ctx context.Context, db *sql.DB, qualified, filter string, timeout time.Duration) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var n int64
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+qualified+" WHERE "+filter).Scan(&n)
	return n, err
}
