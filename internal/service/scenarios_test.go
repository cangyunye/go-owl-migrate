package service

import (
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
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

func TestDSNFamilies_AllDialectsMapped(t *testing.T) {
	families := DSNFamilies()
	for d := range config.ValidDialects {
		if families[d] == "" {
			t.Errorf("dialect %q has no DSN family", d)
		}
	}
	// A few behaviour assertions mirroring DSNExamples().
	checks := map[string]string{
		"postgres": familyPostgres, "opengaussdb": familyPostgres,
		"panweidb": familyPostgres, "panweidb-mysql": familyPostgres, "panweidb-oracle": familyPostgres,
		"mysql": familyMySQL, "goldendb-mysql": familyMySQL,
		"oceanbase-mysql": familyOceanBaseMySQL,
		"oracle":          familyOracle, "goldendb-oracle": familyOracle,
		"oceanbase-oracle": familyOceanBaseOracle,
		"sqlite3":          familyFile, "duckdb": familyFile,
	}
	for d, want := range checks {
		if got := families[d]; got != want {
			t.Errorf("DSNFamilies[%q] = %q, want %q", d, got, want)
		}
	}
}

func TestDSNComponentMeta_Builders(t *testing.T) {
	meta := DSNComponentMeta()
	for _, fam := range []string{familyMySQL, familyOracle, familyPostgres, familyOceanBaseMySQL, familyOceanBaseOracle} {
		m, ok := meta[fam]
		if !ok {
			t.Errorf("missing meta for family %q", fam)
			continue
		}
		if m.Builder == "" || m.DBLabel == "" || m.DBPlaceholder == "" || m.Port == "" {
			t.Errorf("family %q has incomplete meta: %+v", fam, m)
		}
	}
	if !meta[familyOceanBaseMySQL].HasCluster {
		t.Error("oceanbase-mysql family should have cluster support")
	}
	if !meta[familyOceanBaseMySQL].HasTenant {
		t.Error("oceanbase-mysql family should have tenant (folded into username)")
	}
	if !meta[familyOceanBaseOracle].HasCluster {
		t.Error("oceanbase-oracle family should have cluster support")
	}
	if meta[familyFile].Builder != "" {
		t.Errorf("file family should have no builder, got %q", meta[familyFile].Builder)
	}
}

func TestScenarioFields_HaveTypes(t *testing.T) {
	for _, s := range ScenarioSchemas() {
		for _, f := range s.Fields {
			if f.Type != "select" && f.Type != "text" && f.Type != "password" {
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

func TestBuildScenarioConfig_ExportDDL_SchemaMapping(t *testing.T) {
	cfg, _ := BuildScenarioConfig("export-ddl", map[string]string{
		"metadata_type": "csv", "csv_path": "./meta/", "target_type": "postgres",
		"schema_mapping": "SCOTT:public, HR:hr",
	})
	if cfg.DDL.SchemaMapping["SCOTT"] != "public" {
		t.Errorf("SchemaMapping[SCOTT] = %q, want public", cfg.DDL.SchemaMapping["SCOTT"])
	}
	if cfg.DDL.SchemaMapping["HR"] != "hr" {
		t.Errorf("SchemaMapping[HR] = %q, want hr", cfg.DDL.SchemaMapping["HR"])
	}
}

func TestBuildScenarioConfig_ExportDDL_NoSelfMapping(t *testing.T) {
	// A source schema alone must NOT produce a self-mapping (regression).
	cfg, _ := BuildScenarioConfig("export-ddl", map[string]string{
		"metadata_type": "database", "source_type": "oracle", "source_dsn": "oracle://x",
		"source_schema": "billing", "target_type": "postgres",
	})
	if len(cfg.DDL.SchemaMapping) != 0 {
		t.Errorf("SchemaMapping = %v, want empty (no explicit mapping given)", cfg.DDL.SchemaMapping)
	}
}

func TestParseSchemaMapping(t *testing.T) {
	tests := []struct {
		in   string
		want map[string]string
	}{
		{"", map[string]string{}},
		{"SCOTT:public", map[string]string{"SCOTT": "public"}},
		{"SCOTT:public,HR:hr", map[string]string{"SCOTT": "public", "HR": "hr"}},
		{"bad-no-colon", map[string]string{}},
	}
	for _, tt := range tests {
		got := parseSchemaMapping(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("parseSchemaMapping(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for k, v := range tt.want {
			if got[k] != v {
				t.Errorf("parseSchemaMapping(%q)[%q] = %q, want %q", tt.in, k, got[k], v)
			}
		}
	}
}

func TestExtractFormValues_Migrate(t *testing.T) {
	// Build a migrate config, then reverse-map it and check key fields survive.
	cfg, _ := BuildScenarioConfig("migrate", map[string]string{
		"metadata_type": "csv", "csv_path": "./testdata/db/oracle/",
		"source_type": "oracle", "source_dsn": "oracle://scott:tiger@h:1521/XEPDB1", "source_schema": "SCOTT",
		"target_type": "postgres", "target_dsn": "host=h dbname=db", "target_schema": "public",
		"tables": "EMP,DEPT", "schema_mapping": "SCOTT:public",
	})
	v := ExtractFormValues(cfg)

	checks := map[string]string{
		"metadata_type":  "csv",
		"csv_path":       "./testdata/db/oracle/",
		"source_type":    "oracle",
		"source_schema":  "SCOTT",
		"target_type":    "postgres",
		"target_schema":  "public",
		"schema_mapping": "SCOTT:public",
	}
	for k, want := range checks {
		if v[k] != want {
			t.Errorf("ExtractFormValues[%q] = %q, want %q", k, v[k], want)
		}
	}
	if v["tables"] != "EMP,DEPT" {
		t.Errorf("tables = %q, want EMP,DEPT", v["tables"])
	}
}

func TestDetectScenario(t *testing.T) {
	tests := []struct {
		scenario string
		want     string
	}{
		{"migrate", "migrate"},
		{"export", "export"},
		{"import", "import"},
	}
	for _, tt := range tests {
		cfg, _ := BuildScenarioConfig(tt.scenario, map[string]string{
			"metadata_type": "csv", "csv_path": "./m/",
			"source_type": "oracle", "source_dsn": "o", "source_schema": "S",
			"target_type": "postgres", "target_dsn": "t", "target_schema": "public",
			"data_dir": "./data/",
		})
		if got := DetectScenario(cfg); got != tt.want {
			t.Errorf("DetectScenario(%s config) = %q, want %q", tt.scenario, got, tt.want)
		}
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
