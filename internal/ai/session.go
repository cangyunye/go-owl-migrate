package ai

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	// Pure-Go SQLite: sessions must work in CGO_ENABLED=0 release builds.
	_ "modernc.org/sqlite"
)

// Session is the structured conversation state. The session object IS the
// memory: chat logs are never replayed wholesale, so context stays bounded
// regardless of conversation length (design doc §4).
//
// Stage lifecycle: collecting (slots incomplete) → confirming (plan drafted,
// waiting for user go-ahead) → executing (handed to the job layer) → done
// (archived; still referencable for slot inheritance within TTL).
type Session struct {
	ID         string            `json:"session_id"`
	Intent     string            `json:"intent"`
	Sub        string            `json:"sub,omitempty"`
	Slots      map[string]string `json:"slots"`
	Turns      []Turn            `json:"turns"`
	Stage      string            `json:"stage"`
	Artifacts  []Artifact        `json:"artifacts"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Round      int               `json:"round"` // how many tasks this session has completed
}

const (
	// StageCollecting means required slots are still missing.
	StageCollecting = "collecting"
	// StageConfirming means a plan exists and awaits explicit user confirmation.
	StageConfirming = "confirming"
	// StageExecuting means the plan was handed to the job layer.
	StageExecuting = "executing"
	// StageDone means the task finished; the session is archived but can seed
	// a follow-up round via CloneForRound.
	StageDone = "done"

	// TTLDays is how long sessions (including done ones) stay referencable.
	TTLDays = 1
	// keepTurns is the chat-log window: system prompt + this many recent turns
	// keep every request bounded at roughly 10–30KB.
	keepTurns = 8
)

// Turn is one bounded chat-log entry for vendor context replay.
type Turn struct {
	Role      string    `json:"role"` // user | assistant
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Artifact is a server-side product of the session (a generated plan, a job id).
type Artifact struct {
	Kind      string    `json:"kind"` // plan | job
	ID        string    `json:"id"`
	YAML      string    `json:"yaml,omitempty"` // credential-masked
	CreatedAt time.Time `json:"created_at"`
}

// SessionStore persists sessions in a dedicated SQLite file. Separate from the
// job store on purpose: the AI layer is optional and must not weigh on core
// migrations.
type SessionStore struct {
	db *sql.DB
}

// OpenSessions creates/opens the session database (ai/sessions/sessions.db).
func OpenSessions(dir string) (*SessionStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.db"))
	if err != nil {
		return nil, err
	}
	const schema = `
CREATE TABLE IF NOT EXISTS ai_sessions (
	id         TEXT PRIMARY KEY,
	intent     TEXT NOT NULL,
	sub        TEXT,
	slots      TEXT,
	turns      TEXT,
	stage      TEXT,
	artifacts  TEXT,
	round      INTEGER DEFAULT 0,
	created_at TEXT,
	updated_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_ai_sessions_updated ON ai_sessions(updated_at);`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	s := &SessionStore{db: db}
	s.purgeExpired()
	return s, nil
}

// Close releases the SQLite handle.
func (s *SessionStore) Close() error { return s.db.Close() }

func (s *SessionStore) purgeExpired() {
	cutoff := time.Now().AddDate(0, 0, -TTLDays).Format(time.RFC3339)
	s.db.Exec(`DELETE FROM ai_sessions WHERE updated_at < ?`, cutoff)
}

// NewSessionID returns a random opaque session id.
func NewSessionID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("s-%d", time.Now().UnixNano())
	}
	return "s-" + hex.EncodeToString(b)
}

// Create stores a fresh session for the given intent.
func (s *SessionStore) Create(intent, sub string) (*Session, error) {
	now := time.Now()
	sess := &Session{
		ID:        NewSessionID(),
		Intent:    intent,
		Sub:       sub,
		Slots:     map[string]string{},
		Turns:     []Turn{},
		Stage:     StageCollecting,
		Artifacts: []Artifact{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	return sess, s.save(sess)
}

// Get loads one session; expired or unknown sessions return ok=false (the
// caller treats that as "start fresh", never as an error).
func (s *SessionStore) Get(id string) (*Session, bool) {
	if id == "" {
		return nil, false
	}
	row := s.db.QueryRow(`SELECT intent, sub, slots, turns, stage, artifacts, round, created_at, updated_at
		FROM ai_sessions WHERE id = ?`, id)
	var sess Session
	var slotsJSON, turnsJSON, artJSON, created, updated string
	if err := row.Scan(&sess.Intent, &sess.Sub, &slotsJSON, &turnsJSON, &sess.Stage,
		&artJSON, &sess.Round, &created, &updated); err != nil {
		return nil, false
	}
	sess.ID = id
	json.Unmarshal([]byte(slotsJSON), &sess.Slots)
	json.Unmarshal([]byte(turnsJSON), &sess.Turns)
	json.Unmarshal([]byte(artJSON), &sess.Artifacts)
	sess.CreatedAt, _ = time.Parse(time.RFC3339, created)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	if time.Since(sess.UpdatedAt) > TTLDays*24*time.Hour {
		s.db.Exec(`DELETE FROM ai_sessions WHERE id = ?`, id)
		return nil, false
	}
	if sess.Slots == nil {
		sess.Slots = map[string]string{}
	}
	return &sess, true
}

// save writes the full session row (upsert).
func (s *SessionStore) save(sess *Session) error {
	slots, _ := json.Marshal(sess.Slots)
	turns, _ := json.Marshal(sess.Turns)
	arts, _ := json.Marshal(sess.Artifacts)
	sess.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO ai_sessions
		(id, intent, sub, slots, turns, stage, artifacts, round, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET intent=?, sub=?, slots=?, turns=?, stage=?, artifacts=?, round=?, updated_at=?`,
		sess.ID, sess.Intent, sess.Sub, string(slots), string(turns), sess.Stage,
		string(arts), sess.Round, sess.CreatedAt.Format(time.RFC3339), sess.UpdatedAt.Format(time.RFC3339),
		sess.Intent, sess.Sub, string(slots), string(turns), sess.Stage,
		string(arts), sess.Round, sess.UpdatedAt.Format(time.RFC3339))
	return err
}

