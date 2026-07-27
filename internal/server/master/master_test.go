package master

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/service"
)

type mockSpawner struct {
	spawned []SpawnRequest
	pid     int
	waitErr error
	release chan struct{} // wait blocks until closed, simulating a running worker
}

func (m *mockSpawner) Spawn(req SpawnRequest) (int, func() error, error) {
	m.spawned = append(m.spawned, req)
	if m.pid == 0 {
		m.pid = 99999
	}
	wait := func() error {
		if m.release != nil {
			<-m.release
		}
		return m.waitErr
	}
	return m.pid, wait, nil
}

func newTestMaster(t *testing.T) (*Master, *mockSpawner) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := service.NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	spawner := &mockSpawner{release: make(chan struct{})}
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
	// Occupy one port and free another, so the choice is deterministic
	// regardless of what else is running on the machine.
	busyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen busy: %v", err)
	}
	defer busyLn.Close()
	busyPort := busyLn.Addr().(*net.TCPAddr).Port

	freeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free: %v", err)
	}
	freePort := freeLn.Addr().(*net.TCPAddr).Port
	freeLn.Close() // now free

	t.Run("skips occupied preferred port", func(t *testing.T) {
		port, err := selectPort([]int{busyPort, freePort}, nil)
		if err != nil {
			t.Fatalf("selectPort: %v", err)
		}
		if port != freePort {
			t.Errorf("port = %d, want %d (should skip occupied %d)", port, freePort, busyPort)
		}
	})

	t.Run("falls back to range", func(t *testing.T) {
		port, err := selectPort([]int{busyPort}, [][2]int{{freePort, freePort}})
		if err != nil {
			t.Fatalf("selectPort: %v", err)
		}
		if port != freePort {
			t.Errorf("port = %d, want %d (from fallback range)", port, freePort)
		}
	})

	t.Run("errors when nothing available", func(t *testing.T) {
		_, err := selectPort([]int{busyPort}, [][2]int{{busyPort, busyPort}})
		if err == nil {
			t.Error("expected error when all ports occupied")
		}
	})
}

func waitForStatus(t *testing.T, m *Master, jobID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if job, err := m.store.GetJob(jobID); err == nil && job.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	job, _ := m.store.GetJob(jobID)
	t.Fatalf("job %s status = %q, want %q", jobID, job.Status, want)
}

func TestMaster_WorkerExitFinalizesStatus(t *testing.T) {
	t.Run("clean exit -> completed", func(t *testing.T) {
		m, spawner := newTestMaster(t)
		w := doMasterRequest(t, m, "POST", "/api/v1/jobs", `{"type":"export","config":{}}`)
		var resp StartJobResponse
		json.Unmarshal(w.Body.Bytes(), &resp)

		spawner.waitErr = nil
		close(spawner.release)
		waitForStatus(t, m, resp.JobID, "completed")
	})

	t.Run("error exit -> failed", func(t *testing.T) {
		m, spawner := newTestMaster(t)
		w := doMasterRequest(t, m, "POST", "/api/v1/jobs", `{"type":"export","config":{}}`)
		var resp StartJobResponse
		json.Unmarshal(w.Body.Bytes(), &resp)

		spawner.waitErr = fmt.Errorf("exit status 1")
		close(spawner.release)
		waitForStatus(t, m, resp.JobID, "failed")
	})

	t.Run("cancel -> cancelled", func(t *testing.T) {
		m, spawner := newTestMaster(t)
		w := doMasterRequest(t, m, "POST", "/api/v1/jobs", `{"type":"migrate","config":{}}`)
		var resp StartJobResponse
		json.Unmarshal(w.Body.Bytes(), &resp)

		doMasterRequest(t, m, "DELETE", "/api/v1/jobs/"+resp.JobID, "")
		close(spawner.release)
		waitForStatus(t, m, resp.JobID, "cancelled")
	})
}
