package service

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

// roundTrip simulates: fill form -> build config -> save YAML -> upload (parse)
// -> extract form values. It returns the extracted values and the detected
// scenario, so callers can assert the form would be repopulated correctly.
func roundTrip(t *testing.T, scenario string, input map[string]string) (map[string]string, string) {
	t.Helper()
	cfg, err := BuildScenarioConfig(scenario, input)
	if err != nil {
		t.Fatalf("build %s: %v", scenario, err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal %s: %v", scenario, err)
	}
	var parsed config.Config
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal %s: %v", scenario, err)
	}
	return ExtractFormValues(&parsed), DetectScenario(&parsed)
}

func checkFields(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()
	for k, w := range want {
		if got[k] != w {
			t.Errorf("field %q: extracted %q, want %q", k, got[k], w)
		}
	}
}

func TestRoundTrip_Migrate(t *testing.T) {
	got, scenario := roundTrip(t, "migrate", map[string]string{
		"metadata_type": "csv", "csv_path": "./testdata/db/oracle/",
		"source_type": "oracle", "source_dsn": "oracle://scott:tiger@h:1521/XEPDB1", "source_schema": "SCOTT",
		"target_type": "postgres", "target_dsn": "host=h dbname=db", "target_schema": "public",
		"tables": "EMP,DEPT", "schema_mapping": "SCOTT:public",
	})
	if scenario != "migrate" {
		t.Errorf("detected %q, want migrate", scenario)
	}
	checkFields(t, got, map[string]string{
		"metadata_type": "csv", "csv_path": "./testdata/db/oracle/",
		"source_type": "oracle", "source_dsn": "oracle://scott:tiger@h:1521/XEPDB1", "source_schema": "SCOTT",
		"target_type": "postgres", "target_dsn": "host=h dbname=db", "target_schema": "public",
		"tables": "EMP,DEPT", "schema_mapping": "SCOTT:public",
	})
}

func TestRoundTrip_ExportDDL(t *testing.T) {
	got, scenario := roundTrip(t, "export-ddl", map[string]string{
		"metadata_type": "csv", "csv_path": "./meta/",
		"target_type": "postgres", "schema_mapping": "SCOTT:public",
	})
	if scenario != "export-ddl" {
		t.Errorf("detected %q, want export-ddl", scenario)
	}
	checkFields(t, got, map[string]string{
		"metadata_type": "csv", "csv_path": "./meta/",
		"target_type": "postgres", "schema_mapping": "SCOTT:public",
	})
}

func TestRoundTrip_ExportDDL_Database(t *testing.T) {
	got, _ := roundTrip(t, "export-ddl", map[string]string{
		"metadata_type": "database",
		"source_type": "oracle", "source_dsn": "oracle://x", "source_schema": "SCOTT",
		"target_type": "postgres",
	})
	checkFields(t, got, map[string]string{
		"metadata_type": "database",
		"source_type":   "oracle", "source_dsn": "oracle://x", "source_schema": "SCOTT",
		"target_type": "postgres",
	})
}

func TestRoundTrip_GenSelect(t *testing.T) {
	got, _ := roundTrip(t, "gen-select", map[string]string{
		"metadata_type": "csv", "csv_path": "./meta/", "target_type": "mysql",
	})
	checkFields(t, got, map[string]string{
		"metadata_type": "csv", "csv_path": "./meta/", "target_type": "mysql",
	})
}

func TestRoundTrip_Export(t *testing.T) {
	got, scenario := roundTrip(t, "export", map[string]string{
		"source_type": "oracle", "source_dsn": "oracle://x", "source_schema": "SCOTT",
		"tables": "EMP",
	})
	if scenario != "export" {
		t.Errorf("detected %q, want export", scenario)
	}
	checkFields(t, got, map[string]string{
		"source_type": "oracle", "source_dsn": "oracle://x", "source_schema": "SCOTT",
		"tables": "EMP",
	})
}

func TestRoundTrip_Import(t *testing.T) {
	got, scenario := roundTrip(t, "import", map[string]string{
		"data_dir": "./output/data/",
		"target_type": "postgres", "target_dsn": "host=h", "target_schema": "public",
	})
	if scenario != "import" {
		t.Errorf("detected %q, want import", scenario)
	}
	checkFields(t, got, map[string]string{
		"data_dir":      "./output/data/",
		"target_type":   "postgres", "target_dsn": "host=h", "target_schema": "public",
	})
}

func TestRoundTrip_ExportInsert_XLSX(t *testing.T) {
	got, _ := roundTrip(t, "export-insert", map[string]string{
		"metadata_type": "xlsx", "xlsx_path": "./schema.xlsx",
		"data_output_dir": "./out/", "target_type": "postgres",
	})
	checkFields(t, got, map[string]string{
		"metadata_type": "xlsx", "xlsx_path": "./schema.xlsx",
		"data_output_dir": "./out/", "target_type": "postgres",
	})
}

func TestRoundTrip_ExportMetadata(t *testing.T) {
	got, _ := roundTrip(t, "export-metadata", map[string]string{
		"source_type": "oracle", "source_dsn": "oracle://x", "source_schema": "SCOTT",
		"format": "csv",
	})
	checkFields(t, got, map[string]string{
		"source_type": "oracle", "source_dsn": "oracle://x", "source_schema": "SCOTT",
	})
	// NOTE: "format" is a CLI flag, not stored in config, so it cannot round-trip.
	if got["format"] == "csv" {
		t.Log("format unexpectedly round-tripped")
	}
}
