package serve

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func newTestWSServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := service.NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	srv := NewServer(Config{Store: store})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func wsConnect(t *testing.T, ts *httptest.Server, jobID string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/jobs/" + jobID + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial(%s): %v", wsURL, err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

func readWSMessage(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("conn.Read: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("json.Unmarshal: %v (data: %s)", err, string(data))
	}
	return msg
}

func TestWebSocket_CatchUpReplay(t *testing.T) {
	srv, ts := newTestWSServer(t)
	srv.store.CreateJob("job-ws", "migrate", "{}")
	srv.store.WriteEvent("job-ws", "export_start", "SCOTT", "EMP", 0, "starting")
	srv.store.WriteEvent("job-ws", "export_complete", "SCOTT", "EMP", 5000, "done")
	srv.store.WriteEvent("job-ws", "import_complete", "SCOTT", "EMP", 4998, "done")

	conn := wsConnect(t, ts, "job-ws")

	msg1 := readWSMessage(t, conn)
	if msg1["event"] != "export_start" {
		t.Errorf("msg1 event = %v, want export_start", msg1["event"])
	}
	if msg1["seq"].(float64) != 1 {
		t.Errorf("msg1 seq = %v, want 1", msg1["seq"])
	}

	msg2 := readWSMessage(t, conn)
	if msg2["event"] != "export_complete" {
		t.Errorf("msg2 event = %v, want export_complete", msg2["event"])
	}

	msg3 := readWSMessage(t, conn)
	if msg3["event"] != "import_complete" {
		t.Errorf("msg3 event = %v, want import_complete", msg3["event"])
	}
	if msg3["rows"].(float64) != 4998 {
		t.Errorf("msg3 rows = %v, want 4998", msg3["rows"])
	}
}

func TestWebSocket_StreamNewEvents(t *testing.T) {
	srv, ts := newTestWSServer(t)
	srv.store.CreateJob("job-stream", "migrate", "{}")

	conn := wsConnect(t, ts, "job-stream")

	time.Sleep(100 * time.Millisecond)

	srv.store.WriteEvent("job-stream", "export_complete", "SCOTT", "DEPT", 4, "done")

	msg := readWSMessage(t, conn)
	if msg["event"] != "export_complete" {
		t.Errorf("event = %v, want export_complete", msg["event"])
	}
	if msg["table"].(string) != "DEPT" {
		t.Errorf("table = %v, want DEPT", msg["table"])
	}
}

func TestWebSocket_MultipleClients(t *testing.T) {
	srv, ts := newTestWSServer(t)
	srv.store.CreateJob("job-multi", "migrate", "{}")

	conn1 := wsConnect(t, ts, "job-multi")
	conn2 := wsConnect(t, ts, "job-multi")

	time.Sleep(100 * time.Millisecond)

	srv.store.WriteEvent("job-multi", "export_complete", "SCOTT", "EMP", 100, "")

	msg1 := readWSMessage(t, conn1)
	msg2 := readWSMessage(t, conn2)

	if msg1["event"] != "export_complete" || msg2["event"] != "export_complete" {
		t.Errorf("both clients should receive event, got %v and %v", msg1["event"], msg2["event"])
	}
}

func TestWebSocket_NoEvents(t *testing.T) {
	_, ts := newTestWSServer(t)

	srv2, _ := newTestWSServer(t)
	_ = srv2

	conn := wsConnect(t, ts, "job-empty")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Error("expected timeout (no events), got a message")
	}
}

func TestWebSocket_TerminalOnConnect(t *testing.T) {
	srv, ts := newTestWSServer(t)
	srv.store.CreateJob("job-done", "migrate", "{}")
	srv.store.WriteEvent("job-done", "export_complete", "SCOTT", "EMP", 14, "done")
	srv.store.UpdateJobStatus("job-done", "completed")

	conn := wsConnect(t, ts, "job-done")

	// Catch-up event first, then the terminal signal.
	msg1 := readWSMessage(t, conn)
	if msg1["event"] != "export_complete" {
		t.Errorf("msg1 event = %v, want export_complete", msg1["event"])
	}
	msg2 := readWSMessage(t, conn)
	if msg2["type"] != "complete" {
		t.Errorf("terminal type = %v, want complete", msg2["type"])
	}
}

func TestWebSocket_TerminalAfterCompletion(t *testing.T) {
	srv, ts := newTestWSServer(t)
	srv.store.CreateJob("job-live", "migrate", "{}")

	conn := wsConnect(t, ts, "job-live")
	time.Sleep(100 * time.Millisecond)

	srv.store.WriteEvent("job-live", "export_complete", "SCOTT", "EMP", 14, "done")
	srv.store.UpdateJobStatus("job-live", "completed")

	msg1 := readWSMessage(t, conn)
	if msg1["event"] != "export_complete" {
		t.Errorf("msg1 event = %v, want export_complete", msg1["event"])
	}
	msg2 := readWSMessage(t, conn)
	if msg2["type"] != "complete" {
		t.Errorf("terminal type = %v, want complete", msg2["type"])
	}
}

func TestWebSocket_TerminalCancelled(t *testing.T) {
	srv, ts := newTestWSServer(t)
	srv.store.CreateJob("job-cancel", "migrate", "{}")
	srv.store.UpdateJobStatus("job-cancel", "cancelled")

	conn := wsConnect(t, ts, "job-cancel")
	msg := readWSMessage(t, conn)
	if msg["type"] != "cancelled" {
		t.Errorf("terminal type = %v, want cancelled", msg["type"])
	}
}
