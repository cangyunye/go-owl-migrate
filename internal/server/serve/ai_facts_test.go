package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/configbuild"
)

// TestAIPlanWithDatasourceProfile walks the full fact-layer flow: a saved
// datasource profile is injected into the slot stage (name/type only), the
// plan carries a sentinel DSN with NO password prompt for that side, and
// confirm resolves the real DSN from the vault — browser never sees it.
func TestAIPlanWithDatasourceProfile(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServerWithDatasources(t, dir)

	// Save a profile with a real-looking password (encrypted by the vault).
	store, err := srv.dsStore()
	if err != nil {
		t.Fatalf("dsStore: %v", err)
	}
	if err := store.Put("scott-xe", "oracle", "SCOTT", "oracle://scott:RealSecret1@127.0.0.1:1521/XEPDB1", "本地 Oracle 测试"); err != nil {
		t.Fatalf("profile put: %v", err)
	}

	vendor, calls, bodies := seqVendor(t, []string{
		`{"route":"export-data","sub":"format:csv","confidence":"high","missing_slots":[],"out_of_scope":false,"needs_clarify":false,"reason":"导出意图"}`,
		`{"scenario":"export","metadata":"database","source":{"profile":"scott-xe","schema":"SCOTT"},"export":{"format":"csv","tables":["emp"]}}`,
	})
	t.Setenv("OWL_AI_API_KEY", "test-key")
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	// ── plan: user utterance mentions the profile by name only ──
	w := doJSON(t, srv, "POST", "/api/v1/ai/plan",
		`{"utterance":"用 scott-xe 档案导出 emp 表为 csv"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("plan status = %d, body=%s", w.Code, w.Body.String())
	}
	if *calls != 2 {
		t.Fatalf("vendor calls = %d, want 2", *calls)
	}
	// Facts block must reach the slot stage (profile name visible, no secret).
	if !strings.Contains((*bodies)[1], "scott-xe") {
		t.Errorf("slot stage message lacks the profile facts block: %.300s", (*bodies)[1])
	}
	if strings.Contains((*bodies)[1], "RealSecret1") {
		t.Fatal("slot stage message leaks the vault password")
	}

	var resp struct {
		OK        bool     `json:"ok"`
		PlanID    string   `json:"plan_id"`
		SessionID string   `json:"session_id"`
		YAML      string   `json:"yaml"`
		CredSlots []string `json:"credential_slots"`
		FactsUsed []string `json:"facts_used"`
		Session   struct {
			Slots map[string]string `json:"slots"`
		} `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	if !resp.OK || resp.PlanID == "" {
		t.Fatalf("plan resp = %+v", resp)
	}
	// Sentinel DSN present (storable), real password absent.
	if !strings.Contains(resp.YAML, "__PWD_oracle__") {
		t.Errorf("yaml must keep the profile sentinel DSN: %s", resp.YAML)
	}
	if strings.Contains(resp.YAML, "RealSecret1") {
		t.Fatal("masked yaml leaks the vault password")
	}
	// No credential prompt for the profile side.
	if len(resp.CredSlots) != 0 {
		t.Errorf("credential_slots should be empty for a profile-resolved plan, got %v", resp.CredSlots)
	}
	if len(resp.FactsUsed) == 0 || !strings.Contains(resp.FactsUsed[0], "scott-xe") {
		t.Errorf("facts_used = %v", resp.FactsUsed)
	}
	if resp.Session.Slots["source_profile"] != "scott-xe" {
		t.Errorf("session slots missing source_profile: %+v", resp.Session.Slots)
	}

	// ── confirm (activate only): vault re-resolution, no browser credentials ──
	body := map[string]any{"session_id": resp.SessionID, "plan_id": resp.PlanID, "execute": false}
	rawBody, _ := json.Marshal(body)
	wc := doJSONRaw(t, srv, "POST", "/api/v1/ai/plan/confirm", rawBody)
	if wc.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body=%s", wc.Code, wc.Body.String())
	}

	// The activated config must carry the REAL decrypted DSN.
	current := doJSON(t, srv, "GET", "/api/v1/config/current", "")
	var cur struct {
		Values map[string]string `json:"values"`
	}
	if err := json.Unmarshal(current.Body.Bytes(), &cur); err != nil {
		t.Fatalf("unmarshal current: %v", err)
	}
	if got := cur.Values["source_dsn"]; !strings.Contains(got, "RealSecret1") {
		t.Errorf("activated source_dsn should be the resolved vault DSN, got %q", got)
	}
}

