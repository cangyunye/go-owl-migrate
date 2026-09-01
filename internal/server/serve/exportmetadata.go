package serve

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
	"github.com/cangyunye/go-owl-migrate/internal/paths"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func (s *Server) handleExportMetadata(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source config.DBConfig `json:"source"`
		Format string          `json:"format"`
		Scope  string          `json:"scope"`
	}
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}

	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	src := req.Source
	if src.Type == "" {
		src = cfg.Source
	}
	metaSrc := src
	if resolved, refSchema, err := s.resolveDSNRef(src.DSN); err != nil {
		writeError(w, http.StatusBadRequest, "data source: "+err.Error())
		return
	} else {
		src.DSN = resolved
		if src.Schema == "" {
			src.Schema = refSchema
		}
	}
	if src.DSN == "" {
		writeError(w, http.StatusBadRequest, "source.dsn is required for metadata export")
		return
	}

	format := req.Format
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "xlsx" && format != "sql" {
		writeError(w, http.StatusBadRequest, "unsupported format: "+format+" (use csv, xlsx, or sql)")
		return
	}

	targetSchema := src.Schema
	var tableFilter []string
	scope := req.Scope
	if scope != "" && scope != "all" {
		if strings.HasPrefix(scope, "schema:") {
			targetSchema = strings.TrimPrefix(scope, "schema:")
		} else if strings.HasPrefix(scope, "table:") {
			tables := strings.TrimPrefix(scope, "table:")
			tableFilter = strings.Split(tables, ",")
		} else {
			writeError(w, http.StatusBadRequest, "invalid scope: use all, schema:NAME, or table:T1,T2")
			return
		}
	}
	if targetSchema == "" {
		writeError(w, http.StatusBadRequest, "no schema specified (set source.schema or use scope schema:NAME)")
		return
	}

	db, err := service.OpenDB(src)
	if err != nil {
		writeError(w, http.StatusBadRequest, "connect to source: "+err.Error())
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		writeError(w, http.StatusBadRequest, "ping source: "+err.Error())
		return
	}

	sm, err := extractor.Extract(db, dbconn.MetadataSourceType(src), targetSchema)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "extract metadata: "+err.Error())
		return
	}

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

	outDir := filepath.Join(paths.TempDir(), "metadata-"+randSuffix())
	os.MkdirAll(outDir, 0755)

	var files []string
	switch format {
	case "csv":
		files, err = exportMetadataCSV(outDir, sm, tables, targetSchema)
	case "sql":
		files, err = exportMetadataSQL(outDir, src.Type, sm, tables, targetSchema)
	default:
		files, err = exportMetadataCSV(outDir, sm, tables, targetSchema)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "export metadata: "+err.Error())
		return
	}

	meta := sourceMetaFrom(metaSrc, targetSchema)
	meta.Detail = map[string]any{
		"format":      format,
		"scope":       req.Scope,
		"table_count": len(tables),
		"file_count":  len(files),
	}
	if err := s.recordGenOutput("metadata", outDir, meta); err != nil {
		writeError(w, http.StatusInternalServerError, "record output: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"output_dir":  outDir,
		"format":      format,
		"table_count": len(tables),
		"count":       len(files),
		"files":       readGenFiles(files),
	})
}

