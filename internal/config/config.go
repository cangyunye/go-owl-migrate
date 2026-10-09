package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cangyunye/owljdbc"
	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/dsnfields"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// MarshalYAML implements yaml.Marshaler to omit empty config sections.
func (c *Config) MarshalYAML() (interface{}, error) {
	type metaAlias struct {
		Type  string      `yaml:"type"`
		CSV   *CSVConfig  `yaml:"csv,omitempty"`
		XLSX  *XLSXConfig `yaml:"xlsx,omitempty"`
		Files []string    `yaml:"files,omitempty"`
	}
	m := struct {
		General    GeneralConfig    `yaml:"general"`
		Metadata   metaAlias        `yaml:"metadata"`
		Source     *DBConfig        `yaml:"source,omitempty"`
		Target     *DBConfig        `yaml:"target,omitempty"`
		DDL        *DDLConfig       `yaml:"ddl,omitempty"`
		SelectGen  *SelectGenConfig `yaml:"select_gen,omitempty"`
		Export     *ExportConfig    `yaml:"export,omitempty"`
		Import     *ImportConfig    `yaml:"import,omitempty"`
		Online     *OnlineConfig    `yaml:"online,omitempty"`
		Extensions map[string]any   `yaml:"extensions,omitempty"`
		Agent      *AgentConfig     `yaml:"agent,omitempty"`
		OwlJDBC    *OwlJDBCConfig   `yaml:"owljdbc,omitempty"`
		AI         *AIConfig        `yaml:"ai,omitempty"`
	}{
		General: c.General,
		Metadata: metaAlias{
			Type: c.Metadata.Type,
		},
	}
	if c.Metadata.CSV.Path != "" || c.Metadata.CSV.HasHeader {
		v := c.Metadata.CSV
		m.Metadata.CSV = &v
	}
	if c.Metadata.XLSX.Path != "" {
		v := c.Metadata.XLSX
		m.Metadata.XLSX = &v
	}
	if len(c.Metadata.Files) > 0 {
		m.Metadata.Files = c.Metadata.Files
	}
	emit := func(ok bool) bool { return ok || c.ForceAllSections }

	if emit(!c.Source.isZero()) {
		v := c.Source
		m.Source = &v
	}
	if emit(!c.Target.isZero()) {
		v := c.Target
		m.Target = &v
	}
	if emit(!c.DDL.isZero()) {
		v := c.DDL
		m.DDL = &v
	}
	if emit(!c.SelectGen.isZero()) {
		v := c.SelectGen
		m.SelectGen = &v
	}
	if emit(!c.Export.isZero()) {
		v := c.Export
		m.Export = &v
	}
	if emit(!c.Import.isZero()) {
		v := c.Import
		m.Import = &v
	}
	if emit(!c.Online.isZero()) {
		v := c.Online
		m.Online = &v
	}
	if len(c.Extensions) > 0 {
		m.Extensions = c.Extensions
	}
	if len(c.OwlJDBC.Profiles) > 0 {
		v := c.OwlJDBC
		m.OwlJDBC = &v
	}
	if c.Agent.JarsDir != "" || c.Agent.AgentJar != "" || c.Agent.JavaHome != "" {
		v := c.Agent
		m.Agent = &v
	}
	if c.AI != (AIConfig{}) {
		v := c.AI
		m.AI = &v
	}
	return m, nil
}

// IsZero returns true if the DBConfig has no meaningful values set.
func (d DBConfig) isZero() bool {
	return d.Type == "" && d.DSN == "" && d.Schema == "" && d.ConnectTimeout == "" && d.QueryTimeout == "" && d.CompatMode == "" && d.Adapter == "" && d.Pool.isZero()
}

// AdapterIsZero reports whether the adapter plugin reference is unset.
func (d DBConfig) AdapterIsZero() bool { return d.Adapter == "" }

// IsZero returns true if the PoolConfig has no meaningful values set.
func (p PoolConfig) isZero() bool {
	return p.MaxOpenConns == 0 && p.MaxIdleConns == 0 && p.ConnMaxLifetime == "" && p.ConnMaxIdleTime == ""
}

// IsZero returns true if the DDLConfig has no meaningful values set.
func (d DDLConfig) isZero() bool {
	return d.TargetDialect == "" && d.SourceDialect == "" && !d.IncludeComments && !d.IncludeIfNotExists && !d.NoQuoteIdentifiers && len(d.SchemaMapping) == 0 && len(d.ColumnTypes) == 0
}

// IsZero returns true if the SelectGenConfig has no meaningful values set.
func (s SelectGenConfig) isZero() bool {
	return s.OutputDir == "" && s.Batch.isZero() && !s.IncludeRowNumber && !s.AddExportColumns
}

// IsZero returns true if the ExportConfig has no meaningful values set.
func (e ExportConfig) isZero() bool {
	return e.OutputDir == "" && e.Format == "" && e.CSV.isZero() && e.Batch.isZero() && e.Parallel.isZero() && e.Tables.isZero() &&
		len(e.Filters) == 0 && e.FiltersCheck == "" && e.Columns.isZero()
}

// IsZero returns true if the ImportConfig has no meaningful values set.
func (i ImportConfig) isZero() bool {
	return i.SourceDir == "" && i.Format == "" && i.CSV.isZero() && i.Target.isZero() && i.Batch.isZero() && i.Parallel.isZero() && i.DataTransforms.isZero()
}

func (c ExportColumnsConfig) isZero() bool {
	return len(c.Include) == 0 && len(c.Rename) == 0
}

