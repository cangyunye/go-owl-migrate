// Package cdc implements the online incremental migration engine: changelog
// polling, ordered replay, and checkpoint persistence (StateStore).
package cdc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Checkpoint is the per-table sync watermark (by changelog sequence id).
type Checkpoint struct {
	TableName   string `json:"table_name"`
	ShardID     int64  `json:"shard_id"`
	FiledChgID  int64  `json:"filed_chgid"`            // files spilled / rows polled so far
	AckedChgID  int64  `json:"acked_chgid"`            // acknowledged (applied) so far
	MappedTable string `json:"mapped_table,omitempty"` // target table name (after mapping)
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// StateStore persists checkpoints durably (crash-safe).
type StateStore interface {
	LoadCheckpoints() (map[string]Checkpoint, error)
	SaveCheckpoint(cp Checkpoint) error
	Close() error
}

// JSONStateStore persists checkpoints to a JSON file with atomic rename.
type JSONStateStore struct {
	path string
	data map[string]Checkpoint
}

// NewJSONStateStore creates a JSON-backed state store. A missing file yields an
// empty checkpoint map; writes are atomic (tmp + rename) on Close and on every
// SaveCheckpoint via flush.
func NewJSONStateStore(path string) (*JSONStateStore, error) {
	s := &JSONStateStore{path: path, data: make(map[string]Checkpoint)}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read state file: %w", err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.data); err != nil {
			// Corrupt file: start fresh rather than blocking sync.
			s.data = make(map[string]Checkpoint)
		}
	}
	if s.data == nil {
		s.data = make(map[string]Checkpoint)
	}
	return s, nil
}

// LoadCheckpoints returns a copy of all checkpoints keyed by table name.
func (s *JSONStateStore) LoadCheckpoints() (map[string]Checkpoint, error) {
	out := make(map[string]Checkpoint, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out, nil
}

// SaveCheckpoint upserts a checkpoint and persists the whole map atomically.
func (s *JSONStateStore) SaveCheckpoint(cp Checkpoint) error {
	cp.UpdatedAt = time.Now().Format(time.RFC3339)
	s.data[cp.TableName] = cp
	return s.flush()
}

// Close persists any pending state (a no-op given save-on-write, but keeps the
// interface consistent and allows a final flush).
func (s *JSONStateStore) Close() error {
	return s.flush()
}

func (s *JSONStateStore) flush() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(s.sorted(), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	// Atomic write: tmp + rename.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write tmp state: %w", err)
	}
	return os.Rename(tmp, s.path)
}

// sorted returns deterministically ordered checkpoints keyed by table name.
func (s *JSONStateStore) sorted() map[string]Checkpoint {
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]Checkpoint, len(keys))
	for _, k := range keys {
		out[k] = s.data[k]
	}
	return out
}
