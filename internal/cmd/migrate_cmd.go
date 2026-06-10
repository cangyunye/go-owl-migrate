package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/sijms/go-ora/v2"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/importer"
)

func migrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "End-to-end migration: source DB → target DB",
		Long: `Single-command end-to-end database migration.

Flow:
  1. Read metadata from CSV or source database
  2. Export data from source database to CSV files
  3. Create tables in target database (if needed)
  4. Import CSV data into target database
  5. Generate migration report

Use --sql-out to generate INSERT SQL files instead of writing directly to target.
Use --resume to skip tables completed in a previous run.`,
	}

	var (
		tempDir         string
		skipDDL         bool
		continueOnError bool
		sqlOut          string
		resume          bool
		reportFile      string
	)

	cmd.Flags().StringVar(&tempDir, "temp-dir", "./output/temp/", "temporary directory for CSV files")
	cmd.Flags().BoolVar(&skipDDL, "skip-ddl", false, "skip table creation in target (data-only)")
	cmd.Flags().BoolVar(&continueOnError, "continue-on-error", false, "exit with code 0 even if some tables have errors")
	cmd.Flags().StringVar(&sqlOut, "sql-out", "", "output directory for INSERT SQL files (offline mode, skips target DB)")
	cmd.Flags().BoolVar(&resume, "resume", false, "resume from previous migration state (skips completed tables)")
	cmd.Flags().StringVarP(&reportFile, "report", "r", "./output/migration_report.json", "migration report output path")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		report := NewMigrationReport(cfg.Source.Type, cfg.Target.Type)
		startTime := time.Now()

		// Step 1: Load metadata from CSV or database
		fmt.Println("=== Step 1: Load metadata ===")
		sm, err := loadSchemaModel(cfg)
		if err != nil {
			return fmt.Errorf("load metadata: %w", err)
		}
		allTables := sm.GetTables()
		fmt.Printf("Loaded %d tables from metadata\n", len(allTables))

		// Build PK map for cursor pagination
		pkMap := buildPKMap(sm)

		// Step 2: Connect to source
		fmt.Println("=== Step 2: Connect to source ===")
		srcDB, err := openDB(cfg.Source.Type, cfg.Source.DSN)
		if err != nil {
			return fmt.Errorf("connect source: %w", err)
		}
		defer srcDB.Close()
		if err := srcDB.Ping(); err != nil {
			return fmt.Errorf("ping source: %w", err)
		}
		fmt.Printf("Connected to source: %s\n", cfg.Source.Type)

		sqlMode := sqlOut != ""

		// Step 3: Connect to target (only in direct-import mode)
		var tgtDB *sql.DB
		if !sqlMode {
			fmt.Println("=== Step 3: Connect to target ===")
			tgtDB, err = openDB(cfg.Target.Type, cfg.Target.DSN)
			if err != nil {
				return fmt.Errorf("connect target: %w", err)
			}
			defer tgtDB.Close()
			if err := tgtDB.Ping(); err != nil {
				return fmt.Errorf("ping target: %w", err)
			}
			fmt.Printf("Connected to target: %s\n", cfg.Target.Type)
		} else {
			fmt.Println("=== Step 3: Skip (SQL output mode) ===")
		}

		// Load migration state for resume
		stateFile := filepath.Join(tempDir, "migrate_progress.json")
		var ms *migrateState
		if resume {
			ms, err = loadMigrateState(stateFile)
			if err != nil {
				fmt.Printf("  No previous state found at %s, starting fresh\n", stateFile)
			} else {
				fmt.Printf("  Loaded state for %d tables from %s\n", len(ms.Tables), stateFile)
			}
		}
		if ms == nil {
			ms = newMigrateState(cfg.Source.Type, cfg.Target.Type)
		}

		// Filter tables based on resume state
		var tablesToProcess []*md.TableDef
		var resumedCount int
		for _, tbl := range allTables {
			key := tableKey(tbl)
			st := ms.Tables[key]
			if st.Status == "SUCCESS" {
				fmt.Printf("  Skip %s (completed in previous run)\n", key)
				resumedCount++
				continue
			}
			tablesToProcess = append(tablesToProcess, tbl)
		}
		if resumedCount > 0 {
			fmt.Printf("Resume: skipping %d already-completed tables, processing %d tables\n", resumedCount, len(tablesToProcess))
		}

		// Step 4: Create target tables (only in direct-import mode)
		if !sqlMode {
			if !skipDDL {
				fmt.Println("=== Step 4: Create target tables ===")
				// Only create tables that need processing
				tblMap := make(map[string]*md.TableDef, len(tablesToProcess))
				for _, tbl := range tablesToProcess {
					tblMap[tableKey(tbl)] = tbl
				}
				if err := ensureTablesForMigrate(cmd.Context(), tgtDB, &md.SchemaModel{Tables: tblMap}, cfg); err != nil {
					return fmt.Errorf("create tables: %w", err)
				}
			}
		} else {
			fmt.Println("=== Step 4: Skip (SQL output mode) ===")
		}

		// Step 5: Export from source
		fmt.Println("=== Step 5: Export from source ===")
		exportDir := tempDir
		if exportDir == "" {
			exportDir = "./output/temp/"
		}
		os.MkdirAll(exportDir, 0755)
		expLogger := newLogger(cfg)
		exp := exporter.New(srcDB, exporter.Config{
			OutputDir:         exportDir,
			PageSize:          cfg.Export.Batch.PageSize,
			MaxWorkers:        cfg.Export.Parallel.MaxWorkers,
			CSVDelimiter:      cfg.Export.CSV.Delimiter,
			CSVQuoteChar:      cfg.Export.CSV.QuoteChar,
			CSVNullRep:        cfg.Export.CSV.NullRepresentation,
			CSVHeader:         cfg.Export.CSV.Header,
			CSVLineTerminator: cfg.Export.CSV.LineTerminator,
			DBType:            cfg.Source.Type,
			Logger:            expLogger,
		})

		ctx := context.Background()

		// Only export tables that haven't been exported yet
		var tablesToExport []*md.TableDef
		for _, tbl := range tablesToProcess {
			key := tableKey(tbl)
			if st, ok := ms.Tables[key]; ok && st.Exported {
				fmt.Printf("  Skip export %s (already exported)\n", key)
				continue
			}
			tablesToExport = append(tablesToExport, tbl)
		}

		exportResults, err := exp.ExportTables(ctx, tablesToExport, pkMap)
		if err != nil {
			return fmt.Errorf("export: %w", err)
		}

		// Merge export results with already-exported tables for reporting
		type tableExportResult struct {
			Schema string
			Table  string
			Rows   int64
			Error  error
		}
		var allExportResults []tableExportResult
		for _, tbl := range tablesToProcess {
			key := tableKey(tbl)
			st := ms.Tables[key]
			if st.Exported {
				allExportResults = append(allExportResults, tableExportResult{
					Schema: tbl.TableSchema,
					Table:  tbl.TableName,
					Rows:   st.ExportedRows,
				})
				continue
			}
			// Find matching result from current run
			for _, r := range exportResults {
				if strings.EqualFold(r.Schema, tbl.TableSchema) && strings.EqualFold(r.Table, tbl.TableName) {
					allExportResults = append(allExportResults, tableExportResult{
						Schema: r.Schema,
						Table:  r.Table,
						Rows:   r.Rows,
						Error:  r.Error,
					})
					// Update state
					ms.markExported(key, r.Rows, r.Error)
					break
				}
			}
		}

		// Also append any export results for tables not in tablesToProcess (errors)
		for _, r := range exportResults {
			found := false
			for _, ar := range allExportResults {
				if strings.EqualFold(ar.Schema, r.Schema) && strings.EqualFold(ar.Table, r.Table) {
					found = true
					break
				}
			}
			if !found {
				allExportResults = append(allExportResults, tableExportResult{
					Schema: r.Schema,
					Table:  r.Table,
					Rows:   r.Rows,
					Error:  r.Error,
				})
			}
		}
		saveMigrateState(stateFile, ms)

		for _, r := range allExportResults {
			if r.Error != nil {
				fmt.Printf("  FAIL %s.%s: %v\n", r.Schema, r.Table, r.Error)
				continue
			}
			fmt.Printf("  %s.%s → %d rows\n", r.Schema, r.Table, r.Rows)
		}

		// Step 5.5: Generate INSERT SQL (SQL output mode)
		if sqlMode {
			fmt.Println("=== Step 5.5: Generate INSERT SQL ===")
			dialect := cfg.DDL.TargetDialect
			if dialect == "" {
				dialect = cfg.Target.Type
			}
			gen := generator.NewInsertGenerator(generator.InsertConfig{
				OutputDir:    sqlOut,
				BatchSize:    100,
				Dialect:      dialect,
				NullMarker:   cfg.Import.CSV.NullMarker,
				CSVDelimiter: cfg.Import.CSV.Delimiter,
			})
			files, err := gen.Generate(tablesToProcess, exportDir)
			if err != nil {
				return fmt.Errorf("generate SQL: %w", err)
			}
			_ = files // used for iteration below
			for _, f := range files {
				fmt.Printf("  %s\n", f)
			}
			fmt.Printf("Generated %d INSERT SQL files to %s\n", len(files), sqlOut)
			// Mark all tables as imported in SQL mode
			for _, tbl := range tablesToProcess {
				ms.markImported(tableKey(tbl), 0, nil)
			}
			saveMigrateState(stateFile, ms)
		}

		// Step 6: Import to target (only in direct-import mode)
		var importResults []importer.ImportResult
		if !sqlMode {
			fmt.Println("=== Step 6: Import to target ===")

			// Only import tables that haven't been successfully imported yet
			var tablesToImport []*md.TableDef
			for _, tbl := range tablesToProcess {
				key := tableKey(tbl)
				if st, ok := ms.Tables[key]; ok && st.Status == "SUCCESS" {
					fmt.Printf("  Skip import %s (already imported)\n", key)
					continue
				}
				tablesToImport = append(tablesToImport, tbl)
			}

			if len(tablesToImport) > 0 {
				impLogger := newLogger(cfg)
				imp := importer.New(tgtDB, importer.Config{
					SourceDir:      exportDir,
					CSVDelimiter:   cfg.Import.CSV.Delimiter,
					CSVNullMarker:  cfg.Import.CSV.NullMarker,
					TruncateBefore: resume, // truncate on resume to avoid duplicate key errors
					CommitInterval: cfg.Import.Batch.CommitInterval,
					ErrorPolicy:    cfg.Import.Batch.ErrorPolicy,
					MaxErrors:      cfg.Import.Batch.MaxErrorsBeforeStop,
					MaxWorkers:     cfg.Import.Parallel.MaxWorkers,
					DateTimeFormat: cfg.Import.DataTransforms.DatetimeFormat,
					TrimStrings:    cfg.Import.DataTransforms.TrimStrings,
					TargetDBType:   cfg.Target.Type,
					Logger:         impLogger,
				})

				importResults, err = imp.ImportTables(ctx, tablesToImport, cfg.DDL.SchemaMapping)
				if err != nil {
					return fmt.Errorf("import: %w", err)
				}
			}

			for _, tbl := range tablesToProcess {
				key := tableKey(tbl)
				// Check if already successfully imported from state
				if st, ok := ms.Tables[key]; ok && st.Status == "SUCCESS" {
					report.AddTable(tbl.TableSchema, tbl.TableName, st.ExportedRows, st.ExportedRows, 0, 0, "")
					fmt.Printf("  ✅ %s.%s: %d rows (from previous run)\n", tbl.TableSchema, tbl.TableName, st.ExportedRows)
					continue
				}
				// Find result from current import
				for _, r := range importResults {
					if strings.EqualFold(r.Schema, tbl.TableSchema) && strings.EqualFold(r.Table, tbl.TableName) {
						if r.Err != nil {
							ms.markImported(key, 0, r.Err)
							fmt.Printf("  FAIL %s.%s: %v\n", r.Schema, r.Table, r.Err)
							report.AddTable(r.Schema, r.Table, r.Expected, r.Actual, r.Skipped, r.Errors, r.Err.Error())
						} else {
							ms.markImported(key, r.Actual, nil)
							status := "✅"
							if r.Skipped > 0 || r.Actual != r.Expected {
								status = "⚠️"
							}
							fmt.Printf("  %s %s.%s: %d/%d rows\n", status, r.Schema, r.Table, r.Actual, r.Expected)
							report.AddTable(r.Schema, r.Table, r.Expected, r.Actual, r.Skipped, r.Errors, "")
						}
						break
					}
				}
			}
			saveMigrateState(stateFile, ms)
		}

		// Step 7: Generate report
		report.Duration = time.Since(startTime).String()
		fmt.Println("=== Step 7: Migration report ===")
		report.Print()

		if reportFile != "" {
			dir := filepath.Dir(reportFile)
			os.MkdirAll(dir, 0755)
			data, _ := json.MarshalIndent(report, "", "  ")
			os.WriteFile(reportFile, data, 0644)
			fmt.Printf("Report saved to %s\n", reportFile)
		}

		// Return non-zero exit when per-table errors exist and --continue-on-error is off
		if !continueOnError {
			exportErrors := 0
			for _, r := range allExportResults {
				if r.Error != nil {
					exportErrors++
				}
			}
			if !sqlMode {
				importErrors := 0
				for _, r := range importResults {
					if r.Err != nil {
						importErrors++
					}
				}
				if exportErrors > 0 || importErrors > 0 {
					return fmt.Errorf("migration completed with %d export errors, %d import errors", exportErrors, importErrors)
				}
			} else if exportErrors > 0 {
				return fmt.Errorf("export completed with %d export errors", exportErrors)
			}
		}

		return nil
	}

	return cmd
}

