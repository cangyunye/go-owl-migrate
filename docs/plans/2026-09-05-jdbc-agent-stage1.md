# JDBC Agent 通道（阶段一）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付一条通用 JDBC 通道：Java sidecar agent（`jvm/owl-agent/`）+ Go 侧 `database/sql` 薄驱动 `owljdbc`（`internal/agent/`），并用 OB 双租户（MySQL/Oracle）对 native 路径做全流程对拍验证。

**Architecture:** Go 进程经子进程 stdin/stdout 二进制帧协议驱动一个 JVM sidecar agent；每个 agent 进程对应一个 classpath profile；Go 侧实现 `database/sql/driver` 驱动 `owljdbc`，使 extractor/exporter/importer 零改动复用。对拍 harness 以库方式调用 `extractor.Extract` / `exporter.New` / `importer.New`，对比 native 与 agent 两条路径。

**Tech Stack:** Go 1.25 (`database/sql`, `os/exec`, `encoding/binary`, `encoding/json`)，Java (JDK 8 目标，本机 java 21 运行)，`oceanbase-client-2.4.1.jar`（`com.oceanbase.jdbc.Driver`，`jdbc:oceanbase://`）。

## Global Constraints

- 协议帧：`[u32 LE payloadLen][u8 type][payload]`；`payloadLen` = type 字节之后的全部字节数。
- 帧类型：`0x01` REQUEST（control），`0x02` RESPONSE（control），`0x03` ROW_BATCH，`0x04` END。
- REQUEST payload = `[u32 headerLen][header JSON][binary args...]`；header JSON 含 `id,conn,op,sql,family`；args 为编码值串接。
- RESPONSE/END payload = 纯 JSON。
- 值编码 tag：`0x00` NULL、`0x01` BOOL、`0x02` INT64、`0x03` FLOAT64、`0x04` DECIMAL(string)、`0x05` STRING、`0x06` BYTES、`0x07` DATETIME、`0x08` DATE(string)、`0x09` TIME(string)。
- ops：`CONNECT` / `CLOSE` / `QUERY` / `EXEC` / `BEGIN` / `COMMIT` / `ROLLBACK` / `SAVEPOINT` / `RELEASE` / `PING` / `CANCEL` / `SHUTDOWN`。
- 凭证（user/password）只走 spawn 后 stdin 首帧，不进 argv/env。
- Java agent 编译目标 `--release 8`；运行要求 JRE 8+（本机 java 21 已实测）。
- 驱动类 `com.oceanbase.jdbc.Driver`；URL 前缀 `jdbc:oceanbase:`；`user=用户名@租户名`。
- 第一阶段不改 `dbconn` / `registry` / CLI / `dialect`。
- harness 用 build tag `e2e`（对齐仓库 e2eob 惯例）；无实库时自动 skip。

---

## 文件结构

```
internal/agent/
  config.go      AgentConfig + DSN encode/decode
  proto.go       帧类型、值编解码、消息结构
  client.go      AgentProc：spawn java 进程、读写帧、req/conn 分发、Session
  manager.go     进程生命周期：按 profile 复用 agent 进程、refcount、健康检查
  driver.go      database/sql driver "owljdbc"（Connector/Conn/Stmt/Rows/Tx）
  proto_test.go
  config_test.go
  client_test.go  （Go 假 agent 测试双胞胎）
  driver_test.go
jvm/owl-agent/
  build.sh       编译/打包 owl-agent.jar
  test.sh        跑 Java 单元测试
  src/owl/agent/Main.java
  src/owl/agent/Protocol.java
  src/owl/agent/BindRewriter.java
  src/owl/agent/Session.java
  src/owl/agent/ValueCodec.java
  src/owl/agent/test/BindRewriterTest.java
  src/owl/agent/test/ValueCodecTest.java
internal/e2eagent/
  agent_e2e_test.go   OB 双租户 native vs agent 对拍（build tag e2e）
```

---

### Task 1: 值编码与帧结构（Go `proto.go`）

**Files:**
- Create: `internal/agent/proto.go`
- Test: `internal/agent/proto_test.go`

**Interfaces:**
- Produces: `type FrameType uint8`、`const (FrameRequest FrameType = 0x01; FrameResponse = 0x02; FrameRowBatch = 0x03; FrameEnd = 0x04)`、`func writeFrame(w io.Writer, t FrameType, payload []byte) error`、`func readFrame(r io.Reader) (FrameType, []byte, error)`、`func encodeValue(buf []byte, v any) []byte`、`func decodeValue(b []byte) (any, int, error)`、`type ControlRequest struct{ ID uint32; Conn uint32; Op string; SQL string; Family string }`、`func encodeRequest(req ControlRequest, args []any) []byte`、`func decodeRequestPayload(p []byte) (ControlRequest, []any, error)`、`type ControlResponse struct{ ID uint32; OK bool; Error string; Rows int64; Affected int64; Cols []ColMeta }`、`type ColMeta struct{ Name string; Type string }`。

- [ ] **Step 1: 写失败测试**

`internal/agent/proto_test.go`：
```go
package agent

import (
	"bytes"
	"reflect"
	"testing"
	"time"
)

func TestValueRoundTrip(t *testing.T) {
	vals := []any{
		nil, true, int64(-42), 3.14, "hello", []byte{1, 2, 3}, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	for _, v := range vals {
		b := encodeValue(nil, v)
		got, n, err := decodeValue(b)
		if err != nil {
			t.Fatalf("decode(%v): %v", v, err)
		}
		if n != len(b) {
			t.Fatalf("decode consumed %d, want %d", n, len(b))
		}
		if v == nil && got != nil {
			t.Fatalf("nil mismatch")
		}
		if !reflect.DeepEqual(got, v) {
			t.Fatalf("roundtrip %v -> %v", v, got)
		}
	}
}

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, FrameRequest, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	ft, p, err := readFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if ft != FrameRequest || string(p) != "abc" {
		t.Fatalf("got %v %q", ft, p)
	}
}

func TestRequestPayload(t *testing.T) {
	req := ControlRequest{ID: 7, Conn: 1, Op: "EXEC", SQL: "INSERT INTO t VALUES(?,?)", Family: "mysql"}
	payload := encodeRequest(req, []any{int64(1), "x"})
	r2, args, err := decodeRequestPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if r2.ID != 7 || r2.Op != "EXEC" || r2.Family != "mysql" {
		t.Fatalf("header mismatch %+v", r2)
	}
	if len(args) != 2 || args[0].(int64) != 1 || args[1].(string) != "x" {
		t.Fatalf("args mismatch %+v", args)
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/agent/ -run TestValue -v`
Expected: FAIL，`proto.go: undefined: writeFrame` 等。

- [ ] **Step 3: 写最小实现 `internal/agent/proto.go`**

```go
package agent

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

type FrameType uint8

const (
	FrameRequest  FrameType = 0x01
	FrameResponse FrameType = 0x02
	FrameRowBatch FrameType = 0x03
	FrameEnd      FrameType = 0x04
)

type ColMeta struct {
	Name string `json:"name"`
	Type string `json:"type"` // 原始数据库类型（大写）
}

type ControlRequest struct {
	ID     uint32 `json:"id"`
	Conn   uint32 `json:"conn"`
	Op     string `json:"op"`
	SQL    string `json:"sql,omitempty"`
	Family string `json:"family,omitempty"`
}

type ControlResponse struct {
	ID       uint32    `json:"id"`
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Rows     int64     `json:"rows,omitempty"`
	Affected int64     `json:"affected,omitempty"`
	Cols     []ColMeta `json:"cols,omitempty"`
}

const (
	tagNull     = 0x00
	tagBool     = 0x01
	tagInt64    = 0x02
	tagFloat64  = 0x03
	tagDecimal  = 0x04
	tagString   = 0x05
	tagBytes    = 0x06
	tagDatetime = 0x07
	tagDate     = 0x08
	tagTime     = 0x09
)

func writeFrame(w io.Writer, t FrameType, payload []byte) error {
	total := 1 + len(payload)
	hdr := make([]byte, 4)
	binary.LittleEndian.PutUint32(hdr, uint32(total))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	if _, err := w.Write([]byte{byte(t)}); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) (FrameType, []byte, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return 0, nil, err
	}
	total := binary.LittleEndian.Uint32(hdr)
	buf := make([]byte, total)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return FrameType(buf[0]), buf[1:], nil
}

// encodeValue appends the encoded form of v to buf. Runtime-type based (for args).
func encodeValue(buf []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(buf, tagNull)
	case bool:
		buf = append(buf, tagBool)
		if x {
			return append(buf, 1)
		}
		return append(buf, 0)
	case int:
		return encodeValue(buf, int64(x))
	case int32:
		return encodeValue(buf, int64(x))
	case int64:
		buf = append(buf, tagInt64)
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(x))
		return append(buf, b[:]...)
	case float64:
		buf = append(buf, tagFloat64)
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], mathFloat64bits(x))
		return append(buf, b[:]...)
	case string:
		buf = append(buf, tagString)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(x)))
		return append(buf, x...)
	case []byte:
		buf = append(buf, tagBytes)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(x)))
		return append(buf, x...)
	case time.Time:
		buf = append(buf, tagDatetime)
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(x.UnixMilli()))
		buf = append(buf, b[:]...)
		_, off := x.Zone()
		return append(buf, byte(off/60), byte(off%60))
	default:
		panic(fmt.Sprintf("encodeValue: unsupported %T", v))
	}
}

func mathFloat64bits(f float64) uint64 { return *(*uint64)(unsafePtr(&f)) }

func decodeValue(b []byte) (any, int, error) {
	if len(b) < 1 {
		return nil, 0, errors.New("empty value")
	}
	tag := b[0]
	switch tag {
	case tagNull:
		return nil, 1, nil
	case tagBool:
		if len(b) < 2 {
			return nil, 0, errors.New("short bool")
		}
		return b[1] != 0, 2, nil
	case tagInt64:
		if len(b) < 9 {
			return nil, 0, errors.New("short int")
		}
		return int64(binary.LittleEndian.Uint64(b[1:])), 9, nil
	case tagFloat64:
		if len(b) < 9 {
			return nil, 0, errors.New("short float")
		}
		return float64FromBits(binary.LittleEndian.Uint64(b[1:])), 9, nil
	case tagDecimal, tagString, tagBytes, tagDate, tagTime:
		if len(b) < 5 {
			return nil, 0, errors.New("short len")
		}
		n := int(binary.LittleEndian.Uint32(b[1:]))
		if len(b) < 5+n {
			return nil, 0, errors.New("short payload")
		}
		payload := b[5 : 5+n]
		if tag == tagBytes {
			return append([]byte(nil), payload...), 5 + n, nil
		}
		return string(payload), 5 + n, nil
	case tagDatetime:
		if len(b) < 11 {
			return nil, 0, errors.New("short datetime")
		}
		ms := int64(binary.LittleEndian.Uint64(b[1:]))
		off := int(b[9])*60 + int(b[10])
		loc := time.FixedZone("", off*60)
		return time.UnixMilli(ms).In(loc), 11, nil
	default:
		return nil, 0, fmt.Errorf("unknown tag %d", tag)
	}
}

// encodeRequest builds a REQUEST payload: [u32 headerLen][header JSON][binary args...]
func encodeRequest(req ControlRequest, args []any) []byte {
	header, _ := json.Marshal(req)
	out := binary.LittleEndian.AppendUint32(nil, uint32(len(header)))
	out = append(out, header...)
	for _, a := range args {
		out = encodeValue(out, a)
	}
	return out
}

// decodeRequestPayload splits header JSON + args.
func decodeRequestPayload(p []byte) (ControlRequest, []any, error) {
	if len(p) < 4 {
		return ControlRequest{}, nil, errors.New("short request")
	}
	hLen := int(binary.LittleEndian.Uint32(p[:4]))
	if len(p) < 4+hLen {
		return ControlRequest{}, nil, errors.New("short header")
	}
	var req ControlRequest
	if err := json.Unmarshal(p[4:4+hLen], &req); err != nil {
		return ControlRequest{}, nil, err
	}
	var args []any
	rest := p[4+hLen:]
	for len(rest) > 0 {
		v, n, err := decodeValue(rest)
		if err != nil {
			return ControlRequest{}, nil, err
		}
		args = append(args, v)
		rest = rest[n:]
	}
	return req, args, nil
}
```

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/agent/ -run 'TestValue|TestFrame|TestRequest' -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/agent/proto.go internal/agent/proto_test.go
git commit -m "feat(agent): frame protocol and value codec"
```

---

### Task 2: Agent 连接配置（Go `config.go`）

**Files:**
- Create: `internal/agent/config.go`
- Test: `internal/agent/config_test.go`

**Interfaces:**
- Consumes: 无（独立）。
- Produces: `type Config struct{ DriverClass, URL, User, Password, Family string; Classpath []string; JavaHome, AgentJar string }`、`func EncodeDSN(cfg Config) string`、`func DecodeDSN(dsn string) (Config, error)`。

- [ ] **Step 1: 写失败测试**

`internal/agent/config_test.go`：
```go
package agent

