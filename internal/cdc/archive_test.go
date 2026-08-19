package cdc

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAckedFromDoneContiguous(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	os.WriteFile(filepath.Join(dir, BatchFileName(1, now, now, "EMP", 1, 100, false)), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, BatchFileName(2, now, now, "EMP", 101, 200, false)), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, BatchFileName(1, now, now, "DEPT", 1, 50, false)), []byte("x"), 0644)

	acked, err := AckedFromDone(dir)
	if err != nil {
		t.Fatalf("AckedFromDone: %v", err)
	}
	if acked["EMP"] != 200 {
		t.Errorf("EMP acked = %d, want 200", acked["EMP"])
	}
	if acked["DEPT"] != 50 {
		t.Errorf("DEPT acked = %d, want 50", acked["DEPT"])
	}
}

func TestAckedFromDoneGapStops(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// seq 1 and 3 present, seq 2 missing → contiguous prefix only 1.
	os.WriteFile(filepath.Join(dir, BatchFileName(1, now, now, "EMP", 1, 100, false)), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, BatchFileName(3, now, now, "EMP", 300, 400, false)), []byte("x"), 0644)
	acked, _ := AckedFromDone(dir)
	if acked["EMP"] != 100 {
		t.Errorf("EMP acked = %d, want 100 (gap at seq 2)", acked["EMP"])
	}
}

func TestAckedFromDoneMissingDir(t *testing.T) {
	acked, err := AckedFromDone(filepath.Join(t.TempDir(), "no"))
	if err != nil {
		t.Fatalf("AckedFromDone missing dir: %v", err)
	}
	if len(acked) != 0 {
		t.Errorf("expected empty, got %v", acked)
	}
}

func TestArchiveDoneGroupsByTableDate(t *testing.T) {
	done := filepath.Join(t.TempDir(), "done")
	archive := filepath.Join(t.TempDir(), "archive")
	os.MkdirAll(done, 0755)
	now := time.Now()
	os.WriteFile(filepath.Join(done, BatchFileName(1, now, now, "EMP", 1, 10, false)), []byte("stmt1;"), 0644)
	os.WriteFile(filepath.Join(done, BatchFileName(2, now, now, "EMP", 11, 20, false)), []byte("stmt2;"), 0644)
	os.WriteFile(filepath.Join(done, BatchFileName(1, now, now, "DEPT", 1, 5, false)), []byte("d;"), 0644)

	n, err := ArchiveDone(done, archive)
	if err != nil {
		t.Fatalf("ArchiveDone: %v", err)
	}
	if n != 3 {
		t.Errorf("archived count = %d, want 3", n)
	}
	// original files removed
	if entries, _ := os.ReadDir(done); len(entries) != 0 {
		t.Errorf("expected done/ empty after archive, got %d entries", len(entries))
	}
	// archives exist
	date := now.Format("20060102")
	empTar := filepath.Join(archive, "EMP", date+".tar.gz")
	deptTar := filepath.Join(archive, "DEPT", date+".tar.gz")
	if _, err := os.Stat(empTar); err != nil {
		t.Errorf("missing EMP archive: %v", err)
	}
	if _, err := os.Stat(deptTar); err != nil {
		t.Errorf("missing DEPT archive: %v", err)
	}
}

func TestArchiveDoneNoDoneDir(t *testing.T) {
	n, err := ArchiveDone(filepath.Join(t.TempDir(), "no"), filepath.Join(t.TempDir(), "a"))
	if err != nil {
		t.Fatalf("ArchiveDone no dir: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

func TestTarGzContainsFiles(t *testing.T) {
	done := t.TempDir()
	archive := t.TempDir()
	f1 := filepath.Join(done, "x.sql")
	os.WriteFile(f1, []byte("hello;"), 0644)
	if err := tarGzFiles(filepath.Join(archive, "a.tar.gz"), []string{f1}); err != nil {
		t.Fatalf("tarGzFiles: %v", err)
	}
	f, err := os.Open(filepath.Join(archive, "a.tar.gz"))
	if err != nil {
		t.Fatalf("open tar: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("tar next: %v", err)
	}
	if hdr.Name != "x.sql" {
		t.Errorf("tar entry name = %q, want x.sql", hdr.Name)
	}
}
