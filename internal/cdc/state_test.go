package cdc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJSONStateStoreCRUD(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONStateStore(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("NewJSONStateStore: %v", err)
	}

	if err := store.SaveCheckpoint(Checkpoint{TableName: "emp", FiledChgID: 10, AckedChgID: 8}); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	cps, err := store.LoadCheckpoints()
	if err != nil {
		t.Fatalf("LoadCheckpoints: %v", err)
	}
	emp, ok := cps["emp"]
	if !ok {
		t.Fatalf("expected checkpoint for emp, got %v", cps)
	}
	if emp.FiledChgID != 10 || emp.AckedChgID != 8 {
		t.Errorf("checkpoint = %+v, want filed=10 acked=8", emp)
	}
}

func TestJSONStateStorePersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s1, _ := NewJSONStateStore(path)
	if err := s1.SaveCheckpoint(Checkpoint{TableName: "dept", FiledChgID: 5, AckedChgID: 5}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2, err := NewJSONStateStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	defer s2.Close()
	cps, _ := s2.LoadCheckpoints()
	dept := cps["dept"]
	if dept.FiledChgID != 5 || dept.AckedChgID != 5 {
		t.Errorf("reloaded checkpoint = %+v, want filed=5 acked=5", dept)
	}
}

func TestJSONStateStoreMissingFileReturnsEmpty(t *testing.T) {
	store, err := NewJSONStateStore(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("NewJSONStateStore on missing file: %v", err)
	}
	defer store.Close()
	cps, err := store.LoadCheckpoints()
	if err != nil {
		t.Fatalf("LoadCheckpoints on empty: %v", err)
	}
	if len(cps) != 0 {
		t.Errorf("expected empty checkpoints, got %v", cps)
	}
}

func TestJSONStateStoreAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s1, _ := NewJSONStateStore(path)
	s1.SaveCheckpoint(Checkpoint{TableName: "a", FiledChgID: 1, AckedChgID: 1})
	s1.Close()
	// No leftover temp file after successful writes.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover temp file %s after close", e.Name())
		}
	}
}
