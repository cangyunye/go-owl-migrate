package serve

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// newOutputRig builds a Server backed by a real JobStore and a temp dir, so
// tests can lay down fake SQL output under <tempDir>/<jobID>/insert/.
func newOutputRig(t *testing.T) (*httptest.Server, *service.JobStore, string) {
	t.Helper()
	tempDir := t.TempDir()
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	srv := NewServer(Config{Store: store, TempDir: tempDir})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, store, tempDir
}

// seedSQLOutput creates <tempDir>/<jobID>/insert/ with the given files and
// marks the job completed in the store.
func seedSQLOutput(t *testing.T, store *service.JobStore, tempDir, jobID string, files map[string]string) string {
	t.Helper()
	if err := store.CreateJob(jobID, "migrate", "{}"); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := store.UpdateJobStatus(jobID, "completed"); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}
	dir := filepath.Join(tempDir, jobID, "insert")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	return dir
}

func TestJobOutput_SQLFiles(t *testing.T) {
	ts, store, tempDir := newOutputRig(t)
	seedSQLOutput(t, store, tempDir, "job-sql1", map[string]string{
		"scott.emp.insert.sql":  "INSERT INTO emp VALUES (1);\nINSERT INTO emp VALUES (2);\n",
		"scott.dept.insert.sql": "INSERT INTO dept VALUES (10);\n",
	})

	resp, err := http.Get(ts.URL + "/api/v1/jobs/job-sql1/output")
	if err != nil {
		t.Fatalf("GET output: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var out struct {
		HasSQL    bool   `json:"has_sql"`
		Dir       string `json:"dir"`
		FileCount int    `json:"file_count"`
		TotalSize int64  `json:"total_size"`
		Files     []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !out.HasSQL {
		t.Error("has_sql = false, want true")
	}
	if out.FileCount != 2 {
		t.Errorf("file_count = %d, want 2", out.FileCount)
	}
	wantSize := int64(len("INSERT INTO emp VALUES (1);\nINSERT INTO emp VALUES (2);\n") + len("INSERT INTO dept VALUES (10);\n"))
	if out.TotalSize != wantSize {
		t.Errorf("total_size = %d, want %d", out.TotalSize, wantSize)
	}
	if len(out.Files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(out.Files))
	}
}

func TestJobOutput_NoSQLForDirectJob(t *testing.T) {
	ts, store, _ := newOutputRig(t)
	// A direct-mode job has no insert/ dir.
	store.CreateJob("job-direct", "migrate", "{}")
	store.UpdateJobStatus("job-direct", "completed")

	resp, err := http.Get(ts.URL + "/api/v1/jobs/job-direct/output")
	if err != nil {
		t.Fatalf("GET output: %v", err)
	}
	defer resp.Body.Close()

	var out struct {
		HasSQL    bool `json:"has_sql"`
		FileCount int  `json:"file_count"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.HasSQL {
		t.Error("has_sql = true, want false for a direct-mode job")
	}
	if out.FileCount != 0 {
		t.Errorf("file_count = %d, want 0", out.FileCount)
	}
}

func TestJobOutputDownload_TarGz(t *testing.T) {
	ts, store, tempDir := newOutputRig(t)
	want := map[string]string{
		"scott.emp.insert.sql":  "INSERT INTO emp VALUES (1);\n",
		"scott.dept.insert.sql": "INSERT INTO dept VALUES (10);\n",
	}
	seedSQLOutput(t, store, tempDir, "job-tgz", want)

	resp, err := http.Get(ts.URL + "/api/v1/jobs/job-tgz/output/download?format=tar.gz")
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("content-type = %q, want application/gzip", ct)
	}

	got := readTarGz(t, resp.Body)
	for name, content := range want {
		if got[name] != content {
			t.Errorf("tar entry %q = %q, want %q", name, got[name], content)
		}
	}
	if len(got) != len(want) {
		t.Errorf("tar has %d entries, want %d", len(got), len(want))
	}
}

// readTarGz decompresses a gzip stream and returns tar entry name->content.
func readTarGz(t *testing.T, r io.Reader) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(r)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		data, _ := io.ReadAll(tr)
		out[hdr.Name] = string(data)
	}
	return out
}

func TestJobOutputDownload_Zip(t *testing.T) {
	ts, store, tempDir := newOutputRig(t)
	want := map[string]string{
		"scott.emp.insert.sql":  "INSERT INTO emp VALUES (1);\n",
		"scott.dept.insert.sql": "INSERT INTO dept VALUES (10);\n",
	}
	seedSQLOutput(t, store, tempDir, "job-zip", want)

	resp, err := http.Get(ts.URL + "/api/v1/jobs/job-zip/output/download?format=zip")
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content-type = %q, want application/zip", ct)
	}

	got := readZip(t, resp.Body)
	for name, content := range want {
		if got[name] != content {
			t.Errorf("zip entry %q = %q, want %q", name, got[name], content)
		}
	}
	if len(got) != len(want) {
		t.Errorf("zip has %d entries, want %d", len(got), len(want))
	}
}

// readZip reads a zip stream into a name->content map.
func readZip(t *testing.T, r io.Reader) map[string]string {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip reader: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry: %v", err)
		}
		content, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(content)
	}
	return out
}

func TestJobOutputDownload_RawSingleFile(t *testing.T) {
	ts, store, tempDir := newOutputRig(t)
	content := "INSERT INTO emp VALUES (1);\nINSERT INTO emp VALUES (2);\n"
	seedSQLOutput(t, store, tempDir, "job-raw", map[string]string{
		"scott.emp.insert.sql": content,
	})

	resp, err := http.Get(ts.URL + "/api/v1/jobs/job-raw/output/download?format=raw")
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != content {
		t.Errorf("body = %q, want %q", string(body), content)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd == "" {
		t.Error("missing Content-Disposition for raw download")
	}
}

func TestJobOutputDownload_RawMultipleFilesRejected(t *testing.T) {
	ts, store, tempDir := newOutputRig(t)
	seedSQLOutput(t, store, tempDir, "job-raw-multi", map[string]string{
		"a.sql": "x", "b.sql": "y",
	})

	resp, err := http.Get(ts.URL + "/api/v1/jobs/job-raw-multi/output/download?format=raw")
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (raw only valid for a single file)", resp.StatusCode)
	}
}

func TestJobOutputDownload_RejectsIncompleteJob(t *testing.T) {
	ts, store, tempDir := newOutputRig(t)
	// Job still running: even if some files exist, download must be refused.
	store.CreateJob("job-running", "migrate", "{}")
	store.UpdateJobStatus("job-running", "running")
	dir := filepath.Join(tempDir, "job-running", "insert")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "partial.sql"), []byte("INSERT ..."), 0644)

	resp, err := http.Get(ts.URL + "/api/v1/jobs/job-running/output/download?format=tar.gz")
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409 (job not completed)", resp.StatusCode)
	}
}

func TestJobOutputDownload_NoOutput404(t *testing.T) {
	ts, store, _ := newOutputRig(t)
	store.CreateJob("job-empty", "migrate", "{}")
	store.UpdateJobStatus("job-empty", "completed")

	resp, err := http.Get(ts.URL + "/api/v1/jobs/job-empty/output/download?format=tar.gz")
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no SQL output)", resp.StatusCode)
	}
}

// newArtifactRig builds a Server with a temp job dir and a temp shared export
// dir, so artifact-scoping tests never read the repo's real ./output/data/.
func newArtifactRig(t *testing.T) (*Server, *service.JobStore, string, string) {
	t.Helper()
	tempDir := t.TempDir()
	sharedDir := t.TempDir()
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	srv := NewServer(Config{Store: store, TempDir: tempDir})
	srv.cfg = &config.Config{Export: config.ExportConfig{OutputDir: sharedDir}}
	return srv, store, tempDir, sharedDir
}

// TestJobExportFilesScopedToJob locks the core item-1 fix: a job's detail lists
// ONLY its own artifacts, never the whole shared export dir.
func TestJobExportFilesScopedToJob(t *testing.T) {
	srv, store, tempDir, _ := newArtifactRig(t)
	for _, jid := range []string{"job-a", "job-b"} {
		if err := store.CreateJob(jid, "export", "{}"); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		store.UpdateJobStatus(jid, "completed")
	}
	for _, d := range []struct{ jid, name, content string }{
		{"job-a", "a.csv", "a"},
		{"job-b", "b.csv", "b"},
	} {
		dir := filepath.Join(tempDir, d.jid, "data")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, d.name), []byte(d.content), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	// Internal job file in the root must not surface as an artifact.
	os.WriteFile(filepath.Join(tempDir, "job-a", "config.yaml"), []byte("x"), 0644)

	w := doGet(t, srv, "/api/v1/jobs/job-a/files")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Files []struct {
			Name string `json:"name"`
		} `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Files) != 1 || resp.Files[0].Name != "a.csv" {
		t.Fatalf("job-a files = %+v, want exactly [a.csv]", resp.Files)
	}
}

// TestJobArtifactsDownloadBundlesDataAndSQL: the single-job 打包下载 contains
// both the data file and the INSERT SQL (namespaced under sql/).
func TestJobArtifactsDownloadBundlesDataAndSQL(t *testing.T) {
	srv, store, tempDir, _ := newArtifactRig(t)
	store.CreateJob("job-bundle", "migrate", "{}")
	store.UpdateJobStatus("job-bundle", "completed")
	os.MkdirAll(filepath.Join(tempDir, "job-bundle", "insert"), 0755)
	os.WriteFile(filepath.Join(tempDir, "job-bundle", "scott.emp.csv"), []byte("id\n1\n"), 0644)
	os.WriteFile(filepath.Join(tempDir, "job-bundle", "insert", "scott.emp.insert.sql"), []byte("INSERT INTO emp VALUES (1);"), 0644)

	w := doGet(t, srv, "/api/v1/jobs/job-bundle/artifacts/download?format=zip")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	got := readZip(t, w.Body)
	if _, ok := got["scott.emp.csv"]; !ok {
		t.Errorf("bundle missing data file, entries=%v", keysOf(got))
	}
	if _, ok := got["sql/scott.emp.insert.sql"]; !ok {
		t.Errorf("bundle missing namespaced SQL file, entries=%v", keysOf(got))
	}
}

// TestJobArtifactsDownloadRejectsIncomplete: no pulling a half-written bundle.
func TestJobArtifactsDownloadRejectsIncomplete(t *testing.T) {
	srv, store, tempDir, _ := newArtifactRig(t)
	store.CreateJob("job-running2", "export", "{}")
	store.UpdateJobStatus("job-running2", "running")
	os.MkdirAll(filepath.Join(tempDir, "job-running2", "data"), 0755)
	os.WriteFile(filepath.Join(tempDir, "job-running2", "data", "partial.csv"), []byte("x"), 0644)

	w := doGet(t, srv, "/api/v1/jobs/job-running2/artifacts/download?format=tar.gz")
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
}

// TestListArtifacts: the library lists per-job bundles (with job id) and
// standalone shared-dir files separately.
func TestListArtifacts(t *testing.T) {
	srv, store, tempDir, sharedDir := newArtifactRig(t)
	store.CreateJob("job-lib", "export", "{}")
	store.UpdateJobStatus("job-lib", "completed")
	os.MkdirAll(filepath.Join(tempDir, "job-lib", "data"), 0755)
	os.WriteFile(filepath.Join(tempDir, "job-lib", "data", "lib.csv"), []byte("lib"), 0644)
	os.MkdirAll(sharedDir, 0755)
	os.WriteFile(filepath.Join(sharedDir, "orphan.csv"), []byte("orphan"), 0644)

	w := doGet(t, srv, "/api/v1/artifacts")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Jobs []struct {
			JobID     string `json:"job_id"`
			FileCount int    `json:"file_count"`
		} `json:"jobs"`
		Standalone struct {
			Files []struct {
				Name string `json:"name"`
			} `json:"files"`
		} `json:"standalone"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Jobs) != 1 || resp.Jobs[0].JobID != "job-lib" || resp.Jobs[0].FileCount != 1 {
		t.Fatalf("jobs = %+v, want one job-lib with 1 file", resp.Jobs)
	}
	if len(resp.Standalone.Files) != 1 || resp.Standalone.Files[0].Name != "orphan.csv" {
		t.Fatalf("standalone = %+v, want [orphan.csv]", resp.Standalone.Files)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
