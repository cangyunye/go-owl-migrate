package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stubHome(t *testing.T, dir string) {
	t.Helper()
	orig := homeDirFunc
	homeDirFunc = func() (string, error) { return dir, nil }
	t.Cleanup(func() { homeDirFunc = orig })
}

func TestHomeDefault(t *testing.T) {
	fake := t.TempDir()
	stubHome(t, fake)
	t.Setenv(EnvHome, "")
	got := Home()
	want := filepath.Join(fake, ".owl", "migrate")
	if got != want {
		t.Errorf("Home() = %q, want %q", got, want)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("Home() dir not created: %v", err)
	}
}

func TestHomeEnvOverride(t *testing.T) {
	custom := t.TempDir()
	t.Setenv(EnvHome, custom)
	if got := Home(); got != custom {
		t.Errorf("Home() = %q, want %q", got, custom)
	}
}

func TestHomeFallbackOnErr(t *testing.T) {
	orig := homeDirFunc
	homeDirFunc = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { homeDirFunc = orig })
	t.Setenv(EnvHome, "")
	got := Home()
	if !strings.HasPrefix(got, "/tmp") {
		t.Errorf("Home() = %q, want /tmp prefix", got)
	}
}

func TestConfigFileDefault(t *testing.T) {
	stubHome(t, "/fakehome")
	t.Setenv(EnvConfig, "")
	t.Setenv(EnvHome, "")
	want := filepath.Join("/fakehome", ".owl", "migrate", "migrate.yaml")
	if got := ConfigFile(); got != want {
		t.Errorf("ConfigFile() = %q, want %q", got, want)
	}
}

func TestConfigFileEnvOverride(t *testing.T) {
	t.Setenv(EnvConfig, "/custom/my.yaml")
	if got := ConfigFile(); got != "/custom/my.yaml" {
		t.Errorf("ConfigFile() = %q, want /custom/my.yaml", got)
	}
}

func TestDBPathDefault(t *testing.T) {
	stubHome(t, "/fakehome")
	t.Setenv(EnvDBPath, "")
	t.Setenv(EnvHome, "")
	want := filepath.Join("/fakehome", ".owl", "migrate", "owl-migrate.db")
	if got := DBPath(); got != want {
		t.Errorf("DBPath() = %q, want %q", got, want)
	}
}

func TestDBPathEnvOverride(t *testing.T) {
	t.Setenv(EnvDBPath, "/custom/jobs.db")
	if got := DBPath(); got != "/custom/jobs.db" {
		t.Errorf("DBPath() = %q, want /custom/jobs.db", got)
	}
}

func TestLogDirDefault(t *testing.T) {
	stubHome(t, "/fakehome")
	t.Setenv(EnvLogDir, "")
	t.Setenv(EnvHome, "")
	want := filepath.Join("/fakehome", ".owl", "migrate", "logs")
	if got := LogDir(); got != want {
		t.Errorf("LogDir() = %q, want %q", got, want)
	}
}

func TestLogDirEnvOverride(t *testing.T) {
	t.Setenv(EnvLogDir, "/var/log/owl")
	if got := LogDir(); got != "/var/log/owl" {
		t.Errorf("LogDir() = %q, want /var/log/owl", got)
	}
}

func TestHomeCascadesToSubPaths(t *testing.T) {
	custom := t.TempDir()
	t.Setenv(EnvHome, custom)
	t.Setenv(EnvConfig, "")
	t.Setenv(EnvDBPath, "")
	t.Setenv(EnvLogDir, "")

	if got := ConfigFile(); got != filepath.Join(custom, "migrate.yaml") {
		t.Errorf("ConfigFile() = %q", got)
	}
	if got := DBPath(); got != filepath.Join(custom, "owl-migrate.db") {
		t.Errorf("DBPath() = %q", got)
	}
	if got := LogDir(); got != filepath.Join(custom, "logs") {
		t.Errorf("LogDir() = %q", got)
	}
	if got := ConfigLibraryDir(); got != filepath.Join(custom, "configs", "library") {
		t.Errorf("ConfigLibraryDir() = %q", got)
	}
	if got := TempDir(); got != filepath.Join(custom, "temp") {
		t.Errorf("TempDir() = %q", got)
	}
	if got := HeartbeatPath(); got != filepath.Join(custom, "temp", "owl-migrate-master.heartbeat") {
		t.Errorf("HeartbeatPath() = %q", got)
	}
}

func TestResolveConfigPathExplicit(t *testing.T) {
	if got := ResolveConfigPath("/explicit/path.yaml"); got != "/explicit/path.yaml" {
		t.Errorf("ResolveConfigPath() = %q", got)
	}
}

func TestResolveConfigPathCWDPriority(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(orig) })

	os.WriteFile("migrate.yaml", []byte("test"), 0644)
	t.Setenv(EnvConfig, "")
	t.Setenv(EnvHome, "")

	if got := ResolveConfigPath(""); got != "migrate.yaml" {
		t.Errorf("ResolveConfigPath() = %q, want migrate.yaml (CWD)", got)
	}
}

func TestResolveConfigPathFallbackToHome(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(orig) })

	stubHome(t, "/fakehome")
	t.Setenv(EnvConfig, "")
	t.Setenv(EnvHome, "")

	want := filepath.Join("/fakehome", ".owl", "migrate", "migrate.yaml")
	if got := ResolveConfigPath(""); got != want {
		t.Errorf("ResolveConfigPath() = %q, want %q", got, want)
	}
}

func TestResolveConfigPathEnvMiddle(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(orig) })

	t.Setenv(EnvConfig, "/env/config.yaml")
	t.Setenv(EnvHome, "")

	if got := ResolveConfigPath(""); got != "/env/config.yaml" {
		t.Errorf("ResolveConfigPath() = %q, want /env/config.yaml", got)
	}
}

func TestNoConflictWithGoOwl(t *testing.T) {
	stubHome(t, "/fakehome")
	t.Setenv(EnvHome, "")
	t.Setenv(EnvConfig, "")
	t.Setenv(EnvDBPath, "")
	t.Setenv(EnvLogDir, "")

	goOwlPaths := []string{
		filepath.Join("/fakehome", ".owl", "owl.db"),
		filepath.Join("/fakehome", ".owl", "config.yaml"),
		filepath.Join("/fakehome", ".owl", "nodes.json"),
		filepath.Join("/fakehome", ".owl", "logs"),
	}
	migratePaths := []string{
		ConfigFile(), DBPath(), LogDir(), ConfigLibraryDir(), TempDir(), HeartbeatPath(),
	}
	for _, mp := range migratePaths {
		for _, gp := range goOwlPaths {
			if mp == gp {
				t.Errorf("conflict: migrate path %q equals go-owl path %q", mp, gp)
			}
		}
		if !strings.Contains(mp, filepath.Join(".owl", "migrate")) {
			t.Errorf("migrate path %q not under .owl/migrate/", mp)
		}
	}
}
