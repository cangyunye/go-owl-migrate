package serve

import (
	"archive/zip"
	"crypto/rand"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	csvvalidator "github.com/cangyunye/go-owl-migrate/internal/metadata/csv"
	"github.com/cangyunye/go-owl-migrate/internal/paths"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// genFile is a single generated file returned in endpoint responses.
type genFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// genOutputKeep is how many generation outputs are retained per kind; the
// oldest dirs are removed from disk when the limit is exceeded.
const genOutputKeep = 10

// recordGenOutput persists a generation output directory in the job store and
// prunes retired outputs from disk.
func (s *Server) recordGenOutput(kind, dir string) error {
	pruned, err := s.store.RecordGeneration(kind, dir, genOutputKeep)
	for _, d := range pruned {
		if rmErr := os.RemoveAll(d); rmErr != nil {
			fmt.Fprintf(os.Stderr, "warning: remove pruned generation dir %s: %v\n", d, rmErr)
		}
	}
	return err
}

func (s *Server) requireMetadata() (*md.SchemaModel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.schemaModel == nil {
		return nil, fmt.Errorf("metadata not loaded; load it from the 元数据 page first")
	}
	return s.schemaModel, nil
}

// resolveTableInclude merges the request's comma-separated table spec with the
// saved config. Precedence: request body > config.export.tables.include > all.
// "*" or an empty spec means "all tables" (nil filter).
func resolveTableInclude(reqTables *string, cfg *config.Config) []string {
	spec := ""
	if reqTables != nil {
		spec = *reqTables
	} else {
		return normalizeInclude(cfg.Export.Tables.Include)
	}
	var out []string
	for _, t := range strings.Split(spec, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return normalizeInclude(out)
}

func normalizeInclude(in []string) []string {
	if len(in) == 1 && in[0] == "*" {
		return nil
	}
	return in
}

func insertDataDir(cfg *config.Config) string {
	if cfg.Import.SourceDir != "" {
		return cfg.Import.SourceDir
	}
	return "./output/data/"
}

// filterTableDefs keeps only tables matched by include (nil = keep all).
func filterTableDefs(tables []*md.TableDef, include []string) []*md.TableDef {
	if len(include) == 0 {
		return tables
	}
	f := config.TableFilterConfig{Include: include}
	out := make([]*md.TableDef, 0, len(tables))
	for _, t := range tables {
		if config.MatchTable(f, t.TableSchema, t.TableName) {
			out = append(out, t)
		}
	}
	return out
}

// handleListInsertTables reports which tables the INSERT generator would pick
// up from the configured CSV data directory, without generating anything.
func (s *Server) handleListInsertTables(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	dataDir := insertDataDir(cfg)
	type tableEntry struct {
		Schema  string `json:"schema"`
		Name    string `json:"name"`
		Columns int    `json:"columns"`
	}
	resp := map[string]any{"data_dir": dataDir, "tables": []tableEntry{}}
	tables, err := detectTablesFromCSVDir(dataDir)
	if err != nil {
		resp["error"] = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	entries := make([]tableEntry, 0, len(tables))
	for _, t := range tables {
		entries = append(entries, tableEntry{Schema: t.TableSchema, Name: t.TableName, Columns: len(t.Columns)})
	}
	resp["tables"] = entries
	writeJSON(w, http.StatusOK, resp)
}

// filterSchemaTables returns a shallow copy of sm keeping only tables matched
// by include (nil = keep all). Views/sequences/synonyms etc. carry over
// unchanged; indexes and per-table objects follow the table filter.
func filterSchemaTables(sm *md.SchemaModel, include []string) *md.SchemaModel {
	if len(include) == 0 {
		return sm
	}
	f := config.TableFilterConfig{Include: include}
	out := *sm
	out.Tables = make(map[string]*md.TableDef)
	for key, t := range sm.Tables {
		if config.MatchTable(f, t.TableSchema, t.TableName) {
			out.Tables[key] = t
		}
	}
	return &out
}

func readGenFiles(paths []string) []genFile {
	files := make([]genFile, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		files = append(files, genFile{Name: filepath.Base(p), Content: string(data)})
	}
	return files
}

func (s *Server) handleGenerateDDL(w http.ResponseWriter, r *http.Request) {
	sm, err := s.requireMetadata()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	var req struct {
		Tables             *string `json:"tables"`
		NoQuoteIdentifiers *bool   `json:"no_quote_identifiers"`
	}
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}
	sm = filterSchemaTables(sm, resolveTableInclude(req.Tables, cfg))

	d, err := registry.Get(cfg.DDL.TargetDialect)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unknown target dialect: "+err.Error())
		return
	}

	outDir := filepath.Join(paths.TempDir(), "ddl-"+randSuffix())
	os.MkdirAll(outDir, 0755)
	opts := service.ToBuildOptions(cfg)
	if req.NoQuoteIdentifiers != nil {
		opts.NoQuoteIdentifiers = *req.NoQuoteIdentifiers
	}
	gen := generator.NewDDLGenerator(d, opts, outDir)

	schema := cfg.Source.Schema
	if schema == "" {
		if tables := sm.GetTables(); len(tables) > 0 {
			schema = tables[0].TableSchema
		}
	}

	var all []string
	collect := func(files []string, e error) {
		if e == nil {
			all = append(all, files...)
		}
	}
	tbls, err := gen.GenerateTables(sm)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate tables: "+err.Error())
		return
	}
	all = append(all, tbls...)
	collect(gen.GenerateIndexes(sm))
	collect(gen.GenerateViews(sm))
	collect(gen.GenerateSequences(sm, schema))
	collect(gen.GenerateSynonyms(sm, schema))
	collect(gen.GenerateMViews(sm))
	collect(gen.GenerateTriggers(sm))
	collect(gen.GenerateFunctions(sm, schema))
	collect(gen.GeneratePackages(sm, schema))
	collect(gen.GeneratePackageBodies(sm, schema))

	if err := s.recordGenOutput("ddl", outDir); err != nil {
		writeError(w, http.StatusInternalServerError, "record output: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"output_dir": outDir,
		"count":      len(all),
		"files":      readGenFiles(all),
	})
}

