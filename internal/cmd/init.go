package cmd

import (
	"encoding/json"
	"io"
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/configbuild"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

func initCmd() *cobra.Command {
	var (
		sourceType   string
		sourceDSN    string
		sourceSchema string
		targetType   string
		targetDSN    string
		targetSchema string
		outputFile   string
		metadataType string
		scenario     string
		slotsFile    string
		printOut     bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate a configuration file interactively or via flags",
		Long: `Generates a ready-to-use YAML configuration file.

Run without flags for interactive mode — the tool will ask you questions
about your migration setup and generate the config automatically.

Run with flags for non-interactive mode (CI/automation):
  owl-migrate init --source-type oracle --source-dsn "..." --source-schema SCOTT \
    --target-type postgres -o ./migrate.yaml

Use --scenario to control which sections appear in the generated config:
  migrate         — full end-to-end config (default)
  export-ddl      — DDL generation only (alias: gen-ddl)
  export-insert   — INSERT SQL generation only (alias: gen-insert)
  export          — data export only
  import          — data import only
  export-metadata — metadata export only`,
		RunE: func(cmd *cobra.Command, args []string) error {
			hasTarget := cmd.Flags().Changed("target-type")
			hasScenario := cmd.Flags().Changed("scenario")

			// ── 槽位 JSON 模式（AI 工具/脚本的一等入口） ──
			// 结构化 SlotRequest → configbuild.BuildFromSlots 确定性组装，
			// 输出与交互式 init 完全同构（含注释与高级选项块）。
			if slotsFile != "" {
				return runSlotsInit(slotsFile, outputFile, printOut)
			}

			if !hasTarget && !hasScenario {
				return runInteractive(outputFile)
			}

			mt := strings.ToLower(metadataType)
			sc := strings.ToLower(scenario)

			// ── Non-interactive / semi-interactive mode ──
			// If --scenario is set but not --target-type, enter interactive mode
			// for the chosen scenario directly (skip the action prompt) unless
			// the scenario can default the target and all required source inputs
			// are already provided.
			if hasScenario && !hasTarget {
				if !scenarioTargetDefaultable(sc, mt, sourceType, sourceDSN, sourceSchema) {
					r := bufio.NewReader(os.Stdin)
					return runScenarioInteractive(r, sc, outputFile)
				}
			}

			// ── Fully non-interactive ──
			// Target dialect defaults to the source database type, so export-ddl
			// users can dump a schema in its own dialect without specifying
			// --target-type.
			if targetType == "" {
				targetType = defaultTargetDialect(mt, sourceType)
			}

			if mt == "database" {
				if sourceType == "" {
					return fmt.Errorf("--source-type is required when --metadata-type is 'database'")
				}
				if !config.ValidDialects[strings.ToLower(sourceType)] {
					return fmt.Errorf("unsupported --source-type %q: must be one of %v",
						sourceType, sortedDialectKeys())
				}
			}
			if !config.ValidDialects[strings.ToLower(targetType)] {
				return fmt.Errorf("unsupported --target-type %q: must be one of %v",
					targetType, sortedDialectKeys())
			}
			if !config.ValidMetadataTypes[mt] {
				return fmt.Errorf("unsupported --metadata-type %q: must be one of %v",
					metadataType, sortedMetadataKeys())
			}
			if mt == "database" {
				if sourceDSN == "" {
					return fmt.Errorf("--source-dsn is required when --metadata-type is 'database'")
				}
				if sourceSchema == "" && !isEmbedded(sourceType) {
					return fmt.Errorf("--source-schema is required when --metadata-type is 'database'")
				}
			}
			warnUncompiledDialect(sourceType)
			warnUncompiledDialect(targetType)

			cfg := configbuild.BuildScenarioConfig(sc, sourceType, sourceDSN, sourceSchema, targetType, targetDSN, targetSchema, mt)
			return writeConfig(cfg, outputFile)
		},
	}

	cmd.Flags().StringVar(&slotsFile, "slots", "", "build from a JSON SlotRequest (deterministic assembly; '-' = stdin). Schema: internal/configbuild/slots.go")
	cmd.Flags().BoolVar(&printOut, "print", false, "write the generated config to stdout instead of a file")
	cmd.Flags().StringVarP(&sourceType, "source-type", "s", "", "source database type (only for --metadata-type database)")
	cmd.Flags().StringVar(&sourceDSN, "source-dsn", "", "source database DSN")
	cmd.Flags().StringVar(&sourceSchema, "source-schema", "", "source database schema/database name")
	cmd.Flags().StringVarP(&targetType, "target-type", "t", "", "target database type")
	cmd.Flags().StringVar(&targetDSN, "target-dsn", "", "target database DSN (optional for DDL-only workflows)")
	cmd.Flags().StringVar(&targetSchema, "target-schema", "", "target database schema (defaults to source-schema if empty)")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "./migrate.yaml", "output configuration file path")
	cmd.Flags().StringVarP(&metadataType, "metadata-type", "m", "database", "metadata source: csv, xlsx, or database")
	cmd.Flags().StringVarP(&scenario, "scenario", "S", "migrate", "config scenario: export-ddl (alias gen-ddl), export-insert (alias gen-insert), gen-select, export, import, migrate, export-metadata, validate, full")

	return cmd
}

