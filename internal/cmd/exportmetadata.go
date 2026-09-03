package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/xuri/excelize/v2"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func exportMetadataCmd() *cobra.Command {
	var (
		outputDir  string
		format     string
		scope      string
		objectsRaw string
	)

	cmd := &cobra.Command{
		Use:   "export-metadata",
		Short: "Export metadata from a live database to CSV, XLSX, or SQL",
		Long: `Connects to the source database configured in config and exports
metadata (tables, columns, indexes, etc.) to the specified format.

Formats:
  csv   — separate CSV files per metadata type (default)
  xlsx  — single Excel workbook with one sheet per metadata type
  sql   — INSERT statements targeting system metadata tables

Scope options:
  all           — export the configured schema (default)
  schema:NAME   — export a specific schema
  table:T1,T2   — export specific tables from the configured schema

Examples:
  owl-migrate export-metadata -c config.yaml -o ./metadata/ --format csv --scope all
  owl-migrate export-metadata -c config.yaml -o ./schema.xlsx --format xlsx --schema SCOTT
  owl-migrate export-metadata -c config.yaml -o ./meta.sql --format sql`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if cfg.Metadata.Type != "database" && cfg.Source.DSN == "" {
				return fmt.Errorf("source.dsn is required for metadata export")
			}

			// 统一范围解析：all | schema:NAME | table:GLOB[,GLOB] | schema:NAME:table:GLOB[,GLOB]
			extractSchema, patterns, err := service.ParseMetadataExportScope(scope, cfg.Source.Schema)
			if err != nil {
				return err
			}

			var objects md.ObjectSet
			if objectsRaw != "" {
				if objects, err = md.ParseObjectTypes(objectsRaw); err != nil {
					return err
				}
				// 能力校验（ADR-004）：不支持的对象在 CLI 直接报错并列出支持清单
				caps := extractor.Capabilities(cfg.Source.Type)
				var unsupported []string
				for _, o := range md.AllObjectTypes() {
					if objects.Contains(o) && !caps.Contains(o) {
						unsupported = append(unsupported, string(o))
					}
				}
				if len(unsupported) > 0 {
					return fmt.Errorf("source %q does not support object type(s): %s (supported: %s)",
						cfg.Source.Type, strings.Join(unsupported, ","), func() string {
							var a []string
							for _, o := range md.AllObjectTypes() {
								a = append(a, string(o))
							}
							return strings.Join(a, ",")
						}())
				}
			}
			if format == "sql" && service.TargetTypeFamily(cfg.Source.Type) != "oracle" {
				return fmt.Errorf("format sql 仅 oracle 家族有意义（当前 source=%s）；请使用 csv", cfg.Source.Type)
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
			fmt.Printf("Connected to %s, schema: %s\n", cfg.Source.Type, extractSchema)

			sm, err := extractor.Extract(db, dbconn.MetadataSourceType(config.DBConfig{Type: cfg.Source.Type, DSN: cfg.Source.DSN}), extractSchema)
			if err != nil {
				return fmt.Errorf("extract metadata: %w", err)
			}

			// 按范围选择模型（附随随表；独立对象仅整 schema 范围，ADR-002）
			if len(patterns) > 0 {
				if sm, err = (md.ObjectSelector{Schemas: patterns}).Select(sm); err != nil {
					return err
				}
			}
			tableCount := len(sm.GetTables())
			fmt.Printf("Exporting %d tables\n", tableCount)

			switch format {
			case "xlsx":
				return exportMetadataXLSX(outputDir, sm, sm.GetTables(), extractSchema)
			case "sql":
				return exportMetadataSQL(outputDir, cfg.Source.Type, sm, sm.GetTables(), extractSchema)
			default:
				files, err := service.ExportMetadataFiles(outputDir, sm, objects)
				if err != nil {
					return fmt.Errorf("export metadata csv: %w", err)
				}
				for _, f := range files {
					fmt.Printf("  %s\n", f)
				}
				fmt.Printf("Metadata exported to %s/ (%d files)\n", outputDir, len(files))
				return nil
			}
		},
	}
	cmd.Flags().StringVarP(&outputDir, "output", "o", "./output/metadata/", "output directory (CSV) or file path (XLSX/SQL)")
	cmd.Flags().StringVar(&format, "format", "csv", "output format: csv, xlsx, sql")
	cmd.Flags().StringVar(&scope, "scope", "all", "export scope: all | schema:NAME | table:GLOB[,GLOB] | schema:NAME:table:GLOB[,GLOB]")
	cmd.Flags().StringVar(&objectsRaw, "objects", "", "metadata object types to export, comma separated (default: all)")

	return cmd
}

// ── XLSX export ──

