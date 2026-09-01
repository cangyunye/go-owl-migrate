package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
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
	return d.TargetDialect == "" && d.SourceDialect == "" && !d.IncludeComments && !d.IncludeIfNotExists && !d.NoQuoteIdentifiers && len(d.SchemaMapping) == 0
}

// IsZero returns true if the SelectGenConfig has no meaningful values set.
func (s SelectGenConfig) isZero() bool {
	return s.OutputDir == "" && s.Batch.isZero() && !s.IncludeRowNumber && !s.AddExportColumns
}

// IsZero returns true if the ExportConfig has no meaningful values set.
func (e ExportConfig) isZero() bool {
	return e.OutputDir == "" && e.Format == "" && e.CSV.isZero() && e.Batch.isZero() && e.Parallel.isZero() && e.Tables.isZero()
}

// IsZero returns true if the ImportConfig has no meaningful values set.
func (i ImportConfig) isZero() bool {
	return i.SourceDir == "" && i.Format == "" && i.CSV.isZero() && i.Target.isZero() && i.Batch.isZero() && i.Parallel.isZero() && i.DataTransforms.isZero()
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
	return d.DatetimeFormat == "" && !d.TrimStrings && len(d.NullIf) == 0
}
func (t TableListConfig) isZero() bool { return len(t.Include) == 0 }

// ValidDialects lists supported target dialects.
var ValidDialects = map[string]bool{
	"oracle":           true,
	"postgres":         true,
	"mysql":            true,
	"sqlite3":          true,
	"duckdb":           true,
	"goldendb":         true,
	"goldendb-mysql":   true,
	"goldendb-oracle":  true,
	"oceanbase":        true,
	"oceanbase-mysql":  true,
	"oceanbase-oracle": true,
	"panweidb":         true,
	"panweidb-mysql":   true,
	"panweidb-oracle":  true,
	"opengaussdb":      true,
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

	// Adapter references an external target adapter plugin YAML (mode
	// native/client/file-batch) used by online incremental migration when the
	// target has no built-in Go driver.
	Adapter string `yaml:"adapter,omitempty"`

	// CompatMode applies to OceanBase: declares the tenant compatibility mode
	// ("mysql" or "oracle"). When empty it is auto-detected from the live
	// connection and a mismatch raises an error.
	CompatMode string `yaml:"compat_mode,omitempty"`
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
	cfg.applyDefaults()

	// Validate
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.General.LogLevel == "" {
		c.General.LogLevel = "info"
	}
	if c.General.LogFormat == "" {
		c.General.LogFormat = "text"
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
	return nil
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// MatchTable checks whether a table matches the include/exclude filter rules.
// Priority: includes → glob exclude → regex exclude → schema exclude → table exclude.
func MatchTable(f TableFilterConfig, schema, table string) bool {
	// Check includes first. Matching is case-insensitive: Oracle metadata is
	// uppercase while MySQL/PostgreSQL and CSV-derived names are often
	// lowercase, and a migration filter should bridge both.
	matched := false
	lt := strings.ToLower(table)
	lfull := strings.ToLower(schema + "." + table)
	for _, inc := range f.Include {
		if inc == "*" {
			matched = true
			break
		}
		li := strings.ToLower(inc)
		if m, _ := filepath.Match(li, lfull); m {
			matched = true
			break
		}
		if m, _ := filepath.Match(li, lt); m {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}

	// Check excludes
	e := f.Exclude

	// Glob excludes
	for _, g := range e.Glob {
		if m, _ := filepath.Match(g, table); m {
			return false
		}
		if m, _ := filepath.Match(g, schema+"."+table); m {
			return false
		}
	}

	// Regex excludes
	for _, r := range e.Regex {
		re, err := regexp.Compile(r)
		if err != nil {
			continue
		}
		if re.MatchString(table) || re.MatchString(schema+"."+table) {
			return false
		}
	}

	// Schema excludes
	for _, s := range e.Schemas {
		if strings.EqualFold(s, schema) {
			return false
		}
	}

	// Exact table excludes
	for _, t := range e.Tables {
		if strings.EqualFold(t, schema+"."+table) {
			return false
		}
	}

	return true
}
