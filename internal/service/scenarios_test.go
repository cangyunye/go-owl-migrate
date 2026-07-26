package service

import (
	"testing"
)

func TestScenarioSchemas_AllPresent(t *testing.T) {
	scenarios := ScenarioSchemas()
	want := map[string]bool{
		"migrate": false, "export-ddl": false, "gen-select": false,
		"export": false, "import": false, "export-insert": false,
		"export-metadata": false, "validate": false,
	}
	for _, s := range scenarios {
		if _, ok := want[s.Name]; ok {
			want[s.Name] = true
		}
		if s.Label == "" {
			t.Errorf("scenario %q has empty label", s.Name)
		}
		if len(s.Fields) == 0 {
			t.Errorf("scenario %q has no fields", s.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("scenario %q missing", name)
		}
	}
}

func TestScenarioSchema_Lookup(t *testing.T) {
	s, ok := ScenarioSchema("migrate")
	if !ok {
		t.Fatal("migrate scenario not found")
	}
	if s.Command != "owl-migrate migrate" {
		t.Errorf("Command = %q", s.Command)
	}

	_, ok = ScenarioSchema("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent scenario")
	}
}

func TestScenarioFields_HaveTypes(t *testing.T) {
	for _, s := range ScenarioSchemas() {
		for _, f := range s.Fields {
			if f.Type != "select" && f.Type != "text" {
				t.Errorf("%s.%s has invalid type %q", s.Name, f.Name, f.Type)
			}
			if f.Type == "select" && len(f.Options) == 0 {
				t.Errorf("%s.%s is select but has no options", s.Name, f.Name)
			}
		}
	}
}

func TestBuildScenarioConfig_Migrate(t *testing.T) {
	cfg, err := BuildScenarioConfig("migrate", map[string]string{
		"source_type": "oracle", "source_dsn": "oracle://u:p@h:1521/svc", "source_schema": "SCOTT",
		"target_type": "postgres", "target_dsn": "host=h dbname=db", "target_schema": "public",
		"tables": "EMP, DEPT",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if cfg.Source.Type != "oracle" || cfg.Source.Schema != "SCOTT" {
		t.Errorf("source = %+v", cfg.Source)
	}
	if cfg.Target.Type != "postgres" || cfg.Target.Schema != "public" {
		t.Errorf("target = %+v", cfg.Target)
	}
	if cfg.DDL.TargetDialect != "postgres" {
		t.Errorf("target_dialect = %q", cfg.DDL.TargetDialect)
	}
	if cfg.DDL.SchemaMapping["SCOTT"] != "public" {
		t.Errorf("schema_mapping = %v", cfg.DDL.SchemaMapping)
	}
	if len(cfg.Export.Tables.Include) != 2 {
		t.Errorf("tables = %v, want [EMP DEPT]", cfg.Export.Tables.Include)
	}
}

func TestBuildScenarioConfig_Migrate_DefaultTargetSchema(t *testing.T) {
	cfg, _ := BuildScenarioConfig("migrate", map[string]string{
		"source_type": "mysql", "source_dsn": "u:p@tcp(h)/db", "source_schema": "mydb",
		"target_type": "postgres", "target_dsn": "host=h",
	})
	if cfg.Target.Schema != "mydb" {
		t.Errorf("target schema = %q, want mydb (falls back to source)", cfg.Target.Schema)
	}
}

func TestBuildScenarioConfig_ExportDDL_CSV(t *testing.T) {
	cfg, err := BuildScenarioConfig("export-ddl", map[string]string{
		"metadata_type": "csv", "csv_path": "./meta/", "target_type": "mysql",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if cfg.Metadata.Type != "csv" || cfg.Metadata.CSV.Path != "./meta/" {
		t.Errorf("metadata = %+v", cfg.Metadata)
	}
	if cfg.DDL.TargetDialect != "mysql" {
		t.Errorf("target_dialect = %q", cfg.DDL.TargetDialect)
	}
}

func TestBuildScenarioConfig_ExportDDL_Database(t *testing.T) {
	cfg, _ := BuildScenarioConfig("export-ddl", map[string]string{
		"metadata_type": "database", "source_type": "oracle", "source_dsn": "oracle://x",
		"source_schema": "SCOTT", "target_type": "postgres",
	})
	if cfg.Metadata.Type != "database" {
		t.Errorf("metadata type = %q", cfg.Metadata.Type)
	}
	if cfg.Source.Type != "oracle" {
		t.Errorf("source type = %q", cfg.Source.Type)
	}
}

func TestBuildScenarioConfig_Import(t *testing.T) {
	cfg, err := BuildScenarioConfig("import", map[string]string{
		"data_dir": "./data/", "target_type": "postgres", "target_dsn": "host=h", "target_schema": "public",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if cfg.Import.SourceDir != "./data/" {
		t.Errorf("source_dir = %q", cfg.Import.SourceDir)
	}
	if cfg.Target.Type != "postgres" {
		t.Errorf("target = %+v", cfg.Target)
	}
}

func TestBuildScenarioConfig_Unknown(t *testing.T) {
	_, err := BuildScenarioConfig("bogus", map[string]string{})
	if err == nil {
		t.Fatal("expected error for unknown scenario")
	}
}

func TestSplitTables(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"*", 1},
		{"", 1},
		{"EMP", 1},
		{"EMP, DEPT, BONUS", 3},
	}
	for _, tt := range tests {
		got := splitTables(tt.in)
		if len(got) != tt.want {
			t.Errorf("splitTables(%q) = %v, want %d items", tt.in, got, tt.want)
		}
	}
}
