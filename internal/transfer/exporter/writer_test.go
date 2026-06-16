package exporter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func TestCSVWriter_WritesHeaderAndRows(t *testing.T) {
	dir := t.TempDir()
	w := &csvWriter{
		path:    filepath.Join(dir, "test.table.csv"),
		delim:   ",",
		quote:   "\"",
		nullRep: "\\N",
		term:    "\n",
		header:  true,
	}
	cols := []ColumnInfo{
		{Name: "id"}, {Name: "name"}, {Name: "salary"},
	}
	if err := w.WriteHeader(cols); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRow([]any{1, "Alice", 50000}, cols); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRow([]any{2, "Bob", nil}, cols); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(w.path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.HasPrefix(got, "id,name,salary\n") {
		t.Errorf("expected header, got %q", got)
	}
	if !strings.Contains(got, "1,Alice,50000") {
		t.Errorf("expected row 1, got %q", got)
	}
	if !strings.Contains(got, "2,Bob,\\N") {
		t.Errorf("expected row 2 with null, got %q", got)
	}
}

func TestCSVWriter_NoHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "noheader.csv")
	w := &csvWriter{
		path:    path,
		delim:   ",",
		quote:   "\"",
		nullRep: "NULL",
		term:    "\n",
		header:  false,
	}
	cols := []ColumnInfo{{Name: "a"}, {Name: "b"}}
	w.WriteHeader(cols)
	w.WriteRow([]any{"x", "y"}, cols)
	w.Close()

	data, _ := os.ReadFile(path)
	if strings.HasPrefix(string(data), "x,y") {
		// good — no header, data starts immediately
	} else {
		t.Errorf("expected data without header, got %q", string(data))
	}
}

func TestCSVWriter_Quoting(t *testing.T) {
	dir := t.TempDir()
	w := &csvWriter{
		path:    filepath.Join(dir, "quote.csv"),
		delim:   ",",
		quote:   "\"",
		nullRep: "\\N",
		term:    "\n",
		header:  false,
	}
	cols := []ColumnInfo{{Name: "a"}, {Name: "b"}}
	w.WriteHeader(cols)
	w.WriteRow([]any{"contains,comma", "has\"quote"}, cols)
	w.Close()

	data, _ := os.ReadFile(w.path)
	got := string(data)
	if !strings.Contains(got, "\"contains,comma\"") {
		t.Errorf("comma should be quoted, got %q", got)
	}
	if !strings.Contains(got, "\"has\"\"quote\"") {
		t.Errorf("quote should be escaped, got %q", got)
	}
}

func TestSQLWriter_basic(t *testing.T) {
	dir := t.TempDir()
	w := &sqlWriter{
		path:      filepath.Join(dir, "SCOTT.EMP.insert.sql"),
		schema:    "SCOTT",
		table:     "EMP",
		quoter:    func(s string) string { return `"` + s + `"` },
		dialect:   "postgres",
		nullMarker: "\\N",
		batchSize: 100,
	}
	cols := []ColumnInfo{{Name: "EMPNO"}, {Name: "ENAME"}}
	if err := w.WriteHeader(cols); err != nil {
		t.Fatal(err)
	}
	w.WriteRow([]any{7369, "SMITH"}, cols)
	w.WriteRow([]any{7499, "ALLEN"}, cols)
	w.Close()

	data, _ := os.ReadFile(w.path)
	got := string(data)

	if !strings.Contains(got, "BEGIN;") {
		t.Error("expected BEGIN for postgres")
	}
	if !strings.Contains(got, "INSERT INTO \"SCOTT\".\"EMP\" (\"EMPNO\", \"ENAME\")") {
		t.Error("expected INSERT with quoted identifiers")
	}
	if !strings.Contains(got, "VALUES") {
		t.Error("expected VALUES")
	}
	if !strings.Contains(got, "SMITH") {
		t.Error("expected SMITH in output")
	}
	if !strings.Contains(got, "ALLEN") {
		t.Error("expected ALLEN in output")
	}
	if !strings.Contains(got, "COMMIT;") {
		t.Error("expected COMMIT for postgres")
	}
}

