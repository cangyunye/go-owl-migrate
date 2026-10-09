package ai

import (
	"context"
	"encoding/json"
	"io"
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

// base_url 少了 /v1 后缀是"OpenAI 兼容模式"最常见的接入错误：
// {base}/models 404 后应自动改打 {base}/v1/models 并记忆，报错也带实际 URL。
func TestClientV1PathFallback(t *testing.T) {
	var gotChatPath, gotModelsPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		gotModelsPath = r.URL.Path
		w.Write([]byte(`{"data":[{"id":"m-1"}]}`))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		gotChatPath = r.URL.Path
		w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}],"usage":{}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "k", "m", time.Second)
	models, err := c.ListModels(context.Background())
	if err != nil || len(models) != 1 {
		t.Fatalf("ListModels = %v, %v", models, err)
	}
	rep, err := c.Chat(context.Background(), "", []Message{{Role: "user", Content: "x"}}, Options{})
	if err != nil || rep.Content != "hi" {
		t.Fatalf("Chat = %v, %v", rep, err)
	}
	if gotModelsPath != "/v1/models" || gotChatPath != "/v1/chat/completions" {
		t.Errorf("paths = %q / %q", gotModelsPath, gotChatPath)
	}
	if !strings.HasSuffix(c.EffectiveBase(), "/v1") {
		t.Errorf("EffectiveBase = %q", c.EffectiveBase())
	}
}

// 端点确实不存在时，报错必须带上实际尝试的 URL 与 /v1 回退提示。
func TestClientV1FallbackExhaustedError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "k", "m", time.Second)
	_, err := c.ListModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), srv.URL+"/v1/models") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "/v1") || !strings.Contains(err.Error(), "手填") {
		t.Errorf("err should mention fallback + manual hint: %v", err)
	}
}

// 严格 OpenAI 端点场景：Options 未设置 Effort 时，请求体不得携带 effort 字段。
func TestChatOmitsUnsetEffort(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{}}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "k", "m", time.Second)
	if _, err := c.Chat(context.Background(), "", []Message{{Role: "user", Content: "x"}}, Options{JSONMode: true}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, has := raw["effort"]; has {
		t.Error("request body must not carry effort when Options.Effort is empty")
	}
	if _, has := raw["response_format"]; !has {
		t.Error("JSONMode should add response_format")
	}
}
