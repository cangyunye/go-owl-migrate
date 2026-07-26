package master

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/service"
)

type mockSpawner struct {
	spawned []SpawnRequest
	pid     int
}

func (m *mockSpawner) Spawn(req SpawnRequest) (int, error) {
	m.spawned = append(m.spawned, req)
	if m.pid == 0 {
		m.pid = 99999
	}
	return m.pid, nil
}

func newTestMaster(t *testing.T) (*Master, *mockSpawner) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := service.NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	spawner := &mockSpawner{}
	m := New(Config{
		Store:   store,
		Spawner: spawner,
		TempDir: t.TempDir(),
		DBPath:  dbPath,
	})
	return m, spawner
}

func doMasterRequest(t *testing.T, m *Master, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)
	return w
}

func TestMaster_Health(t *testing.T) {
	m, _ := newTestMaster(t)

	w := doMasterRequest(t, m, "GET", "/health", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want ok", resp["status"])
	}
}

func TestMaster_StartJob(t *testing.T) {
	m, spawner := newTestMaster(t)

	body := `{"type":"migrate","config":{"ddl":{"target_dialect":"postgres"},"metadata":{"type":"csv"}}}`
	w := doMasterRequest(t, m, "POST", "/api/v1/jobs", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", w.Code, w.Body.String())
	}

	var resp StartJobResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.JobID == "" {
		t.Error("JobID should not be empty")
	}
	if resp.PID != 99999 {
		t.Errorf("PID = %d, want 99999 (mock)", resp.PID)
	}
	if resp.Status != "running" {
		t.Errorf("Status = %q, want running", resp.Status)
	}

	if len(spawner.spawned) != 1 {
		t.Fatalf("spawner called %d times, want 1", len(spawner.spawned))
	}
	req := spawner.spawned[0]
	if req.JobType != "migrate" {
		t.Errorf("JobType = %q, want migrate", req.JobType)
	}
	if req.JobID != resp.JobID {
		t.Errorf("spawn JobID = %q, want %q", req.JobID, resp.JobID)
	}

	job, err := m.store.GetJob(resp.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != "running" {
		t.Errorf("job status = %q, want running", job.Status)
	}
	if job.PID != 99999 {
		t.Errorf("job PID = %d, want 99999", job.PID)
	}
}

func TestMaster_StartJob_WritesConfigFile(t *testing.T) {
	m, spawner := newTestMaster(t)

	body := `{"type":"export","config":{"export":{"output_dir":"./out/"}}}`
	w := doMasterRequest(t, m, "POST", "/api/v1/jobs", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}

	var resp StartJobResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	req := spawner.spawned[0]
	if req.ConfigPath == "" {
		t.Fatal("ConfigPath should not be empty")
	}
	if _, err := os.Stat(req.ConfigPath); os.IsNotExist(err) {
		t.Errorf("config file %q does not exist", req.ConfigPath)
	}
}

func TestMaster_CancelJob(t *testing.T) {
	m, _ := newTestMaster(t)

	body := `{"type":"migrate","config":{}}`
	w := doMasterRequest(t, m, "POST", "/api/v1/jobs", body)
	var resp StartJobResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	w2 := doMasterRequest(t, m, "DELETE", "/api/v1/jobs/"+resp.JobID, "")
	if w2.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200, body: %s", w2.Code, w2.Body.String())
	}

	var cancelResp map[string]string
	json.Unmarshal(w2.Body.Bytes(), &cancelResp)
	if cancelResp["status"] != "cancelling" {
		t.Errorf("status = %q, want cancelling", cancelResp["status"])
	}

	job, _ := m.store.GetJob(resp.JobID)
	if job.Status != "cancelling" {
		t.Errorf("job status = %q, want cancelling", job.Status)
	}
}

func TestMaster_CancelJob_NotFound(t *testing.T) {
	m, _ := newTestMaster(t)

	w := doMasterRequest(t, m, "DELETE", "/api/v1/jobs/nonexistent", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestMaster_StartJob_InvalidJSON(t *testing.T) {
	m, _ := newTestMaster(t)

	w := doMasterRequest(t, m, "POST", "/api/v1/jobs", `{bad json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestMaster_StartJob_MissingType(t *testing.T) {
	m, _ := newTestMaster(t)

	w := doMasterRequest(t, m, "POST", "/api/v1/jobs", `{"config":{}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (missing type)", w.Code)
	}
}

func TestSelectPort(t *testing.T) {
	tests := []struct {
		name     string
		prefer   []int
		fallback [][2]int
		want     int
	}{
		{"first available", []int{25430, 25431}, [][2]int{{25400, 25499}}, 25430},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, err := selectPort(tt.prefer, tt.fallback)
			if err != nil {
				t.Fatalf("selectPort: %v", err)
			}
			if port != tt.want {
				t.Errorf("port = %d, want %d", port, tt.want)
			}
		})
	}
}
