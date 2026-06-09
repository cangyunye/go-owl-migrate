package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/go-sql-driver/mysql"  // MySQL driver
	_ "github.com/lib/pq"               // PostgreSQL driver
	_ "github.com/sijms/go-ora/v2"     // Oracle driver (pure Go)
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	csvpkg "github.com/cangyunye/go-owl-migrate/internal/metadata/csv"
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

		// Load CSV metadata for table/column info
		csvDir := cfg.Metadata.CSV.Path
		if csvDir == "" {
			csvDir = "./testdata/csv/"
		}
		sm, pkMap, err := loadMetadata(csvDir)
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
			PageSize:           cfg.Export.Batch.PageSize,
			MaxWorkers:         cfg.Export.Parallel.MaxWorkers,
			CSVDelimiter:       cfg.Export.CSV.Delimiter,
			CSVQuoteChar:       cfg.Export.CSV.QuoteChar,
			CSVNullRep:         cfg.Export.CSV.NullRepresentation,
			CSVHeader:          cfg.Export.CSV.Header,
			CSVLineTerminator:  cfg.Export.CSV.LineTerminator,
			DBType:             cfg.Source.Type,
			Logger:             logger,
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

func openDB(dbType, dsn string) (*sql.DB, error) {
	switch strings.ToLower(dbType) {
	case "mysql":
		return sql.Open("mysql", dsn)
	case "postgres", "postgresql":
		return sql.Open("postgres", dsn)
	case "oracle":
		return sql.Open("oracle", dsn)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}
}

func loadMetadata(csvDir string) (*md.SchemaModel, map[string][]string, error) {
	loader := csvpkg.NewLoader()
	entries, err := os.ReadDir(csvDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read metadata dir %q: %w", csvDir, err)
	}
	hasTables := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".csv") {
			continue
		}
		path := filepath.Join(csvDir, entry.Name())
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, fmt.Errorf("open %s: %w", path, err)
		}
		defer f.Close()
		loader.AddReader(entry.Name(), f)
		if entry.Name() == "tables.csv" || entry.Name() == "Tables.csv" {
			hasTables = true
		}
	}
	if !hasTables {
		return nil, nil, fmt.Errorf("tables.csv not found in %s", csvDir)
	}
	sm, err := loader.Load()
	if err != nil {
		return nil, nil, err
	}

	// Build PK map for cursor pagination
	pkMap := make(map[string][]string)
	for _, tbl := range sm.GetTables() {
		pks := tbl.GetPrimaryKeys()
		if len(pks) > 0 {
			key := fmt.Sprintf("%s.%s", tbl.TableSchema, tbl.TableName)
			names := make([]string, len(pks))
			for i, pk := range pks {
				names[i] = pk.ColumnName
			}
			pkMap[key] = names
		}
	}

	return sm, pkMap, nil
}

func filterTables(tables []*md.TableDef, include []string) []*md.TableDef {
	if len(include) == 1 && include[0] == "*" {
		return tables
	}
	includeSet := make(map[string]bool)
	for _, inc := range include {
		includeSet[inc] = true
	}
	var result []*md.TableDef
	for _, tbl := range tables {
		key := fmt.Sprintf("%s.%s", tbl.TableSchema, tbl.TableName)
		if includeSet[key] || includeSet["*"] {
			result = append(result, tbl)
		}
	}
	return result
}