// TestPlanFactsBlockMasksSecrets 直接锁定事实层的脱敏契约：档案只以
// 名称/类型/schema 摘要出现，激活配置的 DSN 只以 MaskDSN 结果出现——
// 仓库密码与激活配置密码都绝不能进入 LLM 上下文（ADR-0002 哨兵协议的第
// 一道闸门，此前只被集成路径间接触达）。
func TestPlanFactsBlockMasksSecrets(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	store, err := srv.dsStore()
	if err != nil {
		t.Fatalf("dsStore: %v", err)
	}
	if err := store.Put("scott-xe", "oracle", "SCOTT", "oracle://scott:RealSecret1@127.0.0.1:1521/XEPDB1", "本地 Oracle 测试"); err != nil {
		t.Fatalf("profile put: %v", err)
	}
	srcDSN := "oracle://scott:ActiveSecret@10.0.0.9:1521/ORCLPDB1"
	tgtDSN := "root:TargetPW@tcp(10.0.0.10:3306)/app"
	srv.cfg = &config.Config{
		Source: config.DBConfig{Type: "oracle", DSN: srcDSN, Schema: "SCOTT"},
		Target: config.DBConfig{Type: "mysql", DSN: tgtDSN, Schema: "app"},
	}

	block := srv.planFactsBlock()

	for _, want := range []string{
		"【可用数据源档案】",
		"scott-xe（oracle, schema SCOTT）：本地 Oracle 测试",
		"【当前激活配置】",
		config.MaskDSN(srcDSN),
		config.MaskDSN(tgtDSN),
		"schema=SCOTT",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("facts block missing %q:\n%s", want, block)
		}
	}
	for _, secret := range []string{"RealSecret1", "ActiveSecret", "TargetPW"} {
		if strings.Contains(block, secret) {
			t.Fatalf("facts block leaks %s:\n%s", secret, block)
		}
	}
	// 档案行绝不携带 DSN（连脱敏形态也不带）——档案摘要只有名称/类型/schema。
	profileLine := block[strings.Index(block, "scott-xe"):]
	if strings.Contains(profileLine[:strings.Index(profileLine, "\n")], "@") {
		t.Errorf("profile summary line must not carry a DSN:\n%s", block)
	}
}

// 无档案、无激活配置时事实块为空——不给 LLM 塞空段落占上下文。
func TestPlanFactsBlockEmptyWhenNothingStored(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	if got := srv.planFactsBlock(); got != "" {
		t.Errorf("facts block should be empty, got:\n%s", got)
	}
}

// fail-closed 契约：档案 DSN 形态无法定位密码段时宁可拒绝计划，也不能把
// 未脱敏的 DSN 放进产物——这是"LLM 永不见真实密码"的最后防线。
func TestResolveProfilesInSlotsFailClosedOnUnmaskableDSN(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	store, err := srv.dsStore()
	if err != nil {
		t.Fatalf("dsStore: %v", err)
	}
	if err := store.Put("odd-dsn", "oracle", "SCOTT", "bare-host-without-password", "形态怪异的 DSN"); err != nil {
		t.Fatalf("profile put: %v", err)
	}

	req := &configbuild.SlotRequest{
		Scenario: "export",
		Source:   configbuild.EndpointSlots{Profile: "odd-dsn"},
	}
	dsns, used, err := srv.resolveProfilesInSlots(req)
	if err == nil {
		t.Fatalf("unmaskable DSN must be rejected, got dsns=%v used=%v dsn=%q", dsns, used, req.Source.DSN)
	}
	if !errors.Is(err, configbuild.ErrIncompleteSlots) {
		t.Errorf("error must be ErrIncompleteSlots so the plan stage turns it into clarify, got: %v", err)
	}
	if !strings.Contains(err.Error(), "无法安全脱敏") || !strings.Contains(err.Error(), "odd-dsn") {
		t.Errorf("error should name the profile and the fix, got: %v", err)
	}
	if strings.Contains(req.Source.DSN, "bare-host") && req.Source.DSN != "" {
		t.Errorf("rejected profile must not leave its raw DSN on the endpoint, got %q", req.Source.DSN)
	}
}

