package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

func writeCSVFile(t *testing.T, dir, name, header, rows string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := header
	if rows != "" {
		data += "\n" + rows
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectTablesFromCSVDir(t *testing.T) {
	dir := t.TempDir()
	writeCSVFile(t, dir, "scott.emp.csv", "EMPNO,ENAME", "1,SMITH")
	writeCSVFile(t, dir, "hr.dept.csv", "ID,NAME", "1,SALES")
	tables, err := DetectTablesFromCSVDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 {
		t.Fatalf("tables = %d, want 2", len(tables))
	}
	byName := map[string]int{}
	for _, tbl := range tables {
		byName[tbl.TableSchema+"."+tbl.TableName] = len(tbl.GetColumns())
	}
	if byName["scott.emp"] != 2 || byName["hr.dept"] != 2 {
		t.Errorf("column counts wrong: %v", byName)
	}
}

func TestDetectTablesFromCSVDirMissingDir(t *testing.T) {
	_, err := DetectTablesFromCSVDir(filepath.Join(t.TempDir(), "no-such-dir"))
	if err == nil {
		t.Fatal("want error for missing data dir")
	}
	// 可操作指引（P5）：告诉用户先导出数据
	if !strings.Contains(err.Error(), "export data") {
		t.Errorf("error should guide to export data, got: %s", err)
	}
}

func TestGenerateInsert(t *testing.T) {
	dataDir := t.TempDir()
	writeCSVFile(t, dataDir, "scott.emp.csv", "EMPNO,ENAME", "1,SMITH")
	writeCSVFile(t, dataDir, "hr.dept.csv", "ID,NAME", "1,SALES")

	out := t.TempDir()
	cfg := &config.Config{}
	files, err := GenerateInsert(cfg, nil, dataDir, out, InsertOptions{Dialect: "postgres", BatchSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %v, want 2", fileNames(files))
	}
	content := readAll(t, files)
	for _, want := range []string{"INSERT INTO", "scott", "hr"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q in:\n%s", want, content)
		}
	}
}

func TestGenerateInsertIncludeFilter(t *testing.T) {
	dataDir := t.TempDir()
	writeCSVFile(t, dataDir, "scott.emp.csv", "EMPNO,ENAME", "1,SMITH")
	writeCSVFile(t, dataDir, "scott.dept.csv", "ID,NAME", "1,SALES")
	out := t.TempDir()
	files, err := GenerateInsert(&config.Config{}, []string{"SCOTT.EMP"}, dataDir, out, InsertOptions{Dialect: "postgres"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "scott.emp.insert.sql" {
		t.Fatalf("include filter files = %v", fileNames(files))
	}
}

// ── P5: 生命周期——产物目录缺失时框架自建（Ensure）──

func TestGenerationCreatesMissingOutDir(t *testing.T) {
	sm := ddlFixture(t) // SCOTT.EMP / HR.EMP
	dataDir := t.TempDir()
	writeCSVFile(t, dataDir, "scott.emp.csv", "EMPNO,ENAME", "1,SMITH")
	base := filepath.Join(t.TempDir(), "nested", "out")

	cases := []struct {
		name string
		run  func(dir string) error
	}{
		{"ddl", func(dir string) error { _, err := GenerateDDL(sm, ddlCfg("oracle"), nil, nil, dir); return err }},
		{"select", func(dir string) error {
			_, err := GenerateSelect(sm, ddlCfg("oracle"), nil, "", 0, nil, dir)
			return err
		}},
		{"insert", func(dir string) error {
			_, err := GenerateInsert(&config.Config{}, nil, dataDir, dir, InsertOptions{Dialect: "postgres"})
			return err
		}},
		{"metadata", func(dir string) error { _, err := ExportMetadataFiles(dir, sm, nil); return err }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(base, c.name)
			if err := c.run(dir); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				t.Errorf("%s output dir not auto-created", c.name)
			}
		})
	}
}