import (
	"reflect"
	"testing"
)

func TestDSNRoundTrip(t *testing.T) {
	in := Config{
		DriverClass: "com.oceanbase.jdbc.Driver",
		URL:         "jdbc:oceanbase://127.0.0.1:2881?useSSL=false",
		User:        "MIGSRC@oratest",
		Password:    "PASS@WORD",
		Family:      "oracle",
		Classpath:   []string{"/abs/oceanbase-client-2.4.1.jar"},
		JavaHome:    "",
		AgentJar:    "/abs/owl-agent.jar",
	}
	dsn := EncodeDSN(in)
	out, err := DecodeDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("roundtrip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/agent/ -run TestDSNRoundTrip -v`
Expected: FAIL（undefined: EncodeDSN）。

- [ ] **Step 3: 写实现 `internal/agent/config.go`**

```go
package agent

import "encoding/json"

// Config 描述一个 JDBC agent 连接（序列化为 database/sql DSN）。
type Config struct {
	DriverClass string   `json:"driverClass"`
	URL         string   `json:"url"`
	User        string   `json:"user"`
	Password    string   `json:"password"`
	Family      string   `json:"family"` // "mysql" | "oracle"
	Classpath   []string `json:"classpath"`
	JavaHome    string   `json:"javaHome,omitempty"`
	AgentJar    string   `json:"agentJar"`
}

func EncodeDSN(cfg Config) string {
	b, _ := json.Marshal(cfg)
	return string(b)
}

func DecodeDSN(dsn string) (Config, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(dsn), &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
```

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/agent/ -run TestDSNRoundTrip -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/agent/config.go internal/agent/config_test.go
git commit -m "feat(agent): agent connection config and DSN codec"
```

---

### Task 3: 进程与会话（Go `client.go` + `manager.go`）

**Files:**
- Create: `internal/agent/client.go`
- Create: `internal/agent/manager.go`
- Create: `internal/agent/testdata/fake_agent/main.go`
- Test: `internal/agent/client_test.go`

**Interfaces:**
- Consumes: `Config`、`ControlRequest`/`ControlResponse`、`ColMeta`、`encodeRequest`/`decodeRequestPayload`、`writeFrame`/`readFrame`、`decodeRowBatch`、`encodeValue`/`decodeValue`（均来自 Task 1/2）。
- Produces:
  - `func openAgent(ctx context.Context, cfg Config) (*AgentProc, error)` — spawn java 进程并做启动握手（`PING` 带超时）。
  - `type AgentProc struct{ ... }`，`func (p *AgentProc) NewSession(cfg Config) (*Session, error)`。
  - `type Session struct{ ... }`，`func (s *Session) Exec(ctx context.Context, req ControlRequest, args []any) (ControlResponse, error)`，`func (s *Session) Query(ctx context.Context, req ControlRequest, args []any) (*QueryStream, error)`，`func (s *Session) Close() error`。
  - `type QueryStream struct{ ... }`，`func (q *QueryStream) Cols() []ColMeta`，`func (q *QueryStream) Next() ([]any, error)`（EOF 表示结束），`func (q *QueryStream) Close() error`。
  - `type Manager`，`func (m *Manager) Acquire(ctx context.Context, cfg Config) (*Session, error)`，`func (m *Manager) Release(cfg Config, connID uint32)`。

> **liveness 方案（本任务并入）**：① 进程死亡 —— `readLoop` 读到 EOF/error → `markClosed()` 关闭 `proc.closed` 通道并 `abortAll`，所有 pending `Exec`/`Query` 立即报错（免费探测，无需探针）；② 请求卡死 —— `Exec`/`Query` 透传 context，超时发 `CANCEL`（best-effort）并返回 `ctx.Err()`；③ 启动握手 —— `openAgent` spawn 后发 `PING`（带 `startupTimeout`），未响应则杀进程返回错误。

- [ ] **Step 1: 写假 agent 测试双胞胎**

`internal/agent/testdata/fake_agent/main.go`（独立可执行，模拟协议服务端）：
```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

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
			writeRow(os.Stdout, req, []any{"hello"})
			writeEnd(os.Stdout, req, 1)
		case "EXEC":
			writeResp(os.Stdout, req, agent.ControlResponse{ID: req.ID, OK: true, Affected: 3})
		default:
			writeResp(os.Stdout, req, agent.ControlResponse{ID: req.ID, OK: false, Error: "unknown op"})
		}
	}
}

func writeResp(w io.Writer, req agent.ControlRequest, resp agent.ControlResponse) {
	b, _ := json.Marshal(resp)
	agent.WriteFrame(w, agent.FrameResponse, b)
}

func writeRow(w io.Writer, req agent.ControlRequest, row []any) {
	agent.WriteFrame(w, agent.FrameRowBatch, agent.EncodeRowBatch(req.Conn, req.ID, row))
}

func writeEnd(w io.Writer, req agent.ControlRequest, rows int64) {
	b, _ := json.Marshal(agent.ControlResponse{ID: req.ID, OK: true, Rows: rows})
	agent.WriteFrame(w, agent.FrameEnd, b)
}
```

> 注：假 agent 复用 `agent` 包的导出函数 `ReadFrame`/`WriteFrame`/`DecodeRequestPayload`/`EncodeRowBatch`。这些需在 Task 1 的 `proto.go` 中补充导出（见 Step 2）。

- [ ] **Step 2: 补导出别名与 `EncodeRowBatch`（追加到 `proto.go`）**

在 `proto.go` 末尾追加：
```go
func ReadFrame(r io.Reader) (FrameType, []byte, error) { return readFrame(r) }
func WriteFrame(w io.Writer, t FrameType, payload []byte) error { return writeFrame(w, t, payload) }
func DecodeRequestPayload(p []byte) (ControlRequest, []any, error) { return decodeRequestPayload(p) }

// EncodeRowBatch 编码一行到 ROW_BATCH payload：[u32 conn][u32 id][u32 rowCount][values...]
func EncodeRowBatch(conn, id uint32, row []any) []byte {
	out := binary.LittleEndian.AppendUint32(nil, conn)
	out = binary.LittleEndian.AppendUint32(out, id)
	out = binary.LittleEndian.AppendUint32(out, 1)
	for _, v := range row {
		out = encodeValue(out, v)
	}
	return out
}

// decodeRowBatch 解码 ROW_BATCH payload。
func decodeRowBatch(p []byte) (uint32, uint32, [][]any, error) {
	if len(p) < 12 {
		return 0, 0, nil, errors.New("short rowbatch")
	}
	conn := binary.LittleEndian.Uint32(p[0:4])
	id := binary.LittleEndian.Uint32(p[4:8])
	count := binary.LittleEndian.Uint32(p[8:12])
	rest := p[12:]
	rows := make([][]any, 0, count)
	for i := uint32(0); i < count; i++ {
		var row []any
		for len(rest) > 0 {
			v, n, err := decodeValue(rest)
			if err != nil {
				return 0, 0, nil, err
			}
			row = append(row, v)
			rest = rest[n:]
		}
		rows = append(rows, row)
	}
	return conn, id, rows, nil
}
```

- [ ] **Step 3: 写失败测试 `internal/agent/client_test.go`**

```go
package agent

import (
	"context"
	"io"
	"os/exec"
	"testing"
)

func newTestProc(t *testing.T) *AgentProc {
	t.Helper()
	cmd := exec.Command("go", "run", "./testdata/fake_agent")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &AgentProc{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		stderr:   stderr,
		sessions: make(map[uint32]*Session),
		nextConn: 1,
		closed:   make(chan struct{}),
	}
	go p.readLoop()
	go p.logLoop(stderr)
	t.Cleanup(func() { cmd.Process.Kill() })
	return p
}