func (s *Server) handleGenerateSelect(w http.ResponseWriter, r *http.Request) {
	sm, err := s.requireMetadata()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	var req struct {
		BatchMethod        string  `json:"batch_method"`
		PageSize           int     `json:"page_size"`
		Tables             *string `json:"tables"`
		NoQuoteIdentifiers *bool   `json:"no_quote_identifiers"`
	}
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}
	sm = filterSchemaTables(sm, resolveTableInclude(req.Tables, cfg))

	d, err := registry.Get(cfg.DDL.TargetDialect)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unknown target dialect: "+err.Error())
		return
	}

	method := req.BatchMethod
	if method == "" {
		method = cfg.SelectGen.Batch.Method
	}
	if method == "" {
		method = "cursor"
	}
	pageSize := req.PageSize
	if pageSize == 0 {
		pageSize = cfg.SelectGen.Batch.PageSize
	}
	if pageSize == 0 {
		pageSize = 5000
	}

	quoteFn := d.Quote
	if req.NoQuoteIdentifiers != nil && *req.NoQuoteIdentifiers {
		quoteFn = func(s string) string { return s }
	}

	outDir := filepath.Join(paths.TempDir(), "select-"+randSuffix())
	os.MkdirAll(outDir, 0755)

	oracleRowNum := strings.Contains(cfg.DDL.TargetDialect, "oracle")
	gen := generator.NewSelectGenerator(method, pageSize, outDir, quoteFn,
		cfg.SelectGen.IncludeRowNumber, cfg.SelectGen.AddExportColumns, oracleRowNum).
		WithPagination(d.BuildPaginationClause)

	files, err := gen.Generate(sm)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate select: "+err.Error())
		return
	}

	if err := s.recordGenOutput("select", outDir); err != nil {
		writeError(w, http.StatusInternalServerError, "record output: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"output_dir": outDir,
		"count":      len(files),
		"files":      readGenFiles(files),
	})
}

