package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
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
