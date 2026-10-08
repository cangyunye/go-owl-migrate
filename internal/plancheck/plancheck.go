// Package plancheck validates a migration plan against reality BEFORE any
// job starts: does the metadata describe tables the source account can
// actually read, does schema_mapping cover every metadata schema, can the
// target account create tables. These checks sit between structural config
// validation (config.Load) and runtime errors, closing the gap where a
// "connection test passes" plan still dies mid-run (e.g. csv metadata
// describing SCOTT.* while the source is MySQL, or an unmapped schema
// hitting ORA-01031 at create-table time).
//
// CLI (owl-migrate preflight / migrate Step 0) and the web preflight
// endpoint share the same checks so the two surfaces never disagree.
package plancheck

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// Status of a single check.
type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Check is one validation outcome.
type Check struct {
	Name   string
	Status Status
	Detail string
}

// OK reports whether the check is not a blocker (pass or warn).
func (c Check) OK() bool { return c.Status != StatusFail }

// Report aggregates checks; OK() is false when any check failed.
type Report struct {
	Checks []Check
	Warns  []string
}

// OK reports whether the plan may proceed (no failed check).
func (r *Report) OK() bool {
	for _, c := range r.Checks {
		if !c.OK() {
			return false
		}
	}
	return true
}

// Failures renders the failed checks as an actionable error body.
func (r *Report) Failures() string {
	var lines []string
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			lines = append(lines, "  ✗ "+c.Name+": "+c.Detail)
		}
	}
	return strings.Join(lines, "\n")
}

// probeAllTables beyond this count gets sampled instead of fully probed, to
// keep preflight latency bounded on 3000-table schemas.
const fullProbeLimit = 100

// distinctSchemas returns the sorted unique schemas of the matched tables.
func distinctSchemas(matched []*md.TableDef) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range matched {
		if !seen[t.TableSchema] {
			seen[t.TableSchema] = true
			out = append(out, t.TableSchema)
		}
	}
	sort.Strings(out)
	return out
}

// sortedCopy returns the tables sorted by schema.table for deterministic
// sampling.
func sortedCopy(matched []*md.TableDef) []*md.TableDef {
	out := append([]*md.TableDef(nil), matched...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].TableSchema != out[j].TableSchema {
			return out[i].TableSchema < out[j].TableSchema
		}
		return out[i].TableName < out[j].TableName
	})
	return out
}

// ── metadata ↔ source consistency ──────────────────────────────────────────

// CheckSourceTables probes that the tables described by offline metadata
// (csv/xlsx) are actually readable through the configured source connection.
// It is a no-op pass for metadata.type=database (the metadata came from the
// source itself, so it cannot drift).
func CheckSourceTables(ctx context.Context, cfg *config.Config, matched []*md.TableDef) Check {
	name := "元数据↔源库一致性"
	if strings.EqualFold(cfg.Metadata.Type, "database") {
		return Check{Name: name, Status: StatusPass, Detail: "元数据来自源库实时抽取，天然一致"}
	}
	if cfg.Source.Type == "" || cfg.Source.DSN == "" {
		return Check{Name: name, Status: StatusWarn, Detail: "未配置源库连接（离线模式），跳过表存在性校验"}
	}
	db, err := openDB(cfg.Source)
	if err != nil {
		return Check{Name: name, Status: StatusFail,
			Detail: "连接源库失败: " + err.Error()}
	}
	defer db.Close()

	probe := matched
	sampled := false
	if len(probe) > fullProbeLimit {
		probe = sortedCopy(matched)[:fullProbeLimit]
		sampled = true
	}
	var okN, badN int
	var bads []string
	for _, tbl := range probe {
		q := quoteQualified(cfg.Source.Type, tbl.TableSchema, tbl.TableName)
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var one int
		err := db.QueryRowContext(pctx, "SELECT 1 FROM "+q+" WHERE 1 = 0").Scan(&one)
		cancel()
		switch {
		case err == nil:
			okN++
		case err == sql.ErrNoRows:
			okN++ // WHERE 1=0 always returns no rows; driver may surface ErrNoRows — table exists
		default:
			badN++
			if len(bads) < 5 {
				bads = append(bads, fmt.Sprintf("%s.%s → %v", tbl.TableSchema, tbl.TableName, err))
			}
		}
	}
	switch {
	case badN == 0:
		detail := fmt.Sprintf("%d 张表在源库 %s 上均可读", okN, cfg.Source.Type)
		if sampled {
			detail += fmt.Sprintf("（共 %d 张，超过 %d 张仅抽查前 %d 张）", len(matched), fullProbeLimit, len(probe))
		}
		return Check{Name: name, Status: StatusPass, Detail: detail}
	default:
		detail := fmt.Sprintf("%d/%d 张表在源库上不可读——元数据与源库不一致（schema 名写错、表不存在或账号无权限）。例如: %s",
			badN, okN+badN, strings.Join(bads, "；"))
		if sampled {
			detail += fmt.Sprintf("（抽查前 %d 张）", len(probe))
		}
		return Check{Name: name, Status: StatusFail, Detail: detail}
	}
}