// IsZero helpers for nested config structs.
func (b BatchConfig) isZero() bool    { return b.Method == "" && b.PageSize == 0 }
func (p ParallelConfig) isZero() bool { return !p.Enabled && p.MaxWorkers == 0 }
func (e ExportCSVConfig) isZero() bool {
	return e.Delimiter == "" && !e.Header && e.NullRepresentation == "" && e.QuoteChar == ""
}
func (i ImportCSVConfig) isZero() bool {
	return i.Delimiter == "" && i.NullMarker == "" && !i.HasHeader
}
func (t ImportTargetConfig) isZero() bool {
	return !t.TruncateBefore && !t.DisableConstraints && !t.DisableTriggers && !t.DropIndexes
}
func (b ImportBatchConfig) isZero() bool {
	return b.CommitInterval == 0 && b.ErrorPolicy == "" && !b.UseCopy
}
func (d DataTransforms) isZero() bool {
	return d.DatetimeFormat == "" && !d.TrimStrings && len(d.NullIf) == 0 && len(d.ColumnDatetimeFormats) == 0
}
func (t TableListConfig) isZero() bool { return len(t.Include) == 0 }

// ValidDialects lists supported target dialects.
var ValidDialects = map[string]bool{
	"oracle":             true,
	"postgres":           true,
	"mysql":              true,
	"sqlite3":            true,
	"duckdb":             true,
	"goldendb":           true,
	"goldendb-mysql":     true,
	"goldendb-oracle":    true,
	"oceanbase":          true,
	"oceanbase-mysql":    true,
	"oceanbase-oracle":   true,
	"panweidb":           true,
	"panweidb-mysql":     true,
	"panweidb-oracle":    true,
	"opengaussdb":        true,
	"opengaussdb-oracle": true,
	"opengaussdb-mysql":  true,
}

// dialectAliases maps target.type spellings accepted by the connection layer
// to canonical dialect names, used when ddl.target_dialect inherits target.type.
var dialectAliases = map[string]string{
	"postgresql": "postgres",
	"mariadb":    "mysql",
}

// ValidMetadataTypes lists supported metadata source types.
var ValidMetadataTypes = map[string]bool{
	"csv":      true,
	"xlsx":     true,
	"database": true,
}

// ValidErrorPolicies lists supported error handling strategies.
var ValidErrorPolicies = map[string]bool{
	"skip_row": true,
	"stop":     true,
	"log_only": true,
}

// Config is the root configuration structure.
type Config struct {
	General    GeneralConfig   `yaml:"general"`
	Metadata   MetadataConfig  `yaml:"metadata"`
	Source     DBConfig        `yaml:"source"`
	Target     DBConfig        `yaml:"target"`
	DDL        DDLConfig       `yaml:"ddl"`
	SelectGen  SelectGenConfig `yaml:"select_gen"`
	Export     ExportConfig    `yaml:"export"`
	Import     ImportConfig    `yaml:"import"`
	Online     OnlineConfig    `yaml:"online"`
	Extensions map[string]any  `yaml:"extensions"`

	// Agent holds global owljdbc channel defaults (jars discovery, JVM) that
	// apply to source/target connections unless overridden per connection.
	Agent AgentConfig `yaml:"agent,omitempty"`

	// OwlJDBC holds externally registered agent-channel database profiles
	// (new database types without code changes; oracle/mysql/postgres-family
	// compatible drivers). See OwlJDBCConfig.
	OwlJDBC OwlJDBCConfig `yaml:"owljdbc,omitempty"`

	// AI holds the optional vendor-API settings for serve's natural-language
	// routing endpoints (/api/v1/ai/*). The API key itself is never stored in
	// config files — only the environment variable name to read it from. See
	// AIConfig.
	AI AIConfig `yaml:"ai,omitempty"`

	// ForceAllSections when true causes MarshalYAML to emit ALL sections
	// even if they are zero-valued. Used by the "full" init scenario.
	ForceAllSections bool `yaml:"-"`
}

// GeneralConfig holds top-level settings.
type GeneralConfig struct {
	LogLevel  string `yaml:"log_level"`
	LogFile   string `yaml:"log_file,omitempty"`
	LogFormat string `yaml:"log_format,omitempty"`
}

// MetadataConfig holds metadata source configuration.
type MetadataConfig struct {
	Type  string     `yaml:"type"` // csv/xlsx/database
	CSV   CSVConfig  `yaml:"csv"`
	XLSX  XLSXConfig `yaml:"xlsx"`
	Files []string   `yaml:"files,omitempty"`
}

// XLSXConfig holds xlsx loading settings.
type XLSXConfig struct {
	Path          string `yaml:"path"`                      // path to .xlsx file
	DataOutputDir string `yaml:"data_output_dir,omitempty"` // output directory for @sheet CSV data
}

// CSVConfig holds CSV parsing settings.
type CSVConfig struct {
	Path               string `yaml:"path"`
	Delimiter          string `yaml:"delimiter,omitempty"`
	Encoding           string `yaml:"encoding,omitempty"`
	HasHeader          bool   `yaml:"has_header,omitempty"`
	NullMarker         string `yaml:"null_marker,omitempty"`
	ColumnNameMatching string `yaml:"column_name_matching,omitempty"`
}

