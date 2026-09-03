package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNoGeneration is returned by LatestGeneration when no output of a kind
// has been recorded yet.
var ErrNoGeneration = errors.New("nothing generated yet")

type Job struct {
	JobID      string `json:"job_id"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Config     string `json:"config"`
	PID        int    `json:"pid"`
	CreatedAt  string `json:"created_at"`
	FinishedAt string `json:"finished_at,omitempty"`
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

// GenerationMeta carries the display metadata recorded with a generation
// output. The full DSN is never stored — only a password-free label.
type GenerationMeta struct {
	SourceLabel    string         `json:"source_label"`
	DatasourceName string         `json:"datasource_name,omitempty"`
	Detail         map[string]any `json:"detail"`
}

// GenerationRecord is a persisted generation output row.
type GenerationRecord struct {
	ID             int64          `json:"id"`
	Kind           string         `json:"kind"`
	Dir            string         `json:"dir"`
	CreatedAt      string         `json:"created_at"`
	SourceLabel    string         `json:"source_label"`
	DatasourceName string         `json:"datasource_name,omitempty"`
	Detail         map[string]any `json:"detail"`
}

type JobStore struct {
	db *sql.DB
}

func NewJobStore(dbPath string) (*JobStore, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", dbPath)
	db, err := sql.Open("sqlite", dsn)
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
    finished_at TEXT,
    node_id     TEXT NOT NULL DEFAULT 'local'
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
    node_id       TEXT NOT NULL DEFAULT 'local',
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
    created_at TEXT DEFAULT (datetime('now')),
    node_id    TEXT NOT NULL DEFAULT 'local'
);

CREATE INDEX IF NOT EXISTS idx_events_job_seq ON progress_events(job_id, seq);

CREATE TABLE IF NOT EXISTS generation_outputs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,
    dir        TEXT NOT NULL,
    created_at TEXT DEFAULT (datetime('now')),
    source_label    TEXT NOT NULL DEFAULT '',
    datasource_name TEXT NOT NULL DEFAULT '',
    detail          TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_gen_kind ON generation_outputs(kind, id);
`
	_, err := s.db.Exec(schema)
	if err != nil {
		return err
	}
	if err := s.addNodeIDColumns(); err != nil {
		return err
	}
	if err := s.addGenOutputColumns(); err != nil {
		return err
	}
	return nil
}

// addNodeIDColumns backfills the 2.0 node_id seam into databases created
// before the column existed. Tables are hardcoded; names never come from input.
func (s *JobStore) addNodeIDColumns() error {
	for _, tbl := range []string{"jobs", "job_checkpoints", "progress_events"} {
		var n int
		q := fmt.Sprintf(`SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = 'node_id'`, tbl)
		if err := s.db.QueryRow(q).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			q = fmt.Sprintf(`ALTER TABLE %s ADD COLUMN node_id TEXT NOT NULL DEFAULT 'local'`, tbl)
			if _, err := s.db.Exec(q); err != nil {
				return err
			}
		}
	}
	return nil
}

// addGenOutputColumns backfills the generation-history seam into databases
// created before the columns existed.
func (s *JobStore) addGenOutputColumns() error {
	for _, col := range []string{"source_label", "datasource_name", "detail"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('generation_outputs') WHERE name = ?`, col).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE generation_outputs ADD COLUMN %s TEXT NOT NULL DEFAULT ''`, col)); err != nil {
				return err
			}
		}
	}
	return nil
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

// RecordGeneration stores a generation output record.
func (s *JobStore) RecordGeneration(kind, dir string, meta GenerationMeta) error {
	detail := "{}"
	if meta.Detail != nil {
		if b, err := json.Marshal(meta.Detail); err == nil {
			detail = string(b)
		}
	}
	_, err := s.db.Exec(
		`INSERT INTO generation_outputs (kind, dir, source_label, datasource_name, detail)
		 VALUES (?, ?, ?, ?, ?)`,
		kind, dir, meta.SourceLabel, meta.DatasourceName, detail,
	)
	return err
}

// PruneGenerations removes records beyond keep (by age) or older than maxAge
// for a kind, returning the dirs that were deleted so callers can remove them
// from disk. Both limits apply; either one tripping deletes the row.
func (s *JobStore) PruneGenerations(kind string, keep int, maxAge time.Duration) ([]string, error) {
	cutoff := time.Now().UTC().Add(-maxAge).Format("2006-01-02 15:04:05")
	rows, err := s.db.Query(
		`SELECT id, dir FROM generation_outputs WHERE kind = ?
		 AND (id NOT IN (SELECT id FROM generation_outputs WHERE kind = ? ORDER BY id DESC LIMIT ?)
		      OR created_at < ?)`,
		kind, kind, keep, cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type stale struct {
		id  int64
		dir string
	}
	var stales []stale
	for rows.Next() {
		var p stale
		if err := rows.Scan(&p.id, &p.dir); err != nil {
			return nil, err
		}
		stales = append(stales, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	dirs := make([]string, 0, len(stales))
	for _, p := range stales {
		if _, err := s.db.Exec(`DELETE FROM generation_outputs WHERE id = ?`, p.id); err != nil {
			return dirs, err
		}
		dirs = append(dirs, p.dir)
	}
	return dirs, nil
}

// ListGenerations returns generation records for a kind, newest first.
func (s *JobStore) ListGenerations(kind string) ([]GenerationRecord, error) {
	rows, err := s.db.Query(
		`SELECT id, kind, dir, created_at, source_label, datasource_name, detail
		 FROM generation_outputs WHERE kind = ? ORDER BY id DESC`, kind,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var recs []GenerationRecord
	for rows.Next() {
		var r GenerationRecord
		var detail string
		if err := rows.Scan(&r.ID, &r.Kind, &r.Dir, &r.CreatedAt, &r.SourceLabel, &r.DatasourceName, &detail); err != nil {
			return nil, err
		}
		if detail != "" {
			_ = json.Unmarshal([]byte(detail), &r.Detail)
		}
		recs = append(recs, r)
	}
	return recs, rows.Err()
}

// GetGeneration returns one generation record by id.
func (s *JobStore) GetGeneration(id int64) (GenerationRecord, error) {
	var r GenerationRecord
	var detail string
	err := s.db.QueryRow(
		`SELECT id, kind, dir, created_at, source_label, datasource_name, detail
		 FROM generation_outputs WHERE id = ?`, id,
	).Scan(&r.ID, &r.Kind, &r.Dir, &r.CreatedAt, &r.SourceLabel, &r.DatasourceName, &detail)
	if err == sql.ErrNoRows {
		return r, fmt.Errorf("%w: generation %d", ErrNoGeneration, id)
	}
	if err != nil {
		return r, err
	}
	if detail != "" {
		_ = json.Unmarshal([]byte(detail), &r.Detail)
	}
	return r, nil
}

// LatestGeneration returns the most recent output dir for kind.
func (s *JobStore) LatestGeneration(kind string) (string, error) {
	var dir string
	err := s.db.QueryRow(
		`SELECT dir FROM generation_outputs WHERE kind = ? ORDER BY id DESC LIMIT 1`, kind,
	).Scan(&dir)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("%w: %s", ErrNoGeneration, kind)
	}
	if err != nil {
		return "", err
	}
	return dir, nil
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