// ── Interactive mode ──

// runScenarioInteractive enters the interactive flow for a pre-selected scenario,
// skipping the "What do you want to do?" prompt.
func runScenarioInteractive(r *bufio.Reader, scenario, outputPath string) error {
	switch strings.ToLower(scenario) {
	case "export-insert", "gen-insert":
		return interactiveGenInsert(r, outputPath)
	case "export-ddl", "gen-ddl", "validate":
		return interactiveGenDDL(r, outputPath)
	case "gen-select":
		return interactiveGenSelect(r, outputPath)
	case "export":
		return interactiveExport(r, outputPath)
	case "import":
		return interactiveImport(r, outputPath)
	case "migrate":
		return interactiveMigrate(r, outputPath)
	case "export-metadata":
		return interactiveExportMetadata(r, outputPath)
	default:
		return interactiveFull(r, outputPath)
	}
}

func ask(r *bufio.Reader, prompt, def string) string {
	if def != "" {
		fmt.Printf("%s (default: %s): ", prompt, def)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	text, _ := r.ReadString('\n')
	text = strings.TrimSpace(text)
	if text == "" {
		return def
	}
	return text
}

// dsnExample returns an example DSN for the given dialect to show as input hint.
func dsnExample(dialect string) string {
	switch strings.ToLower(dialect) {
	case "oracle", "goldendb-oracle":
		return "oracle://user:pass@host:1521/service_name"
	case "oceanbase-oracle":
		return "oceanbase-oracle://sys@tenant:pass@host:2881/db (直连 OBServer 无需集群) or oceanbase-oracle://sys@tenant#cluster:pass@host:2883/db (OBProxy 多集群必填) or oracle://user:pass@host:2883/service (TNS)"
	case "mysql", "goldendb", "goldendb-mysql":
		return "user:pass@tcp(host:3306)/dbname?charset=utf8mb4"
	case "oceanbase", "oceanbase-mysql":
		return "user@tenant:pass@tcp(host:2881)/dbname (OceanBase MySQL mode; 用户名须带租户, 如 root@test; Oracle tenants: use type oceanbase-oracle)"
	case "postgres", "postgresql", "opengaussdb", "opengaussdb-mysql", "opengaussdb-oracle",
		"panweidb", "panweidb-mysql", "panweidb-oracle":
		return "host=127.0.0.1 port=5432 user=postgres password=secret dbname=mydb sslmode=disable"
	case "sqlite3", "duckdb":
		return "/path/to/database.db"
	default:
		return ""
	}
}

// defaultTargetDialect picks a target dialect when --target-type is omitted:
// the source database type when metadata comes from a live database, otherwise
// postgres (the most common DDL target).
func defaultTargetDialect(metaType, srcType string) string {
	if strings.ToLower(metaType) == "database" && strings.TrimSpace(srcType) != "" {
		return srcType
	}
	return "postgres"
}

// scenarioTargetDefaultable reports whether the given scenario can run without
// --target-type and without interactive prompts: only DDL/validate-style
// scenarios where the target is a defaultable dialect, and only when all
// required source inputs are present.
func scenarioTargetDefaultable(scenario, metaType, srcType, srcDSN, srcSchema string) bool {
	switch scenario {
	case "export-ddl", "gen-ddl", "validate":
	default:
		return false
	}
	if strings.ToLower(metaType) != "database" {
		return true // csv/xlsx metadata: paths have defaults, target defaults to postgres
	}
	if srcType == "" || srcDSN == "" {
		return false
	}
	if srcSchema == "" && !isEmbedded(srcType) {
		return false
	}
	return true
}

// askDSN prompts for a DSN, showing a dialect-specific example as hint.
func askDSN(r *bufio.Reader, prompt, dialect, def string) string {
	if ex := dsnExample(dialect); ex != "" {
		fmt.Printf("  # 格式示例: %s\n", ex)
	}
	return ask(r, prompt, def)
}

// askSchema prompts for a schema name, with a note if the target is embedded.
func askSchema(r *bufio.Reader, prompt, dialect, def string) string {
	if isEmbedded(dialect) {
		return "" // schema ignored for embedded databases
	}
	return ask(r, prompt, def)
}

// isEmbedded returns true for databases that don't use host-based connections.
func isEmbedded(dialect string) bool {
	switch strings.ToLower(dialect) {
	case "sqlite3", "duckdb":
		return true
	default:
		return false
	}
}

// askTables prompts for table names, returning all (*) if empty.
func askTables(r *bufio.Reader, prompt string) []string {
	answer := ask(r, prompt, "*")
	answer = strings.TrimSpace(answer)
	if answer == "" || answer == "*" {
		return []string{"*"}
	}
	var tables []string
	for _, t := range strings.Split(answer, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tables = append(tables, t)
		}
	}
	return tables
}

