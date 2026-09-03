package service

import (
	"strings"
	"testing"
)

func TestGenerateSelect(t *testing.T) {
	sm := ddlFixture(t) // SCOTT.EMP / HR.EMP
	cfg := ddlCfg("oracle")
	dir := t.TempDir()
	files, err := GenerateSelect(sm, cfg, nil, "", 0, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	names := fileNames(files)
	if len(names) != 2 {
		t.Fatalf("files = %v, want 2", names)
	}
	content := readAll(t, files)
	for _, want := range []string{"SELECT", "SCOTT", "HR"} {
		if !strings.Contains(content, want) {
			t.Errorf("SELECT output missing %q:\n%s", want, content)
		}
	}
}

func TestGenerateSelectIncludeAndNoQuote(t *testing.T) {
	sm := ddlFixture(t)
	cfg := ddlCfg("oracle")
	dir := t.TempDir()
	files, err := GenerateSelect(sm, cfg, []string{"HR.EMP"}, "", 0, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("include HR.EMP files = %v, want 1", fileNames(files))
	}
	content := readAll(t, files)
	if !strings.Contains(content, "HR") || strings.Contains(content, "SCOTT") {
		t.Errorf("include should keep only hr.emp:\n%s", content)
	}

	dir2 := t.TempDir()
	files2, err := GenerateSelect(sm, cfg, []string{"SCOTT.EMP"}, "", 0, boolPtr(true), dir2)
	if err != nil {
		t.Fatal(err)
	}
	content2 := readAll(t, files2)
	if strings.Contains(content2, `"`) {
		t.Errorf("no_quote_identifiers should omit quotes:\n%s", content2)
	}
}

func TestGenerateSelectPageSizeOverride(t *testing.T) {
	sm := ddlFixture(t)
	cfg := ddlCfg("oracle")
	dir := t.TempDir()
	files, err := GenerateSelect(sm, cfg, []string{"SCOTT.EMP"}, "offset", 10, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %v", fileNames(files))
	}
	content := readAll(t, files)
	if !strings.Contains(content, "10") {
		t.Errorf("offset pagination should carry page size:\n%s", content)
	}
}
