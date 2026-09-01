package serve

import (
	"fmt"
	"os"
	"path/filepath"
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
		{"no dsn", "sqlite3", "", "main", "sqlite3/main"},
		{"empty everything", "", "", "", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sourceLabel(tc.srcType, tc.dsn, tc.schema)
			if got != tc.want {
				t.Errorf("sourceLabel = %q, want %q", got, tc.want)
			}
			for _, secret := range []string{"secret", "tiger", "password="} {
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