func askChoice(r *bufio.Reader, prompt string, options []string, def string) string {
	for {
		fmt.Printf("%s\n  Options: %s\n", prompt, strings.Join(options, ", "))
		p := ""
		if def != "" {
			p = p + fmt.Sprintf(" (default: %s)", def)
		}
		fmt.Printf("  Enter%s: ", p)
		text, _ := r.ReadString('\n')
		text = strings.ToLower(strings.TrimSpace(text))
		if text == "" && def != "" {
			return def
		}
		for _, opt := range options {
			if text == opt {
				return opt
			}
		}
		fmt.Printf("  Invalid. Please enter one of: %s\n", strings.Join(options, ", "))
	}
}

func runInteractive(outputPath string) error {
	r := bufio.NewReader(os.Stdin)

	fmt.Println("What do you want to do?")
	fmt.Println("  (export-ddl=DDL from metadata, export-insert=INSERT from CSV, export=export data to CSV/SQL/XLSX)")
	fmt.Println("  (import=import CSV into DB, migrate=end-to-end, gen-select=paginated SELECT, export-metadata=metadata to CSV/xlsx/SQL)")
	fmt.Println("  (validate=check config, full=all options with hints)")
	action := askChoice(r, "", []string{
		"export-ddl", "export-insert", "export", "import", "migrate",
		"export-metadata", "gen-select", "validate", "full",
	}, "")

	switch action {
	case "export-insert":
		return interactiveGenInsert(r, outputPath)
	case "export-ddl", "validate":
		return interactiveGenDDL(r, outputPath)
	case "gen-select":
		return interactiveGenSelect(r, outputPath)
	case "export":
		return interactiveExport(r, outputPath)
	case "import":
		return interactiveImport(r, outputPath)
	case "migrate":
		return interactiveMigrate(r, outputPath)
	case "export-metadata":
		return interactiveExportMetadata(r, outputPath)
	default:
		return interactiveFull(r, outputPath)
	}
}

func interactiveGenInsert(r *bufio.Reader, outputPath string) error {
	mt := askChoice(r, "Data source type", []string{"csv", "xlsx"}, "csv")
	dialect := askDialect(r, "Target database dialect", "postgres")

	cfg := &config.Config{
		General: config.GeneralConfig{LogLevel: "info"},
		DDL: config.DDLConfig{
			TargetDialect: dialect,
		},
	}

	switch mt {
	case "csv":
		// gen-insert (csv mode) reads data dir from CLI -d/--data flag,
		// not from yaml; nothing else needed here.
		cfg.Metadata = config.MetadataConfig{Type: "csv"}
		_ = ask(r, "CSV data files directory (will be passed via -d flag)", "./output/data/")
	case "xlsx":
		xlsxPath := ask(r, "xlsx file path (with @sheet data sheets)", "./metadata/schema.xlsx")
		dataOut := ask(r, "Directory for extracted CSV data files", "./output/data/")
		cfg.Metadata = config.MetadataConfig{
			Type: "xlsx",
			XLSX: config.XLSXConfig{
				Path:          xlsxPath,
				DataOutputDir: dataOut,
			},
		}
	}

	return writeConfig(cfg, outputPath)
}

func interactiveGenDDL(r *bufio.Reader, outputPath string) error {
	mt := askChoice(r, "Metadata source type", []string{"csv", "xlsx", "database"}, "csv")

	var srcType, srcDSN, srcSchema, csvPath, xlsxPath string

	switch mt {
	case "csv":
		csvPath = ask(r, "CSV metadata directory", "./testdata/csv/")
	case "xlsx":
		xlsxPath = ask(r, "xlsx schema file path", "./metadata/schema.xlsx")
	case "database":
		srcType = askDialect(r, "Source database type", "")
		srcDSN = askDSN(r, "Source database DSN", srcType, "")
		srcSchema = askSchema(r, "Source schema name", srcType, "")
	}

	// Target dialect defaults to the source type for live databases so a
	// plain structure dump keeps the source dialect; press Enter to accept.
	tgtDefault := "postgres"
	if mt == "database" && srcType != "" {
		tgtDefault = srcType
	}
	tgtType := askChoice(r, "Target database dialect", sortedDialectKeys(), tgtDefault)

	cfg := configbuild.BuildDDLConfig(mt, srcType, srcDSN, srcSchema, tgtType, csvPath, xlsxPath)
	return writeConfig(cfg, outputPath)
}