// DBConfig holds database connection settings.
type DBConfig struct {
	Type           string     `yaml:"type"`
	DSN            string     `yaml:"dsn"`
	Schema         string     `yaml:"schema"`
	ConnectTimeout string     `yaml:"connect_timeout,omitempty"`
	QueryTimeout   string     `yaml:"query_timeout,omitempty"`
	Pool           PoolConfig `yaml:"pool,omitempty"`

	// Channel selects the database/sql channel used by dbconn.Open.
	// "" / "native" (default) keeps the native driver path and never starts
	// the agent JVM; "agent" forces the owljdbc channel; "auto" uses native
	// when available and falls back to owljdbc only when the native driver is
	// absent or not compiled into this binary.
	Channel string `yaml:"channel,omitempty"`

	// Agent holds owljdbc channel settings; only read when the agent channel
	// is selected. Empty paths resolve against the working directory.
	Agent AgentConfig `yaml:"agent,omitempty"`

	// Adapter references an external target adapter plugin YAML (mode
	// native/client/file-batch) used by online incremental migration when the
	// target has no built-in Go driver.
	Adapter string `yaml:"adapter,omitempty"`

	// CompatMode applies to OceanBase: declares the tenant compatibility mode
	// ("mysql" or "oracle"). When empty it is auto-detected from the live
	// connection and a mismatch raises an error.
	CompatMode string `yaml:"compat_mode,omitempty"`
}

// AgentConfig holds owljdbc agent-channel connection settings.
type AgentConfig struct {
	// JarsDir is the directory searched for the owl-agent jar and per-type
	// JDBC driver jars. When empty the working directory is searched.
	JarsDir string `yaml:"jars_dir,omitempty"`
	// AgentJar is the path to owl-agent.jar. When empty, "owl-agent.jar" is
	// looked up in JarsDir then the working directory.
	AgentJar string `yaml:"agent_jar,omitempty"`
	// JavaHome selects the java executable directory; empty uses java from PATH.
	JavaHome string `yaml:"java_home,omitempty"`
}

// AIConfig holds vendor-API settings for the natural-language routing layer
// (serve /api/v1/ai/*). First-round vendor is DeepSeek; custom vendors are
// any OpenAI-compatible endpoint via base_url. The API key itself must live
// in an environment variable — APIKeyEnv names it and is read per request,
// so keys never land in config files, logs, or session objects.
type AIConfig struct {
	// Provider selects the vendor preset. Empty = deepseek.
	Provider string `yaml:"provider,omitempty"`
	// BaseURL is the OpenAI-compatible API root (no /chat/completions suffix).
	// Empty = the provider preset's default.
	BaseURL string `yaml:"base_url,omitempty"`
	// APIKeyEnv names the environment variable holding the API key.
	// Empty = OWL_AI_API_KEY (DEEPSEEK_API_KEY is also accepted as fallback).
	APIKeyEnv string `yaml:"api_key_env,omitempty"`
	// Model is the vendor model id, e.g. deepseek-flash.
	Model string `yaml:"model,omitempty"`
	// ContextWindow is the model context budget in tokens. Only used locally
	// for pre-truncation and session budgeting; never sent to the vendor.
	ContextWindow int `yaml:"context_window,omitempty"`
	// Effort is the thinking intensity for routing: low | high | max. Only
	// meaningful for reasoning models (deepseek-flash default level is high;
	// routing uses low for speed/cost).
	Effort string `yaml:"effort,omitempty"`
	// PlanEffortStr is the thinking intensity for config generation, which
	// benefits from deeper reasoning than routing. YAML key: plan_effort.
	PlanEffortStr string `yaml:"plan_effort,omitempty"`
	// MaxTokens caps completion tokens per request. Reasoning models spend
	// thinking tokens from this budget, so keep it generous (default 32768).
	MaxTokens int `yaml:"max_tokens,omitempty"`
	// TimeoutStr is the per-attempt HTTP timeout, e.g. "2m". YAML key stays
	// timeout; the Go field is TimeoutStr so Timeout() can be the parsed form.
	TimeoutStr string `yaml:"timeout,omitempty"`
	// MaxRepairRounds bounds the config repair loop (validate error fed back
	// to the model; only slot values may change, never the intent).
	MaxRepairRounds int `yaml:"max_repair_rounds,omitempty"`
}

// DefaultAIBaseURL maps a provider preset to its OpenAI-compatible API root.
// "custom" 档位不在表内——base_url 必须显式提供。
var DefaultAIBaseURL = map[string]string{
	"deepseek": "https://api.deepseek.com",
	"openai":   "https://api.openai.com/v1",
	"moonshot": "https://api.moonshot.cn/v1",
	"qwen":     "https://dashscope.aliyuncs.com/compatible-mode/v1",
	"glm":      "https://open.bigmodel.cn/api/paas/v4",
	"ollama":   "http://127.0.0.1:11434/v1",
}

// AIProviderPresets lists the selectable provider slots in UI order; custom
// means a hand-filled base_url (不在预设表内).
var AIProviderPresets = []string{"deepseek", "openai", "moonshot", "qwen", "glm", "ollama", "custom"}

// ValidAIProvider reports whether name is a preset or the custom slot.
func ValidAIProvider(name string) bool {
	for _, p := range AIProviderPresets {
		if p == name {
			return true
		}
	}
	return false
}

// ValidAIEfforts lists supported thinking-intensity levels.
var ValidAIEfforts = map[string]bool{"low": true, "high": true, "max": true}

