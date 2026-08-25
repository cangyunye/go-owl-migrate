package service

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) *JobStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestJobStore_CreateJob(t *testing.T) {
	store := newTestStore(t)

	err := store.CreateJob("job-001", "migrate", `{"ddl":{"target_dialect":"postgres"}}`)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	job, err := store.GetJob("job-001")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.JobID != "job-001" {
		t.Errorf("JobID = %q, want %q", job.JobID, "job-001")
	}
	if job.Type != "migrate" {
		t.Errorf("Type = %q, want %q", job.Type, "migrate")
	}
	if job.Status != "running" {
		t.Errorf("Status = %q, want %q", job.Status, "running")
	}
	if job.Config != `{"ddl":{"target_dialect":"postgres"}}` {
		t.Errorf("Config = %q, want JSON", job.Config)
	}
	if job.CreatedAt == "" {
		t.Error("CreatedAt should not be empty")
	}
}

func TestJobStore_CreateJob_Duplicate(t *testing.T) {
	store := newTestStore(t)

	if err := store.CreateJob("job-dup", "export", "{}"); err != nil {
		t.Fatalf("first CreateJob: %v", err)
	}
	err := store.CreateJob("job-dup", "import", "{}")
	if err == nil {
		t.Fatal("expected error for duplicate job_id, got nil")
	}
}

func TestJobStore_UpdateJobStatus(t *testing.T) {
	store := newTestStore(t)
	store.CreateJob("job-002", "migrate", "{}")

	tests := []struct {
		name   string
		status string
	}{
		{"completed", "completed"},
		{"failed", "failed"},
		{"interrupted", "interrupted"},
		{"cancelled", "cancelled"},
		{"cancelling", "cancelling"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := store.UpdateJobStatus("job-002", tt.status); err != nil {
				t.Fatalf("UpdateJobStatus(%q): %v", tt.status, err)
			}
			job, err := store.GetJob("job-002")
			if err != nil {
				t.Fatalf("GetJob: %v", err)
			}
			if job.Status != tt.status {
				t.Errorf("Status = %q, want %q", job.Status, tt.status)
			}
			if tt.status == "completed" || tt.status == "failed" {
				if job.FinishedAt == "" {
					t.Error("FinishedAt should be set for terminal status")
				}
			}
		})
	}
}

func TestJobStore_UpdateJobPID(t *testing.T) {
	store := newTestStore(t)
	store.CreateJob("job-pid", "export", "{}")

	if err := store.UpdateJobPID("job-pid", 12345); err != nil {
		t.Fatalf("UpdateJobPID: %v", err)
	}
	job, _ := store.GetJob("job-pid")
	if job.PID != 12345 {
		t.Errorf("PID = %d, want 12345", job.PID)
	}
}

func TestJobStore_ListJobs(t *testing.T) {
	store := newTestStore(t)

	for i := 0; i < 5; i++ {
		store.CreateJob(fmt.Sprintf("job-%03d", i), "migrate", "{}")
	}

	jobs, err := store.ListJobs(10)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 5 {
		t.Fatalf("len(jobs) = %d, want 5", len(jobs))
	}
	if jobs[0].JobID != "job-004" {
		t.Errorf("first job = %q, want job-004 (newest first)", jobs[0].JobID)
	}

	jobs, err = store.ListJobs(3)
	if err != nil {
		t.Fatalf("ListJobs(3): %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("len(jobs) = %d, want 3", len(jobs))
	}
}

func TestJobStore_GetJob_NotFound(t *testing.T) {
	store := newTestStore(t)

	_, err := store.GetJob("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent job, got nil")
	}
}

func TestJobStore_WriteEvent(t *testing.T) {
	store := newTestStore(t)
	store.CreateJob("job-ev", "migrate", "{}")

	err := store.WriteEvent("job-ev", "export_complete", "SCOTT", "EMP", 5000, "exported 5000 rows")
	if err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}

	events, err := store.GetEvents("job-ev", 0)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Seq != 1 {
		t.Errorf("Seq = %d, want 1", ev.Seq)
	}
	if ev.EventType != "export_complete" {
		t.Errorf("EventType = %q, want export_complete", ev.EventType)
	}
	if ev.Schema != "SCOTT" || ev.TableName != "EMP" {
		t.Errorf("Schema.Table = %s.%s, want SCOTT.EMP", ev.Schema, ev.TableName)
	}
	if ev.Rows != 5000 {
		t.Errorf("Rows = %d, want 5000", ev.Rows)
	}
}

func TestJobStore_GetEvents_AfterSeq(t *testing.T) {
	store := newTestStore(t)
	store.CreateJob("job-seq", "migrate", "{}")

	for i := 1; i <= 5; i++ {
		store.WriteEvent("job-seq", "export_complete", "SCOTT", fmt.Sprintf("T%d", i), int64(i*100), "")
	}

	events, err := store.GetEvents("job-seq", 3)
	if err != nil {
		t.Fatalf("GetEvents(afterSeq=3): %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2 (seq 4 and 5)", len(events))
	}
	if events[0].Seq != 4 {
		t.Errorf("first event seq = %d, want 4", events[0].Seq)
	}
}

func TestJobStore_WriteCheckpoint(t *testing.T) {
	store := newTestStore(t)
	store.CreateJob("job-cp", "migrate", "{}")

	err := store.WriteCheckpoint("job-cp", "SCOTT", "EMP", true, 5000, false, 0, "", "")
	if err != nil {
		t.Fatalf("WriteCheckpoint: %v", err)
	}

	cps, err := store.GetCheckpoints("job-cp")
	if err != nil {
		t.Fatalf("GetCheckpoints: %v", err)
	}
	if len(cps) != 1 {
		t.Fatalf("len(checkpoints) = %d, want 1", len(cps))
	}
	cp := cps[0]
	if !cp.Exported || cp.ExportedRows != 5000 {
		t.Errorf("Exported=%v Rows=%d, want true/5000", cp.Exported, cp.ExportedRows)
	}
	if cp.Imported {
		t.Error("Imported should be false")
	}
}

