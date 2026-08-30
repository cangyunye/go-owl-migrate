package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
	"github.com/cangyunye/go-owl-migrate/internal/service"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/importer"
)

func importCmd() *cobra.Command {
	var noQuote bool
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import CSV files into target database",
		Long:  `Reads CSV data files and inserts rows into the target database using batched INSERT with transaction control.`,
	}

	cmd.Flags().BoolVar(&noQuote, "no-quote-identifiers", false, "do not quote identifiers (bare names, for compatibility)")

	cmd.RunE = func(cmd *cobra.Command, args []string) (retErr error) {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if cmd.Flags().Changed("no-quote-identifiers") {
			cfg.DDL.NoQuoteIdentifiers = noQuote
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

		sm, err := loadSchemaModel(cfg)
		if err != nil {
			return err
		}

		db, err := openDB(cfg.Target)
		if err != nil {
			return fmt.Errorf("connect to target: %w", err)
		}
		defer db.Close()

		pingCtx, pingCancel := context.WithTimeout(context.Background(), connectTimeout(cfg.Target))
		if err := db.PingContext(pingCtx); err != nil {
			pingCancel()
			return fmt.Errorf("ping target: %w", err)
		}
		pingCancel()
		fmt.Printf("Connected to %s\n", cfg.Target.Type)

		if err := ensureTables(cmd.Context(), db, sm, cfg); err != nil {
			return fmt.Errorf("ensure target tables: %w", err)
		}

		logger := newLogger(cfg)
		defer logger.Sync()

		imp := importer.New(db, importer.Config{
			SourceDir:                    cfg.Import.SourceDir,
			CSVDelimiter:                 cfg.Import.CSV.Delimiter,
			CSVNullMarker:                cfg.Import.CSV.NullMarker,
			NullIf:                       cfg.Import.DataTransforms.NullIf,
			NullIdentifiers:              cfg.Import.CSV.NullIdentifiers.Strings,
			NullIdentifiersCaseSensitive: cfg.Import.CSV.NullIdentifiers.CaseSensitive,
			NullIdentifierRegex:          cfg.Import.CSV.NullIdentifiers.Regex,
			OracleEmptyStringIsNull:      cfg.Import.CSV.NullSemantics.OracleEmptyStringIsNull,
			NumericZeroNotNull:           cfg.Import.CSV.NullSemantics.NumericZeroNotNull,
			TruncateBefore:               cfg.Import.Target.TruncateBefore,
			DisableConstraints:           cfg.Import.Target.DisableConstraints,
			DisableTriggers:              cfg.Import.Target.DisableTriggers,
			DropIndexes:                  cfg.Import.Target.DropIndexes,
			IndexDDL: func(tbl *md.TableDef) ([]string, []string) {
				return indexDropRecreate(tbl, cfg.DDL.TargetDialect, toBuildOptions(cfg))
			},
			CommitInterval:           cfg.Import.Batch.CommitInterval,
			ErrorPolicy:              cfg.Import.Batch.ErrorPolicy,
			MaxErrors:                cfg.Import.Batch.MaxErrorsBeforeStop,
			UseCopy:                  cfg.Import.Batch.UseCopy,
			MaxWorkers:               cfg.Import.Parallel.MaxWorkers,
			RespectForeignKeys:       cfg.Import.Parallel.RespectForeignKeys,
			DateTimeFormat:           cfg.Import.DataTransforms.DatetimeFormat,
			DateTimeFormatFallback:   cfg.Import.DataTransforms.DatetimeFormatFallback,
			DateTimeTruncateToTarget: cfg.Import.DataTransforms.DatetimeTruncateToTarget,
			TrimStrings:              cfg.Import.DataTransforms.TrimStrings,
			SourceEncoding:           cfg.Import.DataTransforms.SourceEncoding,
			TargetDBType:             cfg.Target.Type,
			PlaceholderFamily:        placeholderFamilyFor(cfg.Target),
			Logger:                   logger,
			NoQuoteIdentifiers:       cfg.DDL.NoQuoteIdentifiers,
		})

		tables := sm.GetTables()
		ctx := context.Background()
		if qt := queryTimeout(cfg.Target); qt > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, qt)
			defer cancel()
		}
		results, err := imp.ImportTables(ctx, tables, cfg.DDL.SchemaMapping)
		if err != nil {
			return err
		}

		totalExpected := int64(0)
		totalActual := int64(0)
		totalSkipped := int64(0)
		failed := false
		for _, r := range results {
			if r.Err != nil {
				failed = true
				fmt.Printf("  FAIL %s.%s: %v\n", r.Schema, r.Table, r.Err)
				if pw != nil {
					pw.WriteTableError(r.Schema, r.Table, r.Err.Error())
				}
				continue
			}
			status := "✅"
			if r.Skipped > 0 || r.Errors > 0 {
				status = "⚠️"
			}
			fmt.Printf("  %s %s.%s: %d/%d rows (%d skipped, %v)\n",
				status, r.Schema, r.Table, r.Actual, r.Expected, r.Skipped, r.Duration)
			totalExpected += r.Expected
			totalActual += r.Actual
			totalSkipped += r.Skipped
			if pw != nil {
				pw.WriteImportComplete(r.Schema, r.Table, r.Actual, r.Skipped, "")
			}
		}
		fmt.Printf("Imported %d/%d rows across %d tables\n", totalActual, totalExpected, len(results))
		if totalSkipped > 0 {
			fmt.Printf("  ⚠️ %d rows skipped due to errors\n", totalSkipped)
		}
		if pw != nil {
			if failed {
				pw.SetJobFailed("one or more tables failed to import")
			} else {
				pw.SetJobCompleted()
			}
		}
		return nil
	}

	return cmd
}

