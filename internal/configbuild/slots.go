package configbuild

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dsnfields"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
)

// SlotRequest is the structured, language-neutral form of a migration intent —
// the contract between slot-filling (human interactive prompts, AI route+slots,
// scripts) and deterministic config assembly. It is JSON-native: AI tools emit
// exactly this shape, humans can hand-write it.
//
// Credentials live on EndpointSlots.Password and are assembled into the DSN by
// BuildFromSlots — LLM outputs in the plan pipeline never carry passwords (the
// serve layer injects them into the slots it builds from request credentials).
type SlotRequest struct {
	Scenario string         `json:"scenario"` // migrate|export|import|export-ddl|gen-select|export-insert|validate|full
	Metadata string         `json:"metadata,omitempty"`
	Source   EndpointSlots  `json:"source"`
	Target   *EndpointSlots `json:"target,omitempty"`
	Export   *ExportSlots   `json:"export,omitempty"`
	Import   *ImportSlots   `json:"import,omitempty"`
	DDL      *DDLSlots      `json:"ddl,omitempty"`
}

// FlexString accepts both JSON strings and numbers (LLMs routinely emit
// "port": 5432 as a number; coercing beats failing the whole slot request).
type FlexString string

func (s *FlexString) UnmarshalJSON(b []byte) error {
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) == 0 || string(b) == "null" {
		*s = ""
		return nil
	}
	if b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = FlexString(v)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*s = FlexString(n.String())
	return nil
}

// EndpointSlots is one database endpoint. Either DSN (verbatim) or the parts
// (host/port/user/password/database) — DSN wins when present.
type EndpointSlots struct {
	Type       string     `json:"type"`
	Host       string     `json:"host,omitempty"`
	Port       FlexString `json:"port,omitempty"`
	User       string `json:"user,omitempty"`
	Password   string `json:"password,omitempty"`
	Database   string `json:"database,omitempty"` // service/db 名；sqlite3/duckdb 为文件路径
	Schema     string `json:"schema,omitempty"`
	DSN        string `json:"dsn,omitempty"`
	Channel    string `json:"channel,omitempty"`     // native|agent|auto
	CompatMode string `json:"compat_mode,omitempty"` // OceanBase 租户模式: mysql|oracle
}

type ExportSlots struct {
	Format    string            `json:"format,omitempty"`
	OutputDir string            `json:"output_dir,omitempty"`
	Filters   map[string]string `json:"filters,omitempty"`
	Columns   *ColumnSlots      `json:"columns,omitempty"`
}

type ColumnSlots struct {
	Include map[string][]string          `json:"include,omitempty"`
	Rename  map[string]map[string]string `json:"rename,omitempty"`
}

type ImportSlots struct {
	SourceDir string `json:"source_dir,omitempty"`
}

type DDLSlots struct {
	TargetDialect string            `json:"target_dialect,omitempty"`
	SchemaMapping map[string]string `json:"schema_mapping,omitempty"`
	ColumnTypes   map[string]string `json:"column_types,omitempty"`
}

// ErrIncompleteSlots wraps validation failures that mean "the caller must
// supply more information" — the plan endpoint turns these into a clarify
// response instead of falling back to LLM invention.
var ErrIncompleteSlots = errors.New("incomplete slots")

// PasswordSentinelFor returns the credential placeholder a slot should carry
// for the given database type ("__PWD_mysql__" / "__PWD_pg__" /
// "__PWD_oracle__"), or "" for types without a family mapping. Plan flows use
// it so the LLM never handles real passwords: slots carry the sentinel, the
// caller substitutes the actual secret at confirm time.
func PasswordSentinelFor(dbType string) string {
	t := strings.ToLower(strings.TrimSpace(dbType))
	switch {
	case t == "mysql" || strings.HasSuffix(t, "-mysql") || t == "goldendb" || t == "oceanbase":
		return "__PWD_mysql__"
	case strings.Contains(t, "postgres") || strings.HasSuffix(t, "-pg") || t == "kingbase" || strings.HasPrefix(t, "opengauss") || strings.HasPrefix(t, "panwei"):
		return "__PWD_pg__"
	case t == "oracle" || strings.HasSuffix(t, "-oracle") || t == "dm" || t == "timesten":
		return "__PWD_oracle__"
	default:
		return ""
	}
}

// FillPasswordSentinels sets empty endpoint passwords to their family
// sentinel so confirm-time credential injection has a slot to land in.
func FillPasswordSentinels(req *SlotRequest) {
	fill := func(ep *EndpointSlots) {
		if ep.Password == "" {
			ep.Password = PasswordSentinelFor(ep.Type)
		}
	}
	fill(&req.Source)
	if req.Target != nil {
		fill(req.Target)
	}
}

