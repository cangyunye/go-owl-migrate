package cmd

import (
	"context"
	"fmt"

	_ "github.com/go-sql-driver/mysql" // MySQL driver
	_ "github.com/lib/pq"              // PostgreSQL driver
	_ "github.com/sijms/go-ora/v2"     // Oracle driver (pure Go)
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
)

func exportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export data from source database to CSV files",
		Long: `Connects to the source database, reads table data in batches,
	and writes CSV files to the output directory.

	Uses cursor-based pagination when primary keys are available.`,
	}

	var outputDir string
	cmd.Flags().StringVarP(&outputDir, "output", "o", "./output/data/", "output directory for CSV files")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		// Load metadata from CSV or database
		sm, err := loadSchemaModel(cfg)
		if err != nil {
			return err
		}

		// Connect to source database
		db, err := openDB(cfg.Source.Type, cfg.Source.DSN)
		if err != nil {
			return fmt.Errorf("connect to source: %w", err)
		}
		defer db.Close()

		if err := db.Ping(); err != nil {
			return fmt.Errorf("ping source: %w", err)
		}
		fmt.Printf("Connected to %s\n", cfg.Source.Type)

		// Build PK map for cursor pagination
		pkMap := buildPKMap(sm)

		// Build logger
		logCfg := zap.NewDevelopmentConfig()
		logCfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
		if cfg.General.LogLevel == "debug" {
			logCfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
		}
		logger, _ := logCfg.Build()
		defer logger.Sync()

		exp := exporter.New(db, exporter.Config{
			OutputDir:         outputDir,
			PageSize:          cfg.Export.Batch.PageSize,
			MaxWorkers:        cfg.Export.Parallel.MaxWorkers,
			CSVDelimiter:      cfg.Export.CSV.Delimiter,
			CSVQuoteChar:      cfg.Export.CSV.QuoteChar,
			CSVNullRep:        cfg.Export.CSV.NullRepresentation,
			CSVHeader:         cfg.Export.CSV.Header,
			CSVLineTerminator: cfg.Export.CSV.LineTerminator,
			DBType:            cfg.Source.Type,
			Logger:            logger,
		})

		ctx := context.Background()
		tables := filterTables(sm.GetTables(), cfg.Export.Tables.Include)
		results, err := exp.ExportTables(ctx, tables, pkMap)
		if err != nil {
			return err
		}

		// Print summary
		totalRows := int64(0)
		for _, r := range results {
			if r.Error != nil {
				fmt.Printf("  FAIL %s.%s: %v\n", r.Schema, r.Table, r.Error)
				continue
			}
			fmt.Printf("  %s.%s → %s (%d rows, %d batches, %v)\n",
				r.Schema, r.Table, r.OutputFile, r.Rows, r.Batches, r.Duration)
			totalRows += r.Rows
		}
		fmt.Printf("Exported %d rows across %d tables\n", totalRows, len(results))
		return nil
	}

	return cmd
}