func ensureTables(ctx context.Context, db *sql.DB, sm *md.SchemaModel, cfg *config.Config) error {
	for _, tbl := range sm.GetTables() {
		if err := ensureOneTable(ctx, db, tbl, cfg); err != nil {
			return err
		}
	}
	return nil
}

func ensureOneTable(ctx context.Context, db *sql.DB, tbl *md.TableDef, cfg *config.Config) error {
	schema := tbl.TableSchema
	if m, ok := cfg.DDL.SchemaMapping[schema]; ok {
		schema = m
	}

	exists, err := tableExists(ctx, db, cfg.Target.Type, schema, tbl.TableName,
		dbconn.OceanBaseOracleUsesMySQLWire(cfg.Target))
	if err == nil && exists {
		return nil
	}

	createSQL, cerr := buildCreateTableViaDialect(tbl, cfg)
	if cerr != nil {
		return fmt.Errorf("build create table %s.%s: %w", schema, tbl.TableName, cerr)
	}
	if createSQL == "" {
		return nil
	}

	if _, e := db.ExecContext(ctx, createSQL); e != nil {
		if alreadyExistsError(e) {
			return nil
		}
		return fmt.Errorf("create table %s.%s: %w (SQL: %s)", schema, tbl.TableName, e, createSQL)
	}
	fmt.Printf("  Created table %s.%s\n", schema, tbl.TableName)
	return nil
}

func alreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "ALREADY EXISTS") ||
		strings.Contains(msg, "ORA-00955") ||
		strings.Contains(msg, "NAME IS ALREADY USED")
}

func indexDropRecreate(tbl *md.TableDef, dialectName string, opts dialect.BuildOptions) ([]string, []string) {
	d, err := registry.Get(dialectName)
	if err != nil {
		return nil, nil
	}
	targetSchema := tbl.TableSchema
	if m, ok := opts.SchemaMapping[tbl.TableSchema]; ok {
		targetSchema = m
	}
	byName := make(map[string][]*md.IndexDef)
	var order []string
	opts.PreserveIdentifierCase = true
	isMySQL := strings.Contains(d.Name(), "mysql")
	for _, idx := range tbl.GetIndexes() {
		// MySQL primary keys are inline table constraints; DROP INDEX
		// PRIMARY / CREATE UNIQUE INDEX PRIMARY are invalid statements.
		if isMySQL && strings.EqualFold(idx.IndexName, "PRIMARY") {
			continue
		}
		if _, ok := byName[idx.IndexName]; !ok {
			order = append(order, idx.IndexName)
		}
		byName[idx.IndexName] = append(byName[idx.IndexName], idx)
	}
	quote := func(s string) string {
		if opts.NoQuoteIdentifiers {
			return s
		}
		if isMySQL {
			return "`" + strings.ReplaceAll(s, "`", "``") + "`"
		}
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	var drop, recreate []string
	for _, name := range order {
		if isMySQL {
			drop = append(drop, fmt.Sprintf("DROP INDEX %s ON %s.%s", quote(name), quote(targetSchema), quote(tbl.TableName)))
		} else {
			drop = append(drop, fmt.Sprintf("DROP INDEX %s.%s", quote(targetSchema), quote(name)))
		}
		if ddl, err := d.BuildCreateIndex(byName[name], opts); err == nil && ddl != "" {
			recreate = append(recreate, ddl)
		}
	}
	return drop, recreate
}
