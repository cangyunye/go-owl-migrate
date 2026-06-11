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
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate a configuration file interactively or via flags",
		Long: `Generates a ready-to-use YAML configuration file.

	Run without flags for interactive mode — the tool will ask you questions
	about your migration setup and generate the config automatically.

	Run with flags for non-interactive mode (CI/automation):
	  owl-migrate init --source-type oracle --source-dsn "..." --source-schema SCOTT \
	    --target-type postgres -o ./migrate.yaml`,
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
			cfg := buildConfig(sourceType, sourceDSN, sourceSchema, targetType, targetDSN, targetSchema, mt)
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

func askChoice(r *bufio.Reader, prompt string, options []string, def string) string {
	for {
		p := prompt
		if def != "" {
			p = p + fmt.Sprintf(" (default: %s)", def)
		}
		fmt.Printf("%s: ", p)
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
		fmt.Printf("Please enter one of: %s\n", strings.Join(options, ", "))
	}
}

func runInteractive(outputPath string) error {
	r := bufio.NewReader(os.Stdin)

	// Step 1: What do you want to do?
	fmt.Println("What do you want to do?")
	fmt.Println("  gen-ddl          - Generate DDL from metadata (offline)")
	fmt.Println("  gen-insert       - Generate INSERT SQL from CSV data (zero-config)")
	fmt.Println("  export           - Export data from source database to CSV")
	fmt.Println("  import           - Import CSV data into target database")
	fmt.Println("  migrate          - End-to-end: export → create tables → import")
	fmt.Println("  export-metadata  - Export metadata from live DB to CSV/xlsx/SQL")
	fmt.Println("  validate         - Validate metadata configuration")
	fmt.Println("  full             - Full configuration (all options with hints)")
	action := askChoice(r, "", []string{"gen-ddl", "gen-insert", "export", "import", "migrate",
		"export-metadata", "validate", "full"}, "")

	switch action {
	case "gen-insert":
		return interactiveGenInsert(r, outputPath)
	case "gen-ddl", "validate":
		return interactiveGenDDL(r, outputPath, action)
	case "export":
		return interactiveExport(r, outputPath)
	case "import":
		return interactiveImport(r, outputPath)
	case "migrate":
		return interactiveMigrate(r, outputPath)
	case "export-metadata":
		return interactiveExportMetadata(r, outputPath)
	default: // full
		return interactiveFull(r, outputPath)
	}
}

// ── gen-insert: only data dir + dialect ──

func interactiveGenInsert(r *bufio.Reader, outputPath string) error {
	dataDir := ask(r, "CSV data files directory", "./output/data/")
	dialect := askChoice(r, "Target database dialect", sortedDialectKeys(), "postgres")

	cfg := buildConfig("", "", "", dialect, "", "", "csv")
	cfg.Metadata.CSV.Path = "" // CSV metadata not needed for gen-insert
	cfg.Import.SourceDir = dataDir
	cfg.DDL.TargetDialect = dialect

	return writeConfig(cfg, outputPath)
}

// ── gen-ddl / validate: metadata source + target dialect ──

func interactiveGenDDL(r *bufio.Reader, outputPath, action string) error {
	mt := askChoice(r, "Metadata source type", []string{"csv", "xlsx", "database"}, "csv")

	var srcType, srcDSN, srcSchema, csvPath, xlsxPath string

	switch mt {
	case "csv":
		csvPath = ask(r, "CSV metadata directory", "./testdata/csv/")
	case "xlsx":
		xlsxPath = ask(r, "xlsx schema file path", "./metadata/schema.xlsx")
	case "database":
		srcType = askChoice(r, "Source database type", sortedDialectKeys(), "")
		srcDSN = ask(r, "Source database DSN", "")
		srcSchema = ask(r, "Source schema name", "")
	}

	tgtType := askChoice(r, "Target database dialect", sortedDialectKeys(), "postgres")

	// Build partial config
	cfg := buildConfig(srcType, srcDSN, srcSchema, tgtType, "", srcSchema, mt)
	if mt == "csv" && csvPath != "" {
		cfg.Metadata.CSV.Path = csvPath
	}
	if mt == "xlsx" && xlsxPath != "" {
		cfg.Metadata.XLSX.Path = xlsxPath
	}
	// Clear data-related sections
	cfg.Export = config.ExportConfig{}
	cfg.Import = config.ImportConfig{}

	return writeConfig(cfg, outputPath)
}

