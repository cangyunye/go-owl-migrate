package cdc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bwTargetTable() *TargetTable {
	return &TargetTable{
		Table:       "public.emp",
		Columns:     []string{"empno", "ename"},
		KeyCols:     []string{"empno"},
		Quoter:      func(n string) string { return `"` + n + `"` },
		Placeholder: func(i int) string { return "$" },
	}
}

func TestBatchWriterWritesAtomicFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pending")
	w := &BatchWriter{PendingDir: dir, Begin: "BEGIN;", Commit: "COMMIT;", Wrap: true}
	now := time.Now()
	changes := []Change{
		{ChgID: 1, OpType: "I", NewData: []byte(`{"empno":1,"ename":"A"}`)},
		{ChgID: 2, OpType: "U", OldData: []byte(`{"empno":2}`), NewData: []byte(`{"empno":2,"ename":"B"}`)},
	}
	pb, err := w.Write("emp", changes, bwTargetTable(), now)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if pb.Seq != 1 {
		t.Errorf("seq = %d, want 1", pb.Seq)
	}
	if _, err := os.Stat(pb.Path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
	content, _ := os.ReadFile(pb.Path)
	s := string(content)
	for _, frag := range []string{"BEGIN;", "INSERT INTO public.emp", `("empno", "ename") VALUES (1, 'A')`, "UPDATE public.emp", "COMMIT;"} {
		if !strings.Contains(s, frag) {
			t.Errorf("batch file missing %q\n%s", frag, s)
		}
	}
	// No tmp leftover.
	tmpLeftover, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(tmpLeftover) != 0 {
		t.Errorf("leftover tmp files: %v", tmpLeftover)
	}
}

func TestBatchWriterTruncateIsolated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pending")
	w := &BatchWriter{PendingDir: dir, Commit: "COMMIT;", Wrap: false}
	now := time.Now()
	pb, err := w.Write("emp", []Change{{ChgID: 9, OpType: "T"}}, bwTargetTable(), now)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	bf, _ := ParseBatchFileName(pb.FileName)
	if !bf.IsTruncate {
		t.Errorf("expected truncate in name, got %q", pb.FileName)
	}
	content, _ := os.ReadFile(pb.Path)
	if !strings.Contains(string(content), "TRUNCATE TABLE public.emp") {
		t.Errorf("truncate content missing:\n%s", content)
	}
}

func TestBatchWriterRequiresCommit(t *testing.T) {
	w := &BatchWriter{PendingDir: t.TempDir(), Commit: ""}
	if _, err := w.Write("emp", []Change{{ChgID: 1, OpType: "I", NewData: []byte(`{"a":1}`)}}, bwTargetTable(), time.Now()); err == nil {
		t.Fatal("expected error when commit statement missing")
	}
}

func TestBatchWriterSequences(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pending")
	w := &BatchWriter{PendingDir: dir, Commit: "COMMIT;", Wrap: false}
	now := time.Now()
	w.Write("emp", []Change{{ChgID: 1, OpType: "I", NewData: []byte(`{"empno":1,"ename":"A"}`)}}, bwTargetTable(), now)
	pb2, _ := w.Write("emp", []Change{{ChgID: 5, OpType: "I", NewData: []byte(`{"empno":5,"ename":"E"}`)}}, bwTargetTable(), now)
	if pb2.Seq != 2 {
		t.Errorf("second file seq = %d, want 2", pb2.Seq)
	}
}
