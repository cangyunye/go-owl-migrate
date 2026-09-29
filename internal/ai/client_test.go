package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeVendor(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "test-key", "test-model", 5*time.Second)
}

func TestChatRequestShape(t *testing.T) {
	var gotBody chatRequest
	var gotAuth string
	c := fakeVendor(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"route\":\"migrate\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	})

	reply, err := c.Chat(context.Background(), "sys-prompt",
		[]Message{{Role: "user", Content: "hi"}},
		Options{JSONMode: true, Effort: "low", MaxTokens: 32768})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotBody.Model != "test-model" || gotBody.MaxTokens != 32768 || gotBody.Effort != "low" {
		t.Errorf("request body = %+v", gotBody)
	}
	if gotBody.ResponseFormat == nil || gotBody.ResponseFormat.Type != "json_object" {
		t.Errorf("response_format missing: %+v", gotBody.ResponseFormat)
	}
	if len(gotBody.Messages) != 2 || gotBody.Messages[0].Role != "system" {
		t.Errorf("messages = %+v", gotBody.Messages)
	}
	if reply.Content != `{"route":"migrate"}` || reply.Usage.CompletionTokens != 5 {
		t.Errorf("reply = %+v", reply)
	}
}

func TestChatOmitsOptionalFields(t *testing.T) {
	var gotBody chatRequest
	c := fakeVendor(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	})
	if _, err := c.Chat(context.Background(), "", []Message{{Role: "user", Content: "x"}}, Options{}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotBody.Effort != "" || gotBody.MaxTokens != 0 || gotBody.ResponseFormat != nil {
		t.Errorf("optional fields should be omitted: %+v", gotBody)
	}
}

func TestChatRetriesThenSucceeds(t *testing.T) {
	calls := 0
	c := fakeVendor(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{"error":{"message":"upstream"}}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"fine"}}]}`))
	})
	reply, err := c.Chat(context.Background(), "", []Message{{Role: "user", Content: "x"}}, Options{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if calls != 2 || reply.Content != "fine" {
		t.Errorf("calls=%d reply=%q", calls, reply.Content)
	}
}

func TestChatErrorEnvelope(t *testing.T) {
	c := fakeVendor(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Authentication Fails","type":"authentication_error"}}`))
	})
	_, err := c.Chat(context.Background(), "", []Message{{Role: "user", Content: "x"}}, Options{})
	if err == nil || !strings.Contains(err.Error(), "Authentication Fails") {
		t.Fatalf("err = %v, want vendor message", err)
	}
	// 401 不可重试：只调一次的行为由 retryable=false 保证（此处难以断言次数，
	// 至少确认错误信息未被吞掉）。
}

func TestExtractJSONToleratesFences(t *testing.T) {
	raw := "好的，结果如下：\n```json\n{\"route\":\"migrate\",\"reason\":\"x{y}z\"}\n```\n以上。"
	got, err := ExtractJSON(raw)
	if err != nil {
		t.Fatalf("ExtractJSON: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["route"] != "migrate" {
		t.Errorf("route = %v", m["route"])
	}
	if _, err := ExtractJSON("完全没有 JSON"); err == nil {
		t.Error("want error for no-json reply")
	}
}

// TestPromptParity keeps the embedded prompt byte-identical with the eval
// corpus harness copy, so serve routing and evals measure the same contract.
func TestPromptParity(t *testing.T) {
	path := filepath.Join("..", "..", "evals", "ai-router", "router_system.md")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("eval corpus not present: %v", err)
	}
	if RouterSystemPrompt != string(want) {
		t.Error("internal/ai/prompt_router.md 与 evals/ai-router/router_system.md 不一致；请同步后一并提交")
	}
	if !strings.Contains(RouterSystemPrompt, "out-of-scope") {
		t.Error("prompt 缺少路由词表")
	}
}