// ValidScenarios lists the scenarios BuildFromSlots accepts.
func ValidScenarios() []string {
	return []string{"migrate", "export", "import", "export-ddl", "gen-select", "export-insert", "validate", "full"}
}

// BuildFromSlots assembles a validated config from structured slots. The
// returned config passes config.Load except for facts only the database can
// confirm (reachability, filter columns — the conditional-COUNT gate covers
// those at run time).
func BuildFromSlots(req SlotRequest) (*config.Config, error) {
	scenario := strings.ToLower(strings.TrimSpace(req.Scenario))
	if scenario == "" {
		scenario = "migrate"
	}
	if err := validateEndpoint("source", req.Source, needsTarget(scenario)); err != nil {
		return nil, err
	}
	if needsTarget(scenario) && req.Target == nil {
		return nil, fmt.Errorf("%w: target: scenario %q requires a target endpoint", ErrIncompleteSlots, scenario)
	}
	// 同实例迁移是头号场景：target 未给 host/port/user 时继承 source
	// （database/schema 仍各自独立——同实例不同库/用户正是用途所在）。
	// 必须在 target 校验之前执行，否则空 host 会先触发 ErrIncompleteSlots。
	if req.Target != nil && req.Target.DSN == "" {
		if req.Target.Host == "" {
			req.Target.Host = req.Source.Host
		}
		if req.Target.Port == "" {
			req.Target.Port = req.Source.Port
		}
		if req.Target.User == "" && !isEmbedded(req.Target.Type) {
			req.Target.User = req.Source.User
		}
	}
	if req.Target != nil {
		if err := validateEndpoint("target", *req.Target, true); err != nil {
			return nil, err
		}
	}

	defaultSchemaForFamily(&req.Source)
	srcDSN, err := endpointDSN(req.Source)
	if err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	tgtDSN := ""
	var tgtType, tgtSchema string
	if req.Target != nil {
		defaultSchemaForFamily(req.Target)
		tgtDSN, err = endpointDSN(*req.Target)
		if err != nil {
			return nil, fmt.Errorf("target: %w", err)
		}
		tgtType, tgtSchema = req.Target.Type, req.Target.Schema
	}
	metaType := req.Metadata
	if metaType == "" {
		metaType = "database"
	}

	var cfg *config.Config
	switch scenario {
	case "migrate":
		cfg = BuildMigrateConfig(req.Source.Type, srcDSN, req.Source.Schema, tgtType, tgtDSN, tgtSchema)
	case "export":
		cfg = BuildScenarioConfig("export", req.Source.Type, srcDSN, req.Source.Schema, tgtType, tgtDSN, tgtSchema, metaType)
	case "import":
		cfg = BuildScenarioConfig("import", req.Source.Type, srcDSN, req.Source.Schema, tgtType, tgtDSN, tgtSchema, metaType)
	case "export-ddl", "validate":
		cfg = BuildScenarioConfig("export-ddl", req.Source.Type, srcDSN, req.Source.Schema, tgtType, tgtDSN, tgtSchema, metaType)
	case "gen-select":
		cfg = BuildScenarioConfig("gen-select", req.Source.Type, srcDSN, req.Source.Schema, tgtType, tgtDSN, tgtSchema, metaType)
	case "export-insert":
		cfg = BuildScenarioConfig("export-insert", req.Source.Type, srcDSN, req.Source.Schema, tgtType, tgtDSN, tgtSchema, metaType)
	case "full":
		cfg = BuildFullConfig(metaType, req.Source.Type, srcDSN, req.Source.Schema, tgtType, tgtDSN, tgtSchema, "", "")
	default:
		return nil, fmt.Errorf("scenario %q 不支持；可用: %s", req.Scenario, strings.Join(ValidScenarios(), ", "))
	}
	if cfg == nil {
		return nil, fmt.Errorf("scenario %q 组装失败", scenario)
	}

	applyEndpoint(&cfg.Source, req.Source)
	if req.Target != nil {
		applyEndpoint(&cfg.Target, *req.Target)
	}
	// 无 target 的场景（export/validate/gen-select）Load 仍要求目标方言：
	// 缺省用源方言（同构导出是最常见语义；INSERT 语法方言可经 ddl 槽位覆盖）。
	if cfg.DDL.TargetDialect == "" && cfg.Target.Type == "" {
		cfg.DDL.TargetDialect = req.Source.Type
	}
	if err := applyExportSlots(cfg, req.Export); err != nil {
		return nil, err
	}
	if err := applyImportSlots(cfg, req.Import); err != nil {
		return nil, err
	}
	if err := applyDDLSlots(cfg, req.DDL); err != nil {
		return nil, err
	}
	return cfg, nil
}

