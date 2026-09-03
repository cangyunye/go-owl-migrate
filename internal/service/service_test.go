package service

import (
	"testing"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func TestLoadMetadata_CSV(t *testing.T) {
	cfg := &config.Config{
		Metadata: config.MetadataConfig{
			Type: "csv",
			CSV: config.CSVConfig{
				Path:               "../../testdata/csv/",
				ColumnNameMatching: "case_insensitive",
			},
		},
	}

	sm, err := LoadMetadata(cfg)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	tables := sm.GetTables()
	if len(tables) == 0 {
		t.Fatal("expected at least 1 table from SCOTT CSV metadata")
	}

	found := false
	for _, tbl := range tables {
		if tbl.TableName == "EMP" {
			found = true
			if len(tbl.Columns) == 0 {
				t.Error("EMP table has no columns")
			}
			break
		}
	}
	if !found {
		t.Error("EMP table not found in loaded metadata")
	}
}

func TestLoadMetadata_UnsupportedType(t *testing.T) {
	cfg := &config.Config{
		Metadata: config.MetadataConfig{Type: "yaml"},
	}
	_, err := LoadMetadata(cfg)
	if err == nil {
		t.Fatal("expected error for unsupported metadata type")
	}
}

func TestBuildPKMap(t *testing.T) {
	sm := md.NewSchemaModel()
	tbl := &md.TableDef{TableSchema: "SCOTT", TableName: "EMP"}
	tbl.AddPrimaryKey("PK_EMP", "EMPNO")
	sm.AddTable(tbl)

	tbl2 := &md.TableDef{TableSchema: "SCOTT", TableName: "DEPT"}
	tbl2.AddPrimaryKey("PK_DEPT", "DEPTNO")
	sm.AddTable(tbl2)

	tbl3 := &md.TableDef{TableSchema: "SCOTT", TableName: "NOPK"}
	sm.AddTable(tbl3)

	pkMap := BuildPKMap(sm)

	if len(pkMap) != 2 {
		t.Fatalf("len(pkMap) = %d, want 2 (NOPK has no PK)", len(pkMap))
	}
	if pkMap["SCOTT.EMP"][0] != "EMPNO" {
		t.Errorf("pkMap[SCOTT.EMP] = %v, want [EMPNO]", pkMap["SCOTT.EMP"])
	}
	if pkMap["SCOTT.DEPT"][0] != "DEPTNO" {
		t.Errorf("pkMap[SCOTT.DEPT] = %v, want [DEPTNO]", pkMap["SCOTT.DEPT"])
	}
}

func TestFilterTables(t *testing.T) {
	tables := []*md.TableDef{
		{TableSchema: "SCOTT", TableName: "EMP"},
		{TableSchema: "SCOTT", TableName: "DEPT"},
		{TableSchema: "SCOTT", TableName: "BONUS"},
	}

	tests := []struct {
		name    string
		include []string
		want    int
	}{
		{"wildcard", []string{"*"}, 3},
		{"exact", []string{"SCOTT.EMP", "SCOTT.DEPT"}, 2},
		{"single", []string{"SCOTT.BONUS"}, 1},
		{"none", []string{"SCOTT.NONEXIST"}, 0},
		{"empty list = keep all", []string{}, 3}, // 与 UI "留空 = 全部表" 一致
		{"lowercase exact", []string{"scott.emp"}, 1},
		{"glob", []string{"SCOTT.E*"}, 1},
		{"schema wildcard", []string{"SCOTT.*"}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterTables(tables, tt.include)
			if len(result) != tt.want {
				t.Errorf("FilterTables(%v) len = %d, want %d", tt.include, len(result), tt.want)
			}
		})
	}
}

func TestToBuildOptions(t *testing.T) {
	cfg := &config.Config{
		DDL: config.DDLConfig{
			TargetDialect:      "postgres",
			SchemaMapping:      map[string]string{"SCOTT": "public"},
			IncludeComments:    true,
			IncludeIfNotExists: true,
			NoQuoteIdentifiers: true,
		},
	}

	opts := ToBuildOptions(cfg)

	if opts.TargetDialect != "postgres" {
		t.Errorf("TargetDialect = %q, want postgres", opts.TargetDialect)
	}
	if opts.SchemaMapping["SCOTT"] != "public" {
		t.Errorf("SchemaMapping[SCOTT] = %q, want public", opts.SchemaMapping["SCOTT"])
	}
	if !opts.IncludeComments {
		t.Error("IncludeComments should be true")
	}
	if !opts.IncludeIfNotExists {
		t.Error("IncludeIfNotExists should be true")
	}
	if !opts.NoQuoteIdentifiers {
		t.Error("NoQuoteIdentifiers should be true")
	}
}

func TestConnectTimeout(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.DBConfig
		want time.Duration
	}{
		{"default", config.DBConfig{}, 30 * time.Second},
		{"custom", config.DBConfig{ConnectTimeout: "10s"}, 10 * time.Second},
		{"invalid", config.DBConfig{ConnectTimeout: "bad"}, 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConnectTimeout(tt.cfg)
			if got != tt.want {
				t.Errorf("ConnectTimeout = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryTimeout(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.DBConfig
		want time.Duration
	}{
		{"default", config.DBConfig{}, 0},
		{"custom", config.DBConfig{QueryTimeout: "5m"}, 5 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := QueryTimeout(tt.cfg)
			if got != tt.want {
				t.Errorf("QueryTimeout = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOpenDB_Unsupported(t *testing.T) {
	_, err := OpenDB(config.DBConfig{Type: "mongodb", DSN: "foo"})
	if err == nil {
		t.Fatal("expected error for unsupported db type")
	}
}

func TestToBuildOptions_ZeroConfig(t *testing.T) {
	cfg := &config.Config{}
	opts := ToBuildOptions(cfg)

	if opts.TargetDialect != "" {
		t.Errorf("TargetDialect = %q, want empty", opts.TargetDialect)
	}
	if opts.IncludeComments {
		t.Error("IncludeComments should be false")
	}
	if opts.IncludeIfNotExists {
		t.Error("IncludeIfNotExists should be false")
	}
	if opts.NoQuoteIdentifiers {
		t.Error("NoQuoteIdentifiers should be false")
	}
	if len(opts.SchemaMapping) != 0 {
		t.Errorf("SchemaMapping should be empty, got %v", opts.SchemaMapping)
	}
}