func TestJobStore_WriteCheckpoint_Upsert(t *testing.T) {
	store := newTestStore(t)
	store.CreateJob("job-up", "migrate", "{}")

	store.WriteCheckpoint("job-up", "SCOTT", "EMP", true, 5000, false, 0, "", "")
	store.WriteCheckpoint("job-up", "SCOTT", "EMP", true, 5000, true, 4998, "SUCCESS", "")

	cps, _ := store.GetCheckpoints("job-up")
	if len(cps) != 1 {
		t.Fatalf("len(checkpoints) = %d, want 1 (upserted)", len(cps))
	}
	if !cps[0].Imported || cps[0].ImportedRows != 4998 {
		t.Errorf("Imported=%v Rows=%d, want true/4998", cps[0].Imported, cps[0].ImportedRows)
	}
	if cps[0].Status != "SUCCESS" {
		t.Errorf("Status = %q, want SUCCESS", cps[0].Status)
	}
}

func TestJobStore_MarkRunningAsInterrupted(t *testing.T) {
	store := newTestStore(t)

	store.CreateJob("job-run1", "migrate", "{}")
	store.CreateJob("job-run2", "export", "{}")
	store.CreateJob("job-done", "import", "{}")
	store.UpdateJobStatus("job-done", "completed")

	count, err := store.MarkRunningAsInterrupted()
	if err != nil {
		t.Fatalf("MarkRunningAsInterrupted: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}

	j1, _ := store.GetJob("job-run1")
	j2, _ := store.GetJob("job-run2")
	jd, _ := store.GetJob("job-done")
	if j1.Status != "interrupted" {
		t.Errorf("job-run1 status = %q, want interrupted", j1.Status)
	}
	if j2.Status != "interrupted" {
		t.Errorf("job-run2 status = %q, want interrupted", j2.Status)
	}
	if jd.Status != "completed" {
		t.Errorf("job-done status = %q, want completed (unchanged)", jd.Status)
	}
}

func TestJobStore_ConcurrentWrites(t *testing.T) {
	store := newTestStore(t)
	store.CreateJob("job-conc", "migrate", "{}")

	var wg sync.WaitGroup
	for g := 0; g < 5; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				store.WriteEvent("job-conc", "export_complete", "SCOTT",
					fmt.Sprintf("T%d_%d", gid, i), int64(i), "")
			}
		}(g)
	}
	wg.Wait()

	events, err := store.GetEvents("job-conc", 0)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 100 {
		t.Errorf("len(events) = %d, want 100", len(events))
	}
}

func TestJobStore_NodeIDUpgrade(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "upgrade.db")

	// Create a legacy database lacking node_id columns.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE jobs (job_id TEXT PRIMARY KEY, type TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'running', config TEXT, pid INTEGER DEFAULT 0, created_at TEXT DEFAULT (datetime('now')), finished_at TEXT)`,
		`CREATE TABLE job_checkpoints (job_id TEXT NOT NULL, schema TEXT NOT NULL, table_name TEXT NOT NULL, exported INTEGER DEFAULT 0, exported_rows INTEGER DEFAULT 0, imported INTEGER DEFAULT 0, imported_rows INTEGER DEFAULT 0, status TEXT DEFAULT '', error TEXT DEFAULT '', PRIMARY KEY (job_id, schema, table_name))`,
		`CREATE TABLE progress_events (id INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL, seq INTEGER NOT NULL, event_type TEXT NOT NULL, schema TEXT DEFAULT '', table_name TEXT DEFAULT '', rows INTEGER DEFAULT 0, message TEXT DEFAULT '', created_at TEXT DEFAULT (datetime('now')))`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("create legacy table: %v", err)
		}
	}
	raw.Close()

	store, err := NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore on legacy db: %v", err)
	}
	defer store.Close()

	if err := store.CreateJob("j1", "migrate", "{}"); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	var nodeID string
	if err := store.db.QueryRow(`SELECT node_id FROM jobs WHERE job_id = 'j1'`).Scan(&nodeID); err != nil {
		t.Fatalf("node_id missing: %v", err)
	}
	if nodeID != "local" {
		t.Errorf("node_id = %q, want \"local\"", nodeID)
	}
}

func TestJobStore_GenerationRetention(t *testing.T) {
	store, err := NewJobStore(filepath.Join(t.TempDir(), "gen.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	defer store.Close()

	var allPruned []string
	for i := 1; i <= 3; i++ {
		pruned, err := store.RecordGeneration("ddl", fmt.Sprintf("/tmp/gen-%d", i), 2)
		if err != nil {
			t.Fatalf("RecordGeneration(%d): %v", i, err)
		}
		allPruned = append(allPruned, pruned...)
	}

	if len(allPruned) != 1 || allPruned[0] != "/tmp/gen-1" {
		t.Fatalf("pruned = %v, want [/tmp/gen-1]", allPruned)
	}
	dir, err := store.LatestGeneration("ddl")
	if err != nil {
		t.Fatalf("LatestGeneration: %v", err)
	}
	if dir != "/tmp/gen-3" {
		t.Errorf("LatestGeneration = %q, want /tmp/gen-3", dir)
	}
	if _, err := store.LatestGeneration("insert"); err == nil {
		t.Error("LatestGeneration for unknown kind should error")
	}
}