func interactiveExport(r *bufio.Reader, outputPath string) error {
	srcType := askDialect(r, "Source database type", "")
	srcDSN := askDSN(r, "Source database DSN", srcType, "")
	srcSchema := askSchema(r, "Source schema name", srcType, "")
	tables := askTables(r, "Tables to migrate (comma-separated, or * for all)")

	cfg := &config.Config{
		General:  config.GeneralConfig{LogLevel: "info"},
		Metadata: config.MetadataConfig{Type: "database"},
		Source:   config.DBConfig{Type: srcType, DSN: srcDSN, Schema: srcSchema},
		Export: config.ExportConfig{
			OutputDir: "./output/data/",
			Format:    "csv",
			CSV: config.ExportCSVConfig{
				Delimiter:          ",",
				QuoteChar:          "\"",
				Header:             true,
				NullRepresentation: "\\N",
			},
			Batch: config.BatchConfig{PageSize: 5000},
			Parallel: config.ParallelConfig{
				Enabled:    true,
				MaxWorkers: 4,
			},
			Tables: config.TableListConfig{
				Include: tables,
			},
		},
	}
	return writeConfig(cfg, outputPath)
}

func interactiveImport(r *bufio.Reader, outputPath string) error {
	dataDir := ask(r, "CSV data files directory", "./output/data/")
	tgtType := askDialect(r, "Target database type", "")
	tgtDSN := askDSN(r, "Target database DSN", tgtType, "")
	tgtSchema := askSchema(r, "Target schema name", tgtType, "")

	cfg := &config.Config{
		General:  config.GeneralConfig{LogLevel: "info"},
		Metadata: config.MetadataConfig{Type: "csv"},
		Target:   config.DBConfig{Type: tgtType, DSN: tgtDSN, Schema: tgtSchema},
		DDL: config.DDLConfig{
			TargetDialect:      tgtType,
			IncludeIfNotExists: true,
			SchemaMapping:      map[string]string{tgtSchema: tgtSchema},
		},
		Import: config.ImportConfig{
			SourceDir: dataDir,
			Format:    "csv",
			CSV:       config.ImportCSVConfig{NullMarker: "\\N"},
			Target:    config.ImportTargetConfig{TruncateBefore: true},
			Batch: config.ImportBatchConfig{
				CommitInterval: 1000,
				ErrorPolicy:    "skip_row",
			},
			// FK-aware order: parents before children, sequential. Slower than
			// parallel but avoids silent skip_row data loss on FK-linked schemas.
			Parallel: config.ParallelConfig{
				Enabled:            true,
				MaxWorkers:         4,
				RespectForeignKeys: true,
			},
			DataTransforms: config.DataTransforms{
				DatetimeFormat: "yyyyMMddHHmmss",
				TrimStrings:    true,
				NullIf:         []string{"NULL", "null", "\\N"},
			},
		},
	}
	return writeConfig(cfg, outputPath)
}

func interactiveMigrate(r *bufio.Reader, outputPath string) error {
	srcType := askDialect(r, "Source database type", "")
	srcDSN := askDSN(r, "Source database DSN", srcType, "")
	srcSchema := askSchema(r, "Source schema name", srcType, "")
	tables := askTables(r, "Tables to migrate (comma-separated, or * for all)")
	tgtType := askDialect(r, "Target database type", "")
	tgtDSN := askDSN(r, "Target database DSN", tgtType, "")
	tgtSchema := askSchema(r, "Target schema name", tgtType, "")
	if tgtSchema == "" {
		tgtSchema = srcSchema
	}

	cfg := configbuild.BuildMigrateConfig(srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema)
	cfg.Export.Tables = config.TableListConfig{Include: tables}
	return writeConfig(cfg, outputPath)
}

func interactiveGenSelect(r *bufio.Reader, outputPath string) error {
	mt := askChoice(r, "Metadata source type", []string{"csv", "xlsx", "database"}, "csv")

	var srcType, srcDSN, srcSchema, csvPath, xlsxPath string

	switch mt {
	case "csv":
		csvPath = ask(r, "CSV metadata directory", "./testdata/csv/")
	case "xlsx":
		xlsxPath = ask(r, "xlsx schema file path", "./metadata/schema.xlsx")
	case "database":
		srcType = askDialect(r, "Source database type", "")
		srcDSN = askDSN(r, "Source database DSN", srcType, "")
		srcSchema = askSchema(r, "Source schema name", srcType, "")
	}

	tgtType := askDialect(r, "Target dialect (controls identifier quoting)", "postgres")

	cfg := &config.Config{
		General: config.GeneralConfig{LogLevel: "info"},
		SelectGen: config.SelectGenConfig{
			OutputDir: "./output/select/",
			Batch: config.BatchConfig{
				Method:   "cursor",
				PageSize: 5000,
			},
		},
	}

	switch mt {
	case "csv":
		cfg.Metadata = config.MetadataConfig{Type: "csv", CSV: config.CSVConfig{Path: csvPath}}
	case "xlsx":
		cfg.Metadata = config.MetadataConfig{
			Type: "xlsx",
			XLSX: config.XLSXConfig{Path: xlsxPath, DataOutputDir: "./output/data/"},
		}
	case "database":
		cfg.Metadata = config.MetadataConfig{Type: "database"}
		cfg.Source = config.DBConfig{Type: srcType, DSN: srcDSN, Schema: srcSchema}
	}

	cfg.DDL = config.DDLConfig{TargetDialect: tgtType}
	return writeConfig(cfg, outputPath)
}

