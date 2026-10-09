package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// isolatedAIProviderServer: aiSessionsDir 指到唯一子目录，api_key.json 落在
// 各测试专属的父目录里，互不串扰；env key 链清零。
func isolatedAIProviderServer(t *testing.T) *Server {
	t.Helper()
	srv := newTestServerWithDatasources(t, t.TempDir())
	srv.aiSessionsDir = filepath.Join(t.TempDir(), "sessions")
	t.Setenv("OWL_AI_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "")
	return srv
}

func aiProviderGET(t *testing.T, srv *Server) map[string]any {
	t.Helper()
	w := doGet(t, srv, "/api/v1/ai/provider")
	if w.Code != http.StatusOK {
		t.Fatalf("provider GET: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	return resp
}

func TestAIKeyVaultRoundTrip(t *testing.T) {
	srv := isolatedAIProviderServer(t)

	// 初始：无 vault、无 env → 未配置
	st := aiProviderGET(t, srv)
	if st["key_source"] != "none" || st["key_set"] != false {
		t.Fatalf("initial key state = %v/%v", st["key_source"], st["key_set"])
	}

	// 保存 key → vault；响应与所有读路径都不含 key 本体
	w := doJSON(t, srv, "POST", "/api/v1/ai/key", `{"key":"sk-secret-123"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("save key: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sk-secret-123") {
		t.Fatal("save response echoes the key")
	}
	st = aiProviderGET(t, srv)
	if st["key_source"] != "vault" || st["key_set"] != true {
		t.Fatalf("after save: %v/%v", st["key_source"], st["key_set"])
	}
	if strings.Contains(w.Body.String(), "sk-secret-123") {
		t.Fatal("provider response echoes the key")
	}
	cur := doGet(t, srv, "/api/v1/config/current")
	if strings.Contains(cur.Body.String(), "sk-secret-123") {
		t.Fatal("config YAML/JSON contains the key")
	}
	sw := doGet(t, srv, "/api/v1/ai/status")
	if !strings.Contains(sw.Body.String(), `"key_source":"vault"`) {
		t.Errorf("status = %s", sw.Body.String())
	}

	// 重复保存 = 更新（覆盖）
	w = doJSON(t, srv, "POST", "/api/v1/ai/key", `{"key":"sk-rotated"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("rotate key: %d", w.Code)
	}

	// 删除 → 回落（env 无 → none）
	w = doJSON(t, srv, "DELETE", "/api/v1/ai/key", "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete key: %d", w.Code)
	}
	st = aiProviderGET(t, srv)
	if st["key_source"] != "none" || st["key_set"] != false {
		t.Fatalf("after delete: %v/%v", st["key_source"], st["key_set"])
	}
}

func TestAIKeyVaultOverridesEnv(t *testing.T) {
	srv := isolatedAIProviderServer(t)
	t.Setenv("OWL_AI_API_KEY", "env-key")

	// env 先在 → env
	st := aiProviderGET(t, srv)
	if st["key_source"] != "env" {
		t.Fatalf("env-only source = %v", st["key_source"])
	}
	// vault 保存后优先于 env；删除后回落 env
	doJSON(t, srv, "POST", "/api/v1/ai/key", `{"key":"vault-key"}`)
	if st = aiProviderGET(t, srv); st["key_source"] != "vault" {
		t.Fatalf("vault source = %v", st["key_source"])
	}
	doJSON(t, srv, "DELETE", "/api/v1/ai/key", "")
	if st = aiProviderGET(t, srv); st["key_source"] != "env" {
		t.Fatalf("fallback source = %v", st["key_source"])
	}
}

func TestAIUpdateProvider(t *testing.T) {
	srv := isolatedAIProviderServer(t)

	// custom + 自填 base_url + model
	w := doJSON(t, srv, "PUT", "/api/v1/ai/provider",
		`{"provider":"custom","base_url":"http://127.0.0.1:9/v1","model":"my-model"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update provider: %d %s", w.Code, w.Body.String())
	}
	st := aiProviderGET(t, srv)
	if st["provider"] != "custom" || st["base_url"] != "http://127.0.0.1:9/v1" || st["model"] != "my-model" {
		t.Fatalf("after PUT = %v/%v/%v", st["provider"], st["base_url"], st["model"])
	}

	// 预设切换：base_url 未填时吃预设默认
	w = doJSON(t, srv, "PUT", "/api/v1/ai/provider", `{"provider":"moonshot"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("preset switch: %d %s", w.Code, w.Body.String())
	}
	st = aiProviderGET(t, srv)
	if st["base_url"] != "https://api.moonshot.cn/v1" {
		t.Fatalf("preset base_url = %v", st["base_url"])
	}

	// 高级参数可更新且校验
	if w = doJSON(t, srv, "PUT", "/api/v1/ai/provider", `{"effort":"turbo"}`); w.Code != http.StatusBadRequest {
		t.Errorf("bad effort = %d, want 400", w.Code)
	}
	if w = doJSON(t, srv, "PUT", "/api/v1/ai/provider", `{"base_url":"ftp://x"}`); w.Code != http.StatusBadRequest {
		t.Errorf("bad base_url = %d, want 400", w.Code)
	}
	if w = doJSON(t, srv, "PUT", "/api/v1/ai/provider", `{"max_tokens":8192,"timeout":"3m"}`); w.Code != http.StatusOK {
		t.Errorf("advanced params = %d %s", w.Code, w.Body.String())
	}
	if st = aiProviderGET(t, srv); st["max_tokens"] != float64(8192) || st["timeout"] != "3m0s" {
		t.Errorf("advanced round-trip = %v/%v", st["max_tokens"], st["timeout"])
	}

	// 未知供应商拒绝
	if w = doJSON(t, srv, "PUT", "/api/v1/ai/provider", `{"provider":"anthropic"}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown provider = %d, want 400", w.Code)
	}
}

// aiModelsVendor mocks the OpenAI /models listing.
func aiModelsVendor(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer vk-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"m-b"},{"id":"m-a"},{"id":"m-a"}]}`))
	}))
	t.Cleanup(srv.Close)
	_ = status
	return srv
}