// ── schema_mapping coverage ────────────────────────────────────────────────

// CheckMappingCoverage warns when metadata schemas are not covered by
// ddl.schema_mapping and would land under their original name on the target —
// the silent trap that surfaces later as ORA-01031 / schema-not-found.
func CheckMappingCoverage(cfg *config.Config, matched []*md.TableDef) Check {
	name := "Schema 映射覆盖"
	if cfg.Target.Type == "" || cfg.Target.DSN == "" {
		return Check{Name: name, Status: StatusPass, Detail: "未配置目标库（导出/离线场景），不适用"}
	}
	schemas := distinctSchemas(matched)
	if len(schemas) == 0 {
		return Check{Name: name, Status: StatusPass, Detail: "元数据无表，不适用"}
	}
	var unmapped []string
	for _, s := range schemas {
		if _, ok := cfg.DDL.SchemaMapping[s]; !ok {
			unmapped = append(unmapped, s)
		}
	}
	sort.Strings(unmapped)
	if len(unmapped) == 0 {
		return Check{Name: name, Status: StatusPass,
			Detail: fmt.Sprintf("%d 个 schema 全部有映射: %s", len(schemas), strings.Join(schemas, ", "))}
	}
	landing := cfg.Target.Schema
	if landing == "" {
		landing = "目标原名"
	}
	return Check{Name: name, Status: StatusWarn,
		Detail: fmt.Sprintf("%s 未出现在 ddl.schema_mapping 中，将按原名落到目标（当前目标 schema=%s）；"+
			"若目标账号无权在该 schema 建表/写表，建表阶段会失败。建议补充映射，如 schema_mapping: {%s: %s}",
			strings.Join(unmapped, ", "), landing, strings.Join(unmapped, ", "), landing)}
}

// ── target DDL permission probe ────────────────────────────────────────────

// CheckTargetDDL probes that the target account can actually CREATE + DROP a
// table in the target schema. Warn-level: sql-out / pre-created-table flows
// legitimately skip target DDL, so a failure here is advice, not a blocker —
// but it quotes the exact driver error so the Step-4 failure is never a
// surprise.
func CheckTargetDDL(ctx context.Context, cfg *config.Config, db *sql.DB) Check {
	name := "目标建表权限"
	if cfg.Target.Type == "" || cfg.Target.DSN == "" {
		return Check{Name: name, Status: StatusPass, Detail: "未配置目标库，跳过"}
	}
	closeDB := false
	if db == nil {
		var err error
		db, err = openDB(cfg.Target)
		if err != nil {
			return Check{Name: name, Status: StatusWarn, Detail: "连接目标库失败（建表阶段会重试并报错）: " + err.Error()}
		}
		closeDB = true
		defer func() {
			if closeDB {
				db.Close()
			}
		}()
	}

	probe := fmt.Sprintf("OWL_PLANCHK_%04X", time.Now().UnixNano()%0xFFFF)
	qualified := quoteQualified(cfg.Target.Type, cfg.Target.Schema, probe)
	if cfg.Target.Schema == "" {
		// No target schema: use the connection default; quoting just the table.
		qualified = quoteIdent(cfg.Target.Type, probe)
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(tctx, "CREATE TABLE "+qualified+" (id int)"); err != nil {
		return Check{Name: name, Status: StatusWarn,
			Detail: fmt.Sprintf("在 %s 上试建探针表失败: %v —— 目标账号可能无 CREATE TABLE 权限，建表阶段会失败（--skip-ddl / 预建表流程可忽略此项）", qualified, err)}
	}
	if _, err := db.ExecContext(tctx, "DROP TABLE "+qualified); err != nil {
		return Check{Name: name, Status: StatusWarn,
			Detail: fmt.Sprintf("探针表 %s 建表成功但清理失败（%v），请手动删除", qualified, err)}
	}
	return Check{Name: name, Status: StatusPass,
		Detail: fmt.Sprintf("目标账号可在 %s 上建表并删除（探针表已清理）", cfg.Target.Schema)}
}

// ── composition ────────────────────────────────────────────────────────────

// Options tunes Run for the calling surface.
type Options struct {
	// SkipTarget omits the target-side checks (migrate --sql-out, export).
	SkipTarget bool
	// TRUNCATE warning appended verbatim when set (config import.truncate_before).
	TruncateWarn string
}

// Run executes every plan-level check. matched comes from filtering the
// loaded metadata by export.tables.include (the tables the plan will touch).
func Run(ctx context.Context, cfg *config.Config, matched []*md.TableDef, opts Options) *Report {
	r := &Report{}
	r.Checks = append(r.Checks, CheckSourceTables(ctx, cfg, matched))
	r.Checks = append(r.Checks, CheckMappingCoverage(cfg, matched))
	if !opts.SkipTarget {
		r.Checks = append(r.Checks, CheckTargetDDL(ctx, cfg, nil))
	}
	if opts.TruncateWarn != "" {
		r.Warns = append(r.Warns, opts.TruncateWarn)
	}
	return r
}