// ── export: source DB only ──

func interactiveExport(r *bufio.Reader, outputPath string) error {
	srcType := askChoice(r, "Source database type", sortedDialectKeys(), "")
	srcDSN := ask(r, "Source database DSN", "")
	srcSchema := ask(r, "Source schema name", "")

	cfg := buildConfig(srcType, srcDSN, srcSchema, srcType, "", srcSchema, "database")
	cfg.DDL.TargetDialect = srcType
	cfg.DDL.SchemaMapping = map[string]string{srcSchema: srcSchema}
	cfg.Import = config.ImportConfig{}
	cfg.DDL = config.DDLConfig{TargetDialect: srcType}

	return writeConfig(cfg, outputPath)
}

// ── import: data dir + target DB ──

func interactiveImport(r *bufio.Reader, outputPath string) error {
	dataDir := ask(r, "CSV data files directory", "./output/data/")
	tgtType := askChoice(r, "Target database type", sortedDialectKeys(), "")
	tgtDSN := ask(r, "Target database DSN", "")
	tgtSchema := ask(r, "Target schema name", "")

	cfg := buildConfig(tgtType, tgtDSN, tgtSchema, tgtType, tgtDSN, tgtSchema, "database")
	cfg.DDL.TargetDialect = tgtType
	cfg.Import.SourceDir = dataDir
	cfg.Export = config.ExportConfig{}

	return writeConfig(cfg, outputPath)
}

// ── migrate: source + target ──

func interactiveMigrate(r *bufio.Reader, outputPath string) error {
	srcType := askChoice(r, "Source database type", sortedDialectKeys(), "")
	srcDSN := ask(r, "Source database DSN", "")
	srcSchema := ask(r, "Source schema name", "")
	tgtType := askChoice(r, "Target database type", sortedDialectKeys(), "")
	tgtDSN := ask(r, "Target database DSN", "")
	tgtSchema := ask(r, "Target schema name (default: source schema)", "")
	if tgtSchema == "" {
		tgtSchema = srcSchema
	}

	cfg := buildConfig(srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema, "database")
	cfg.DDL.TargetDialect = tgtType
	cfg.DDL.SchemaMapping = map[string]string{srcSchema: tgtSchema}

	return writeConfig(cfg, outputPath)
}

// ── export-metadata: source DB + format ──

func interactiveExportMetadata(r *bufio.Reader, outputPath string) error {
	srcType := askChoice(r, "Source database type", sortedDialectKeys(), "")
	srcDSN := ask(r, "Source database DSN", "")
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

	cfg := buildConfig(srcType, srcDSN, srcSchema, srcType, "", srcSchema, "database")
	cfg.DDL.TargetDialect = srcType
	cfg.Import = config.ImportConfig{}
	cfg.Export = config.ExportConfig{}
	cfg.DDL = config.DDLConfig{TargetDialect: srcType}

	return writeConfig(cfg, outputPath)
}

// ── full: all options with hints ──

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
			srcDSN = ask(r, "Source database DSN", "")
			hint("Example: oracle://user:pass@host:1521/service")
			srcSchema = ask(r, "Source schema name", "")
			hint("For Oracle: schema/owner name. For MySQL: database name. For PG: schema name.")
		}
	}

	fmt.Println()
	hint("Target: the database you are migrating TO. Determines DDL dialect and is required for import/migrate.")
	tgtType = askChoice(r, "Target database type (for DDL generation)", sortedDialectKeys(), "postgres")
	tgtDSN = ask(r, "Target database DSN (optional, leave blank for DDL-only)", "")
	tgtSchema = ask(r, "Target schema name (leave blank to use source schema)", "")

	// Build config
	cfg := buildConfig(srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema, mt)
	if mt == "csv" && csvPath != "" {
		cfg.Metadata.CSV.Path = csvPath
	}
	if mt == "xlsx" && xlsxPath != "" {
		cfg.Metadata.XLSX.Path = xlsxPath
	}

	return writeConfig(cfg, outputPath)
}

