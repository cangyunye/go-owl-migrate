package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// newDSRig builds a standalone server over httptest with a hermetic
// data-sources directory and an encrypted vault keyed to a temp dir.
func newDSRig(t *testing.T) *httptest.Server {
	t.Helper()
	dsDir := filepath.Join(t.TempDir(), "datasources")
	srv := NewServer(Config{DataSourcesDir: dsDir})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(mustJSON(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func putJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, url, strings.NewReader(mustJSON(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	return resp
}

func TestDataSourcesCRUDFlow(t *testing.T) {
	ts := newDSRig(t)

	// Create.
	resp := postJSON(t, ts.URL+"/api/v1/datasources", map[string]any{
		"name": "prod-oracle", "type": "oracle", "schema": "SCOTT",
		"dsn": "oracle://u:p@h:1521/XE", "remark": "prod",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// List must not leak the password or its ciphertext.
	listResp, err := http.Get(ts.URL + "/api/v1/datasources")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer listResp.Body.Close()
	var list []map[string]any
	json.NewDecoder(listResp.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	if _, ok := list[0]["dsn"]; ok {
		t.Fatalf("list exposed dsn field: %v", list[0])
	}
	if list[0]["name"] != "prod-oracle" || list[0]["type"] != "oracle" {
		t.Fatalf("list item = %v", list[0])
	}

	// Pick returns type/schema + ref, never the dsn.
	pickResp := postJSON(t, ts.URL+"/api/v1/datasources/prod-oracle/pick", map[string]any{})
	defer pickResp.Body.Close()
	if pickResp.StatusCode != http.StatusOK {
		t.Fatalf("pick status = %d", pickResp.StatusCode)
	}
	var pick map[string]any
	json.NewDecoder(pickResp.Body).Decode(&pick)
	if pick["type"] != "oracle" || pick["schema"] != "SCOTT" {
		t.Fatalf("pick = %v", pick)
	}
	if _, ok := pick["dsn"]; ok {
		t.Fatalf("pick exposed dsn: %v", pick)
	}
	if pick["ref"] != "datasource:prod-oracle" {
		t.Fatalf("pick ref = %v", pick["ref"])
	}

	// Delete.
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/datasources/prod-oracle", nil)
	delResp, _ := http.DefaultClient.Do(delReq)
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", delResp.StatusCode)
	}
	delResp.Body.Close()
}

func TestDataSourceBuildResolvesRefServerSide(t *testing.T) {
	ts := newDSRig(t)
	postJSON(t, ts.URL+"/api/v1/datasources", map[string]any{
		"name": "src", "type": "oracle", "schema": "SCOTT",
		"dsn": "oracle://u:p@h:1521/XE",
	})

	// Build a migrate config referencing the data source via the ref sentinel.
	resp := postJSON(t, ts.URL+"/api/v1/scenarios/migrate/build", map[string]any{
		"values": map[string]any{
			"metadata_type": "csv", "csv_path": "./testdata/csv/",
			"source_type": "oracle", "source_dsn": "datasource:src", "source_schema": "SCOTT",
			"target_type": "postgres", "target_dsn": "host=t user=p password=s dbname=d",
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("build status = %d", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	configMap, _ := body["config"].(map[string]any)
	src, _ := configMap["source"].(map[string]any)
	dsn, _ := src["dsn"].(string)
	if dsn != "oracle://u:******@h:1521/XE" {
		t.Fatalf("resolved source dsn = %q (want masked)", dsn)
	}
	// The YAML preview must never include the plaintext password either.
	if strings.Contains(body["yaml"].(string), "u:p@h") {
		t.Fatalf("yaml preview leaked plaintext password")
	}
	if !strings.Contains(body["yaml"].(string), "******") {
		t.Fatalf("yaml preview missing masked dsn")
	}
}

func TestDataSourceStructuredFields(t *testing.T) {
	ts := newDSRig(t)

	// Create from structured fields.
	resp := postJSON(t, ts.URL+"/api/v1/datasources", map[string]any{
		"name": "pg", "type": "postgres", "schema": "public", "remark": "prod",
		"fields": map[string]any{"username": "app", "password": "secret", "host": "127.0.0.1", "port": "5432", "database": "mydb"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Detail must return decomposed fields, never the password.
	detailResp, err := http.Get(ts.URL + "/api/v1/datasources/pg")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	defer detailResp.Body.Close()
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d", detailResp.StatusCode)
	}
	var detail map[string]any
	json.NewDecoder(detailResp.Body).Decode(&detail)
	if detail["password_set"] != true {
		t.Fatalf("password_set = %v, want true", detail["password_set"])
	}
	fields, _ := detail["fields"].(map[string]any)
	if fields["username"] != "app" || fields["host"] != "127.0.0.1" || fields["database"] != "mydb" {
		t.Fatalf("fields = %v", fields)
	}
	if _, ok := fields["password"]; ok {
		t.Fatalf("detail leaked password field: %v", fields)
	}

	// Update host only, blank password → stored password must be preserved
	// server-side and the DSN rebuilt from the URL form.
	resp = putJSON(t, ts.URL+"/api/v1/datasources/pg", map[string]any{
		"type": "postgres", "schema": "public", "remark": "prod",
		"fields": map[string]any{"username": "app", "password": "", "host": "db.internal", "port": "5432", "database": "mydb"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Resolve via a build to confirm the password survived and host changed.
	buildResp := postJSON(t, ts.URL+"/api/v1/scenarios/migrate/build", map[string]any{
		"values": map[string]any{
			"metadata_type": "csv", "csv_path": "./testdata/csv/",
			"source_type": "postgres", "source_dsn": "datasource:pg", "source_schema": "public",
			"target_type": "mysql", "target_dsn": "user:pass@tcp(h:3306)/db",
		},
	})
	defer buildResp.Body.Close()
	if buildResp.StatusCode != http.StatusOK {
		t.Fatalf("build status = %d", buildResp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(buildResp.Body).Decode(&body)
	configMap, _ := body["config"].(map[string]any)
	src, _ := configMap["source"].(map[string]any)
	if got := src["dsn"].(string); got != "postgres://app:******@db.internal:5432/mydb" {
		t.Fatalf("resolved dsn = %q", got)
	}
}

func TestDataSourceStructuredFieldsFileType(t *testing.T) {
	ts := newDSRig(t)
	resp := postJSON(t, ts.URL+"/api/v1/datasources", map[string]any{
		"name": "sql", "type": "sqlite3",
		"fields": map[string]any{"database": "/tmp/data.db"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	detailResp, err := http.Get(ts.URL + "/api/v1/datasources/sql")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	defer detailResp.Body.Close()
	var detail map[string]any
	json.NewDecoder(detailResp.Body).Decode(&detail)
	fields, _ := detail["fields"].(map[string]any)
	if fields["database"] != "/tmp/data.db" {
		t.Fatalf("file fields = %v", fields)
	}
}

func TestDataSourceBuildStaleRefFails(t *testing.T) {
	ts := newDSRig(t)
	resp := postJSON(t, ts.URL+"/api/v1/scenarios/migrate/build", map[string]any{
		"values": map[string]any{
			"source_type": "oracle", "source_dsn": "datasource:missing",
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("stale ref build status = %d, want 400", resp.StatusCode)
	}
}
