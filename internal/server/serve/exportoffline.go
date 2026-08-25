package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cangyunye/go-owl-migrate/internal/paths"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
)

func (s *Server) handleExportOffline(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DataDir  string `json:"data_dir"`
		XLSXPath string `json:"xlsx_path"`
		Format   string `json:"format"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.DataDir == "" && req.XLSXPath == "" {
		writeError(w, http.StatusBadRequest, "either data_dir (CSV) or xlsx_path is required")
		return
	}

	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	format := req.Format
	if format == "" {
		format = "csv"
	}

	outDir := filepath.Join(paths.TempDir(), "export-offline-"+randSuffix())
	os.MkdirAll(outDir, 0755)

	exp := exporter.New(nil, exporter.Config{
		OutputDir:         outDir,
		Format:            format,
		CSVDelimiter:      cfg.Export.CSV.Delimiter,
		CSVQuoteChar:      cfg.Export.CSV.QuoteChar,
		CSVNullRep:        cfg.Export.CSV.NullRepresentation,
		CSVHeader:         cfg.Export.CSV.Header,
		CSVLineTerminator: cfg.Export.CSV.LineTerminator,
		DBType:            cfg.DDL.TargetDialect,
	})

	var dataTables []*exporter.DataTable

	if req.XLSXPath != "" {
		tables, err := exporter.DetectTablesFromXLSX(req.XLSXPath)
		if err != nil {
			writeError(w, http.StatusBadRequest, "detect xlsx tables: "+err.Error())
			return
		}
		for _, tbl := range tables {
			dt, err := exporter.ReadXLSXTable(req.XLSXPath, tbl)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("read %s.%s: %v", tbl.TableSchema, tbl.TableName, err))
				return
			}
			dataTables = append(dataTables, dt)
		}
	} else {
		tables, err := exporter.DetectTablesFromCSV(req.DataDir)
		if err != nil {
			writeError(w, http.StatusBadRequest, "detect csv tables: "+err.Error())
			return
		}
		for _, tbl := range tables {
			dt, err := exporter.ReadCSVTable(req.DataDir, tbl)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("read %s.%s: %v", tbl.TableSchema, tbl.TableName, err))
				return
			}
			dataTables = append(dataTables, dt)
		}
	}

	results, err := exp.ExportTablesFromData(dataTables)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "export: "+err.Error())
		return
	}

	var totalRows int64
	var failed []string
	var outputFiles []string
	for _, res := range results {
		if res.Error != nil {
			failed = append(failed, fmt.Sprintf("%s.%s: %v", res.Schema, res.Table, res.Error))
			continue
		}
		totalRows += res.Rows
		outputFiles = append(outputFiles, res.OutputFile)
	}

	if err := s.recordGenOutput("export-offline", outDir); err != nil {
		writeError(w, http.StatusInternalServerError, "record output: "+err.Error())
		return
	}

	resp := map[string]any{
		"output_dir":  outDir,
		"format":      format,
		"table_count": len(results),
		"total_rows":  totalRows,
		"files":       readGenFiles(outputFiles),
	}
	if len(failed) > 0 {
		resp["errors"] = failed
	}
	writeJSON(w, http.StatusOK, resp)
}
