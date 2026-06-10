package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

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
	"opengaussdb":      true,
}

// ValidMetadataTypes lists supported metadata source types.
var ValidMetadataTypes = map[string]bool{
	"csv":      true,
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
}

// GeneralConfig holds top-level settings.
type GeneralConfig struct {
	LogLevel  string `yaml:"log_level"`
	LogFile   string `yaml:"log_file"`
	LogFormat string `yaml:"log_format"`
}

// MetadataConfig holds metadata source configuration.
type MetadataConfig struct {
	Type  string    `yaml:"type"` // csv/xlsx/database
	CSV   CSVConfig `yaml:"csv"`
	Files []string  `yaml:"files"`
}

// CSVConfig holds CSV parsing settings.
type CSVConfig struct {
	Path               string `yaml:"path"`
	Delimiter          string `yaml:"delimiter"`
	Encoding           string `yaml:"encoding"`
	HasHeader          bool   `yaml:"has_header"`
	NullMarker         string `yaml:"null_marker"`
	ColumnNameMatching string `yaml:"column_name_matching"`
}

// DBConfig holds database connection settings.
type DBConfig struct {
	Type           string `yaml:"type"`
	DSN            string `yaml:"dsn"`
	Schema         string `yaml:"schema"`
	Charset        string `yaml:"charset"`
	ConnectTimeout string `yaml:"connect_timeout"`
	QueryTimeout   string `yaml:"query_timeout"`
}

// DDLConfig holds DDL generation settings.
type DDLConfig struct {
	OutputDir          string            `yaml:"output_dir"`
	TargetDialect      string            `yaml:"target_dialect"`
	IncludeComments    bool              `yaml:"include_comments"`
	IncludeIfNotExists bool              `yaml:"include_if_not_exists"`
	IncludeDrop        bool              `yaml:"include_drop"`
	SplitByObject      bool              `yaml:"split_by_object"`
	SchemaMapping      map[string]string `yaml:"schema_mapping"`
	TableFilter        TableFilterConfig `yaml:"table_filter"`
	TypeOverrides      map[string]string `yaml:"type_overrides"`
	IdentityToSerial   bool              `yaml:"identity_to_serial"`
	AddRowIDColumn     bool              `yaml:"add_rowid_column"`
	EmptyStringToNull  bool              `yaml:"empty_string_to_null"`
	BooleanMapping     map[string]bool   `yaml:"boolean_mapping"`
	Partition          PartitionConfig   `yaml:"partition"`
	NoQuoteIdentifiers  bool               `yaml:"no_quote_identifiers"`
}

// TableFilterConfig holds table include/exclude rules.
type TableFilterConfig struct {
	Include []string           `yaml:"include"`
	Exclude TableExcludeConfig `yaml:"exclude"`
}

// TableExcludeConfig holds table exclusion rules.
type TableExcludeConfig struct {
	Glob    []string `yaml:"glob"`
	Regex   []string `yaml:"regex"`
	Schemas []string `yaml:"schemas"`
	Tables  []string `yaml:"tables"`
}

// PartitionConfig controls partition migration behavior.
type PartitionConfig struct {
	Migrate bool `yaml:"migrate"`
}

// SelectGenConfig holds SELECT generation settings.
type SelectGenConfig struct {
	OutputDir        string      `yaml:"output_dir"`
	Batch            BatchConfig `yaml:"batch"`
	IncludeRowNumber bool        `yaml:"include_row_number"`
	AddExportColumns bool        `yaml:"add_export_columns"`
}

// ExportConfig holds data export settings.
type ExportConfig struct {
	OutputDir string          `yaml:"output_dir"`
	Format    string          `yaml:"format"`
	CSV       ExportCSVConfig `yaml:"csv"`
	Batch     BatchConfig     `yaml:"batch"`
	Parallel  ParallelConfig  `yaml:"parallel"`
	Tables    TableListConfig `yaml:"tables"`
}