func exportMetadataCSV(dir string, sm *md.SchemaModel, tables []*md.TableDef, schema string) ([]string, error) {
	var files []string

	write := func(name string, headers []string, rows [][]string) error {
		p := filepath.Join(dir, name)
		f, err := os.Create(p)
		if err != nil {
			return err
		}
		defer f.Close()
		w := csv.NewWriter(f)
		w.Write(headers)
		for _, row := range rows {
			w.Write(row)
		}
		w.Flush()
		files = append(files, p)
		return w.Error()
	}

	var tableRows [][]string
	for _, tbl := range tables {
		tableRows = append(tableRows, []string{tbl.TableSchema, tbl.TableName, tbl.TableType, tbl.TableComment})
	}
	if err := write("tables.csv", []string{"TABLE_SCHEMA", "TABLE_NAME", "TABLE_TYPE", "TABLE_COMMENT"}, tableRows); err != nil {
		return nil, err
	}

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
	if err := write("columns.csv", []string{"TABLE_SCHEMA", "TABLE_NAME", "COLUMN_NAME", "ORDINAL_POSITION", "DATA_TYPE", "DATA_LENGTH", "DATA_PRECISION", "DATA_SCALE", "NULLABLE", "DEFAULT_VALUE", "COLUMN_COMMENT"}, colRows); err != nil {
		return nil, err
	}

	var pkRows [][]string
	for _, tbl := range tables {
		for _, pk := range tbl.GetPrimaryKeys() {
			pkRows = append(pkRows, []string{
				pk.TableSchema, pk.TableName, pk.ConstraintName, pk.ColumnName,
				fmt.Sprintf("%d", pk.OrdinalPosition),
			})
		}
	}
	if err := write("primary_keys.csv", []string{"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "COLUMN_NAME", "ORDINAL_POSITION"}, pkRows); err != nil {
		return nil, err
	}

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
	if err := write("indexes.csv", []string{"TABLE_SCHEMA", "TABLE_NAME", "INDEX_NAME", "INDEX_TYPE", "UNIQUENESS", "COLUMN_NAME", "ORDINAL_POSITION", "EXPRESSION"}, idxRows); err != nil {
		return nil, err
	}

	var fkRows [][]string
	for _, tbl := range tables {
		for _, fk := range tbl.GetForeignKeys() {
			fkRows = append(fkRows, []string{
				fk.ConstraintName, fk.TableSchema, fk.TableName, fk.ColumnName,
				fk.RefSchema, fk.RefTable, fk.RefColumn, fk.DeleteRule,
			})
		}
	}
	if err := write("foreign_keys.csv", []string{"CONSTRAINT_NAME", "TABLE_SCHEMA", "TABLE_NAME", "COLUMN_NAME", "REF_SCHEMA", "REF_TABLE", "REF_COLUMN", "DELETE_RULE"}, fkRows); err != nil {
		return nil, err
	}

	var viewRows [][]string
	for _, v := range sm.GetViews() {
		viewRows = append(viewRows, []string{v.ViewSchema, v.ViewName, v.ViewDefinition, v.ViewComment})
	}
	if err := write("views.csv", []string{"VIEW_SCHEMA", "VIEW_NAME", "VIEW_DEFINITION", "VIEW_COMMENT"}, viewRows); err != nil {
		return nil, err
	}

	var seqRows [][]string
	for _, seq := range sm.GetSequences(schema) {
		seqRows = append(seqRows, []string{
			seq.SequenceSchema, seq.SequenceName,
			fmt.Sprintf("%d", seq.StartValue), fmt.Sprintf("%d", seq.IncrementBy),
			fmt.Sprintf("%d", seq.MinValue), fmt.Sprintf("%d", seq.MaxValue),
			seq.Cycle, fmt.Sprintf("%d", seq.CacheSize),
		})
	}
	if err := write("sequences.csv", []string{"SEQUENCE_SCHEMA", "SEQUENCE_NAME", "START_VALUE", "INCREMENT_BY", "MIN_VALUE", "MAX_VALUE", "CYCLE", "CACHE_SIZE"}, seqRows); err != nil {
		return nil, err
	}

	var trgRows [][]string
	for _, tbl := range tables {
		for _, trg := range sm.GetTriggers(tbl.TableSchema, tbl.TableName) {
			trgRows = append(trgRows, []string{
				trg.TriggerSchema, trg.TriggerName, trg.TableSchema, trg.TableName,
				trg.TriggerType, trg.TriggerEvent, trg.TriggerBody, trg.Status,
			})
		}
	}
	if err := write("triggers.csv", []string{"TRIGGER_SCHEMA", "TRIGGER_NAME", "TABLE_SCHEMA", "TABLE_NAME", "TRIGGER_TYPE", "TRIGGER_EVENT", "TRIGGER_BODY", "STATUS"}, trgRows); err != nil {
		return nil, err
	}

	var synRows [][]string
	for _, syn := range sm.GetSynonyms(schema) {
		synRows = append(synRows, []string{
			syn.SynonymName, syn.SynonymSchema, syn.TargetSchema, syn.TargetName, syn.IsPublic,
		})
	}
	if err := write("synonyms.csv", []string{"SYNONYM_NAME", "SYNONYM_SCHEMA", "TARGET_SCHEMA", "TARGET_NAME", "IS_PUBLIC"}, synRows); err != nil {
		return nil, err
	}

	return files, nil
}

func exportMetadataSQL(dir string, dbType string, sm *md.SchemaModel, tables []*md.TableDef, schema string) ([]string, error) {
	p := filepath.Join(dir, "metadata.sql")
	f, err := os.Create(p)
	if err != nil {
		return nil, err
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
	fmt.Fprintf(f, "-- Generated by owl-migrate web UI\n\n")

	for _, tbl := range tables {
		tableOwner := tbl.Owner
		if tableOwner == "" {
			tableOwner = schema
		}
		fmt.Fprintf(f, "INSERT INTO dba_tables (%s, %s, %s) VALUES (%s, %s, %s);\n",
			q("OWNER"), q("TABLE_NAME"), q("TABLE_TYPE"),
			q(tableOwner), q(tbl.TableName), q(tbl.TableType))
	}

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

	for _, v := range sm.GetViews() {
		fmt.Fprintf(f, "INSERT INTO dba_views (%s, %s, %s) VALUES (%s, %s, %s);\n",
			q("OWNER"), q("VIEW_NAME"), q("TEXT"),
			q(v.ViewSchema), q(v.ViewName), q(v.ViewDefinition))
	}

	for _, seq := range sm.GetSequences(schema) {
		fmt.Fprintf(f, "INSERT INTO dba_sequences (%s, %s, %s, %s, %s) VALUES (%s, %s, %d, %d, %d);\n",
			q("SEQUENCE_OWNER"), q("SEQUENCE_NAME"), q("MIN_VALUE"), q("MAX_VALUE"), q("INCREMENT_BY"),
			q(schema), q(seq.SequenceName), seq.MinValue, seq.MaxValue, seq.IncrementBy)
	}

	fmt.Fprintf(f, "\n-- %d tables, %d views, %d sequences exported\n",
		len(tables), len(sm.GetViews()), len(sm.GetSequences(schema)))

	return []string{p}, nil
}
