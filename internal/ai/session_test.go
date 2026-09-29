package ai

import (
	"testing"
	"time"
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
	// 原会话不动
	orig, _ := s.Get(sess.ID)
	if orig.Stage != StageDone || len(orig.Artifacts) != 1 {
		t.Errorf("original mutated: %+v", orig)
	}
}
