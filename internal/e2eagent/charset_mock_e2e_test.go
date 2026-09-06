//go:build e2e

package e2eagent

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"

	agent "github.com/cangyunye/owljdbc"
)

// TestE2E_CharsetMockProbe 驱动 fake_agent 的字符集探针剧本：它假定一个
// OB-Oracle UTF8 租户 / MySQL utf8mb4 服务端，按 dbconn.ProbeServerEncoding
// 发出的探针 SQL 形状返回固定值。该测试锁住 mock 契约与帧级往返（无需真实
// 数据库与 JVM）；探针在真实 UTF8 租户上的确认见 charset_e2e_test.go。
func TestE2E_CharsetMockProbe(t *testing.T) {
	cs := csStartFakeAgent(t)

	cases := []struct {
		name     string
		probeSQL string
		want     string
	}{
		{"oracle family hits nls mock", "SELECT VALUE FROM NLS_DATABASE_PARAMETERS WHERE PARAMETER = 'NLS_CHARACTERSET'", "AL32UTF8"},
		{"mysql family hits charset mock", "SELECT @@character_set_server", "utf8mb4"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := csQuery(t, cs, uint32(i+1), tc.probeSQL)
			if got != tc.want {
				t.Fatalf("mock probe row = %q, want %q", got, tc.want)
			}
		})
	}
}

type csFakeAgent struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex
	nextID uint32
}

func csStartFakeAgent(t *testing.T) *csFakeAgent {
	t.Helper()
	cmd := exec.Command("go", "run", "./testdata/fake_agent")
	cmd.Dir = "../../owljdbc"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, stderr)
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
	})
	// go run 需要编译时间；CONNECT 探活到就绪。
	r := bufio.NewReader(stdout)
	deadline := time.Now().Add(60 * time.Second)
	for {
		if err := csWriteRequest(rStdin{stdin}, 0, "PING", ""); err != nil {
			t.Fatal(err)
		}
		ft, payload, err := agent.ReadFrame(r)
		if err == nil && ft == agent.FrameResponse {
			var resp agent.ControlResponse
			if json.Unmarshal(payload, &resp) == nil && resp.OK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("fake agent did not become ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	return &csFakeAgent{cmd: cmd, stdin: stdin, stdout: r}
}

// rStdin 适配 csWriteRequest 的 io.Writer 形参。
type rStdin struct{ io.WriteCloser }

func (r rStdin) Write(p []byte) (int, error) { return r.WriteCloser.Write(p) }

func csWriteRequest(w io.Writer, id uint32, op, sqlText string) error {
	// REQUEST payload 与 internal/agent.encodeRequest 同构：4B LE 头长 + JSON
	// 头 + 类型化参数（探针查询无参数）。
	req := agent.ControlRequest{ID: id, Conn: 1, Op: op, SQL: sqlText}
	header, err := json.Marshal(req)
	if err != nil {
		return err
	}
	out := []byte{
		byte(len(header)), byte(len(header) >> 8),
		byte(len(header) >> 16), byte(len(header) >> 24),
	}
	out = append(out, header...)
	return agent.WriteFrame(w, agent.FrameRequest, out)
}

// csQuery 发送探针 QUERY 并收集到 END 前的第一行值。
func csQuery(t *testing.T, cs *csFakeAgent, id uint32, probeSQL string) string {
	t.Helper()
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if err := csWriteRequest(cs.stdin, id, "QUERY", probeSQL); err != nil {
		t.Fatalf("write request: %v", err)
	}
	var row string
	for {
		ft, payload, err := agent.ReadFrame(cs.stdout)
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		switch ft {
		case agent.FrameResponse:
			var resp agent.ControlResponse
			if err := json.Unmarshal(payload, &resp); err != nil {
				t.Fatalf("bad response: %v", err)
			}
			if !resp.OK {
				t.Fatalf("probe rejected: %s", resp.Error)
			}
		case agent.FrameRowBatch:
			_, batchID, rows, err := agent.DecodeRowBatch(payload)
			if err != nil {
				t.Fatalf("decode row batch: %v", err)
			}
			_ = batchID
			if len(rows) > 0 && len(rows[0]) > 0 {
				if s, ok := rows[0][0].(string); ok {
					row = s
				}
			}
		case agent.FrameEnd:
			var end agent.ControlResponse
			if err := json.Unmarshal(payload, &end); err != nil {
				t.Fatalf("bad end: %v", err)
			}
			if !end.OK {
				t.Fatalf("probe ended with error: %s", end.Error)
			}
			return row
		}
	}
}
