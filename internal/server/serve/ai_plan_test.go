package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// seqVendor returns a vendor whose replies come from a scripted list, so a
// plan request's internal call sequence (route → draft → repair…) is
// deterministic. Each item also records the request body for assertions.
func seqVendor(t *testing.T, replies []string) (*httptest.Server, *int, *[]string) {
	t.Helper()
	calls := 0
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&env)
		last := ""
		if len(env.Messages) > 0 {
			last = env.Messages[len(env.Messages)-1].Content
		}
		bodies = append(bodies, last)
		idx := calls
		calls++
		if idx >= len(replies) {
			idx = len(replies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": replies[idx]}}},
			"usage":   map[string]any{"prompt_tokens": 50, "completion_tokens": 10},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, &bodies
}

const goodPlanYAML = `metadata:
  type: database
ddl:
  target_dialect: postgres
source:
  type: mysql
  dsn: "root:__PWD_mysql__@tcp(127.0.0.1:3306)/owl_demo"
  schema: owl_demo
export:
  format: csv
`

// TestAIPlanDisabled503: no key → 503, no vendor call.
func TestAIPlanDisabled503(t *testing.T) {
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "")
	w := doJSON(t, srv, "POST", "/api/v1/ai/plan", `{"utterance":"导出 owl_demo.users 为 csv"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

// TestAIPlanHappyPath: route says export-data, draft is valid after credential
// injection; response carries a masked yaml, session in confirming stage.
func TestAIPlanHappyPath(t *testing.T) {
	vendor, calls, bodies := seqVendor(t, []string{
		`{"route":"export-data","sub":"","confidence":"high","missing_slots":[],"out_of_scope":false,"needs_clarify":false,"reason":"导出意图"}`,
		goodPlanYAML,
	})
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "test-key")
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	w := doJSON(t, srv, "POST", "/api/v1/ai/plan",
		`{"utterance":"导出 mysql owl_demo 库 users 表为 csv，root 密码见占位符","credentials":{"__PWD_mysql__":"root123456"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if *calls != 2 {
		t.Fatalf("vendor calls = %d, want 2 (route + draft)", *calls)
	}
	// 生成段的消息必须带占位符协议提示（system 已嵌，user 里是槽位+话语）
	if !strings.Contains((*bodies)[1], "【用户最新一句话】") {
		t.Errorf("plan user message = %.200s", (*bodies)[1])
	}

	var resp struct {
		OK          bool   `json:"ok"`
		SessionID   string `json:"session_id"`
		PlanID      string `json:"plan_id"`
		YAML        string `json:"yaml"`
		RepairRounds int   `json:"repair_rounds"`
		Continuity  map[string]any `json:"continuity"`
		Session     struct {
			Stage string `json:"stage"`
			Slots map[string]string `json:"slots"`
		} `json:"session"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK || resp.SessionID == "" || resp.PlanID == "" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Session.Stage != "confirming" {
		t.Errorf("stage = %s", resp.Session.Stage)
	}
	if resp.Session.Slots["source_type"] != "mysql" || resp.Session.Slots["format"] != "csv" {
		t.Errorf("slots = %+v", resp.Session.Slots)
	}
	// 凭据已注入且被脱敏：真实密码绝不出现在响应里
	if strings.Contains(resp.YAML, "root123456") || strings.Contains(resp.YAML, "__PWD_") {
		t.Errorf("yaml leaks credential: %s", resp.YAML)
	}
	if !strings.Contains(resp.YAML, "******") {
		t.Errorf("yaml should be masked: %s", resp.YAML)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("warnings = %v", resp.Warnings)
	}
	if resp.Continuity["mode"] != "new" {
		t.Errorf("continuity = %v", resp.Continuity)
	}
}

// TestAIPlanRepairLoop: the first draft fails config.Load, the repaired second
// draft passes — the endpoint must converge without changing the intent.
func TestAIPlanRepairLoop(t *testing.T) {
	badYAML := strings.Replace(goodPlanYAML, "  type: database\n", "", 1) // 丢 metadata.type
	vendor, calls, _ := seqVendor(t, []string{
		`{"route":"export-data","sub":"","reason":"导出"}`,
		badYAML,
		goodPlanYAML, // 修复轮
	})
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "test-key")
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	w := doJSON(t, srv, "POST", "/api/v1/ai/plan", `{"utterance":"导出 owl_demo.users csv"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if *calls != 3 {
		t.Fatalf("vendor calls = %d, want 3 (route + bad draft + repair)", *calls)
	}
	var resp struct {
		RepairRounds int `json:"repair_rounds"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.RepairRounds != 1 {
		t.Errorf("repair_rounds = %d, want 1", resp.RepairRounds)
	}
}

// TestAIPlanSessionContinuation: turn 2 with the same session_id keeps slots;
// the plan prompt must carry the round-1 slots so "再导一份 xlsx" resolves.
func TestAIPlanSessionContinuation(t *testing.T) {
	vendor, calls, bodies := seqVendor(t, []string{
		`{"route":"export-data","sub":"","reason":"导出"}`,
		goodPlanYAML,
		`{"route":"export-data","sub":"format:xlsx","reason":"续轮换格式"}`,
		strings.Replace(goodPlanYAML, "format: csv", "format: xlsx", 1),
	})
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "test-key")
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	// 第 1 轮
	w1 := doJSON(t, srv, "POST", "/api/v1/ai/plan", `{"utterance":"导出 owl_demo.users csv"}`)
	var r1 struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(w1.Body.Bytes(), &r1)
	if r1.SessionID == "" {
		t.Fatal("no session id from turn 1")
	}

	// 第 2 轮：同会话，只说换格式
	w2 := doJSON(t, srv, "POST", "/api/v1/ai/plan",
		`{"utterance":"再导一份 xlsx 格式的","session_id":"`+r1.SessionID+`"}`)
	if w2.Code != http.StatusOK {
		t.Fatalf("turn2 status = %d, body=%s", w2.Code, w2.Body.String())
	}
	// calls: 轮1(route+plan) + 轮2(route+plan) = 4
	if *calls != 4 {
		t.Fatalf("vendor calls = %d, want 4", *calls)
	}
	// 轮 2 的生成消息（第 4 次调用）必须携带第 1 轮的槽位
	if !strings.Contains((*bodies)[3], "已知槽位") || !strings.Contains((*bodies)[3], "owl_demo") {
		t.Errorf("turn2 plan message missing inherited slots: %.300s", (*bodies)[3])
	}
	var r2 struct {
		Continuity map[string]any `json:"continuity"`
		YAML       string         `json:"yaml"`
		Session    struct {
			Slots map[string]string `json:"slots"`
		} `json:"session"`
	}
	json.Unmarshal(w2.Body.Bytes(), &r2)
	if r2.Continuity["mode"] != "continued" {
		t.Errorf("continuity = %v", r2.Continuity)
	}
	if r2.Session.Slots["format"] != "xlsx" {
		t.Errorf("format slot should update to xlsx: %+v", r2.Session.Slots)
	}
}

// TestAIPlanIntentSwitchStartsNewRound: same session, different intent → new
// round session cloned from old slots, old session archived untouched.
func TestAIPlanIntentSwitchStartsNewRound(t *testing.T) {
	routeReply := `{"route":"%s","sub":"","reason":"x"}`
	_ = routeReply
	vendor, calls, _ := seqVendor(t, []string{
		`{"route":"export-data","sub":"","reason":"导出"}`,
		goodPlanYAML,
		`{"route":"migrate","sub":"","reason":"迁移"}`,
		goodPlanYAML,
	})
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "test-key")
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	w1 := doJSON(t, srv, "POST", "/api/v1/ai/plan", `{"utterance":"导出 owl_demo.users csv"}`)
	var r1 struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(w1.Body.Bytes(), &r1)

	w2 := doJSON(t, srv, "POST", "/api/v1/ai/plan",
		`{"utterance":"把这些表迁到 postgres","session_id":"`+r1.SessionID+`"}`)
	if w2.Code != http.StatusOK {
		t.Fatalf("turn2 status = %d, body=%s", w2.Code, w2.Body.String())
	}
	if *calls != 4 {
		t.Fatalf("vendor calls = %d, want 4", *calls)
	}
	var r2 struct {
		SessionID  string         `json:"session_id"`
		Continuity map[string]any `json:"continuity"`
	}
	json.Unmarshal(w2.Body.Bytes(), &r2)
	if r2.SessionID == "" || r2.SessionID == r1.SessionID {
		t.Errorf("intent switch must start a new session: %s vs %s", r2.SessionID, r1.SessionID)
	}
	if r2.Continuity["mode"] != "new_round" || r2.Continuity["from_session"] != r1.SessionID {
		t.Errorf("continuity = %v", r2.Continuity)
	}
}

// TestAIPlanClarifyNoPlan: ambiguous utterance → no plan generated, decision
// recorded in session, needs_clarify surfaced.
func TestAIPlanClarifyNoPlan(t *testing.T) {
	vendor, calls, _ := seqVendor(t, []string{
		`{"route":"clarify","sub":"","missing_slots":["导出数据还是结构"],"needs_clarify":true,"reason":"『导出』歧义"}`,
	})
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "test-key")
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	w := doJSON(t, srv, "POST", "/api/v1/ai/plan", `{"utterance":"帮我把这个库导出一下"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if *calls != 1 {
		t.Fatalf("vendor calls = %d, want 1 (no plan call)", *calls)
	}
	var resp struct {
		Plan   *map[string]any `json:"plan"`
		Result aiRouteResult   `json:"result"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Plan != nil {
		t.Errorf("clarify must not produce a plan")
	}
	if !resp.Result.NeedsClarify {
		t.Errorf("result = %+v", resp.Result)
	}
}
