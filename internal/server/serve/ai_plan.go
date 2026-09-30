package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/ai"
	"github.com/cangyunye/go-owl-migrate/internal/config"
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
		Context     []string          `json:"context"`              // 显式前文（与 /ai/route 同语义）
		SessionID   string            `json:"session_id"`           // 缺省 = 新会话
		Credentials map[string]string `json:"credentials,omitempty"` // "__PWD_mysql__" → 真实密码（服务端注入，不入会话/日志/响应）
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
	key := a.APIKey()
	if key == "" {
		writeError(w, http.StatusServiceUnavailable,
			"AI 未配置：设置环境变量 "+a.APIKeyEnv+"（或 DEEPSEEK_API_KEY），或在配置里加 ai 段")
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
	if len(req.Credentials) > 0 {
		// 凭据由调用方带外提供（占位符注入），路由器不得因"缺密码"而澄清。
		routeUser += "\n\n【凭据说明】连接密码/凭据由调用方另行提供（服务端占位符注入），缺少密码不构成澄清理由。"
	}
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
		})
		return
	}

	// ── 第 2 段：配置生成（占位符协议 + config.Load 校验 + 修复回路） ──
	planMsg := buildPlanUserMessage(req.Utterance, sess, route)
	yamlText, repairs, genErr := generateAndRepair(ctx, client, a, planMsg, req.Credentials)
	if genErr != nil {
		writeError(w, http.StatusBadGateway, genErr.Error())
		return
	}

	planID := ai.NewSessionID()
	masked := maskYAML(yamlText, req.Credentials)
	store.MergeSlots(sess, yamlSlots(yamlText))
	store.AddArtifact(sess, ai.Artifact{Kind: "plan", ID: planID, YAML: masked})
	store.SetStage(sess, ai.StageConfirming)
	store.AddTurn(sess, "user", req.Utterance)
	store.AddTurn(sess, "assistant", "已生成配置草案 "+planID+"（等待确认执行）")

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"session_id":    sess.ID,
		"session":       sess,
		"continuity":    continuity,
		"route":         route.Route,
		"sub":           route.Sub,
		"plan_id":       planID,
		"yaml":          masked, // 脱敏预览；含真实凭据的版本只在服务端内存/会话产物里
		"repair_rounds": repairs,
		"warnings":      remainingPlaceholders(yamlText, req.Credentials),
		"next":          "确认后经任务端点执行（执行时凭据由服务端注入，浏览器不接触）",
	})
}

// resolveSession decides new-session vs continue. Same intent continues with
// slots carried forward; a different intent archives the old session and
// clones a fresh round (facts survive, history does not replay). The returned
// continuity map explains the decision so the client can render
// "已自动开新一轮，原会话仍可引用" instead of the user having to know.
func (s *Server) resolveSession(store *ai.SessionStore, sessionID string, route aiRouteResult, utterance string) (*ai.Session, map[string]any) {
	if sess, ok := s.sessionFrom(sessionID, store); ok {
		if sess.Intent == route.Route {
			store.AddTurn(sess, "user", utterance)
			return sess, map[string]any{"mode": "continued", "from_session": sess.ID, "reason": "同意图，沿用会话与槽位"}
		}
		next, err := store.CloneForRound(sess, route.Route, route.Sub)
		if err == nil {
			store.AddTurn(next, "user", utterance)
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
	store.AddTurn(sess, "user", utterance)
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