// 档案解析必须覆盖 LLM 自编的连接部件：host/port/user/database 一律清空，
// 以档案为准——防"档案存对、槽位抄错"的静默漂移；密码族换哨兵、无密码族
// （sqlite3）直用路径且不进哨兵清单。
func TestResolveProfilesInSlotsReplacesHandMadeParts(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	store, err := srv.dsStore()
	if err != nil {
		t.Fatalf("dsStore: %v", err)
	}
	if err := store.Put("scott-xe", "oracle", "SCOTT", "oracle://scott:RealSecret1@127.0.0.1:1521/XEPDB1", ""); err != nil {
		t.Fatalf("put oracle: %v", err)
	}
	localPath := filepath.Join(t.TempDir(), "app.db")
	if err := store.Put("local-db", "sqlite3", "", localPath, ""); err != nil {
		t.Fatalf("put sqlite: %v", err)
	}

	req := &configbuild.SlotRequest{
		Scenario: "migrate",
		Source: configbuild.EndpointSlots{
			Profile: "scott-xe",
			Host:    "llm-invented-host", Port: "1", User: "llm", Database: "madeup",
		},
		Target: &configbuild.EndpointSlots{Profile: "local-db"},
	}
	dsns, used, err := srv.resolveProfilesInSlots(req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	src := req.Source
	if src.Host != "" || string(src.Port) != "" || src.User != "" || src.Database != "" {
		t.Errorf("hand-made parts must be wiped, got host=%q port=%q user=%q db=%q", src.Host, src.Port, src.User, src.Database)
	}
	if src.Type != "oracle" || src.Schema != "SCOTT" {
		t.Errorf("type/schema should come from the vault, got %q/%q", src.Type, src.Schema)
	}
	if !strings.Contains(src.DSN, "__PWD_oracle__") || strings.Contains(src.DSN, "RealSecret1") {
		t.Errorf("source DSN must carry the sentinel, got %q", src.DSN)
	}
	if len(dsns) != 1 || dsns[0] != src.DSN {
		t.Errorf("profileDSNs should list exactly the sentinel DSN, got %v", dsns)
	}
	if req.Target.DSN != localPath {
		t.Errorf("passwordless family should use the path verbatim, got %q", req.Target.DSN)
	}
	if strings.Contains(strings.Join(used, " "), "RealSecret1") {
		t.Fatal("facts_used leaks the vault password")
	}
}

// 引用不存在的档案 → ErrIncompleteSlots 且文案指向数据源页（可操作的
// 引导，而不是裸错误）。
func TestResolveProfilesInSlotsUnknownProfile(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	req := &configbuild.SlotRequest{
		Scenario: "export",
		Source:   configbuild.EndpointSlots{Profile: "no-such-profile"},
	}
	_, _, err := srv.resolveProfilesInSlots(req)
	if !errors.Is(err, configbuild.ErrIncompleteSlots) {
		t.Fatalf("unknown profile must be ErrIncompleteSlots, got: %v", err)
	}
	if !strings.Contains(err.Error(), "no-such-profile") {
		t.Errorf("error should name the missing profile, got: %v", err)
	}
}

// TestSetYAMLSectionDSN covers the confirm-time yaml surgery.
func TestSetYAMLSectionDSN(t *testing.T) {
	in := "general:\n    log_level: info\nmetadata:\n    type: database\nsource:\n    type: oracle\n    dsn: oracle://scott:__PWD_oracle__@h:1521/XEPDB1\n    schema: SCOTT\ntarget:\n    type: mysql\n    dsn: root:__PWD_mysql__@tcp(h:3306)/db\n"
	out, err := setYAMLSectionDSN(in, "source", "oracle://scott:Real@h:1521/XEPDB1")
	if err != nil {
		t.Fatalf("setYAMLSectionDSN: %v", err)
	}
	if !strings.Contains(out, "oracle://scott:Real@h:1521/XEPDB1") {
		t.Errorf("source dsn not swapped:\n%s", out)
	}
	if !strings.Contains(out, "root:__PWD_mysql__@tcp(h:3306)/db") {
		t.Errorf("target dsn must be untouched:\n%s", out)
	}
	if !strings.Contains(out, "schema: SCOTT") {
		t.Errorf("sibling keys must be preserved:\n%s", out)
	}
	if _, err := setYAMLSectionDSN(in, "nonexistent", "x"); err == nil {
		t.Error("missing section must error")
	}
}

// TestAIPlanSelectedProfilesResolveSourceClarifyAndTarget locks the two chat
// regressions together: (2) when source+target profiles are selected in the UI,
// the router's "which source?" clarify is suppressed; (3) when the slot stage
// omits the target entirely, the selected target profile is synthesized so the
// builder no longer fails with "scenario migrate requires a target endpoint".
func TestAIPlanSelectedProfilesResolveSourceClarifyAndTarget(t *testing.T) {
	srv := newTestServerWithDatasources(t, t.TempDir())
	store, err := srv.dsStore()
	if err != nil {
		t.Fatalf("dsStore: %v", err)
	}
	if err := store.Put("src-pg", "postgres", "public", "postgres://srcu:SrcSecret1@127.0.0.1:5432/srcdb", ""); err != nil {
		t.Fatalf("put src: %v", err)
	}
	if err := store.Put("tgt-pg", "postgres", "public", "postgres://tgtu:TgtSecret2@127.0.0.1:5432/tgtdb", ""); err != nil {
		t.Fatalf("put tgt: %v", err)
	}

	vendor, calls, bodies := seqVendor(t, []string{
		// Router wrongly asks which source, despite the UI selection.
		`{"route":"migrate","sub":"","confidence":"high","missing_slots":["源数据源"],"out_of_scope":false,"needs_clarify":true,"reason":"缺源"}`,
		// Slot stage only fills source; target is omitted on purpose.
		`{"scenario":"migrate","source":{"profile":"src-pg"},"export":{"tables":["users"]}}`,
	})
	t.Setenv("OWL_AI_API_KEY", "test-key")
	srv.cfg.AI.BaseURL = vendor.URL
	srv.cfg.AI.ApplyDefaults()

	w := doJSON(t, srv, "POST", "/api/v1/ai/plan",
		`{"utterance":"把 users 表的数据迁移到目标库 b 用户下","profile":{"source":"src-pg","target":"tgt-pg"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		OK           bool     `json:"ok"`
		PlanID       string   `json:"plan_id"`
		Route        string   `json:"route"`
		NeedsClarify bool     `json:"needs_clarify"`
		YAML         string   `json:"yaml"`
		FactsUsed    []string `json:"facts_used"`
		Session      struct {
			Slots map[string]string `json:"slots"`
		} `json:"session"`
		Continuity map[string]any `json:"continuity"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Issue 3: a plan must be produced (not the "target required" clarify).
	if resp.PlanID == "" || resp.NeedsClarify {
		t.Fatalf("expected a plan, got plan_id=%q needs_clarify=%v body=%s", resp.PlanID, resp.NeedsClarify, w.Body.String())
	}
	if resp.Route != "migrate" {
		t.Errorf("route = %q, want migrate", resp.Route)
	}
	if *calls != 2 {
		t.Fatalf("vendor calls = %d, want 2 (route + slots)", *calls)
	}
	// Issue 2: the route-stage prompt carried the UI selection as a fact.
	if !strings.Contains((*bodies)[0], "界面已选数据源") || !strings.Contains((*bodies)[0], "tgt-pg") {
		t.Errorf("route message lacks selected-profile facts:\n%.400s", (*bodies)[0])
	}
	// Both profile sides resolved into the plan (sentinel DSNs, no secrets).
	if !strings.Contains(resp.YAML, "target:") || !strings.Contains(resp.YAML, "__PWD_pg__") {
		t.Errorf("plan yaml should carry a resolved target profile:\n%s", resp.YAML)
	}
	for _, secret := range []string{"SrcSecret1", "TgtSecret2"} {
		if strings.Contains(resp.YAML, secret) {
			t.Fatalf("plan yaml leaks %s", secret)
		}
	}
	if resp.Session.Slots["target_profile"] != "tgt-pg" {
		t.Errorf("session slots missing target_profile: %+v", resp.Session.Slots)
	}
}

// TestProfileCoversConnectionGap covers the deterministic side-match used to
// suppress router clarifies: source gaps need a source profile, target gaps a
// target profile, and unqualified connection gaps accept either.
func TestProfileCoversConnectionGap(t *testing.T) {
	cases := []struct {
		missing, src, tgt string
		want              bool
	}{
		{"源数据源", "src", "tgt", true},
		{"源数据源", "", "tgt", false},
		{"目标连接", "src", "", false},
		{"目标连接", "src", "tgt", true},
		{"数据库连接", "src", "", true},
		{"host", "", "tgt", true},
		{"导出格式", "src", "tgt", false},
		{"可导出的表名", "src", "tgt", false},
	}
	for _, c := range cases {
		if got := profileCoversConnectionGap(c.missing, c.src, c.tgt); got != c.want {
			t.Errorf("profileCoversConnectionGap(%q, %q, %q) = %v, want %v", c.missing, c.src, c.tgt, got, c.want)
		}
	}
}

// newTestServerWithDatasources: test server with isolated datasource AND
// AI-session dirs — without the latter, tests would read/write the real
// user session database at ~/.owl/migrate/ai/sessions.
func newTestServerWithDatasources(t *testing.T, dir string) *Server {
	t.Helper()
	srv := newTestServer(t)
	srv.dataSourcesDir = dir
	srv.aiSessionsDir = t.TempDir()
	srv.dsOnce = sync.Once{}
	srv.aiOnce = sync.Once{}
	return srv
}

// fakeMaster spins a stub master IPC that accepts job launches. Needed by
// confirm(execute=true) — effective marking and job artifacts depend on a
// successful launch.
func fakeMaster(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"job_id":"job-test-0001","status":"running"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// doJSONRaw issues a request with a pre-marshalled body.
func doJSONRaw(t *testing.T, srv *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}