func (s *Server) handleGenerateInsert(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	var req struct {
		BatchSize          int     `json:"batch_size"`
		Truncate           bool    `json:"truncate"`
		Tables             *string `json:"tables"`
		NoQuoteIdentifiers *bool   `json:"no_quote_identifiers"`
	}
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}

	dataDir := insertDataDir(cfg)

	tables, err := detectTablesFromCSVDir(dataDir)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read CSV data: "+err.Error())
		return
	}
	tables = filterTableDefs(tables, resolveTableInclude(req.Tables, cfg))

	dialect := cfg.DDL.TargetDialect
	if dialect == "" {
		dialect = "postgres"
	}

	outDir := filepath.Join(paths.TempDir(), "insert-"+randSuffix())
	os.MkdirAll(outDir, 0755)

	batchSize := req.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	noQuote := cfg.DDL.NoQuoteIdentifiers
	if req.NoQuoteIdentifiers != nil {
		noQuote = *req.NoQuoteIdentifiers
	}

	gen := generator.NewInsertGenerator(generator.InsertConfig{
		OutputDir:          outDir,
		BatchSize:          batchSize,
		TruncateBefore:     req.Truncate,
		Dialect:            dialect,
		NullMarker:         cfg.Import.CSV.NullMarker,
		CSVDelimiter:       cfg.Import.CSV.Delimiter,
		NoQuoteIdentifiers: noQuote,
	})

	files, err := gen.Generate(tables, dataDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate insert: "+err.Error())
		return
	}

	if err := s.recordGenOutput("insert", outDir); err != nil {
		writeError(w, http.StatusInternalServerError, "record output: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"output_dir": outDir,
		"count":      len(files),
		"files":      readGenFiles(files),
	})
}

func (s *Server) handleMetadataValidate(w http.ResponseWriter, r *http.Request) {
	sm, err := s.requireMetadata()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	errs := csvvalidator.Validate(sm)
	out := make([]map[string]any, 0, len(errs))
	for _, e := range errs {
		out = append(out, map[string]any{
			"severity": e.Severity,
			"message":  e.Message,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(out), "errors": out})
}

func (s *Server) handleMetadataTableDetail(w http.ResponseWriter, r *http.Request) {
	sm, err := s.requireMetadata()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	schema := r.PathValue("schema")
	name := r.PathValue("table")
	tbl := sm.GetTable(schema, name)
	if tbl == nil {
		writeError(w, http.StatusNotFound, "table not found")
		return
	}

	cols := make([]map[string]any, 0, len(tbl.Columns))
	for _, c := range tbl.Columns {
		cols = append(cols, map[string]any{
			"name": c.ColumnName, "type": c.DataType, "length": c.DataLength,
			"precision": c.DataPrecision, "scale": c.DataScale, "nullable": c.Nullable,
			"default": c.DefaultValue, "comment": c.ColumnComment, "identity": c.IsIdentity,
		})
	}
	pks := make([]string, 0)
	for _, p := range tbl.GetPrimaryKeys() {
		pks = append(pks, p.ColumnName)
	}
	idxs := make([]map[string]any, 0)
	for _, ix := range tbl.Indexes {
		idxs = append(idxs, map[string]any{
			"name": ix.IndexName, "column": ix.ColumnName, "unique": ix.Uniqueness,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema": tbl.TableSchema, "name": tbl.TableName, "comment": tbl.TableComment,
		"columns": cols, "primary_keys": pks, "indexes": idxs,
	})
}

// handleDownloadGen zips the most recent generation output of the given kind.
func (s *Server) handleDownloadGen(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dir, err := s.store.LatestGeneration(kind)
		if err != nil {
			if errors.Is(err, service.ErrNoGeneration) {
				writeError(w, http.StatusBadRequest, err.Error())
			} else {
				writeError(w, http.StatusInternalServerError, "lookup generation output: "+err.Error())
			}
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, kind))
		zw := zip.NewWriter(w)
		defer zw.Close()
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			f, err := zw.Create(e.Name())
			if err != nil {
				continue
			}
			f.Write(data)
		}
	}
}

func randSuffix() string {
	var b [4]byte
	rand.Read(b[:])
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), b[:])
}

// detectTablesFromCSVDir infers TableDefs from CSV data file headers,
// mirroring cmd/export_insert.go's detectTablesFromCSV.
func detectTablesFromCSVDir(dir string) ([]*md.TableDef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read data directory %q: %w", dir, err)
	}
	var tables []*md.TableDef
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".csv") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".csv")
		parts := strings.SplitN(name, ".", 2)
		if len(parts) != 2 {
			continue
		}
		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		r := csv.NewReader(f)
		header, err := r.Read()
		f.Close()
		if err != nil {
			return nil, err
		}
		tbl, err := md.NewTableDef(parts[0], parts[1])
		if err != nil {
			return nil, err
		}
		for i, colName := range header {
			col, err := md.NewColumnDef(parts[0], parts[1], colName, i+1, "VARCHAR")
			if err != nil {
				return nil, err
			}
			tbl.AddColumn(col)
		}
		tables = append(tables, tbl)
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("no {schema}.{table}.csv files found in %q", dir)
	}
	return tables, nil
}
