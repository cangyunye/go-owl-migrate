package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/paths"
)

func resetRootState(t *testing.T) {
	t.Helper()
	origCfg := cfgFile
	t.Cleanup(func() { cfgFile = origCfg })
}

func writeTestConfig(t *testing.T, dir, csvPath string) string {
	t.Helper()
	content := `metadata:
  type: csv
  csv:
    path: ` + csvPath + `
ddl:
  target_dialect: postgres
`
	p := filepath.Join(dir, "migrate.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return p
}

func absTestdataCSV(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../../testdata/csv/")
	if err != nil {
		t.Fatalf("abs testdata: %v", err)
	}
	return abs
}

func TestE2E_ConfigResolution_CWDPriority(t *testing.T) {
	resetRootState(t)
	csvDir := absTestdataCSV(t)

	workDir := t.TempDir()
	writeTestConfig(t, workDir, csvDir)

	homeDir := t.TempDir()
	t.Setenv(paths.EnvHome, homeDir)
	t.Setenv(paths.EnvConfig, "")

	orig, _ := os.Getwd()
	os.Chdir(workDir)
	t.Cleanup(func() { os.Chdir(orig) })

	cfgFile = ""
	rootCmd.SetArgs([]string{"validate"})
	rootCmd.Execute()
	if cfgFile != "migrate.yaml" {
		t.Errorf("cfgFile = %q, want migrate.yaml (CWD)", cfgFile)
	}
}

func TestE2E_ConfigResolution_EnvOverride(t *testing.T) {
	resetRootState(t)
	csvDir := absTestdataCSV(t)

	envDir := t.TempDir()
	envCfg := writeTestConfig(t, envDir, csvDir)

	homeDir := t.TempDir()
	t.Setenv(paths.EnvHome, homeDir)
	t.Setenv(paths.EnvConfig, envCfg)

	emptyDir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(emptyDir)
	t.Cleanup(func() { os.Chdir(orig) })

	cfgFile = ""
	rootCmd.SetArgs([]string{"validate"})
	rootCmd.Execute()
	if cfgFile != envCfg {
		t.Errorf("cfgFile = %q, want %q (env)", cfgFile, envCfg)
	}
}

func TestE2E_ConfigResolution_CLIFlagHighest(t *testing.T) {
	resetRootState(t)
	csvDir := absTestdataCSV(t)

	flagDir := t.TempDir()
	flagCfg := writeTestConfig(t, flagDir, csvDir)

	homeDir := t.TempDir()
	t.Setenv(paths.EnvHome, homeDir)
	t.Setenv(paths.EnvConfig, "/should/not/use/this.yaml")

	emptyDir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(emptyDir)
	t.Cleanup(func() { os.Chdir(orig) })

	cfgFile = ""
	rootCmd.SetArgs([]string{"validate", "-c", flagCfg})
	rootCmd.Execute()
	if cfgFile != flagCfg {
		t.Errorf("cfgFile = %q, want %q (flag)", cfgFile, flagCfg)
	}
}

func TestE2E_ConfigResolution_FallbackToHome(t *testing.T) {
	resetRootState(t)
	csvDir := absTestdataCSV(t)

	homeDir := t.TempDir()
	migrateDir := filepath.Join(homeDir, ".owl", "migrate")
	os.MkdirAll(migrateDir, 0755)
	writeTestConfig(t, migrateDir, csvDir)

	t.Setenv(paths.EnvHome, "")
	t.Setenv(paths.EnvConfig, "")

	origHome := os.Getenv("HOME")
	t.Setenv("HOME", homeDir)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	emptyDir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(emptyDir)
	t.Cleanup(func() { os.Chdir(orig) })

	cfgFile = ""
	rootCmd.SetArgs([]string{"validate"})
	rootCmd.Execute()
	want := filepath.Join(migrateDir, "migrate.yaml")
	if cfgFile != want {
		t.Errorf("cfgFile = %q, want %q (home fallback)", cfgFile, want)
	}
}

func TestE2E_NoConflictWithGoOwl(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv(paths.EnvHome, "")
	t.Setenv(paths.EnvConfig, "")
	t.Setenv(paths.EnvDBPath, "")
	t.Setenv(paths.EnvLogDir, "")

	origHome := os.Getenv("HOME")
	t.Setenv("HOME", homeDir)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	goOwlFiles := []string{
		filepath.Join(homeDir, ".owl", "owl.db"),
		filepath.Join(homeDir, ".owl", "config.yaml"),
		filepath.Join(homeDir, ".owl", "nodes.json"),
	}

	migratePaths := []string{
		paths.ConfigFile(),
		paths.DBPath(),
		paths.LogDir(),
		paths.ConfigLibraryDir(),
		paths.TempDir(),
		paths.HeartbeatPath(),
	}

	for _, mp := range migratePaths {
		for _, gf := range goOwlFiles {
			if mp == gf {
				t.Errorf("CONFLICT: migrate path %q == go-owl path %q", mp, gf)
			}
		}
		if !strings.Contains(mp, filepath.Join(".owl", "migrate")) {
			t.Errorf("migrate path %q not under .owl/migrate/", mp)
		}
	}
}

func TestE2E_ServeDBPathIsolation(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv(paths.EnvHome, "")
	t.Setenv(paths.EnvDBPath, "")

	origHome := os.Getenv("HOME")
	t.Setenv("HOME", homeDir)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	dbPath := paths.DBPath()
	goOwlDB := filepath.Join(homeDir, ".owl", "owl.db")

	if dbPath == goOwlDB {
		t.Errorf("migrate DB %q conflicts with go-owl DB %q", dbPath, goOwlDB)
	}
	want := filepath.Join(homeDir, ".owl", "migrate", "owl-migrate.db")
	if dbPath != want {
		t.Errorf("DBPath() = %q, want %q", dbPath, want)
	}
}

func TestE2E_InitStillCWD(t *testing.T) {
	resetRootState(t)

	cmd, _, err := rootCmd.Find([]string{"init"})
	if err != nil {
		t.Fatalf("init command not found: %v", err)
	}
	outputFlag := cmd.Flag("output")
	if outputFlag == nil {
		t.Fatal("init should have --output flag")
	}
	if outputFlag.DefValue != "./migrate.yaml" {
		t.Errorf("init --output default = %q, want ./migrate.yaml", outputFlag.DefValue)
	}
}

func TestE2E_TempDirStaysCWDRelative(t *testing.T) {
	resetRootState(t)

	cmd, _, err := rootCmd.Find([]string{"migrate"})
	if err != nil {
		t.Fatalf("migrate command not found: %v", err)
	}
	tempFlag := cmd.Flag("temp-dir")
	if tempFlag == nil {
		t.Fatal("migrate should have --temp-dir flag")
	}
	if tempFlag.DefValue != "./output/temp/" {
		t.Errorf("migrate --temp-dir default = %q, want ./output/temp/", tempFlag.DefValue)
	}

	reportFlag := cmd.Flag("report")
	if reportFlag == nil {
		t.Fatal("migrate should have --report flag")
	}
	if reportFlag.DefValue != "./output/migration_report.json" {
		t.Errorf("migrate --report default = %q, want ./output/migration_report.json", reportFlag.DefValue)
	}
}

func TestE2E_OutputDirsStayCWDRelative(t *testing.T) {
	resetRootState(t)

	cases := []struct {
		args    []string
		flag    string
		wantDef string
	}{
		{[]string{"export", "ddl"}, "output", "./output/ddl/"},
		{[]string{"export", "data"}, "output", "./output/data/"},
		{[]string{"export", "insert"}, "output", "./output/insert/"},
		{[]string{"gen-select"}, "output", "./output/select/"},
		{[]string{"export-metadata"}, "output", "./output/metadata/"},
	}
	for _, tc := range cases {
		cmd, _, err := rootCmd.Find(tc.args)
		if err != nil {
			t.Fatalf("command %v not found: %v", tc.args, err)
		}
		f := cmd.Flag(tc.flag)
		if f == nil {
			t.Errorf("command %v missing --%s flag", tc.args, tc.flag)
			continue
		}
		if f.DefValue != tc.wantDef {
			t.Errorf("command %v --%s default = %q, want %q", tc.args, tc.flag, f.DefValue, tc.wantDef)
		}
	}
}

func TestE2E_HeartbeatUnderMigrateHome(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv(paths.EnvHome, "")

	origHome := os.Getenv("HOME")
	t.Setenv("HOME", homeDir)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	hb := paths.HeartbeatPath()
	want := filepath.Join(homeDir, ".owl", "migrate", "temp", "owl-migrate-master.heartbeat")
	if hb != want {
		t.Errorf("HeartbeatPath() = %q, want %q", hb, want)
	}

	goOwlHB := filepath.Join(os.TempDir(), "owl-migrate-master.heartbeat")
	if hb == goOwlHB {
		t.Errorf("heartbeat should not be in OS temp dir (old behavior)")
	}
}

func TestE2E_MigrateHomeEnvCascades(t *testing.T) {
	custom := t.TempDir()
	t.Setenv(paths.EnvHome, custom)
	t.Setenv(paths.EnvConfig, "")
	t.Setenv(paths.EnvDBPath, "")
	t.Setenv(paths.EnvLogDir, "")

	if got := paths.ConfigFile(); got != filepath.Join(custom, "migrate.yaml") {
		t.Errorf("ConfigFile() = %q", got)
	}
	if got := paths.DBPath(); got != filepath.Join(custom, "owl-migrate.db") {
		t.Errorf("DBPath() = %q", got)
	}
	if got := paths.LogDir(); got != filepath.Join(custom, "logs") {
		t.Errorf("LogDir() = %q", got)
	}
	if got := paths.HeartbeatPath(); got != filepath.Join(custom, "temp", "owl-migrate-master.heartbeat") {
		t.Errorf("HeartbeatPath() = %q", got)
	}
}
