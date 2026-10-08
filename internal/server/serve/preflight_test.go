package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

// web 预检端点（POST /api/v1/migrate/preflight）的进程内测试，锁定不依赖
// 真库即可验证的对外契约：未配置快速失败、元数据可读性、include 无命中、
// plancheck 失败透传、TRUNCATE 警告进入 warnings。真实 sqlite3 链路
// （全绿路径、filters 条件 COUNT、sql-out 跳过）见 preflight_sqlite3_test.go。

// preflightResp 是端点的 JSON 契约形状：ok/checks/warnings 三段，
// checks 逐项 name/ok/detail，warnings 永不为 null。
type preflightResp struct {
	Ok     bool `json:"ok"`
	Checks []struct {
		Name   string `json:"name"`
		Ok     bool   `json:"ok"`
		Detail string `json:"detail"`
	} `json:"checks"`
	Warnings []string `json:"warnings"`
}

func decodePreflight(t *testing.T, body []byte) preflightResp {
	t.Helper()
	var resp preflightResp
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode preflight response: %v\nbody: %s", err, body)
	}
	return resp
}

func checkByName(resp preflightResp, name string) (struct {
	Name   string `json:"name"`
	Ok     bool   `json:"ok"`
	Detail string `json:"detail"`
}, bool) {
	for _, c := range resp.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return struct {
		Name   string `json:"name"`
		Ok     bool   `json:"ok"`
		Detail string `json:"detail"`
	}{}, false
}

// csvMetaDir 写一个最小 csv 元数据目录（tables.csv + columns.csv）并返回路径。
func csvMetaDir(t *testing.T, rows ...string) string {
	t.Helper()
	dir := t.TempDir()
	tables := "TABLE_SCHEMA,TABLE_NAME,TABLE_TYPE,TABLE_COMMENT\n" + strings.Join(rows, "\n") + "\n"
	cols := "TABLE_SCHEMA,TABLE_NAME,COLUMN_NAME,ORDINAL_POSITION,DATA_TYPE,NULLABLE\n"
	for _, r := range rows {
		parts := strings.Split(r, ",")
		cols += parts[0] + "," + parts[1] + ",ID,1,NUMBER,YES\n"
	}
	for name, content := range map[string]string{"tables.csv": tables, "columns.csv": cols} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// 未加载任何配置时必须给出明确的"配置"失败项，而不是 500 或空 panic。
func TestMigratePreflightWithoutConfigFails(t *testing.T) {
	srv := newTestServer(t)

	w := doJSON(t, srv, http.MethodPost, "/api/v1/migrate/preflight", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (preflight reports via body, not status): %s", w.Code, w.Body.String())
	}
	resp := decodePreflight(t, w.Body.Bytes())
	if resp.Ok {
		t.Error("empty server must not report ok")
	}
	c, found := checkByName(resp, "配置")
	if !found || c.Ok {
		t.Fatalf("first check must be a failed 配置 item, got %+v", resp.Checks)
	}
	if !strings.Contains(c.Detail, "配置") {
		t.Errorf("detail should point the user at the config page, got: %s", c.Detail)
	}
}

// csv 元数据目录不可读 → "源库/元数据" 失败项。
func TestMigratePreflightMetadataLoadFailure(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg = &config.Config{
		Metadata: config.MetadataConfig{
			Type: "csv",
			CSV:  config.CSVConfig{Path: filepath.Join(t.TempDir(), "no-such-dir")},
		},
	}

	w := doJSON(t, srv, http.MethodPost, "/api/v1/migrate/preflight", "")
	resp := decodePreflight(t, w.Body.Bytes())
	if resp.Ok {
		t.Fatal("unreadable metadata must fail the preflight")
	}
	if c, found := checkByName(resp, "源库/元数据"); !found || c.Ok {
		t.Errorf("expected failed 源库/元数据 check, got %+v", resp.Checks)
	}
}

// include 过滤零命中 → "表清单" 失败项，detail 要点名 include 列表。
func TestMigratePreflightIncludeNoMatch(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg = &config.Config{
		Metadata: config.MetadataConfig{
			Type: "csv",
			CSV:  config.CSVConfig{Path: csvMetaDir(t, "SCOTT,EMP,TABLE,emp", "SCOTT,DEPT,TABLE,dept")},
		},
		Export: config.ExportConfig{Tables: config.TableListConfig{Include: []string{"NOPE.*"}}},
	}

	w := doJSON(t, srv, http.MethodPost, "/api/v1/migrate/preflight", "")
	resp := decodePreflight(t, w.Body.Bytes())
	if resp.Ok {
		t.Fatal("zero-match include must fail the preflight")
	}
	c, found := checkByName(resp, "表清单")
	if !found || c.Ok {
		t.Fatalf("expected failed 表清单 check, got %+v", resp.Checks)
	}
	if !strings.Contains(c.Detail, "NOPE.*") {
		t.Errorf("detail should echo the include list, got: %s", c.Detail)
	}
}

// plancheck 源库检查失败必须透传为失败项——web 与 CLI 共用同一实现，
// 两表面对同一份计划永不允许给出不同结论。
func TestMigratePreflightSourceCheckFailurePropagates(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg = &config.Config{
		Metadata: config.MetadataConfig{
			Type: "csv",
			CSV:  config.CSVConfig{Path: csvMetaDir(t, "SCOTT,EMP,TABLE,emp")},
		},
		Source: config.DBConfig{Type: "mysql", DSN: "root:pw@tcp(127.0.0.1:1)/none", Schema: "SCOTT"},
		Export: config.ExportConfig{Tables: config.TableListConfig{Include: []string{"SCOTT.*"}}},
	}

	w := doJSON(t, srv, http.MethodPost, "/api/v1/migrate/preflight", "")
	resp := decodePreflight(t, w.Body.Bytes())
	if resp.Ok {
		t.Fatal("unreachable source must fail the preflight")
	}
	c, found := checkByName(resp, "元数据↔源库一致性")
	if !found || c.Ok {
		t.Fatalf("expected failed 元数据↔源库一致性 check, got %+v", resp.Checks)
	}
}

// import.target.truncate_before 是破坏性操作，预检必须在 warnings 里点名，
// 即使后续检查失败也不能丢掉这条警告。
func TestMigratePreflightTruncateWarningSurvivesFailure(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg = &config.Config{
		Metadata: config.MetadataConfig{
			Type: "csv",
			CSV:  config.CSVConfig{Path: csvMetaDir(t, "SCOTT,EMP,TABLE,emp")},
		},
		Source: config.DBConfig{Type: "mysql", DSN: "root:pw@tcp(127.0.0.1:1)/none", Schema: "SCOTT"},
		Export: config.ExportConfig{Tables: config.TableListConfig{Include: []string{"SCOTT.*"}}},
		Import: config.ImportConfig{Target: config.ImportTargetConfig{TruncateBefore: true}},
	}

	w := doJSON(t, srv, http.MethodPost, "/api/v1/migrate/preflight", "")
	resp := decodePreflight(t, w.Body.Bytes())
	if resp.Ok {
		t.Fatal("unreachable source must fail")
	}
	found := false
	for _, warn := range resp.Warnings {
		if strings.Contains(warn, "TRUNCATE") {
			found = true
		}
	}
	if !found {
		t.Errorf("truncate warning must survive the failure response, warnings: %v", resp.Warnings)
	}
}
