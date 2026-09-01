package serve

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func TestSourceLabel_NoPasswordLeak(t *testing.T) {
	cases := []struct {
		name, srcType, dsn, schema, want string
	}{
		{"url", "mysql", "mysql://root:secret@127.0.0.1:3306/default_db", "default_db", "mysql@127.0.0.1:3306/default_db"},
		{"oracle user/pass", "oracle", "scott/tiger@127.0.0.1:1521/XEPDB1", "SCOTT", "oracle@127.0.0.1:1521/SCOTT"},
		{"pg keyword", "postgres", "host=127.0.0.1 port=5432 user=postgres password=secret dbname=postgres_db sslmode=disable", "public", "postgres@127.0.0.1:5432/public"},
		{"pg keyword with @ in password", "postgres", "host=127.0.0.1 user=postgres password=p@ss dbname=db sslmode=disable", "public", "postgres@127.0.0.1/public"},
		{"no dsn", "sqlite3", "", "main", "sqlite3/main"},
		{"empty everything", "", "", "", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sourceLabel(tc.srcType, tc.dsn, tc.schema)
			if got != tc.want {
				t.Errorf("sourceLabel = %q, want %q", got, tc.want)
			}
			for _, secret := range []string{"secret", "tiger", "password=", "p@ss"} {
				if contains(got, secret) {
					t.Errorf("label %q leaks secret %q", got, secret)
				}
			}
		})
	}
}

func TestSourceMetaFrom_DatasourceRef(t *testing.T) {
	cfg := config.DBConfig{Type: "oracle", DSN: "datasource:prod-ora", Schema: "SCOTT"}
	m := sourceMetaFrom(cfg, cfg.Schema)
	if m.DatasourceName != "prod-ora" {
		t.Errorf("DatasourceName = %q, want prod-ora", m.DatasourceName)
	}
	if m.SourceLabel != "oracle@prod-ora" {
		t.Errorf("SourceLabel = %q, want oracle@prod-ora", m.SourceLabel)
	}
}

func TestSourceMetaFrom_EmptyRefNameIsPlain(t *testing.T) {
	cfg := config.DBConfig{Type: "oracle", DSN: "datasource:", Schema: "SCOTT"}
	m := sourceMetaFrom(cfg, cfg.Schema)
	if m.DatasourceName != "" {
		t.Errorf("DatasourceName = %q, want empty", m.DatasourceName)
	}
	if m.SourceLabel != "oracle/SCOTT" {
		t.Errorf("SourceLabel = %q, want oracle/SCOTT", m.SourceLabel)
	}
}

