package cdc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBatchFileNameAndParse(t *testing.T) {
	start := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 18, 9, 15, 0, 0, time.UTC)
	name := BatchFileName(1, start, end, "EMP", 1234, 2000, false)
	want := "000001_20260818090000-20260818091500_EMP_1234-2000.sql"
	if name != want {
		t.Fatalf("BatchFileName = %q, want %q", name, want)
	}
	bf, err := ParseBatchFileName(name)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if bf.Seq != 1 || bf.Table != "EMP" || bf.StartChgID != 1234 || bf.EndChgID != 2000 {
		t.Errorf("parsed = %+v", bf)
	}
	if !bf.Start.Equal(start) || !bf.End.Equal(end) {
		t.Errorf("times = %v/%v", bf.Start, bf.End)
	}
}

func TestBatchFileNameTruncate(t *testing.T) {
	name := BatchFileName(3, time.Now(), time.Now(), "EMP", 1, 1, true)
	bf, err := ParseBatchFileName(name)
	if err != nil {
		t.Fatalf("parse truncate: %v", err)
	}
	if !bf.IsTruncate {
		t.Errorf("expected truncate flag on %q", name)
	}
}

func TestBatchFileNameSanitizesTable(t *testing.T) {
	name := BatchFileName(2, time.Now(), time.Now(), "a/b", 1, 1, false)
	if name != BatchFileName(2, time.Now(), time.Now(), "a_b", 1, 1, false) {
		t.Errorf("table slash not sanitized: %q", name)
	}
}

func TestNextSeqFromPending(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir, 0755)
	now := time.Now()
	os.WriteFile(filepath.Join(dir, BatchFileName(1, now, now, "EMP", 1, 1, false)), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, BatchFileName(5, now, now, "DEPT", 1, 1, false)), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "random.txt"), []byte("x"), 0644)
	seq, err := NextSeqFromPending(dir)
	if err != nil {
		t.Fatalf("NextSeqFromPending: %v", err)
	}
	if seq != 6 {
		t.Errorf("next seq = %d, want 6", seq)
	}
}

func TestNextSeqFromPendingMissingDir(t *testing.T) {
	seq, err := NextSeqFromPending(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("next seq missing dir: %v", err)
	}
	if seq != 1 {
		t.Errorf("next seq = %d, want 1", seq)
	}
}
