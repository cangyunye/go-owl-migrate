package cmd

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/xuri/excelize/v2"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func exportMetadataCmd() *cobra.Command {
	var (
		outputDir string
		format    string
		scope     string
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

			// Determine schema and table filters from scope
			targetSchema := cfg.Source.Schema
			var tableFilter []string

			if scope != "" && scope != "all" {
				if strings.HasPrefix(scope, "schema:") {
					targetSchema = strings.TrimPrefix(scope, "schema:")
				} else if strings.HasPrefix(scope, "table:") {
					tables := strings.TrimPrefix(scope, "table:")
					tableFilter = strings.Split(tables, ",")
				} else {
					return fmt.Errorf("invalid scope %q: use all, schema:NAME, or table:T1,T2", scope)
				}
			}

			if targetSchema == "" {
				return fmt.Errorf("no schema specified (set source.schema or use --scope schema:NAME)")
			}

			// Connect and extract metadata
			db, err := openDB(cfg.Source.Type, cfg.Source.DSN)
			if err != nil {
				return fmt.Errorf("connect to source: %w", err)
			}
			defer db.Close()

			if err := db.Ping(); err != nil {
				return fmt.Errorf("ping source: %w", err)
			}
			fmt.Printf("Connected to %s, schema: %s\n", cfg.Source.Type, targetSchema)

			sm, err := extractor.Extract(db, cfg.Source.Type, targetSchema)
			if err != nil {
				return fmt.Errorf("extract metadata: %w", err)
			}

			// Filter tables if needed
			tables := sm.GetTables()
			if len(tableFilter) > 0 {
				filterSet := make(map[string]bool)
				for _, t := range tableFilter {
					filterSet[strings.TrimSpace(t)] = true
				}
				var filtered []*md.TableDef
				for _, tbl := range tables {
					if filterSet[tbl.TableName] {
						filtered = append(filtered, tbl)
					}
				}
				tables = filtered
			}

			fmt.Printf("Exporting %d tables\n", len(tables))

			switch format {
			case "xlsx":
				return exportMetadataXLSX(outputDir, sm, tables, targetSchema)
			case "sql":
				return exportMetadataSQL(outputDir, cfg.Source.Type, sm, tables, targetSchema)
			default:
				return exportMetadataCSV(outputDir, sm, tables, targetSchema)
			}
		},
	}

	cmd.Flags().StringVarP(&outputDir, "output", "o", "./output/metadata/", "output directory (CSV) or file path (XLSX/SQL)")
	cmd.Flags().StringVar(&format, "format", "csv", "output format: csv, xlsx, sql")
	cmd.Flags().StringVar(&scope, "scope", "all", "export scope: all, schema:NAME, or table:T1,T2")

	return cmd
}

// ── CSV export ──

