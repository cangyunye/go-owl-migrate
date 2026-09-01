package serve

import (
	"encoding/json"
	"net/http"
	"path/filepath"
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
