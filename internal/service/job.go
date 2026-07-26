package service

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Job struct {
	JobID      string  `json:"job_id"`
	Type       string  `json:"type"`
	Status     string  `json:"status"`
	Config     string  `json:"config"`
	PID        int     `json:"pid"`
	CreatedAt  string  `json:"created_at"`
	FinishedAt string  `json:"finished_at,omitempty"`
}

type ProgressEvent struct {
	ID        int64  `json:"id"`
	JobID     string `json:"job_id"`
	Seq       int64  `json:"seq"`
	EventType string `json:"event_type"`
	Schema    string `json:"schema"`
	TableName string `json:"table_name"`
	Rows      int64  `json:"rows"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

type JobCheckpoint struct {
	JobID        string `json:"job_id"`
	Schema       string `json:"schema"`
	TableName    string `json:"table_name"`
	Exported     bool   `json:"exported"`
	ExportedRows int64  `json:"exported_rows"`
	Imported     bool   `json:"imported"`
	ImportedRows int64  `json:"imported_rows"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
}

type JobStore struct {
	db *sql.DB
}

func NewJobStore(dbPath string) (*JobStore, error) {
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &JobStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return s, nil
}

func (s *JobStore) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS jobs (
    job_id      TEXT PRIMARY KEY,
    type        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'running',
    config      TEXT,
    pid         INTEGER DEFAULT 0,
    created_at  TEXT DEFAULT (datetime('now')),
    finished_at TEXT
);

CREATE TABLE IF NOT EXISTS job_checkpoints (
    job_id        TEXT NOT NULL,
    schema        TEXT NOT NULL,
    table_name    TEXT NOT NULL,
    exported      INTEGER DEFAULT 0,
    exported_rows INTEGER DEFAULT 0,
    imported      INTEGER DEFAULT 0,
    imported_rows INTEGER DEFAULT 0,
    status        TEXT DEFAULT '',
    error         TEXT DEFAULT '',
    PRIMARY KEY (job_id, schema, table_name)
);

CREATE TABLE IF NOT EXISTS progress_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id     TEXT NOT NULL,
    seq        INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    schema     TEXT DEFAULT '',
    table_name TEXT DEFAULT '',
    rows       INTEGER DEFAULT 0,
    message    TEXT DEFAULT '',
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_events_job_seq ON progress_events(job_id, seq);
`
	_, err := s.db.Exec(schema)
	return err
}

func (s *JobStore) CreateJob(jobID, jobType, configJSON string) error {
	_, err := s.db.Exec(
		`INSERT INTO jobs (job_id, type, status, config) VALUES (?, ?, 'running', ?)`,
		jobID, jobType, configJSON,
	)
	return err
}

func (s *JobStore) GetJob(jobID string) (*Job, error) {
	row := s.db.QueryRow(
		`SELECT job_id, type, status, config, pid, created_at, COALESCE(finished_at, '') FROM jobs WHERE job_id = ?`,
		jobID,
	)
	var j Job
	err := row.Scan(&j.JobID, &j.Type, &j.Status, &j.Config, &j.PID, &j.CreatedAt, &j.FinishedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job %q not found", jobID)
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *JobStore) ListJobs(limit int) ([]Job, error) {
	rows, err := s.db.Query(
		`SELECT job_id, type, status, config, pid, created_at, COALESCE(finished_at, '') FROM jobs ORDER BY created_at DESC, rowid DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.JobID, &j.Type, &j.Status, &j.Config, &j.PID, &j.CreatedAt, &j.FinishedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *JobStore) UpdateJobStatus(jobID, status string) error {
	var finishedAt *string
	switch status {
	case "completed", "failed", "cancelled", "interrupted":
		now := time.Now().Format("2006-01-02 15:04:05")
		finishedAt = &now
	}
	_, err := s.db.Exec(
		`UPDATE jobs SET status = ?, finished_at = COALESCE(?, finished_at) WHERE job_id = ?`,
		status, finishedAt, jobID,
	)
	return err
}

func (s *JobStore) UpdateJobPID(jobID string, pid int) error {
	_, err := s.db.Exec(`UPDATE jobs SET pid = ? WHERE job_id = ?`, pid, jobID)
	return err
}

func (s *JobStore) WriteEvent(jobID, eventType, schema, tableName string, rows int64, message string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var maxSeq sql.NullInt64
	if err := tx.QueryRow(
		`SELECT MAX(seq) FROM progress_events WHERE job_id = ?`, jobID,
	).Scan(&maxSeq); err != nil {
		return err
	}
	nextSeq := int64(1)
	if maxSeq.Valid {
		nextSeq = maxSeq.Int64 + 1
	}

	_, err = tx.Exec(
		`INSERT INTO progress_events (job_id, seq, event_type, schema, table_name, rows, message) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		jobID, nextSeq, eventType, schema, tableName, rows, message,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *JobStore) GetEvents(jobID string, afterSeq int64) ([]ProgressEvent, error) {
	rows, err := s.db.Query(
		`SELECT id, job_id, seq, event_type, schema, table_name, rows, message, created_at
		 FROM progress_events WHERE job_id = ? AND seq > ? ORDER BY seq`,
		jobID, afterSeq,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []ProgressEvent
	for rows.Next() {
		var e ProgressEvent
		if err := rows.Scan(&e.ID, &e.JobID, &e.Seq, &e.EventType, &e.Schema, &e.TableName, &e.Rows, &e.Message, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *JobStore) WriteCheckpoint(jobID, schema, tableName string, exported bool, exportedRows int64, imported bool, importedRows int64, status, errMsg string) error {
	_, err := s.db.Exec(
		`INSERT INTO job_checkpoints (job_id, schema, table_name, exported, exported_rows, imported, imported_rows, status, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(job_id, schema, table_name) DO UPDATE SET
		   exported = excluded.exported,
		   exported_rows = excluded.exported_rows,
		   imported = excluded.imported,
		   imported_rows = excluded.imported_rows,
		   status = excluded.status,
		   error = excluded.error`,
		jobID, schema, tableName, boolToInt(exported), exportedRows, boolToInt(imported), importedRows, status, errMsg,
	)
	return err
}

func (s *JobStore) GetCheckpoints(jobID string) ([]JobCheckpoint, error) {
	rows, err := s.db.Query(
		`SELECT job_id, schema, table_name, exported, exported_rows, imported, imported_rows, status, error
		 FROM job_checkpoints WHERE job_id = ? ORDER BY schema, table_name`,
		jobID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cps []JobCheckpoint
	for rows.Next() {
		var cp JobCheckpoint
		var exported, imported int
		if err := rows.Scan(&cp.JobID, &cp.Schema, &cp.TableName, &exported, &cp.ExportedRows, &imported, &cp.ImportedRows, &cp.Status, &cp.Error); err != nil {
			return nil, err
		}
		cp.Exported = exported != 0
		cp.Imported = imported != 0
		cps = append(cps, cp)
	}
	return cps, rows.Err()
}

func (s *JobStore) MarkRunningAsInterrupted() (int64, error) {
	res, err := s.db.Exec(
		`UPDATE jobs SET status = 'interrupted', finished_at = datetime('now') WHERE status = 'running'`,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *JobStore) Close() error {
	return s.db.Close()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
