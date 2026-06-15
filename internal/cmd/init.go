package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/config"
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
  gen-ddl         — DDL generation only
  gen-insert      — INSERT SQL generation only
  export          — data export only
  import          — data import only
  export-metadata — metadata export only`,
		RunE: func(cmd *cobra.Command, args []string) error {
			hasFlags := cmd.Flags().Changed("target-type")
			if !hasFlags {
				return runInteractive(outputFile)
			}

			// ── Non-interactive mode ──
			mt := strings.ToLower(metadataType)

			if mt == "database" {
				if sourceType == "" {
					return fmt.Errorf("--source-type is required when --metadata-type is 'database'")
				}
				if !config.ValidDialects[strings.ToLower(sourceType)] {
					return fmt.Errorf("unsupported --source-type %q: must be one of %v",
						sourceType, sortedDialectKeys())
				}
			}
			if targetType == "" {
				return fmt.Errorf("--target-type is required")
			}
			if !config.ValidDialects[strings.ToLower(targetType)] {
				return fmt.Errorf("unsupported --target-type %q: must be one of %v",
					targetType, sortedDialectKeys())
			}
			mt = strings.ToLower(metadataType)
			if !config.ValidMetadataTypes[mt] {
				return fmt.Errorf("unsupported --metadata-type %q: must be one of %v",
					metadataType, sortedMetadataKeys())
			}
			if mt == "database" {
				if sourceDSN == "" {
					return fmt.Errorf("--source-dsn is required when --metadata-type is 'database'")
				}
				if sourceSchema == "" {
					return fmt.Errorf("--source-schema is required when --metadata-type is 'database'")
				}
			}

			sc := strings.ToLower(scenario)
			cfg := buildScenarioConfig(sc, sourceType, sourceDSN, sourceSchema, targetType, targetDSN, targetSchema, mt)
			return writeConfig(cfg, outputFile)
		},
	}

	cmd.Flags().StringVarP(&sourceType, "source-type", "s", "", "source database type (only for --metadata-type database)")
	cmd.Flags().StringVar(&sourceDSN, "source-dsn", "", "source database DSN")
	cmd.Flags().StringVar(&sourceSchema, "source-schema", "", "source database schema/database name")
	cmd.Flags().StringVarP(&targetType, "target-type", "t", "", "target database type")
	cmd.Flags().StringVar(&targetDSN, "target-dsn", "", "target database DSN (optional for DDL-only workflows)")
	cmd.Flags().StringVar(&targetSchema, "target-schema", "", "target database schema (defaults to source-schema if empty)")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "./migrate.yaml", "output configuration file path")
	cmd.Flags().StringVarP(&metadataType, "metadata-type", "m", "database", "metadata source: csv, xlsx, or database")
	cmd.Flags().StringVarP(&scenario, "scenario", "S", "migrate", "config scenario: migrate, gen-ddl, gen-insert, export, import, export-metadata")

	return cmd
}

// ── Interactive mode ──

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
	case "oracle", "goldendb-oracle", "oceanbase-oracle", "panweidb-oracle":
		return "oracle://user:pass@host:1521/service_name"
	case "mysql", "goldendb", "goldendb-mysql", "oceanbase-mysql", "panweidb-mysql":
		return "user:pass@tcp(host:3306)/dbname?charset=utf8mb4"
	case "postgres", "postgresql", "opengaussdb", "panweidb":
		return "host=127.0.0.1 port=5432 user=postgres password=secret dbname=mydb sslmode=disable"
	case "oceanbase":
		return "user:pass@tcp(host:2881)/dbname  (or oracle:// for Oracle mode)"
	default:
		return ""
	}
}

// askDSN prompts for a DSN, showing a dialect-specific example as hint.
func askDSN(r *bufio.Reader, prompt, dialect, def string) string {
	if ex := dsnExample(dialect); ex != "" {
		fmt.Printf("  # 格式示例: %s\n", ex)
	}
	return ask(r, prompt, def)
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

	action := askChoice(r, "What do you want to do?", []string{
		"gen-ddl", "gen-insert", "export", "import", "migrate",
		"export-metadata", "validate", "full",
	}, "")
	fmt.Println("  (gen-ddl=DDL from metadata, gen-insert=INSERT from CSV, export=export data to CSV)")
	fmt.Println("  (import=import CSV into DB, migrate=end-to-end, export-metadata=metadata to CSV/xlsx/SQL)")
	fmt.Println("  (validate=check config, full=all options with hints)")

	switch action {
	case "gen-insert":
		return interactiveGenInsert(r, outputPath)
	case "gen-ddl", "validate":
		return interactiveGenDDL(r, outputPath)
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
	dialect := askChoice(r, "Target database dialect", sortedDialectKeys(), "postgres")

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
		srcType = askChoice(r, "Source database type", sortedDialectKeys(), "")
		srcDSN = askDSN(r, "Source database DSN", srcType, "")
		srcSchema = ask(r, "Source schema name", "")
	}

	tgtType := askChoice(r, "Target database dialect", sortedDialectKeys(), "postgres")

	cfg := buildDDLConfig(mt, srcType, srcDSN, srcSchema, tgtType, csvPath, xlsxPath)
	return writeConfig(cfg, outputPath)
}

func interactiveExport(r *bufio.Reader, outputPath string) error {
	srcType := askChoice(r, "Source database type", sortedDialectKeys(), "")
	srcDSN := askDSN(r, "Source database DSN", srcType, "")
	srcSchema := ask(r, "Source schema name", "")

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
				Include: []string{"*"},
			},
		},
	}
	return writeConfig(cfg, outputPath)
}

func interactiveImport(r *bufio.Reader, outputPath string) error {
	dataDir := ask(r, "CSV data files directory", "./output/data/")
	tgtType := askChoice(r, "Target database type", sortedDialectKeys(), "")
	tgtDSN := askDSN(r, "Target database DSN", tgtType, "")
	tgtSchema := ask(r, "Target schema name", "")

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
	return writeConfig(cfg, outputPath)
}

func interactiveMigrate(r *bufio.Reader, outputPath string) error {
	srcType := askChoice(r, "Source database type", sortedDialectKeys(), "")
	srcDSN := askDSN(r, "Source database DSN", srcType, "")
	srcSchema := ask(r, "Source schema name", "")
	tgtType := askChoice(r, "Target database type", sortedDialectKeys(), "")
	tgtDSN := askDSN(r, "Target database DSN", tgtType, "")
	tgtSchema := ask(r, "Target schema name (default: source schema)", "")
	if tgtSchema == "" {
		tgtSchema = srcSchema
	}

	cfg := buildMigrateConfig(srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema)
	return writeConfig(cfg, outputPath)
}

func interactiveExportMetadata(r *bufio.Reader, outputPath string) error {
	srcType := askChoice(r, "Source database type", sortedDialectKeys(), "")
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
			srcType = askChoice(r, "Source database type", sortedDialectKeys(), "")
			srcDSN = askDSN(r, "Source database DSN", srcType, "")
			srcSchema = ask(r, "Source schema name", "")
			hint("For Oracle: schema/owner name. For MySQL: database name. For PG: schema name.")
		}
	}

	fmt.Println()
	hint("Target: the database you are migrating TO. Determines DDL dialect and is required for import/migrate.")
	tgtType = askChoice(r, "Target database type (for DDL generation)", sortedDialectKeys(), "postgres")
	tgtDSN = askDSN(r, "Target database DSN (optional, leave blank for DDL-only)", tgtType, "")
	tgtSchema = ask(r, "Target schema name (leave blank to use source schema)", "")

	// Build FULL template with ALL 8 sections
	cfg := buildFullConfig(mt, srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema, csvPath, xlsxPath)
	return writeConfig(cfg, outputPath)
}

// ── Scenario-aware config builders ──

func buildScenarioConfig(scenario, srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema, metaType string) *config.Config {
	switch scenario {
	case "gen-ddl", "validate":
		csvPath := ""
		xlsxPath := ""
		if metaType == "csv" {
			csvPath = "./testdata/csv/"
		}
		if metaType == "xlsx" {
			xlsxPath = "./metadata/schema.xlsx"
		}
		return buildDDLConfig(metaType, srcType, srcDSN, srcSchema, tgtType, csvPath, xlsxPath)
	case "gen-insert":
		cfg := &config.Config{
			General: config.GeneralConfig{LogLevel: "info"},
			DDL:     config.DDLConfig{TargetDialect: tgtType},
		}
		switch metaType {
		case "xlsx":
			cfg.Metadata = config.MetadataConfig{
				Type: "xlsx",
				XLSX: config.XLSXConfig{
					Path:          "./metadata/schema.xlsx",
					DataOutputDir: "./output/data/",
				},
			}
		default:
			// csv: gen-insert reads data dir from CLI -d/--data flag.
			cfg.Metadata = config.MetadataConfig{Type: "csv"}
		}
		return cfg
	case "export":
		return &config.Config{
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
				Tables: config.TableListConfig{Include: []string{"*"}},
			},
		}
	case "import":
		return &config.Config{
			General:  config.GeneralConfig{LogLevel: "info"},
			Metadata: config.MetadataConfig{Type: "csv"},
			Target:   config.DBConfig{Type: tgtType, DSN: tgtDSN, Schema: tgtSchema},
			DDL: config.DDLConfig{
				TargetDialect:      tgtType,
				IncludeIfNotExists: true,
				SchemaMapping:      map[string]string{tgtSchema: tgtSchema},
			},
			Import: config.ImportConfig{
				SourceDir: "./output/data/",
				Format:    "csv",
				CSV:       config.ImportCSVConfig{NullMarker: "\\N"},
				Target:    config.ImportTargetConfig{TruncateBefore: true},
				Batch: config.ImportBatchConfig{
					CommitInterval: 1000,
					ErrorPolicy:    "skip_row",
				},
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
	case "export-metadata":
		return &config.Config{
			General:  config.GeneralConfig{LogLevel: "info"},
			Metadata: config.MetadataConfig{Type: "database"},
			Source:   config.DBConfig{Type: srcType, DSN: srcDSN, Schema: srcSchema},
		}
	default:
		return buildMigrateConfig(srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema)
	}
}

func buildDDLConfig(metaType, srcType, srcDSN, srcSchema, tgtType, csvPath, xlsxPath string) *config.Config {
	cfg := &config.Config{
		General: config.GeneralConfig{LogLevel: "info"},
		DDL: config.DDLConfig{
			TargetDialect:      tgtType,
			IncludeComments:    true,
			IncludeIfNotExists: true,
		},
	}

	switch metaType {
	case "csv":
		cfg.Metadata = config.MetadataConfig{Type: "csv", CSV: config.CSVConfig{Path: csvPath}}
	case "xlsx":
		cfg.Metadata = config.MetadataConfig{Type: "xlsx", XLSX: config.XLSXConfig{Path: xlsxPath}}
	case "database":
		cfg.Metadata = config.MetadataConfig{Type: "database"}
		cfg.Source = config.DBConfig{Type: srcType, DSN: srcDSN, Schema: srcSchema}
	}

	if srcSchema != "" {
		cfg.DDL.SchemaMapping = map[string]string{srcSchema: srcSchema}
	}

	return cfg
}

func buildMigrateConfig(srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema string) *config.Config {
	if tgtSchema == "" {
		tgtSchema = srcSchema
	}

	schemaMapping := make(map[string]string)
	if srcSchema != "" {
		schemaMapping[srcSchema] = tgtSchema
	}

	return &config.Config{
		General:  config.GeneralConfig{LogLevel: "info"},
		Metadata: config.MetadataConfig{Type: "database"},
		Source:   config.DBConfig{Type: srcType, DSN: srcDSN, Schema: srcSchema},
		Target:   config.DBConfig{Type: tgtType, DSN: tgtDSN, Schema: tgtSchema},
		DDL: config.DDLConfig{
			TargetDialect:      tgtType,
			IncludeIfNotExists: true,
			SchemaMapping:      schemaMapping,
		},
		Export: config.ExportConfig{
			CSV: config.ExportCSVConfig{
				Delimiter:          ",",
				Header:             true,
				NullRepresentation: "\\N",
			},
			Batch: config.BatchConfig{PageSize: 5000},
			Parallel: config.ParallelConfig{
				Enabled:    true,
				MaxWorkers: 4,
			},
		},
		Import: config.ImportConfig{
			CSV:    config.ImportCSVConfig{NullMarker: "\\N"},
			Target: config.ImportTargetConfig{TruncateBefore: true},
			Batch: config.ImportBatchConfig{
				CommitInterval: 1000,
				ErrorPolicy:    "skip_row",
			},
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
}

// buildFullConfig builds a complete config with ALL sections for the "full" scenario.
// Unlike scenario-specific builders, full mode always includes all 8 sections
// (general, metadata, source/target, ddl, select_gen, export, import) with comments
// explaining which commands actually use each section.
func buildFullConfig(metaType, srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema, csvPath, xlsxPath string) *config.Config {
	schemaMapping := make(map[string]string)
	if srcSchema != "" {
		schemaMapping[srcSchema] = tgtSchema
	}

	cfg := &config.Config{
		ForceAllSections: true,
		General:          config.GeneralConfig{LogLevel: "info"},
		Metadata:         config.MetadataConfig{Type: metaType},
		// Source always appears in full template; comment explains it's database-only.
		Source: config.DBConfig{
			Type:   srcType,
			DSN:    srcDSN,
			Schema: srcSchema,
		},
		Target: config.DBConfig{
			Type:   tgtType,
			DSN:    tgtDSN,
			Schema: tgtSchema,
		},
		DDL: config.DDLConfig{
			TargetDialect:      tgtType,
			IncludeComments:    true,
			IncludeIfNotExists: true,
			SchemaMapping:      schemaMapping,
		},
		// select_gen always appears; comment explains gen-select only.
		SelectGen: config.SelectGenConfig{
			OutputDir: "./output/select/",
			Batch: config.BatchConfig{
				Method:   "cursor",
				PageSize: 5000,
			},
		},
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
			Tables:   config.TableListConfig{Include: []string{"*"}},
		},
		Import: config.ImportConfig{
			SourceDir: "./output/data/",
			Format:    "csv",
			CSV:       config.ImportCSVConfig{NullMarker: "\\N"},
			Target:    config.ImportTargetConfig{TruncateBefore: true},
			Batch: config.ImportBatchConfig{
				CommitInterval: 1000,
				ErrorPolicy:    "skip_row",
			},
			Parallel: config.ParallelConfig{Enabled: true, MaxWorkers: 4},
			DataTransforms: config.DataTransforms{
				DatetimeFormat: "yyyyMMddHHmmss",
				TrimStrings:    true,
				NullIf:         []string{"NULL", "null", "\\N"},
			},
		},
	}

	// Populate the active metadata source
	switch metaType {
	case "csv":
		if csvPath == "" {
			csvPath = "./testdata/csv/"
		}
		cfg.Metadata.CSV.Path = csvPath
	case "xlsx":
		if xlsxPath == "" {
			xlsxPath = "./metadata/schema.xlsx"
		}
		cfg.Metadata.XLSX.Path = xlsxPath
		cfg.Metadata.XLSX.DataOutputDir = "./output/data/"
	}

	return cfg
}

// ── Config writing ──

// fieldComments maps "<section>.<field>" or "<section>" to inline comments.
// "<section>" alone matches the top-level key on its declaration line.
// Nested keys use their YAML path with dots.
var fieldComments = map[string]string{
	// general
	"general":           "# 通用日志设置",
	"general.log_level": "# 日志级别: debug/info/warn/error",

	// metadata
	"metadata":                     "# 【必填】元数据来源——表结构定义从哪里来",
	"metadata.type":                "# 元数据类型: csv / xlsx / database",
	"metadata.csv":                 "# CSV 元数据加载器配置（type=csv 时生效）",
	"metadata.csv.path":            "# CSV 元数据目录（含 tables.csv/columns.csv 等）",
	"metadata.xlsx":                "# xlsx 元数据加载器配置（type=xlsx 时生效）",
	"metadata.xlsx.path":           "# xlsx 文件路径（含 tables/columns sheet 和可选 @TableName 数据 sheet）",
	"metadata.xlsx.data_output_dir": "# @sheet 数据被抽取为 CSV 后写入此目录",

	// source / target
	"source":        "# 源数据库连接（type=database 或 export/migrate 时使用）",
	"source.type":   "# 源数据库方言: oracle/postgres/mysql/...",
	"source.dsn":    "# 源数据库 DSN 连接串",
	"source.schema": "# 源 schema/数据库名（Oracle: 用户名; MySQL: db 名; PG: schema 名）",
	"target":        "# 目标数据库连接（import/migrate 时使用）",
	"target.type":   "# 目标数据库方言",
	"target.dsn":    "# 目标数据库 DSN 连接串",
	"target.schema": "# 目标 schema/数据库名",

	// ddl
	"ddl":                       "# DDL 生成器配置",
	"ddl.target_dialect":        "# 【必填】目标方言, 决定 CREATE TABLE / INSERT 语法",
	"ddl.include_comments":      "# 仅 gen-ddl: 是否生成 COMMENT ON 语句 (PG)",
	"ddl.include_if_not_exists": "# 仅 gen-ddl/import: CREATE TABLE 是否加 IF NOT EXISTS",
	"ddl.schema_mapping":        "# schema 映射: {源 schema: 目标 schema}",
	"ddl.no_quote_identifiers":  "# true 时标识符不加引号 (SCOTT.EMP 而非 \"SCOTT\".\"EMP\")",

	// select_gen (only generated in "full" scenario)
	"select_gen":               "# SELECT 分页语句生成（仅 gen-select 命令使用）",
	"select_gen.output_dir":    "# 生成的 SELECT 语句输出目录",
	"select_gen.batch":         "# 分页设置",
	"select_gen.batch.method":  "# 分页方法: cursor(游标)/offset(偏移)",
	"select_gen.batch.page_size": "# 每页行数",

	// export
	"export":                          "# 数据导出配置（仅 export/migrate 命令使用）",
	"export.output_dir":               "# 仅 export 独立运行时使用; migrate 用 --temp-dir",
	"export.format":                   "# 输出格式: 当前仅支持 csv",
	"export.csv":                      "# 导出 CSV 格式选项",
	"export.csv.delimiter":            "# CSV 分隔符",
	"export.csv.quote_char":           "# CSV 引号字符",
	"export.csv.header":               "# 是否写入表头行",
	"export.csv.null_representation":  "# DB NULL 写入 CSV 时的占位字符串",
	"export.batch":                    "# 批量读取设置",
	"export.batch.page_size":          "# 每批读取行数（游标分页）",
	"export.parallel":                 "# 并发设置",
	"export.parallel.enabled":         "# 是否启用多表并发导出",
	"export.parallel.max_workers":     "# 最大并发 worker 数",
	"export.tables":                   "# 表过滤规则",
	"export.tables.include":           "# 包含的表列表; ['*'] 表示全部",

	// import
	"import":                                    "# 数据导入配置（仅 import 命令使用）",
	"import.source_dir":                         "# CSV 数据目录（仅 import 命令使用；migrate 走 --temp-dir，gen-insert 走 -d/--data flag）",
	"import.format":                             "# 输入格式: 当前仅支持 csv",
	"import.csv":                                "# CSV 解析选项",
	"import.csv.null_marker":                    "# CSV 中表示 NULL 的占位字符串",
	"import.target":                             "# 目标表写入策略",
	"import.target.truncate_before":             "# 写入前是否 TRUNCATE 目标表",
	"import.batch":                              "# 批次提交策略",
	"import.batch.commit_interval":              "# 每多少行提交一次事务",
	"import.batch.error_policy":                 "# 行级错误处理: skip_row/stop/log_only",
	"import.parallel":                           "# 并发设置",
	"import.parallel.enabled":                   "# 是否启用多表并发导入",
	"import.parallel.max_workers":               "# 最大并发 worker 数",
	"import.data_transforms":                    "# 数据转换规则",
	"import.data_transforms.datetime_format":    "# 紧凑日期串格式（如 yyyyMMddHHmmss）",
	"import.data_transforms.trim_strings":       "# 是否对字符串字段去首尾空白",
	"import.data_transforms.null_if":            "# 这些字符串值会被视为 NULL",
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

func writeConfig(cfg *config.Config, outputPath string) error {
	buf, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	annotated := annotateYAML(buf)

	header := "# Auto-generated by owl-migrate init\n" +
		"# Edit this file to fine-tune migration settings, then run:\n" +
		"#   owl-migrate validate -c " + outputPath + "\n" +
		"#   owl-migrate gen-ddl  -c " + outputPath + "\n" +
		"#   owl-migrate migrate  -c " + outputPath + "\n\n"

	content := append([]byte(header), annotated...)

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
	return keys
}

func sortedMetadataKeys() []string {
	keys := make([]string, 0, len(config.ValidMetadataTypes))
	for k := range config.ValidMetadataTypes {
		keys = append(keys, k)
	}
	return keys
}
