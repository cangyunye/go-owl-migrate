package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := service.NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	srv := NewServer(Config{
		Store:     store,
		MasterURL: "",
	})
	return srv
}

func doGet(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func doJSON(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestHandler_Health(t *testing.T) {
	srv := newTestServer(t)

	w := doGet(t, srv, "/api/v1/health")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want ok", resp["status"])
	}
}

func TestHandler_ListJobs_Empty(t *testing.T) {
	srv := newTestServer(t)

	w := doGet(t, srv, "/api/v1/jobs")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var jobs []service.Job
	json.Unmarshal(w.Body.Bytes(), &jobs)
	if jobs == nil {
		t.Fatal("expected empty array, got null")
	}
	if len(jobs) != 0 {
		t.Errorf("len(jobs) = %d, want 0", len(jobs))
	}
}

func TestHandler_ListJobs_WithData(t *testing.T) {
	srv := newTestServer(t)
	srv.store.CreateJob("job-1", "migrate", "{}")
	srv.store.CreateJob("job-2", "export", "{}")

	w := doGet(t, srv, "/api/v1/jobs")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var jobs []service.Job
	json.Unmarshal(w.Body.Bytes(), &jobs)
	if len(jobs) != 2 {
		t.Fatalf("len(jobs) = %d, want 2", len(jobs))
	}
}

func TestHandler_GetJob(t *testing.T) {
	srv := newTestServer(t)
	srv.store.CreateJob("job-get", "migrate", `{"ddl":{"target_dialect":"postgres"}}`)

	w := doGet(t, srv, "/api/v1/jobs/job-get")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var job service.Job
	json.Unmarshal(w.Body.Bytes(), &job)
	if job.JobID != "job-get" {
		t.Errorf("JobID = %q, want job-get", job.JobID)
	}
	if job.Type != "migrate" {
		t.Errorf("Type = %q, want migrate", job.Type)
	}
}

func TestHandler_GetJob_NotFound(t *testing.T) {
	srv := newTestServer(t)

	w := doGet(t, srv, "/api/v1/jobs/nonexistent")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandler_GetJobEvents(t *testing.T) {
	srv := newTestServer(t)
	srv.store.CreateJob("job-ev", "migrate", "{}")
	srv.store.WriteEvent("job-ev", "export_complete", "SCOTT", "EMP", 5000, "done")
	srv.store.WriteEvent("job-ev", "import_complete", "SCOTT", "EMP", 4998, "done")

	w := doGet(t, srv, "/api/v1/jobs/job-ev/events")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var events []service.ProgressEvent
	json.Unmarshal(w.Body.Bytes(), &events)
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].Seq != 1 || events[1].Seq != 2 {
		t.Errorf("seqs = %d,%d, want 1,2", events[0].Seq, events[1].Seq)
	}
}

func TestHandler_GetJobEvents_AfterSeq(t *testing.T) {
	srv := newTestServer(t)
	srv.store.CreateJob("job-seq", "migrate", "{}")
	for i := 1; i <= 5; i++ {
		srv.store.WriteEvent("job-seq", "export_complete", "S", "T", int64(i), "")
	}

	w := doGet(t, srv, "/api/v1/jobs/job-seq/events?after_seq=3")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var events []service.ProgressEvent
	json.Unmarshal(w.Body.Bytes(), &events)
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2 (seq 4,5)", len(events))
	}
}

func TestHandler_GetJobCheckpoints(t *testing.T) {
	srv := newTestServer(t)
	srv.store.CreateJob("job-cp", "migrate", "{}")
	srv.store.WriteCheckpoint("job-cp", "SCOTT", "EMP", true, 5000, true, 4998, "SUCCESS", "")

	w := doGet(t, srv, "/api/v1/jobs/job-cp/checkpoints")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var cps []service.JobCheckpoint
	json.Unmarshal(w.Body.Bytes(), &cps)
	if len(cps) != 1 {
		t.Fatalf("len(checkpoints) = %d, want 1", len(cps))
	}
	if cps[0].TableName != "EMP" || cps[0].ExportedRows != 5000 {
		t.Errorf("checkpoint = %+v, want EMP/5000", cps[0])
	}
}

func TestHandler_GetDialects(t *testing.T) {
	srv := newTestServer(t)

	w := doGet(t, srv, "/api/v1/dialects")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var dialects []string
	json.Unmarshal(w.Body.Bytes(), &dialects)
	if len(dialects) == 0 {
		t.Fatal("expected at least 1 dialect")
	}

	found := false
	for _, d := range dialects {
		if d == "postgres" {
			found = true
			break
		}
	}
	if !found {
		t.Error("postgres not found in dialect list")
	}
}

func TestHandler_GetConfig_Default(t *testing.T) {
	srv := newTestServer(t)

	w := doGet(t, srv, "/api/v1/config")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var cfg map[string]any
	json.Unmarshal(w.Body.Bytes(), &cfg)
	if cfg == nil {
		t.Fatal("expected config JSON object")
	}
}

func TestHandler_PutConfig(t *testing.T) {
	srv := newTestServer(t)

	body := `{"ddl":{"target_dialect":"postgres"},"metadata":{"type":"csv","csv":{"path":"./testdata/csv/"}}}`
	w := doJSON(t, srv, "PUT", "/api/v1/config", body)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	w2 := doGet(t, srv, "/api/v1/config")
	var cfg map[string]any
	json.Unmarshal(w2.Body.Bytes(), &cfg)
	ddl, ok := cfg["ddl"].(map[string]any)
	if !ok {
		t.Fatal("config.ddl not found after PUT")
	}
	if ddl["target_dialect"] != "postgres" {
		t.Errorf("target_dialect = %v, want postgres", ddl["target_dialect"])
	}
}

func TestHandler_PutConfig_InvalidJSON(t *testing.T) {
	srv := newTestServer(t)

	w := doJSON(t, srv, "PUT", "/api/v1/config", `{invalid json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandler_MetadataLoad_CSV(t *testing.T) {
	srv := newTestServer(t)

	body := `{"metadata":{"type":"csv","csv":{"path":"../../../testdata/csv/","column_name_matching":"case_insensitive"}}}`
	w := doJSON(t, srv, "POST", "/api/v1/metadata/load", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	tables, ok := resp["tables"].([]any)
	if !ok {
		t.Fatal("response.tables not found")
	}
	if len(tables) == 0 {
		t.Error("expected at least 1 table from SCOTT CSV")
	}
}

func TestHandler_MetadataTables(t *testing.T) {
	srv := newTestServer(t)

	body := `{"metadata":{"type":"csv","csv":{"path":"../../../testdata/csv/","column_name_matching":"case_insensitive"}}}`
	doJSON(t, srv, "POST", "/api/v1/metadata/load", body)

	w := doGet(t, srv, "/api/v1/metadata/tables")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var tables []map[string]any
	json.Unmarshal(w.Body.Bytes(), &tables)
	if len(tables) == 0 {
		t.Error("expected tables after metadata load")
	}
}

func TestHandler_MetadataTables_NotLoaded(t *testing.T) {
	srv := newTestServer(t)

	w := doGet(t, srv, "/api/v1/metadata/tables")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (metadata not loaded)", w.Code)
	}
}
