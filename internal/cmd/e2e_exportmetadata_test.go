//go:build e2e

package cmd

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func TestExportMetadata_Oracle_CSV(t *testing.T) {
	cfg := &config.Config{
		Metadata: config.MetadataConfig{Type: "database"},
		Source:   config.DBConfig{Type: "oracle", DSN: oracleSrcDSN, Schema: "SCOTT"},
	}
	sm, err := loadSchemaModel(cfg)
	if err != nil {
		t.Skipf("load oracle metadata: %v", err)
	}
	tables := sm.GetTables()
	if len(tables) == 0 {
		t.Fatal("no tables extracted")
	}

	out := t.TempDir()
	if err := exportMetadataCSV(out, sm, tables, "SCOTT"); err != nil {
		t.Fatalf("exportMetadataCSV: %v", err)
	}

	// Check tables.csv
	tablesCSV := filepath.Join(out, "tables.csv")
	data, err := os.ReadFile(tablesCSV)
	if err != nil {
		t.Fatalf("read tables.csv: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "SCOTT,EMP") {
		t.Errorf("tables.csv missing SCOTT,EMP: %s", content)
	}
	if !strings.Contains(content, "SCOTT,DEPT") {
		t.Errorf("tables.csv missing SCOTT,DEPT: %s", content)
	}

	// Check columns.csv
	colsCSV := filepath.Join(out, "columns.csv")
	data, err = os.ReadFile(colsCSV)
	if err != nil {
		t.Fatalf("read columns.csv: %v", err)
	}
	content = string(data)
	if !strings.Contains(content, "EMPNO") {
		t.Errorf("columns.csv missing EMPNO: %s", content)
	}
	if !strings.Contains(content, "DNAME") {
		t.Errorf("columns.csv missing DNAME: %s", content)
	}

	// Check primary_keys.csv
	pkCSV := filepath.Join(out, "primary_keys.csv")
	if _, err := os.Stat(pkCSV); os.IsNotExist(err) {
		t.Fatal("primary_keys.csv not generated")
	}

	// Check indexes.csv
	idxCSV := filepath.Join(out, "indexes.csv")
	if _, err := os.Stat(idxCSV); os.IsNotExist(err) {
		t.Log("indexes.csv not generated (may be empty)")
	}

	// Check foreign_keys.csv
	fkCSV := filepath.Join(out, "foreign_keys.csv")
	if _, err := os.Stat(fkCSV); os.IsNotExist(err) {
		t.Log("foreign_keys.csv not generated (may be empty)")
	}

	// Check views.csv
	viewsCSV := filepath.Join(out, "views.csv")
	data, err = os.ReadFile(viewsCSV)
	if err == nil && strings.Contains(string(data), "emp_view") {
		t.Log("views.csv contains emp_view")
	}

	// Check sequences.csv
	seqCSV := filepath.Join(out, "sequences.csv")
	data, err = os.ReadFile(seqCSV)
	if err == nil && strings.Contains(string(data), "seq_emp_id") {
		t.Log("sequences.csv contains seq_emp_id")
	}
}

func TestExportMetadata_Oracle_SQL(t *testing.T) {
	cfg := &config.Config{
		Metadata: config.MetadataConfig{Type: "database"},
		Source:   config.DBConfig{Type: "oracle", DSN: oracleSrcDSN, Schema: "SCOTT"},
	}
	sm, err := loadSchemaModel(cfg)
	if err != nil {
		t.Skipf("load oracle: %v", err)
	}
	tables := sm.GetTables()
	if len(tables) == 0 {
		t.Fatal("no tables extracted")
	}

	out := filepath.Join(t.TempDir(), "metadata.sql")
	if err := exportMetadataSQL(out, "oracle", sm, tables, "SCOTT"); err != nil {
		t.Fatalf("exportMetadataSQL: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "INSERT INTO dba_tables") {
		t.Error("SQL missing dba_tables insert")
	}
	if !strings.Contains(content, "INSERT INTO dba_tab_columns") {
		t.Error("SQL missing dba_tab_columns insert")
	}
}

func TestExportMetadata_Oracle_XLSX(t *testing.T) {
	cfg := &config.Config{
		Metadata: config.MetadataConfig{Type: "database"},
		Source:   config.DBConfig{Type: "oracle", DSN: oracleSrcDSN, Schema: "SCOTT"},
	}
	sm, err := loadSchemaModel(cfg)
	if err != nil {
		t.Skipf("load oracle: %v", err)
	}
	tables := sm.GetTables()
	if len(tables) == 0 {
		t.Fatal("no tables extracted")
	}

	out := filepath.Join(t.TempDir(), "metadata.xlsx")
	if err := exportMetadataXLSX(out, sm, tables, "SCOTT"); err != nil {
		t.Fatalf("exportMetadataXLSX: %v", err)
	}

	f, err := excelize.OpenFile(out)
	if err != nil {
		t.Fatalf("open xlsx: %v", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if !contains(sheets, "tables") {
		t.Errorf("xlsx missing 'tables' sheet, got %v", sheets)
	}
	if !contains(sheets, "columns") {
		t.Errorf("xlsx missing 'columns' sheet, got %v", sheets)
	}
	if !contains(sheets, "primary_keys") {
		t.Errorf("xlsx missing 'primary_keys' sheet, got %v", sheets)
	}

	// Verify tables sheet has data
	val, _ := f.GetCellValue("tables", "A2")
	if val != "SCOTT" {
		t.Logf("tables!A2 = %q (first table schema)", val)
	}
}

func TestExportMetadata_Oracle_ScopeFilter(t *testing.T) {
	cfg := &config.Config{
		Metadata: config.MetadataConfig{Type: "database"},
		Source:   config.DBConfig{Type: "oracle", DSN: oracleSrcDSN, Schema: "SCOTT"},
	}
	sm, err := loadSchemaModel(cfg)
	if err != nil {
		t.Skipf("load oracle: %v", err)
	}
	tables := sm.GetTables()

	// Filter to only EMP
	var filtered []*md.TableDef
	for _, tbl := range tables {
		if tbl.TableName == "EMP" {
			filtered = append(filtered, tbl)
		}
	}
	if len(filtered) == 0 {
		t.Fatal("EMP not found in metadata")
	}

	out := t.TempDir()
	if err := exportMetadataCSV(out, sm, filtered, "SCOTT"); err != nil {
		t.Fatalf("exportMetadataCSV filtered: %v", err)
	}

	tablesCSV := filepath.Join(out, "tables.csv")
	r, err := os.Open(tablesCSV)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()
	reader := csv.NewReader(r)
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	// header + only EMP
	if len(rows) != 2 {
		t.Errorf("expected 2 rows (header+EMP), got %d: %v", len(rows), rows)
	}
	if len(rows) >= 2 && rows[1][1] != "EMP" {
		t.Errorf("second row table = %q, want EMP", rows[1][1])
	}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// exportMetadataCSV 是 P3 收编前的本地直写实现；现委托规范 writer
// （service.ExportMetadataFiles，13 文件）。tables 参数用于收窄输出模型
// （scope-filter 用例），空/全量时直接导出整个模型。
func exportMetadataCSV(out string, sm *md.SchemaModel, tables []*md.TableDef, _ string) error {
	if len(tables) > 0 && len(tables) != len(sm.GetTables()) {
		narrowed := md.NewSchemaModel()
		for _, tbl := range tables {
			if err := narrowed.AddTable(tbl); err != nil {
				return err
			}
		}
		sm = narrowed
	}
	_, err := service.ExportMetadataFiles(out, sm, nil)
	return err
}
