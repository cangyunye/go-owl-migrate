package cmd

import (
	"context"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/sijms/go-ora/v2"
	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/service"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
)

func exportDataCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data",
		Short: "Export data from source database or CSV/XLSX to output files",
		Long: `Exports table data from a live database, CSV files, or XLSX files.

Online mode (requires config with source.type/source.dsn):
  owl-migrate export data -c ./migrate.yaml -o ./output/data/

Offline CSV mode (no database needed):
  owl-migrate export data -d ./data/ -o ./output/sql/ --format sql

Offline XLSX mode (no database needed):
  owl-migrate export data --xlsx ./data.xlsx -o ./output/xlsx/ --format xlsx

Supported output formats: csv (default), sql, xlsx`,
	}

	var (
		outputDir string
		noQuote   bool
		dataDir   string
		xlsxPath  string
		format    string
	)
	cmd.Flags().StringVarP(&outputDir, "output", "o", "./output/data/", "output directory for export files")
	cmd.Flags().BoolVar(&noQuote, "no-quote-identifiers", false, "do not quote identifiers (bare names, for compatibility)")
	cmd.Flags().StringVarP(&dataDir, "data", "d", "", "directory containing CSV data files (offline mode)")
	cmd.Flags().StringVar(&xlsxPath, "xlsx", "", "path to xlsx file with @ data sheets (offline mode)")
	cmd.Flags().StringVar(&format, "format", "", "output format: csv (default), sql, xlsx")

	cmd.RunE = func(cmd *cobra.Command, args []string) (retErr error) {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			cfg = &config.Config{}
		}
		if cmd.Flags().Changed("no-quote-identifiers") {
			cfg.DDL.NoQuoteIdentifiers = noQuote
		}
		if cmd.Flags().Changed("format") {
			cfg.Export.Format = format
		}

		var pw *service.ProgressWriter
		if progressDB != "" && jobID != "" {
			if w, e := service.NewProgressWriter(progressDB, jobID); e == nil {
				pw = w
				defer pw.Close()
				defer func() {
					if retErr != nil {
						pw.SetJobFailed(retErr.Error())
					}
				}()
			}
		}

		// Determine if we're in offline mode (CSV or XLSX) or online mode (DB)
		offlineCSV := cmd.Flags().Changed("data")
		offlineXLSX := cmd.Flags().Changed("xlsx")

		// ── Offline CSV mode ──
		if offlineCSV {
			tables, err := exporter.DetectTablesFromCSV(dataDir)
			if err != nil {
				return err
			}

			var dataTables []*exporter.DataTable
			for _, tbl := range tables {
				dt, err := exporter.ReadCSVTable(dataDir, tbl)
				if err != nil {
					return fmt.Errorf("read %s.%s: %w", tbl.TableSchema, tbl.TableName, err)
				}
				dataTables = append(dataTables, dt)
			}

			exp := exporter.New(nil, exporter.Config{
				OutputDir:         outputDir,
				Format:            cfg.Export.Format,
				CSVDelimiter:      cfg.Export.CSV.Delimiter,
				CSVQuoteChar:      cfg.Export.CSV.QuoteChar,
				CSVNullRep:        cfg.Export.CSV.NullRepresentation,
				CSVHeader:         cfg.Export.CSV.Header,
				CSVLineTerminator: cfg.Export.CSV.LineTerminator,
				DBType:            cfg.DDL.TargetDialect,
			})
			results, err := exp.ExportTablesFromData(dataTables)
			if err != nil {
				return err
			}
			printExportResults(results)
			reportExportToStore(pw, results)
			return nil
		}

		// ── Offline XLSX mode ──
		if offlineXLSX {
			tables, err := exporter.DetectTablesFromXLSX(xlsxPath)
			if err != nil {
				return err
			}

			var dataTables []*exporter.DataTable
			for _, tbl := range tables {
				dt, err := exporter.ReadXLSXTable(xlsxPath, tbl)
				if err != nil {
					return fmt.Errorf("read %s.%s: %w", tbl.TableSchema, tbl.TableName, err)
				}
				dataTables = append(dataTables, dt)
			}

			exp := exporter.New(nil, exporter.Config{
				OutputDir:         outputDir,
				Format:            cfg.Export.Format,
				CSVDelimiter:      cfg.Export.CSV.Delimiter,
				CSVQuoteChar:      cfg.Export.CSV.QuoteChar,
				CSVNullRep:        cfg.Export.CSV.NullRepresentation,
				CSVHeader:         cfg.Export.CSV.Header,
				CSVLineTerminator: cfg.Export.CSV.LineTerminator,
				DBType:            cfg.DDL.TargetDialect,
			})
			results, err := exp.ExportTablesFromData(dataTables)
			if err != nil {
				return err
			}
			printExportResults(results)
			reportExportToStore(pw, results)
			return nil
		}

		// ── Online DB mode ──
		if cfg.Source.Type == "" {
			return fmt.Errorf(`no data source configured.
Use -d <dir> for offline CSV input,
   --xlsx <file> for offline XLSX input,
   or configure source.type and source.dsn in the config file.
Run 'owl-migrate init --scenario export' to generate a proper config.`)
		}

		sm, err := loadSchemaModel(cfg)
		if err != nil {
			return err
		}

		db, err := openDB(cfg.Source)
		if err != nil {
			return fmt.Errorf("connect to source: %w", err)
		}
		defer db.Close()

		pingCtx, pingCancel := context.WithTimeout(context.Background(), connectTimeout(cfg.Source))
		if err := db.PingContext(pingCtx); err != nil {
			pingCancel()
			return fmt.Errorf("ping source: %w", err)
		}
		pingCancel()
		fmt.Printf("Connected to %s\n", cfg.Source.Type)

		pkMap := buildPKMap(sm)

		logger := newLogger(cfg)
		defer logger.Sync()

		exp := exporter.New(db, exporter.Config{
			OutputDir:            outputDir,
			Format:               cfg.Export.Format,
			PageSize:             cfg.Export.Batch.PageSize,
			MaxWorkers:           cfg.Export.Parallel.MaxWorkers,
			CSVDelimiter:         cfg.Export.CSV.Delimiter,
			CSVQuoteChar:         cfg.Export.CSV.QuoteChar,
			CSVNullRep:           cfg.Export.CSV.NullRepresentation,
			CSVHeader:            cfg.Export.CSV.Header,
			CSVLineTerminator:    cfg.Export.CSV.LineTerminator,
			CSVEscapeChar:        cfg.Export.CSV.EscapeChar,
			CSVEncoding:          cfg.Export.CSV.Encoding,
			CSVNullOverrides:     cfg.Export.CSV.NullOverrides,
			CSVEmptyStringToNull: cfg.Export.CSV.EmptyStringToNull,
			DBType:               cfg.Source.Type,
			PlaceholderFamily:    placeholderFamilyFor(cfg.Source),
			Logger:               logger,
		})

		ctx := context.Background()
		if qt := queryTimeout(cfg.Source); qt > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, qt)
			defer cancel()
		}
		tables := filterTables(sm.GetTables(), cfg.Export.Tables.Include)
		results, err := exp.ExportTables(ctx, tables, pkMap)
		if err != nil {
			return err
		}
		printExportResults(results)
		reportExportToStore(pw, results)
		return nil
	}

	return cmd
}

func printExportResults(results []exporter.TableResult) {
	totalRows := int64(0)
	for _, r := range results {
		if r.Error != nil {
			fmt.Printf("  FAIL %s.%s: %v\n", r.Schema, r.Table, r.Error)
			continue
		}
		fmt.Printf("  %s.%s → %s (%d rows)\n",
			r.Schema, r.Table, r.OutputFile, r.Rows)
		totalRows += r.Rows
	}
	fmt.Printf("Exported %d rows across %d tables\n", totalRows, len(results))
}

// reportExportToStore writes per-table progress and the final job status to the
// shared store when running as a web worker.
func reportExportToStore(pw *service.ProgressWriter, results []exporter.TableResult) {
	if pw == nil {
		return
	}
	failed := false
	for _, r := range results {
		if r.Error != nil {
			failed = true
			pw.WriteTableError(r.Schema, r.Table, r.Error.Error())
		} else {
			pw.WriteExportComplete(r.Schema, r.Table, r.Rows)
		}
	}
	if failed {
		pw.SetJobFailed("one or more tables failed to export")
	} else {
		pw.SetJobCompleted()
	}
}