// defaultSchemaForFamily fills the schema with the database name for the
// MySQL family, where "库" is both database and schema (matching the CLI
// guidance "MySQL: db 名").
func defaultSchemaForFamily(ep *EndpointSlots) {
	if ep.Schema != "" || ep.Database == "" {
		return
	}
	switch strings.ToLower(ep.Type) {
	case "mysql", "goldendb", "goldendb-mysql", "oceanbase", "oceanbase-mysql":
		ep.Schema = ep.Database
	}
}

func needsTarget(scenario string) bool {
	switch scenario {
	case "migrate", "import", "full":
		return true
	default:
		return false
	}
}

// validateEndpoint checks the parts needed to assemble a DSN for the type.
func validateEndpoint(side string, ep EndpointSlots, required bool) error {
	if ep.Type == "" {
		if !required && ep.DSN == "" && ep.Host == "" {
			return nil
		}
		return fmt.Errorf("%w: %s.type is required", ErrIncompleteSlots, side)
	}
	if ep.DSN != "" {
		return nil // 完整 DSN 直填，其余字段可不给
	}
	embedded := strings.EqualFold(ep.Type, "sqlite3") || strings.EqualFold(ep.Type, "duckdb")
	if embedded {
		if ep.Database == "" {
			return fmt.Errorf("%w: %s.database is required for %s (file path)", ErrIncompleteSlots, side, ep.Type)
		}
		return nil
	}
	if ep.Host == "" {
		return fmt.Errorf("%w: %s.host is required for %s (or provide %s.dsn verbatim)", ErrIncompleteSlots, side, ep.Type, side)
	}
	if ep.Database == "" {
		return fmt.Errorf("%w: %s.database is required for %s", ErrIncompleteSlots, side, ep.Type)
	}
	return nil
}

// endpointDSN assembles the DSN: verbatim slot DSN wins; otherwise the parts
// are composed by dsnfields.Build (same grammar the web form uses).
func endpointDSN(ep EndpointSlots) (string, error) {
	if dsn := strings.TrimSpace(ep.DSN); dsn != "" {
		return dsn, nil
	}
	f, err := dsnfields.Build(ep.Type, dsnfields.Fields{
		Username: ep.User,
		Password: ep.Password,
		Host:     ep.Host,
		Port:     string(ep.Port),
		Database: ep.Database,
	}, "")
	if err != nil {
		return "", err
	}
	return f, nil
}

// applyEndpoint copies per-endpoint slots that the scenario builders don't set.
func applyEndpoint(db *config.DBConfig, ep EndpointSlots) { //nolint:unused shape kept for pointer callers

	if ep.Channel != "" {
		db.Channel = ep.Channel
	}
	if ep.CompatMode != "" {
		db.CompatMode = ep.CompatMode
	}
}

func applyExportSlots(cfg *config.Config, s *ExportSlots) error {
	if s == nil {
		return nil
	}
	if s.Format != "" {
		cfg.Export.Format = s.Format
	}
	if s.OutputDir != "" {
		cfg.Export.OutputDir = s.OutputDir
	}
	if len(s.Filters) > 0 {
		cleaned := map[string]string{}
		for pattern, frag := range s.Filters {
			if strings.TrimSpace(frag) == "" {
				continue // 空片段 = 该表无条件，与未命中同义
			}
			if err := exporter.ValidateFilterFragment(frag); err != nil {
				return fmt.Errorf("filters[%q]: %w", pattern, err)
			}
			cleaned[pattern] = strings.TrimSpace(frag)
		}
		if len(cleaned) > 0 {
			cfg.Export.Filters = cleaned
		}
	}
	if s.Columns != nil {
		if len(s.Columns.Include) == 0 && len(s.Columns.Rename) == 0 {
			return fmt.Errorf("columns: include/rename 均为空——省略 columns 或至少给一项")
		}
		cfg.Export.Columns = config.ExportColumnsConfig{Include: s.Columns.Include, Rename: s.Columns.Rename}
	}
	return nil
}

func applyImportSlots(cfg *config.Config, s *ImportSlots) error {
	if s == nil {
		return nil
	}
	if s.SourceDir != "" {
		cfg.Import.SourceDir = s.SourceDir
	}
	return nil
}

func applyDDLSlots(cfg *config.Config, s *DDLSlots) error {
	if s == nil {
		return nil
	}
	if s.TargetDialect != "" {
		cfg.DDL.TargetDialect = s.TargetDialect
	}
	if len(s.SchemaMapping) > 0 {
		cfg.DDL.SchemaMapping = s.SchemaMapping
	}
	if len(s.ColumnTypes) > 0 {
		cfg.DDL.ColumnTypes = s.ColumnTypes
	}
	return nil
}
