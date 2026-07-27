package master

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/service"
)

type SpawnRequest struct {
	JobID      string
	JobType    string
	Mode       string // for migrate: "direct" | "sql-out"
	ConfigPath string
	DBPath     string
	ParentPID  int
	TempDir    string
}

type Spawner interface {
	// Spawn starts a worker and returns its PID plus a wait function that
	// blocks until the worker exits, returning its exit error (if any).
	Spawn(req SpawnRequest) (pid int, wait func() error, err error)
}

type Config struct {
	Store      *service.JobStore
	Spawner    Spawner
	TempDir    string
	DBPath     string
	HeartbeatPath string
}

type StartJobResponse struct {
	JobID     string `json:"job_id"`
	PID       int    `json:"pid"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

type Master struct {
	store         *service.JobStore
	spawner       Spawner
	tempDir       string
	dbPath        string
	heartbeatPath string
}

func New(cfg Config) *Master {
	hbPath := cfg.HeartbeatPath
	if hbPath == "" {
		hbPath = filepath.Join(os.TempDir(), "owl-migrate-master.heartbeat")
	}
	return &Master{
		store:         cfg.Store,
		spawner:       cfg.Spawner,
		tempDir:       cfg.TempDir,
		dbPath:        cfg.DBPath,
		heartbeatPath: hbPath,
	}
}

func (m *Master) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", m.handleHealth)
	mux.HandleFunc("POST /api/v1/jobs", m.handleStartJob)
	mux.HandleFunc("DELETE /api/v1/jobs/{id}", m.handleCancelJob)
	return mux
}

func (m *Master) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (m *Master) handleStartJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type       string          `json:"type"`
		Mode       string          `json:"mode"`
		Config     json.RawMessage `json:"config"`
		ResumeFrom string          `json:"resume_from"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Type == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "type is required"})
		return
	}

	jobID := "job-" + randomHex(8)
	configJSON := string(req.Config)

	if err := m.store.CreateJob(jobID, req.Type, configJSON); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	jobDir := filepath.Join(m.tempDir, jobID)
	os.MkdirAll(jobDir, 0755)

	configPath := filepath.Join(jobDir, "config.yaml")
	if err := writeConfigYAML(req.Config, configPath); err != nil {
		m.store.UpdateJobStatus(jobID, "failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write config: " + err.Error()})
		return
	}

	pid, wait, err := m.spawner.Spawn(SpawnRequest{
		JobID:      jobID,
		JobType:    req.Type,
		Mode:       req.Mode,
		ConfigPath: configPath,
		DBPath:     m.dbPath,
		ParentPID:  os.Getpid(),
		TempDir:    jobDir,
	})
	if err != nil {
		m.store.UpdateJobStatus(jobID, "failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "spawn worker: " + err.Error()})
		return
	}

	m.store.UpdateJobPID(jobID, pid)
	go m.monitorWorker(jobID, wait)

	writeJSON(w, http.StatusCreated, StartJobResponse{
		JobID:     jobID,
		PID:       pid,
		Status:    "running",
		CreatedAt: time.Now().Format(time.RFC3339),
	})
}

// monitorWorker waits for a worker to exit and finalizes the job status if the
// worker did not set it itself. Workers that report progress (migrate) set
// their own terminal status; this is the safety net for crashes and cancels.
func (m *Master) monitorWorker(jobID string, wait func() error) {
	exitErr := wait()

	job, err := m.store.GetJob(jobID)
	if err != nil {
		return
	}
	switch job.Status {
	case "completed", "failed", "cancelled", "interrupted":
		return // worker already finalized
	case "cancelling":
		m.store.UpdateJobStatus(jobID, "cancelled")
		return
	}
	// Still "running": worker exited without reporting. Infer from exit code.
	if exitErr != nil {
		m.store.WriteEvent(jobID, "error", "", "", 0, "worker exited: "+exitErr.Error())
		m.store.UpdateJobStatus(jobID, "failed")
	} else {
		m.store.UpdateJobStatus(jobID, "completed")
	}
}

func (m *Master) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")

	job, err := m.store.GetJob(jobID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}

	if job.Status != "running" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("job is %s, not running", job.Status)})
		return
	}

	m.store.UpdateJobStatus(jobID, "cancelling")

	if job.PID > 0 {
		killProcess(job.PID)
	}

	writeJSON(w, http.StatusOK, map[string]string{"job_id": jobID, "status": "cancelling"})
}

func (m *Master) WriteHeartbeat() error {
	content := fmt.Sprintf("%d %d", os.Getpid(), time.Now().Unix())
	return os.WriteFile(m.heartbeatPath, []byte(content), 0644)
}

func (m *Master) HeartbeatPath() string {
	return m.heartbeatPath
}

func writeConfigYAML(rawJSON json.RawMessage, path string) error {
	var m map[string]any
	if err := json.Unmarshal(rawJSON, &m); err != nil {
		return err
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func killProcess(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	proc.Signal(os.Kill)
}

func selectPort(preferred []int, ranges [][2]int) (int, error) {
	for _, port := range preferred {
		if isPortFree(port) {
			return port, nil
		}
	}
	for _, r := range ranges {
		for port := r[0]; port <= r[1]; port++ {
			if isPortFree(port) {
				return port, nil
			}
		}
	}
	return 0, fmt.Errorf("no available port found in ranges %v", ranges)
}

func isPortFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)[:n]
}
