package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/importer"
)

func importCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import CSV files into target database",
		Long:  `Reads CSV data files and inserts rows into the target database using batched INSERT with transaction control.`,
	}

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

		// Connect to target database
		db, err := openDB(cfg.Target.Type, cfg.Target.DSN)
		if err != nil {
			return fmt.Errorf("connect to target: %w", err)
		}
		defer db.Close()

		if err := db.Ping(); err != nil {
			return fmt.Errorf("ping target: %w", err)
		}
		fmt.Printf("Connected to %s\n", cfg.Target.Type)

		// Ensure target tables exist (create basic structure from metadata)
		if err := ensureTables(cmd.Context(), db, sm, cfg, cfg.DDL.SchemaMapping); err != nil {
			return fmt.Errorf("ensure target tables: %w", err)
		}

		logCfg := zap.NewDevelopmentConfig()
		logCfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
		if cfg.General.LogLevel == "debug" {
			logCfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
		}
		logger, _ := logCfg.Build()
		defer logger.Sync()

		imp := importer.New(db, importer.Config{
			SourceDir:      cfg.Import.SourceDir,
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
		Logger:         logger,
		})

		tables := sm.GetTables() // All tables from metadata
		ctx := context.Background()
		results, err := imp.ImportTables(ctx, tables, cfg.DDL.SchemaMapping)
		if err != nil {
			return err
		}

		totalExpected := int64(0)
		totalActual := int64(0)
		totalSkipped := int64(0)
		for _, r := range results {
			if r.Err != nil {
				fmt.Printf("  FAIL %s.%s: %v\n", r.Schema, r.Table, r.Err)
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
		}
		fmt.Printf("Imported %d/%d rows across %d tables\n", totalActual, totalExpected, len(results))
		if totalSkipped > 0 {
			fmt.Printf("  ⚠️ %d rows skipped due to errors\n", totalSkipped)
		}
		return nil
	}

	return cmd
}

func ensureTables(ctx context.Context, db *sql.DB, sm *md.SchemaModel, cfg *config.Config, schemaMapping map[string]string) error {
	for _, tbl := range sm.GetTables() {
		schema := tbl.TableSchema
		if m, ok := schemaMapping[schema]; ok {
			schema = m
		}

		// Check if table exists
		var count int
		checkSQL := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2"
		if cfg.Target.Type == "mysql" {
			checkSQL = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?"
		}

		var err error
		if cfg.Target.Type == "mysql" {
			err = db.QueryRowContext(ctx, checkSQL, schema, tbl.TableName).Scan(&count)
		} else {
			err = db.QueryRowContext(ctx, checkSQL, schema, tbl.TableName).Scan(&count)
		}

		if err != nil {
			// Table likely doesn't exist — try to create it
			createSQL := buildCreateTableSQL(tbl, schema, cfg)
			if createSQL != "" {
				if _, err := db.ExecContext(ctx, createSQL); err != nil {
					return fmt.Errorf("create table %s.%s: %w (SQL: %s)", schema, tbl.TableName, err, createSQL)
				}
				fmt.Printf("  Created table %s.%s\n", schema, tbl.TableName)
			}
		} else if count == 0 {
			createSQL := buildCreateTableSQL(tbl, schema, cfg)
			if createSQL != "" {
				if _, err := db.ExecContext(ctx, createSQL); err != nil {
					return fmt.Errorf("create table %s.%s: %w", schema, tbl.TableName, err)
				}
				fmt.Printf("  Created table %s.%s\n", schema, tbl.TableName)
			}
		}
	}
	return nil
}

func buildCreateTableSQL(tbl *md.TableDef, schema string, cfg *config.Config) string {
	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	if cfg.DDL.IncludeIfNotExists {
		b.WriteString("IF NOT EXISTS ")
	}

	// MySQL uses backticks, others use double quotes
	targetIsMySQL := strings.EqualFold(cfg.Target.Type, "mysql")
	q := func(name string) string {
		if targetIsMySQL {
			return "`" + name + "`"
		}
		return `"` + name + `"`
	}

	b.WriteString(fmt.Sprintf("%s.%s", q(schema), q(tbl.TableName)))
	b.WriteString(" (\n")
	cols := tbl.GetColumns()

	// Extended type map for cross-dialect compatibility
	typeMap := map[string]string{
		"INT":                           "INTEGER",
		"INTEGER":                       "INTEGER",
		"VARCHAR":                       "VARCHAR",
		"CHARACTER VARYING":             "VARCHAR",
		"VARCHAR2":                      "VARCHAR",
		"CHAR":                          "CHAR",
		"CHARACTER":                     "CHAR",
		"DECIMAL":                       "DECIMAL",
		"NUMERIC":                       "DECIMAL",
		"NUMBER":                        "DECIMAL",
		"DATE":                          "DATE",
		"TIMESTAMP":                     "DATETIME",
		"TIMESTAMPTZ":                   "DATETIME",
		"BIGINT":                        "BIGINT",
		"SMALLINT":                      "SMALLINT",
		"BOOLEAN":                       "TINYINT(1)",
		"REAL":                          "FLOAT",
		"DOUBLE PRECISION":              "DOUBLE",
		"TEXT":                          "LONGTEXT",
		"CLOB":                          "LONGTEXT",
		"BLOB":                          "LONGBLOB",
		"BYTEA":                         "LONGBLOB",
		"JSON":                          "JSON",
		"JSONB":                         "JSON",
		"XML":                           "LONGTEXT",
	}

	// Build PK column set for inline PRIMARY KEY
	pks := tbl.GetPrimaryKeys()
	pkSet := make(map[string]bool, len(pks))
	for _, pk := range pks {
		pkSet[strings.ToUpper(pk.ColumnName)] = true
	}

	for i, col := range cols {
		b.WriteString("  ")
		b.WriteString(q(col.ColumnName))
		b.WriteString(" ")
		targetType := col.DataType
		if m, ok := typeMap[strings.ToUpper(col.DataType)]; ok {
			targetType = m
		}

		// Handle VARCHAR types: respect length if set, provide default for MySQL
		upType := strings.ToUpper(col.DataType)
		if upType == "VARCHAR" || upType == "VARCHAR2" || upType == "CHARACTER VARYING" || upType == "CHARACTER" {
			if col.DataLength > 0 {
				targetType = fmt.Sprintf("VARCHAR(%d)", col.DataLength)
			} else if targetIsMySQL {
				targetType = "VARCHAR(255)"
			}
		}
		if (upType == "DECIMAL" || upType == "NUMERIC" || upType == "NUMBER") && col.DataPrecision > 0 && col.DataScale > 0 {
			targetType = fmt.Sprintf("DECIMAL(%d,%d)", col.DataPrecision, col.DataScale)
		}
		b.WriteString(targetType)
		if col.Nullable == "NO" {
			b.WriteString(" NOT NULL")
		}
		if i < len(cols)-1 || len(pks) > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}

	// Inline PRIMARY KEY (avoids duplicate PK error when table already exists)
	if len(pks) > 0 {
		pkNames := make([]string, len(pks))
		for i, pk := range pks {
			pkNames[i] = q(pk.ColumnName)
		}
		b.WriteString(fmt.Sprintf("  PRIMARY KEY (%s)\n", strings.Join(pkNames, ", ")))
	}

	b.WriteString(")")
	return b.String()
}
