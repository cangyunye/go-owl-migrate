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
  5. Generate migration report`,
	}

	var (
		tempDir         string
		skipDDL         bool
		continueOnError bool
		reportFile      string
	)

	cmd.Flags().StringVar(&tempDir, "temp-dir", "./output/temp/", "temporary directory for CSV files")
	cmd.Flags().BoolVar(&skipDDL, "skip-ddl", false, "skip table creation in target (data-only)")
	cmd.Flags().BoolVar(&continueOnError, "continue-on-error", false, "exit with code 0 even if some tables have errors")
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
		fmt.Printf("Loaded %d tables from metadata\n", len(sm.GetTables()))

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

		// Step 3: Connect to target
		fmt.Println("=== Step 3: Connect to target ===")
		tgtDB, err := openDB(cfg.Target.Type, cfg.Target.DSN)
		if err != nil {
			return fmt.Errorf("connect target: %w", err)
		}
		defer tgtDB.Close()
		if err := tgtDB.Ping(); err != nil {
			return fmt.Errorf("ping target: %w", err)
		}
		fmt.Printf("Connected to target: %s\n", cfg.Target.Type)

		// Step 4: Create target tables
		if !skipDDL {
			fmt.Println("=== Step 4: Create target tables ===")
			if err := ensureTablesForMigrate(cmd.Context(), tgtDB, sm, cfg); err != nil {
				return fmt.Errorf("create tables: %w", err)
			}
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
		tables := sm.GetTables()
		exportResults, err := exp.ExportTables(ctx, tables, pkMap)
		if err != nil {
			return fmt.Errorf("export: %w", err)
		}
		for _, r := range exportResults {
			if r.Error != nil {
				fmt.Printf("  FAIL %s.%s: %v\n", r.Schema, r.Table, r.Error)
				continue
			}
			fmt.Printf("  %s.%s → %d rows\n", r.Schema, r.Table, r.Rows)
		}

		// Step 6: Import to target
		fmt.Println("=== Step 6: Import to target ===")
		impLogger := newLogger(cfg)
		imp := importer.New(tgtDB, importer.Config{
			SourceDir:      exportDir,
			CSVDelimiter:   cfg.Import.CSV.Delimiter,
			CSVNullMarker:  cfg.Import.CSV.NullMarker,
			TruncateBefore: cfg.Import.Target.TruncateBefore,
			CommitInterval: cfg.Import.Batch.CommitInterval,
			ErrorPolicy:    cfg.Import.Batch.ErrorPolicy,
			MaxErrors:      cfg.Import.Batch.MaxErrorsBeforeStop,
			MaxWorkers:     cfg.Import.Parallel.MaxWorkers,
			DateTimeFormat: cfg.Import.DataTransforms.DatetimeFormat,
			TrimStrings:    cfg.Import.DataTransforms.TrimStrings,
			TargetDBType:   cfg.Target.Type,
			Logger:         impLogger,
		})

		importResults, err := imp.ImportTables(ctx, tables, cfg.DDL.SchemaMapping)
		if err != nil {
			return fmt.Errorf("import: %w", err)
		}
		for _, r := range importResults {
			if r.Err != nil {
				fmt.Printf("  FAIL %s.%s: %v\n", r.Schema, r.Table, r.Err)
				report.AddTable(r.Schema, r.Table, r.Expected, r.Actual, r.Skipped, r.Errors, r.Err.Error())
				continue
			}
			status := "✅"
			if r.Skipped > 0 || r.Actual != r.Expected {
				status = "⚠️"
			}
			fmt.Printf("  %s %s.%s: %d/%d rows\n", status, r.Schema, r.Table, r.Actual, r.Expected)
			report.AddTable(r.Schema, r.Table, r.Expected, r.Actual, r.Skipped, r.Errors, "")
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
			for _, r := range exportResults {
				if r.Error != nil {
					exportErrors++
				}
			}
			importErrors := 0
			for _, r := range importResults {
				if r.Err != nil {
					importErrors++
				}
			}
			if exportErrors > 0 || importErrors > 0 {
				return fmt.Errorf("migration completed with %d export errors, %d import errors", exportErrors, importErrors)
			}
		}

		return nil
	}

	return cmd
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
