package exporter

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// DataTable holds the data read from an offline source (CSV or XLSX).
type DataTable struct {
	Table   *md.TableDef
	Columns []ColumnInfo
	Rows    [][]any
}

// ReadCSVTable reads a single CSV file and returns its header columns and data rows.
// File naming convention: {schema}.{table}.csv
// First row is column headers; remaining rows are data (all string values).
func ReadCSVTable(dataDir string, tbl *md.TableDef) (*DataTable, error) {
	filename := fmt.Sprintf("%s.%s.csv", strings.ToLower(tbl.TableSchema), strings.ToLower(tbl.TableName))
	path := filepath.Join(dataDir, filename)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open CSV %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.LazyQuotes = true
	r.TrimLeadingSpace = true

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header from %s: %w", path, err)
	}

	columns := make([]ColumnInfo, len(header))
	for i, name := range header {
		columns[i] = ColumnInfo{Name: name, TypeName: "VARCHAR", Nullable: true}
	}

	var rows [][]any
	for {
		record, err := r.Read()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("read row from %s: %w", path, err)
		}
		row := make([]any, len(record))
		for i, v := range record {
			row[i] = v
		}
		rows = append(rows, row)
	}

	return &DataTable{
		Table:   tbl,
		Columns: columns,
		Rows:    rows,
	}, nil
}

// ReadXLSXTable reads a data sheet (@tablename) from an xlsx file.
// The sheet name should be @ followed by the table name (case-insensitive match).
func ReadXLSXTable(xlsxPath string, tbl *md.TableDef) (*DataTable, error) {
	f, err := excelize.OpenFile(xlsxPath)
	if err != nil {
		return nil, fmt.Errorf("open xlsx %q: %w", xlsxPath, err)
	}
	defer f.Close()

	// Find matching @ sheet
	sheetName := ""
	for _, sheet := range f.GetSheetList() {
		if !strings.HasPrefix(sheet, "@") {
			continue
		}
		name := strings.TrimPrefix(sheet, "@")
		if strings.EqualFold(name, tbl.TableName) {
			sheetName = sheet
			break
		}
	}
	if sheetName == "" {
		return nil, fmt.Errorf("xlsx %q: no @%s data sheet found", xlsxPath, tbl.TableName)
	}

	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, fmt.Errorf("read sheet %q: %w", sheetName, err)
	}
	if len(rows) < 2 {
		// Header only or empty
		return &DataTable{
			Table:   tbl,
			Columns: nil,
			Rows:    nil,
		}, nil
	}

	header := rows[0]
	columns := make([]ColumnInfo, len(header))
	for i, name := range header {
		columns[i] = ColumnInfo{Name: name, TypeName: "VARCHAR", Nullable: true}
	}

	dataRows := rows[1:]
	var result [][]any
	for _, row := range dataRows {
		vals := make([]any, len(row))
		for i, v := range row {
			if v == "" {
				vals[i] = nil
			} else {
				vals[i] = v
			}
		}
		result = append(result, vals)
	}

	return &DataTable{
		Table:   tbl,
		Columns: columns,
		Rows:    result,
	}, nil
}

// DetectTablesFromCSV scans a directory for CSV files and creates TableDef entries.
// File naming: {schema}.{table}.csv
func DetectTablesFromCSV(dir string) ([]*md.TableDef, error) {
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
		schema := parts[0]
		tableName := parts[1]

		// Read header to infer columns
		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", entry.Name(), err)
		}
		r := csv.NewReader(f)
		header, err := r.Read()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("read header from %s: %w", entry.Name(), err)
		}

		tbl, err := md.NewTableDef(schema, tableName)
		if err != nil {
			return nil, fmt.Errorf("create table def for %s: %w", entry.Name(), err)
		}
		for i, colName := range header {
			col, err := md.NewColumnDef(schema, tableName, colName, i+1, "VARCHAR")
			if err != nil {
				return nil, fmt.Errorf("create column %s: %w", colName, err)
			}
			tbl.AddColumn(col)
		}
		tables = append(tables, tbl)
	}

	if len(tables) == 0 {
		return nil, fmt.Errorf("no CSV files found in %q", dir)
	}
	return tables, nil
}

// DetectTablesFromXLSX reads table definitions from an xlsx file's metadata sheets.
// It reuses the xlsx loader for SchemaModel loading; returns only table definitions from it.
func DetectTablesFromXLSX(xlsxPath string) ([]*md.TableDef, error) {
	f, err := excelize.OpenFile(xlsxPath)
	if err != nil {
		return nil, fmt.Errorf("open xlsx %q: %w", xlsxPath, err)
	}
	defer f.Close()

	// Find @ data sheets and build table defs from their names
	var tables []*md.TableDef
	for _, sheet := range f.GetSheetList() {
		if !strings.HasPrefix(sheet, "@") {
			continue
		}
		tableName := strings.TrimPrefix(sheet, "@")
		if tableName == "" {
			continue
		}

		// Read first row to infer column names
		rows, err := f.GetRows(sheet)
		if err != nil || len(rows) < 1 {
			continue
		}
		header := rows[0]

		tbl, err := md.NewTableDef("xlsx", tableName)
		if err != nil {
			return nil, fmt.Errorf("create table def for @%s: %w", tableName, err)
		}
		for i, colName := range header {
			col, err := md.NewColumnDef("xlsx", tableName, colName, i+1, "VARCHAR")
			if err != nil {
				return nil, fmt.Errorf("create column %s: %w", colName, err)
			}
			tbl.AddColumn(col)
		}
		tables = append(tables, tbl)
	}

	if len(tables) == 0 {
		return nil, fmt.Errorf("no @ data sheets found in %q", xlsxPath)
	}
	return tables, nil
}