func TestSQLWriter_batchSize(t *testing.T) {
	dir := t.TempDir()
	w := &sqlWriter{
		path:      filepath.Join(dir, "batch.sql"),
		schema:    "public",
		table:     "t1",
		quoter:    func(s string) string { return `"` + s + `"` },
		dialect:   "postgres",
		nullMarker: "\\N",
		batchSize: 3,
	}
	cols := []ColumnInfo{{Name: "id"}}
	w.WriteHeader(cols)
	for i := 1; i <= 7; i++ {
		w.WriteRow([]any{i}, cols)
	}
	w.Close()

	data, _ := os.ReadFile(w.path)
	got := string(data)

	// 7 rows, batchSize=3 => 3+3+1=3 INSERTs
	if strings.Count(got, "INSERT INTO") != 3 {
		t.Errorf("expected 3 INSERTs for 7 rows with batch=3, got %d", strings.Count(got, "INSERT INTO"))
	}
	// Postgres: COMMIT per batch (3 batches => 3 COMMITs)
	if strings.Count(got, "COMMIT;") != 3 {
		t.Errorf("expected 3 COMMITs (one per batch), got %d", strings.Count(got, "COMMIT;"))
	}
}

func TestSQLWriter_mysqlStyle(t *testing.T) {
	dir := t.TempDir()
	w := &sqlWriter{
		path:      filepath.Join(dir, "mysql.sql"),
		schema:    "testdb",
		table:     "users",
		quoter:    func(s string) string { return "`" + s + "`" },
		dialect:   "mysql",
		nullMarker: "\\N",
		batchSize: 100,
	}
	cols := []ColumnInfo{{Name: "id"}, {Name: "name"}, {Name: "salary"}}
	w.WriteHeader(cols)
	w.WriteRow([]any{1, "Alice", 5000.0}, cols)
	w.WriteRow([]any{2, "Bob", nil}, cols)
	w.Close()

	data, _ := os.ReadFile(w.path)
	got := string(data)

	if strings.Contains(got, "BEGIN;") {
		t.Error("MySQL should not have BEGIN/COMMIT")
	}
	if !strings.Contains(got, "`id`") {
		t.Error("MySQL should use backtick quoting")
	}
	if !strings.Contains(got, "NULL") {
		t.Error("nil value should become NULL")
	}
}

func TestXLSXWriter_basic(t *testing.T) {
	dir := t.TempDir()
	w := &xlsxWriter{
		path:   filepath.Join(dir, "SCOTT.EMP.xlsx"),
		schema: "SCOTT",
		table:  "EMP",
	}
	cols := []ColumnInfo{{Name: "EMPNO"}, {Name: "ENAME"}, {Name: "SAL"}}
	if err := w.WriteHeader(cols); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRow([]any{7369, "SMITH", 5000.0}, cols); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRow([]any{7499, "ALLEN", nil}, cols); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Verify file exists and has content
	info, err := os.Stat(w.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Error("xlsx file should not be empty")
	}

	// Read back with excelize to verify content
	rows := readXLSXRows(t, w.path)
	if len(rows) < 3 {
		t.Fatalf("expected at least 3 rows (header+data), got %d", len(rows))
	}
	// Header
	if rows[0][0] != "EMPNO" || rows[0][1] != "ENAME" {
		t.Errorf("header mismatch: %v", rows[0])
	}
	// Data row 1
	if rows[1][0] != "7369" || rows[1][1] != "SMITH" {
		t.Errorf("row 1 mismatch: %v", rows[1])
	}
}

func TestSQLWriter_OutputFile(t *testing.T) {
	dir := t.TempDir()
	w := &sqlWriter{
		path:      filepath.Join(dir, "t.sql"),
		schema:    "s",
		table:     "t",
		quoter:    func(s string) string { return `"` + s + `"` },
		dialect:   "postgres",
		nullMarker: "\\N",
		batchSize: 100,
	}
	if w.OutputFile() != filepath.Join(dir, "t.sql") {
		t.Errorf("unexpected output file: %s", w.OutputFile())
	}
}

func TestCSVWriter_OutputFile(t *testing.T) {
	dir := t.TempDir()
	w := &csvWriter{
		path: filepath.Join(dir, "my.csv"),
	}
	if w.OutputFile() != filepath.Join(dir, "my.csv") {
		t.Errorf("unexpected output file: %s", w.OutputFile())
	}
}

func TestXLSXWriter_OutputFile(t *testing.T) {
	dir := t.TempDir()
	w := &xlsxWriter{
		path: filepath.Join(dir, "t.xlsx"),
	}
	if w.OutputFile() != filepath.Join(dir, "t.xlsx") {
		t.Errorf("unexpected output file: %s", w.OutputFile())
	}
}

func TestXLSXWriter_largeBatch(t *testing.T) {
	dir := t.TempDir()
	w := &xlsxWriter{
		path:   filepath.Join(dir, "large.xlsx"),
		schema: "s",
		table:  "large",
	}
	cols := []ColumnInfo{{Name: "col1"}, {Name: "col2"}}
	w.WriteHeader(cols)
	for i := 1; i <= 2000; i++ {
		w.WriteRow([]any{i, "data"}, cols)
	}
	w.Close()

	rows := readXLSXRows(t, w.path)
	if len(rows) != 2001 { // header + 2000 data
		t.Errorf("expected 2001 rows, got %d", len(rows))
	}
}

