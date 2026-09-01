package serve

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func TestHandler_ShowQuery_AllTypes(t *testing.T) {
	srv := newTestServer(t)

	w := doGet(t, srv, "/api/v1/show-query?dialect=oracle")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["dialect"] != "oracle" {
		t.Errorf("dialect = %v, want oracle", resp["dialect"])
	}
	types, ok := resp["object_types"].(map[string]any)
	if !ok {
		t.Fatal("object_types not found in response")
	}
	if len(types) == 0 {
		t.Error("expected at least 1 object type")
	}
	if _, ok := types["tables"]; !ok {
		t.Error("expected 'tables' in object_types")
	}
}

func TestHandler_ShowQuery_SingleType(t *testing.T) {
	srv := newTestServer(t)

	w := doGet(t, srv, "/api/v1/show-query?dialect=postgres&object_type=columns")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	types := resp["object_types"].(map[string]any)
	if len(types) != 1 {
		t.Errorf("len(object_types) = %d, want 1", len(types))
	}
	sql, ok := types["columns"].(string)
	if !ok || sql == "" {
		t.Error("expected non-empty SQL for columns")
	}
}

func TestHandler_ShowQuery_MissingDialect(t *testing.T) {
	srv := newTestServer(t)

	w := doGet(t, srv, "/api/v1/show-query")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandler_ShowQuery_UnknownDialect(t *testing.T) {
	srv := newTestServer(t)

	w := doGet(t, srv, "/api/v1/show-query?dialect=nonexistent")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandler_ExportOffline_MissingInput(t *testing.T) {
	srv := newTestServer(t)

	w := doJSON(t, srv, "POST", "/api/v1/export/offline", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandler_ExportOffline_CSV(t *testing.T) {
	srv := newTestServer(t)

	dataDir := t.TempDir()
	csvContent := "EMPNO,ENAME,SAL\n7369,SMITH,800\n7499,ALLEN,1600\n"
	os.WriteFile(filepath.Join(dataDir, "scott.emp.csv"), []byte(csvContent), 0644)

	body := `{"data_dir":"` + dataDir + `","format":"csv"}`
	w := doJSON(t, srv, "POST", "/api/v1/export/offline", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["table_count"].(float64) != 1 {
		t.Errorf("table_count = %v, want 1", resp["table_count"])
	}
	if resp["total_rows"].(float64) != 2 {
		t.Errorf("total_rows = %v, want 2", resp["total_rows"])
	}
}

func TestHandler_ExportOffline_CSVToSQL(t *testing.T) {
	srv := newTestServer(t)

	dataDir := t.TempDir()
	csvContent := "ID,NAME\n1,Alice\n2,Bob\n"
	os.WriteFile(filepath.Join(dataDir, "public.users.csv"), []byte(csvContent), 0644)

	body := `{"data_dir":"` + dataDir + `","format":"sql"}`
	w := doJSON(t, srv, "POST", "/api/v1/export/offline", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	files, ok := resp["files"].([]any)
	if !ok || len(files) == 0 {
		t.Fatal("expected output files")
	}
	first := files[0].(map[string]any)
	content := first["content"].(string)
	if len(content) == 0 {
		t.Error("expected non-empty SQL output")
	}
}

func TestHandler_ExportOffline_BadDir(t *testing.T) {
	srv := newTestServer(t)

	w := doJSON(t, srv, "POST", "/api/v1/export/offline", `{"data_dir":"/nonexistent/path"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandler_ExportMetadata_MissingDSN(t *testing.T) {
	srv := newTestServer(t)

	w := doJSON(t, srv, "POST", "/api/v1/metadata/export", `{"source":{"type":"oracle","schema":"SCOTT"}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandler_ExportMetadata_BadFormat(t *testing.T) {
	srv := newTestServer(t)

	w := doJSON(t, srv, "POST", "/api/v1/metadata/export",
		`{"source":{"type":"oracle","dsn":"x/y@localhost:1521/orcl","schema":"SCOTT"},"format":"pdf"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandler_ExportMetadata_BadScope(t *testing.T) {
	srv := newTestServer(t)

	w := doJSON(t, srv, "POST", "/api/v1/metadata/export",
		`{"source":{"type":"oracle","dsn":"x/y@localhost:1521/orcl","schema":"SCOTT"},"scope":"invalid"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandler_ExportMetadata_MissingSchema(t *testing.T) {
	srv := newTestServer(t)

	w := doJSON(t, srv, "POST", "/api/v1/metadata/export",
		`{"source":{"type":"oracle","dsn":"x/y@localhost:1521/orcl"}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDownloadGen_PersistedAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	store, err := service.NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outDir, "emp.sql"), []byte("CREATE TABLE emp (id INT);"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordGeneration("ddl", outDir, 10); err != nil {
		t.Fatalf("RecordGeneration: %v", err)
	}

	srv := NewServer(Config{Store: store})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/v1/ddl/download")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "emp.sql" {
		t.Fatalf("zip contents wrong: %+v", zr.File)
	}
}

func TestGetConfig_MasksDSNPassword(t *testing.T) {
	srv := NewServer(Config{ConfigPath: filepath.Join(t.TempDir(), "migrate.yaml")})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	putBody, err := json.Marshal(map[string]any{
		"source": map[string]any{"type": "oracle", "dsn": "oracle://scott:tiger@db:1521/ORCL"},
		"target": map[string]any{"type": "postgres", "dsn": "postgres://u:p@h/db"},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/config", bytes.NewReader(putBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/api/v1/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "tiger") || strings.Contains(string(body), ":p@h/") {
		t.Fatalf("response leaks password: %s", body)
	}
	if strings.Count(string(body), "******") < 2 {
		t.Fatalf("expected both dsns masked, got: %s", body)
	}
}

func TestBodyLimit_RejectsOversizedPayload(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	srv := NewServer(Config{Store: store})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/api/v1/metadata/load", "application/json",
		bytes.NewReader(bytes.Repeat([]byte("A"), 2<<20)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "request body too large") {
		t.Fatalf("expected body-limit error, got: %s", body)
	}
}

func TestWebSocket_RejectsForeignOrigin(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	srv := NewServer(Config{Store: store})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	hdr := http.Header{}
	hdr.Set("Origin", "http://evil.example")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/jobs/j1/ws"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("expected foreign-origin dial to be rejected")
	}
}

func TestAuth_RequiresToken(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	srv := NewServer(Config{Store: store, Token: "s3cret"})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	client := ts.Client()
	// No token → 401
	resp, err := client.Get(ts.URL + "/api/v1/jobs")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-token status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Correct token → 200
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/jobs", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// Download routes accept the token as a query param (the SPA's plain
	// <a href> downloads cannot set an Authorization header). No generation
	// record exists yet, so the handler answers 400 — the point is auth passed.
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/metadata/export/download?token=s3cret", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("download with query token got 401, want auth to pass")
	}
	resp.Body.Close()

	// Wrong query token on a download route still 401s.
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/metadata/export/download?token=nope", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("download with wrong query token status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Query token does NOT bypass auth on non-download API routes.
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/jobs?token=s3cret", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("query token on non-download status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Health is exempt
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/health", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAuth_AllowsWebSocketWithToken(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	srv := NewServer(Config{Store: store, Token: "s3cret"})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/jobs/j1/ws?token=s3cret"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial(%s) got 401/rejected handshake: %v", wsURL, err)
	}
	// Handshake succeeded under a configured token (no 401). Keep the
	// connection open to prove the WS is reachable before closing.
	conn.Close(websocket.StatusNormalClosure, "")
}

func TestSPA_UIShellServed(t *testing.T) {
	srv := NewServer(Config{})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := ts.Client().Get(ts.URL + "/ui")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ui status = %d, want 200", resp.StatusCode)
	}

	// index asset within /ui/static/ or /static/ui/
	r2, err := ts.Client().Get(ts.URL + "/static/ui/router.js")
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/ui/router.js status = %d, want 200", r2.StatusCode)
	}
}

func TestAuth_DisabledWhenNoToken(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	srv := NewServer(Config{Store: store}) // Token empty
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := ts.Client().Get(ts.URL + "/api/v1/jobs")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no-token-configured status = %d, want 200", resp.StatusCode)
	}
}
