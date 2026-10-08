package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ── Step 3：会话历史 / 恢复 / 复用端点（红测试先行） ──

// seedPlanSession 走一次完整 plan（带档案），返回 session/plan id 供各端点测试用。
func seedPlanSession(t *testing.T, srv *Server) (sessionID, planID string) {
	t.Helper()
	vendor, _, _ := seqVendor(t, []string{
		`{"route":"export-data","sub":"format:csv","confidence":"high","missing_slots":[],"out_of_scope":false,"needs_clarify":false,"reason":"导出意图"}`,
		`{"scenario":"export","metadata":"database","source":{"profile":"oracle-scott","schema":"SCOTT"},"export":{"format":"csv","tables":["emp"]}}`,
	})
	t.Setenv("OWL_AI_API_KEY", "test-key")
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	w := doJSON(t, srv, "POST", "/api/v1/ai/plan", `{"utterance":"用 oracle-scott 档案导出 emp 表为 csv"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		SessionID string `json:"session_id"`
		PlanID    string `json:"plan_id"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	return resp.SessionID, resp.PlanID
}

func TestListSessionsEndpoint(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	store, _ := srv.dsStore()
	store.Put("oracle-scott", "oracle", "SCOTT", "oracle://scott:pw@127.0.0.1:1521/XEPDB1", "")
	seedPlanSession(t, srv)

	w := doJSON(t, srv, "GET", "/api/v1/ai/sessions", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	var items []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Stage  string `json:"stage"`
		Effect bool   `json:"effective"`
	}
	json.Unmarshal(w.Body.Bytes(), &items)
	if len(items) != 1 || items[0].Title == "" || items[0].Stage != "confirming" {
		t.Fatalf("list = %+v", items)
	}
	// q 过滤：命中
	w = doJSON(t, srv, "GET", "/api/v1/ai/sessions?q=oracle", "")
	json.Unmarshal(w.Body.Bytes(), &items)
	if len(items) != 1 {
		t.Errorf("q=oracle: %d items", len(items))
	}
	// q 过滤：不命中
	w = doJSON(t, srv, "GET", "/api/v1/ai/sessions?q=不存在的词", "")
	json.Unmarshal(w.Body.Bytes(), &items)
	if len(items) != 0 {
		t.Errorf("q=miss: %d items", len(items))
	}
}

func TestGetSessionEndpoint(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	store, _ := srv.dsStore()
	store.Put("oracle-scott", "oracle", "SCOTT", "oracle://scott:pw@127.0.0.1:1521/XEPDB1", "")
	sid, pid := seedPlanSession(t, srv)

	w := doJSON(t, srv, "GET", "/api/v1/ai/session/"+sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d", w.Code)
	}
	var resp struct {
		Session struct {
			ID    string `json:"session_id"`
			Stage string `json:"stage"`
		} `json:"session"`
		LastPlan struct {
			PlanID   string   `json:"plan_id"`
			YAML     string   `json:"yaml"`
			CredSlot []string `json:"credential_slots"`
		} `json:"last_plan"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Session.ID != sid || resp.Session.Stage != "confirming" {
		t.Errorf("session = %+v", resp.Session)
	}
	if resp.LastPlan.PlanID != pid || !strings.Contains(resp.LastPlan.YAML, "__PWD_oracle__") {
		t.Errorf("last_plan = %+v", resp.LastPlan)
	}
	if strings.Contains(resp.LastPlan.YAML, "pw@") {
		t.Error("restored yaml leaks the profile password")
	}
	// 档案哨兵 DSN 不算 credential slot
	for _, s := range resp.LastPlan.CredSlot {
		if s == "__PWD_oracle__" {
			t.Errorf("profile sentinel must be excluded: %v", resp.LastPlan.CredSlot)
		}
	}
	// 不存在
	if w := doJSON(t, srv, "GET", "/api/v1/ai/session/s-nope", ""); w.Code != http.StatusNotFound {
		t.Errorf("missing session: %d", w.Code)
	}
}

func TestCloneSessionEndpoint(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	store, _ := srv.dsStore()
	store.Put("oracle-scott", "oracle", "SCOTT", "oracle://scott:pw@127.0.0.1:1521/XEPDB1", "")
	sid, _ := seedPlanSession(t, srv)

	w := doJSON(t, srv, "POST", "/api/v1/ai/session/"+sid+"/clone", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("clone: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		SessionID string `json:"session_id"`
		From      string `json:"from_session"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.SessionID == "" || resp.SessionID == sid || resp.From != sid {
		t.Fatalf("clone resp = %+v", resp)
	}
	// 源未有效 → 被淘汰；按 id 读取视为不存在，默认列表不含它
	src, _ := srv.aiSessions()
	if _, ok := src.Get(sid); ok {
		t.Errorf("origin should be discarded (invisible by id after fork)")
	}
	lw := doJSON(t, srv, "GET", "/api/v1/ai/sessions", "")
	var items []map[string]any
	json.Unmarshal(lw.Body.Bytes(), &items)
	for _, it := range items {
		if it["id"] == sid {
			t.Errorf("discarded origin in default list")
		}
	}
	// 克隆继承槽位
	clone, _ := src.Get(resp.SessionID)
	if clone.Slots["source_profile"] != "oracle-scott" {
		t.Errorf("clone slots = %+v", clone.Slots)
	}
}

func TestApplySessionEndpoint(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	store, _ := srv.dsStore()
	store.Put("oracle-scott", "oracle", "SCOTT", "oracle://scott:RealPw@127.0.0.1:1521/XEPDB1", "")

	// 无 config 产物 → 400
	sid, pid := seedPlanSession(t, srv)
	if w := doJSON(t, srv, "POST", "/api/v1/ai/session/"+sid+"/apply", `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("apply without config artifact: %d %s", w.Code, w.Body.String())
	}

	// confirm(execute=true)（假 master 接受启动）→ 加密 config 产物 +
	// effective → apply 激活
	srv.masterURL = fakeMaster(t).URL
	wc := doJSON(t, srv, "POST", "/api/v1/ai/plan/confirm",
		`{"session_id":"`+sid+`","plan_id":"`+pid+`","execute":true}`)
	if wc.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", wc.Code, wc.Body.String())
	}

	// 会话存储不得含明文密码
	store2, _ := srv.aiSessions()
	got, _ := store2.Get(sid)
	for _, a := range got.Artifacts {
		if strings.Contains(a.YAML, "RealPw") {
			t.Fatal("session artifact leaks plaintext password")
		}
	}
	if !got.Effective {
		t.Error("session must be effective after execute=true")
	}

	wa := doJSON(t, srv, "POST", "/api/v1/ai/session/"+sid+"/apply", `{}`)
	if wa.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", wa.Code, wa.Body.String())
	}
	cur := doJSON(t, srv, "GET", "/api/v1/config/current", "")
	if !strings.Contains(cur.Body.String(), "oracle://scott:RealPw@127.0.0.1:1521/XEPDB1") &&
		!strings.Contains(cur.Body.String(), "127.0.0.1:1521") {
		t.Errorf("activated config should reflect the session's resolved DSN: %.200s", cur.Body.String())
	}
}

func TestApplySessionWithUserPasswordBlocked(t *testing.T) {
	// 手填密码（非档案）的计划：草案带未注入哨兵 → apply 必须拒绝（不能激活
	// 一份带哨兵的残缺配置）。
	srv := newTestServer(t)
	vendor, _, _ := seqVendor(t, []string{
		`{"route":"export-data","sub":"","confidence":"high","missing_slots":[],"out_of_scope":false,"needs_clarify":false,"reason":"导出"}`,
		`{"scenario":"export","metadata":"database","source":{"type":"mysql","host":"127.0.0.1","port":"3306","user":"root","password":"__PWD_mysql__","database":"shop"},"export":{"format":"csv"}}`,
	})
	t.Setenv("OWL_AI_API_KEY", "test-key")
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	w := doJSON(t, srv, "POST", "/api/v1/ai/plan", `{"utterance":"导出 shop 库 emp 为 csv"}`)
	var resp struct {
		SessionID string `json:"session_id"`
		PlanID    string `json:"plan_id"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	// 仅激活（execute=false）也会存加密 config 产物（内含未注入哨兵）
	wc := doJSON(t, srv, "POST", "/api/v1/ai/plan/confirm",
		`{"session_id":"`+resp.SessionID+`","plan_id":"`+resp.PlanID+`","execute":false}`)
	if wc.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", wc.Code, wc.Body.String())
	}

	wa := doJSON(t, srv, "POST", "/api/v1/ai/session/"+resp.SessionID+"/apply", `{}`)
	if wa.Code != http.StatusBadRequest {
		t.Fatalf("apply with unfilled sentinel must 400: %d %s", wa.Code, wa.Body.String())
	}
	if !strings.Contains(wa.Body.String(), "凭据") && !strings.Contains(wa.Body.String(), "credential") {
		t.Errorf("error should mention credentials: %s", wa.Body.String())
	}
}

func TestClarifyItemsMapping(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	store, _ := srv.dsStore()
	store.Put("oracle-scott", "oracle", "SCOTT", "oracle://scott:pw@127.0.0.1:1521/XEPDB1", "")
	store.Put("mysql-shop", "mysql", "shop", "root:pw@tcp(127.0.0.1:3306)/shop", "")

	// 路由层 clarify：导出歧义 → 内容/格式选项 + 档案选项
	t.Setenv("OWL_AI_API_KEY", "test-key")
	vendor, _, _ := seqVendor(t, []string{
		`{"route":"clarify","sub":"","confidence":"medium","missing_slots":["导出内容：users 的表结构（元数据）还是表数据","输出格式（csv/sql/xlsx）","源库连接信息（host 或 dsn）"],"out_of_scope":false,"needs_clarify":true,"reason":"导出表为 csv 既可指元数据也可指数据，需确认"}`,
	})
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()
	w := doJSON(t, srv, "POST", "/api/v1/ai/plan", `{"utterance":"导出 users 表为 csv"}`)
	var resp struct {
		ClarifyItems []struct {
			Question  string   `json:"question"`
			Options   []string `json:"options"`
			AllowText bool     `json:"allow_text"`
		} `json:"clarify_items"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.ClarifyItems) == 0 {
		t.Fatalf("no clarify_items: %.300s", w.Body.String())
	}
	foundContent, foundFormat, foundProfile := false, false, false
	for _, it := range resp.ClarifyItems {
		switch {
		case strings.Contains(it.Question, "导出内容"):
			foundContent = len(it.Options) >= 2
		case strings.Contains(it.Question, "格式"):
			foundFormat = len(it.Options) == 3
		case strings.Contains(it.Question, "数据源"):
			foundProfile = len(it.Options) == 2 // 两个档案名
		}
	}
	if !foundContent || !foundFormat || !foundProfile {
		t.Errorf("items missing: content=%v format=%v profile=%v", foundContent, foundFormat, foundProfile)
	}
}

func TestFactsUsedWithIdentity(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	store, _ := srv.dsStore()
	store.Put("appuser-xe", "oracle", "APPUSER", "oracle://appuser:pw@127.0.0.1:1521/XEPDB1", "")
	t.Setenv("OWL_AI_API_KEY", "test-key")
	vendor, _, _ := seqVendor(t, []string{
		`{"route":"export-data","sub":"format:csv","confidence":"high","missing_slots":[],"out_of_scope":false,"needs_clarify":false,"reason":"导出意图"}`,
		`{"scenario":"export","metadata":"database","source":{"profile":"appuser-xe","schema":"APPUSER"},"export":{"format":"csv"}}`,
	})
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	w := doJSON(t, srv, "POST", "/api/v1/ai/plan", `{"utterance":"用 appuser-xe 导出全部表为 csv"}`)
	var resp struct {
		FactsUsed []string `json:"facts_used"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.FactsUsed) == 0 {
		t.Fatalf("no facts_used")
	}
	joined := strings.Join(resp.FactsUsed, " ")
	if !strings.Contains(joined, "appuser-xe") || !strings.Contains(joined, "appuser@127.0.0.1:1521/XEPDB1") {
		t.Errorf("facts_used should carry masked identity: %v", resp.FactsUsed)
	}
	if strings.Contains(joined, "pw@") || strings.Contains(joined, ":pw") {
		t.Errorf("facts_used leaks password: %v", resp.FactsUsed)
	}
}

func TestForcedProfileVsUtterance(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	store, _ := srv.dsStore()
	store.Put("p1", "oracle", "S1", "oracle://u1:pw@h1:1521/XEPDB1", "")
	store.Put("p2", "mysql", "S2", "root:pw@tcp(h2:3306)/shop", "")
	t.Setenv("OWL_AI_API_KEY", "test-key")

	// LLM 槽位未提任何 profile + 请求 forced.source=p1 → 生效 p1
	vendor, _, _ := seqVendor(t, []string{
		`{"route":"export-data","sub":"format:csv","confidence":"high","missing_slots":[],"out_of_scope":false,"needs_clarify":false,"reason":"导出意图"}`,
		`{"scenario":"export","metadata":"database","source":{"type":"oracle","schema":"SCOTT","host":"db","user":"u","database":"XEPDB1"},"export":{"format":"csv"}}`,
	})
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	w := doJSON(t, srv, "POST", "/api/v1/ai/plan",
		`{"utterance":"导出 SCOTT 的全部表为 csv","profile":{"source":"p1"}}`)
	var resp1 struct {
		Session struct {
			Slots map[string]string `json:"slots"`
		} `json:"session"`
		YAML string `json:"yaml"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp1)
	if !strings.Contains(resp1.YAML, "__PWD_oracle__") || !strings.Contains(resp1.YAML, "h1") {
		t.Errorf("forced p1 should win when utterance silent: %.300s", resp1.YAML)
	}

	// 话语显式 profile=p2 → 覆盖 forced p1（mock 第二轮总是返回同一槽位，但
	// 话语里显式 profile 的槽位由 mock 提供）
	vendor2, _, _ := seqVendor(t, []string{
		`{"route":"export-data","sub":"format:csv","confidence":"high","missing_slots":[],"out_of_scope":false,"needs_clarify":false,"reason":"导出意图"}`,
		`{"scenario":"export","metadata":"database","source":{"profile":"p2","schema":"shop"},"export":{"format":"csv"}}`,
	})
	srv.cfg.AI.BaseURL = vendor2.URL
	w = doJSON(t, srv, "POST", "/api/v1/ai/plan",
		`{"utterance":"改用 p2 导出 shop 的表","profile":{"source":"p1"}}`)
	var resp2 struct {
		YAML string `json:"yaml"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp2)
	if !strings.Contains(resp2.YAML, "__PWD_mysql__") {
		t.Errorf("explicit utterance profile p2 must beat forced p1: %.300s", resp2.YAML)
	}

	// LLM 给完整 DSN → 覆盖档案（无哨兵、无档案引用）
	vendor3, _, _ := seqVendor(t, []string{
		`{"route":"export-data","sub":"format:csv","confidence":"high","missing_slots":[],"out_of_scope":false,"needs_clarify":false,"reason":"导出意图"}`,
		`{"scenario":"export","metadata":"database","source":{"type":"mysql","dsn":"root:pass123@tcp(10.0.0.9:3306)/shop"},"export":{"format":"csv"}}`,
	})
	srv.cfg.AI.BaseURL = vendor3.URL
	w = doJSON(t, srv, "POST", "/api/v1/ai/plan",
		`{"utterance":"用这个 DSN 导出 shop：root:pass123@tcp(10.0.0.9:3306)/shop","profile":{"source":"p1"}}`)
	var resp3 struct {
		YAML string `json:"yaml"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp3)
	if !strings.Contains(resp3.YAML, "10.0.0.9") {
		t.Errorf("verbatim DSN must beat forced profile: %.300s", resp3.YAML)
	}
}

func TestDeleteSessionEndpoint(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	store, err := srv.aiSessions()
	if err != nil {
		t.Fatalf("aiSessions: %v", err)
	}
	sess, _ := store.Create("export-data", "")
	store.SetMeta(sess, "待删除会话", "export csv")

	w := doJSON(t, srv, "DELETE", "/api/v1/ai/session/"+sess.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	// 列表不再包含
	lw := doJSON(t, srv, "GET", "/api/v1/ai/sessions", "")
	var items []map[string]any
	json.Unmarshal(lw.Body.Bytes(), &items)
	if len(items) != 0 {
		t.Errorf("list after delete: %d items", len(items))
	}
	// 恢复包 404（删除 = 对所有按 id 路径不存在）
	if w := doJSON(t, srv, "GET", "/api/v1/ai/session/"+sess.ID, ""); w.Code != http.StatusNotFound {
		t.Errorf("get deleted session: %d", w.Code)
	}
	// 再删 404
	if w := doJSON(t, srv, "DELETE", "/api/v1/ai/session/"+sess.ID, ""); w.Code != http.StatusNotFound {
		t.Errorf("re-delete: %d", w.Code)
	}
}

func TestBatchDeleteSessionsEndpoint(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	store, _ := srv.aiSessions()
	var toDelete []string
	for i := 0; i < 3; i++ {
		sess, _ := store.Create("export-data", "")
		if i < 2 {
			toDelete = append(toDelete, sess.ID)
		}
	}
	body, _ := json.Marshal(map[string]any{"ids": toDelete})
	w := doJSON(t, srv, "POST", "/api/v1/ai/sessions/batch-delete", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("batch delete: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Deleted int `json:"deleted"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Deleted != 2 {
		t.Errorf("deleted = %d, want 2", resp.Deleted)
	}
	lw := doJSON(t, srv, "GET", "/api/v1/ai/sessions", "")
	var items []map[string]any
	json.Unmarshal(lw.Body.Bytes(), &items)
	if len(items) != 1 {
		t.Errorf("list = %d items, want 1", len(items))
	}
	// 空 ids → 400
	if w := doJSON(t, srv, "POST", "/api/v1/ai/sessions/batch-delete", `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("empty ids: %d", w.Code)
	}
	// 未知 id：跳过并如实计数
	if w := doJSON(t, srv, "POST", "/api/v1/ai/sessions/batch-delete", `{"ids":["s-nope","s-nope2"]}`); w.Code != http.StatusOK {
		t.Errorf("unknown ids: %d", w.Code)
	} else {
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Deleted != 0 {
			t.Errorf("unknown ids deleted = %d, want 0", resp.Deleted)
		}
	}
}
