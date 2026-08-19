package cdc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BatchWriter spills a partition of changes into an atomic batch SQL file in a
// pending directory (file-batch / client fallback mode). One file is written
// per (table, seq) and may contain many changes; it is discovered by an
// external runner which executes it and moves it to done/ or failed/.
type BatchWriter struct {
	PendingDir          string         // directory for not-yet-executed files
	Begin               string         // optional transaction-open statement ("" = rely on implicit)
	Commit              string         // required transaction-commit statement
	Wrap                bool           // if true, wrap file contents in Begin/Commit
	FMValue             ValueFormatter // literal value formatter (default: DefaultValueFormatter)
	PlaceholderPerTable bool           // reserved
}

// Write renders the changes for one table into a batch file and atomically
// places it in the pending directory. Returns the pending batch metadata.
func (w *BatchWriter) Write(table string, changes []Change, tt *TargetTable, now time.Time) (PendingBatch, error) {
	if len(changes) == 0 {
		return PendingBatch{}, fmt.Errorf("no changes to write")
	}
	if w.Commit == "" {
		return PendingBatch{}, fmt.Errorf("batch writer requires a commit statement")
	}
	if err := os.MkdirAll(w.PendingDir, 0755); err != nil {
		return PendingBatch{}, fmt.Errorf("create pending dir: %w", err)
	}

	fmv := w.FMValue
	if fmv == nil {
		fmv = DefaultValueFormatter
	}

	// Compute chg_id range.
	var startID, endID int64
	for i, c := range changes {
		if i == 0 || c.ChgID < startID {
			startID = c.ChgID
		}
		if c.ChgID > endID {
			endID = c.ChgID
		}
	}

	// A TRUNCATE change is isolated in its own file.
	isTruncate := len(changes) == 1 && strings.EqualFold(changes[0].OpType, "T")

	seq, err := NextSeqFromPending(w.PendingDir)
	if err != nil {
		return PendingBatch{}, fmt.Errorf("compute seq: %w", err)
	}

	fileBase := BatchFileName(seq, now, now, table, startID, endID, isTruncate)

	var sb strings.Builder
	if w.Wrap && w.Begin != "" {
		sb.WriteString(w.Begin)
		sb.WriteString("\n")
	}
	for _, ch := range changes {
		lit, err := BuildReplayLiterals(tt, ch, fmv)
		if err != nil {
			return PendingBatch{}, fmt.Errorf("render change %d: %w", ch.ChgID, err)
		}
		sb.WriteString(lit)
		sb.WriteString("\n")
	}
	// Always end with the required commit so the file is a single committed unit.
	sb.WriteString(w.Commit)
	sb.WriteString("\n")

	// Atomic write: tmp + rename.
	tmpPath := filepath.Join(w.PendingDir, fileBase+".tmp")
	finalPath := filepath.Join(w.PendingDir, fileBase)
	if err := os.WriteFile(tmpPath, []byte(sb.String()), 0644); err != nil {
		return PendingBatch{}, fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return PendingBatch{}, fmt.Errorf("rename tmp: %w", err)
	}
	return PendingBatch{
		Dir:      w.PendingDir,
		FileName: fileBase,
		Path:     finalPath,
		Seq:      seq,
	}, nil
}