func exportMetadataXLSX(path string, sm *md.SchemaModel, tables []*md.TableDef, schema string) error {
	f := excelize.NewFile()
	defer f.Close()

	// tables sheet
	f.SetSheetName("Sheet1", "tables")
	f.SetCellValue("tables", "A1", "TABLE_SCHEMA")
	f.SetCellValue("tables", "B1", "TABLE_NAME")
	f.SetCellValue("tables", "C1", "TABLE_TYPE")
	f.SetCellValue("tables", "D1", "TABLE_COMMENT")
	for i, tbl := range tables {
		r := i + 2
		f.SetCellValue("tables", fmt.Sprintf("A%d", r), tbl.TableSchema)
		f.SetCellValue("tables", fmt.Sprintf("B%d", r), tbl.TableName)
		f.SetCellValue("tables", fmt.Sprintf("C%d", r), tbl.TableType)
		f.SetCellValue("tables", fmt.Sprintf("D%d", r), tbl.TableComment)
	}

	// columns sheet
	idx, err := f.NewSheet("columns")
	if err != nil {
		return fmt.Errorf("create columns sheet: %w", err)
	}
	f.SetActiveSheet(idx)
	f.SetCellValue("columns", "A1", "TABLE_SCHEMA")
	f.SetCellValue("columns", "B1", "TABLE_NAME")
	f.SetCellValue("columns", "C1", "COLUMN_NAME")
	f.SetCellValue("columns", "D1", "ORDINAL_POSITION")
	f.SetCellValue("columns", "E1", "DATA_TYPE")
	f.SetCellValue("columns", "F1", "NULLABLE")
	row := 2
	for _, tbl := range tables {
		for _, col := range tbl.GetColumns() {
			f.SetCellValue("columns", fmt.Sprintf("A%d", row), col.TableSchema)
			f.SetCellValue("columns", fmt.Sprintf("B%d", row), col.TableName)
			f.SetCellValue("columns", fmt.Sprintf("C%d", row), col.ColumnName)
			f.SetCellValue("columns", fmt.Sprintf("D%d", row), col.OrdinalPosition)
			f.SetCellValue("columns", fmt.Sprintf("E%d", row), col.DataType)
			f.SetCellValue("columns", fmt.Sprintf("F%d", row), col.Nullable)
			row++
		}
	}

	// primary_keys sheet
	idx, err = f.NewSheet("primary_keys")
	if err != nil {
		return fmt.Errorf("create primary_keys sheet: %w", err)
	}
	f.SetActiveSheet(idx)
	f.SetCellValue("primary_keys", "A1", "TABLE_SCHEMA")
	f.SetCellValue("primary_keys", "B1", "TABLE_NAME")
	f.SetCellValue("primary_keys", "C1", "CONSTRAINT_NAME")
	f.SetCellValue("primary_keys", "D1", "COLUMN_NAME")
	row = 2
	for _, tbl := range tables {
		for _, pk := range tbl.GetPrimaryKeys() {
			f.SetCellValue("primary_keys", fmt.Sprintf("A%d", row), pk.TableSchema)
			f.SetCellValue("primary_keys", fmt.Sprintf("B%d", row), pk.TableName)
			f.SetCellValue("primary_keys", fmt.Sprintf("C%d", row), pk.ConstraintName)
			f.SetCellValue("primary_keys", fmt.Sprintf("D%d", row), pk.ColumnName)
			row++
		}
	}

	if err := f.SaveAs(path); err != nil {
		return fmt.Errorf("save xlsx %q: %w", path, err)
	}
	fmt.Printf("Metadata exported to %s\n", path)
	return nil
}

// ── SQL export ──

