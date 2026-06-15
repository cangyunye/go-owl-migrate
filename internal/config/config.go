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
	if len(c.Extensions) > 0 {
		m.Extensions = c.Extensions
	}
	return m, nil
}

// IsZero returns true if the DBConfig has no meaningful values set.
func (d DBConfig) isZero() bool {
	return d.Type == "" && d.DSN == "" && d.Schema == "" && d.Charset == "" && d.ConnectTimeout == "" && d.QueryTimeout == ""
}

// IsZero returns true if the DDLConfig has no meaningful values set.
func (d DDLConfig) isZero() bool {
	return d.TargetDialect == "" && !d.IncludeComments && !d.IncludeIfNotExists && !d.NoQuoteIdentifiers && len(d.SchemaMapping) == 0
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
func (b BatchConfig) isZero() bool           { return b.Method == "" && b.PageSize == 0 }
func (p ParallelConfig) isZero() bool        { return !p.Enabled && p.MaxWorkers == 0 }
func (e ExportCSVConfig) isZero() bool        { return e.Delimiter == "" && !e.Header && e.NullRepresentation == "" && e.QuoteChar == "" }
func (i ImportCSVConfig) isZero() bool        { return i.Delimiter == "" && i.NullMarker == "" && !i.HasHeader }
func (t ImportTargetConfig) isZero() bool     { return !t.TruncateBefore && !t.DisableConstraints && !t.DisableTriggers && !t.DropIndexes }
func (b ImportBatchConfig) isZero() bool      { return b.CommitInterval == 0 && b.ErrorPolicy == "" }
func (d DataTransforms) isZero() bool         { return d.DatetimeFormat == "" && !d.TrimStrings && len(d.NullIf) == 0 }
func (t TableListConfig) isZero() bool        { return len(t.Include) == 0 }

// ValidDialects lists supported target dialects.
var ValidDialects = map[string]bool{
	"oracle":           true,
	"postgres":         true,
	"mysql":            true,
	"goldendb":         true,
	"goldendb-mysql":   true,
	"goldendb-oracle":  true,
	"oceanbase":        true,
	"oceanbase-mysql":  true,
	"oceanbase-oracle": true,
	"panweidb":         true,
	"panweidb-mysql":    true,
	"panweidb-oracle":   true,
	"opengaussdb":      true,
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
	Type  string    `yaml:"type"` // csv/xlsx/database
	CSV   CSVConfig `yaml:"csv"`
	XLSX  XLSXConfig `yaml:"xlsx"`
	Files []string  `yaml:"files,omitempty"`
}

// XLSXConfig holds xlsx loading settings.
type XLSXConfig struct {
	Path          string `yaml:"path"`                     // path to .xlsx file
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
	Type           string `yaml:"type"`
	DSN            string `yaml:"dsn"`
	Schema         string `yaml:"schema"`
	Charset        string `yaml:"charset,omitempty"`
	ConnectTimeout string `yaml:"connect_timeout,omitempty"`
	QueryTimeout   string `yaml:"query_timeout,omitempty"`
}

// DDLConfig holds DDL generation settings.
type DDLConfig struct {
	OutputDir          string            `yaml:"output_dir,omitempty"`
	TargetDialect      string            `yaml:"target_dialect"`
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
	QuotePolicy        string            `yaml:"quote_policy,omitempty"`
	NewlineHandling    string            `yaml:"newline_handling,omitempty"`
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
}

// DataTransforms holds data transformation rules.
type DataTransforms struct {
	DatetimeFormat           string   `yaml:"datetime_format,omitempty"`
	DatetimeFormatFallback   []string `yaml:"datetime_format_fallback,omitempty"`
	DatetimeTruncateToTarget bool     `yaml:"datetime_truncate_to_target,omitempty"`
	TrimStrings              bool     `yaml:"trim_strings"`
	NullIf                   []string `yaml:"null_if,omitempty"`
	SourceEncoding           string   `yaml:"source_encoding,omitempty"` // e.g. "GBK", "" = UTF-8
	TargetEncoding           string   `yaml:"target_encoding,omitempty"` // e.g. "GBK", "" = UTF-8
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
	MaxTableWorkers    int  `yaml:"max_table_workers,omitempty"`
	RespectForeignKeys bool `yaml:"respect_foreign_keys,omitempty"`
}

// TableListConfig holds per-table configuration.
type TableListConfig struct {
	Include   []string                 `yaml:"include,omitempty"`
	Exclude   TableExcludeConfig       `yaml:"exclude,omitempty"`
	Overrides map[string]TableOverride `yaml:"overrides,omitempty"`
}

// TableOverride holds per-table override settings.
type TableOverride struct {
	PageSize        int `yaml:"page_size,omitempty"`
	MaxTableWorkers int `yaml:"max_table_workers,omitempty"`
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
	if c.Export.Batch.PageSize == 0 {
		c.Export.Batch.PageSize = 5000
	}
	if c.Import.Batch.CommitInterval == 0 {
		c.Import.Batch.CommitInterval = 1000
	}
	if !c.Metadata.CSV.HasHeader {
		c.Metadata.CSV.HasHeader = true
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
		return fmt.Errorf("ddl.target_dialect is required")
	}
	if !ValidDialects[c.DDL.TargetDialect] {
		return fmt.Errorf("unknown ddl.target_dialect %q: must be one of %v", c.DDL.TargetDialect, mapKeys(ValidDialects))
	}
	if c.Import.Batch.ErrorPolicy != "" && !ValidErrorPolicies[c.Import.Batch.ErrorPolicy] {
		return fmt.Errorf("invalid import.batch.error_policy %q: must be one of %v", c.Import.Batch.ErrorPolicy, mapKeys(ValidErrorPolicies))
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
	// Check includes first
	matched := false
	for _, inc := range f.Include {
		if inc == "*" {
			matched = true
			break
		}
		if m, _ := filepath.Match(inc, schema+"."+table); m {
			matched = true
			break
		}
		if m, _ := filepath.Match(inc, table); m {
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
