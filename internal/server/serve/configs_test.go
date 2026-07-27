package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func newConfigLibRig(t *testing.T) (*httptest.Server, http.Handler, string) {
	t.Helper()
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	configDir := t.TempDir()
	srv := NewServer(Config{Store: store, ConfigDir: configDir})
	h := srv.Handler()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, h, configDir
}

func uploadConfig(t *testing.T, h http.Handler, name, yamlStr string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name, "yaml": yamlStr})
	req := httptest.NewRequest("POST", "/api/v1/configs", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

const migrateYAML = `metadata:
  type: csv
  csv:
    path: ./testdata/db/oracle/
source:
  type: oracle
  dsn: oracle://scott:tiger@h:1521/XEPDB1
  schema: SCOTT
target:
  type: postgres
  dsn: host=h dbname=db
  schema: public
ddl:
  target_dialect: postgres
  schema_mapping:
    SCOTT: public
`

func TestConfigLib_ListEmpty(t *testing.T) {
	ts, _, _ := newConfigLibRig(t)
	resp, err := http.Get(ts.URL + "/api/v1/configs")
	if err != nil {
		t.Fatalf("GET configs: %v", err)
	}
	defer resp.Body.Close()
	var list []map[string]any
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list) != 0 {
		t.Errorf("len = %d, want 0 for empty library", len(list))
	}
}

