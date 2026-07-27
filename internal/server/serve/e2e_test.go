package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/server/master"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

type recordingSpawner struct {
	requests []master.SpawnRequest
}

func (r *recordingSpawner) Spawn(req master.SpawnRequest) (int, error) {
	r.requests = append(r.requests, req)
	return 4242, nil
}

// newE2ERig wires a real Server and a real Master (with a recording spawner)
// together over httptest, mirroring the production topology: browser → serve
// HTTP → master IPC → worker spawn.
func newE2ERig(t *testing.T) (*httptest.Server, *service.JobStore, *recordingSpawner) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	store, err := service.NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	spawner := &recordingSpawner{}
	m := master.New(master.Config{
		Store:   store,
		Spawner: spawner,
		TempDir: t.TempDir(),
		DBPath:  dbPath,
	})
	masterTS := httptest.NewServer(m.Handler())
	t.Cleanup(masterTS.Close)

	srv := NewServer(Config{Store: store, MasterURL: masterTS.URL, ConfigPath: filepath.Join(t.TempDir(), "owl-migrate.yaml")})
	serveTS := httptest.NewServer(srv.Handler())
	t.Cleanup(serveTS.Close)

	return serveTS, store, spawner
}

func e2ePost(t *testing.T, ts *httptest.Server, path, body string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestE2E_MigrationJobLifecycle(t *testing.T) {
	ts, store, spawner := newE2ERig(t)

	// 1. Save a config.
	putReq, _ := http.NewRequest("PUT", ts.URL+"/api/v1/config",
		strings.NewReader(`{"ddl":{"target_dialect":"postgres"},"metadata":{"type":"csv"}}`))
	putReq.Header.Set("Content-Type", "application/json")
	cfgResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("PUT config: %v", err)
	}
	cfgResp.Body.Close()

	// 2. Start a migration job via the public API.
	resp, body := e2ePost(t, ts, "/api/v1/migrate", `{}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("start migrate: status %d, body %v", resp.StatusCode, body)
	}
	jobID, _ := body["job_id"].(string)
	if jobID == "" {
		t.Fatal("expected job_id in response")
	}

	// 3. The master must have spawned a worker with the progress flags.
	if len(spawner.requests) != 1 {
		t.Fatalf("spawner called %d times, want 1", len(spawner.requests))
	}
	req := spawner.requests[0]
	if req.JobType != "migrate" {
		t.Errorf("JobType = %q, want migrate", req.JobType)
	}
	if req.JobID != jobID {
		t.Errorf("spawn JobID = %q, want %q", req.JobID, jobID)
	}

	// 4. The job is visible and running via the public API.
	getResp, err := http.Get(ts.URL + "/api/v1/jobs/" + jobID)
	if err != nil {
		t.Fatalf("GET job: %v", err)
	}
	defer getResp.Body.Close()
	var job service.Job
	json.NewDecoder(getResp.Body).Decode(&job)
	if job.Status != "running" {
		t.Errorf("job status = %q, want running", job.Status)
	}
	if job.PID != 4242 {
		t.Errorf("job PID = %d, want 4242", job.PID)
	}

	// 5. Simulate the worker writing progress + completion to the shared store.
	store.WriteEvent(jobID, "export_complete", "SCOTT", "EMP", 14, "done")
	store.UpdateJobStatus(jobID, "completed")

	// 6. Progress events are readable via the public API.
	evResp, err := http.Get(ts.URL + "/api/v1/jobs/" + jobID + "/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer evResp.Body.Close()
	var events []service.ProgressEvent
	json.NewDecoder(evResp.Body).Decode(&events)
	if len(events) != 1 || events[0].EventType != "export_complete" {
		t.Errorf("events = %+v, want 1 export_complete", events)
	}

	// 7. Final status is reflected.
	getResp2, err := http.Get(ts.URL + "/api/v1/jobs/" + jobID)
	if err != nil {
		t.Fatalf("GET job (final): %v", err)
	}
	defer getResp2.Body.Close()
	var job2 service.Job
	json.NewDecoder(getResp2.Body).Decode(&job2)
	if job2.Status != "completed" {
		t.Errorf("final status = %q, want completed", job2.Status)
	}
}

func TestE2E_CancelJob(t *testing.T) {
	ts, store, _ := newE2ERig(t)

	_, body := e2ePost(t, ts, "/api/v1/migrate", `{}`)
	jobID, _ := body["job_id"].(string)

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/jobs/"+jobID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200", resp.StatusCode)
	}

	job, _ := store.GetJob(jobID)
	if job.Status != "cancelling" {
		t.Errorf("status = %q, want cancelling", job.Status)
	}
}

func TestE2E_PagesRenderDistinctContent(t *testing.T) {
	ts, _, _ := newE2ERig(t)

	pages := map[string]string{
		"/":        "数据库迁移工具",
		"/config":  "配置",
		"/migrate": "数据迁移",
		"/jobs":    "任务历史",
	}
	for path, wantTitle := range pages {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, resp.StatusCode)
		}
		if !strings.Contains(string(data), "<h1>"+wantTitle+"</h1>") {
			t.Errorf("GET %s missing title %q", path, wantTitle)
		}
	}
}

func TestE2E_StaticAssetsServed(t *testing.T) {
	ts, _, _ := newE2ERig(t)

	for _, asset := range []string{"/static/css/style.css", "/static/js/app.js"} {
		resp, err := http.Get(ts.URL + asset)
		if err != nil {
			t.Fatalf("GET %s: %v", asset, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", asset, resp.StatusCode)
		}
	}
}

func TestE2E_ScenarioListAndBuild(t *testing.T) {
	ts, _, _ := newE2ERig(t)

	// List scenarios.
	resp, err := http.Get(ts.URL + "/api/v1/scenarios")
	if err != nil {
		t.Fatalf("GET scenarios: %v", err)
	}
	defer resp.Body.Close()
	var list struct {
		Scenarios []struct {
			Name   string `json:"name"`
			Fields []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"fields"`
		} `json:"scenarios"`
		DSNExamples map[string]string `json:"dsn_examples"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Scenarios) < 8 {
		t.Errorf("scenarios = %d, want >= 8", len(list.Scenarios))
	}
	if list.DSNExamples["oracle"] == "" {
		t.Error("missing oracle DSN example")
	}

	// Build a migrate config from form values (preview, no save).
	r, body := e2ePost(t, ts, "/api/v1/scenarios/migrate/build",
		`{"values":{"source_type":"oracle","source_dsn":"oracle://u:p@h:1521/s","source_schema":"SCOTT","target_type":"postgres","target_dsn":"host=h","target_schema":"public","tables":"EMP"},"save":false}`)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("build: status %d, body %v", r.StatusCode, body)
	}
	yamlStr, _ := body["yaml"].(string)
	if !strings.Contains(yamlStr, "target_dialect: postgres") {
		t.Errorf("yaml missing target_dialect, got:\n%s", yamlStr)
	}
	if !strings.Contains(yamlStr, "SCOTT") {
		t.Errorf("yaml missing schema mapping, got:\n%s", yamlStr)
	}
	if body["saved"].(bool) {
		t.Error("preview should not save")
	}
}

func TestE2E_ScenarioConditionalFields(t *testing.T) {
	ts, _, _ := newE2ERig(t)

	// export-ddl schema should mark source fields conditional on metadata_type.
	resp, err := http.Get(ts.URL + "/api/v1/scenarios/export-ddl")
	if err != nil {
		t.Fatalf("GET scenario: %v", err)
	}
	defer resp.Body.Close()
	var sc struct {
		Fields []struct {
			Name     string `json:"name"`
			ShowWhen *struct {
				Field string `json:"field"`
				Value string `json:"value"`
			} `json:"show_when"`
		} `json:"fields"`
	}
	json.NewDecoder(resp.Body).Decode(&sc)

	foundConditional := false
	for _, f := range sc.Fields {
		if f.Name == "source_dsn" && f.ShowWhen != nil &&
			f.ShowWhen.Field == "metadata_type" && f.ShowWhen.Value == "database" {
			foundConditional = true
		}
	}
	if !foundConditional {
		t.Error("source_dsn should be conditional on metadata_type=database")
	}
}

func TestE2E_DDLGenerateAndDownload(t *testing.T) {
	ts, _, _ := newE2ERig(t)

	// Load metadata + set target dialect.
	e2ePost(t, ts, "/api/v1/metadata/load",
		`{"metadata":{"type":"csv","csv":{"path":"../../../testdata/csv/","column_name_matching":"case_insensitive"}}}`)
	putReq, _ := http.NewRequest("PUT", ts.URL+"/api/v1/config",
		strings.NewReader(`{"ddl":{"target_dialect":"postgres"}}`))
	putReq.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(putReq); err == nil {
		resp.Body.Close()
	}

	// Generate DDL.
	resp, body := e2ePost(t, ts, "/api/v1/ddl/generate", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ddl generate: status %d, body %v", resp.StatusCode, body)
	}
	count, _ := body["count"].(float64)
	if count < 3 {
		t.Errorf("ddl count = %v, want >= 3 (EMP/DEPT/BONUS tables)", count)
	}
	files, _ := body["files"].([]any)
	if len(files) == 0 {
		t.Fatal("no DDL files returned")
	}
	first := files[0].(map[string]any)
	if !strings.Contains(first["content"].(string), "CREATE TABLE") {
		t.Error("DDL content missing CREATE TABLE")
	}

	// Download as ZIP.
	dlResp, err := http.Get(ts.URL + "/api/v1/ddl/download")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusOK {
		t.Errorf("download status = %d, want 200", dlResp.StatusCode)
	}
	if ct := dlResp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content-type = %q, want application/zip", ct)
	}
}

func TestE2E_SelectGenerate(t *testing.T) {
	ts, _, _ := newE2ERig(t)
	e2ePost(t, ts, "/api/v1/metadata/load",
		`{"metadata":{"type":"csv","csv":{"path":"../../../testdata/csv/","column_name_matching":"case_insensitive"}}}`)
	putReq, _ := http.NewRequest("PUT", ts.URL+"/api/v1/config",
		strings.NewReader(`{"ddl":{"target_dialect":"postgres"}}`))
	putReq.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(putReq); err == nil {
		resp.Body.Close()
	}

	resp, body := e2ePost(t, ts, "/api/v1/select/generate", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("select generate: status %d, body %v", resp.StatusCode, body)
	}
	count, _ := body["count"].(float64)
	if count < 3 {
		t.Errorf("select count = %v, want >= 3", count)
	}
}

func TestE2E_GenerateRequiresMetadata(t *testing.T) {
	ts, _, _ := newE2ERig(t)
	resp, _ := e2ePost(t, ts, "/api/v1/ddl/generate", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("ddl generate without metadata: status %d, want 400", resp.StatusCode)
	}
}

func TestE2E_MetadataValidateAndDetail(t *testing.T) {
	ts, _, _ := newE2ERig(t)
	e2ePost(t, ts, "/api/v1/metadata/load",
		`{"metadata":{"type":"csv","csv":{"path":"../../../testdata/csv/","column_name_matching":"case_insensitive"}}}`)

	// Validate.
	vResp, err := http.Get(ts.URL + "/api/v1/metadata/validate")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	defer vResp.Body.Close()
	var vOut map[string]any
	json.NewDecoder(vResp.Body).Decode(&vOut)
	if _, ok := vOut["errors"]; !ok {
		t.Error("validate response missing errors field")
	}

	// Table detail.
	dResp, err := http.Get(ts.URL + "/api/v1/metadata/tables/SCOTT/EMP")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	defer dResp.Body.Close()
	if dResp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", dResp.StatusCode)
	}
	var detail struct {
		Name    string `json:"name"`
		Columns []struct {
			Name string `json:"name"`
		} `json:"columns"`
		PrimaryKeys []string `json:"primary_keys"`
	}
	json.NewDecoder(dResp.Body).Decode(&detail)
	if detail.Name != "EMP" {
		t.Errorf("name = %q, want EMP", detail.Name)
	}
	if len(detail.Columns) == 0 {
		t.Error("EMP has no columns")
	}
	if len(detail.PrimaryKeys) == 0 || detail.PrimaryKeys[0] != "EMPNO" {
		t.Errorf("primary_keys = %v, want [EMPNO]", detail.PrimaryKeys)
	}
}

func TestE2E_MigrateModeThreadedToSpawner(t *testing.T) {
	ts, _, spawner := newE2ERig(t)

	// sql-out mode should reach the spawner with Mode set.
	e2ePost(t, ts, "/api/v1/migrate", `{"mode":"sql-out"}`)
	if len(spawner.requests) != 1 {
		t.Fatalf("spawner called %d times, want 1", len(spawner.requests))
	}
	if spawner.requests[0].Mode != "sql-out" {
		t.Errorf("Mode = %q, want sql-out", spawner.requests[0].Mode)
	}

	// direct mode (default).
	e2ePost(t, ts, "/api/v1/migrate", `{}`)
	if spawner.requests[1].Mode != "" {
		t.Errorf("Mode = %q, want empty (direct)", spawner.requests[1].Mode)
	}
}

func TestE2E_ConfigUpload(t *testing.T) {
	ts, _, _ := newE2ERig(t)

	resp, body := e2ePost(t, ts, "/api/v1/config/upload",
		"{\"yaml\":\"ddl:\\n  target_dialect: mysql\\nmetadata:\\n  type: csv\\n\"}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: status %d, body %v", resp.StatusCode, body)
	}
	if body["status"] != "uploaded" {
		t.Errorf("status = %v, want uploaded", body["status"])
	}

	// The uploaded config is now the current config.
	getResp, err := http.Get(ts.URL + "/api/v1/config")
	if err != nil {
		t.Fatalf("GET config: %v", err)
	}
	defer getResp.Body.Close()
	var cfg map[string]any
	json.NewDecoder(getResp.Body).Decode(&cfg)
	ddl := cfg["ddl"].(map[string]any)
	if ddl["target_dialect"] != "mysql" {
		t.Errorf("target_dialect = %v, want mysql", ddl["target_dialect"])
	}
}

func TestE2E_ConfigUpload_InvalidYAML(t *testing.T) {
	ts, _, _ := newE2ERig(t)
	resp, _ := e2ePost(t, ts, "/api/v1/config/upload", "{\"yaml\":\":::bad\"}")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid yaml: status %d, want 400", resp.StatusCode)
	}
}

func TestE2E_ConfigPersistenceAndStatus(t *testing.T) {
	ts, _, _ := newE2ERig(t)

	// Saving a scenario config persists it to disk.
	_, body := e2ePost(t, ts, "/api/v1/scenarios/migrate/build",
		`{"values":{"source_type":"oracle","source_dsn":"o","source_schema":"S","target_type":"postgres","target_dsn":"t"},"save":true}`)
	path, _ := body["path"].(string)
	if path == "" {
		t.Fatal("expected a saved path")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("config file %q was not written to disk", path)
	}

	// Status reflects the persisted config.
	stResp, err := http.Get(ts.URL + "/api/v1/config/status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	defer stResp.Body.Close()
	var st map[string]any
	json.NewDecoder(stResp.Body).Decode(&st)
	if st["on_disk"] != true {
		t.Errorf("on_disk = %v, want true", st["on_disk"])
	}
	if st["target_dialect"] != "postgres" {
		t.Errorf("target_dialect = %v, want postgres", st["target_dialect"])
	}
}

func TestE2E_ConfigDownload(t *testing.T) {
	ts, _, _ := newE2ERig(t)
	e2ePost(t, ts, "/api/v1/scenarios/migrate/build",
		`{"values":{"source_type":"oracle","source_dsn":"o","target_type":"postgres","target_dsn":"t"},"save":true}`)

	resp, err := http.Get(ts.URL + "/api/v1/config/download")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d, want 200", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(data), "target_dialect: postgres") {
		t.Errorf("downloaded YAML missing target_dialect:\n%s", string(data))
	}
}

func TestE2E_FullConfigToMetadataFlow(t *testing.T) {
	ts, _, _ := newE2ERig(t)

	// Load metadata from the bundled SCOTT CSV fixture.
	resp, body := e2ePost(t, ts, "/api/v1/metadata/load",
		`{"metadata":{"type":"csv","csv":{"path":"../../../testdata/csv/","column_name_matching":"case_insensitive"}}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metadata load: status %d, body %v", resp.StatusCode, body)
	}
	count, _ := body["table_count"].(float64)
	if count < 3 {
		t.Errorf("table_count = %v, want >= 3 (EMP, DEPT, BONUS)", count)
	}

	// Tables are now queryable.
	tablesResp, err := http.Get(ts.URL + "/api/v1/metadata/tables")
	if err != nil {
		t.Fatalf("GET tables: %v", err)
	}
	defer tablesResp.Body.Close()
	var tables []map[string]any
	json.NewDecoder(tablesResp.Body).Decode(&tables)
	if len(tables) < 3 {
		t.Errorf("len(tables) = %d, want >= 3", len(tables))
	}
}
