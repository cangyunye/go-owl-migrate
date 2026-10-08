package ai

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	ID        string            `json:"session_id"`
	Intent    string            `json:"intent"`
	Sub       string            `json:"sub,omitempty"`
	Title     string            `json:"title,omitempty"`    // 首条用户话语截断；历史列表的主显示
	Keywords  string            `json:"keywords,omitempty"` // 首轮分析拆出的检索词（空格分隔）
	Slots     map[string]string `json:"slots"`
	Turns     []Turn            `json:"turns"`
	Stage     string            `json:"stage"`
	Artifacts []Artifact        `json:"artifacts"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Round     int               `json:"round"` // how many tasks this session has completed
	// Effective: 计划被确认且任务成功启动（launch 即置位、永久）——检索与
	// 复用的对象。Discarded: 意图分叉时源头未有效则淘汰（不进默认列表）。
	Effective bool `json:"effective"`
	Discarded bool `json:"discarded"`
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
	// 30 天：会话历史的价值在回溯与复用，一天太短。
	TTLDays = 30
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
	if err := migrateSessions(db); err != nil {
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
	row := s.db.QueryRow(`SELECT intent, sub, title, keywords, slots, turns, stage, artifacts, round, effective, discarded, created_at, updated_at
		FROM ai_sessions WHERE id = ?`, id)
	var sess Session
	var slotsJSON, turnsJSON, artJSON, created, updated string
	if err := row.Scan(&sess.Intent, &sess.Sub, &sess.Title, &sess.Keywords, &slotsJSON, &turnsJSON, &sess.Stage,
		&artJSON, &sess.Round, &sess.Effective, &sess.Discarded, &created, &updated); err != nil {
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
	// 已删除/淘汰的会话对所有按 id 的路径视为不存在（恢复、克隆、取用、续聊）。
	if sess.Discarded {
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
		(id, intent, sub, title, keywords, slots, turns, stage, artifacts, round, effective, discarded, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET intent=?, sub=?, title=?, keywords=?, slots=?, turns=?, stage=?, artifacts=?, round=?, effective=?, discarded=?, updated_at=?`,
		sess.ID, sess.Intent, sess.Sub, sess.Title, sess.Keywords, string(slots), string(turns), sess.Stage,
		string(arts), sess.Round, sess.Effective, sess.Discarded, sess.CreatedAt.Format(time.RFC3339), sess.UpdatedAt.Format(time.RFC3339),
		sess.Intent, sess.Sub, sess.Title, sess.Keywords, string(slots), string(turns), sess.Stage,
		string(arts), sess.Round, sess.Effective, sess.Discarded, sess.UpdatedAt.Format(time.RFC3339))
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
	if err := s.save(next); err != nil {
		return nil, err
	}
	// 淘汰规则：源头会话从未有效（没跑成过任务）→ 分叉即淘汰；已有效的
	// 源头保留（它是检索与复用的对象）。
	if !sess.Effective {
		sess.Discarded = true
		if err := s.save(sess); err != nil {
			return next, err
		}
	}
	return next, nil
}

// SetMeta stores the display title and retrieval keywords for the history list.
func (s *SessionStore) SetMeta(sess *Session, title, keywords string) error {
	sess.Title = title
	sess.Keywords = keywords
	return s.save(sess)
}

// MarkEffective permanently flags the session as "produced an executable
// task". Set at confirm(execute=true) once the job id exists; never cleared —
// a later job failure is an environment problem, not a plan problem.
func (s *SessionStore) MarkEffective(sess *Session) error {
	sess.Effective = true
	return s.save(sess)
}

// Delete soft-deletes sessions by id (discarded=1). List and every by-id
// lookup treat them as gone; rows stay on disk until the TTL purge so an
// accidental delete stays forensically recoverable. Returns how many
// previously-live sessions were actually deleted.
func (s *SessionStore) Delete(ids ...string) (int, error) {
	deleted := 0
	for _, id := range ids {
		if id == "" {
			continue
		}
		res, err := s.db.Exec(`UPDATE ai_sessions SET discarded = 1 WHERE id = ? AND discarded = 0`, id)
		if err != nil {
			return deleted, err
		}
		if n, err := res.RowsAffected(); err == nil {
			deleted += int(n)
		}
	}
	return deleted, nil
}

// SessionSummary is the history-list projection of a session.
type SessionSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Intent    string    `json:"intent"`
	Sub       string    `json:"sub,omitempty"`
	Stage     string    `json:"stage"`
	Keywords  string    `json:"keywords,omitempty"`
	Effective bool      `json:"effective"`
	Round     int       `json:"round"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListSessions returns non-discarded sessions, newest first, offset pages
// through the result (history list loads batch by batch). q is a
// space-separated keyword query: every token must match (case-insensitive)
// the session's title, keywords, intent, or sub — deterministic keyword
// retrieval, no vector search.
func (s *SessionStore) ListSessions(limit, offset int, q string) ([]SessionSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	where := `WHERE discarded = 0`
	args := []any{}
	for _, tok := range strings.Fields(q) {
		where += ` AND (title || ' ' || ifnull(keywords,'') || ' ' || intent || ' ' || ifnull(sub,'')) LIKE ?`
		args = append(args, "%"+strings.ToLower(tok)+"%")
	}
	rows, err := s.db.Query(`SELECT id, intent, sub, title, keywords, stage, round, effective, updated_at
		FROM ai_sessions `+where+` ORDER BY updated_at DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionSummary
	for rows.Next() {
		var it SessionSummary
		var updated string
		if err := rows.Scan(&it.ID, &it.Intent, &it.Sub, &it.Title, &it.Keywords, &it.Stage, &it.Round, &it.Effective, &updated); err != nil {
			return nil, err
		}
		it.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, it)
	}
	return out, rows.Err()
}

// migrateSessions adds columns introduced after the first release. Old rows
// read back with zero-valued new fields.
func migrateSessions(db *sql.DB) error {
	cols := map[string]bool{}
	rows, err := db.Query(`PRAGMA table_info(ai_sessions)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range []struct{ name, decl string }{
		{"title", `TEXT NOT NULL DEFAULT ''`},
		{"keywords", `TEXT NOT NULL DEFAULT ''`},
		{"effective", `INTEGER NOT NULL DEFAULT 0`},
		{"discarded", `INTEGER NOT NULL DEFAULT 0`},
	} {
		if !cols[c.name] {
			if _, err := db.Exec(`ALTER TABLE ai_sessions ADD COLUMN ` + c.name + ` ` + c.decl); err != nil {
				return fmt.Errorf("migrate ai_sessions add %s: %w", c.name, err)
			}
		}
	}
	return nil
}