func interactiveExportMetadata(r *bufio.Reader, outputPath string) error {
	srcType := askDialect(r, "Source database type", "")
	srcDSN := askDSN(r, "Source database DSN", srcType, "")
	srcSchema := ask(r, "Source schema name", "")
	fmt.Println()
	fmt.Println("Output format:")
	fmt.Println("  csv   - Separate CSV files per metadata type")
	fmt.Println("  xlsx  - Single Excel workbook")
	fmt.Println("  sql   - INSERT statements for system metadata tables")
	fmt.Print("Format (default: csv): ")
	fmtOut, _ := r.ReadString('\n')
	fmtOut = strings.TrimSpace(strings.ToLower(fmtOut))
	if fmtOut == "" {
		fmtOut = "csv"
	}

	cfg := &config.Config{
		General:  config.GeneralConfig{LogLevel: "info"},
		Metadata: config.MetadataConfig{Type: "database"},
		Source:   config.DBConfig{Type: srcType, DSN: srcDSN, Schema: srcSchema},
	}
	return writeConfig(cfg, outputPath)
}

func interactiveFull(r *bufio.Reader, outputPath string) error {
	hint := func(text string) {
		fmt.Printf("  # %s\n", text)
	}

	fmt.Println()
	fmt.Println("Enter values for each configuration option.")
	fmt.Println("Leave blank to use default where available.")

	mt := askChoice(r, "Metadata source type (csv/xlsx/database)", []string{"csv", "xlsx", "database"}, "csv")

	var srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema, csvPath, xlsxPath string

	if mt == "csv" || mt == "xlsx" {
		fmt.Println()
		hint("Metadata files define table schemas, columns, indexes, etc.")
		if mt == "csv" {
			csvPath = ask(r, "CSV metadata directory", "./testdata/csv/")
			hint("Data files are the CSV files with actual row data for INSERT generation.")
			_ = ask(r, "CSV data files directory", "./output/data/")
		} else {
			xlsxPath = ask(r, "xlsx schema file path", "./metadata/schema.xlsx")
			_ = ask(r, "xlsx @sheet data output directory", "./output/data/")
		}
	}

	if mt == "database" || mt == "csv" || mt == "xlsx" {
		fmt.Println()
		hint("Source: the database you are migrating FROM. Required for live extraction, data export, and migration.")
		if mt == "database" {
			srcType = askDialect(r, "Source database type", "")
			srcDSN = askDSN(r, "Source database DSN", srcType, "")
			srcSchema = askSchema(r, "Source schema name", srcType, "")
			if !isEmbedded(srcType) {
				hint("For Oracle: schema/owner name. For MySQL: database name. For PG: schema name.")
			}
		}
	}

	fmt.Println()
	hint("Target: the database you are migrating TO. Determines DDL dialect and is required for import/migrate.")
	tgtType = askDialect(r, "Target database type (for DDL generation)", "postgres")
	tgtDSN = askDSN(r, "Target database DSN (optional, leave blank for DDL-only)", tgtType, "")
	tgtSchema = askSchema(r, "Target schema name (leave blank to use source schema)", tgtType, "")

	// Build FULL template with ALL 8 sections
	cfg := configbuild.BuildFullConfig(mt, srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema, csvPath, xlsxPath)
	askAdvancedOptions(r, cfg, srcSchema, tgtSchema)
	return writeConfig(cfg, outputPath)
}

// ── Scenario-aware config builders ──

// ── Config writing ──

