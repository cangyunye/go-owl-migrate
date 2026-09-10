package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProgressWriter_WriteEvents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	defer store.Close()

	store.CreateJob("job-pw", "migrate", "{}")

	pw, err := NewProgressWriter(dbPath, "job-pw")
	if err != nil {
		t.Fatalf("NewProgressWriter: %v", err)
	}
	defer pw.Close()

	if err := pw.WriteExportComplete("SCOTT", "EMP", 5000); err != nil {
		t.Fatalf("WriteExportComplete: %v", err)
	}
	if err := pw.WriteImportComplete("SCOTT", "EMP", 4998, 2, ""); err != nil {
		t.Fatalf("WriteImportComplete: %v", err)
	}

	events, _ := store.GetEvents("job-pw", 0)
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].EventType != "export_complete" {
		t.Errorf("event[0] type = %q, want export_complete", events[0].EventType)
	}
	if events[1].EventType != "import_complete" {
		t.Errorf("event[1] type = %q, want import_complete", events[1].EventType)
	}

	cps, _ := store.GetCheckpoints("job-pw")
	if len(cps) != 1 {
		t.Fatalf("len(checkpoints) = %d, want 1", len(cps))
	}
	if !cps[0].Exported || !cps[0].Imported {
		t.Error("checkpoint should have both exported and imported = true")
	}
	if cps[0].Status != "SUCCESS" {
		t.Errorf("status = %q, want SUCCESS", cps[0].Status)
	}
}

func TestProgressWriter_ImportWithError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, _ := NewJobStore(dbPath)
	defer store.Close()
	store.CreateJob("job-err", "import", "{}")

	pw, _ := NewProgressWriter(dbPath, "job-err")
	defer pw.Close()

	pw.WriteImportComplete("SCOTT", "DEPT", 0, 0, "connection refused")

	cps, _ := store.GetCheckpoints("job-err")
	if len(cps) != 1 {
		t.Fatalf("len(checkpoints) = %d, want 1", len(cps))
	}
	if cps[0].Status != "FAIL" {
		t.Errorf("status = %q, want FAIL", cps[0].Status)
	}
	if cps[0].Error != "connection refused" {
		t.Errorf("error = %q, want 'connection refused'", cps[0].Error)
	}
}

func TestProgressWriter_JobStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, _ := NewJobStore(dbPath)
	defer store.Close()
	store.CreateJob("job-status", "migrate", "{}")

	pw, _ := NewProgressWriter(dbPath, "job-status")
	defer pw.Close()

	pw.SetJobCompleted()
	job, _ := store.GetJob("job-status")
	if job.Status != "completed" {
		t.Errorf("status = %q, want completed", job.Status)
	}
}

func TestProgressWriter_JobFailed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, _ := NewJobStore(dbPath)
	defer store.Close()
	store.CreateJob("job-fail", "migrate", "{}")

	pw, _ := NewProgressWriter(dbPath, "job-fail")
	defer pw.Close()

	pw.SetJobFailed("export error")
	job, _ := store.GetJob("job-fail")
	if job.Status != "failed" {
		t.Errorf("status = %q, want failed", job.Status)
	}
}

// A command's deferred error reporter may run after the caller already
// finalized the job (e.g. the aggregated table-error path); it must not
// overwrite a terminal status or append a second error event.
func TestProgressWriter_SetJobFailed_IsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, _ := NewJobStore(dbPath)
	defer store.Close()
	store.CreateJob("job-idem", "migrate", "{}")

	pw, _ := NewProgressWriter(dbPath, "job-idem")
	defer pw.Close()

	pw.SetJobCompleted()
	if err := pw.SetJobFailed("late fatal error"); err != nil {
		t.Fatalf("SetJobFailed() error = %v", err)
	}
	job, _ := store.GetJob("job-idem")
	if job.Status != "completed" {
		t.Errorf("status = %q, want completed (failure must not overwrite a terminal status)", job.Status)
	}
	events, _ := store.GetEvents("job-idem", 0)
	for _, e := range events {
		if e.EventType == "error" {
			t.Errorf("unexpected error event %q appended after completion", e.Message)
		}
	}
}

// --continue-on-error runs finish, but must not look like a clean success.
func TestProgressWriter_CompletedWithErrors(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, _ := NewJobStore(dbPath)
	defer store.Close()
	store.CreateJob("job-partial", "migrate", "{}")

	pw, _ := NewProgressWriter(dbPath, "job-partial")
	defer pw.Close()

	if err := pw.SetJobCompletedWithErrors("2 export errors, 1 import errors"); err != nil {
		t.Fatalf("SetJobCompletedWithErrors() error = %v", err)
	}
	job, _ := store.GetJob("job-partial")
	if job.Status != "completed_with_errors" {
		t.Errorf("status = %q, want completed_with_errors", job.Status)
	}
	if job.FinishedAt == "" {
		t.Error("finished_at not set for completed_with_errors")
	}

	// The deferred failure reporter runs after this; it must not downgrade it.
	pw.SetJobFailed("late fatal error")
	job, _ = store.GetJob("job-partial")
	if job.Status != "completed_with_errors" {
		t.Errorf("status = %q, want completed_with_errors after late SetJobFailed", job.Status)
	}
	events, _ := store.GetEvents("job-partial", 0)
	hasWarning := false
	for _, e := range events {
		if e.EventType == "warning" {
			hasWarning = true
		}
		if e.EventType == "error" {
			t.Errorf("unexpected error event %q", e.Message)
		}
	}
	if !hasWarning {
		t.Error("missing warning event summarizing the partial failure")
	}
}

func TestProgressWriter_WriteStage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, _ := NewJobStore(dbPath)
	defer store.Close()
	store.CreateJob("job-stage", "migrate", "{}")

	pw, _ := NewProgressWriter(dbPath, "job-stage")
	defer pw.Close()

	if err := pw.WriteStage("connect_target"); err != nil {
		t.Fatalf("WriteStage() error = %v", err)
	}
	events, _ := store.GetEvents("job-stage", 0)
	if len(events) != 1 || events[0].EventType != "stage" || events[0].Message != "connect_target" {
		t.Errorf("events = %+v, want one stage event for connect_target", events)
	}
}

func TestHeartbeatMonitor_DetectsStale(t *testing.T) {
	hbPath := filepath.Join(t.TempDir(), "heartbeat")

	os.WriteFile(hbPath, []byte("123 1000000"), 0644)

	monitor := NewHeartbeatMonitor(hbPath, 50*time.Millisecond, 150*time.Millisecond)

	detected := make(chan struct{})
	monitor.OnParentDeath = func() {
		close(detected)
	}

	ctx := t.Context()
	monitor.Start(ctx)

	select {
	case <-detected:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat monitor did not detect stale parent within 2s")
	}
}

func TestHeartbeatMonitor_AliveParent(t *testing.T) {
	hbPath := filepath.Join(t.TempDir(), "heartbeat")

	monitor := NewHeartbeatMonitor(hbPath, 50*time.Millisecond, 500*time.Millisecond)

	detected := make(chan struct{}, 1)
	monitor.OnParentDeath = func() {
		detected <- struct{}{}
	}

	ctx := t.Context()
	monitor.Start(ctx)

	for i := 0; i < 5; i++ {
		os.WriteFile(hbPath, []byte("123 "+time.Now().Format("1136239445")), 0644)
		time.Sleep(60 * time.Millisecond)
	}

	select {
	case <-detected:
		t.Fatal("should not detect death when heartbeat is fresh")
	default:
	}
}
