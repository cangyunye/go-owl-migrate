package exporter

import (
	"fmt"
	"os"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/cangyunye/go-owl-migrate/internal/generator"
)

// excelizeOpenFile is a variable so tests can override it.
var excelizeOpenFile = excelize.OpenFile

// ExportWriter writes exported table data in a specific format.
type ExportWriter interface {
	// WriteHeader writes the column headers (if applicable for the format).
	WriteHeader(columns []ColumnInfo) error
	// WriteRow writes a single data row.
	WriteRow(row []any, columns []ColumnInfo) error
	// Close flushes and closes the output file.
	Close() error
	// OutputFile returns the path to the written file.
	OutputFile() string
}

// ── CSV Writer ──

type csvWriter struct {
	path    string
	delim   string
	quote   string
	nullRep string
	term    string
	header  bool
	f       *os.File
	first   bool
}

func (w *csvWriter) WriteHeader(columns []ColumnInfo) error {
	f, err := os.Create(w.path)
	if err != nil {
		return fmt.Errorf("create CSV: %w", err)
	}
	w.f = f
	if w.header && len(columns) > 0 {
		header := make([]string, len(columns))
		for i, col := range columns {
			header[i] = col.Name
		}
		_, err := f.WriteString(w.csvLine(header))
		return err
	}
	return nil
}

func (w *csvWriter) WriteRow(row []any, columns []ColumnInfo) error {
	vals := make([]string, len(row))
	for i, v := range row {
		vals[i] = w.formatValue(v, columns[i])
	}
	_, err := w.f.WriteString(w.csvLine(vals))
	return err
}

func (w *csvWriter) Close() error {
	if w.f != nil {
		return w.f.Close()
	}
	return nil
}

func (w *csvWriter) OutputFile() string { return w.path }

func (w *csvWriter) formatValue(v any, col ColumnInfo) string {
	if v == nil {
		return w.nullRep
	}
	var s string
	switch t := v.(type) {
	case []byte:
		if isBinaryType(col.TypeName) {
			s = fmt.Sprintf("%x", t)
		} else {
			s = string(t)
		}
	case string:
		s = t
	default:
		s = fmt.Sprintf("%v", v)
	}
	// RFC 4180: quote if contains delimiter, quote char, or newline
	needsQuote := strings.Contains(s, w.delim) ||
		strings.Contains(s, w.quote) ||
		strings.Contains(s, "\n") ||
		strings.Contains(s, "\r")
	if needsQuote {
		q := w.quote
		s = q + strings.ReplaceAll(s, q, q+q) + q
	}
	return s
}

func (w *csvWriter) csvLine(vals []string) string {
	return strings.Join(vals, w.delim) + w.term
}

// ── SQL Writer ──

type sqlWriter struct {
	path       string
	schema     string
	table      string
	quoter     func(string) string
	dialect    string
	nullMarker string
	batchSize  int
	f          *os.File
	cols       []string
	rows       []string
	started    bool // whether COMMIT has been written (to avoid double COMMIT in Close)
}

func (w *sqlWriter) WriteHeader(columns []ColumnInfo) error {
	f, err := os.Create(w.path)
	if err != nil {
		return fmt.Errorf("create SQL file: %w", err)
	}
	w.f = f
	w.cols = make([]string, len(columns))
	for i, col := range columns {
		w.cols[i] = w.quoter(col.Name)
	}
	w.rows = nil
	return nil
}

func (w *sqlWriter) WriteRow(row []any, columns []ColumnInfo) error {
	vals := make([]string, len(row))
	for i, v := range row {
		if v == nil {
			vals[i] = "NULL"
		} else {
			s := fmt.Sprintf("%v", v)
			vals[i] = generator.FormatSQLValue(s, w.nullMarker, w.dialect)
		}
	}
	w.rows = append(w.rows, strings.Join(vals, ", "))

	if len(w.rows) >= w.batchSize {
		return w.flushBatch()
	}
	return nil
}

func (w *sqlWriter) Close() error {
	if w.f == nil {
		return nil
	}
	// Flush remaining rows
	if len(w.rows) > 0 {
		if err := w.flushBatch(); err != nil {
			w.f.Close()
			return err
		}
	}
	return w.f.Close()
}

func (w *sqlWriter) OutputFile() string { return w.path }

func (w *sqlWriter) flushBatch() error {
	if len(w.rows) == 0 {
		return nil
	}

	write := func(s string) {
		w.f.WriteString(s + "\n")
	}

	// Always write BEGIN for non-MySQL batches
	if w.dialect != "mysql" {
		write("BEGIN;")
		write("")
	}

	colList := strings.Join(w.cols, ", ")
	write(fmt.Sprintf("INSERT INTO %s.%s (%s)",
		w.quoter(w.schema), w.quoter(w.table), colList))
	write("VALUES")

	for i, row := range w.rows {
		comma := ","
		if i == len(w.rows)-1 {
			comma = ";"
		}
		write(fmt.Sprintf("  (%s)%s", row, comma))
	}
	write("")

	// COMMIT after batch for non-MySQL
	if w.dialect != "mysql" {
		write("COMMIT;")
		write("")
	}

	w.rows = nil
	w.started = true
	return nil
}

// ── XLSX Writer ──

type xlsxWriter struct {
	path   string
	schema string
	table  string
	f      *excelize.File
	sheet  string
	rowIdx int
}

func (w *xlsxWriter) WriteHeader(columns []ColumnInfo) error {
	w.f = excelize.NewFile()
	w.sheet = w.table
	// Rename default sheet
	w.f.SetSheetName("Sheet1", w.sheet)
	for i, col := range columns {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		w.f.SetCellValue(w.sheet, cell, col.Name)
	}
	w.rowIdx = 2
	return nil
}

func (w *xlsxWriter) WriteRow(row []any, columns []ColumnInfo) error {
	for i, v := range row {
		cell, _ := excelize.CoordinatesToCellName(i+1, w.rowIdx)
		if v == nil {
			w.f.SetCellValue(w.sheet, cell, "")
		} else {
			w.f.SetCellValue(w.sheet, cell, v)
		}
	}
	w.rowIdx++
	return nil
}

func (w *xlsxWriter) Close() error {
	if w.f == nil {
		return nil
	}
	return w.f.SaveAs(w.path)
}

func (w *xlsxWriter) OutputFile() string { return w.path }