// ExportCSVConfig holds export-specific CSV settings.
type ExportCSVConfig struct {
	Delimiter          string            `yaml:"delimiter"`
	LineTerminator     string            `yaml:"line_terminator"`
	QuoteChar          string            `yaml:"quote_char"`
	EscapeChar         string            `yaml:"escape_char"`
	Encoding           string            `yaml:"encoding"`
	Header             bool              `yaml:"header"`
	NullRepresentation string            `yaml:"null_representation"`
	NullOverrides      map[string]string `yaml:"null_overrides"`
	EmptyStringToNull  bool              `yaml:"empty_string_to_null"`
	QuotePolicy        string            `yaml:"quote_policy"`
	NewlineHandling    string            `yaml:"newline_handling"`
}

// ImportConfig holds data import settings.
type ImportConfig struct {
	SourceDir      string             `yaml:"source_dir"`
	Format         string             `yaml:"format"`
	CSV            ImportCSVConfig    `yaml:"csv"`
	Target         ImportTargetConfig `yaml:"target"`
	Batch          ImportBatchConfig  `yaml:"batch"`
	Parallel       ParallelConfig     `yaml:"parallel"`
	DataTransforms DataTransforms     `yaml:"data_transforms"`
}

// ImportCSVConfig holds import-specific CSV settings.
type ImportCSVConfig struct {
	Delimiter       string               `yaml:"delimiter"`
	Encoding        string               `yaml:"encoding"`
	HasHeader       bool                 `yaml:"has_header"`
	NullMarker      string               `yaml:"null_marker"`
	NullIdentifiers NullIdentifierConfig `yaml:"null_identifiers"`
	NullSemantics   NullSemanticsConfig  `yaml:"null_semantics"`
}

// NullIdentifierConfig holds NULL recognition rules.
type NullIdentifierConfig struct {
	Strings       []string `yaml:"strings"`
	CaseSensitive bool     `yaml:"case_sensitive"`
	Regex         string   `yaml:"regex"`
}

// NullSemanticsConfig holds database-specific NULL semantics.
type NullSemanticsConfig struct {
	OracleEmptyStringIsNull bool `yaml:"oracle_empty_string_is_null"`
	NumericZeroNotNull      bool `yaml:"numeric_zero_not_null"`
}

// ImportTargetConfig holds import target table options.
type ImportTargetConfig struct {
	TruncateBefore     bool `yaml:"truncate_before"`
	DisableConstraints bool `yaml:"disable_constraints"`
	DisableTriggers    bool `yaml:"disable_triggers"`
	DropIndexes        bool `yaml:"drop_indexes"`
}

// ImportBatchConfig holds batch insertion settings.
type ImportBatchConfig struct {
	CommitInterval      int    `yaml:"commit_interval"`
	ErrorPolicy         string `yaml:"error_policy"`
	MaxErrorsBeforeStop int    `yaml:"max_errors_before_stop"`
}

// DataTransforms holds data transformation rules.
type DataTransforms struct {
	DatetimeFormat           string   `yaml:"datetime_format"`
	DatetimeFormatFallback   []string `yaml:"datetime_format_fallback"`
	DatetimeTruncateToTarget bool     `yaml:"datetime_truncate_to_target"`
	TrimStrings              bool     `yaml:"trim_strings"`
	NullIf                   []string `yaml:"null_if"`
	SourceEncoding           string   `yaml:"source_encoding"` // e.g. "GBK", "" = UTF-8
	TargetEncoding           string   `yaml:"target_encoding"` // e.g. "GBK", "" = UTF-8
}

// BatchConfig holds shared batch processing settings.
type BatchConfig struct {
	Method   string `yaml:"method"`
	PageSize int    `yaml:"page_size"`
}

// ParallelConfig holds parallel execution settings.
type ParallelConfig struct {
	Enabled            bool `yaml:"enabled"`
	MaxWorkers         int  `yaml:"max_workers"`
	MaxTableWorkers    int  `yaml:"max_table_workers"`
	RespectForeignKeys bool `yaml:"respect_foreign_keys"`
}

// TableListConfig holds per-table configuration.
type TableListConfig struct {
	Include   []string                 `yaml:"include"`
	Exclude   TableExcludeConfig       `yaml:"exclude"`
	Overrides map[string]TableOverride `yaml:"overrides"`
}

// TableOverride holds per-table override settings.
type TableOverride struct {
	PageSize        int `yaml:"page_size"`
	MaxTableWorkers int `yaml:"max_table_workers"`
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
