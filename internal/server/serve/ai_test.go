package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAIVendor stands in for the OpenAI-compatible vendor: it echoes a fixed
// routing JSON so handler tests stay deterministic and offline.
func fakeAIVendor(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"content": content},
			}},
			"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAIStatusDisabled(t *testing.T) {
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "")

	w := doGet(t, srv, "/api/v1/ai/status")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["enabled"] != false || resp["key_set"] != false {
		t.Errorf("resp = %v", resp)
	}
	if resp["model"] != "deepseek-flash" {
		t.Errorf("model default = %v", resp["model"])
	}
}

func TestAIStatusEnabled(t *testing.T) {
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "test-key")

	w := doGet(t, srv, "/api/v1/ai/status")
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["enabled"] != true || resp["provider"] != "deepseek" {
		t.Errorf("resp = %v", resp)
	}
}

func TestAIRouteDisabled503(t *testing.T) {
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "")

	w := doJSON(t, srv, "POST", "/api/v1/ai/route", `{"utterance":"把 scott 的表迁到 pg"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestAIRouteBadRequest(t *testing.T) {
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "test-key")
	w := doJSON(t, srv, "POST", "/api/v1/ai/route", `{"utterance":"  "}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAIRouteHappyPath(t *testing.T) {
	vendor := fakeAIVendor(t, `{"route":"migrate","sub":"","confidence":"high","missing_slots":[],"out_of_scope":false,"needs_clarify":false,"reason":"迁移意图"}`)
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "test-key")
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	w := doJSON(t, srv, "POST", "/api/v1/ai/route",
		`{"utterance":"把 oracle scott 的 emp 表迁到 postgres","context":["上一句"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK     bool `json:"ok"`
		Route  string `json:"route"`
		Result struct {
			Reason string `json:"reason"`
		} `json:"result"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK || resp.Route != "migrate" || resp.Result.Reason != "迁移意图" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage.PromptTokens != 100 || resp.Model != "deepseek-flash" {
		t.Errorf("usage/model = %+v", resp)
	}
}

func TestAIRouteVendorGarbage(t *testing.T) {
	vendor := fakeAIVendor(t, "这不是 JSON")
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "test-key")
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	w := doJSON(t, srv, "POST", "/api/v1/ai/route", `{"utterance":"x"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	if !strings.Contains(w.Body.String(), "JSON") {
		t.Errorf("body = %s", w.Body.String())
	}
}