func exportMetadataCSV(dir string, sm *md.SchemaModel, tables []*md.TableDef, schema string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// tables.csv
	if err := writeCSV(filepath.Join(dir, "tables.csv"), [][]string{
		{"TABLE_SCHEMA", "TABLE_NAME", "TABLE_TYPE", "TABLE_COMMENT"},
	}, func() [][]string {
		var rows [][]string
		for _, tbl := range tables {
			rows = append(rows, []string{tbl.TableSchema, tbl.TableName, tbl.TableType, tbl.TableComment})
		}
		return rows
	}()); err != nil {
		return err
	}

	// columns.csv
	var colRows [][]string
	for _, tbl := range tables {
		for _, col := range tbl.GetColumns() {
			colRows = append(colRows, []string{
				col.TableSchema, col.TableName, col.ColumnName,
				fmt.Sprintf("%d", col.OrdinalPosition), col.DataType,
				fmt.Sprintf("%d", col.DataLength), fmt.Sprintf("%d", col.DataPrecision),
				fmt.Sprintf("%d", col.DataScale), col.Nullable, col.DefaultValue, col.ColumnComment,
			})
		}
	}
	if err := writeCSV(filepath.Join(dir, "columns.csv"), [][]string{
		{"TABLE_SCHEMA", "TABLE_NAME", "COLUMN_NAME", "ORDINAL_POSITION", "DATA_TYPE",
			"DATA_LENGTH", "DATA_PRECISION", "DATA_SCALE", "NULLABLE", "DEFAULT_VALUE", "COLUMN_COMMENT"},
	}, colRows); err != nil {
		return err
	}

	// primary_keys.csv
	var pkRows [][]string
	for _, tbl := range tables {
		for _, pk := range tbl.GetPrimaryKeys() {
			pkRows = append(pkRows, []string{
				pk.TableSchema, pk.TableName, pk.ConstraintName, pk.ColumnName,
				fmt.Sprintf("%d", pk.OrdinalPosition),
			})
		}
	}
	if err := writeCSV(filepath.Join(dir, "primary_keys.csv"), [][]string{
		{"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "COLUMN_NAME", "ORDINAL_POSITION"},
	}, pkRows); err != nil {
		return err
	}

	// indexes.csv
	var idxRows [][]string
	for _, tbl := range tables {
		for _, idx := range tbl.GetIndexes() {
			idxRows = append(idxRows, []string{
				idx.TableSchema, idx.TableName, idx.IndexName, idx.IndexType,
				idx.Uniqueness, idx.ColumnName, fmt.Sprintf("%d", idx.OrdinalPosition),
				idx.Expression,
			})
		}
	}
	if err := writeCSV(filepath.Join(dir, "indexes.csv"), [][]string{
		{"TABLE_SCHEMA", "TABLE_NAME", "INDEX_NAME", "INDEX_TYPE", "UNIQUENESS",
			"COLUMN_NAME", "ORDINAL_POSITION", "EXPRESSION"},
	}, idxRows); err != nil {
		return err
	}

	// foreign_keys.csv
	var fkRows [][]string
	for _, tbl := range tables {
		for _, fk := range tbl.GetForeignKeys() {
			fkRows = append(fkRows, []string{
				fk.ConstraintName, fk.TableSchema, fk.TableName, fk.ColumnName,
				fk.RefSchema, fk.RefTable, fk.RefColumn, fk.DeleteRule,
			})
		}
	}
	if err := writeCSV(filepath.Join(dir, "foreign_keys.csv"), [][]string{
		{"CONSTRAINT_NAME", "TABLE_SCHEMA", "TABLE_NAME", "COLUMN_NAME",
			"REF_SCHEMA", "REF_TABLE", "REF_COLUMN", "DELETE_RULE"},
	}, fkRows); err != nil {
		return err
	}

	// views.csv
	var viewRows [][]string
	for _, v := range sm.GetViews() {
		viewRows = append(viewRows, []string{
			v.ViewSchema, v.ViewName, v.ViewDefinition, v.ViewComment,
		})
	}
	if err := writeCSV(filepath.Join(dir, "views.csv"), [][]string{
		{"VIEW_SCHEMA", "VIEW_NAME", "VIEW_DEFINITION", "VIEW_COMMENT"},
	}, viewRows); err != nil {
		return err
	}

	// sequences.csv
	var seqRows [][]string
	for _, seq := range sm.GetSequences(schema) {
		seqRows = append(seqRows, []string{
			seq.SequenceSchema, seq.SequenceName,
			fmt.Sprintf("%d", seq.StartValue), fmt.Sprintf("%d", seq.IncrementBy),
			fmt.Sprintf("%d", seq.MinValue), fmt.Sprintf("%d", seq.MaxValue),
			seq.Cycle, fmt.Sprintf("%d", seq.CacheSize),
		})
	}
	if err := writeCSV(filepath.Join(dir, "sequences.csv"), [][]string{
		{"SEQUENCE_SCHEMA", "SEQUENCE_NAME", "START_VALUE", "INCREMENT_BY",
			"MIN_VALUE", "MAX_VALUE", "CYCLE", "CACHE_SIZE"},
	}, seqRows); err != nil {
		return err
	}

	// triggers.csv
	var trgRows [][]string
	for _, tbl := range tables {
		for _, trg := range sm.GetTriggers(tbl.TableSchema, tbl.TableName) {
			trgRows = append(trgRows, []string{
				trg.TriggerSchema, trg.TriggerName, trg.TableSchema, trg.TableName,
				trg.TriggerType, trg.TriggerEvent, trg.TriggerBody, trg.Status,
			})
		}
	}
	if err := writeCSV(filepath.Join(dir, "triggers.csv"), [][]string{
		{"TRIGGER_SCHEMA", "TRIGGER_NAME", "TABLE_SCHEMA", "TABLE_NAME",
			"TRIGGER_TYPE", "TRIGGER_EVENT", "TRIGGER_BODY", "STATUS"},
	}, trgRows); err != nil {
		return err
	}

	// synonyms.csv
	var synRows [][]string
	for _, syn := range sm.GetSynonyms(schema) {
		synRows = append(synRows, []string{
			syn.SynonymName, syn.SynonymSchema, syn.TargetSchema, syn.TargetName, syn.IsPublic,
		})
	}
	if err := writeCSV(filepath.Join(dir, "synonyms.csv"), [][]string{
		{"SYNONYM_NAME", "SYNONYM_SCHEMA", "TARGET_SCHEMA", "TARGET_NAME", "IS_PUBLIC"},
	}, synRows); err != nil {
		return err
	}

	fmt.Printf("Metadata exported to %s/\n", dir)
	return nil
}

func writeCSV(path string, headers, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if len(headers) > 0 {
		w.Write(headers[0])
	}
	for _, row := range rows {
		w.Write(row)
	}
	w.Flush()
	return w.Error()
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