func exportMetadataSQL(path string, dbType string, sm *md.SchemaModel, tables []*md.TableDef, schema string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	dialect := strings.ToLower(dbType)
	q := func(s string) string {
		if dialect == "mysql" {
			return "`" + s + "`"
		}
		return `"` + s + `"`
	}

	fmt.Fprintf(f, "-- Metadata export for schema %s\n", schema)
	fmt.Fprintf(f, "-- Dialect: %s\n", dbType)
	fmt.Fprintf(f, "-- Generated by owl-migrate export-metadata\n\n")

	// Tables (dba_tables)
	for _, tbl := range tables {
		tableOwner := tbl.Owner
		if tableOwner == "" {
			tableOwner = schema
		}
		fmt.Fprintf(f, "INSERT INTO dba_tables (%s, %s, %s) VALUES (%s, %s, %s);\n",
			q("OWNER"), q("TABLE_NAME"), q("TABLE_TYPE"),
			q(tableOwner), q(tbl.TableName), q(tbl.TableType))
	}

	// Columns (dba_tab_columns)
	for _, tbl := range tables {
		for _, col := range tbl.GetColumns() {
			nullable := "Y"
			if col.Nullable == "NO" {
				nullable = "N"
			}
			fmt.Fprintf(f, "INSERT INTO dba_tab_columns (%s, %s, %s, %s, %s, %s) VALUES (%s, %s, %s, %d, %s, %s);\n",
				q("OWNER"), q("TABLE_NAME"), q("COLUMN_NAME"),
				q("DATA_TYPE"), q("DATA_LENGTH"), q("NULLABLE"),
				q(schema), q(tbl.TableName), q(col.ColumnName),
				col.DataLength, q(col.DataType), q(nullable))
		}
	}

	// Primary keys (dba_constraints + dba_cons_columns)
	for _, tbl := range tables {
		for _, pk := range tbl.GetPrimaryKeys() {
			fmt.Fprintf(f, "INSERT INTO dba_constraints (%s, %s, %s, %s, %s) VALUES (%s, %s, %s, 'P', 'ENABLED');\n",
				q("OWNER"), q("TABLE_NAME"), q("CONSTRAINT_NAME"), q("CONSTRAINT_TYPE"), q("STATUS"),
				q(schema), q(tbl.TableName), q(pk.ConstraintName))
			fmt.Fprintf(f, "INSERT INTO dba_cons_columns (%s, %s, %s, %s, %s) VALUES (%s, %s, %s, %s, %d);\n",
				q("OWNER"), q("CONSTRAINT_NAME"), q("TABLE_NAME"), q("COLUMN_NAME"), q("COLUMN_POSITION"),
				q(schema), q(pk.ConstraintName), q(tbl.TableName), q(pk.ColumnName), pk.OrdinalPosition)
		}
	}

	// Indexes (dba_indexes + dba_ind_columns)
	for _, tbl := range tables {
		for _, idx := range tbl.GetIndexes() {
			fmt.Fprintf(f, "INSERT INTO dba_indexes (%s, %s, %s, %s, %s) VALUES (%s, %s, %s, %s, %s);\n",
				q("OWNER"), q("INDEX_NAME"), q("TABLE_NAME"), q("UNIQUENESS"), q("INDEX_TYPE"),
				q(schema), q(idx.IndexName), q(tbl.TableName), q(idx.Uniqueness), q(idx.IndexType))
			fmt.Fprintf(f, "INSERT INTO dba_ind_columns (%s, %s, %s, %s, %s) VALUES (%s, %s, %s, %s, %d);\n",
				q("INDEX_OWNER"), q("INDEX_NAME"), q("TABLE_NAME"), q("COLUMN_NAME"), q("COLUMN_POSITION"),
				q(schema), q(idx.IndexName), q(tbl.TableName), q(idx.ColumnName), idx.OrdinalPosition)
		}
	}

	// Foreign keys (dba_constraints FK type)
	for _, tbl := range tables {
		for _, fk := range tbl.GetForeignKeys() {
			fmt.Fprintf(f, "INSERT INTO dba_constraints (%s, %s, %s, %s, %s, %s) VALUES (%s, %s, %s, 'R', %s, %s);\n",
				q("OWNER"), q("TABLE_NAME"), q("CONSTRAINT_NAME"), q("CONSTRAINT_TYPE"),
				q("R_OWNER"), q("DELETE_RULE"),
				q(schema), q(tbl.TableName), q(fk.ConstraintName),
				q(fk.RefSchema), q(fk.DeleteRule))
		}
	}

	// Views (dba_views)
	for _, v := range sm.GetViews() {
		fmt.Fprintf(f, "INSERT INTO dba_views (%s, %s, %s) VALUES (%s, %s, %s);\n",
			q("OWNER"), q("VIEW_NAME"), q("TEXT"),
			q(v.ViewSchema), q(v.ViewName), q(v.ViewDefinition))
	}

	// Sequences (dba_sequences)
	for _, seq := range sm.GetSequences(schema) {
		fmt.Fprintf(f, "INSERT INTO dba_sequences (%s, %s, %s, %s, %s) VALUES (%s, %s, %d, %d, %d);\n",
			q("SEQUENCE_OWNER"), q("SEQUENCE_NAME"), q("MIN_VALUE"), q("MAX_VALUE"), q("INCREMENT_BY"),
			q(schema), q(seq.SequenceName), seq.MinValue, seq.MaxValue, seq.IncrementBy)
	}

	// Triggers (dba_triggers)
	for _, tbl := range tables {
		for _, trg := range sm.GetTriggers(tbl.TableSchema, tbl.TableName) {
			fmt.Fprintf(f, "INSERT INTO dba_triggers (%s, %s, %s, %s, %s, %s) VALUES (%s, %s, %s, %s, %s, %s);\n",
				q("OWNER"), q("TRIGGER_NAME"), q("TABLE_NAME"), q("TRIGGER_TYPE"),
				q("TRIGGERING_EVENT"), q("STATUS"),
				q(trg.TriggerSchema), q(trg.TriggerName), q(trg.TableName),
				q(trg.TriggerType), q(trg.TriggerEvent), q(trg.Status))
		}
	}

	// Synonyms (dba_synonyms)
	for _, syn := range sm.GetSynonyms(schema) {
		fmt.Fprintf(f, "INSERT INTO dba_synonyms (%s, %s, %s, %s) VALUES (%s, %s, %s, %s);\n",
			q("SYNONYM_NAME"), q("TABLE_OWNER"), q("TABLE_NAME"), q("OWNER"),
			q(syn.SynonymName), q(syn.TargetSchema), q(syn.TargetName), q(syn.SynonymSchema))
	}

	fmt.Fprintf(f, "\n-- %d tables, %d views, %d sequences, %d synonyms exported\n",
		len(tables), len(sm.GetViews()), len(sm.GetSequences(schema)), len(sm.GetSynonyms(schema)))
	fmt.Printf("Metadata exported to %s\n", path)
	return nil
}