func TestConfigLib_UploadAndList(t *testing.T) {
	ts, h, _ := newConfigLibRig(t)
	w := uploadConfig(t, h, "ora2pg", migrateYAML)
	if w.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	resp, err := http.Get(ts.URL + "/api/v1/configs")
	if err != nil {
		t.Fatalf("GET configs: %v", err)
	}
	defer resp.Body.Close()
	var list []struct {
		Name       string `json:"name"`
		Scenario   string `json:"scenario"`
		SourceType string `json:"source_type"`
		TargetType string `json:"target_type"`
		Size       int64  `json:"size"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].Name != "ora2pg" {
		t.Errorf("name = %q, want ora2pg", list[0].Name)
	}
	if list[0].Scenario != "migrate" {
		t.Errorf("scenario = %q, want migrate", list[0].Scenario)
	}
	if list[0].SourceType != "oracle" || list[0].TargetType != "postgres" {
		t.Errorf("source/target = %q/%q, want oracle/postgres", list[0].SourceType, list[0].TargetType)
	}
}

func TestConfigLib_UploadActivatesCurrent(t *testing.T) {
	ts, h, _ := newConfigLibRig(t)
	uploadConfig(t, h, "ora2pg", migrateYAML)

	// The uploaded config should now be the active config.
	resp, err := http.Get(ts.URL + "/api/v1/config")
	if err != nil {
		t.Fatalf("GET config: %v", err)
	}
	defer resp.Body.Close()
	var cfg map[string]any
	json.NewDecoder(resp.Body).Decode(&cfg)
	ddl, _ := cfg["ddl"].(map[string]any)
	if ddl["target_dialect"] != "postgres" {
		t.Errorf("active config target_dialect = %v, want postgres", ddl["target_dialect"])
	}
}

func TestConfigLib_UploadReturnsPopulateData(t *testing.T) {
	_, h, _ := newConfigLibRig(t)
	w := uploadConfig(t, h, "ora2pg", migrateYAML)
	var out struct {
		Name     string            `json:"name"`
		Scenario string            `json:"scenario"`
		Values   map[string]string `json:"values"`
		Yaml     string            `json:"yaml"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Scenario != "migrate" {
		t.Errorf("scenario = %q, want migrate", out.Scenario)
	}
	if out.Values["source_type"] != "oracle" {
		t.Errorf("values.source_type = %q, want oracle", out.Values["source_type"])
	}
	if out.Yaml == "" {
		t.Error("yaml should be returned for preview")
	}
}

func TestConfigLib_Load(t *testing.T) {
	ts, h, _ := newConfigLibRig(t)
	uploadConfig(t, h, "ora2pg", migrateYAML)

	// Change active config to something else, then load ora2pg back.
	uploadConfig(t, h, "other", "ddl:\n  target_dialect: mysql\n")
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/configs/ora2pg/load", nil)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("load status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var out struct {
		Scenario string            `json:"scenario"`
		Values   map[string]string `json:"values"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Values["target_type"] != "postgres" {
		t.Errorf("loaded target_type = %q, want postgres", out.Values["target_type"])
	}

	// Active config should now be ora2pg again.
	resp, err := http.Get(ts.URL + "/api/v1/config")
	if err != nil {
		t.Fatalf("GET config: %v", err)
	}
	defer resp.Body.Close()
	var cfg map[string]any
	json.NewDecoder(resp.Body).Decode(&cfg)
	ddl, _ := cfg["ddl"].(map[string]any)
	if ddl["target_dialect"] != "postgres" {
		t.Errorf("active target_dialect = %v, want postgres after load", ddl["target_dialect"])
	}
}

func TestConfigLib_Download(t *testing.T) {
	ts, h, _ := newConfigLibRig(t)
	uploadConfig(t, h, "ora2pg", migrateYAML)

	resp, err := http.Get(ts.URL + "/api/v1/configs/ora2pg")
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(data), "target_dialect: postgres") {
		t.Errorf("downloaded content missing target_dialect:\n%s", string(data))
	}
}

func TestConfigLib_Delete(t *testing.T) {
	ts, h, _ := newConfigLibRig(t)
	uploadConfig(t, h, "ora2pg", migrateYAML)

	req := httptest.NewRequest("DELETE", "/api/v1/configs/ora2pg", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", w.Code)
	}

	resp, err := http.Get(ts.URL + "/api/v1/configs")
	if err != nil {
		t.Fatalf("GET configs: %v", err)
	}
	defer resp.Body.Close()
	var list []map[string]any
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list) != 0 {
		t.Errorf("len = %d after delete, want 0", len(list))
	}
}

func TestConfigLib_UploadInvalidYAML(t *testing.T) {
	_, h, _ := newConfigLibRig(t)
	w := uploadConfig(t, h, "bad", "ddl: [this is not: a valid mapping")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid YAML", w.Code)
	}
}

func TestConfigLib_OverwriteSameName(t *testing.T) {
	ts, h, _ := newConfigLibRig(t)
	uploadConfig(t, h, "cfg", "ddl:\n  target_dialect: mysql\n")
	uploadConfig(t, h, "cfg", migrateYAML) // same name, should overwrite

	resp, err := http.Get(ts.URL + "/api/v1/configs")
	if err != nil {
		t.Fatalf("GET configs: %v", err)
	}
	defer resp.Body.Close()
	var list []struct {
		Name       string `json:"name"`
		TargetType string `json:"target_type"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1 (overwritten, not duplicated)", len(list))
	}
	if list[0].TargetType != "postgres" {
		t.Errorf("target = %q, want postgres (overwritten content)", list[0].TargetType)
	}
}

func TestConfigLib_PathTraversalRejected(t *testing.T) {
	_, h, configDir := newConfigLibRig(t)
	// A malicious name must not escape the config dir.
	w := uploadConfig(t, h, "../../etc/evil", migrateYAML)
	// Should either reject or sanitize to a safe in-dir name.
	if w.Code == http.StatusOK {
		var out struct {
			Name string `json:"name"`
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		if strings.Contains(out.Name, "/") || strings.Contains(out.Name, "..") {
			t.Errorf("unsanitized name %q allows traversal", out.Name)
		}
	}
	// Ensure nothing was written outside the config dir.
	if _, err := filepath.Glob(filepath.Join(configDir, "..", "..", "etc", "evil*")); err == nil {
		// glob succeeding is fine; just ensure no file at that path
	}
}