// AddTurn appends one chat-log entry, trimming to the bounded window.
func (s *SessionStore) AddTurn(sess *Session, role, content string) error {
	sess.Turns = append(sess.Turns, Turn{Role: role, Content: content, CreatedAt: time.Now()})
	if len(sess.Turns) > keepTurns {
		sess.Turns = sess.Turns[len(sess.Turns)-keepTurns:]
	}
	return s.save(sess)
}

// MergeSlots folds new slot values over existing ones and persists. Empty
// values never overwrite (a blank "还是原来的库" must not erase the slot).
func (s *SessionStore) MergeSlots(sess *Session, slots map[string]string) error {
	for k, v := range slots {
		if v != "" {
			sess.Slots[k] = v
		}
	}
	return s.save(sess)
}

// SetStage transitions the session and persists.
func (s *SessionStore) SetStage(sess *Session, stage string) error {
	sess.Stage = stage
	return s.save(sess)
}

// AddArtifact records a plan/job artifact and persists.
func (s *SessionStore) AddArtifact(sess *Session, art Artifact) error {
	art.CreatedAt = time.Now()
	sess.Artifacts = append(sess.Artifacts, art)
	return s.save(sess)
}

// CloneForRound starts a NEW session inheriting this one's slots — the
// "same task, next round" semantics ("继续导出 Where 条件 2"): history does
// not replay, but the reusable facts (库/表/格式/凭据引用) carry forward.
func (s *SessionStore) CloneForRound(sess *Session, intent, sub string) (*Session, error) {
	next, err := s.Create(intent, sub)
	if err != nil {
		return nil, err
	}
	for k, v := range sess.Slots {
		next.Slots[k] = v
	}
	next.Round = sess.Round + 1
	return next, s.save(next)
}