// fieldComments maps "<section>.<field>" or "<section>" to inline comments.
// "<section>" alone matches the top-level key on its declaration line.
// Nested keys use their YAML path with dots.
var fieldComments = map[string]string{
	// general
	"general":           "# 通用日志设置",
	"general.log_level": "# 日志级别: debug/info/warn/error",

	// metadata
	"metadata":                      "# 【必填】元数据来源——表结构定义从哪里来",
	"metadata.type":                 "# 元数据类型: csv / xlsx / database",
	"metadata.csv":                  "# CSV 元数据加载器配置（type=csv 时生效）",
	"metadata.csv.path":             "# CSV 元数据目录（含 tables.csv/columns.csv 等）",
	"metadata.xlsx":                 "# xlsx 元数据加载器配置（type=xlsx 时生效）",
	"metadata.xlsx.path":            "# xlsx 文件路径（含 tables/columns sheet 和可选 @TableName 数据 sheet）",
	"metadata.xlsx.data_output_dir": "# @sheet 数据被抽取为 CSV 后写入此目录",

	// source / target
	"source":        "# 源数据库连接（type=database 或 export/migrate 时使用）",
	"source.type":   "# 源数据库方言: oracle/postgres/mysql/sqlite3/...",
	"source.dsn":    "# 源数据库 DSN 连接串",
	"source.schema": "# 源 schema/数据库名（Oracle: 用户名; MySQL: db 名; PG: schema 名）",
	"target":        "# 目标数据库连接（import/migrate 时使用）",
	"target.type":   "# 目标数据库方言",
	"target.dsn":    "# 目标数据库 DSN 连接串",
	"target.schema": "# 目标 schema/数据库名",

	// ddl
	"ddl":                       "# DDL 生成器配置",
	"ddl.target_dialect":        "# 【必填】目标方言, 决定 CREATE TABLE / INSERT 语法",
	"ddl.include_comments":      "# 仅 export ddl: 是否生成 COMMENT ON 语句 (PG)",
	"ddl.include_if_not_exists": "# 仅 export ddl/import: CREATE TABLE 是否加 IF NOT EXISTS",
	"ddl.schema_mapping":        "# schema 映射: {源 schema: 目标 schema}",
	"ddl.no_quote_identifiers":  "# true 时标识符不加引号 (SCOTT.EMP 而非 \"SCOTT\".\"EMP\")",

	// select_gen (only generated in "full" scenario)
	"select_gen":                 "# SELECT 分页语句生成（仅 gen-select 命令使用）",
	"select_gen.output_dir":      "# 生成的 SELECT 语句输出目录",
	"select_gen.batch":           "# 分页设置",
	"select_gen.batch.method":    "# 分页方法: cursor(游标)/offset(偏移)",
	"select_gen.batch.page_size": "# 每页行数",

	// export
	"export":                         "# 数据导出配置（仅 export data/migrate 命令使用）",
	"export.output_dir":              "# 仅 export data 独立运行时使用; migrate 用 --temp-dir",
	"export.format":                  "# 输出格式: csv(默认), sql, xlsx",
	"export.csv":                     "# 导出 CSV 格式选项",
	"export.csv.delimiter":           "# CSV 分隔符",
	"export.csv.quote_char":          "# CSV 引号字符",
	"export.csv.header":              "# 是否写入表头行",
	"export.csv.null_representation": "# DB NULL 写入 CSV 时的占位字符串",
	"export.batch":                   "# 批量读取设置",
	"export.batch.page_size":         "# 每批读取行数（游标分页）",
	"export.parallel":                "# 并发设置",
	"export.parallel.enabled":        "# 是否启用多表并发导出",
	"export.parallel.max_workers":    "# 最大并发 worker 数",
	"export.tables":                  "# 表过滤规则",
	"export.tables.include":          "# 包含的表列表; ['*'] 表示全部",
	"export.filters":                 "# WHERE 条件导出: 表模式 → 字面 SQL 片段（见文末高级选项）",
	"export.filters_check":           "# 条件 COUNT 门禁: count(默认,执行前校验条件)/off",
	"export.columns":                 "# 列投影/改名（见文末高级选项）",

	// import
	"import":                                 "# 数据导入配置（仅 import 命令使用）",
	"import.source_dir":                      "# CSV 数据目录（仅 import 命令使用；migrate 走 --temp-dir，gen-insert 走 -d/--data flag）",
	"import.format":                          "# 输入格式: 当前仅支持 csv",
	"import.csv":                             "# CSV 解析选项",
	"import.csv.null_marker":                 "# CSV 中表示 NULL 的占位字符串",
	"import.target":                          "# 目标表写入策略",
	"import.target.truncate_before":          "# 写入前是否 TRUNCATE 目标表",
	"import.batch":                           "# 批次提交策略",
	"import.batch.commit_interval":           "# 每多少行提交一次事务",
	"import.batch.error_policy":              "# 行级错误处理: skip_row/stop/log_only",
	"import.parallel":                        "# 并发设置",
	"import.parallel.enabled":                "# 是否启用多表并发导入",
	"import.parallel.max_workers":            "# 最大并发 worker 数",
	"import.data_transforms":                 "# 数据转换规则",
	"import.data_transforms.datetime_format": "# 紧凑日期串格式（如 yyyyMMddHHmmss）",
	"import.data_transforms.trim_strings":    "# 是否对字符串字段去首尾空白",
	"import.data_transforms.null_if":         "# 这些字符串值会被视为 NULL",
}

