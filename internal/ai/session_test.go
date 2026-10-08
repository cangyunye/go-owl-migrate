package ai

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestSessions(t *testing.T) *SessionStore {
	t.Helper()
	s, err := OpenSessions(t.TempDir())
	if err != nil {
		t.Fatalf("OpenSessions: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSessionCreateGetRoundTrip(t *testing.T) {
	s := openTestSessions(t)
	sess, err := s.Create("export-data", "format:csv")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.MergeSlots(sess, map[string]string{
		"source_type": "mysql", "source_dsn": "tcp(127.0.0.1:3306)/owl_demo", "format": "csv",
	}); err != nil {
		t.Fatalf("MergeSlots: %v", err)
	}
	if err := s.AddTurn(sess, "user", "导出 users 表"); err != nil {
		t.Fatalf("AddTurn: %v", err)
	}

	got, ok := s.Get(sess.ID)
	if !ok {
		t.Fatal("session lost after save")
	}
	if got.Intent != "export-data" || got.Slots["source_type"] != "mysql" || len(got.Turns) != 1 {
		t.Errorf("round trip = %+v", got)
	}
}

func TestSessionMergeSkipsEmptyValues(t *testing.T) {
	s := openTestSessions(t)
	sess, _ := s.Create("export-data", "")
	s.MergeSlots(sess, map[string]string{"schema": "owl_demo", "format": ""})
	got, _ := s.Get(sess.ID)
	if got.Slots["schema"] != "owl_demo" {
		t.Errorf("schema slot lost: %+v", got.Slots)
	}
	if _, exists := got.Slots["format"]; exists {
		t.Errorf("empty value must not create slot: %+v", got.Slots)
	}
}

func TestSessionTurnWindowBounded(t *testing.T) {
	s := openTestSessions(t)
	sess, _ := s.Create("export-data", "")
	for i := 0; i < keepTurns+5; i++ {
		s.AddTurn(sess, "user", "消息")
	}
	got, _ := s.Get(sess.ID)
	if len(got.Turns) != keepTurns {
		t.Errorf("turns = %d, want %d", len(got.Turns), keepTurns)
	}
}

func TestSessionTTLExpiry(t *testing.T) {
	s := openTestSessions(t)
	sess, _ := s.Create("export-data", "")
	s.AddTurn(sess, "user", "hi")
	// 人为把 updated_at 拨到 TTL 之外
	expired := time.Now().AddDate(0, 0, -(TTLDays + 1)).Format(time.RFC3339)
	s.db.Exec(`UPDATE ai_sessions SET updated_at = ? WHERE id = ?`, expired, sess.ID)

	if _, ok := s.Get(sess.ID); ok {
		t.Fatal("expired session must not be returned")
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM ai_sessions`).Scan(&n)
	if n != 0 {
		t.Errorf("expired row not purged: %d", n)
	}
}

func TestSessionCloneForRound(t *testing.T) {
	s := openTestSessions(t)
	sess, _ := s.Create("export-data", "")
	s.MergeSlots(sess, map[string]string{"schema": "owl_demo", "tables": "owl_demo.users", "format": "csv"})
	s.SetStage(sess, StageDone)
	s.AddArtifact(sess, Artifact{Kind: "plan", ID: "p1"})

	next, err := s.CloneForRound(sess, "export-data", "format:xlsx")
	if err != nil {
		t.Fatalf("CloneForRound: %v", err)
	}
	got, _ := s.Get(next.ID)
	if got.ID == sess.ID {
		t.Fatal("clone must be a new session")
	}
	if got.Slots["schema"] != "owl_demo" || got.Slots["tables"] != "owl_demo.users" {
		t.Errorf("slots not inherited: %+v", got.Slots)
	}
	// 全量继承：format 也带过来，新一轮 utterance 给新值时由 MergeSlots 覆盖
	if got.Slots["format"] != "csv" {
		t.Errorf("format should be inherited: %+v", got.Slots)
	}
	if err := s.MergeSlots(got, map[string]string{"format": "xlsx"}); err != nil {
		t.Fatalf("MergeSlots: %v", err)
	}
	if got.Slots["format"] != "xlsx" {
		t.Errorf("new value must override: %+v", got.Slots)
	}
	if len(got.Artifacts) != 0 {
		t.Errorf("clone should start with no artifacts: %+v", got)
	}
	if got.Round != 1 {
		t.Errorf("round = %d, want 1", got.Round)
	}
	// 原会话的既有事实不动（stage/artifacts 保持；分叉只会追加淘汰标记，
	// 而淘汰后的会话按 id 读取视为不存在）
	var origStage, origArts string
	if err := s.db.QueryRow(`SELECT stage, artifacts FROM ai_sessions WHERE id = ?`, sess.ID).Scan(&origStage, &origArts); err != nil {
		t.Fatalf("origin row missing: %v", err)
	}
	if origStage != StageDone || !strings.Contains(origArts, `"p1"`) {
		t.Errorf("original mutated: stage=%q artifacts=%s", origStage, origArts)
	}
}

// ── Step 1 红测试：元数据列 / 列表检索 / 有效与淘汰 / 迁移 ──

func TestSetMetaTitleKeywords(t *testing.T) {
	s := openTestSessions(t)
	sess, _ := s.Create("export-data", "format:csv")
	if err := s.SetMeta(sess, "导出 oracle scott 的 emp 为 csv", "export oracle scott-xe csv"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	got, ok := s.Get(sess.ID)
	if !ok {
		t.Fatal("session lost")
	}
	if got.Title != "导出 oracle scott 的 emp 为 csv" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Keywords != "export oracle scott-xe csv" {
		t.Errorf("keywords = %q", got.Keywords)
	}
}

func TestListSessionsOrderLimit(t *testing.T) {
	s := openTestSessions(t)
	ids := make([]string, 3)
	for i := 0; i < 3; i++ {
		sess, _ := s.Create("export-data", "")
		ids[i] = sess.ID
		s.SetMeta(sess, fmt.Sprintf("会话%d", i), "")
	}
	// 手工拨乱 updated_at 造成确定顺序
	for i, id := range ids {
		ts := time.Now().Add(time.Duration(i) * time.Hour).Format(time.RFC3339)
		s.db.Exec(`UPDATE ai_sessions SET updated_at = ? WHERE id = ?`, ts, id)
	}
	list, err := s.ListSessions(2, 0, "")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2 (limit)", len(list))
	}
	if list[0].Title != "会话2" || list[1].Title != "会话1" {
		t.Errorf("order wrong: %q, %q", list[0].Title, list[1].Title)
	}
	// offset 翻页：跳过前 2 条后只剩最早一条
	list, err = s.ListSessions(2, 2, "")
	if err != nil {
		t.Fatalf("ListSessions offset: %v", err)
	}
	if len(list) != 1 || list[0].Title != "会话0" {
		t.Errorf("offset page wrong: %+v", list)
	}
}

func TestListSessionsKeywordMatch(t *testing.T) {
	s := openTestSessions(t)
	mk := func(title, keywords, intent string) {
		sess, _ := s.Create(intent, "")
		s.SetMeta(sess, title, keywords)
	}
	mk("导出 scott 的 emp", "export oracle scott-xe csv", "export-data")
	mk("mysql 到 oracle 迁移", "migrate mysql oracle", "migrate")
	mk("建 DDL", "export-ddl postgres", "export-ddl")

	list, err := s.ListSessions(10, 0, "oracle csv")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 1 || list[0].Title != "导出 scott 的 emp" {
		t.Errorf("multi-token AND match failed: %+v", list)
	}
	list, _ = s.ListSessions(10, 0, "ORACLE")
	if len(list) != 2 {
		t.Errorf("case-insensitive match failed: %d", len(list))
	}
	// intent 也是检索域
	list, _ = s.ListSessions(10, 0, "migrate")
	if len(list) != 1 || list[0].Intent != "migrate" {
		t.Errorf("intent match failed: %+v", list)
	}
	// 空查询返回全部
	list, _ = s.ListSessions(10, 0, "")
	if len(list) != 3 {
		t.Errorf("empty q = %d, want 3", len(list))
	}
}

func TestSchemaMigrationOldRows(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	old := `CREATE TABLE ai_sessions (
		id TEXT PRIMARY KEY, intent TEXT NOT NULL, sub TEXT, slots TEXT, turns TEXT,
		stage TEXT, artifacts TEXT, round INTEGER DEFAULT 0, created_at TEXT, updated_at TEXT);`
	if _, err := db.Exec(old); err != nil {
		t.Fatalf("seed old schema: %v", err)
	}
	now := time.Now().Format(time.RFC3339)
	// 真实旧行由 save() 写入：sub 恒为空串（非 NULL），slots/turns/artifacts
	// 恒为 JSON 文本——种子必须与之一致，否则测的是"NULL 扫描失败"而非迁移。
	db.Exec(`INSERT INTO ai_sessions (id, intent, sub, slots, turns, stage, artifacts, round, created_at, updated_at)
		VALUES ('s-old', 'export-data', '', '{}', '[]', 'done', '[]', 0, ?, ?)`, now, now)
	db.Close()

	s, err := OpenSessions(dir)
	if err != nil {
		t.Fatalf("OpenSessions over old schema: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	got, ok := s.Get("s-old")
	if !ok {
		t.Fatal("old row lost after migration")
	}
	if got.Title != "" || got.Keywords != "" || got.Effective || got.Discarded {
		t.Errorf("new fields must default zero on old rows: %+v", got)
	}
}

func TestEffectiveOnLaunch(t *testing.T) {
	s := openTestSessions(t)
	sess, _ := s.Create("export-data", "")
	if err := s.MarkEffective(sess); err != nil {
		t.Fatalf("MarkEffective: %v", err)
	}
	got, _ := s.Get(sess.ID)
	if !got.Effective {
		t.Error("effective flag not persisted")
	}
	// 永久：后续阶段迁移不清除
	s.SetStage(got, StageExecuting)
	got, _ = s.Get(sess.ID)
	if !got.Effective {
		t.Error("effective must survive stage transitions")
	}
	// 仅激活不标记：Create/其他操作后仍 false
	other, _ := s.Create("export-data", "")
	s.SetStage(other, StageDone)
	other2, _ := s.Get(other.ID)
	if other2.Effective {
		t.Error("activate-only session must not be effective")
	}
}

func TestCloneForRoundDiscardsIneffectiveOrigin(t *testing.T) {
	s := openTestSessions(t)
	// 源未有效：分叉后淘汰
	sess, _ := s.Create("export-data", "")
	next, err := s.CloneForRound(sess, "migrate", "")
	if err != nil {
		t.Fatalf("CloneForRound: %v", err)
	}
	// 淘汰落库为 discarded 标记（行保留至 TTL 清理），按 id 读取视为不存在
	var discarded int
	if err := s.db.QueryRow(`SELECT discarded FROM ai_sessions WHERE id = ?`, sess.ID).Scan(&discarded); err != nil || discarded != 1 {
		t.Error("ineffective origin must be discarded on fork")
	}
	if _, ok := s.Get(sess.ID); ok {
		t.Error("discarded origin must be invisible by id")
	}
	clone, _ := s.Get(next.ID)
	if clone.Discarded {
		t.Error("clone must not be discarded")
	}
	// 源已有效：分叉后保留
	sess2, _ := s.Create("export-data", "")
	s.MarkEffective(sess2)
	if _, err := s.CloneForRound(sess2, "migrate", ""); err != nil {
		t.Fatalf("CloneForRound: %v", err)
	}
	orig2, _ := s.Get(sess2.ID)
	if orig2.Discarded {
		t.Error("effective origin must be kept on fork")
	}
	// 已淘汰会话不出现在默认列表
	s.SetMeta(sess, "被淘汰的", "export")
	list, _ := s.ListSessions(10, 0, "export")
	for _, item := range list {
		if item.ID == sess.ID {
			t.Errorf("discarded session must not appear in default list: %+v", item)
		}
	}
}

func TestDeleteSessions(t *testing.T) {
	s := openTestSessions(t)
	ids := make([]string, 3)
	for i := 0; i < 3; i++ {
		sess, _ := s.Create("export-data", "")
		ids[i] = sess.ID
		s.SetMeta(sess, fmt.Sprintf("待删除会话%d", i), "export csv")
	}
	// 批量软删 2 条
	n, err := s.Delete(ids[0], ids[1], "")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted = %d, want 2（空 id 跳过）", n)
	}
	// 列表不再包含；按 id 读取视为不存在
	list, _ := s.ListSessions(10, 0, "")
	if len(list) != 1 || list[0].ID != ids[2] {
		t.Errorf("list after delete: %+v", list)
	}
	if _, ok := s.Get(ids[0]); ok {
		t.Error("deleted session must be invisible by id")
	}
	// 重复删除计 0
	if n, _ := s.Delete(ids[0]); n != 0 {
		t.Errorf("re-delete = %d, want 0", n)
	}
	// 行仍在磁盘（软删，TTL 清理前可审计）
	var cnt int
	if err := s.db.QueryRow(`SELECT count(*) FROM ai_sessions WHERE id IN (?,?)`, ids[0], ids[1]).Scan(&cnt); err != nil || cnt != 2 {
		t.Errorf("soft-deleted rows should remain on disk: cnt=%d err=%v", cnt, err)
	}
}

func TestSessionTTL30DaysBoundary(t *testing.T) {
	s := openTestSessions(t)
	sess, _ := s.Create("export-data", "")
	within := time.Now().AddDate(0, 0, -(TTLDays - 1)).Format(time.RFC3339)
	s.db.Exec(`UPDATE ai_sessions SET updated_at = ? WHERE id = ?`, within, sess.ID)
	if _, ok := s.Get(sess.ID); !ok {
		t.Fatalf("session within TTL must survive")
	}
	beyond := time.Now().AddDate(0, 0, -(TTLDays + 1)).Format(time.RFC3339)
	s.db.Exec(`UPDATE ai_sessions SET updated_at = ? WHERE id = ?`, beyond, sess.ID)
	if _, ok := s.Get(sess.ID); ok {
		t.Fatal("session beyond TTL must expire")
	}
}