func (a *AIConfig) ApplyDefaults() {
	if a.Provider == "" {
		a.Provider = "deepseek"
	}
	if a.BaseURL == "" {
		a.BaseURL = DefaultAIBaseURL[a.Provider]
	}
	if a.APIKeyEnv == "" {
		a.APIKeyEnv = "OWL_AI_API_KEY"
	}
	// deepseek 兜底默认模型；custom 端点的模型必须由用户探测或手填——
	// 拿 deepseek 的模型名打别的端点只会得到一个必然失败的请求。
	if a.Model == "" && (a.Provider == "" || a.Provider == "deepseek") {
		a.Model = "deepseek-flash"
	}
	if a.ContextWindow == 0 {
		a.ContextWindow = 1048576
	}
	// effort/plan_effort 是 DeepSeek 系的思考强度字段，严格 OpenAI 端点会
	// 拒收未知参数：仅 deepseek 预设默认注入；custom/其他端点留空 =
	// 不随请求发送（用户仍可在高级参数里显式设置）。
	if a.Effort == "" && (a.Provider == "" || a.Provider == "deepseek") {
		a.Effort = "low"
	}
	if a.PlanEffortStr == "" && (a.Provider == "" || a.Provider == "deepseek") {
		a.PlanEffortStr = "high"
	}
	if a.MaxTokens == 0 {
		a.MaxTokens = 32768
	}
	if a.TimeoutStr == "" {
		a.TimeoutStr = "2m"
	}
	if a.MaxRepairRounds == 0 {
		a.MaxRepairRounds = 3
	}
}

// APIKey resolves the key: the configured env var first, then the well-known
// DEEPSEEK_API_KEY fallback. Empty means the AI layer is not provisioned.
func (a *AIConfig) APIKey() string {
	if v := os.Getenv(a.APIKeyEnv); v != "" {
		return v
	}
	if a.APIKeyEnv != "DEEPSEEK_API_KEY" {
		return os.Getenv("DEEPSEEK_API_KEY")
	}
	return ""
}

// PlanEffort returns the thinking intensity for config generation; empty
// means "not set" (custom 端点默认不发送该字段), defaults are applied by
// ApplyDefaults for the deepseek preset.
func (a *AIConfig) PlanEffort() string {
	return a.PlanEffortStr
}

func (a *AIConfig) Timeout() time.Duration {
	d, err := time.ParseDuration(a.TimeoutStr)
	if err != nil || d <= 0 {
		return 2 * time.Minute
	}
	return d
}

// OwlJDBCConfig registers external agent-channel database profiles: new
// database types (oracle/mysql/postgres-family compatible) that connect
// through the owljdbc sidecar without code changes. Built-in catalog entries
// remain the baseline; a profile registered here under the same type name
// overrides it.
type OwlJDBCConfig struct {
	Profiles map[string]OwlJDBCProfileSpec `yaml:"profiles,omitempty"`
}

// OwlJDBCProfileSpec declares one externally registered database type. The
// fields map 1:1 onto owljdbc.ProfileSpec; DSNSyntax additionally tells the
// host-side DSN parser which grammar the source/target dsn uses.
type OwlJDBCProfileSpec struct {
	// DriverClass is the JDBC driver class name loaded by the sidecar.
	DriverClass string `yaml:"driver_class"`
	// JarGlobs are candidate file names of the driver jar inside jars_dir
	// (any match wins).
	JarGlobs []string `yaml:"jar_globs"`
	// Family is the connection semantics family: oracle | mysql | postgres.
	// It drives metadata-querier selection, placeholder family, and the
	// TRUNCATE/identifier behaviors of the matched family.
	Family string `yaml:"family"`
	// URLTemplate builds the JDBC URL from the decomposed DSN; supports
	// {host} {port} {database}. Credentials never go through the template.
	URLTemplate string `yaml:"url_template,omitempty"`
	// DSNRaw means the dsn itself is a complete JDBC URL and is passed
	// through verbatim (URLTemplate is ignored).
	DSNRaw bool `yaml:"dsn_raw,omitempty"`
	// DSNSyntax declares how source/target dsn strings parse: url (scheme://
	// user:pass@host:port/db), pg-kv (host=... port=...), mysql-tcp
	// (user:pass@tcp(host:port)/db), or kv (semicolon-separated KEY=VALUE).
	DSNSyntax string `yaml:"dsn_syntax,omitempty"`
}

// RegisterOwlJDBCProfiles publishes cfg.OwlJDBC.Profiles to the owljdbc
// catalog and the dsnfields syntax registry. It must run wherever a Config
// enters the system (config.Load and every serve-side yaml activation);
// registration is idempotent per type name. Worker subprocesses re-run this
// through Load, so registered types work across the serve→worker boundary.
func (c *Config) RegisterOwlJDBCProfiles() error {
	if len(c.OwlJDBC.Profiles) == 0 {
		return nil
	}
	specs := make(map[string]owljdbc.ProfileSpec, len(c.OwlJDBC.Profiles))
	syntaxes := make(map[string]string, len(c.OwlJDBC.Profiles))
	for name, p := range c.OwlJDBC.Profiles {
		specs[strings.ToLower(strings.TrimSpace(name))] = owljdbc.ProfileSpec{
			DriverClass: p.DriverClass,
			JarGlobs:    p.JarGlobs,
			Family:      p.Family,
			URLTemplate: p.URLTemplate,
			DSNRaw:      p.DSNRaw,
			DSNSyntax:   p.DSNSyntax,
		}
		if p.DSNSyntax != "" {
			syntaxes[strings.ToLower(strings.TrimSpace(name))] = p.DSNSyntax
		}
	}
	if err := owljdbc.RegisterSpecs(specs); err != nil {
		return err
	}
	dsnfields.RegisterSyntaxes(syntaxes)
	return nil
}

