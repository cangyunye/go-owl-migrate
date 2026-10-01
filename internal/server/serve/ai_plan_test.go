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

var testSlotsJSON = `{"scenario":"export","metadata":"database","source":{"type":"mysql","host":"127.0.0.1","port":"3306","user":"root","password":"__PWD_mysql__","database":"owl_demo","schema":"owl_demo"},"export":{"format":"csv"}}`

var testSlotsYAMLJSON = strings.Replace(testSlotsJSON, `"format":"csv"`, `"format":"xlsx"`, 1)

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
		testSlotsJSON,
		`{"route":"export-data","sub":"","reason":"再导"}`,
		testSlotsJSON,
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
		Engine      string `json:"engine"`
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
	if resp.Engine != "builder" {
		t.Errorf("engine = %q, want builder", resp.Engine)
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
		`{"scenario":"export","nonsense":true}`, // 槽位缺 source → builder 失败 → 回退
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
	if *calls != 4 {
		t.Fatalf("vendor calls = %d, want 4 (route + slots + fallback bad + repair)", *calls)
	}
	var resp struct {
		RepairRounds int    `json:"repair_rounds"`
		Engine       string `json:"engine"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.RepairRounds != 1 {
		t.Errorf("repair_rounds = %d, want 1", resp.RepairRounds)
	}
	if resp.Engine != "llm-fallback" {
		t.Errorf("engine = %q, want llm-fallback", resp.Engine)
	}
}

// TestAIPlanSessionContinuation: turn 2 with the same session_id keeps slots;
// the plan prompt must carry the round-1 slots so "再导一份 xlsx" resolves.
func TestAIPlanSessionContinuation(t *testing.T) {
	vendor, calls, bodies := seqVendor(t, []string{
		`{"route":"export-data","sub":"","reason":"导出"}`,
		testSlotsJSON,
		`{"route":"export-data","sub":"format:xlsx","reason":"续轮换格式"}`,
		testSlotsYAMLJSON,
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
		testSlotsJSON,
		`{"route":"migrate","sub":"","reason":"迁移"}`,
		`{"scenario":"migrate","source":{"type":"mysql","host":"127.0.0.1","port":"3306","user":"root","database":"owl_demo","schema":"owl_demo"},"target":{"type":"postgres","host":"127.0.0.1","port":"5432","user":"postgres","database":"app"},"ddl":{"schema_mapping":{"owl_demo":"public"}}}`,
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

// TestAIPlanConfirmActivateAndExecute: plan → confirm (activate only) →
// confirm with execute (launchJob; master unavailable in unit tests → the
// activation must have happened and the failure must be a clean 503).
func TestAIPlanConfirmActivateAndExecute(t *testing.T) {
	vendor, _, _ := seqVendor(t, []string{
		`{"route":"export-data","sub":"","reason":"导出"}`,
		testSlotsJSON,
	})
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "test-key")
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	w := doJSON(t, srv, "POST", "/api/v1/ai/plan",
		`{"utterance":"导出 mysql owl_demo users csv","credentials":{"__PWD_mysql__":"root123456"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("plan status = %d, body=%s", w.Code, w.Body.String())
	}
	var pr struct {
		SessionID string `json:"session_id"`
		PlanID    string `json:"plan_id"`
		Engine    string `json:"engine"`
		YAML      string `json:"yaml"`
	}
	json.Unmarshal(w.Body.Bytes(), &pr)
	if pr.Engine != "builder" {
		t.Fatalf("engine = %q", pr.Engine)
	}

	// confirm：只激活
	w2 := doJSON(t, srv, "POST", "/api/v1/ai/plan/confirm",
		`{"session_id":"`+pr.SessionID+`","plan_id":"`+pr.PlanID+`","execute":false}`)
	if w2.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body=%s", w2.Code, w2.Body.String())
	}
	var cr struct {
		Activated bool   `json:"activated"`
		JobType   string `json:"job_type"`
		Next      string `json:"next"`
	}
	json.Unmarshal(w2.Body.Bytes(), &cr)
	if !cr.Activated || cr.JobType != "export" {
		t.Errorf("confirm resp = %+v", cr)
	}
	// 激活后服务端活动配置已被替换
	srv.mu.RLock()
	got := srv.cfg.Export.Format
	srv.mu.RUnlock()
	if got != "csv" {
		t.Errorf("active config format = %q", got)
	}
	// 已激活会话再次 confirm 同一 plan → 阶段不再是 confirming → 409
	w3 := doJSON(t, srv, "POST", "/api/v1/ai/plan/confirm",
		`{"session_id":"`+pr.SessionID+`","plan_id":"`+pr.PlanID+`","execute":false}`)
	if w3.Code != http.StatusConflict {
		t.Errorf("re-confirm status = %d, want 409", w3.Code)
	}

	// execute=true（单测无 master IPC → 503，但验证路径走到启动）
	var sess2 struct {
		SessionID string `json:"session_id"`
		PlanID    string `json:"plan_id"`
		Route     string `json:"route"`
		Session   struct {
			Intent string `json:"intent"`
		} `json:"session"`
	}
	// 重新生成一个 plan 用于 execute 分支（激活后指向全新 vendor，排除
	// keep-alive 连接被上一段流程破坏的测试基建干扰）
	vendor2, _, _ := seqVendor(t, []string{
		`{"route":"export-data","sub":"","reason":"再导"}`,
		testSlotsJSON,
	})
	srv.mu.Lock()
	srv.cfg.AI.BaseURL = vendor2.URL
	srv.mu.Unlock()
	w4 := doJSON(t, srv, "POST", "/api/v1/ai/plan", `{"utterance":"再导一份"}`)
	if w4.Code != http.StatusOK {
		t.Fatalf("plan2 status = %d, body=%s", w4.Code, w4.Body.String())
	}
	json.Unmarshal(w4.Body.Bytes(), &sess2)
	w5 := doJSON(t, srv, "POST", "/api/v1/ai/plan/confirm",
		`{"session_id":"`+sess2.SessionID+`","plan_id":"`+sess2.PlanID+`","execute":true}`)
	if w5.Code != http.StatusServiceUnavailable {
		t.Fatalf("execute status = %d, want 503 (no master IPC in tests), body=%s", w5.Code, w5.Body.String())
	}
	if !strings.Contains(w5.Body.String(), "master IPC") {
		t.Errorf("body = %s", w5.Body.String())
	}
}

// TestAIPlanConfirmUnknownPlan: nonexistent plan id → 404.
func TestAIPlanConfirmUnknownPlan(t *testing.T) {
	srv := newTestServer(t)
	t.Setenv("OWL_AI_API_KEY", "test-key")
	w := doJSON(t, srv, "POST", "/api/v1/ai/plan/confirm",
		`{"session_id":"s-none","plan_id":"p-none","execute":false}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