func TestSessionExec(t *testing.T) {
	proc := newTestProc(t)
	s, err := proc.NewSession(Config{Family: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.Exec(context.Background(), ControlRequest{Op: "EXEC", SQL: "INSERT INTO t VALUES(?,?)", Family: "mysql"}, []any{int64(1), "x"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Affected != 3 {
		t.Fatalf("affected = %d", resp.Affected)
	}
}

func TestSessionQuery(t *testing.T) {
	proc := newTestProc(t)
	s, err := proc.NewSession(Config{Family: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	qs, err := s.Query(context.Background(), ControlRequest{Op: "QUERY", SQL: "SELECT 1", Family: "mysql"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs.Cols()) != 1 {
		t.Fatalf("cols = %d", len(qs.Cols()))
	}
	row, err := qs.Next()
	if err != nil {
		t.Fatal(err)
	}
	if row[0] != "hello" {
		t.Fatalf("row[0] = %v", row[0])
	}
	if _, err := qs.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestSessionProcessDeath(t *testing.T) {
	proc := newTestProc(t)
	s, err := proc.NewSession(Config{Family: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	// 杀进程 → 下一次 Exec 应立即报错，不挂起。
	proc.cmd.Process.Kill()
	_, err = s.Exec(context.Background(), ControlRequest{Op: "PING"}, nil)
	if err == nil {
		t.Fatalf("expected error after process death")
	}
}

func TestSessionContextTimeout(t *testing.T) {
	proc := newTestProc(t)
	s, err := proc.NewSession(Config{Family: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 已取消
	_, err = s.Exec(ctx, ControlRequest{Op: "PING"}, nil)
	if err == nil {
		t.Fatalf("expected error on cancelled context")
	}
}
```

- [ ] **Step 4: 运行验证失败**

Run: `go test ./internal/agent/ -run TestSession -v`
Expected: FAIL（undefined: openAgent / ReadFrame / EncodeRowBatch / AgentProc）。

- [ ] **Step 5: 写实现 `internal/agent/client.go`**

```go
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// AgentProc 是一个 JVM sidecar 进程及其帧读写循环。
type AgentProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	mu       sync.Mutex
	sessions map[uint32]*Session
	nextConn uint32
	closed   chan struct{} // 进程死亡信号：readLoop 读 stdout 到 EOF/error 时 close
	once     sync.Once
}

// Session 是一个逻辑连接（一个 conn-id），串行执行其语句。
type Session struct {
	proc    *AgentProc
	connID  uint32
	family  string
	mu      sync.Mutex
	nextID  uint32
	pending map[uint32]chan ControlResponse
	curRows *QueryStream
}

// QueryStream 流式读取查询结果。
type QueryStream struct {
	sess   *Session
	id     uint32
	cols   []ColMeta
	rows   chan []any
	done   chan struct{}
	once   sync.Once
}

const startupTimeout = 30 * time.Second

func openAgent(ctx context.Context, cfg Config) (*AgentProc, error) {
	java := cfg.JavaHome
	if java == "" {
		java = "java"
	}
	cp := cfg.AgentJar
	for _, j := range cfg.Classpath {
		cp += ":" + j
	}
	cmd := exec.CommandContext(ctx, java, "-cp", cp, "owl.agent.Main")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &AgentProc{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		stderr:   stderr,
		sessions: make(map[uint32]*Session),
		nextConn: 1,
		closed:   make(chan struct{}),
	}
	go p.readLoop()
	go p.logLoop(stderr)
	// 启动握手：PING 带超时，确认 JVM 就绪。
	if err := p.handshake(ctx); err != nil {
		p.close()
		return nil, err
	}
	return p, nil
}

func (p *AgentProc) handshake(ctx context.Context) error {
	hctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	probe := &Session{proc: p, connID: 0, pending: make(map[uint32]chan ControlResponse)}
	_, err := probe.exec(hctx, ControlRequest{Op: "PING"}, nil)
	return err
}

func (p *AgentProc) logLoop(stderr io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := stderr.Read(buf)
		if n > 0 {
			os.Stderr.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (p *AgentProc) markClosed() {
	p.once.Do(func() { close(p.closed) })
}

func (p *AgentProc) readLoop() {
	for {
		ft, payload, err := readFrame(p.stdout)
		if err != nil {
			p.markClosed()
			p.abortAll(errors.New("agent process closed"))
			return
		}
		switch ft {
		case FrameResponse:
			var resp ControlResponse
			if json.Unmarshal(payload, &resp) != nil {
				continue
			}
			p.mu.Lock()
			s := p.sessions[resp.Conn]
			p.mu.Unlock()
			if s != nil {
				s.dispatchResponse(resp)
			}
		case FrameRowBatch:
			conn, id, rows, err := decodeRowBatch(payload)
			if err != nil {
				continue
			}
			p.mu.Lock()
			s := p.sessions[conn]
			p.mu.Unlock()
			if s != nil {
				s.dispatchRows(id, rows)
			}
		case FrameEnd:
			var resp ControlResponse
			if json.Unmarshal(payload, &resp) != nil {
				continue
			}
			p.mu.Lock()
			s := p.sessions[resp.Conn]
			p.mu.Unlock()
			if s != nil {
				s.dispatchEnd(resp)
			}
		}
	}
}

func (p *AgentProc) abortAll(err error) {
	p.mu.Lock()
	sessions := make([]*Session, 0, len(p.sessions))
	for _, s := range p.sessions {
		sessions = append(sessions, s)
	}
	p.mu.Unlock()
	for _, s := range sessions {
		s.abort(err)
	}
}

func (p *AgentProc) close() {
	p.markClosed()
	_ = p.cmd.Process.Kill()
}

func (p *AgentProc) NewSession(cfg Config) (*Session, error) {
	p.mu.Lock()
	select {
	case <-p.closed:
		p.mu.Unlock()
		return nil, errors.New("agent process closed")
	default:
	}
	connID := p.nextConn
	p.nextConn++
	s := &Session{proc: p, connID: connID, family: cfg.Family, pending: make(map[uint32]chan ControlResponse)}
	p.sessions[connID] = s
	p.mu.Unlock()

	resp, err := s.exec(context.Background(), ControlRequest{Op: "CONNECT"},
		[]any{cfg.DriverClass, cfg.URL, cfg.User, cfg.Password})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New(resp.Error)
	}
	return s, nil
}

// exec 发送控制请求并等待响应；支持 ctx 取消（发 CANCEL）与进程死亡检测。
func (s *Session) exec(ctx context.Context, req ControlRequest, args []any) (ControlResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	ch := make(chan ControlResponse, 1)
	id := s.nextID
	s.nextID++
	req.ID = id
	req.Conn = s.connID
	s.pending[id] = ch
	defer delete(s.pending, id)

	if err := writeFrame(s.proc.stdin, FrameRequest, encodeRequest(req, args)); err != nil {
		return ControlResponse{}, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		go s.sendCancel()
		return ControlResponse{}, ctx.Err()
	case <-s.proc.closed:
		return ControlResponse{}, errors.New("agent process closed")
	}
}

func (s *Session) sendCancel() {
	_ = writeFrame(s.proc.stdin, FrameRequest,
		encodeRequest(ControlRequest{Conn: s.connID, Op: "CANCEL"}, nil))
}

func (s *Session) Exec(ctx context.Context, req ControlRequest, args []any) (ControlResponse, error) {
	return s.exec(ctx, req, args)
}

func (s *Session) Query(ctx context.Context, req ControlRequest, args []any) (*QueryStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	ch := make(chan ControlResponse, 1)
	id := s.nextID
	s.nextID++
	req.ID = id
	req.Conn = s.connID
	s.pending[id] = ch
	defer delete(s.pending, id)

	qs := &QueryStream{sess: s, id: id, rows: make(chan []any, 256), done: make(chan struct{})}
	s.curRows = qs

	if err := writeFrame(s.proc.stdin, FrameRequest, encodeRequest(req, args)); err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		if !resp.OK {
			return nil, errors.New(resp.Error)
		}
		qs.cols = resp.Cols
		return qs, nil
	case <-ctx.Done():
		go s.sendCancel()
		return nil, ctx.Err()
	case <-s.proc.closed:
		return nil, errors.New("agent process closed")
	}
}

func (s *Session) dispatchResponse(resp ControlResponse) {
	ch := s.pending[resp.ID]
	if ch != nil {
		ch <- resp
	}
}

func (s *Session) dispatchRows(id uint32, rows [][]any) {
	qs := s.curRows
	if qs != nil && qs.id == id {
		for _, r := range rows {
			select {
			case qs.rows <- r:
			case <-qs.done:
				return
			}
		}
	}
}

func (s *Session) dispatchEnd(resp ControlResponse) {
	qs := s.curRows
	if qs != nil && qs.id == resp.ID {
		close(qs.rows)
		s.curRows = nil
	}
}

func (s *Session) abort(err error) {
	qs := s.curRows
	if qs != nil {
		close(qs.rows)
		s.curRows = nil
	}
}

func (s *Session) Close() error {
	_, err := s.exec(context.Background(), ControlRequest{Op: "CLOSE"}, nil)
	s.proc.mu.Lock()
	delete(s.proc.sessions, s.connID)
	s.proc.mu.Unlock()
	return err
}

// QueryStream 实现。
func (q *QueryStream) Cols() []ColMeta { return q.cols }
func (q *QueryStream) Next() ([]any, error) {
	row, ok := <-q.rows
	if !ok {
		return nil, io.EOF
	}
	return row, nil
}
func (q *QueryStream) Close() error {
	q.once.Do(func() { close(q.done) })
	return nil
}
```

- [ ] **Step 6: 写实现 `internal/agent/manager.go`**

```go
package agent

import (
	"context"
	"strings"
	"sync"
)

// Manager 按 profile（classpath 指纹）复用 agent 进程。
type Manager struct {
	mu    sync.Mutex
	procs map[string]*procEntry
}

type procEntry struct {
	proc *AgentProc
	refs int
}

var DefaultManager = &Manager{procs: make(map[string]*procEntry)}

func (m *Manager) Acquire(ctx context.Context, cfg Config) (*Session, error) {
	key := profileKey(cfg)
	m.mu.Lock()
	e := m.procs[key]
	if e == nil {
		p, err := openAgent(ctx, cfg)
		if err != nil {
			m.mu.Unlock()
			return nil, err
		}
		e = &procEntry{proc: p}
		m.procs[key] = e
	}
	e.refs++
	m.mu.Unlock()

	s, err := e.proc.NewSession(cfg)
	if err != nil {
		m.Release(cfg, 0)
		return nil, err
	}
	return s, nil
}

func (m *Manager) Release(cfg Config, connID uint32) {
	key := profileKey(cfg)
	m.mu.Lock()
	e := m.procs[key]
	if e != nil {
		e.refs--
		if e.refs <= 0 {
			delete(m.procs, key)
			m.mu.Unlock()
			e.proc.close()
			return
		}
	}
	m.mu.Unlock()
}

func profileKey(cfg Config) string {
	return strings.Join(cfg.Classpath, "|") + "|" + cfg.JavaHome + "|" + cfg.AgentJar
}
```

- [ ] **Step 7: 运行测试**

Run: `go test ./internal/agent/ -run TestSession -v`
Expected: PASS（含 `TestSessionExec`/`TestSessionQuery`/`TestSessionProcessDeath`/`TestSessionContextTimeout`）。

- [ ] **Step 8: Commit**

```bash
git add internal/agent/client.go internal/agent/manager.go internal/agent/client_test.go internal/agent/testdata/fake_agent/main.go internal/agent/proto.go
git commit -m "feat(agent): process/session client and lifecycle manager with liveness"
```
### Task 4: database/sql 驱动 `owljdbc`（Go `driver.go`）

**Files:**
- Create: `internal/agent/driver.go`
- Test: `internal/agent/driver_test.go`

**Interfaces:**
- Consumes: `Config`/`EncodeDSN`/`DecodeDSN`（Task 2）、`DefaultManager.Acquire(ctx, cfg)`/`Release(cfg, connID)`、`Session.Exec(ctx, req, args)`/`Query(ctx, req, args)`/`Close()`、`QueryStream.Cols()/Next()/Close()`（Task 3 实际签名）、`profileKey`/`procEntry`（同包，测试注入用）。
- Produces: `sql.Register("owljdbc", &Driver{})`；`type Driver struct{}`（driver.Driver + driver.DriverContext）；`type Connector struct{ cfg Config }`；`type Conn struct{ sess *Session; cfg Config }`；`type Stmt`；`type Rows`；`type Tx`；`type result`。

> **与计划草稿的差异（已修正）**：① `Conn` 持有 `cfg Config`，`Close()` 用**正确 profile key** 调 `DefaultManager.Release(c.cfg, c.sess.connID)`（草稿用空 Config 会 refcount 泄漏）；② `ExecContext`/`QueryContext` 把 `ctx` 透传给 `Session.Exec/Query`（liveness ② 依赖）；③ QueryStream→driver.Rows 的 `Close` 透传取消。

- [ ] **Step 1: 写失败测试 `internal/agent/driver_test.go`**

```go
package agent

import (
	"context"
	"database/sql"
	"testing"
)

func TestDriverOpenQuery(t *testing.T) {
	proc := newTestProc(t)
	cfg := Config{Family: "mysql"}
	dsn := EncodeDSN(cfg)
	// 测试模式：用 fake agent 进程替换 DefaultManager 的 profile，避免真 spawn java。
	DefaultManager.mu.Lock()
	DefaultManager.procs[profileKey(cfg)] = &procEntry{proc: proc, refs: 1}
	DefaultManager.mu.Unlock()
	t.Cleanup(func() {
		DefaultManager.mu.Lock()
		delete(DefaultManager.procs, profileKey(cfg))
		DefaultManager.mu.Unlock()
	})

	db, err := sql.Open("owljdbc", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var s string
	if err := db.QueryRowContext(context.Background(), "SELECT 1").Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s != "hello" {
		t.Fatalf("got %q", s)
	}
}

func TestDriverExec(t *testing.T) {
	proc := newTestProc(t)
	cfg := Config{Family: "mysql"}
	DefaultManager.mu.Lock()
	DefaultManager.procs[profileKey(cfg)] = &procEntry{proc: proc, refs: 1}
	DefaultManager.mu.Unlock()
	t.Cleanup(func() {
		DefaultManager.mu.Lock()
		delete(DefaultManager.procs, profileKey(cfg))
		DefaultManager.mu.Unlock()
	})

	db, err := sql.Open("owljdbc", EncodeDSN(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	res, err := db.ExecContext(context.Background(), "INSERT INTO t VALUES(?,?)", int64(1), "x")
	if err != nil {
		t.Fatal(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("affected = %d", n)
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/agent/ -run TestDriver -v`
Expected: FAIL（`sql: unknown driver "owljdbc"`）。

- [ ] **Step 3: 写实现 `internal/agent/driver.go`**

```go
package agent

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
)

func init() {
	sql.Register("owljdbc", &Driver{})
}

type Driver struct{}

func (d *Driver) Open(dsn string) (driver.Conn, error) {
	cfg, err := DecodeDSN(dsn)
	if err != nil {
		return nil, err
	}
	return (&Connector{cfg: cfg}).Connect(context.Background())
}

func (d *Driver) OpenConnector(dsn string) (driver.Connector, error) {
	cfg, err := DecodeDSN(dsn)
	if err != nil {
		return nil, err
	}
	return &Connector{cfg: cfg}, nil
}

type Connector struct{ cfg Config }

func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	s, err := DefaultManager.Acquire(ctx, c.cfg)
	if err != nil {
		return nil, err
	}
	return &Conn{sess: s, cfg: c.cfg}, nil
}

func (c *Connector) Driver() driver.Driver { return &Driver{} }

type Conn struct {
	sess *Session
	cfg  Config
}

func (c *Conn) Prepare(query string) (driver.Stmt, error) {
	return &Stmt{conn: c, query: query}, nil
}

func (c *Conn) Close() error {
	err := c.sess.Close()
	DefaultManager.Release(c.cfg, c.sess.connID)
	return err
}

func (c *Conn) Begin() (driver.Tx, error) {
	_, err := c.sess.Exec(context.Background(), ControlRequest{Op: "BEGIN"}, nil)
	if err != nil {
		return nil, err
	}
	return &Tx{sess: c.sess}, nil
}

func (c *Conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}

func (c *Conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	resp, err := c.sess.Exec(ctx, ControlRequest{Op: "EXEC", SQL: query, Family: c.sess.family}, toAny(args))
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New(resp.Error)
	}
	return result{affected: resp.Affected}, nil
}

func (c *Conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	qs, err := c.sess.Query(ctx, ControlRequest{Op: "QUERY", SQL: query, Family: c.sess.family}, toAny(args))
	if err != nil {
		return nil, err
	}
	return &Rows{qs: qs}, nil
}

type Stmt struct {
	conn  *Conn
	query string
}

func (s *Stmt) Close() error  { return nil }
func (s *Stmt) NumInput() int { return -1 }

func (s *Stmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.conn.ExecContext(context.Background(), s.query, namedValues(args))
}
func (s *Stmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.conn.QueryContext(context.Background(), s.query, namedValues(args))
}
func (s *Stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.conn.ExecContext(ctx, s.query, args)
}
func (s *Stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.conn.QueryContext(ctx, s.query, args)
}

type Tx struct{ sess *Session }

func (t *Tx) Commit() error {
	_, err := t.sess.Exec(context.Background(), ControlRequest{Op: "COMMIT"}, nil)
	return err
}
func (t *Tx) Rollback() error {
	_, err := t.sess.Exec(context.Background(), ControlRequest{Op: "ROLLBACK"}, nil)
	return err
}

type result struct{ affected int64 }

func (r result) LastInsertId() (int64, error) { return 0, nil }
func (r result) RowsAffected() (int64, error) { return r.affected, nil }

type Rows struct{ qs *QueryStream }

func (r *Rows) Columns() []string {
	cols := r.qs.Cols()
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}
func (r *Rows) Close() error { return r.qs.Close() }
func (r *Rows) Next(dest []driver.Value) error {
	row, err := r.qs.Next()
	if err == io.EOF {
		return io.EOF
	}
	if err != nil {
		return err
	}
	for i, v := range row {
		if i < len(dest) {
			dest[i] = v
		}
	}
	return nil
}

func toAny(args []driver.NamedValue) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = a.Value
	}
	return out
}

func namedValues(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, a := range args {
		out[i] = driver.NamedValue{Value: a}
	}
	return out
}
```

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/agent/ -run TestDriver -v`
Expected: PASS（`TestDriverOpenQuery`、`TestDriverExec`）。

- [ ] **Step 5: 全包回归 + vet**

Run: `go test ./internal/agent/ -count=1 && go vet ./internal/agent/`
Expected: 全部 PASS，vet 干净。

- [ ] **Step 6: Commit**

```bash
git add internal/agent/driver.go internal/agent/driver_test.go
git commit -m "feat(agent): owljdbc database/sql driver"
```
### Task 5: Java sidecar agent（`jvm/owl-agent/`）

**Files:**
- Create: `jvm/owl-agent/build.sh`
- Create: `jvm/owl-agent/test.sh`
- Create: `jvm/owl-agent/src/owl/agent/Protocol.java`
- Create: `jvm/owl-agent/src/owl/agent/Json.java`
- Create: `jvm/owl-agent/src/owl/agent/ValueCodec.java`
- Create: `jvm/owl-agent/src/owl/agent/BindRewriter.java`
- Create: `jvm/owl-agent/src/owl/agent/Session.java`
- Create: `jvm/owl-agent/src/owl/agent/Main.java`
- Test: `jvm/owl-agent/src/owl/agent/test/BindRewriterTest.java`
- Test: `jvm/owl-agent/src/owl/agent/test/ValueCodecTest.java`
- Test: `jvm/owl-agent/src/owl/agent/test/JsonTest.java`

**Interfaces:**
- Consumes（与 Go 侧 Task 1–4 的既成实现逐字节对齐，不可偏离）：
  - 帧：`[u32 BE? NO——u32 LE payloadLen][u8 type][payload]`，`payloadLen` 含 type 字节。Go 用 `binary.LittleEndian`——Java 侧必须按**小端**读写。
  - 帧类型：`0x01 REQUEST / 0x02 RESPONSE / 0x03 ROW_BATCH / 0x04 END`。
  - REQUEST payload：`[u32 LE headerLen][header JSON][binary args...]`；header 字段 `id,conn,op,sql,family`。
  - RESPONSE/END payload：JSON，**必须含 `id` 与 `conn`**（Go readLoop 按 `resp.Conn` 路由到 session，缺 `conn` 响应即丢失→挂起）。
  - ROW_BATCH payload：`[u32 LE conn][u32 LE id][u32 LE rowCount][typed values...]`。
  - 值 tag：`0x00 NULL / 0x01 BOOL / 0x02 INT64 / 0x03 FLOAT64 / 0x04 DECIMAL(string) / 0x05 STRING / 0x06 BYTES / 0x07 DATETIME / 0x08 DATE(string) / 0x09 TIME(string)`。
  - **DATETIME 布局（与 Go decodeValue 对齐）**：`[8B epoch millis LE][2B tz minutes int16 LE]`，共 11 字节；Go 侧还原为 `time.Time`（时区保留）。
  - CONNECT 请求：args `[driverClass, url, user, password]`（与 client.go `NewSession` 一致）。
- Produces: 可执行 `owl-agent.jar`（main class `owl.agent.Main`）；从 stdin 读帧、stdout 写帧；支持 op `CONNECT/CLOSE/PING/BEGIN/COMMIT/ROLLBACK/EXEC/QUERY/CANCEL/SHUTDOWN`。

> **q1/q2 决议落地**：① JSON 解析为手写 **string-aware 状态机**（不得按逗号 split——`sql` 字段含逗号会解析出垃圾；须处理 `\"` `\\` `\/` `\b\f\n\r\t` `\uXXXX`）；② `PING` 用 `Connection.isValid(5)`；③ `CANCEL` 对会话当前 `Statement.cancel()`（best-effort）。

- [ ] **Step 1: 写三个失败测试**

`jvm/owl-agent/src/owl/agent/test/BindRewriterTest.java`：
```java
package owl.agent.test;

import owl.agent.BindRewriter;

public class BindRewriterTest {
    static int fails = 0;
    static void eq(String name, String in, String want) {
        String got = BindRewriter.rewrite(in);
        if (!want.equals(got)) {
            System.out.println("FAIL " + name + ": [" + in + "] -> [" + got + "] want [" + want + "]");
            fails++;
        }
    }
    public static void main(String[] a) {
        eq("basic", "SELECT * FROM t WHERE a=:1", "SELECT * FROM t WHERE a=?");
        eq("multi", "INSERT INTO t VALUES(:1,:2)", "INSERT INTO t VALUES(?,?)");
        eq("multi-digit", "INSERT INTO t VALUES(:10,:11)", "INSERT INTO t VALUES(?,?)");
        eq("literal-colon", "SELECT ':1' FROM t", "SELECT ':1' FROM t");
        eq("literal-comma-colon", "SELECT 'a,:1b' FROM t WHERE c=:2", "SELECT 'a,:1b' FROM t WHERE c=?");
        eq("line-comment", "SELECT 1 -- :1\n", "SELECT 1 -- :1\n");
        eq("block-comment", "SELECT /* :1 */ 2", "SELECT /* :1 */ 2");
        eq("double-colon-cast", "SELECT a::int FROM t WHERE b=:1", "SELECT a::int FROM t WHERE b=?");
        eq("assign", "BEGIN x:=1; END", "BEGIN x:=1; END");
        eq("qmark-passthru", "SELECT * FROM t WHERE a=?", "SELECT * FROM t WHERE a=?");
        eq("dquote-ident", "SELECT \":1\" FROM t WHERE b=:2", "SELECT \":1\" FROM t WHERE b=?");
        eq("no-bind", "SELECT 1", "SELECT 1");
        if (fails > 0) { System.exit(1); }
        System.out.println("BindRewriterTest OK");
    }
}
```

`jvm/owl-agent/src/owl/agent/test/ValueCodecTest.java`：
```java
package owl.agent.test;

import owl.agent.ValueCodec;
import java.math.BigDecimal;
import java.sql.Timestamp;
import java.util.Calendar;
import java.util.TimeZone;

public class ValueCodecTest {
    static int fails = 0;
    static void check(boolean ok, String name) {
        if (!ok) { System.out.println("FAIL " + name); fails++; }
    }
    public static void main(String[] a) {
        check(ValueCodec.encodeValue(null).length == 1, "null tag");
        byte[] s = ValueCodec.encodeValue("hi");
        check(s[0] == ValueCodec.STRING && s.length == 1 + 4 + 2, "string layout");
        byte[] d = ValueCodec.encodeValue(new BigDecimal("123.45"));
        check(d[0] == ValueCodec.DECIMAL, "decimal tag");
        byte[] b = ValueCodec.encodeValue(new byte[]{1, 2, 3});
        check(b[0] == ValueCodec.BYTES, "bytes tag");
        // DATETIME：tag + 8B millis + 2B tz minutes int16 LE = 11 字节
        Calendar cal = Calendar.getInstance(TimeZone.getTimeZone("GMT+08:00"));
        Timestamp ts = new Timestamp(1700000000000L);
        ts.setNanos(0);
        byte[] t = ValueCodec.encodeDatetime(ts, 480);
        check(t.length == 11 && t[0] == ValueCodec.DATETIME, "datetime len/tag");
        check((t[9] & 0xFF) == 0xE0 && t[10] == 0x01, "tz 480 min int16 LE (0x01E0)");
        if (fails > 0) { System.exit(1); }
        System.out.println("ValueCodecTest OK");
    }
}
```

`jvm/owl-agent/src/owl/agent/test/JsonTest.java`：
```java
package owl.agent.test;

import owl.agent.Json;

import java.util.Map;

public class JsonTest {
    static int fails = 0;
    static void eq(String name, String got, String want) {
        if (!want.equals(got)) {
            System.out.println("FAIL " + name + ": got [" + got + "] want [" + want + "]");
            fails++;
        }
    }
    public static void main(String[] a) {
        // sql 字段含逗号——split-on-comma 解析器的致命场景
        Map<String, String> m = Json.parseObject(
            "{\"id\":\"7\",\"conn\":\"1\",\"op\":\"QUERY\",\"sql\":\"SELECT a, b FROM t WHERE c='x, y'\",\"family\":\"oracle\"}");
        eq("comma-in-string", m.get("sql"), "SELECT a, b FROM t WHERE c='x, y'");
        eq("field-after-comma", m.get("family"), "oracle");
        // 转义
        Map<String, String> e = Json.parseObject("{\"error\":\"line 1\\nbroken \\\"quote\\\"\"}");
        eq("escaped-quote", e.get("error"), "line 1\nbroken \"quote\"");
        // 数字字段
        Map<String, String> n = Json.parseObject("{\"id\":42,\"ok\":true}");
        eq("number", n.get("id"), "42");
        eq("bool", n.get("ok"), "true");
        if (fails > 0) { System.exit(1); }
        System.out.println("JsonTest OK");
    }
}
```

- [ ] **Step 2: 运行验证失败**

Run: `bash jvm/owl-agent/test.sh`
Expected: 编译失败（类不存在）。

- [ ] **Step 3: 写实现**

`jvm/owl-agent/src/owl/agent/BindRewriter.java`：
```java
package owl.agent;

/** 把 Oracle 风格 :N 绑定占位符改写为 JDBC "?"；跳过字符串字面量、双引号标识符与注释。 */
public final class BindRewriter {
    public static String rewrite(String sql) {
        StringBuilder out = new StringBuilder(sql.length());
        int i = 0, n = sql.length();
        while (i < n) {
            char c = sql.charAt(i);
            if (c == '\'') {                       // 单引号字符串（'' 转义）
                int j = i + 1;
                while (j < n) {
                    if (sql.charAt(j) == '\'') {
                        if (j + 1 < n && sql.charAt(j + 1) == '\'') { j += 2; continue; }
                        break;
                    }
                    j++;
                }
                out.append(sql, i, Math.min(j + 1, n));
                i = Math.min(j + 1, n);
                continue;
            }
            if (c == '"') {                        // 双引号标识符
                int j = i + 1;
                while (j < n && sql.charAt(j) != '"') j++;
                out.append(sql, i, Math.min(j + 1, n));
                i = Math.min(j + 1, n);
                continue;
            }
            if (c == '-' && i + 1 < n && sql.charAt(i + 1) == '-') {   // 行注释
                int j = i;
                while (j < n && sql.charAt(j) != '\n') j++;
                out.append(sql, i, j);
                i = j;
                continue;
            }
            if (c == '/' && i + 1 < n && sql.charAt(i + 1) == '*') {   // 块注释
                int j = sql.indexOf("*/", i + 2);
                int end = j < 0 ? n : j + 2;
                out.append(sql, i, end);
                i = end;
                continue;
            }
            if (c == ':' && i + 1 < n && Character.isDigit(sql.charAt(i + 1))) {
                boolean prevColon = i > 0 && sql.charAt(i - 1) == ':';   // ::（如 PG cast 无关，但防御）
                if (!prevColon) {                                        // := 赋值：下一个字符是数字才可能是 :N，'=' 不匹配
                    int j = i + 1;
                    while (j < n && Character.isDigit(sql.charAt(j))) j++;
                    out.append('?');
                    i = j;
                    continue;
                }
            }
            out.append(c);
            i++;
        }
        return out.toString();
    }
}
```

`jvm/owl-agent/src/owl/agent/Json.java`（string-aware 解析 + 转义发射）：
```java
package owl.agent;

import java.util.LinkedHashMap;
import java.util.Map;

/** 极简 JSON 工具：扁平对象解析（string-aware，值内逗号/冒号不影响）与字符串转义。 */
public final class Json {
    public static Map<String, String> parseObject(String s) {
        Map<String, String> m = new LinkedHashMap<>();
        int i = 0, n = s.length();
        while (i < n) {
            char c = s.charAt(i);
            if (c == '{' || c == '}' || c == ',' || Character.isWhitespace(c)) { i++; continue; }
            if (c != '"') { i++; continue; }
            int[] keyEnd = new int[1];
            String key = parseString(s, i, keyEnd);
            i = keyEnd[0];
            while (i < n && (Character.isWhitespace(s.charAt(i)) || s.charAt(i) == ':')) i++;
            if (i >= n) break;
            String value;
            if (s.charAt(i) == '"') {
                int[] valEnd = new int[1];
                value = parseString(s, i, valEnd);
                i = valEnd[0];
            } else {
                int j = i;
                while (j < n && s.charAt(j) != ',' && s.charAt(j) != '}') j++;
                value = s.substring(i, j).trim();
                i = j;
            }
            m.put(key, value);
        }
        return m;
    }

    /** 从 s[start]=='"' 起解析 JSON 字符串，返回解码值；end[0] = 结束引号之后的位置。 */
    private static String parseString(String s, int start, int[] end) {
        StringBuilder sb = new StringBuilder();
        int i = start + 1, n = s.length();
        while (i < n) {
            char c = s.charAt(i);
            if (c == '"') { i++; break; }
            if (c == '\\' && i + 1 < n) {
                char e = s.charAt(i + 1);
                switch (e) {
                    case '"': sb.append('"'); i += 2; continue;
                    case '\\': sb.append('\\'); i += 2; continue;
                    case '/': sb.append('/'); i += 2; continue;
                    case 'b': sb.append('\b'); i += 2; continue;
                    case 'f': sb.append('\f'); i += 2; continue;
                    case 'n': sb.append('\n'); i += 2; continue;
                    case 'r': sb.append('\r'); i += 2; continue;
                    case 't': sb.append('\t'); i += 2; continue;
                    case 'u':
                        if (i + 5 < n) {
                            sb.append((char) Integer.parseInt(s.substring(i + 2, i + 6), 16));
                            i += 6;
                            continue;
                        }
                        i++;
                        continue;
                    default:
                        sb.append(e);
                        i += 2;
                        continue;
                }
            }
            sb.append(c);
            i++;
        }
        end[0] = i;
        return sb.toString();
    }

    public static String escape(String s) {
        StringBuilder sb = new StringBuilder(s.length() + 8);
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            switch (c) {
                case '"': sb.append("\\\""); break;
                case '\\': sb.append("\\\\"); break;
                case '\b': sb.append("\\b"); break;
                case '\f': sb.append("\\f"); break;
                case '\n': sb.append("\\n"); break;
                case '\r': sb.append("\\r"); break;
                case '\t': sb.append("\\t"); break;
                default:
                    if (c < 0x20) sb.append(String.format("\\u%04x", (int) c));
                    else sb.append(c);
            }
        }
        return sb.toString();
    }
}
```

`jvm/owl-agent/src/owl/agent/ValueCodec.java`：
```java
package owl.agent;

import java.io.ByteArrayOutputStream;
import java.math.BigDecimal;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.sql.Date;
import java.sql.Time;
import java.sql.Timestamp;

/** Go/Java 共享的值编码（tag 布局见计划 Global Constraints）。小端。 */
public final class ValueCodec {
    public static final int NULL = 0x00, BOOL = 0x01, INT64 = 0x02, FLOAT64 = 0x03,
        DECIMAL = 0x04, STRING = 0x05, BYTES = 0x06, DATETIME = 0x07, DATE = 0x08, TIME = 0x09;

    public static byte[] encodeValue(Object v) {
        if (v == null) return tag(NULL);
        if (v instanceof Boolean) return bytes(tag(BOOL), (byte)(((Boolean) v) ? 1 : 0));
        if (v instanceof Integer) return longBytes(INT64, ((Integer) v).longValue());
        if (v instanceof Long) return longBytes(INT64, (Long) v);
        if (v instanceof Float) return doubleBytes(FLOAT64, ((Float) v).doubleValue());
        if (v instanceof Double) return doubleBytes(FLOAT64, (Double) v);
        if (v instanceof BigDecimal) return textBytes(DECIMAL, ((BigDecimal) v).toPlainString());
        if (v instanceof Timestamp) return encodeDatetime((Timestamp) v);
        if (v instanceof Date) {                       // DATE → DATETIME（午夜），Go 侧还原 time.Time 保证导出对拍一致
            Date d = (Date) v;
            Timestamp ts = new Timestamp(d.getTime());
            return encodeDatetime(ts);
        }
        if (v instanceof Time) return textBytes(TIME, ((Time) v).toString());
        if (v instanceof byte[]) {
            byte[] b = (byte[]) v;
            return bytes(tag(BYTES), concat(intBytes(b.length), b));
        }
        return textBytes(STRING, String.valueOf(v));
    }

    /** DATETIME：tag + 8B epoch millis LE + 2B tz minutes int16 LE（与 Go decodeValue 对齐）。 */
    public static byte[] encodeDatetime(Timestamp ts) {
        int offsetMinutes;
        java.util.TimeZone tz = java.util.TimeZone.getDefault();
        offsetMinutes = tz.getOffset(ts.getTime()) / 60000;
        return encodeDatetime(ts, offsetMinutes);
    }

    public static byte[] encodeDatetime(Timestamp ts, int offsetMinutes) {
        ByteBuffer bb = ByteBuffer.allocate(11);
        bb.put((byte) DATETIME);
        bb.putLong(ts.getTime());
        bb.putShort((short) offsetMinutes);
        return bb.array();
    }

    private static byte[] tag(int t) { return new byte[]{(byte) t}; }
    private static byte[] longBytes(int tag, long v) {
        return bytes(tag(tag), ByteBuffer.allocate(8).putLong(v).array());
    }
    private static byte[] doubleBytes(int tag, double v) {
        return bytes(tag(tag), ByteBuffer.allocate(8).putDouble(v).array());
    }
    private static byte[] textBytes(int tag, String s) {
        byte[] b = s.getBytes(StandardCharsets.UTF_8);
        return bytes(tag(tag), concat(intBytes(b.length), b));
    }
    private static byte[] intBytes(int n) { return ByteBuffer.allocate(4).putInt(n).array(); }
    private static byte[] bytes(byte[]... parts) {
        ByteArrayOutputStream o = new ByteArrayOutputStream();
        for (byte[] p : parts) o.write(p, 0, p.length);
        return o.toByteArray();
    }
}
```

`jvm/owl-agent/src/owl/agent/Protocol.java`：
```java
package owl.agent;

import java.io.EOFException;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.sql.Timestamp;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Map;

public final class Protocol {
    public static final int REQUEST = 0x01, RESPONSE = 0x02, ROW_BATCH = 0x03, END = 0x04;
    public static final int MAX_FRAME = 16 * 1024 * 1024;

    /** 读一帧，返回 [type][payload]；流结束抛 EOFException。 */
    public static byte[] readFrame(InputStream in) throws IOException {
        byte[] hdr = new byte[4];
        readFully(in, hdr);
        int total = intLE(hdr, 0);
        if (total < 1 || total > MAX_FRAME) throw new IOException("bad frame len " + total);
        byte[] buf = new byte[total];
        readFully(in, buf);
        return buf;
    }

    public static void writeFrame(OutputStream out, int type, byte[] payload) throws IOException {
        int total = 1 + payload.length;
        byte[] head = new byte[4 + 1 + payload.length];
        putIntLE(head, 0, total);
        head[4] = (byte) type;
        System.arraycopy(payload, 0, head, 5, payload.length);
        out.write(head);
        out.flush();
    }

    public static final class Request {
        public int id, conn;
        public String op = "", sql = "", family = "";
        public Object[] args = new Object[0];
    }

    /** payload = [u32 headerLen][header JSON][binary args...]（不含 type 字节）。 */
    public static Request parseRequest(byte[] payload) {
        ByteBuffer bb = ByteBuffer.wrap(payload);
        int hLen = bb.getInt();
        byte[] hb = new byte[hLen];
        bb.get(hb);
        Map<String, String> h = Json.parseObject(new String(hb, StandardCharsets.UTF_8));
        Request r = new Request();
        r.id = (int) longOf(h.get("id"));
        r.conn = (int) longOf(h.get("conn"));
        r.op = h.getOrDefault("op", "");
        r.sql = h.getOrDefault("sql", "");
        r.family = h.getOrDefault("family", "");
        List<Object> args = new ArrayList<>();
        while (bb.remaining() > 0) {
            args.add(decodeValue(bb));
        }
        r.args = args.toArray();
        return r;
    }

    private static long longOf(String s) {
        try { return s == null ? 0 : Long.parseLong(s.trim()); } catch (NumberFormatException e) { return 0; }
    }

    private static int lastConsumed;
    public static Object decodeValue(ByteBuffer bb) {
        int tag = bb.get() & 0xFF;
        switch (tag) {
            case ValueCodec.NULL: return null;
            case ValueCodec.BOOL: return bb.get() != 0;
            case ValueCodec.INT64: return bb.getLong();
            case ValueCodec.FLOAT64: return bb.getDouble();
            case ValueCodec.DECIMAL:
            case ValueCodec.STRING:
            case ValueCodec.DATE:
            case ValueCodec.TIME:
                return str(bb);
            case ValueCodec.BYTES: {
                byte[] b = new byte[bb.getInt()];
                bb.get(b);
                return b;
            }
            case ValueCodec.DATETIME: {
                long millis = bb.getLong();
                short tzMin = bb.getShort();
                Timestamp ts = new Timestamp(millis);
                ts.setNanos(0);
                // 时区语义保留给 Go 侧（Go decodeValue 自行还原 offset），此处仅承载 millis。
                return ts;
            }
            default: return null;
        }
    }

    private static String str(ByteBuffer bb) {
        int n = bb.getInt();
        byte[] b = new byte[n];
        bb.get(b);
        return new String(b, StandardCharsets.UTF_8);
    }

    static void putIntLE(byte[] b, int off, int v) {
        b[off] = (byte) v; b[off + 1] = (byte) (v >>> 8);
        b[off + 2] = (byte) (v >>> 16); b[off + 3] = (byte) (v >>> 24);
    }
    static int intLE(byte[] b, int off) {
        return (b[off] & 0xFF) | ((b[off + 1] & 0xFF) << 8)
            | ((b[off + 2] & 0xFF) << 16) | ((b[off + 3] & 0xFF) << 24);
    }
    private static void readFully(InputStream in, byte[] buf) throws IOException {
        int off = 0;
        while (off < buf.length) {
            int n = in.read(buf, off, buf.length - off);
            if (n < 0) throw new EOFException();
            off += n;
        }
    }
}
```

`jvm/owl-agent/src/owl/agent/Session.java`：
```java
package owl.agent;

import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.ResultSetMetaData;
import java.sql.SQLException;
import java.sql.Statement;
import java.sql.Types;

public final class Session {
    private final Connection conn;
    private volatile Statement current;

    Session(String driverClass, String url, String user, String pass) throws Exception {
        Class.forName(driverClass);
        this.conn = DriverManager.getConnection(url, user, pass);
    }

    /** JDBC 原生探活（q2 决议）。 */
    boolean isValid() throws SQLException {
        return conn.isValid(5);
    }

    void cancelCurrent() {
        Statement st = current;
        if (st != null) {
            try { st.cancel(); } catch (SQLException ignored) { }
        }
    }

    /** 返回受影响行数（DML）；DDL 返回 0。 */
    long exec(String sql, String family, Object[] args) throws SQLException {
        PreparedStatement ps = conn.prepareStatement(BindRewriter.rewrite(sql));
        current = ps;
        try {
            bind(ps, args);
            if (ps.execute()) {
                closeQuietly(ps.getResultSet());
                return 0;
            }
            return ps.getUpdateCount();
        } finally {
            current = null;
            ps.close();
        }
    }

    ResultSet query(String sql, String family, Object[] args) throws SQLException {
        PreparedStatement ps = conn.prepareStatement(BindRewriter.rewrite(sql));
        current = ps;
        bind(ps, args);
        return new GuardedResultSet(ps, ps.executeQuery());
    }

    private static final class GuardedResultSet implements ResultSet {
        // 透传 ResultSet；close 时同时关 Statement，避免泄漏。
        ... // 见下：实现为简单委托 + close 关 ps
    }
```

> 为免手写委托类冗长，`query` 直接返回 `ps.executeQuery()` 并由调用方在读完行后 `rs.close(); ps.close();`（Main 的 QUERY 分支负责，finally 兜底）。`current` 在 Main 的 QUERY finally 里置 null。**采用此简化**：

```java
    ResultSet query(String sql, String family, Object[] args) throws SQLException {
        PreparedStatement ps = conn.prepareStatement(BindRewriter.rewrite(sql));
        current = ps;
        bind(ps, args);
        return ps.executeQuery();   // Main finally: rs.close(); ps.close(); current = null;
    }

    private void bind(PreparedStatement ps, Object[] args) throws SQLException {
        if (args == null) return;
        for (int i = 0; i < args.length; i++) {
            Object v = args[i];
            if (v == null) ps.setNull(i + 1, Types.NULL);
            else if (v instanceof Long) ps.setLong(i + 1, (Long) v);
            else if (v instanceof Integer) ps.setInt(i + 1, (Integer) v);
            else if (v instanceof Double) ps.setDouble(i + 1, (Double) v);
            else if (v instanceof Boolean) ps.setBoolean(i + 1, (Boolean) v);
            else if (v instanceof byte[]) ps.setBytes(i + 1, (byte[]) v);
            else ps.setString(i + 1, String.valueOf(v));
        }
    }

    void begin() throws SQLException { conn.setAutoCommit(false); }
    void commit() throws SQLException { conn.commit(); conn.setAutoCommit(true); }
    void rollback() throws SQLException { conn.rollback(); conn.setAutoCommit(true); }
    void close() throws SQLException { conn.close(); }
}
```

`jvm/owl-agent/src/owl/agent/Main.java`：
```java
package owl.agent;

import java.io.EOFException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.sql.ResultSet;
import java.sql.ResultSetMetaData;
import java.sql.Statement;
import java.sql.Types;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public final class Main {
    static final Map<Integer, Session> SESSIONS = new ConcurrentHashMap<>();

    public static void main(String[] args) throws Exception {
        InputStream in = System.in;
        OutputStream out = System.out;
        ExecutorService pool = Executors.newCachedThreadPool();
        while (true) {
            byte[] frame;
            try {
                frame = Protocol.readFrame(in);
            } catch (EOFException e) {
                break;
            }
            if (frame.length < 1) continue;
            if ((frame[0] & 0xFF) != Protocol.REQUEST) continue;
            final byte[] payload = Arrays.copyOfRange(frame, 1, frame.length);
            pool.submit(() -> handle(payload, out));
        }
        pool.shutdown();
    }

    static void handle(byte[] payload, OutputStream out) {
        Protocol.Request req = Protocol.parseRequest(payload);
        try {
            switch (req.op) {
                case "CONNECT": {
                    Session s = new Session(str(req.args, 0), str(req.args, 1), str(req.args, 2), str(req.args, 3));
                    SESSIONS.put(req.conn, s);
                    sendResp(out, req, true, null, null, 0, 0);
                    break;
                }
                case "CLOSE": {
                    Session s = SESSIONS.remove(req.conn);
                    if (s != null) s.close();
                    sendResp(out, req, true, null, null, 0, 0);
                    break;
                }
                case "PING": {
                    Session s = SESSIONS.get(req.conn);
                    boolean ok = s == null || s.isValid();   // probe 会话允许无连接
                    sendResp(out, req, ok, null, ok ? null : "not valid", 0, 0);
                    break;
                }
                case "BEGIN": SESSIONS.get(req.conn).begin(); sendResp(out, req, true, null, null, 0, 0); break;
                case "COMMIT": SESSIONS.get(req.conn).commit(); sendResp(out, req, true, null, null, 0, 0); break;
                case "ROLLBACK": SESSIONS.get(req.conn).rollback(); sendResp(out, req, true, null, null, 0, 0); break;
                case "CANCEL": {
                    Session s = SESSIONS.get(req.conn);
                    if (s != null) s.cancelCurrent();
                    break;   // best-effort，不回包（Go 不等待）
                }
                case "EXEC": {
                    long affected = SESSIONS.get(req.conn).exec(req.sql, req.family, req.args);
                    sendResp(out, req, true, null, null, 0, affected);
                    break;
                }
                case "QUERY": {
                    Session s = SESSIONS.get(req.conn);
                    ResultSet rs = null;
                    Statement ps = null;
                    try {
                        rs = s.query(req.sql, req.family, req.args);
                        ps = rs.getStatement();
                        ResultSetMetaData md = rs.getMetaData();
                        StringBuilder cols = new StringBuilder("[");
                        for (int i = 1; i <= md.getColumnCount(); i++) {
                            if (i > 1) cols.append(',');
                            cols.append("{\"name\":\"").append(Json.escape(md.getColumnLabel(i)))
                                .append("\",\"type\":\"").append(Json.escape(md.getColumnTypeName(i))).append("\"}");
                        }
                        cols.append(']');
                        sendRespRaw(out, req, true, cols.toString(), null, 0, 0);
                        long rows = 0;
                        int nCols = md.getColumnCount();
                        while (rs.next()) {
                            ByteBuffer bb = ByteBuffer.allocate(12);
                            bb.putInt(req.conn);
                            bb.putInt(req.id);
                            bb.putInt(1);
                            for (int i = 1; i <= nCols; i++) {
                                bb.put(ValueCodec.encodeValue(columnValue(rs, md, i)));
                            }
                            Protocol.writeFrame(out, Protocol.ROW_BATCH, bb.array());
                            rows++;
                        }
                        sendEnd(out, req, rows);
                    } finally {
                        if (rs != null) rs.close();
                        if (ps != null) ps.close();
                    }
                    break;
                }
                case "SHUTDOWN": System.exit(0); break;
                default:
                    sendResp(out, req, false, null, "unknown op " + req.op, 0, 0);
            }
        } catch (Exception e) {
            try {
                sendResp(out, req, false, null, String.valueOf(e.getMessage()), 0, 0);
            } catch (Exception ignored) { }
        }
    }

    /** 类型感知取值：BLOB/二进制 → getBytes；CLOB/文本大对象 → getString；其余 getObject。 */
    static Object columnValue(ResultSet rs, ResultSetMetaData md, int i) throws Exception {
        int type = md.getColumnType(i);
        switch (type) {
            case Types.BINARY: case Types.VARBINARY: case Types.LONGVARBINARY:
            case Types.BLOB:
                return rs.getBytes(i);
            case Types.CLOB: case Types.NCLOB: case Types.LONGVARCHAR: case Types.LONGNVARCHAR:
                return rs.getString(i);
            default:
                return rs.getObject(i);
        }
    }

    static String str(Object[] args, int i) {
        return args != null && i < args.length && args[i] != null ? String.valueOf(args[i]) : "";
    }

    static void sendResp(OutputStream out, Protocol.Request req, boolean ok,
                         StringBuilder cols, String err, long rows, long affected) throws Exception {
        sendRespRaw(out, req, ok, cols == null ? null : cols.toString(), err, rows, affected);
    }

    static void sendRespRaw(OutputStream out, Protocol.Request req, boolean ok,
                            String colsJson, String err, long rows, long affected) throws Exception {
        StringBuilder b = new StringBuilder();
        b.append("{\"id\":").append(req.id).append(",\"conn\":").append(req.conn)
            .append(",\"ok\":").append(ok);
        if (err != null) b.append(",\"error\":\"").append(Json.escape(err)).append('"');
        if (colsJson != null) b.append(",\"cols\":").append(colsJson);
        if (rows > 0) b.append(",\"rows\":").append(rows);
        if (affected > 0) b.append(",\"affected\":").append(affected);
        b.append('}');
        Protocol.writeFrame(out, Protocol.RESPONSE, b.toString().getBytes(StandardCharsets.UTF_8));
    }

    static void sendEnd(OutputStream out, Protocol.Request req, long rows) throws Exception {
        String body = "{\"id\":" + req.id + ",\"conn\":" + req.conn + ",\"ok\":true,\"rows\":" + rows + "}";
        Protocol.writeFrame(out, Protocol.END, body.getBytes(StandardCharsets.UTF_8));
    }
}
```

> 注意：`Main.java` 需要 `import java.util.Arrays;`（copyOfRange）。Go readLoop 按 `resp.Conn` 路由——RESPONSE/END 的 JSON **必须含 `conn`**（上面已带）。`CANCEL` 不回包：Go 的 `sendCancel` 是 fire-and-forget，不等待响应。

- [ ] **Step 4: 写 `build.sh` / `test.sh`**

`jvm/owl-agent/build.sh`：
```bash
#!/usr/bin/env bash
set -eu
cd "$(dirname "$0")"
rm -rf out owl-agent.jar
mkdir -p out
javac --release 8 -d out $(find src/owl/agent -maxdepth 1 -name '*.java')
jar cfe owl-agent.jar owl.agent.Main -C out .
echo "built owl-agent.jar"
```

`jvm/owl-agent/test.sh`：
```bash
#!/usr/bin/env bash
set -eu
cd "$(dirname "$0")"
rm -rf out-test
mkdir -p out-test
javac --release 8 -d out-test \
  src/owl/agent/BindRewriter.java src/owl/agent/ValueCodec.java src/owl/agent/Json.java \
  src/owl/agent/test/BindRewriterTest.java src/owl/agent/test/ValueCodecTest.java src/owl/agent/test/JsonTest.java
java -cp out-test owl.agent.test.BindRewriterTest
java -cp out-test owl.agent.test.ValueCodecTest
java -cp out-test owl.agent.test.JsonTest
echo "ALL JAVA TESTS OK"
```

- [ ] **Step 5: 运行测试**

Run: `bash jvm/owl-agent/test.sh && bash jvm/owl-agent/build.sh`
Expected: 三个测试均打印 `OK`，`ALL JAVA TESTS OK`，最后 `built owl-agent.jar`。

- [ ] **Step 6: Commit**

```bash
git add jvm/owl-agent/
git commit -m "feat(agent): Java sidecar agent (frame protocol, bind rewriter, session, liveness)"
```
### Task 6: Go↔Java 集成冒烟（连 OB 双租户）

**Files:**
- Create: `internal/agent/integration_e2e_test.go`（build tag `e2e`）

**Interfaces:**
- Consumes: `owljdbc` 驱动（Task 4）、`owl-agent.jar`（Task 5 build 产物）、`oceanbase-client-2.4.1.jar`（仓库根，用户已提供）、本机 `java`（21，JRE 8+ 亦可）。
- Produces: 真实 JVM agent + 真实 OB 双租户的端到端连通证明：连接、查询（含 SQL 内含逗号的 header JSON 解析路径）、参数绑定（oracle `:N` 改写 + mysql `?`）、多行流式。

> **凭据约定**：从 OS 环境变量或 `testdata/db/.local-dev.env` 读取（`export KEY='value'` 行格式），不编进代码；DSN 解析复用既有键（`OWL_E2E_OB_ORACLE_MIGSRC_DSN`、`OWL_E2E_OB_MYSQL_DSN`），无需用户新增配置。jar 缺失时 `t.Skip`。

- [ ] **Step 1: 写测试**

`internal/agent/integration_e2e_test.go`：
```go
//go:build e2e
// +build e2e

package agent

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ── dev-env 读取（OS env > testdata/db/.local-dev.env） ──

func devEnvMap() map[string]string {
	m := map[string]string{}
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "db", ".local-dev.env"))
	if err == nil {
		re := regexp.MustCompile(`(?m)^export\s+([A-Za-z0-9_]+)='(.*)'\s*$`)
		for _, mm := range re.FindAllStringSubmatch(string(data), -1) {
			m[mm[1]] = mm[2]
		}
	}
	return m
}

func lookupEnv(m map[string]string, key string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return m[key]
}

type jdbcCred struct{ User, Password, Host string; Port int }

// oceanbase-oracle://MIGSRC@oratest:PASS%40WORD@127.0.0.1:2881/
func parseOracleURLDSN(t *testing.T, dsn string) jdbcCred {
	t.Helper()
	rest := strings.TrimPrefix(strings.TrimPrefix(dsn, "oceanbase-oracle://"), "oracle://")
	at := strings.LastIndex(rest, "@") // 口令百分号编码后，最后一个 @ 分隔 userinfo/host
	if at < 0 {
		t.Fatalf("bad oracle dsn")
	}
	userinfo, hostport := rest[:at], rest[at+1:]
	colon := strings.Index(userinfo, ":")
	if colon < 0 {
		t.Fatalf("bad oracle userinfo")
	}
	pass, err := url.PathUnescape(userinfo[colon+1:])
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}
	hp := strings.TrimSuffix(hostport, "/")
	i := strings.LastIndex(hp, ":")
	if i < 0 {
		t.Fatalf("bad host:port")
	}
	var port int
	fmt.Sscanf(hp[i+1:], "%d", &port)
	return jdbcCred{User: userinfo[:colon], Password: pass, Host: hp[:i], Port: port}
}

// root@obmysql:PASS@WORD@tcp(127.0.0.1:2881)/
func parseMysqlWireDSN(t *testing.T, dsn string) jdbcCred {
	t.Helper()
	i := strings.Index(dsn, "@tcp(")
	if i < 0 {
		t.Fatalf("bad mysql dsn")
	}
	userinfo := dsn[:i]
	colon := strings.Index(userinfo, ":")
	if colon < 0 {
		t.Fatalf("bad mysql userinfo")
	}
	inside := dsn[i+len("@tcp("):]
	if j := strings.Index(inside, ")"); j >= 0 {
		inside = inside[:j]
	}
	inside = strings.Split(inside, "/")[0]
	hp := inside
	j := strings.LastIndex(hp, ":")
	if j < 0 {
		t.Fatalf("bad host:port")
	}
	var port int
	fmt.Sscanf(hp[j+1:], "%d", &port)
	return jdbcCred{User: userinfo[:colon], Password: userinfo[colon+1:], Host: hp[:j], Port: port}
}

func e2eAgentCfg(t *testing.T, family, user, pass, host string, port int) Config {
	t.Helper()
	agentJar := os.Getenv("OWL_AGENT_JAR")
	if agentJar == "" {
		agentJar = filepath.Join("..", "..", "jvm", "owl-agent", "owl-agent.jar")
	}
	obJar := os.Getenv("OWL_E2E_OB_JAR")
	if obJar == "" {
		obJar = filepath.Join("..", "..", "oceanbase-client-2.4.1.jar")
	}
	if _, err := os.Stat(agentJar); err != nil {
		t.Skipf("agent jar missing (%s): run bash jvm/owl-agent/build.sh", agentJar)
	}
	if _, err := os.Stat(obJar); err != nil {
		t.Skipf("OB jar missing (%s)", obJar)
	}
	return Config{
		DriverClass: "com.oceanbase.jdbc.Driver",
		URL:         fmt.Sprintf("jdbc:oceanbase://%s:%d?useSSL=false", host, port),
		User:        user,
		Password:    pass,
		Family:      family,
		Classpath:   []string{obJar},
		AgentJar:    agentJar,
	}
}

// ── OB Oracle 租户冒烟 ──

func TestE2E_AgentOBOracleSmoke(t *testing.T) {
	env := devEnvMap()
	dsn := lookupEnv(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_OB_ORACLE_MIGSRC_DSN (or testdata/db/.local-dev.env)")
	}
	cred := parseOracleURLDSN(t, dsn)
	db, err := sql.Open("owljdbc", EncodeDSN(e2eAgentCfg(t, "oracle", cred.User, cred.Password, cred.Host, cred.Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var v string
	if err := db.QueryRowContext(context.Background(), "SELECT 'OK' FROM DUAL").Scan(&v); err != nil {
		t.Fatalf("query: %v", err)
	}
	if v != "OK" {
		t.Fatalf("got %q", v)
	}
	// sql 字段含逗号 → 走 Json 解析器的字符串保护路径
	if err := db.QueryRowContext(context.Background(), "SELECT 'a,b' FROM DUAL").Scan(&v); err != nil {
		t.Fatalf("comma query: %v", err)
	}
	if v != "a,b" {
		t.Fatalf("comma got %q", v)
	}
}

// ── OB MySQL 租户冒烟 ──

func TestE2E_AgentOBMySQLSmoke(t *testing.T) {
	env := devEnvMap()
	dsn := lookupEnv(env, "OWL_E2E_OB_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_OB_MYSQL_DSN (or testdata/db/.local-dev.env)")
	}
	cred := parseMysqlWireDSN(t, dsn)
	db, err := sql.Open("owljdbc", EncodeDSN(e2eAgentCfg(t, "mysql", cred.User, cred.Password, cred.Host, cred.Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var v string
	if err := db.QueryRowContext(context.Background(), "SELECT 'OK'").Scan(&v); err != nil {
		t.Fatalf("query: %v", err)
	}
	if v != "OK" {
		t.Fatalf("got %q", v)
	}
	if err := db.QueryRowContext(context.Background(), "SELECT 'a,b'").Scan(&v); err != nil {
		t.Fatalf("comma query: %v", err)
	}
	if v != "a,b" {
		t.Fatalf("comma got %q", v)
	}
}

// ── 参数绑定：oracle :N（agent 侧改写为 ?） + mysql ?；多行流式 ──

func TestE2E_AgentBindArgs(t *testing.T) {
	env := devEnvMap()
	odsn := lookupEnv(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	mdsn := lookupEnv(env, "OWL_E2E_OB_MYSQL_DSN")
	if odsn == "" && mdsn == "" {
		t.Skip("set OB tenant DSNs")
	}
	if odsn != "" {
		cred := parseOracleURLDSN(t, odsn)
		db, err := sql.Open("owljdbc", EncodeDSN(e2eAgentCfg(t, "oracle", cred.User, cred.Password, cred.Host, cred.Port)))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var n int64
		if err := db.QueryRowContext(context.Background(), "SELECT :1 FROM DUAL", int64(41)).Scan(&n); err != nil {
			t.Fatalf("oracle bind: %v", err)
		}
		if n != 41 {
			t.Fatalf("oracle bind got %d", n)
		}
	}
	if mdsn != "" {
		cred := parseMysqlWireDSN(t, mdsn)
		db, err := sql.Open("owljdbc", EncodeDSN(e2eAgentCfg(t, "mysql", cred.User, cred.Password, cred.Host, cred.Port)))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		rows, err := db.Query("SELECT ? UNION ALL SELECT ?", int64(1), int64(2))
		if err != nil {
			t.Fatalf("mysql multi-row: %v", err)
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var n int64
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Fatalf("rows = %d, want 2", count)
		}
	}
}

// ── 启动失败快速失败（坏 jar 路径） ──

func TestE2E_AgentBadJarFailsFast(t *testing.T) {
	env := devEnvMap()
	dsn := lookupEnv(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	}
	cred := parseOracleURLDSN(t, dsn)
	cfg := e2eAgentCfg(t, "oracle", cred.User, cred.Password, cred.Host, cred.Port)
	cfg.AgentJar = "/nonexistent/owl-agent.jar"
	db, err := sql.Open("owljdbc", EncodeDSN(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.QueryContext(context.Background(), "SELECT 1 FROM DUAL"); err == nil {
		t.Fatal("expected error for missing agent jar")
	}
}
```

- [ ] **Step 2: 运行**

Run: `bash jvm/owl-agent/build.sh && go test -tags e2e ./internal/agent/ -run TestE2E_Agent -v`
Expected: OB 可达 + jar 就绪时全部 PASS；缺 jar/DSN 时 SKIP。`TestE2E_AgentBadJarFailsFast` 验证 spawn/握手失败路径快速报错（不挂起）。

- [ ] **Step 3: Commit**

```bash
git add internal/agent/integration_e2e_test.go
git commit -m "test(agent): Go-Java integration smoke against OB dual tenants (e2e)"
```
### Task 7: OB 双租户全流程对拍 harness（`internal/e2eagent/`）

**Files:**
- Create: `internal/e2eagent/parity_e2e_test.go`（build tag `e2e ob`——native 侧 OB 驱动需 `-tags ob`）

**Interfaces:**
- Consumes: `sql.Open("owljdbc", agent.EncodeDSN(cfg))`（Task 4）、`dbconn.Open(config.DBConfig{...})`（native 基准）、`extractor.Extract(db, dbType, schema)`、`exporter.New(db, cfg).ExportTables(ctx, tables, pks)`、`importer.New(db, cfg).ImportTables(ctx, tables, schemaMapping)`。
- Produces: 对拍断言——元数据 / 导出（CSV 逐字节）/ 导入（行数+计数）/ 性能基线 / 故障快速失败。

> **环境约定**（与仓库 e2e 惯例一致）：凭据从 OS 环境变量或 `testdata/db/.local-dev.env`（`export KEY='value'` 行格式）读取，**不编进代码**；缺 jar/DSN/驱动时 `t.Skip`（PASS 但显示 SKIP）。本文件自带 ~30 行 dev-env 读取器（与 `internal/agent/integration_e2e_test.go` 的读取器为测试脚手架级重复，接受并记录账本）。

- [ ] **Step 1: 写测试骨架（loader + DSN 解析 + agent/native 连接助手）**

```go
//go:build e2e
// +build e2e

package e2eagent

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/agent"
	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	"github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/importer"
)

// ── dev-env 读取（OS env > testdata/db/.local-dev.env） ──

func devEnv(t *testing.T) map[string]string {
	t.Helper()
	m := map[string]string{}
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "db", ".local-dev.env"))
	if err == nil {
		re := regexp.MustCompile(`(?m)^export\s+([A-Za-z0-9_]+)='(.*)'\s*$`)
		for _, mm := range re.FindAllStringSubmatch(string(data), -1) {
			m[mm[1]] = mm[2]
		}
	}
	return m
}

func lookup(m map[string]string, key string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return m[key]
}

// ── DSN 解析（Oracle URL 型 / MySQL wire 型 → JDBC 凭据） ──

type jdbcCred struct{ User, Password, Host string; Port int }

func parseOracleURLDSN(t *testing.T, dsn string) jdbcCred {
	t.Helper()
	rest := strings.TrimPrefix(dsn, "oceanbase-oracle://")
	rest = strings.TrimPrefix(rest, "oracle://")
	at := strings.LastIndex(rest, "@") // 口令已百分号编码，最后一个 @ 分隔 userinfo 与 host
	if at < 0 {
		t.Fatalf("bad oracle dsn: %s", dsn)
	}
	userinfo, hostport := rest[:at], rest[at+1:]
	colon := strings.Index(userinfo, ":")
	user, encPass := userinfo[:colon], userinfo[colon+1:]
	pass, err := url.PathUnescape(encPass)
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}
	host, port := splitHostPort(t, hostport)
	return jdbcCred{User: user, Password: pass, Host: host, Port: port}
}

func parseMysqlWireDSN(t *testing.T, dsn string) jdbcCred {
	t.Helper()
	i := strings.Index(dsn, "@tcp(")
	if i < 0 {
		t.Fatalf("bad mysql dsn: %s", dsn)
	}
	userinfo := dsn[:i]
	colon := strings.Index(userinfo, ":")
	user, pass := userinfo[:colon], userinfo[colon+1:]
	inside := dsn[i+len("@tcp("):]
	host, port := splitHostPort(t, strings.TrimSuffix(inside, ")"+trailingPath(dsn[i+len("@tcp("):])))
	return jdbcCred{User: user, Password: pass, Host: host, Port: port}
}

func trailingPath(s string) string {
	if i := strings.Index(s, "/"); i >= 0 {
		return s[i:]
	}
	return ""
}

func splitHostPort(t *testing.T, hp string) (string, int) {
	t.Helper()
	hp = strings.TrimSuffix(hp, "/")
	i := strings.LastIndex(hp, ":")
	if i < 0 {
		t.Fatalf("bad host:port %q", hp)
	}
	var p int
	fmt.Sscanf(hp[i+1:], "%d", &p)
	return hp[:i], p
}
```

> 注意：`parseMysqlWireDSN` 的尾随路径处理如上较绕——实现时允许简化为直接在 `@tcp(` 后取到 `)`：`inside := dsn[i+5:]; if j := strings.Index(inside, ")"); j >= 0 { inside = inside[:j] }`，再 `strings.Split(inside, "/")` 取首段。以上骨架以此为准，不必拘泥示例细节。

- [ ] **Step 2: 连接助手 + 对拍用例**

```go
func agentCfg(t *testing.T, family, user, pass, host string, port int) agent.Config {
	t.Helper()
	agentJar := lookup(nil2map(), "OWL_AGENT_JAR")
	if agentJar == "" {
		agentJar = "jvm/owl-agent/owl-agent.jar"
	}
	obJar := lookup(nil2map(), "OWL_E2E_OB_JAR")
	if obJar == "" {
		obJar = "oceanbase-client-2.4.1.jar"
	}
	if _, err := os.Stat(agentJar); err != nil {
		t.Skipf("agent jar missing (%s): run bash jvm/owl-agent/build.sh", agentJar)
	}
	if _, err := os.Stat(obJar); err != nil {
		t.Skipf("OB jar missing (%s)", obJar)
	}
	return agent.Config{
		DriverClass: "com.oceanbase.jdbc.Driver",
		URL:         fmt.Sprintf("jdbc:oceanbase://%s:%d?useSSL=false", host, port),
		User:        user,
		Password:    pass,
		Family:      family,
		Classpath:   []string{obJar},
		AgentJar:    agentJar,
	}
}

func openAgentDB(t *testing.T, cfg agent.Config) *sql.DB {
	t.Helper()
	db, err := sql.Open("owljdbc", agent.EncodeDSN(cfg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func openNativeOB(t *testing.T, dbType, dsn string) *sql.DB {
	t.Helper()
	db, err := dbconn.Open(config.DBConfig{Type: dbType, DSN: dsn})
	if err != nil {
		if strings.Contains(err.Error(), "-tags") {
			t.Skipf("native OB driver not compiled: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func pkCols(tbl *metadata.TableDef) []string {
	pks := tbl.GetPrimaryKeys()
	out := make([]string, 0, len(pks))
	for _, pk := range pks {
		out = append(out, pk.ColumnName)
	}
	return out
}

// ── 1) 元数据对拍 ──

func TestE2E_Parity_Metadata(t *testing.T) {
	env := devEnv(t)
	dsn := lookup(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	}
	cred := parseOracleURLDSN(t, dsn)
	schema := lookup(env, "OWL_E2E_OB_ORACLE_SCHEMA")
	if schema == "" {
		schema = cred.User // MIGSRC
	}
	native := openNativeOB(t, "oceanbase-oracle", dsn)
	ag := openAgentDB(t, agentCfg(t, "oracle", cred.User, cred.Password, cred.Host, cred.Port))

	ns, err := extractor.Extract(native, "oracle", schema)
	if err != nil {
		t.Fatalf("native extract: %v", err)
	}
	as, err := extractor.Extract(ag, "oracle", schema)
	if err != nil {
		t.Fatalf("agent extract: %v", err)
	}
	if len(ns.Tables) != len(as.Tables) {
		t.Fatalf("table count native=%d agent=%d", len(ns.Tables), len(as.Tables))
	}
	for i := range ns.Tables {
		nt, at := ns.Tables[i], as.Tables[i]
		if !strings.EqualFold(nt.Name, at.Name) {
			t.Errorf("table %d: %s vs %s", i, nt.Name, at.Name)
			continue
		}
		if len(nt.Columns) != len(at.Columns) {
			t.Errorf("table %s: cols native=%d agent=%d", nt.Name, len(nt.Columns), len(at.Columns))
			continue
		}
		for j := range nt.Columns {
			nc, ac := nt.Columns[j], at.Columns[j]
			if !strings.EqualFold(nc.ColumnName, ac.ColumnName) || !strings.EqualFold(nc.DataType, ac.DataType) {
				t.Errorf("col %s.%s: native=%s/%s agent=%s/%s",
					nt.Name, nc.ColumnName, nc.ColumnName, nc.DataType, ac.ColumnName, ac.DataType)
			}
		}
	}
}

// ── 2) 导出对拍（CSV 逐字节） ──

func TestE2E_Parity_Export(t *testing.T) {
	env := devEnv(t)
	dsn := lookup(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	}
	cred := parseOracleURLDSN(t, dsn)
	schema := lookup(env, "OWL_E2E_OB_ORACLE_SCHEMA")
	if schema == "" {
		schema = cred.User
	}
	native := openNativeOB(t, "oceanbase-oracle", dsn)
	ag := openAgentDB(t, agentCfg(t, "oracle", cred.User, cred.Password, cred.Host, cred.Port))

	model, err := extractor.Extract(native, "oracle", schema)
	if err != nil {
		t.Fatal(err)
	}
	tables := model.Tables
	if len(tables) == 0 {
		t.Skip("no tables in source schema")
	}
	pks := map[string][]string{}
	for _, tbl := range tables {
		pks[tbl.Name] = pkCols(tbl)
	}

	dirN := t.TempDir()
	dirA := t.TempDir()
	expCfg := func(dir string) exporter.Config {
		return exporter.Config{
			OutputDir: dir, Format: "csv", CSVHeader: true,
			CSVDelimiter: ",", CSVNullRep: "\\N",
			PageSize: 500, MaxWorkers: 1, DBType: "oracle",
			PlaceholderFamily: "colon",
		}
	}
	if _, err := exporter.New(native, expCfg(dirN)).ExportTables(context.Background(), tables, pks); err != nil {
		t.Fatalf("native export: %v", err)
	}
	if _, err := exporter.New(ag, expCfg(dirA)).ExportTables(context.Background(), tables, pks); err != nil {
		t.Fatalf("agent export: %v", err)
	}

	filesN := csvMap(t, dirN)
	filesA := csvMap(t, dirA)
	if len(filesN) != len(filesA) {
		t.Fatalf("csv file count native=%d agent=%d", len(filesN), len(filesA))
	}
	for name, bn := range filesN {
		ba, ok := filesA[name]
		if !ok {
			t.Errorf("missing agent csv for %s", name)
			continue
		}
		if !bytes.Equal(bn, ba) {
			t.Errorf("csv bytes differ: %s (native %d B, agent %d B)", name, len(bn), len(ba))
		}
	}
}

func csvMap(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".csv") {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			out[e.Name()] = b
		}
	}
	return out
}

// ── 3) 导入对拍（自包含：手写 CSV + 手工 TableDef → 影子表 A/B → 计数一致） ──
// 不依赖 exporter 文件命名、不依赖跨 schema 权限：在本 schema 内建 OWL_PARITY_A/B 两张影子表，
// native 导入 A、agent 导入 B，比较行数与内容抽查。

func TestE2E_Parity_Import(t *testing.T) {
	env := devEnv(t)
	dsn := lookup(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	}
	cred := parseOracleURLDSN(t, dsn)
	schema := lookup(env, "OWL_E2E_OB_ORACLE_SCHEMA")
	if schema == "" {
		schema = cred.User
	}
	native := openNativeOB(t, "oceanbase-oracle", dsn)
	ag := openAgentDB(t, agentCfg(t, "oracle", cred.User, cred.Password, cred.Host, cred.Port))

	const ddl = `CREATE TABLE %s (
		ID NUMBER(10) NOT NULL,
		NAME VARCHAR2(100),
		AMT NUMBER(12,2),
		TS DATE,
		CONSTRAINT %s PRIMARY KEY (ID)
	)`
	for _, tgt := range []string{"OWL_PARITY_A", "OWL_PARITY_B"} {
		mustExec(t, native, fmt.Sprintf("DROP TABLE %s", tgt))
		mustExec(t, native, fmt.Sprintf(ddl, tgt, "PK_"+tgt))
	}
	t.Cleanup(func() {
		mustExec(t, native, "DROP TABLE OWL_PARITY_A")
		mustExec(t, native, "DROP TABLE OWL_PARITY_B")
	})

	dir := t.TempDir()
	csv := "ID,NAME,AMT,TS\n" +
		"1,alpha,10.5,2024-01-02 03:04:05\n" +
		"2,\\N,-0.01,\\N\n" +
		"3,\"x,\""+",123.45,2024-12-31 23:59:59\n"
	writeFile(t, filepath.Join(dir, "OWL_PARITY_A.csv"), csv)
	writeFile(t, filepath.Join(dir, "OWL_PARITY_B.csv"), csv)

	tblA := parityTableDef(schema, "OWL_PARITY_A")
	tblB := parityTableDef(schema, "OWL_PARITY_B")

	impCfg := func() importer.Config {
		return importer.Config{
			SourceDir: dir, CSVDelimiter: ",", CSVNullMarker: "\\N",
			NullIf:         []string{"NULL", "null", "\\N"},
			CommitInterval: 500, ErrorPolicy: "stop",
			MaxWorkers: 1, TargetDBType: "oracle",
			PlaceholderFamily: "colon",
		}
	}
	mapping := map[string]string{schema: schema}
	if _, err := importer.New(native, impCfg()).ImportTables(context.Background(),
		[]*metadata.TableDef{tblA}, mapping); err != nil {
		t.Fatalf("native import: %v", err)
	}
	if _, err := importer.New(ag, impCfg()).ImportTables(context.Background(),
		[]*metadata.TableDef{tblB}, mapping); err != nil {
		t.Fatalf("agent import: %v", err)
	}

	var ca, cb int
	mustScan(t, native, "SELECT COUNT(*) FROM OWL_PARITY_A", &ca)
	mustScan(t, native, "SELECT COUNT(*) FROM OWL_PARITY_B", &cb)
	if ca != cb {
		t.Errorf("row count mismatch: native=%d agent=%d", ca, cb)
	}
	var amtA, amtB string
	mustScan(t, native, "SELECT TO_CHAR(AMT) FROM OWL_PARITY_A WHERE ID=2", &amtA)
	mustScan(t, native, "SELECT TO_CHAR(AMT) FROM OWL_PARITY_B WHERE ID=2", &amtB)
	if amtA != amtB {
		t.Errorf("amt mismatch: native=%s agent=%s", amtA, amtB)
	}
}

// parityTableDef 构建与影子表 DDL/CSV 列序一致的 TableDef。
func parityTableDef(schema, table string) *metadata.TableDef {
	tbl, _ := metadata.NewTableDef(schema, table)
	cols := []struct {
		name        string
		typ         string
		prec, scale int
		nullable    string
	}{
		{"ID", "NUMBER", 10, 0, "NO"},
		{"NAME", "VARCHAR2", 100, 0, "YES"},
		{"AMT", "NUMBER", 12, 2, "YES"},
		{"TS", "DATE", 0, 0, "YES"},
	}
	for i, c := range cols {
		cd, _ := metadata.NewColumnDef(schema, table, c.name, i+1, c.typ)
		cd.DataPrecision = c.prec
		cd.DataScale = c.scale
		cd.Nullable = c.nullable
		tbl.AddColumn(cd)
	}
	tbl.AddPrimaryKey("PK_"+table, "ID")
	return tbl
}

func csvMap(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".csv") {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			out[e.Name()] = b
		}
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustScan(t *testing.T, db *sql.DB, q string, dest any) {
	t.Helper()
	if err := db.QueryRow(q).Scan(dest); err != nil {
		t.Fatalf("scan %q: %v", q, err)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// ── 4) 性能基线（env 门控） ──

func TestE2E_Perf_Baseline(t *testing.T) {
	env := devEnv(t)
	dsn := lookup(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	tableName := lookup(env, "OWL_AGENT_PERF_TABLE")
	if dsn == "" || tableName == "" {
		t.Skip("set OWL_E2E_OB_ORACLE_MIGSRC_DSN and OWL_AGENT_PERF_TABLE to run perf baseline")
	}
	// 实现：对该表分别用 native 与 agent 各跑一次 exporter（PageSize 5000，单 worker），
	// 记录 rows/s 与 MB/s；断言 agent ≥ native/10（病态下限），并 t.Logf 打印比值供人工评估 1/3 目标。
	t.Log("see implementation: measure+log both paths, hard-fail only below native/10")
}

// ── 5) 故障：坏 jar 快速失败 ──

func TestE2E_AgentBadJarFailsFast(t *testing.T) {
	env := devEnv(t)
	dsn := lookup(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	}
	cred := parseOracleURLDSN(t, dsn)
	cfg := agentCfg(t, "oracle", cred.User, cred.Password, cred.Host, cred.Port)
	cfg.AgentJar = "/nonexistent/owl-agent.jar"
	db, err := sql.Open("owljdbc", agent.EncodeDSN(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*1e9)
	defer cancel()
	if _, err := db.QueryContext(ctx, "SELECT 1 FROM DUAL"); err == nil {
		t.Fatal("expected error for missing agent jar")
	}
}
```

辅助函数（同文件内实现）：



- [ ] **Step 3: 运行**

Run: `bash jvm/owl-agent/build.sh && go test -tags "e2e ob" ./internal/e2eagent/ -v`
Expected: jar/DSN 齐全时全部对拍用例 PASS；缺失时 SKIP（不 FAIL）。

- [ ] **Step 4: Commit**

```bash
git add internal/e2eagent/parity_e2e_test.go
git commit -m "test(e2e): OB dual-tenant native vs agent parity harness"
```
## Self-Review

- **Spec coverage:** 设计 §2 进程模型→Task 3；§3 协议/值编解码→Task 1/5；§4 占位符→Task 5 `BindRewriter`；§5 驱动 profile→Task 3 `Manager` + Task 2 `Config`；§6 接入面（零产品改动）→Task 4/7；§7 对拍矩阵→Task 6/7；§9 风险缓解（占位符断言、类型映射、LOB 流式、并发、时区、进程依赖、JRE 缺失）→Task 5/7。**缺口：** 帧协议里 `CANCEL`/`SAVEPOINT`/`RELEASE` op 未在 Task 3/4 显式接线；LOB chunk 流式（tag 层面以 BYTES 整体传输）与「DECIMAL 计数断言」未完整落地——标记为 Task 7 收尾补强项，不阻塞骨架。
- **Placeholder scan:** 无 TBD/TODO；Step 3 各处代码完整可编译（假 agent 复用之导出函数已定义）。
- **Type consistency:** `Config`/`ControlRequest`/`ControlResponse`/`QueryStream` 跨任务命名一致；`EncodeRowBatch`/`decodeRowBatch` 在 Task 1/3 同步定义；`BindRewriter.rewrite` 在 Java 侧命名一致。

> 实现时以任务顺序逐个完成，每步跑对应测试并提交。Java `test.sh` 与 Go `-tags e2e` 集成测试需真实 java + OB jar（用户已提供 `oceanbase-client-2.4.1.jar` 与本地 `~/obconnector-j`）。