// ── Config writing ──

func writeConfig(cfg *config.Config, outputPath string) error {
	buf, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	header := "# Auto-generated by owl-migrate init\n" +
		"# Edit this file to fine-tune migration settings, then run:\n" +
		"#   owl-migrate validate -c " + outputPath + "\n" +
		"#   owl-migrate gen-ddl  -c " + outputPath + "\n" +
		"#   owl-migrate migrate  -c " + outputPath + "\n\n"

	content := append([]byte(header), buf...)

	if err := os.WriteFile(outputPath, content, 0644); err != nil {
		return fmt.Errorf("write config to %q: %w", outputPath, err)
	}

	fmt.Printf("\nConfiguration written to %s\n", outputPath)
	return nil
}

// buildConfig constructs a fully-populated Config from init command parameters.
func buildConfig(srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema, metaType string) *config.Config {
	srcType = strings.ToLower(srcType)
	tgtType = strings.ToLower(tgtType)

	if tgtSchema == "" {
		tgtSchema = srcSchema
	}

	schemaMapping := make(map[string]string)
	if srcSchema != "" {
		schemaMapping[srcSchema] = tgtSchema
	}

	csvPath := ""
	xlsxPath := ""
	if metaType == "csv" {
		csvPath = "./testdata/csv/"
	}
	if metaType == "xlsx" {
		xlsxPath = "./metadata/schema.xlsx"
	}

	return &config.Config{
		General: config.GeneralConfig{
			LogLevel:  "info",
			LogFormat: "text",
		},
		Metadata: config.MetadataConfig{
			Type: metaType,
			XLSX: config.XLSXConfig{
				Path:          xlsxPath,
				DataOutputDir: "./output/data/",
			},
			CSV: config.CSVConfig{
				Path:               csvPath,
				Delimiter:          ",",
				Encoding:           "utf-8",
				HasHeader:          true,
				NullMarker:         "\\N",
				ColumnNameMatching: "case_insensitive",
			},
		},
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
			OutputDir:          "./output/ddl/",
			TargetDialect:      tgtType,
			IncludeComments:    true,
			IncludeIfNotExists: true,
			SplitByObject:      true,
			SchemaMapping:      schemaMapping,
			TableFilter: config.TableFilterConfig{
				Include: []string{"*"},
				Exclude: config.TableExcludeConfig{
					Glob:    []string{"*_LOG", "TMP_*"},
					Schemas: []string{"SYS", "SYSTEM"},
				},
			},
			TypeOverrides:      make(map[string]string),
			NoQuoteIdentifiers: false,
		},
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
				Encoding:           "utf-8",
				Header:             true,
				NullRepresentation: "\\N",
			},
			Batch: config.BatchConfig{
				Method:   "cursor",
				PageSize: 5000,
			},
			Parallel: config.ParallelConfig{
				Enabled:    true,
				MaxWorkers: 4,
			},
			Tables: config.TableListConfig{
				Include: []string{"*"},
			},
		},
		Import: config.ImportConfig{
			SourceDir: "./output/data/",
			Format:    "csv",
			CSV: config.ImportCSVConfig{
				Delimiter:  ",",
				HasHeader:  true,
				NullMarker: "\\N",
			},
			Target: config.ImportTargetConfig{
				TruncateBefore: false,
			},
			Batch: config.ImportBatchConfig{
				CommitInterval: 1000,
				ErrorPolicy:    "skip_row",
			},
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
