package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
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

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if cmd.Flags().Changed("no-quote-identifiers") {
			cfg.DDL.NoQuoteIdentifiers = noQuote
		}

		sm, err := loadSchemaModel(cfg)
		if err != nil {
			return err
		}

		db, err := openDB(cfg.Target.Type, cfg.Target.DSN)
		if err != nil {
			return fmt.Errorf("connect to target: %w", err)
		}
		defer db.Close()

		if err := db.Ping(); err != nil {
			return fmt.Errorf("ping target: %w", err)
		}
		fmt.Printf("Connected to %s\n", cfg.Target.Type)

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
			SourceEncoding: cfg.Import.DataTransforms.SourceEncoding,
			TargetDBType:   cfg.Target.Type,
			Logger:         logger,
			NoQuoteIdentifiers: cfg.DDL.NoQuoteIdentifiers,
		})

		tables := sm.GetTables()
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
	targetType := registry.Normalize(strings.ToLower(cfg.Target.Type))
	targetIsMySQL := targetType == "mysql" || strings.HasSuffix(targetType, "-mysql")

	for _, tbl := range sm.GetTables() {
		schema := tbl.TableSchema
		if m, ok := schemaMapping[schema]; ok {
			schema = m
		}

		var count int
		checkSQL := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2"
		if targetIsMySQL {
			checkSQL = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?"
		}

		err := db.QueryRowContext(ctx, checkSQL, schema, tbl.TableName).Scan(&count)

		if err != nil {
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

	targetType := registry.Normalize(strings.ToLower(cfg.Target.Type))
	targetIsMySQL := strings.HasSuffix(targetType, "-mysql")
	targetIsOracle := strings.HasSuffix(targetType, "-oracle")

	if cfg.DDL.IncludeIfNotExists && !targetIsOracle {
		b.WriteString("IF NOT EXISTS ")
	}

	q := func(name string) string {
		if cfg.DDL.NoQuoteIdentifiers {
			return name
		}
		if targetIsMySQL {
			return "`" + name + "`"
		}
		return `"` + name + `"`
	}

	b.WriteString(fmt.Sprintf("%s.%s", q(schema), q(tbl.TableName)))
	b.WriteString(" (\n")
	cols := tbl.GetColumns()

	// Oracle-specific type map
	oracleMap := map[string]string{
		"INT":               "NUMBER(10)",
		"INTEGER":           "NUMBER(10)",
		"BIGINT":            "NUMBER(19)",
		"SMALLINT":          "NUMBER(5)",
		"BOOLEAN":           "NUMBER(1)",
		"REAL":              "BINARY_FLOAT",
		"DOUBLE PRECISION":  "BINARY_DOUBLE",
		"TEXT":              "CLOB",
		"CLOB":              "CLOB",
		"BLOB":              "BLOB",
		"BYTEA":             "BLOB",
		"JSON":              "CLOB",
		"JSONB":             "CLOB",
		"XML":               "XMLTYPE",
		"TIMESTAMP":         "TIMESTAMP",
		"TIMESTAMPTZ":       "TIMESTAMP WITH TIME ZONE",
		"VARCHAR":           "VARCHAR2",
		"CHARACTER VARYING": "VARCHAR2",
		"DECIMAL":           "NUMBER",
		"NUMERIC":           "NUMBER",
		"NUMBER":            "NUMBER",
		"FLOAT":             "BINARY_FLOAT",
		"DOUBLE":            "BINARY_DOUBLE",
	}

	// MySQL-specific type map (also serves as general cross-dialect)
	mysqlMap := map[string]string{
		"INT":               "INTEGER",
		"INTEGER":           "INTEGER",
		"VARCHAR":           "VARCHAR",
		"CHARACTER VARYING": "VARCHAR",
		"VARCHAR2":          "VARCHAR",
		"CHAR":              "CHAR",
		"CHARACTER":         "CHAR",
		"DECIMAL":           "DECIMAL",
		"NUMERIC":           "DECIMAL",
		"NUMBER":            "DECIMAL",
		"DATE":              "DATE",
		"TIMESTAMP":         "DATETIME",
		"TIMESTAMPTZ":       "DATETIME",
		"BIGINT":            "BIGINT",
		"SMALLINT":          "SMALLINT",
		"BOOLEAN":           "TINYINT(1)",
		"REAL":              "FLOAT",
		"DOUBLE PRECISION":  "DOUBLE",
		"TEXT":              "LONGTEXT",
		"CLOB":              "LONGTEXT",
		"BLOB":              "LONGBLOB",
		"BYTEA":             "LONGBLOB",
		"JSON":              "JSON",
		"JSONB":             "JSON",
		"XML":               "LONGTEXT",
	}

	// PG target keeps source types mostly as-is
	pgMap := map[string]string{
		"VARCHAR2": "VARCHAR",
		"NUMBER":   "NUMERIC",
		"BOOLEAN":  "BOOLEAN",
		"CLOB":     "TEXT",
		"BLOB":     "BYTEA",
		"JSON":     "JSONB",
		"XML":      "XML",
	}

	pks := tbl.GetPrimaryKeys()

	for i, col := range cols {
		b.WriteString("  ")
		b.WriteString(q(col.ColumnName))
		b.WriteString(" ")

		upType := normalizeColumnType(col.DataType)
		var targetType string

		if targetIsOracle {
			if m, ok := oracleMap[upType]; ok {
				targetType = m
			} else {
				targetType = col.DataType
			}
		} else if targetIsMySQL {
			if m, ok := mysqlMap[upType]; ok {
				targetType = m
			} else {
				targetType = col.DataType
			}
		} else {
			if m, ok := pgMap[upType]; ok {
				targetType = m
			} else {
				targetType = col.DataType
			}
		}

		// Handle VARCHAR lengths
		if col.DataLength > 0 {
			if targetIsOracle {
				if upType == "VARCHAR" || upType == "VARCHAR2" || upType == "CHARACTER VARYING" || upType == "CHARACTER" {
					targetType = fmt.Sprintf("VARCHAR2(%d)", col.DataLength)
				} else if upType == "CHAR" || upType == "CHARACTER" {
					targetType = fmt.Sprintf("CHAR(%d)", col.DataLength)
				}
			} else {
				if upType == "VARCHAR" || upType == "VARCHAR2" || upType == "CHARACTER VARYING" || upType == "CHARACTER" {
					targetType = fmt.Sprintf("VARCHAR(%d)", col.DataLength)
				} else if upType == "CHAR" || upType == "CHARACTER" {
					targetType = fmt.Sprintf("CHAR(%d)", col.DataLength)
				}
			}
		} else {
			// Provide default length where required
			if targetIsMySQL && (upType == "VARCHAR" || upType == "VARCHAR2" || upType == "CHARACTER VARYING") {
				targetType = "VARCHAR(255)"
			}
			if targetIsMySQL && upType == "CHARACTER" {
				targetType = "CHAR(255)"
			}
		}

		// Handle NUMBER/DECIMAL with precision/scale
		isNumeric := upType == "DECIMAL" || upType == "NUMERIC" || upType == "NUMBER"
		if isNumeric && col.DataPrecision > 0 {
			if col.DataScale > 0 {
				if targetIsOracle {
					targetType = fmt.Sprintf("NUMBER(%d,%d)", col.DataPrecision, col.DataScale)
				} else {
					targetType = fmt.Sprintf("DECIMAL(%d,%d)", col.DataPrecision, col.DataScale)
				}
			} else {
				if targetIsOracle {
					targetType = fmt.Sprintf("NUMBER(%d)", col.DataPrecision)
				} else {
					targetType = fmt.Sprintf("DECIMAL(%d)", col.DataPrecision)
				}
			}
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

func normalizeColumnType(dataType string) string {
	t := strings.ToUpper(strings.TrimSpace(dataType))
	t = strings.Join(strings.Fields(t), " ")
	switch t {
	case "TIMESTAMP WITHOUT TIME ZONE":
		return "TIMESTAMP"
	case "TIMESTAMP WITH TIME ZONE":
		return "TIMESTAMPTZ"
	case "CHARACTER VARYING":
		return "VARCHAR"
	case "DOUBLE PRECISION":
		return "DOUBLE PRECISION"
	case "USER-DEFINED":
		return "TEXT"
	default:
		return t
	}
}
