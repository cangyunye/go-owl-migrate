package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// Regression: config saved from the web UI (#/config) must survive a serve
// restart and be reloadable by the form via GET /api/v1/config/current.
func TestCurrentConfig_SurvivesRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := service.NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	defer store.Close()
	cfgPath := filepath.Join(t.TempDir(), "migrate.yaml")

	srv := NewServer(Config{Store: store, ConfigPath: cfgPath})
	w := doJSON(t, srv, "POST", "/api/v1/scenarios/migrate/build",
		`{"values":{"source_type":"mysql","source_dsn":"u:p@tcp(127.0.0.1:3306)/db","target_type":"postgres"},"save":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("build status = %d, body = %s", w.Code, w.Body.String())
	}

	srv2 := NewServer(Config{Store: store, ConfigPath: cfgPath})
	w2 := doGet(t, srv2, "/api/v1/config/current")
	if w2.Code != http.StatusOK {
		t.Fatalf("current status = %d", w2.Code)
	}
	var resp struct {
		Scenario string            `json:"scenario"`
		Values   map[string]string `json:"values"`
		Empty    bool              `json:"empty"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Empty {
		t.Fatal("config should not be empty after restart")
	}
	if resp.Scenario != "migrate" {
		t.Errorf("scenario = %q, want migrate", resp.Scenario)
	}
	if resp.Values["source_dsn"] != "u:p@tcp(127.0.0.1:3306)/db" {
		t.Errorf("source_dsn = %q", resp.Values["source_dsn"])
	}
}

// 表单配置可另存进配置库：库文件含服务端解析后的完整 YAML（数据源引用已
// 还原），且当前配置不受影响。
func TestBuildScenario_SaveToLibrary(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := service.NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	defer store.Close()
	cfgPath := filepath.Join(t.TempDir(), "migrate.yaml")
	libDir := filepath.Join(t.TempDir(), "configs", "library")

	srv := NewServer(Config{Store: store, ConfigPath: cfgPath, ConfigDir: libDir})
	w := doJSON(t, srv, "POST", "/api/v1/scenarios/migrate/build",
		`{"values":{"source_type":"mysql","source_dsn":"u:p@tcp(127.0.0.1:3306)/db","target_type":"postgres"},"save":false,"library":"存档-01"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("build status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Saved        bool   `json:"saved"`
		LibrarySaved string `json:"library_saved"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Saved {
		t.Error("save:false must not touch the current config")
	}
	if resp.LibrarySaved != "存档-01" {
		t.Fatalf("library_saved = %q", resp.LibrarySaved)
	}
	data, err := os.ReadFile(filepath.Join(libDir, "存档-01.yaml"))
	if err != nil {
		t.Fatalf("library file: %v", err)
	}
	if !strings.Contains(string(data), "mysql") {
		t.Errorf("library yaml missing source type: %.200s", data)
	}

	// 当前配置仍是空（save:false 未落盘）
	w2 := doGet(t, srv, "/api/v1/config/status")
	var st struct {
		SourceType string `json:"source_type"`
		OnDisk     bool   `json:"on_disk"`
	}
	json.Unmarshal(w2.Body.Bytes(), &st)
	if st.SourceType != "" && st.OnDisk {
		t.Errorf("current config unexpectedly written: %+v", st)
	}
}
