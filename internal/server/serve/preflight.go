package serve

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/metadata"
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

	finish(true, warnings)
}