// annotateYAML walks each line of marshaled YAML and appends inline comments
// for keys present in fieldComments. Tracks the current top-level section so
// nested keys can be looked up as "<section>.<field>".
func annotateYAML(buf []byte) []byte {
	lines := strings.Split(string(buf), "\n")
	var section string
	var sb strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		indent := len(line) - len(trimmed)
		// Strip trailing comments only when adding our own; keep line otherwise.

		key := ""
		// A YAML key line ends with ":" or "<key>: <value>"
		if idx := strings.Index(trimmed, ":"); idx > 0 {
			key = trimmed[:idx]
		}

		if key != "" {
			// Update top-level section tracker (indent 0)
			if indent == 0 {
				section = key
			}
			// Build lookup path
			path := key
			if indent > 0 && section != "" {
				// For nested keys we use "<section>.<key>"; sub-nested keys still
				// match because we only define one level of nesting in fieldComments.
				// Walk indent: if indent is 4 (one level), use section.key.
				// Deeper nesting (indent 8+) would need full path tracking — skipped
				// for now since our schema rarely needs comments deeper than 2 levels.
				path = section + "." + key
			}
			if comment, ok := fieldComments[path]; ok {
				// Avoid duplicating an existing comment
				if !strings.Contains(line, "#") {
					line = line + "  " + comment
				}
			} else if indent == 0 {
				// Top-level section without a value: try the section comment
				if comment, ok := fieldComments[key]; ok {
					if !strings.Contains(line, "#") {
						line = line + "  " + comment
					}
				}
			}
		}

		sb.WriteString(line)
		sb.WriteString("\n")
	}
	// Strip the extra trailing newline added by the loop
	out := sb.String()
	if strings.HasSuffix(out, "\n\n") {
		out = out[:len(out)-1]
	}
	return []byte(out)
}

// askAdvancedOptions offers the optional power features in interactive mode:
// WHERE 条件导出、列投影/改名。回车跳过（默认全量全列）。输入即时校验，
// 非法片段要求重输——与执行前的条件 COUNT 门禁同一套规则。
func askAdvancedOptions(r *bufio.Reader, cfg *config.Config, srcSchema, tgtSchema string) {
	fmt.Println()
	if !askYesNo(r, "配置高级选项（WHERE 条件导出 / 列投影与改名）?", false) {
		return
	}
	fmt.Println("  提示: 表模式支持精确名与 glob（SCOTT.EMP / SCOTT.* / *.T / T_*），片段为字面 SQL WHERE（禁 ; 注释与绑定占位符）。")
	for {
		pat := strings.TrimSpace(ask(r, "条件导出——表模式（回车结束）", ""))
		if pat == "" {
			break
		}
		frag := strings.TrimSpace(ask(r, "  WHERE 片段", ""))
		if err := exporter.ValidateFilterFragment(frag); err != nil {
			fmt.Printf("  ✗ %v，请重输\n", err)
			continue
		}
		if cfg.Export.Filters == nil {
			cfg.Export.Filters = map[string]string{}
		}
		cfg.Export.Filters[pat] = frag
	}
	pat := strings.TrimSpace(ask(r, "列投影——表模式（回车跳过整节）", ""))
	if pat != "" {
		cols := strings.Split(ask(r, "  输出列（逗号分隔，顺序=输出顺序）", ""), ",")
		var include []string
		for _, c := range cols {
			if c = strings.TrimSpace(c); c != "" {
				include = append(include, c)
			}
		}
		if len(include) > 0 {
			if cfg.Export.Columns.Include == nil {
				cfg.Export.Columns.Include = map[string][]string{}
			}
			cfg.Export.Columns.Include[pat] = include
			ren := strings.TrimSpace(ask(r, "  改名（源列:新名，逗号分隔，可空）", ""))
			if ren != "" {
				m := map[string]string{}
				for _, pair := range strings.Split(ren, ",") {
					if k, v, ok := strings.Cut(strings.TrimSpace(pair), ":"); ok {
						m[strings.TrimSpace(k)] = strings.TrimSpace(v)
					}
				}
				if len(m) > 0 {
					if cfg.Export.Columns.Rename == nil {
						cfg.Export.Columns.Rename = map[string]map[string]string{}
					}
					cfg.Export.Columns.Rename[pat] = m
				}
			}
		}
	}
}

func askYesNo(r *bufio.Reader, prompt string, def bool) bool {
	suffix := " (y/N) "
	if def {
		suffix = " (Y/n) "
	}
	fmt.Print(prompt + suffix)
	line, _ := r.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return def
	}
	return line == "y" || line == "yes"
}

