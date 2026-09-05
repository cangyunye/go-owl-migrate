package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/agent"
)

func main() {
	for {
		ft, payload, err := agent.ReadFrame(os.Stdin)
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintln(os.Stderr, err)
			return
		}
		if ft != agent.FrameRequest {
			continue
		}
		req, _, err := agent.DecodeRequestPayload(payload)
		if err != nil {
			continue
		}
		switch req.Op {
		case "CONNECT", "PING", "BEGIN", "COMMIT", "ROLLBACK", "CLOSE":
			writeResp(os.Stdout, req, agent.ControlResponse{ID: req.ID, OK: true})
		case "QUERY":
			writeResp(os.Stdout, req, agent.ControlResponse{ID: req.ID, OK: true, Cols: []agent.ColMeta{{Name: "X", Type: "VARCHAR"}}})
			if strings.Contains(req.SQL, "__MIDSTREAM_FAIL__") {
				// 中途失败剧本：一列、一行，然后 END(ok:false) 而非干净收尾
				writeRow(os.Stdout, req, []any{"hello"})
				writeEndErr(os.Stdout, req, "boom")
			} else {
				writeRow(os.Stdout, req, []any{"hello"})
				writeEnd(os.Stdout, req, 1)
			}
		case "EXEC":
			writeResp(os.Stdout, req, agent.ControlResponse{ID: req.ID, OK: true, Affected: 3})
		default:
			writeResp(os.Stdout, req, agent.ControlResponse{ID: req.ID, OK: false, Error: "unknown op"})
		}
	}
}

func writeResp(w io.Writer, req agent.ControlRequest, resp agent.ControlResponse) {
	// 回填 conn-id：客户端 readLoop 按 Conn 路由响应到对应 session。
	resp.Conn = req.Conn
	b, _ := json.Marshal(resp)
	agent.WriteFrame(w, agent.FrameResponse, b)
}

func writeRow(w io.Writer, req agent.ControlRequest, row []any) {
	agent.WriteFrame(w, agent.FrameRowBatch, agent.EncodeRowBatch(req.Conn, req.ID, row))
}

func writeEnd(w io.Writer, req agent.ControlRequest, rows int64) {
	b, _ := json.Marshal(agent.ControlResponse{ID: req.ID, Conn: req.Conn, OK: true, Rows: rows})
	agent.WriteFrame(w, agent.FrameEnd, b)
}

func writeEndErr(w io.Writer, req agent.ControlRequest, msg string) {
	b, _ := json.Marshal(agent.ControlResponse{ID: req.ID, Conn: req.Conn, OK: false, Error: msg})
	agent.WriteFrame(w, agent.FrameEnd, b)
}
