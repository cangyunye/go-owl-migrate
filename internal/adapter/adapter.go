// Package adapter loads external target adapter plugins for online incremental
// migration. A target adapter is a declarative YAML describing how to reach and
// write to a target database that has no built-in Go driver, in one of three
// execution modes:
//
//	native      — process-internal Go driver replay
//	client      — replay via an official CLI client
//	file-batch  — spill SQL batch files executed externally (fallback)
package adapter

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/cdc"
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
)

// Mode is the adapter execution mode.
type Mode string

const (
	ModeNative    Mode = "native"
	ModeClient    Mode = "client"
	ModeFileBatch Mode = "file-batch"
)

// Adapter is a parsed target adapter plugin.
type Adapter struct {
	Name           string                       `yaml:"name"`
	Mode           Mode                         `yaml:"mode"`
	Quote          string                       `yaml:"quote"`
	Placeholder    string                       `yaml:"placeholder"`
	IdentifierCase string                       `yaml:"identifier_case"` // lower/upper/keep
	Driver         string                       `yaml:"driver"`
	Client         ClientConfig                 `yaml:"client"`
	Metadata       MetadataConfig               `yaml:"metadata"`
	TypeMap        map[string]string            `yaml:"type_map"`
	FallbackType   string                       `yaml:"fallback_type"`
	ColumnMap      map[string]map[string]string `yaml:"column_map"`
	rawTypeMap     map[string]string
}

// ClientConfig describes how to invoke an external CLI client.
type ClientConfig struct {
	Command      string      `yaml:"command"`
	ArgsTemplate string      `yaml:"args_template"`
	Transaction  Transaction `yaml:"transaction"`
}

// Transaction controls file-level transaction wrapping for client/file-batch.
type Transaction struct {
	Begin  string `yaml:"begin"`
	Commit string `yaml:"commit"`
	Wrap   bool   `yaml:"wrap"`
}

// MetadataConfig optionally provides queries for native-mode metadata discovery.
type MetadataConfig struct {
	ListTables  string `yaml:"list_tables"`
	ListColumns string `yaml:"list_columns"`
}

// Load reads and parses an adapter YAML file from disk.
func Load(path string) (*Adapter, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read adapter %q: %w", path, err)
	}
	return Parse(raw)
}

// Parse decodes adapter YAML (optionally wrapped under an `adapter:` root),
// applying defaults and validation.
func Parse(raw []byte) (*Adapter, error) {
	var a Adapter
	// Support both a bare `name: ...` document and the `adapter:` root wrapper
	// used in the design examples.
	if err := yaml.Unmarshal(raw, &struct {
		Adapter *Adapter `yaml:"adapter"`
	}{Adapter: &a}); err != nil {
		return nil, fmt.Errorf("parse adapter: %w", err)
	}
	// If the `adapter:` wrapper was absent, Unmarshal into the leaf struct.
	if a.Name == "" && a.Mode == "" && a.Client.Command == "" && len(a.TypeMap) == 0 {
		if err := yaml.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("parse adapter: %w", err)
		}
	}
	if err := a.applyDefaultsAndValidate(); err != nil {
		return nil, err
	}
	return &a, nil
}

func (a *Adapter) applyDefaultsAndValidate() error {
	switch a.Mode {
	case "":
		a.Mode = ModeNative
	case ModeNative, ModeClient, ModeFileBatch:
	default:
		return fmt.Errorf("unsupported adapter mode %q: must be native/client/file-batch", a.Mode)
	}
	if a.Mode != ModeNative && a.Client.Command == "" {
		return fmt.Errorf("adapter mode %q requires client.command", a.Mode)
	}
	if a.IdentifierCase == "" {
		a.IdentifierCase = "lower"
	}
	if a.FallbackType == "" {
		a.FallbackType = "text"
	}
	// Normalize type_map keys and keep the original under rawTypeMap. When the
	// adapter omits type_map, use the built-in default LogicalBase set.
	if len(a.TypeMap) == 0 {
		a.TypeMap = DefaultTypeMap()
	}
	a.rawTypeMap = make(map[string]string, len(a.TypeMap))
	for k, v := range a.TypeMap {
		a.rawTypeMap[strings.ToUpper(strings.TrimSpace(k))] = v
	}
	return nil
}