// PoolConfig holds connection pool tuning parameters.
type PoolConfig struct {
	MaxOpenConns    int    `yaml:"max_open_conns,omitempty"`
	MaxIdleConns    int    `yaml:"max_idle_conns,omitempty"`
	ConnMaxLifetime string `yaml:"conn_max_lifetime,omitempty"`
	ConnMaxIdleTime string `yaml:"conn_max_idle_time,omitempty"`
}

// DDLConfig holds DDL generation settings.
type DDLConfig struct {
	OutputDir          string            `yaml:"output_dir,omitempty"`
	TargetDialect      string            `yaml:"target_dialect"`
	SourceDialect      string            `yaml:"source_dialect,omitempty"`
	IncludeComments    bool              `yaml:"include_comments,omitempty"`
	IncludeIfNotExists bool              `yaml:"include_if_not_exists,omitempty"`
	IncludeDrop        bool              `yaml:"include_drop,omitempty"`
	SplitByObject      bool              `yaml:"split_by_object,omitempty"`
	SchemaMapping      map[string]string `yaml:"schema_mapping,omitempty"`
	TableFilter        TableFilterConfig `yaml:"table_filter,omitempty"`
	TypeOverrides      map[string]string `yaml:"type_overrides,omitempty"`
	// ColumnTypes overrides a specific column's target type, keyed
	// "SCHEMA.TABLE.COLUMN" (case-insensitive on lookup). Wins over
	// TypeOverrides; value is a target-dialect type template supporting
	// the same %l/%p/%s placeholders.
	ColumnTypes        map[string]string `yaml:"column_types,omitempty"`
	IdentityToSerial   bool              `yaml:"identity_to_serial,omitempty"`
	AddRowIDColumn     bool              `yaml:"add_rowid_column,omitempty"`
	EmptyStringToNull  bool              `yaml:"empty_string_to_null,omitempty"`
	BooleanMapping     map[string]bool   `yaml:"boolean_mapping,omitempty"`
	Partition          PartitionConfig   `yaml:"partition,omitempty"`
	NoQuoteIdentifiers bool              `yaml:"no_quote_identifiers,omitempty"`
}

// TableFilterConfig holds table include/exclude rules.
type TableFilterConfig struct {
	Include []string           `yaml:"include,omitempty"`
	Exclude TableExcludeConfig `yaml:"exclude,omitempty"`
}

// TableExcludeConfig holds table exclusion rules.
type TableExcludeConfig struct {
	Glob    []string `yaml:"glob,omitempty"`
	Regex   []string `yaml:"regex,omitempty"`
	Schemas []string `yaml:"schemas,omitempty"`
	Tables  []string `yaml:"tables,omitempty"`
}

// PartitionConfig controls partition migration behavior.
type PartitionConfig struct {
	Migrate bool `yaml:"migrate"`
}

// SelectGenConfig holds SELECT generation settings.
type SelectGenConfig struct {
	OutputDir        string      `yaml:"output_dir,omitempty"`
	Batch            BatchConfig `yaml:"batch,omitempty"`
	IncludeRowNumber bool        `yaml:"include_row_number,omitempty"`
	AddExportColumns bool        `yaml:"add_export_columns,omitempty"`
}

// ExportConfig holds data export settings.
type ExportConfig struct {
	OutputDir string          `yaml:"output_dir,omitempty"`
	Format    string          `yaml:"format,omitempty"`
	CSV       ExportCSVConfig `yaml:"csv,omitempty"`
	Batch     BatchConfig     `yaml:"batch,omitempty"`
	Parallel  ParallelConfig  `yaml:"parallel,omitempty"`
	Tables    TableListConfig `yaml:"tables,omitempty"`

	// Filters maps a table-selection pattern (same glob semantics as
	// tables.include: "S.T" / "S.*" / "*.T" / "T_*"; exact name wins over
	// glob) to a literal WHERE fragment applied on the source when exporting
	// that table. The fragment is never parsed by the tool: syntax and column
	// errors are surfaced by the database during the conditional-COUNT gate
	// (see FiltersCheck). It must be deterministic (keyset pagination) and
	// must not contain bind placeholders, ';' or SQL comments — validate
	// rejects those. agent channel composes the same way (SQL-level).
	Filters map[string]string `yaml:"filters,omitempty"`
	// FiltersCheck controls the pre-export conditional-COUNT gate:
	// "count" (default; SELECT COUNT(*) WHERE <filter> — validates syntax and
	// columns and yields the source-side expected row count) or "off" (skip;
	// large-table escape hatch, report loses the source-side baseline).
	FiltersCheck string `yaml:"filters_check,omitempty"`

	// Columns prunes and renames the exported column set per table:
	// include's list order defines the CSV column order (source order is
	// ignored); rename maps source column → output name in place. Primary
	// key columns must survive (keyset pagination depends on them).
	Columns ExportColumnsConfig `yaml:"columns,omitempty"`
}

// ExportColumnsConfig holds per-table column projection/rename rules.
type ExportColumnsConfig struct {
	// Include maps table pattern → ordered column list. Output order = list
	// order. Empty/absent = all source columns in source order.
	Include map[string][]string `yaml:"include,omitempty"`
	// Rename maps table pattern → {source column: output name}. Applied after
	// include pruning, in place.
	Rename map[string]map[string]string `yaml:"rename,omitempty"`
}

