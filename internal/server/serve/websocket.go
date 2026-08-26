package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"nhooyr.io/websocket"

	"github.com/cangyunye/go-owl-migrate/internal/service"
)

const wsPollInterval = 500 * time.Millisecond

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.token != "" && r.URL.Query().Get("token") != s.token {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	jobID := r.PathValue("id")

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	events, err := s.store.GetEvents(jobID, 0)
	if err == nil {
		for _, ev := range events {
			msg := eventToMessage(ev)
			data, _ := json.Marshal(msg)
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				cancel()
				return
			}
			cancel()
		}
	}

	lastSeq := int64(0)
	if len(events) > 0 {
		lastSeq = events[len(events)-1].Seq
	}

	// sendTerminalIfDone writes a terminal message (complete/cancelled/error)
	// and returns true once the job has finished, so the caller can close.
	sendTerminalIfDone := func() bool {
		job, err := s.store.GetJob(jobID)
		if err != nil {
			return false
		}
		tm := terminalMessage(job.Status)
		if tm == nil {
			return false
		}
		data, _ := json.Marshal(tm)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		conn.Write(ctx, websocket.MessageText, data)
		cancel()
		return true
	}

	// Job may already be finished by the time we connect.
	if sendTerminalIfDone() {
		return
	}

	ticker := time.NewTicker(wsPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			newEvents, err := s.store.GetEvents(jobID, lastSeq)
			if err == nil {
				for _, ev := range newEvents {
					msg := eventToMessage(ev)
					data, _ := json.Marshal(msg)
					ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
					if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
						cancel()
						return
					}
					cancel()
					lastSeq = ev.Seq
				}
			}
			if sendTerminalIfDone() {
				return
			}
		}
	}
}

// terminalMessage maps a terminal job status to the WebSocket message the
// frontend uses to stop streaming and reveal post-run UI (e.g. SQL download).
// Returns nil for non-terminal statuses (running/cancelling).
func terminalMessage(status string) map[string]any {
	switch status {
	case "completed":
		return map[string]any{"type": "complete", "status": status}
	case "cancelled":
		return map[string]any{"type": "cancelled", "status": status}
	case "failed", "interrupted":
		return map[string]any{"type": "error", "status": status}
	default:
		return nil
	}
}

func eventToMessage(ev service.ProgressEvent) map[string]any {
	return map[string]any{
		"type":   "progress",
		"seq":    ev.Seq,
		"event":  ev.EventType,
		"schema": ev.Schema,
		"table":  ev.TableName,
		"rows":   ev.Rows,
		"message": ev.Message,
	}
}
