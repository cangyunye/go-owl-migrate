// Package xlsx loads database metadata and data from an Excel (.xlsx) file.
//
// Sheet naming convention:
//   - Metadata sheets: tables, columns, primary_keys, indexes, foreign_keys,
//     views, sequences, triggers, functions, synonyms
//   - Data sheets: @tablename — first row is column headers, remaining rows are data
//
// Data from @ sheets is written as CSV files to a data output directory,
// reusing the existing CSV-based DDL/INSERT pipeline.
package xlsx

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"

	csvpkg "github.com/cangyunye/go-owl-migrate/internal/metadata/csv"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// knownMetadataSheets maps lowercase sheet names to their human-readable labels.
var knownMetadataSheets = map[string]string{
	"tables":       "tables",
	"columns":      "columns",
	"primary_keys":  "primary_keys",
	"indexes":      "indexes",
	"foreign_keys":  "foreign_keys",
	"views":        "views",
	"sequences":    "sequences",
	"triggers":     "triggers",
	"functions":    "functions",
	"synonyms":     "synonyms",
}

// Config holds xlsx loading options.
type Config struct {
	FilePath      string // path to the .xlsx file
	DataOutputDir string // directory for CSV data output from @ sheets
}

// Load reads an xlsx file and builds a SchemaModel from its sheets.
// Metadata sheets are parsed using the CSV parsers (reused).
// @ sheets are written as CSV data files to DataOutputDir.
func Load(cfg Config) (*md.SchemaModel, error) {
	if cfg.FilePath == "" {
		return nil, fmt.Errorf("xlsx file path is required")
	}
	if cfg.DataOutputDir == "" {
		cfg.DataOutputDir = "./output/data/"
	}

	f, err := excelize.OpenFile(cfg.FilePath)
	if err != nil {
		return nil, fmt.Errorf("open xlsx %q: %w", cfg.FilePath, err)
	}
	defer f.Close()

	sheetList := f.GetSheetList()
	if len(sheetList) == 0 {
		return nil, fmt.Errorf("xlsx file %q has no sheets", cfg.FilePath)
	}

	// Build SchemaModel by writing each metadata sheet as in-memory CSV
	// and reusing the CSV parser functions.
	//
	// We use an in-memory loader that registers CSV text for each sheet,
	// then calls the CSV loader's Load method.
	loader := csvpkg.NewLoader()
	hasTables := false

	for _, sheet := range sheetList {
		lower := strings.ToLower(sheet)

		// Check if this is a known metadata sheet
		metaName, isMeta := knownMetadataSheets[lower]
		if !isMeta {
			continue // handled as data sheet later, or skipped
		}

		rows, err := f.GetRows(sheet)
		if err != nil {
			return nil, fmt.Errorf("read sheet %q: %w", sheet, err)
		}
		if len(rows) == 0 {
			continue
		}

		// Build CSV text from the rows and register with the loader
		csvText := rowsToCSV(rows)
		fileName := metaName + ".csv"
		loader.AddReader(fileName, strings.NewReader(csvText))

		if lower == "tables" {
			hasTables = true
		}
	}

	if !hasTables {
		return nil, fmt.Errorf("xlsx %q: required sheet 'tables' not found", cfg.FilePath)
	}

	sm, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("load metadata from xlsx: %w", err)
	}

	// Process @ sheets: write data as CSV files
	if err := processDataSheets(f, sheetList, sm, cfg.DataOutputDir); err != nil {
		return nil, fmt.Errorf("process data sheets: %w", err)
	}

	return sm, nil
}

// rowsToCSV converts a [][]string to CSV text.
func rowsToCSV(rows [][]string) string {
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	for _, row := range rows {
		// Replace "" with empty string (null value in CSV convention)
		normalized := make([]string, len(row))
		for i, v := range row {
			if v == "" {
				normalized[i] = "\\N" // null marker
			} else {
				normalized[i] = v
			}
		}
		w.Write(normalized)
	}
	w.Flush()
	return buf.String()
}

// processDataSheets iterates @ sheets and writes their data as CSV files.
func processDataSheets(f *excelize.File, sheetList []string, sm *md.SchemaModel, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create data output dir: %w", err)
	}

	for _, sheet := range sheetList {
		if !strings.HasPrefix(sheet, "@") {
			continue
		}
		tableName := sheet[1:] // strip @ prefix
		if tableName == "" {
			continue
		}

		// Find the matching table in SchemaModel
		var matchedTbl *md.TableDef
		for _, tbl := range sm.GetTables() {
			if strings.EqualFold(tbl.TableName, tableName) {
				matchedTbl = tbl
				break
			}
		}
		if matchedTbl == nil {
			return fmt.Errorf("data sheet @%s has no matching table definition", tableName)
		}

		rows, err := f.GetRows(sheet)
		if err != nil {
			return fmt.Errorf("read data sheet %q: %w", sheet, err)
		}
		if len(rows) < 2 {
			// Header only or empty — nothing to write
			continue
		}

		header := rows[0]
		dataRows := rows[1:]

		// Write CSV data file
		filename := fmt.Sprintf("%s.%s.csv",
			strings.ToLower(matchedTbl.TableSchema),
			strings.ToLower(matchedTbl.TableName))
		path := filepath.Join(outputDir, filename)

		out, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("create CSV %q: %w", path, err)
		}

		w := csv.NewWriter(out)
		// Write header
		w.Write(header)
		// Write data rows
		for _, row := range dataRows {
			normalized := make([]string, len(row))
			for i, v := range row {
				if v == "" {
					normalized[i] = "\\N"
				} else {
					normalized[i] = v
				}
			}
			w.Write(normalized)
		}
		w.Flush()
		if err := w.Error(); err != nil {
			out.Close()
			return fmt.Errorf("write CSV %q: %w", path, err)
		}
		out.Close()

		fmt.Printf("  %s → %s (%d rows)\n", sheet, path, len(dataRows))
	}

	return nil
}