func TestAIModelsProbe(t *testing.T) {
	srv := isolatedAIProviderServer(t)
	vendor := aiModelsVendor(t, 200)

	// 未设 key → 503 提示先保存
	w := doJSON(t, srv, "POST", "/api/v1/ai/models", `{"base_url":"`+vendor.URL+`"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no-key probe = %d, want 503", w.Code)
	}
	doJSON(t, srv, "POST", "/api/v1/ai/key", `{"key":"vk-1"}`)

	w = doJSON(t, srv, "POST", "/api/v1/ai/models", `{"base_url":"`+vendor.URL+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("probe: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Models []string `json:"models"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Models) != 2 || resp.Models[0] != "m-a" || resp.Models[1] != "m-b" {
		t.Errorf("models = %v (want sorted deduped-ish [m-a m-b])", resp.Models)
	}

	// base_url 缺省时用当前配置
	doJSON(t, srv, "PUT", "/api/v1/ai/provider", `{"base_url":"`+vendor.URL+`"}`)
	w = doJSON(t, srv, "POST", "/api/v1/ai/models", `{}`)
	if w.Code != http.StatusOK {
		t.Errorf("default base_url probe = %d %s", w.Code, w.Body.String())
	}
}

func TestAITestProbe(t *testing.T) {
	srv := isolatedAIProviderServer(t)
	doJSON(t, srv, "POST", "/api/v1/ai/key", `{"key":"vk-1"}`)

	// 兼容端点：接受 response_format → ok
	okVendor := fakeAIVendor(t, `{"ok":true}`)
	w := doJSON(t, srv, "POST", "/api/v1/ai/test",
		`{"base_url":"`+okVendor.URL+`","model":"m1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("test: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Model string `json:"model"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.OK || resp.Model != "m1" {
		t.Errorf("resp = %+v", resp)
	}

	// 不支持 JSON 模式的端点 → ok:false + 指向 response_format 的提示
	badVendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"Unknown field: response_format"}}`))
	}))
	t.Cleanup(badVendor.Close)
	w = doJSON(t, srv, "POST", "/api/v1/ai/test",
		`{"base_url":"`+badVendor.URL+`","model":"m1"}`)
	if !strings.Contains(w.Body.String(), "JSON 模式") {
		t.Errorf("json-mode hint missing: %s", w.Body.String())
	}
}

// 输入框里已粘贴但未保存的 Key 优先于（可能过期的）环境变量 Key——
// 用户心智是"粘贴 → 直接测试"，不该被旧 env key 挡住。
func TestAIModelsInputKeyPriority(t *testing.T) {
	srv := isolatedAIProviderServer(t)
	t.Setenv("OWL_AI_API_KEY", "stale-key")
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh-key" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"message":"Authentication Fails"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"m-1"}]}`))
	}))
	t.Cleanup(vendor.Close)

	// 不带 key（用 env 的 stale-key）→ 供应商 401 → 502 透出
	w := doJSON(t, srv, "POST", "/api/v1/ai/models", `{"base_url":"`+vendor.URL+`"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("stale env key probe = %d, want 502", w.Code)
	}
	// 带 input key → 成功
	w = doJSON(t, srv, "POST", "/api/v1/ai/models",
		`{"base_url":"`+vendor.URL+`","key":"fresh-key"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("input key probe = %d %s", w.Code, w.Body.String())
	}
}
