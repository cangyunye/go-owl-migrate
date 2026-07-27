package service

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type ProgressWriter struct {
	store *JobStore
	jobID string
}

func NewProgressWriter(dbPath, jobID string) (*ProgressWriter, error) {
	store, err := NewJobStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open progress db: %w", err)
	}
	return &ProgressWriter{store: store, jobID: jobID}, nil
}

func (pw *ProgressWriter) WriteExportComplete(schema, table string, rows int64) error {
	if err := pw.store.WriteEvent(pw.jobID, "export_complete", schema, table, rows,
		fmt.Sprintf("exported %d rows", rows)); err != nil {
		return err
	}
	return pw.store.WriteCheckpoint(pw.jobID, schema, table, true, rows, false, 0, "", "")
}

func (pw *ProgressWriter) WriteImportComplete(schema, table string, rows, skipped int64, errMsg string) error {
	status := "SUCCESS"
	if errMsg != "" {
		status = "FAIL"
	}
	msg := fmt.Sprintf("imported %d rows", rows)
	if skipped > 0 {
		msg += fmt.Sprintf(", %d skipped", skipped)
	}
	if err := pw.store.WriteEvent(pw.jobID, "import_complete", schema, table, rows, msg); err != nil {
		return err
	}
	return pw.store.WriteCheckpoint(pw.jobID, schema, table, true, 0, true, rows, status, errMsg)
}

func (pw *ProgressWriter) SetJobCompleted() error {
	return pw.store.UpdateJobStatus(pw.jobID, "completed")
}

// WriteTableError records a per-table failure as an event and a FAIL checkpoint.
func (pw *ProgressWriter) WriteTableError(schema, table, msg string) error {
	if err := pw.store.WriteEvent(pw.jobID, "error", schema, table, 0, msg); err != nil {
		return err
	}
	return pw.store.WriteCheckpoint(pw.jobID, schema, table, false, 0, false, 0, "FAIL", msg)
}

func (pw *ProgressWriter) SetJobFailed(reason string) error {
	pw.store.WriteEvent(pw.jobID, "error", "", "", 0, reason)
	return pw.store.UpdateJobStatus(pw.jobID, "failed")
}

func (pw *ProgressWriter) SetJobInterrupted() error {
	return pw.store.UpdateJobStatus(pw.jobID, "interrupted")
}

func (pw *ProgressWriter) Close() error {
	return pw.store.Close()
}

type HeartbeatMonitor struct {
	heartbeatPath string
	checkInterval time.Duration
	staleThreshold time.Duration
	OnParentDeath func()
}

func NewHeartbeatMonitor(heartbeatPath string, checkInterval, staleThreshold time.Duration) *HeartbeatMonitor {
	return &HeartbeatMonitor{
		heartbeatPath:  heartbeatPath,
		checkInterval:  checkInterval,
		staleThreshold: staleThreshold,
	}
}

func (hm *HeartbeatMonitor) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(hm.checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if hm.isStale() {
					if hm.OnParentDeath != nil {
						hm.OnParentDeath()
					}
					return
				}
			}
		}
	}()
}

func (hm *HeartbeatMonitor) isStale() bool {
	data, err := os.ReadFile(hm.heartbeatPath)
	if err != nil {
		return true
	}
	parts := strings.Fields(string(data))
	if len(parts) < 2 {
		return true
	}
	ts, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return true
	}
	age := time.Since(time.Unix(ts, 0))
	return age > hm.staleThreshold
}
