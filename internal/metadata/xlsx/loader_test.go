package xlsx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	xlsxPath := filepath.Join(dir, "schema.xlsx")

	f := excelize.NewFile()
	f.SetSheetName("Sheet1", "tables")
	f.SetSheetRow("tables", "A1", &[]interface{}{"TABLE_SCHEMA", "TABLE_NAME", "TABLE_TYPE"})
	f.SetSheetRow("tables", "A2", &[]interface{}{"SCOTT", "EMP", "TABLE"})

	f.NewSheet("columns")
	f.SetSheetRow("columns", "A1", &[]interface{}{"TABLE_SCHEMA", "TABLE_NAME", "COLUMN_NAME", "ORDINAL_POSITION", "DATA_TYPE"})
	f.SetSheetRow("columns", "A2", &[]interface{}{"SCOTT", "EMP", "EMPNO", 1, "NUMBER"})

	f.NewSheet("@EMP")
	f.SetSheetRow("@EMP", "A1", &[]interface{}{"EMPNO"})
	f.SetSheetRow("@EMP", "A2", &[]interface{}{7369})

	if err := f.SaveAs(xlsxPath); err != nil {
		t.Fatalf("save xlsx: %v", err)
	}

	dataDir := filepath.Join(dir, "data")
	sm, err := Load(Config{FilePath: xlsxPath, DataOutputDir: dataDir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tbl := sm.GetTable("SCOTT", "EMP")
	if tbl == nil {
		t.Fatal("expected SCOTT.EMP table")
	}
	cols := tbl.GetColumns()
	if len(cols) != 1 || cols[0].ColumnName != "EMPNO" {
		t.Errorf("unexpected columns: %+v", cols)
	}
	dataFile := filepath.Join(dataDir, "scott.emp.csv")
	if _, err := os.Stat(dataFile); err != nil {
		t.Errorf("expected data CSV %s: %v", dataFile, err)
	}
}

func TestLoad_MissingTablesSheet(t *testing.T) {
	dir := t.TempDir()
	xlsxPath := filepath.Join(dir, "schema.xlsx")
	f := excelize.NewFile()
	f.SetSheetName("Sheet1", "columns")
	f.SetSheetRow("columns", "A1", &[]interface{}{"TABLE_SCHEMA"})
	if err := f.SaveAs(xlsxPath); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := Load(Config{FilePath: xlsxPath, DataOutputDir: dir}); err == nil {
		t.Error("expected error when tables sheet missing")
	}
}
