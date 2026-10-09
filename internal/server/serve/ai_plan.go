package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/ai"
	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/configbuild"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	"github.com/cangyunye/go-owl-migrate/internal/owlagent"
	"github.com/cangyunye/owljdbc"
	"gopkg.in/yaml.v3"
)

// aiRouteResult is the shared shape of the router's JSON verdict.
type aiRouteResult struct {
	Route        string   `json:"route"`
	Sub          string   `json:"sub"`
	Confidence   string   `json:"confidence"`
	MissingSlots []string `json:"missing_slots"`
	OutOfScope   bool     `json:"out_of_scope"`
	NeedsClarify bool     `json:"needs_clarify"`
	Reason       string   `json:"reason"`
}

// handleAIPlan turns one utterance into a validated migrate.yaml draft:
// route (LLM) → generate (LLM, credential placeholders) → deterministic
// credential injection → config.Load structural validation → repair loop
// (validation error fed back, ≤ max_repair_rounds) → masked draft returned.
// Nothing is executed here; confirm + job launch stay separate endpoints.
//
// Session continuity: pass session_id to continue. Same intent → slots carry
// forward (new utterance values override); different intent → the old session
// stays archived and a fresh round clones its slots, so the response can tell
// the user "已自动开新一轮" instead of forcing a manual new conversation.
func (s *Server) handleAIPlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Utterance   string            `json:"utterance"`
		Context     []string          `json:"context"`               // 显式前文（与 /ai/route 同语义）
		SessionID   string            `json:"session_id"`            // 缺省 = 新会话
		Credentials map[string]string `json:"credentials,omitempty"` // "__PWD_mysql__" → 真实密码（服务端注入，不入会话/日志/响应）
		Profile     struct {
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"profile,omitempty"` // 对话页选择器的默认档案（话语显式提及优先）
	}
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}
	req.Utterance = strings.TrimSpace(req.Utterance)
	if req.Utterance == "" {
		writeError(w, http.StatusBadRequest, "utterance is required")
		return
	}

	a := s.aiSettings()
	key, _ := s.aiKeyMaterial()
	if key == "" {
		writeError(w, http.StatusServiceUnavailable,
			"AI 未配置：到配置页「AI 供应商」保存 Key，或设置环境变量 "+a.APIKeyEnv)
		return
	}
	if a.Model == "" {
		writeError(w, http.StatusBadRequest,
			"模型未设置：到配置页「AI 供应商」探测或手填模型")
		return
	}
	store, err := s.aiSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "会话存储不可用: "+err.Error())
		return
	}

	// 两段 LLM 调用 + 修复轮：预算 = (1 路由 + 1 生成 + N 修复) × 单次超时。
	budget := (2 + time.Duration(a.MaxRepairRounds)) * (a.Timeout() + 15*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	client := ai.NewClient(a.BaseURL, key, a.Model, a.Timeout())

	// ── 第 1 段：意图路由（会话内时把有界的轮次窗口并进上文） ──
	var ctxLines []string
	ctxLines = append(ctxLines, req.Context...)
	if sess, ok := s.sessionFrom(req.SessionID, store); ok {
		for _, t := range sess.Turns {
			ctxLines = append(ctxLines, t.Role+": "+t.Content)
		}
	}
	routeUser := buildRouteUserMessage(req.Utterance, ctxLines)
	routeUser += s.planFactsBlock()
	// 凭据与连接由服务端管理（数据源档案/哨兵注入）：无论是否带 credentials，
	// 路由器都不应因"缺密码/缺 host"而澄清——缺的事实要么在档案里，要么由
	// 生成段澄清，路由层放行。
	routeUser += "\n\n【凭据与连接说明】连接凭据由服务端管理（数据源档案/哨兵注入），缺少密码或连接信息不构成澄清理由；用户请求匹配【可用数据源档案】时视为连接信息完备。"
	routeReply, err := client.Chat(ctx, ai.RouterSystemPrompt,
		[]ai.Message{{Role: "user", Content: routeUser}},
		ai.Options{JSONMode: true, Effort: a.Effort, MaxTokens: a.MaxTokens, Timeout: a.Timeout()})
	if err != nil {
		writeError(w, http.StatusBadGateway, "AI 路由失败: "+err.Error())
		return
	}
	routeRaw, err := ai.ExtractJSON(routeReply.Content)
	if err != nil {
		writeError(w, http.StatusBadGateway, "AI 路由回复不是合法 JSON: "+err.Error())
		return
	}
	var route aiRouteResult
	if err := json.Unmarshal(routeRaw, &route); err != nil {
		writeError(w, http.StatusBadGateway, "AI 路由 JSON 结构不符: "+err.Error())
		return
	}

	// ── 会话接续判定：同意图沿用；意图变化自动开新一轮（克隆槽位） ──
	sess, continuity := s.resolveSession(store, req.SessionID, route, req.Utterance)

	// 澄清/范围外不生成配置：判定落会话后直接返回，让 UI 引导用户。
	if route.OutOfScope || route.NeedsClarify || route.Route == "clarify" || route.Route == "out-of-scope" {
		store.AddTurn(sess, "user", req.Utterance)
		store.AddTurn(sess, "assistant", route.Reason)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "session_id": sess.ID, "session": sess, "continuity": continuity,
			"route": route.Route, "result": route, "plan": nil,
			"clarify_items": s.clarifyItemsFor(route.MissingSlots, sess),
		})
		return
	}

	// ── 第 2 段：配置组装。首选确定性 builder（LLM 只填槽、永不碰凭据与
	// YAML 结构）；builder 处理不了的组合回退 LLM-YAML（engine 标注）。
	planMsg := buildPlanUserMessage(req.Utterance, sess, route) + s.planFactsBlock()
	res, sbErr := s.buildViaSlots(ctx, client, a, store, sess, route, planMsg, req.Credentials,
		req.Profile.Source, req.Profile.Target)
	var (
		yamlText    string
		engine      string
		repairs     int
		profileDSNs []string
		factsUsed   []string
		fallbackWhy string
	)
	switch {
	case sbErr == nil:
		yamlText, engine, repairs = res.yaml, res.engine, res.repairs
		profileDSNs, factsUsed = res.profileDSNs, res.factsUsed
	case errors.Is(sbErr, configbuild.ErrIncompleteSlots):
		// 槽位不足（如缺目标库名）→ 澄清，而不是让 LLM 编造缺失事实。
		// builder 的错误文本是内部形态（"source.host is required for
		// oracle…"），直接透出读起来像失败而非追问——翻译成中文引导，
		// 并给出结构化 missing_slots 供 UI 逐项提示。
		reason, missing := friendlyClarify(sbErr.Error())
		store.AddTurn(sess, "assistant", reason)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "session_id": sess.ID, "session": sess, "continuity": continuity,
			"route": route.Route, "plan": nil, "needs_clarify": true,
			"clarify_reason": reason, "missing_slots": missing,
			"clarify_items": s.clarifyItemsFor(missing, sess),
		})
		return
	default:
		var genErr error
		yamlText, repairs, genErr = generateAndRepair(ctx, client, a, planMsg, req.Credentials)
		if genErr != nil {
			writeError(w, http.StatusBadGateway, genErr.Error())
			return
		}
		engine = "llm-fallback"
		fallbackWhy = sbErr.Error()
	}

	// 能力前置告警：agent 通道（显式 agent 或 auto 落 agent）的 Java/sidecar/
	// 驱动 jar 就绪检查——计划阶段给指引，不让用户到连接阶段才兜圈子。
	warnings := s.agentPrerequisiteWarningsForYAML(yamlText)

	// credential_slots: 草案里仍未注入真实值的凭据哨兵（如 "__PWD_mysql__"）。
	// 单独成字段，前端据此渲染"连接密码"输入框——用户不需要认识哨兵协议，
	// 哨兵也不和能力告警混在一起。真实值只在 confirm 时经服务端注入。
	// 档案解析出的哨兵 DSN 整段从扫描中移除：档案已持有密码，UI 不该再要。
	scanText := yamlText
	for _, pd := range profileDSNs {
		scanText = strings.ReplaceAll(scanText, pd, "")
	}
	credSlots := remainingPlaceholders(scanText, nil)

	planID := ai.NewSessionID()
	masked := maskYAML(yamlText, req.Credentials)
	store.MergeSlots(sess, yamlSlots(yamlText))
	// profile 槽位（source_profile/target_profile）与 facts_used 已在
	// buildViaSlots 内部处理——那里才有 LLM 提取的槽位对象。
	store.AddArtifact(sess, ai.Artifact{Kind: "plan", ID: planID, YAML: masked})
	store.SetStage(sess, ai.StageConfirming)
	store.AddTurn(sess, "user", req.Utterance)
	store.AddTurn(sess, "assistant", "已生成配置草案 "+planID+"（等待确认执行）")
	// 会话元数据：title=首条话语截断（历史列表主显示），keywords=槽位拆词。
	// 只在首个计划时落（后续轮次保留用户最初的表达作为标题）。
	if sess.Title == "" {
		title := req.Utterance
		if r := []rune(title); len(r) > 48 {
			title = string(r[:48]) + "…"
		}
		_ = store.SetMeta(sess, title, strings.Join(res.keywords, " "))
	}

	if factsUsed == nil {
		factsUsed = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"session_id":       sess.ID,
		"session":          sess,
		"continuity":       continuity,
		"route":            route.Route,
		"sub":              route.Sub,
		"engine":           engine,
		"fallback_reason":  fallbackWhy,
		"plan_id":          planID,
		"yaml":             masked, // 脱敏预览；含真实凭据的版本只在服务端内存/会话产物里
		"repair_rounds":    repairs,
		"credential_slots": credSlots,
		"facts_used":       factsUsed,
		"warnings":         warnings,
		"next":             "确认后经任务端点执行（执行时凭据由服务端注入，浏览器不接触）",
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// friendlyClarify translates a configbuild.ErrIncompleteSlots message into a
// user-facing follow-up question. The builder's errors are precise but
// internal-shaped ("source.host is required for oracle (or provide
// source.dsn verbatim)"); surfacing them verbatim reads like a failure
// instead of a question. Returns the friendly reason plus a structured
// missing list for the UI to render as per-item guidance. Unrecognized
// messages fall back to the raw text so no information is lost.
func friendlyClarify(raw string) (string, []string) {
	missing := []string{}
	re := regexp.MustCompile(`(\w+)\.(\w+) is required for ([\w-]+)`)
	for _, m := range re.FindAllStringSubmatch(raw, -1) {
		sideLabel := "源库"
		if strings.EqualFold(m[1], "target") {
			sideLabel = "目标库"
		}
		what := m[2]
		switch strings.ToLower(m[2]) {
		case "type":
			what = "数据库类型"
		case "host":
			what = "连接地址（host，或直接给完整 DSN）"
		case "port":
			what = "端口"
		case "user":
			what = "用户名"
		case "database":
			if strings.EqualFold(m[3], "sqlite3") || strings.EqualFold(m[3], "duckdb") {
				what = "数据库文件路径"
			} else {
				what = "库名/服务名"
			}
		case "schema":
			what = "schema"
		}
		missing = append(missing, sideLabel+" "+what)
	}
	if len(missing) == 0 {
		return "信息不足以生成配置：" + raw, missing
	}
	return "信息不足以生成配置。", missing
}

// buildViaSlots extracts a SlotRequest via the LLM (LLM never emits passwords:
// the prompt mandates sentinels, real values come from req.Credentials) and
// assembles the config with configbuild. Endpoints referencing a stored
// datasource profile are resolved server-side to a sentinel DSN — the vault
// keeps the real password, so neither the LLM nor the browser ever touches it.
// Returns the YAML (credentials injected), engine label "builder", repair
// rounds (0 unless slot JSON needed fixing), an error when the slot path
// can't represent the request (caller falls back to LLM-YAML), and the
// sentinel DSNs installed for profiles (excluded from credential_slots).
type slotBuild struct {
	yaml        string
	engine      string
	repairs     int
	profileDSNs []string // 档案解析安装的哨兵 DSN（从 credential_slots 扫描中剔除）
	factsUsed   []string
	keywords    []string // 从槽位拆出的检索词（历史列表）
}

func (s *Server) buildViaSlots(ctx context.Context, client *ai.Client, a config.AIConfig,
	store *ai.SessionStore, sess *ai.Session,
	route aiRouteResult, planMsg string, creds map[string]string,
	forcedSourceProfile, forcedTargetProfile string) (slotBuild, error) {
	reply, err := client.Chat(ctx, ai.SlotsSystemPrompt,
		[]ai.Message{{Role: "user", Content: planMsg}},
		ai.Options{JSONMode: true, Effort: a.PlanEffort(), MaxTokens: a.MaxTokens, Timeout: a.Timeout()})
	if err != nil {
		return slotBuild{}, fmt.Errorf("slots 提取调用失败: %w", err)
	}
	raw, err := ai.ExtractJSON(reply.Content)
	if err != nil {
		return slotBuild{}, fmt.Errorf("slots 回复非 JSON: %w", err)
	}
	var req configbuild.SlotRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return slotBuild{}, fmt.Errorf("slots JSON 结构不符: %w", err)
	}
	// 哨兵协议：槽位密码一律填类型族哨兵（模型无需也不得接触真实密码），
	// 产物 YAML 保留哨兵（非密文、可存储）；真实凭据由调用方在 confirm 时
	// 再次提供并即时注入——会话存储与日志永不接触明文。
	// 对话页选择器的默认档案：仅当 LLM 槽位既没显式给档案、也没给完整 DSN
	// 时才生效（话语显式 > 选择器默认）。
	if forcedSourceProfile != "" && req.Source.Profile == "" && req.Source.DSN == "" {
		req.Source.Profile = forcedSourceProfile
	}
	if forcedTargetProfile != "" && req.Target != nil && req.Target.Profile == "" && req.Target.DSN == "" {
		req.Target.Profile = forcedTargetProfile
	}
	profileDSNs, factsUsed, perr := s.resolveProfilesInSlots(&req)
	if perr != nil {
		return slotBuild{}, perr
	}
	configbuild.FillPasswordSentinels(&req)
	_ = creds
	cfg, err := configbuild.BuildFromSlots(req)
	if err != nil {
		return slotBuild{}, fmt.Errorf("builder: %w", err)
	}
	// 结构校验兜底（builder 产物应恒过；失败也回退 LLM 路径）
	tmp, err := os.CreateTemp("", "owl-slots-*.yaml")
	if err != nil {
		return slotBuild{}, fmt.Errorf("temp: %w", err)
	}
	inter, merr := cfg.MarshalYAML()
	if merr != nil {
		os.Remove(tmp.Name())
		return slotBuild{}, fmt.Errorf("marshal: %w", merr)
	}
	raw2, merr := yaml.Marshal(inter)
	if merr != nil {
		os.Remove(tmp.Name())
		return slotBuild{}, fmt.Errorf("marshal: %w", merr)
	}
	tmp.Write(raw2)
	tmp.Close()
	if _, verr := config.Load(tmp.Name()); verr != nil {
		// builder 保证结构；Load 的个别场景校验差异不阻塞——防御分支放行，
		// 剩余问题由执行阶段暴露。
		os.Remove(tmp.Name())
		return slotBuild{yaml: string(raw2), engine: "builder", repairs: 0, profileDSNs: profileDSNs, factsUsed: factsUsed, keywords: deriveKeywords(&req)}, nil
	}
	os.Remove(tmp.Name())
	// 档案引用进会话槽位（仅名称，无秘密）：confirm 时服务端据此回填真实 DSN。
	profileSlots := map[string]string{}
	if req.Source.Profile != "" {
		profileSlots["source_profile"] = req.Source.Profile
	}
	if req.Target != nil && req.Target.Profile != "" {
		profileSlots["target_profile"] = req.Target.Profile
	}
	if len(profileSlots) > 0 {
		_ = store.MergeSlots(sess, profileSlots)
	}
	return slotBuild{yaml: string(raw2), engine: "builder", repairs: 0, profileDSNs: profileDSNs, factsUsed: factsUsed, keywords: deriveKeywords(&req)}, nil
}

// agentPrerequisiteWarnings checks the agent-channel prerequisites for each
// configured endpoint (explicit agent channel, or a type with no linked native
// driver where auto would fall back to agent): Java, sidecar jar, driver jar.
// Advisory only — the launch preflight in launchJob is the hard gate.
func (s *Server) agentPrerequisiteWarningsForYAML(yamlText string) []string {
	tmp, err := os.CreateTemp("", "owl-agentchk-*.yaml")
	if err != nil {
		return nil
	}
	tmp.WriteString(yamlText)
	tmp.Close()
	cfg, err := config.Load(tmp.Name())
	os.Remove(tmp.Name())
	if err != nil {
		return nil
	}
	return s.agentPrerequisiteWarnings(cfg)
}

func (s *Server) agentPrerequisiteWarnings(cfg *config.Config) []string {
	var out []string
	sides := []struct {
		name string
		db   config.DBConfig
	}{{"源", cfg.Source}, {"目标", cfg.Target}}
	for _, side := range sides {
		if side.db.Type == "" {
			continue
		}
		ch, err := dbconn.ResolveChannel(side.db)
		if err != nil || ch != dbconn.ChannelAgent {
			continue
		}
		label := side.name + "(" + side.db.Type + ")"
		agentCfg := side.db.Agent
		if js := javaStatus(agentCfg.JavaHome); js["found"] != true {
			out = append(out, label+" 走 agent 通道但未找到 java：安装 JRE 或配置 agent.java_home")
		}
		dirs := owljdbc.JarSearchDirs(agentCfg.JarsDir)
		if js := agentJarStatus(dirs, agentCfg.AgentJar); js["found"] != true {
			out = append(out, label+" 的 owl-agent.jar 缺失：首次连接会自动下载；离线环境用 "+
				owlagent.AgentJarURLEnv+" 指向内网镜像或手动放置")
		}
		if _, ok := owljdbc.FindProfileJars(dbconn.AgentProfileType(strings.ToLower(strings.TrimSpace(side.db.Type))), dirs); !ok {
			out = append(out, label+" 缺少 JDBC 驱动 jar：放入 "+agentCfg.JarsDir+"（或工作目录），"+
				"或 bash owljdbc/scripts/fetch-jars.sh 下载常用驱动")
		}
	}
	return out
}

// injectSlotCredentials replaces credential sentinels in endpoint slots with
// the caller-supplied real values (same map contract as the YAML path).
func injectSlotCredentials(req *configbuild.SlotRequest, creds map[string]string) {
	if len(creds) == 0 {
		return
	}
	fix := func(ep *configbuild.EndpointSlots) {
		if v, ok := creds[ep.Password]; ok {
			ep.Password = v
		}
	}
	fix(&req.Source)
	if req.Target != nil {
		fix(req.Target)
	}
}

// resolveSession decides new-session vs continue. Same intent continues with
// slots carried forward; a different intent archives the old session and
// clones a fresh round (facts survive, history does not replay). The returned
// continuity map explains the decision so the client can render
// "已自动开新一轮，原会话仍可引用" instead of the user having to know.
func (s *Server) resolveSession(store *ai.SessionStore, sessionID string, route aiRouteResult, utterance string) (*ai.Session, map[string]any) {
	if sess, ok := s.sessionFrom(sessionID, store); ok {
		if sess.Intent == route.Route {
			return sess, map[string]any{"mode": "continued", "from_session": sess.ID, "reason": "同意图，沿用会话与槽位"}
		}
		next, err := store.CloneForRound(sess, route.Route, route.Sub)
		if err == nil {
			return next, map[string]any{
				"mode": "new_round", "from_session": sess.ID, "session_id": next.ID,
				"reason": "意图从 " + sess.Intent + " 变为 " + route.Route + "：已自动开新一轮（继承库/表等事实槽位，不回放旧对话）；原会话 " + sess.ID + " 保留可引用",
			}
		}
	}
	sess, err := store.Create(route.Route, route.Sub)
	if err != nil {
		return &ai.Session{ID: "", Intent: route.Route, Sub: route.Sub, Slots: map[string]string{}},
			map[string]any{"mode": "stateless", "reason": "会话存储不可用，本轮无记忆"}
	}
	return sess, map[string]any{"mode": "new", "reason": "新会话"}
}

// sessionFrom loads a session; expired/unknown ids mean "start fresh".
func (s *Server) sessionFrom(id string, store *ai.SessionStore) (*ai.Session, bool) {
	if id == "" {
		return nil, false
	}
	return store.Get(id)
}

// generateAndRepair runs the plan loop: draft → inject creds → config.Load →
// feed validation errors back (config may change, intent may not).
func generateAndRepair(ctx context.Context, client *ai.Client, a config.AIConfig, planMsg string, creds map[string]string) (string, int, error) {
	var yamlText string
	for round := 0; ; round++ {
		reply, err := client.Chat(ctx, ai.PlanSystemPrompt, []ai.Message{{Role: "user", Content: planMsg}},
			ai.Options{Effort: a.PlanEffort(), MaxTokens: a.MaxTokens, Timeout: a.Timeout()})
		if err != nil {
			return "", round, fmt.Errorf("AI 配置生成失败: %w", err)
		}
		raw, err := ai.ExtractYAML(reply.Content)
		if err != nil {
			return "", round, fmt.Errorf("AI 回复不含 YAML: %w", err)
		}
		yamlText = ai.InjectCredentials(raw, creds)

		tmp, err := os.CreateTemp("", "owl-plan-*.yaml")
		if err != nil {
			return "", round, err
		}
		tmp.WriteString(yamlText)
		tmp.Close()
		_, loadErr := config.Load(tmp.Name())
		os.Remove(tmp.Name())
		if loadErr == nil {
			return yamlText, round, nil
		}
		if round >= a.MaxRepairRounds {
			return "", round, fmt.Errorf("配置在 %d 轮修复后仍无法通过校验: %s", round, loadErr)
		}
		planMsg += "\n\n【上一次输出的 YAML 未通过工具校验，错误如下。请修正后重新输出完整 YAML（只输出 YAML 本体）。只许修正配置内容，不许更换任务意图】\n" + loadErr.Error()
	}
}

// maskYAML replaces every injected credential with ****** so drafts can be
// shown without echoing secrets; a protocol-violating literal password is
// masked as a fallback too.
func maskYAML(yamlText string, creds map[string]string) string {
	for _, v := range creds {
		if v != "" {
			yamlText = strings.ReplaceAll(yamlText, v, "******")
		}
	}
	return regexp.MustCompile(`(?i)(password[=:]\s*)([^\s"&]+)`).ReplaceAllString(yamlText, "${1}******")
}

// remainingPlaceholders lists placeholders with no injected value: the draft
// is structurally valid but not yet executable until they are filled.
func remainingPlaceholders(yamlText string, creds map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range regexp.MustCompile(`__PWD_[a-z]+__`).FindAllString(yamlText, -1) {
		if _, ok := creds[m]; !ok && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// yamlSlots lifts the generated YAML's key facts back into session slots —
// deterministic parse, no extra LLM call.
func yamlSlots(yamlText string) map[string]string {
	var doc struct {
		Source struct {
			Type    string `yaml:"type"`
			DSN     string `yaml:"dsn"`
			Schema  string `yaml:"schema"`
			Channel string `yaml:"channel"`
		} `yaml:"source"`
		Target struct {
			Type string `yaml:"type"`
		} `yaml:"target"`
		Export struct {
			Format string `yaml:"format"`
		} `yaml:"export"`
		DDL struct {
			TargetDialect string `yaml:"target_dialect"`
		} `yaml:"ddl"`
	}
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		return nil
	}
	slots := map[string]string{
		"source_type": doc.Source.Type, "source_dsn": doc.Source.DSN,
		"source_schema": doc.Source.Schema, "channel": doc.Source.Channel,
		"target_type": doc.Target.Type, "format": doc.Export.Format,
		"target_dialect": doc.DDL.TargetDialect,
	}
	for k, v := range slots {
		if v == "" {
			delete(slots, k)
		}
	}
	return slots
}

// buildRouteUserMessage mirrors the eval harness message shape exactly.
func buildRouteUserMessage(utterance string, context []string) string {
	var b strings.Builder
	if len(context) > 0 {
		b.WriteString("【对话上文】\n")
		for _, t := range context {
			b.WriteString("- " + strings.TrimSpace(t) + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("【用户最新一句话】\n")
	b.WriteString(utterance)
	return b.String()
}

// buildPlanUserMessage carries the utterance plus known slots, so round 2
// ("再导一份 xlsx") resolves against round 1's facts without replaying the
// whole conversation.
func buildPlanUserMessage(utterance string, sess *ai.Session, route aiRouteResult) string {
	var b strings.Builder
	if len(sess.Slots) > 0 {
		b.WriteString("【已知槽位（历史轮次延续，未显式修改则沿用）】\n")
		keys := make([]string, 0, len(sess.Slots))
		for k := range sess.Slots {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString("- " + k + ": " + sess.Slots[k] + "\n")
		}
		b.WriteString("\n")
	}
	if route.Sub != "" {
		b.WriteString("【意图变体】" + route.Route + " / " + route.Sub + "\n")
	}
	b.WriteString("【用户最新一句话】\n")
	b.WriteString(utterance)
	return b.String()
}