// yamlAnnotated renders a config to annotated YAML bytes (comments via
// fieldComments + the advanced-options trailer).
func yamlAnnotated(cfg *config.Config) ([]byte, error) {
	buf, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	annotated := annotateYAML(buf)
	annotated = append(annotated, []byte(advancedOptionsTrailer)...)
	return annotated, nil
}

// advancedOptionsTrailer documents opt-in features that have no default
// value in the generated YAML (enabling them changes migration semantics, so
// they stay commented out). Fragments carry the correct indent for the target
// section and no top-level keys — pasting them under the matching section
// keeps the file valid.
const advancedOptionsTrailer = `
# ── 高级选项（按需复制到上方对应段内，注意保持缩进）────────────────
#
# export:                        # ← 加到 export: 段内（缩进 2 格）
#   filters:                     #   WHERE 条件导出；执行前对每张命中表跑
#     "SCOTT.EMP": "deptno = 20" #   条件 COUNT 校验（列名/语法错会中止并指名 filter）
#   filters_check: off           #   跳过门禁（默认 count）
#   columns:                     #   列投影/改名：include 列表顺序 = 输出顺序
#     include: {"SCOTT.EMP": ["empno", "sal", "ename"]}
#     rename:  {"SCOTT.EMP": {SAL: salary}}
#
# ddl:                           # ← 加到 ddl: 段内
#   column_types: {"SCOTT.EMP.SAL": "number(10,2)"}   # 按列类型覆盖（自动建表/export ddl）
#
# import:                        # ← 加到 import.data_transforms 段内
#   data_transforms:
#     column_datetime_formats: {"SCOTT.EMP.HIREDATE": "yyyyMMdd"}
#
# 详见 docs/filtered-export.md
`

// runSlotsInit --slots JSON 模式：确定性组装 + 与交互式 init 相同的注释化输出。
// '-' 从 stdin 读（工具管道友好）；--print 时写到 stdout。
func runSlotsInit(slotsFile, outputFile string, printOut bool) error {
	var data []byte
	var err error
	if slotsFile == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(slotsFile)
	}
	if err != nil {
		return fmt.Errorf("read slots: %w", err)
	}
	var req configbuild.SlotRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("parse slots JSON: %w", err)
	}
	cfg, err := configbuild.BuildFromSlots(req)
	if err != nil {
		return err
	}
	if printOut {
		buf, err := yamlAnnotated(cfg)
		if err != nil {
			return err
		}
		os.Stdout.Write(buf)
		return nil
	}
	return writeConfig(cfg, outputFile)
}

func writeConfig(cfg *config.Config, outputPath string) error {
	buf, err := yamlAnnotated(cfg)
	if err != nil {
		return err
	}

	header := "# Auto-generated by owl-migrate init\n" +
		"# Edit this file to fine-tune migration settings, then run:\n" +
		"#   owl-migrate validate -c " + outputPath + "\n"

	// Suggest appropriate commands based on config content
	if cfg.SelectGen.OutputDir != "" && cfg.Export.OutputDir == "" && cfg.Target.DSN == "" {
		header += "#   owl-migrate gen-select -c " + outputPath + "\n"
	} else if cfg.DDL.TargetDialect != "" && cfg.Target.DSN == "" && cfg.Metadata.Type != "" {
		header += "#   owl-migrate export ddl  -c " + outputPath + "\n"
	} else {
		header += "#   owl-migrate export ddl  -c " + outputPath + "\n" +
			"#   owl-migrate migrate  -c " + outputPath + "\n"
	}
	header += "\n"

	content := append([]byte(nil), buf...)
	if err := os.WriteFile(outputPath, content, 0644); err != nil {
		return fmt.Errorf("write config to %q: %w", outputPath, err)
	}

	fmt.Printf("\nConfiguration written to %s\n", outputPath)
	return nil
}

func sortedDialectKeys() []string {
	keys := make([]string, 0, len(config.ValidDialects))
	for k := range config.ValidDialects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// warnUncompiledDialect prints a warning when the chosen dialect needs a build
// tag this binary was not compiled with — the generated config is still valid,
// but running it here would fail until the binary is rebuilt.
func warnUncompiledDialect(name string) {
	if tag := registry.MissingBuildTag(name); tag != "" {
		fmt.Printf("  ⚠️  dialect %q is not compiled into this binary (build tag: %s); running it here will fail unless rebuilt\n", name, tag)
	}
}

// askDialect asks for a database dialect from the full (sorted) dialect list.
func askDialect(r *bufio.Reader, prompt, def string) string {
	choice := askChoice(r, prompt, sortedDialectKeys(), def)
	warnUncompiledDialect(choice)
	return choice
}

func sortedMetadataKeys() []string {
	keys := make([]string, 0, len(config.ValidMetadataTypes))
	for k := range config.ValidMetadataTypes {
		keys = append(keys, k)
	}
	return keys
}