// DefaultTypeMap returns the built-in LogicalBase → target type template map.
func DefaultTypeMap() map[string]string {
	return map[string]string{
		"VARCHAR":     "varchar(%l)",
		"CHAR":        "char(%l)",
		"INT":         "integer",
		"BIGINT":      "bigint",
		"SMALLINT":    "smallint",
		"NUMERIC":     "numeric(%p,%s)",
		"FLOAT":       "real",
		"DOUBLE":      "double precision",
		"DATE":        "date",
		"TIME":        "time",
		"DATETIME":    "timestamp",
		"TIMESTAMP":   "timestamp",
		"TIMESTAMPTZ": "timestamptz",
		"INTERVAL":    "interval",
		"INTERVALYM":  "interval year to month",
		"INTERVALDS":  "interval day to second",
		"BOOLEAN":     "boolean",
		"CLOB":        "text",
		"BLOB":        "bytea",
		"JSON":        "jsonb",
		"XML":         "xml",
		"ENUM":        "text",
		"BINARY":      "bytea",
		"VARBINARY":   "bytea",
		"ROWID":       "text",
	}
}

// IsFileBatch reports whether the adapter uses file-batch mode.
func (a *Adapter) IsFileBatch() bool { return a.Mode == ModeFileBatch }

// Quoter returns an identifier quote function based on the adapter config.
func (a *Adapter) Quoter() func(string) string {
	q := a.Quote
	if q == "" {
		q = `"`
	}
	return func(name string) string { return q + name + q }
}

// PlaceholderFn returns a 1-based placeholder function (e.g. $N, ?, :N).
func (a *Adapter) PlaceholderFn() func(int) string {
	switch strings.ToLower(a.Placeholder) {
	case "?":
		return func(int) string { return "?" }
	default:
		// Default to $N or the literal $%d pattern.
		pat := a.Placeholder
		if pat == "" {
			pat = "$%d"
		}
		return func(i int) string { return fmt.Sprintf(pat, i) }
	}
}

// ResolveType maps a LogicalType to a target type string using type_map, with
// %l/%p/%s substitution. Falls back to a.FallbackType when unmapped or
// parameter-free.
func (a *Adapter) ResolveType(lt dialect.LogicalType) string {
	tmpl, ok := a.rawTypeMap[strings.ToUpper(lt.Base.String())]
	if !ok || tmpl == "" {
		return a.FallbackType
	}
	r := strings.NewReplacer(
		"%l", strOr(lt.Length),
		"%p", strOr(lt.Precision),
		"%s", strOr(lt.Scale),
	)
	return r.Replace(tmpl)
}

func strOr(v int) string {
	if v <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", v)
}

var typeParamRe = regexp.MustCompile(`%[lps]`)

// HasTypeParams reports whether the resolved template contains %l/%p/%s.
func (a *Adapter) HasTypeParams(lt dialect.LogicalType) bool {
	tmpl, ok := a.rawTypeMap[strings.ToUpper(lt.Base.String())]
	return ok && typeParamRe.MatchString(tmpl)
}

// ColumnMapFor returns the source→target column mapping for a table, if any.
func (a *Adapter) ColumnMapFor(table string) map[string]string {
	return a.ColumnMap[table]
}

// ToTargetTable builds a cdc.TargetTable for the given source table name using
// the adapter's quoter, placeholder, and column map.
func (a *Adapter) ToTargetTable(sourceTable, targetTable string, columns, keyCols []string) *cdc.TargetTable {
	tt := &cdc.TargetTable{
		Table:       targetTable,
		Columns:     columns,
		KeyCols:     keyCols,
		Quoter:      a.Quoter(),
		Placeholder: a.PlaceholderFn(),
	}
	if cm := a.ColumnMapFor(sourceTable); len(cm) > 0 {
		tt.ColumnMap = cm
	}
	return tt
}

// RunnerTemplateFor returns a cdc.RunnerTemplate for client/file-batch mode.
func (a *Adapter) RunnerTemplateFor(pending, done, failed string) (cdc.RunnerTemplate, error) {
	if a.Client.Command == "" {
		return cdc.RunnerTemplate{}, fmt.Errorf("adapter %q has no client.command", a.Name)
	}
	if !strings.Contains(a.Client.ArgsTemplate, "{file}") {
		return cdc.RunnerTemplate{}, fmt.Errorf("adapter %q client.args_template must contain {file}", a.Name)
	}
	return cdc.RunnerTemplate{
		Command:      a.Client.Command,
		ArgsTemplate: a.Client.ArgsTemplate,
		Pending:      pending,
		Done:         done,
		Failed:       failed,
	}, nil
}

// BatchWriterFor returns a cdc.BatchWriter configured from the adapter's
// client transaction settings and the given value formatter.
func (a *Adapter) BatchWriterFor(pendingDir string, fmtv cdc.ValueFormatter) *cdc.BatchWriter {
	txn := a.Client.Transaction
	return &cdc.BatchWriter{
		PendingDir: pendingDir,
		Begin:      txn.Begin,
		Commit:     txn.Commit,
		Wrap:       txn.Wrap,
		FMValue:    fmtv,
	}
}