// ── Offline CSV/SQL integration tests ──

func TestReadCSVTable(t *testing.T) {
	dir := t.TempDir()

	// Write a CSV file
	csvPath := filepath.Join(dir, "scott.emp.csv")
	f, _ := os.Create(csvPath)
	f.WriteString("EMPNO,ENAME,SAL\n")
	f.WriteString("7369,SMITH,5000\n")
	f.WriteString("7499,ALLEN,6000\n")
	f.Close()

	tbl, _ := md.NewTableDef("scott", "emp")
	dt, err := ReadCSVTable(dir, tbl)
	if err != nil {
		t.Fatal(err)
	}

	if len(dt.Columns) != 3 {
		t.Errorf("expected 3 columns, got %d", len(dt.Columns))
	}
	if len(dt.Rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(dt.Rows))
	}
	if dt.Rows[0][0] != "7369" {
		t.Errorf("expected '7369', got %v", dt.Rows[0][0])
	}
	if dt.Rows[1][1] != "ALLEN" {
		t.Errorf("expected 'ALLEN', got %v", dt.Rows[1][1])
	}
}

func TestExportTablesFromData_toSQL(t *testing.T) {
	dir := t.TempDir()
	outDir := t.TempDir()

	// Create CSV input
	csvPath := filepath.Join(dir, "scott.emp.csv")
	f, _ := os.Create(csvPath)
	f.WriteString("id,name\n1,Alice\n2,Bob\n")
	f.Close()

	tbl, _ := md.NewTableDef("scott", "emp")
	dt, _ := ReadCSVTable(dir, tbl)

	exp := New(nil, Config{
		OutputDir: outDir,
		Format:    "sql",
		DBType:    "postgres",
	})
	results, err := exp.ExportTablesFromData([]*DataTable{dt})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Rows != 2 {
		t.Errorf("expected 2 rows, got %d", results[0].Rows)
	}
	if results[0].Error != nil {
		t.Errorf("unexpected error: %v", results[0].Error)
	}

	// Verify output file exists
	data, err := os.ReadFile(results[0].OutputFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "INSERT INTO") {
		t.Error("SQL output should contain INSERT INTO")
	}
	if !strings.Contains(content, "Alice") {
		t.Error("SQL output should contain data")
	}
}

func TestExportTablesFromData_toXLSX(t *testing.T) {
	dir := t.TempDir()
	outDir := t.TempDir()

	csvPath := filepath.Join(dir, "scott.emp.csv")
	f, _ := os.Create(csvPath)
	f.WriteString("id,name\n1,Alice\n2,Bob\n")
	f.Close()

	tbl, _ := md.NewTableDef("scott", "emp")
	dt, _ := ReadCSVTable(dir, tbl)

	exp := New(nil, Config{
		OutputDir: outDir,
		Format:    "xlsx",
	})
	results, err := exp.ExportTablesFromData([]*DataTable{dt})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	info, err := os.Stat(results[0].OutputFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Error("xlsx output should not be empty")
	}
}

func TestExportTablesFromData_toCSV(t *testing.T) {
	dir := t.TempDir()
	outDir := t.TempDir()

	csvPath := filepath.Join(dir, "scott.emp.csv")
	f, _ := os.Create(csvPath)
	f.WriteString("id,name\n1,Alice\n2,Bob\n")
	f.Close()

	tbl, _ := md.NewTableDef("scott", "emp")
	dt, _ := ReadCSVTable(dir, tbl)

	exp := New(nil, Config{
		OutputDir: outDir,
		Format:    "csv",
		CSVHeader: true,
	})
	results, err := exp.ExportTablesFromData([]*DataTable{dt})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(results[0].OutputFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "id,name") {
		t.Errorf("CSV should have header, got: %s", content)
	}
	if !strings.Contains(content, "Alice") {
		t.Error("CSV should contain data")
	}
}

// readXLSXRows reads back an xlsx file and returns all rows.
func readXLSXRows(t *testing.T, path string) [][]string {
	t.Helper()
	// Use excelize to read back
	f, err := excelizeOpenFile(path)
	if err != nil {
		t.Fatalf("open xlsx for verification: %v", err)
	}
	defer f.Close()
	rows, err := f.GetRows(f.GetSheetList()[0])
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	return rows
}