// ExportCSVConfig holds export-specific CSV settings.
type ExportCSVConfig struct {
	Delimiter          string            `yaml:"delimiter,omitempty"`
	LineTerminator     string            `yaml:"line_terminator,omitempty"`
	QuoteChar          string            `yaml:"quote_char,omitempty"`
	EscapeChar         string            `yaml:"escape_char,omitempty"`
	Encoding           string            `yaml:"encoding,omitempty"`
	Header             bool              `yaml:"header,omitempty"`
	NullRepresentation string            `yaml:"null_representation,omitempty"`
	NullOverrides      map[string]string `yaml:"null_overrides,omitempty"`
	EmptyStringToNull  bool              `yaml:"empty_string_to_null,omitempty"`
}

// ImportConfig holds data import settings.
type ImportConfig struct {
	SourceDir      string             `yaml:"source_dir,omitempty"`
	Format         string             `yaml:"format,omitempty"`
	CSV            ImportCSVConfig    `yaml:"csv,omitempty"`
	Target         ImportTargetConfig `yaml:"target,omitempty"`
	Batch          ImportBatchConfig  `yaml:"batch,omitempty"`
	Parallel       ParallelConfig     `yaml:"parallel,omitempty"`
	DataTransforms DataTransforms     `yaml:"data_transforms,omitempty"`
}

// ImportCSVConfig holds import-specific CSV settings.
type ImportCSVConfig struct {
	Delimiter       string               `yaml:"delimiter,omitempty"`
	Encoding        string               `yaml:"encoding,omitempty"`
	HasHeader       bool                 `yaml:"has_header,omitempty"`
	NullMarker      string               `yaml:"null_marker,omitempty"`
	NullIdentifiers NullIdentifierConfig `yaml:"null_identifiers,omitempty"`
	NullSemantics   NullSemanticsConfig  `yaml:"null_semantics,omitempty"`
}

// NullIdentifierConfig holds NULL recognition rules.
type NullIdentifierConfig struct {
	Strings       []string `yaml:"strings,omitempty"`
	CaseSensitive bool     `yaml:"case_sensitive,omitempty"`
	Regex         string   `yaml:"regex,omitempty"`
}

// NullSemanticsConfig holds database-specific NULL semantics.
type NullSemanticsConfig struct {
	OracleEmptyStringIsNull bool `yaml:"oracle_empty_string_is_null,omitempty"`
	NumericZeroNotNull      bool `yaml:"numeric_zero_not_null,omitempty"`
}

// ImportTargetConfig holds import target table options.
type ImportTargetConfig struct {
	TruncateBefore     bool `yaml:"truncate_before"`
	DisableConstraints bool `yaml:"disable_constraints,omitempty"`
	DisableTriggers    bool `yaml:"disable_triggers,omitempty"`
	DropIndexes        bool `yaml:"drop_indexes,omitempty"`
}

// ImportBatchConfig holds batch insertion settings.
type ImportBatchConfig struct {
	CommitInterval      int    `yaml:"commit_interval"`
	ErrorPolicy         string `yaml:"error_policy,omitempty"`
	MaxErrorsBeforeStop int    `yaml:"max_errors_before_stop,omitempty"`
	// UseCopy enables the PostgreSQL COPY fast path for PG-family targets.
	// Falls back to batched INSERT automatically when COPY cannot be used.
	UseCopy bool `yaml:"use_copy,omitempty"`
}

// DataTransforms holds data transformation rules.
type DataTransforms struct {
	DatetimeFormat           string   `yaml:"datetime_format,omitempty"`
	// ColumnDatetimeFormats overrides DatetimeFormat per column. Keys are
	// "SCHEMA.TABLE.COLUMN" (case-insensitive on lookup); values use the same
	// compact-template grammar (yyyyMMddHHmmss / yyyyMMdd).
	ColumnDatetimeFormats    map[string]string `yaml:"column_datetime_formats,omitempty"`
	DatetimeFormatFallback   []string `yaml:"datetime_format_fallback,omitempty"`
	DatetimeTruncateToTarget bool     `yaml:"datetime_truncate_to_target,omitempty"`
	TrimStrings              bool     `yaml:"trim_strings"`
	NullIf                   []string `yaml:"null_if,omitempty"`
	SourceEncoding           string   `yaml:"source_encoding,omitempty"` // e.g. "GBK", "" = UTF-8
}

// BatchConfig holds shared batch processing settings.
type BatchConfig struct {
	Method   string `yaml:"method,omitempty"`
	PageSize int    `yaml:"page_size"`
}

// ParallelConfig holds parallel execution settings.
type ParallelConfig struct {
	Enabled            bool `yaml:"enabled,omitempty"`
	MaxWorkers         int  `yaml:"max_workers,omitempty"`
	RespectForeignKeys bool `yaml:"respect_foreign_keys,omitempty"`
}

// TableListConfig holds per-table configuration.
type TableListConfig struct {
	Include []string           `yaml:"include,omitempty"`
	Exclude TableExcludeConfig `yaml:"exclude,omitempty"`
}

// OnlineConfig holds configuration for the online incremental migration
// (owl-migrate online) feature: trigger CDC capture and ordered replay.
type OnlineConfig struct {
	CDC     OnlineCDCConfig     `yaml:"cdc"`
	Sync    OnlineSyncConfig    `yaml:"sync"`
	Files   OnlineFilesConfig   `yaml:"files"`
	Archive OnlineArchiveConfig `yaml:"archive"`
	State   OnlineStateConfig   `yaml:"state"`
}

// OnlineCDCConfig configures changelog/trigger generation (online init).
type OnlineCDCConfig struct {
	ChangelogPrefix string   `yaml:"changelog_prefix"`
	Tables          []string `yaml:"tables"`
	Apply           bool     `yaml:"apply"`
	ScriptDir       string   `yaml:"script_dir"`
	RequireKey      bool     `yaml:"require_key"`
}

