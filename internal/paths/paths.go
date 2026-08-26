package paths

import (
	"os"
	"path/filepath"
)

var homeDirFunc = os.UserHomeDir

const (
	EnvHome   = "OWL_MIGRATE_HOME"
	EnvConfig = "OWL_MIGRATE_CONFIG"
	EnvDBPath = "OWL_MIGRATE_DB_PATH"
	EnvLogDir = "OWL_MIGRATE_LOG_DIR"
)

func Home() string {
	if v := os.Getenv(EnvHome); v != "" {
		os.MkdirAll(v, 0755)
		return v
	}
	home, err := homeDirFunc()
	if err != nil || home == "" {
		home = "/tmp"
	}
	dir := filepath.Join(home, ".owl", "migrate")
	os.MkdirAll(dir, 0755)
	return dir
}

func ConfigFile() string {
	if v := os.Getenv(EnvConfig); v != "" {
		return v
	}
	return filepath.Join(Home(), "migrate.yaml")
}

func DBPath() string {
	if v := os.Getenv(EnvDBPath); v != "" {
		return v
	}
	return filepath.Join(Home(), "owl-migrate.db")
}

func LogDir() string {
	if v := os.Getenv(EnvLogDir); v != "" {
		return v
	}
	return filepath.Join(Home(), "logs")
}

func ConfigLibraryDir() string {
	return filepath.Join(Home(), "configs", "library")
}

func DataSourcesDir() string {
	return filepath.Join(Home(), "datasources")
}

func TempDir() string {
	return filepath.Join(Home(), "temp")
}

func HeartbeatPath() string {
	return filepath.Join(TempDir(), "owl-migrate-master.heartbeat")
}

func ServeLockPath() string {
	return filepath.Join(Home(), "serve.lock")
}

func ResolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if _, err := os.Stat("migrate.yaml"); err == nil {
		return "migrate.yaml"
	}
	if v := os.Getenv(EnvConfig); v != "" {
		return v
	}
	return filepath.Join(Home(), "migrate.yaml")
}
