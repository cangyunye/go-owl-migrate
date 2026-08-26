package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

// FieldCond makes a field visible only when another field equals a value
// (Value) or is one of several values (Values). Values takes precedence over
// Value when both are set.
type FieldCond struct {
	Field  string   `json:"field"`
	Value  string   `json:"value,omitempty"`
	Values []string `json:"values,omitempty"`
}

// Field describes a single form input for a scenario config page.
type Field struct {
	Name        string     `json:"name"`
	Label       string     `json:"label"`
	Type        string     `json:"type"` // select | text | password
	Options     []string   `json:"options,omitempty"`
	Default     string     `json:"default,omitempty"`
	Help        string     `json:"help,omitempty"`
	Placeholder string     `json:"placeholder,omitempty"`
	Required    bool       `json:"required,omitempty"`
	ShowWhen    *FieldCond `json:"show_when,omitempty"`
}

// Scenario is a config template matching one `owl-migrate init --scenario`.
type Scenario struct {
	Name        string  `json:"name"`
	Label       string  `json:"label"`
	Description string  `json:"description"`
	Command     string  `json:"command"`
	Fields      []Field `json:"fields"`
}

func dialectOptions() []string {
	keys := make([]string, 0, len(config.ValidDialects))
	for k := range config.ValidDialects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// DSN family keys classify dialects by connection shape, not wire protocol:
// they decide which structured login fields are offered and how the DSN is
// assembled in the UI.
const (
	familyMySQL           = "mysql"
	familyOracle          = "oracle"
	familyPostgres        = "postgres"
	familyOceanBaseMySQL  = "oceanbase-mysql"
	familyOceanBaseOracle = "oceanbase-oracle"
	familyFile            = "file"
)

// DSNFamilyMeta describes the structured connection fields for a DSN family,
// plus the template used to assemble a DSN from those fields. {key} tokens in
// Builder are replaced by the corresponding connection field value.
type DSNFamilyMeta struct {
	DBLabel       string `json:"db_label"`              // label for the database component (dbname/service/tenant)
	DBPlaceholder string `json:"db_placeholder"`        // placeholder for the database component
	Port          string `json:"port"`                  // default port shown as placeholder
	Builder       string `json:"builder"`               // DSN assembly template
	HasCluster    bool   `json:"has_cluster,omitempty"` // OceanBase optional cluster
	HasTenant     bool   `json:"has_tenant,omitempty"`  // OceanBase MySQL: tenant/cluster fold into the username
	URLStyle      bool   `json:"url_style"`             // assemble as URL (user/password/db need encoding)
}

func srcType() Field {
	return Field{Name: "source_type", Label: "源数据库类型", Type: "select", Options: dialectOptions(), Required: true}
}
func srcDSN() Field {
	return Field{Name: "source_dsn", Label: "源数据库 DSN", Type: "text", Required: true,
		Placeholder: "点击「结构化填写」或粘贴连接串"}
}
func srcSchema() Field {
	return Field{Name: "source_schema", Label: "源 schema", Type: "text", Help: "Oracle: 用户名；MySQL: 库名；PG: schema 名"}
}
func tgtType() Field {
	return Field{Name: "target_type", Label: "目标数据库类型", Type: "select", Options: dialectOptions(), Default: "postgres", Required: true}
}
func tgtDSN() Field {
	return Field{Name: "target_dsn", Label: "目标数据库 DSN", Type: "text", Placeholder: "点击「结构化填写」或粘贴连接串"}
}

// dsnFamily returns the connection family for a dialect, mirroring what the
// DSN format example in DSNExamples() implies.
func dsnFamily(dialect string) string {
	switch strings.ToLower(dialect) {
	case "oracle", "goldendb-oracle":
		return familyOracle
	case "goldendb", "goldendb-mysql", "mysql", "mariadb":
		return familyMySQL
	case "oceanbase-oracle":
		return familyOceanBaseOracle
	case "oceanbase", "oceanbase-mysql":
		return familyOceanBaseMySQL
	case "postgres", "postgresql", "opengaussdb", "panweidb", "panweidb-mysql", "panweidb-oracle":
		return familyPostgres
	case "sqlite3", "duckdb":
		return familyFile
	default:
		return ""
	}
}

// dsnFamilyMeta returns per-family connection metadata, ordered for stable
// display. The database/Build labels mirror DSNExamples() and cmd/init.go.
func dsnFamilyMeta() map[string]DSNFamilyMeta {
	return map[string]DSNFamilyMeta{
		familyMySQL: {
			DBLabel:       "数据库名",
			DBPlaceholder: "例如: mydb",
			Port:          "3306",
			Builder:       "{user}:{password}@tcp({host}:{port})/{db}",
			URLStyle:      true,
		},
		familyOracle: {
			DBLabel:       "服务名",
			DBPlaceholder: "例如: XEPDB1（或 ORCL）",
			Port:          "1521",
			Builder:       "oracle://{user}:{password}@{host}:{port}/{db}",
			URLStyle:      true,
		},
		familyPostgres: {
			DBLabel:       "数据库名",
			DBPlaceholder: "例如: mydb",
			Port:          "5432",
			Builder:       "host={host} port={port} user={user} password={password} dbname={db} sslmode=disable",
		},
		familyOceanBaseMySQL: {
			DBLabel:       "数据库名（租户库）",
			DBPlaceholder: "例如: test_db（租户内库名）",
			Port:          "2881",
			Builder:       "{user}:{password}@tcp({host}:{port})/{db}",
			HasCluster:    true,
			HasTenant:     true,
			URLStyle:      true,
		},
		familyOceanBaseOracle: {
			DBLabel:       "租户",
			DBPlaceholder: "例如: oracle_tenant",
			Port:          "2881",
			Builder:       "oceanbase-oracle://{user}:{password}@{host}:{port}/{db}",
			HasCluster:    true,
			URLStyle:      true,
		},
		familyFile: {
			DBLabel:       "数据库文件",
			DBPlaceholder: "例如: /path/to/database.db",
		},
	}
}

// DSNFamilies maps each dialect to its connection family.
func DSNFamilies() map[string]string {
	out := map[string]string{}
	for k := range config.ValidDialects {
		if f := dsnFamily(k); f != "" {
			out[k] = f
		}
	}
	return out
}

// DSNComponentMeta returns per-family structured connection metadata.
func DSNComponentMeta() map[string]DSNFamilyMeta {
	return dsnFamilyMeta()
}
func tgtSchema() Field {
	return Field{Name: "target_schema", Label: "目标 schema", Type: "text", Help: "留空则与源 schema 相同"}
}
func tablesField() Field {
	return Field{Name: "tables", Label: "迁移的表", Type: "text", Default: "*", Help: "逗号分隔多个表名，* 表示全部"}
}
func metaType() Field {
	return Field{Name: "metadata_type", Label: "元数据来源", Type: "select", Options: []string{"csv", "xlsx", "database"}, Default: "csv", Required: true}
}
func schemaMappingField() Field {
	return Field{Name: "schema_mapping", Label: "Schema 映射", Type: "text",
		Help: "源 schema 重命名到目标 schema，格式 源:目标，多个用逗号分隔，如 SCOTT:public,HR:hr。留空则不改名"}
}
func csvPath() Field {
	return Field{Name: "csv_path", Label: "CSV 元数据目录", Type: "text", Default: "./testdata/csv/", ShowWhen: &FieldCond{Field: "metadata_type", Value: "csv"}}
}
func xlsxPath() Field {
	return Field{Name: "xlsx_path", Label: "XLSX 文件路径", Type: "text", Default: "./metadata/schema.xlsx", ShowWhen: &FieldCond{Field: "metadata_type", Value: "xlsx"}}
}
func dbSourceFields() []Field {
	return []Field{
		withCond(srcType(), "metadata_type", "database"),
		withCond(srcDSN(), "metadata_type", "database"),
		withCond(srcSchema(), "metadata_type", "database"),
	}
}

func withCond(f Field, field, value string) Field {
	f.ShowWhen = &FieldCond{Field: field, Value: value}
	return f
}

// ScenarioSchemas returns every config scenario, mirroring `init --scenario`.
func ScenarioSchemas() []Scenario {
	return []Scenario{
		{
			Name: "migrate", Label: "完整迁移", Command: "owl-migrate migrate",
			Description: "源库 → 导出 CSV → 目标库，端到端自动完成",
			Fields: []Field{
				metaType(), csvPath(), xlsxPath(),
				srcType(), srcDSN(), srcSchema(), tablesField(),
				tgtType(), tgtDSN(), tgtSchema(), schemaMappingField(),
			},
		},
		{
			Name: "export-ddl", Label: "生成 DDL", Command: "owl-migrate export ddl",
			Description: "从元数据生成目标库建表语句",
			Fields: append([]Field{metaType(), csvPath(), xlsxPath()},
				append(dbSourceFields(), tgtType(), schemaMappingField())...),
		},
		{
			Name: "gen-select", Label: "生成 SELECT", Command: "owl-migrate gen-select",
			Description: "生成分页查询语句用于手动导出",
			Fields: append([]Field{metaType(), csvPath(), xlsxPath()},
				append(dbSourceFields(), tgtType())...),
		},
		{
			Name: "export", Label: "导出数据", Command: "owl-migrate export data",
			Description: "从源库导出 CSV / SQL / XLSX 文件",
			Fields:      []Field{srcType(), srcDSN(), srcSchema(), tablesField()},
		},
		{
			Name: "import", Label: "导入数据", Command: "owl-migrate import",
			Description: "将 CSV 文件导入目标数据库",
			Fields: []Field{
				{Name: "data_dir", Label: "CSV 数据目录", Type: "text", Default: "./output/data/", Required: true},
				tgtType(), withCondRequired(tgtDSN()), tgtSchema(),
			},
		},
		{
			Name: "export-insert", Label: "生成 INSERT", Command: "owl-migrate export insert",
			Description: "从 CSV 数据生成 INSERT SQL 文件",
			Fields: []Field{
				{Name: "metadata_type", Label: "数据来源", Type: "select", Options: []string{"csv", "xlsx"}, Default: "csv", Required: true},
				xlsxPath(),
				{Name: "data_output_dir", Label: "数据输出目录", Type: "text", Default: "./output/data/", ShowWhen: &FieldCond{Field: "metadata_type", Value: "xlsx"}},
				tgtType(),
			},
		},
		{
			Name: "export-metadata", Label: "导出元数据", Command: "owl-migrate export-metadata",
			Description: "从源库抽取表结构元数据",
			Fields: []Field{
				srcType(), srcDSN(), srcSchema(),
				{Name: "format", Label: "输出格式", Type: "select", Options: []string{"csv", "xlsx", "sql"}, Default: "csv"},
			},
		},
		{
			Name: "validate", Label: "校验配置", Command: "owl-migrate validate",
			Description: "校验元数据与配置的正确性",
			Fields: append([]Field{metaType(), csvPath(), xlsxPath()},
				append(dbSourceFields(), tgtType(), schemaMappingField())...),
		},
	}
}

func withCondRequired(f Field) Field {
	f.Required = true
	return f
}

// ScenarioSchema looks up a single scenario by name.
func ScenarioSchema(name string) (Scenario, bool) {
	for _, s := range ScenarioSchemas() {
		if s.Name == name {
			return s, true
		}
	}
	return Scenario{}, false
}

// DSNExamples returns a per-dialect DSN format hint, mirroring init's dsnExample.
func DSNExamples() map[string]string {
	return map[string]string{
		"oracle":           "oracle://user:pass@host:1521/service_name",
		"goldendb-oracle":  "oracle://user:pass@host:1521/service_name",
		"oceanbase-oracle": "oceanbase-oracle://user:pass@host:2881/db (or oracle://user:pass@host:2883/service via OBProxy TNS)",
		"panweidb-oracle":  "host=127.0.0.1 port=5432 user=postgres password=secret dbname=mydb sslmode=disable",
		"mysql":            "user:pass@tcp(host:3306)/dbname?charset=utf8mb4",
		"goldendb":         "user:pass@tcp(host:3306)/dbname?charset=utf8mb4",
		"goldendb-mysql":   "user:pass@tcp(host:3306)/dbname?charset=utf8mb4",
		"oceanbase-mysql":  "user:pass@tcp(host:2881)/dbname",
		"panweidb-mysql":   "host=127.0.0.1 port=5432 user=postgres password=secret dbname=mydb sslmode=disable",
		"postgres":         "host=127.0.0.1 port=5432 user=postgres password=secret dbname=mydb sslmode=disable",
		"postgresql":       "host=127.0.0.1 port=5432 user=postgres password=secret dbname=mydb sslmode=disable",
		"opengaussdb":      "host=127.0.0.1 port=5432 user=postgres password=secret dbname=mydb sslmode=disable",
		"panweidb":         "host=127.0.0.1 port=5432 user=postgres password=secret dbname=mydb sslmode=disable",
		"oceanbase":        "user:pass@tcp(host:2881)/dbname (OceanBase MySQL mode; Oracle tenants: use type oceanbase-oracle)",
		"sqlite3":          "/path/to/database.db",
		"duckdb":           "/path/to/database.db",
	}
}

// BuildScenarioConfig builds a Config from submitted form values for a
// scenario. The struct literals mirror the builders in cmd/init.go so the
// web UI produces exactly what `owl-migrate init` would.
func BuildScenarioConfig(name string, v map[string]string) (*config.Config, error) {
	switch name {
	case "migrate":
		return buildMigrateCfg(v), nil
	case "export-ddl", "validate":
		return buildDDLCfg(v), nil
	case "gen-select":
		return buildSelectCfg(v), nil
	case "export":
		return buildExportCfg(v), nil
	case "import":
		return buildImportCfg(v), nil
	case "export-insert":
		return buildInsertCfg(v), nil
	case "export-metadata":
		return buildExportMetadataCfg(v), nil
	default:
		return nil, fmt.Errorf("unknown scenario %q", name)
	}
}

func splitTables(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return []string{"*"}
	}
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// setMetadataSource fills the metadata section based on the metadata_type field.
func setMetadataSource(cfg *config.Config, v map[string]string) {
	switch v["metadata_type"] {
	case "csv":
		cfg.Metadata = config.MetadataConfig{Type: "csv", CSV: config.CSVConfig{
			Path: v["csv_path"], Delimiter: ",", HasHeader: true, ColumnNameMatching: "case_insensitive",
		}}
	case "xlsx":
		cfg.Metadata = config.MetadataConfig{Type: "xlsx", XLSX: config.XLSXConfig{Path: v["xlsx_path"]}}
	case "database":
		cfg.Metadata = config.MetadataConfig{Type: "database"}
		cfg.Source = config.DBConfig{Type: v["source_type"], DSN: v["source_dsn"], Schema: v["source_schema"]}
	}
}

func buildMigrateCfg(v map[string]string) *config.Config {
	srcSchema := v["source_schema"]
	tgtSchema := v["target_schema"]
	if tgtSchema == "" {
		tgtSchema = srcSchema
	}
	// Prefer an explicit schema_mapping; fall back to source→target.
	schemaMapping := parseSchemaMapping(v["schema_mapping"])
	if len(schemaMapping) == 0 && srcSchema != "" {
		schemaMapping = map[string]string{srcSchema: tgtSchema}
	}

	cfg := &config.Config{
		General: config.GeneralConfig{LogLevel: "info"},
		Source:  config.DBConfig{Type: v["source_type"], DSN: v["source_dsn"], Schema: srcSchema},
		Target:  config.DBConfig{Type: v["target_type"], DSN: v["target_dsn"], Schema: tgtSchema},
		DDL: config.DDLConfig{
			TargetDialect:      v["target_type"],
			IncludeIfNotExists: true,
			SchemaMapping:      schemaMapping,
		},
		Export: config.ExportConfig{
			CSV:      config.ExportCSVConfig{Delimiter: ",", Header: true, NullRepresentation: "\\N"},
			Batch:    config.BatchConfig{PageSize: 5000},
			Parallel: config.ParallelConfig{Enabled: true, MaxWorkers: 4},
			Tables:   config.TableListConfig{Include: splitTables(v["tables"])},
		},
		Import: config.ImportConfig{
			CSV:    config.ImportCSVConfig{NullMarker: "\\N"},
			Target: config.ImportTargetConfig{TruncateBefore: true},
			Batch:  config.ImportBatchConfig{CommitInterval: 1000, ErrorPolicy: "skip_row"},
			Parallel: config.ParallelConfig{
				Enabled:    true,
				MaxWorkers: 4,
			},
			DataTransforms: config.DataTransforms{
				DatetimeFormat: "yyyyMMddHHmmss",
				TrimStrings:    true,
				NullIf:         []string{"NULL", "null", "\\N"},
			},
		},
	}
	setMetadataSource(cfg, v)
	return cfg
}

func buildDDLCfg(v map[string]string) *config.Config {
	cfg := &config.Config{
		General: config.GeneralConfig{LogLevel: "info"},
		DDL: config.DDLConfig{
			TargetDialect:      v["target_type"],
			IncludeComments:    true,
			IncludeIfNotExists: true,
		},
	}
	setMetadataSource(cfg, v)
	if m := parseSchemaMapping(v["schema_mapping"]); len(m) > 0 {
		cfg.DDL.SchemaMapping = m
	}
	return cfg
}

// parseSchemaMapping parses "SRC:DST,SRC2:DST2" into a schema mapping.
func parseSchemaMapping(s string) map[string]string {
	m := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, ":", 2)
		if len(kv) == 2 {
			m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return m
}

func formatSchemaMapping(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(m))
	for k, val := range m {
		pairs = append(pairs, k+":"+val)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

// ExtractFormValues reverse-maps a Config into form field values so an uploaded
// config can populate a scenario form. It extracts every known field; the
// frontend applies only those present in the active scenario's form.
func ExtractFormValues(cfg *config.Config) map[string]string {
	v := map[string]string{}
	switch cfg.Metadata.Type {
	case "csv":
		v["metadata_type"] = "csv"
		v["csv_path"] = cfg.Metadata.CSV.Path
	case "xlsx":
		v["metadata_type"] = "xlsx"
		v["xlsx_path"] = cfg.Metadata.XLSX.Path
		v["data_output_dir"] = cfg.Metadata.XLSX.DataOutputDir
	case "database":
		v["metadata_type"] = "database"
	}
	v["source_type"] = cfg.Source.Type
	v["source_dsn"] = cfg.Source.DSN
	v["source_schema"] = cfg.Source.Schema
	v["target_type"] = cfg.Target.Type
	if v["target_type"] == "" {
		v["target_type"] = cfg.DDL.TargetDialect
	}
	v["target_dsn"] = cfg.Target.DSN
	v["target_schema"] = cfg.Target.Schema
	if len(cfg.Export.Tables.Include) > 0 {
		v["tables"] = strings.Join(cfg.Export.Tables.Include, ",")
	} else {
		v["tables"] = "*"
	}
	v["schema_mapping"] = formatSchemaMapping(cfg.DDL.SchemaMapping)
	v["data_dir"] = cfg.Import.SourceDir
	return v
}

// DetectScenario guesses which scenario form best matches a config, so an
// upload can switch the form to the right scenario automatically.
func DetectScenario(cfg *config.Config) string {
	hasSource := cfg.Source.Type != ""
	hasTarget := cfg.Target.Type != ""
	hasExport := cfg.Export.CSV.Delimiter != "" || cfg.Export.Batch.PageSize > 0 || len(cfg.Export.Tables.Include) > 0
	hasImport := cfg.Import.SourceDir != "" || cfg.Import.CSV.NullMarker != "" || cfg.Import.Batch.CommitInterval > 0
	switch {
	case hasSource && hasTarget:
		return "migrate"
	case hasSource && hasExport:
		return "export"
	case hasTarget && hasImport:
		return "import"
	case cfg.Metadata.Type != "" && cfg.DDL.TargetDialect != "":
		return "export-ddl"
	default:
		return "migrate"
	}
}

func buildSelectCfg(v map[string]string) *config.Config {
	cfg := &config.Config{
		General: config.GeneralConfig{LogLevel: "info"},
		SelectGen: config.SelectGenConfig{
			OutputDir: "./output/select/",
			Batch:     config.BatchConfig{Method: "cursor", PageSize: 5000},
		},
	}
	setMetadataSource(cfg, v)
	cfg.DDL = config.DDLConfig{TargetDialect: v["target_type"]}
	return cfg
}

func buildExportCfg(v map[string]string) *config.Config {
	return &config.Config{
		General:  config.GeneralConfig{LogLevel: "info"},
		Metadata: config.MetadataConfig{Type: "database"},
		Source:   config.DBConfig{Type: v["source_type"], DSN: v["source_dsn"], Schema: v["source_schema"]},
		Export: config.ExportConfig{
			OutputDir: "./output/data/",
			Format:    "csv",
			CSV: config.ExportCSVConfig{
				Delimiter:          ",",
				QuoteChar:          "\"",
				Header:             true,
				NullRepresentation: "\\N",
			},
			Batch:    config.BatchConfig{PageSize: 5000},
			Parallel: config.ParallelConfig{Enabled: true, MaxWorkers: 4},
			Tables:   config.TableListConfig{Include: splitTables(v["tables"])},
		},
	}
}

func buildImportCfg(v map[string]string) *config.Config {
	tgtSchema := v["target_schema"]
	return &config.Config{
		General:  config.GeneralConfig{LogLevel: "info"},
		Metadata: config.MetadataConfig{Type: "csv"},
		Target:   config.DBConfig{Type: v["target_type"], DSN: v["target_dsn"], Schema: tgtSchema},
		DDL: config.DDLConfig{
			TargetDialect:      v["target_type"],
			IncludeIfNotExists: true,
			SchemaMapping:      map[string]string{tgtSchema: tgtSchema},
		},
		Import: config.ImportConfig{
			SourceDir: v["data_dir"],
			Format:    "csv",
			CSV:       config.ImportCSVConfig{NullMarker: "\\N"},
			Target:    config.ImportTargetConfig{TruncateBefore: true},
			Batch:     config.ImportBatchConfig{CommitInterval: 1000, ErrorPolicy: "skip_row"},
			Parallel:  config.ParallelConfig{Enabled: true, MaxWorkers: 4},
			DataTransforms: config.DataTransforms{
				DatetimeFormat: "yyyyMMddHHmmss",
				TrimStrings:    true,
				NullIf:         []string{"NULL", "null", "\\N"},
			},
		},
	}
}

func buildInsertCfg(v map[string]string) *config.Config {
	cfg := &config.Config{
		General: config.GeneralConfig{LogLevel: "info"},
		DDL:     config.DDLConfig{TargetDialect: v["target_type"]},
	}
	switch v["metadata_type"] {
	case "xlsx":
		out := v["data_output_dir"]
		if out == "" {
			out = "./output/data/"
		}
		cfg.Metadata = config.MetadataConfig{
			Type: "xlsx",
			XLSX: config.XLSXConfig{Path: v["xlsx_path"], DataOutputDir: out},
		}
	default:
		cfg.Metadata = config.MetadataConfig{Type: "csv"}
	}
	return cfg
}

func buildExportMetadataCfg(v map[string]string) *config.Config {
	return &config.Config{
		General:  config.GeneralConfig{LogLevel: "info"},
		Metadata: config.MetadataConfig{Type: "database"},
		Source:   config.DBConfig{Type: v["source_type"], DSN: v["source_dsn"], Schema: v["source_schema"]},
	}
}