// OnlineSyncConfig configures the changelog poller/replayer (online sync).
type OnlineSyncConfig struct {
	PollInterval string `yaml:"poll_interval"`
	BatchSize    int    `yaml:"batch_size"`
	OnError      string `yaml:"on_error"`
	ErrorTable   string `yaml:"error_table"`
}

// OnlineFilesConfig configures the file-batch fallback directories.
type OnlineFilesConfig struct {
	Pending string `yaml:"pending"`
	Done    string `yaml:"done"`
	Failed  string `yaml:"failed"`
}

// OnlineArchiveConfig configures done/ archiving (online archive).
type OnlineArchiveConfig struct {
	Enabled bool   `yaml:"enabled"`
	Format  string `yaml:"format"`
	Dir     string `yaml:"dir"`
}

// OnlineStateConfig configures checkpoint storage.
type OnlineStateConfig struct {
	DB string `yaml:"db"`
}

// isOnlineZero reports whether the OnlineConfig carries no meaningful values.
func (o OnlineConfig) isZero() bool {
	return !o.CDC.Apply && !o.CDC.RequireKey && o.Sync.PollInterval == "" && o.Sync.OnError == "" &&
		!o.Archive.Enabled && o.State.DB == "" && len(o.CDC.Tables) == 0
}

// Load reads and validates a YAML config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %q: %w", path, err)
	}

	// Apply defaults
	cfg.ApplyDefaults()

	// Publish external owljdbc profiles before validation so validation
	// errors (if any) surface immediately.
	if err := cfg.RegisterOwlJDBCProfiles(); err != nil {
		return nil, err
	}

	// Validate
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ApplyDefaults fills unset fields with documented defaults. Beyond Load it
// must also run wherever a Config enters the system without going through
// Load — e.g. the serve config-upload path — so the global agent section
// reaches per-connection settings before dbconn.Open resolves jars.
func (c *Config) ApplyDefaults() {
	if c.General.LogLevel == "" {
		c.General.LogLevel = "info"
	}
	if c.General.LogFormat == "" {
		c.General.LogFormat = "text"
	}
	// AI vendor settings default to the deepseek preset; zero-value stays
	// zero in MarshalYAML except for provider-dependent resolution done here.
	if c.AI != (AIConfig{}) {
		c.AI.ApplyDefaults()
	}
	// Global agent defaults flow into per-connection settings so dbconn.Open
	// (which only sees a DBConfig) resolves jars/JVM without root context.
	for _, db := range []*DBConfig{&c.Source, &c.Target} {
		if db.Agent.JarsDir == "" {
			db.Agent.JarsDir = c.Agent.JarsDir
		}
		if db.Agent.AgentJar == "" {
			db.Agent.AgentJar = c.Agent.AgentJar
		}
		if db.Agent.JavaHome == "" {
			db.Agent.JavaHome = c.Agent.JavaHome
		}
	}
	if c.Metadata.CSV.Delimiter == "" {
		c.Metadata.CSV.Delimiter = ","
	}
	if c.Metadata.CSV.Encoding == "" {
		c.Metadata.CSV.Encoding = "utf-8"
	}
	if c.Metadata.CSV.ColumnNameMatching == "" {
		c.Metadata.CSV.ColumnNameMatching = "case_insensitive"
	}
	if c.DDL.TableFilter.Include == nil {
		c.DDL.TableFilter.Include = []string{"*"}
	}
	if c.DDL.TargetDialect == "" && c.Target.Type != "" {
		t := strings.ToLower(strings.TrimSpace(c.Target.Type))
		if alias, ok := dialectAliases[t]; ok {
			t = alias
		}
		c.DDL.TargetDialect = t
	}
	if c.Export.Batch.PageSize == 0 {
		c.Export.Batch.PageSize = 5000
	}
	if c.Import.Batch.CommitInterval == 0 {
		c.Import.Batch.CommitInterval = 1000
	}
	if !c.Metadata.CSV.HasHeader {
		c.Metadata.CSV.HasHeader = true
	}
	// export.csv.header / import.csv.has_header 的文档默认值为 true。
	// migrate 管线内 exporter→importer 以 CSV 首行为表头衔接：缺省 false 时
	// exporter 不写表头，importer 会把首行数据当列名，全部插入错位。
	if !c.Export.CSV.Header {
		c.Export.CSV.Header = true
	}
	if !c.Import.CSV.HasHeader {
		c.Import.CSV.HasHeader = true
	}
	// online defaults
	if c.Online.CDC.ChangelogPrefix == "" {
		c.Online.CDC.ChangelogPrefix = "owl_chg_"
	}
	if c.Online.CDC.ScriptDir == "" {
		c.Online.CDC.ScriptDir = "./output/online/"
	}
	if c.Online.Sync.PollInterval == "" {
		c.Online.Sync.PollInterval = "1s"
	}
	if c.Online.Sync.BatchSize == 0 {
		c.Online.Sync.BatchSize = 500
	}
	if c.Online.Sync.OnError == "" {
		c.Online.Sync.OnError = "skip"
	}
	if c.Online.Sync.ErrorTable == "" {
		c.Online.Sync.ErrorTable = "owl_sync_error"
	}
	if c.Online.Archive.Format == "" {
		c.Online.Archive.Format = "tar.gz"
	}
	if !c.Online.Archive.Enabled {
		c.Online.Archive.Enabled = true
	}
	if c.Online.Archive.Dir == "" {
		c.Online.Archive.Dir = "./online/archive/"
	}
	if c.Online.Files.Pending == "" {
		c.Online.Files.Pending = "./online/pending/"
	}
	if c.Online.Files.Done == "" {
		c.Online.Files.Done = "./online/done/"
	}
	if c.Online.Files.Failed == "" {
		c.Online.Files.Failed = "./online/failed/"
	}
	if c.Online.State.DB == "" {
		c.Online.State.DB = "./output/online/online.db"
	}
}