// ── Migration state (checkpoint/resume) ──

type tableCheckpoint struct {
	Exported     bool   `json:"exported"`
	ExportedRows int64  `json:"exported_rows"`
	Imported     bool   `json:"imported"`
	Status       string `json:"status"` // "", "SUCCESS", "FAIL"
	Error        string `json:"error,omitempty"`
}

type migrateState struct {
	Version   int                        `json:"version"`
	Source    string                     `json:"source"`
	Target    string                     `json:"target"`
	Tables    map[string]tableCheckpoint `json:"tables"`
	StartedAt string                     `json:"started_at"`
}

func tableKey(tbl *md.TableDef) string {
	return fmt.Sprintf("%s.%s", strings.ToLower(tbl.TableSchema), strings.ToLower(tbl.TableName))
}

func newMigrateState(source, target string) *migrateState {
	return &migrateState{
		Version:   1,
		Source:    source,
		Target:    target,
		Tables:    make(map[string]tableCheckpoint),
		StartedAt: time.Now().Format(time.RFC3339),
	}
}

func loadMigrateState(path string) (*migrateState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ms migrateState
	if err := json.Unmarshal(data, &ms); err != nil {
		return nil, err
	}
	if ms.Tables == nil {
		ms.Tables = make(map[string]tableCheckpoint)
	}
	return &ms, nil
}

