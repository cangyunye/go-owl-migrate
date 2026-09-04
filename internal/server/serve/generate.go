package serve

import (
	"archive/zip"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	csvvalidator "github.com/cangyunye/go-owl-migrate/internal/metadata/csv"
	"github.com/cangyunye/go-owl-migrate/internal/paths"
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

// recordGenOutput persists a generation output directory in the job store,
// prunes retired outputs (count + age) from disk, then removes their dirs.
func (s *Server) recordGenOutput(kind, dir string, meta service.GenerationMeta) error {
	if err := s.store.RecordGeneration(kind, dir, meta); err != nil {
		return err
	}
	pruned, err := s.store.PruneGenerations(kind, genOutputKeep, genOutputMaxAge)
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

// findTableCaseInsensitive resolves schema.table with an exact-key hit first,
// then a case-insensitive scan (schema and name must both fold-match). Table
// detail lookups come from user-typed names, so matching follows the unified
// case-insensitive selection semantics (ADR-003) instead of raw map keys.
func findTableCaseInsensitive(sm *md.SchemaModel, schema, name string) *md.TableDef {
	if t := sm.GetTable(schema, name); t != nil {
		return t
	}
	for _, t := range sm.GetTables() {
		if strings.EqualFold(t.TableSchema, schema) && strings.EqualFold(t.TableName, name) {
			return t
		}
	}
	return nil
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

// readGenFiles reads generation output files for API responses.
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

// handleListInsertTables reports which tables the INSERT generator would pick
// up from the configured CSV data directory, without generating anything.
func (s *Server) handleListInsertTables(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	dataDir := service.InsertDataDir(cfg)
	resp := map[string]any{"data_dir": dataDir, "tables": []map[string]any{}}
	tables, err := service.DetectTablesFromCSVDir(dataDir)
	if err != nil {
		resp["error"] = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	entries := make([]map[string]any, 0, len(tables))
	for _, t := range tables {
		entries = append(entries, map[string]any{"schema": t.TableSchema, "name": t.TableName, "columns": len(t.GetColumns())})
	}
	resp["tables"] = entries
	writeJSON(w, http.StatusOK, resp)
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

	outDir := filepath.Join(paths.TempDir(), "ddl-"+randSuffix())
	os.MkdirAll(outDir, 0755)

	// CLI export ddl 与 serve 共用 service.GenerateDDL（含跨方言类型转换、
	// 按 owner 分组、include 过滤、no-quote 覆盖）。
	include := resolveTableInclude(req.Tables, cfg)
	files, err := service.GenerateDDL(sm, cfg, include, req.NoQuoteIdentifiers, outDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate ddl: "+err.Error())
		return
	}

	meta := sourceMetaFrom(cfg.Source, "")
	meta.Detail = map[string]any{"file_count": len(files)}
	if err := s.recordGenOutput("ddl", outDir, meta); err != nil {
		writeError(w, http.StatusInternalServerError, "record output: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"output_dir": outDir,
		"count":      len(files),
		"files":      readGenFiles(files),
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

	outDir := filepath.Join(paths.TempDir(), "select-"+randSuffix())
	os.MkdirAll(outDir, 0755)

	// CLI gen-select 与 serve 共用 service.GenerateSelect。
	files, err := service.GenerateSelect(sm, cfg, resolveTableInclude(req.Tables, cfg),
		req.BatchMethod, req.PageSize, req.NoQuoteIdentifiers, outDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate select: "+err.Error())
		return
	}

	meta := sourceMetaFrom(cfg.Source, cfg.Source.Schema)
	meta.Detail = map[string]any{"file_count": len(files)}
	if err := s.recordGenOutput("select", outDir, meta); err != nil {
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

	dataDir := service.InsertDataDir(cfg)

	// CLI export insert 与 serve 共用 service.GenerateInsert（目录检测、include
	// 过滤、缺失目录的可操作指引、方言/批大小回落）。
	tables, err := service.DetectTablesFromCSVDir(dataDir)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read CSV data: "+err.Error())
		return
	}
	outDir := filepath.Join(paths.TempDir(), "insert-"+randSuffix())
	os.MkdirAll(outDir, 0755)

	files, err := service.GenerateInsert(cfg, resolveTableInclude(req.Tables, cfg), dataDir, outDir,
		service.InsertOptions{
			BatchSize: req.BatchSize,
			Truncate:  req.Truncate,
			NoQuote:   req.NoQuoteIdentifiers,
		})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate insert: "+err.Error())
		return
	}

	meta := sourceMetaFrom(cfg.Source, cfg.Source.Schema)
	meta.Detail = map[string]any{"table_count": len(tables), "file_count": len(files)}
	if err := s.recordGenOutput("insert", outDir, meta); err != nil {
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
	tbl := findTableCaseInsensitive(sm, schema, name)
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

// handleDownloadGen zips the most recent generation output of the given kind,
// or the specific record selected via ?id=.
func (s *Server) handleDownloadGen(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var (
			dir string
			err error
		)
		if idStr := r.URL.Query().Get("id"); idStr != "" {
			id, perr := strconv.ParseInt(idStr, 10, 64)
			if perr != nil {
				writeError(w, http.StatusBadRequest, "invalid generation id")
				return
			}
			rec, gerr := s.store.GetGeneration(id)
			if gerr != nil {
				if errors.Is(gerr, service.ErrNoGeneration) {
					writeError(w, http.StatusNotFound, "generation not found")
				} else {
					writeError(w, http.StatusInternalServerError, "lookup generation output: "+gerr.Error())
				}
				return
			}
			if rec.Kind != kind {
				writeError(w, http.StatusNotFound, "generation not found")
				return
			}
			dir = rec.Dir
		} else {
			dir, err = s.store.LatestGeneration(kind)
		}
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
			writeError(w, http.StatusNotFound, "generation files no longer exist")
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