func (c *Config) validate() error {
	switch strings.ToLower(strings.TrimSpace(c.Export.FiltersCheck)) {
	case "", "count", "off":
	default:
		return fmt.Errorf("invalid export.filters_check %q: must be count or off", c.Export.FiltersCheck)
	}
	if c.Metadata.Type == "" {
		return fmt.Errorf("metadata.type is required")
	}
	if !ValidMetadataTypes[c.Metadata.Type] {
		return fmt.Errorf("unsupported metadata.type %q: must be one of %v", c.Metadata.Type, mapKeys(ValidMetadataTypes))
	}
	if c.Metadata.Type == "database" {
		if c.Source.Type == "" {
			return fmt.Errorf("source.type is required when metadata.type is 'database'")
		}
		if c.Source.DSN == "" {
			return fmt.Errorf("source.dsn is required when metadata.type is 'database'")
		}
	}
	if c.Metadata.Type == "xlsx" && c.Metadata.XLSX.Path == "" {
		return fmt.Errorf("metadata.xlsx.path is required when metadata.type is 'xlsx'")
	}
	if c.DDL.TargetDialect == "" {
		return fmt.Errorf("ddl.target_dialect is required (or set target.type to inherit its dialect)")
	}
	if !ValidDialects[c.DDL.TargetDialect] {
		return fmt.Errorf("unknown ddl.target_dialect %q: must be one of %v", c.DDL.TargetDialect, mapKeys(ValidDialects))
	}
	if c.DDL.SourceDialect != "" && !ValidDialects[c.DDL.SourceDialect] {
		return fmt.Errorf("unknown ddl.source_dialect %q: must be one of %v", c.DDL.SourceDialect, mapKeys(ValidDialects))
	}
	if c.Import.Batch.ErrorPolicy != "" && !ValidErrorPolicies[c.Import.Batch.ErrorPolicy] {
		return fmt.Errorf("invalid import.batch.error_policy %q: must be one of %v", c.Import.Batch.ErrorPolicy, mapKeys(ValidErrorPolicies))
	}
	for _, dbc := range []struct {
		name string
		cfg  DBConfig
	}{{"source", c.Source}, {"target", c.Target}} {
		switch strings.ToLower(dbc.cfg.CompatMode) {
		case "", "mysql", "oracle":
		default:
			return fmt.Errorf("invalid %s.compat_mode %q: must be mysql or oracle", dbc.name, dbc.cfg.CompatMode)
		}
	}
	switch c.Online.Sync.OnError {
	case "", "skip", "stop", "retry":
	default:
		return fmt.Errorf("invalid online.sync.on_error %q: must be skip/stop/retry", c.Online.Sync.OnError)
	}
	switch strings.ToLower(strings.TrimSpace(c.Export.FiltersCheck)) {
	case "", "count", "off":
	default:
		return fmt.Errorf("invalid export.filters_check %q: must be count or off", c.Export.FiltersCheck)
	}
	if c.AI != (AIConfig{}) {
		a := c.AI
		a.ApplyDefaults()
		// effort 留空合法（custom 端点不发送该字段），只拒绝非空的非法值。
		if a.Effort != "" && !ValidAIEfforts[a.Effort] {
			return fmt.Errorf("invalid ai.effort %q: must be low, high or max", a.Effort)
		}
		if a.PlanEffortStr != "" && !ValidAIEfforts[a.PlanEffortStr] {
			return fmt.Errorf("invalid ai.plan_effort %q: must be low, high or max", a.PlanEffortStr)
		}
		if a.BaseURL == "" {
			return fmt.Errorf("ai.base_url is required for provider %q (no preset)", a.Provider)
		}
		if a.MaxTokens <= 0 || a.ContextWindow <= 0 || a.MaxRepairRounds <= 0 {
			return fmt.Errorf("ai.max_tokens / ai.context_window / ai.max_repair_rounds must be positive")
		}
	}
	return nil
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// Selector 把 table_filter 转换为一元对象选择器（metadata.ObjectSelector），
// 供 MatchTable 及请求/生成层共用（ADR-003）。
func (c TableFilterConfig) Selector() md.ObjectSelector {
	sel := md.SelectorFromInclude(c.Include)
	sel.Exclude = md.ExcludeFilter{
		Glob:    c.Exclude.Glob,
		Regex:   c.Exclude.Regex,
		Schemas: c.Exclude.Schemas,
		Tables:  c.Exclude.Tables,
	}
	return sel
}

// MatchTable checks whether a table matches the include/exclude filter rules.
// Semantics unified with metadata.ObjectSelector (ADR-003)：匹配大小写不敏感；
// 优先级 = 显式点名精确表 > exclude > glob include。
// 兼容旧行为：include 为空时任何表都不匹配（调用方在空 include 时应自行跳过过滤）。
func MatchTable(f TableFilterConfig, schema, table string) bool {
	if len(f.Include) == 0 {
		return false
	}
	return f.Selector().Matches(schema, table)
}