func saveMigrateState(path string, ms *migrateState) error {
	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0755)
	data, err := json.MarshalIndent(ms, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (ms *migrateState) markExported(key string, rows int64, err error) {
	st := ms.Tables[key]
	st.Exported = true
	st.ExportedRows = rows
	if err != nil {
		st.Status = "FAIL"
		st.Error = err.Error()
	} else {
		st.Status = ""
	}
	ms.Tables[key] = st
}

func (ms *migrateState) markImported(key string, rows int64, err error) {
	st := ms.Tables[key]
	st.Imported = true
	if err != nil {
		st.Status = "FAIL"
		st.Error = err.Error()
	} else {
		st.Status = "SUCCESS"
		st.Error = ""
	}
	ms.Tables[key] = st
}

// MigrationReport holds the summary of a migration operation.
type MigrationReport struct {
	SourceDialect string        `json:"source_dialect"`
	TargetDialect string        `json:"target_dialect"`
	GeneratedAt   string        `json:"generated_at"`
	Duration      string        `json:"duration"`
	Tables        []TableReport `json:"tables"`
	TotalExpected int64         `json:"total_expected"`
	TotalActual   int64         `json:"total_actual"`
	TotalSkipped  int64         `json:"total_skipped"`
	TotalErrors   int64         `json:"total_errors"`
	Status        string        `json:"status"`
}

// TableReport holds per-table migration results.
type TableReport struct {
	Schema   string `json:"schema"`
	Table    string `json:"table"`
	Expected int64  `json:"expected"`
	Actual   int64  `json:"actual"`
	Skipped  int64  `json:"skipped"`
	Errors   int64  `json:"errors"`
	Error    string `json:"error,omitempty"`
}

func NewMigrationReport(srcDialect, tgtDialect string) *MigrationReport {
	return &MigrationReport{
		SourceDialect: srcDialect,
		TargetDialect: tgtDialect,
		GeneratedAt:   time.Now().Format("2006-01-02 15:04:05"),
	}
}

func (r *MigrationReport) AddTable(schema, table string, expected, actual, skipped, errors int64, errMsg string) {
	r.Tables = append(r.Tables, TableReport{
		Schema: schema, Table: table,
		Expected: expected, Actual: actual,
		Skipped: skipped, Errors: errors,
		Error: errMsg,
	})
	r.TotalExpected += expected
	r.TotalActual += actual
	r.TotalSkipped += skipped
	r.TotalErrors += errors
}

func (r *MigrationReport) Print() {
	if r.TotalErrors > 0 || r.TotalSkipped > 0 || r.TotalActual != r.TotalExpected {
		r.Status = "PARTIAL"
	} else {
		r.Status = "SUCCESS"
	}

	fmt.Printf("\n=== Migration Report ===\n")
	fmt.Printf("Source: %s  →  Target: %s\n", r.SourceDialect, r.TargetDialect)
	fmt.Printf("Status: %s  Duration: %s\n", r.Status, r.Duration)
	fmt.Printf("Total: %d/%d rows (%d skipped, %d errors)\n\n", r.TotalActual, r.TotalExpected, r.TotalSkipped, r.TotalErrors)

	for _, t := range r.Tables {
		status := "✅"
		if t.Errors > 0 || t.Skipped > 0 {
			status = "⚠️"
		} else if t.Error != "" {
			status = "❌"
		}
		fmt.Printf("  %s %s.%s: %d/%d rows", status, t.Schema, t.Table, t.Actual, t.Expected)
		if t.Skipped > 0 {
			fmt.Printf(" (%d skipped)", t.Skipped)
		}
		if t.Error != "" {
			fmt.Printf(" — %s", t.Error)
		}
		fmt.Println()
	}
}

func ensureTablesForMigrate(ctx context.Context, db *sql.DB, sm *md.SchemaModel, cfg *config.Config) error {
	// Track which schemas we've already created
	createdSchemas := make(map[string]bool)

	for _, tbl := range sm.GetTables() {
		schema := tbl.TableSchema
		if m, ok := cfg.DDL.SchemaMapping[schema]; ok {
			schema = m
		}

		// Create schema if needed (PostgreSQL)
		if !createdSchemas[schema] {
			schemaSQL := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)
			db.ExecContext(ctx, schemaSQL) // ignore error (MySQL doesn't have schemas)
			createdSchemas[schema] = true
		}

		createSQL := buildCreateTableSQL(tbl, schema, cfg)
		if createSQL != "" {
			if _, err := db.ExecContext(ctx, createSQL); err != nil {
				// Table may already exist — try with IF NOT EXISTS
				if strings.Contains(err.Error(), "already exists") {
					fmt.Printf("  Table %s.%s already exists, skipping\n", schema, tbl.TableName)
					continue
				}
				return fmt.Errorf("create table %s.%s: %w (SQL: %s)", schema, tbl.TableName, err, createSQL)
			}
			fmt.Printf("  Created %s.%s\n", schema, tbl.TableName)
		}
	}
	return nil
}

func newLogger(cfg *config.Config) *zap.Logger {
	logCfg := zap.NewDevelopmentConfig()
	logCfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	if cfg.General.LogLevel == "debug" {
		logCfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	}
	logger, _ := logCfg.Build()
	return logger
}