func TestDirStats(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.csv"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(dir, "b.csv"), []byte("world"), 0644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "c.csv"), []byte("!"), 0644)
	fc, sz := dirStats(dir)
	if fc != 3 {
		t.Errorf("file_count = %d, want 3", fc)
	}
	if sz != 11 {
		t.Errorf("size = %d, want 11", sz)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
func TestRecordGenOutput_PersistsMetaAndPrunes(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := NewServer(Config{Store: store})

	dirs := make([]string, 0, genOutputKeep+1)
	for i := 0; i < genOutputKeep+1; i++ {
		d := filepath.Join(t.TempDir(), fmt.Sprintf("out-%d", i))
		os.MkdirAll(d, 0755)
		dirs = append(dirs, d)
	}
	meta := service.GenerationMeta{SourceLabel: "mysql@h:3306/s", Detail: map[string]any{"format": "csv"}}
	for i, d := range dirs {
		if err := srv.recordGenOutput("metadata", d, meta); err != nil {
			t.Fatalf("recordGenOutput(%d): %v", i, err)
		}
	}

	// 最旧目录已被磁盘删除
	if _, err := os.Stat(dirs[0]); !os.IsNotExist(err) {
		t.Errorf("pruned dir %s still exists", dirs[0])
	}
	recs, err := store.ListGenerations("metadata")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != genOutputKeep {
		t.Errorf("records = %d, want %d", len(recs), genOutputKeep)
	}
	if recs[0].Detail["format"] != "csv" {
		t.Errorf("detail = %v, want csv", recs[0].Detail)
	}
}
func TestGenerationsAPI(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	outDir := t.TempDir()
	os.WriteFile(filepath.Join(outDir, "tables.csv"), []byte("x"), 0644)
	store.RecordGeneration("metadata", outDir, service.GenerationMeta{
		SourceLabel: "mysql@h:3306/s", Detail: map[string]any{"format": "csv", "file_count": float64(1)},
	})
	store.RecordGeneration("ddl", t.TempDir(), service.GenerationMeta{})

	srv := NewServer(Config{Store: store})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// 列表：kind 过滤 + 元数据 + 实时大小
	resp, _ := http.Get(ts.URL + "/api/v1/generations?kind=metadata")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d: %s", resp.StatusCode, body)
	}
	var list struct {
		Kind  string `json:"kind"`
		Items []struct {
			ID          int64          `json:"id"`
			SourceLabel string         `json:"source_label"`
			Detail      map[string]any `json:"detail"`
			FileCount   int            `json:"file_count"`
			SizeBytes   int64          `json:"size_bytes"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if list.Kind != "metadata" || len(list.Items) != 1 {
		t.Fatalf("list = %+v", list)
	}
	it := list.Items[0]
	if it.SourceLabel != "mysql@h:3306/s" || it.FileCount != 1 || it.SizeBytes != 1 {
		t.Errorf("item = %+v", it)
	}

	// files：内容可读
	resp2, _ := http.Get(fmt.Sprintf("%s/api/v1/generations/%d/files", ts.URL, it.ID))
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("files status = %d: %s", resp2.StatusCode, b2)
	}
	if !strings.Contains(string(b2), "tables.csv") {
		t.Errorf("files body missing tables.csv: %s", b2)
	}

	// 未知 id → 404
	resp3, _ := http.Get(ts.URL + "/api/v1/generations/9999/files")
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", resp3.StatusCode)
	}
}

func TestDownloadGen_ByIDAndLatest(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	d1 := t.TempDir()
	os.WriteFile(filepath.Join(d1, "old.sql"), []byte("OLD"), 0644)
	store.RecordGeneration("ddl", d1, service.GenerationMeta{})
	d2 := t.TempDir()
	os.WriteFile(filepath.Join(d2, "new.sql"), []byte("NEW"), 0644)
	store.RecordGeneration("ddl", d2, service.GenerationMeta{})

	srv := NewServer(Config{Store: store})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// 解压 zip 并读出指定 entry 的内容
	zipEntry := func(body []byte, name string) (string, error) {
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return "", err
		}
		for _, f := range zr.File {
			if f.Name != name {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return "", err
			}
			return string(data), nil
		}
		return "", fmt.Errorf("zip entry %q not found", name)
	}

	// 缺省 = 最新
	resp, _ := http.Get(ts.URL + "/api/v1/ddl/download")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	newContent, err := zipEntry(body, "new.sql")
	if err != nil {
		t.Errorf("latest download: %v", err)
	} else if !strings.Contains(newContent, "NEW") {
		t.Errorf("latest download new.sql = %q, want to contain NEW", newContent)
	}
	if oldContent, err := zipEntry(body, "old.sql"); err == nil {
		t.Errorf("latest download unexpectedly contains old.sql entry (%q)", oldContent)
	}

	// 按 id 取旧的
	recs, _ := store.ListGenerations("ddl")
	resp2, _ := http.Get(fmt.Sprintf("%s/api/v1/ddl/download?id=%d", ts.URL, recs[1].ID))
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	oldContent, err := zipEntry(b2, "old.sql")
	if err != nil {
		t.Errorf("by-id download: %v", err)
	} else if !strings.Contains(oldContent, "OLD") {
		t.Errorf("by-id download old.sql = %q, want to contain OLD", oldContent)
	}

	// 跨 kind 的 id → 404（metadata 端点上拿 ddl 记录）
	resp3, _ := http.Get(fmt.Sprintf("%s/api/v1/metadata/export/download?id=%d", ts.URL, recs[0].ID))
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-kind id status = %d, want 404", resp3.StatusCode)
	}

	// 不存在的 id → 404
	resp4, _ := http.Get(ts.URL + "/api/v1/ddl/download?id=99999")
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", resp4.StatusCode)
	}
}
