package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

// onlineTestConfig builds a CSV-metadata config for online init script mode.
func onlineTestConfig(t *testing.T, scriptDir string) *config.Config {
	t.Helper()
	abs := absPath(t, "../../testdata/csv")
	return &config.Config{
		General:  config.GeneralConfig{LogLevel: "info"},
		Metadata: config.MetadataConfig{Type: "csv", CSV: config.CSVConfig{Path: abs, HasHeader: true}},
		Source:   config.DBConfig{Type: "oracle", Schema: "SCOTT"},
		Target:   config.DBConfig{Type: "postgres"},
		DDL: config.DDLConfig{
			TargetDialect: "postgres",
			SourceDialect: "oracle",
			SchemaMapping: map[string]string{"SCOTT": "public"},
		},
		Online: config.OnlineConfig{
			CDC:  config.OnlineCDCConfig{ScriptDir: scriptDir, ChangelogPrefix: "owl_chg_"},
			Sync: config.OnlineSyncConfig{PollInterval: "1s", BatchSize: 100, OnError: "skip"},
		},
	}
}

func absPath(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	return a
}

func TestOnlineInitScriptGeneration(t *testing.T) {
	dir := t.TempDir()
	cfg := onlineTestConfig(t, filepath.Join(dir, "out"))
	if err := runOnlineInit(context.Background(), cfg); err != nil {
		t.Fatalf("runOnlineInit: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatalf("read script dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected generated script files")
	}
	found := false
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(dir, "out", e.Name()))
		s := string(data)
		if strings.Contains(s, "OWL_CHG_EMP") || strings.Contains(s, "owl_chg_emp") || strings.Contains(s, "OWL_SYNC_ROW_EMP") || strings.Contains(s, "owl_sync_row_emp") {
			found = true
		}
	}
	if !found {
		t.Error("no EMP changelog/trigger found in generated scripts")
	}
}

func TestOnlineInitRequireKeyRejects(t *testing.T) {
	cfg := onlineTestConfig(t, t.TempDir())
	cfg.Online.CDC.RequireKey = true
	if err := runOnlineInit(context.Background(), cfg); err == nil {
		t.Fatal("expected error when a table has no primary key and require-key is set")
	}
}
