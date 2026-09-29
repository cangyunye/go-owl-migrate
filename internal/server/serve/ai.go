package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/ai"
	"github.com/cangyunye/go-owl-migrate/internal/config"
)

// aiSettings resolves the effective AI vendor settings: the active config's
// ai section with defaults applied. Zero config still yields the deepseek
// preset, so provisioning is just "set the key env var".
func (s *Server) aiSettings() config.AIConfig {
	a := s.cfg.AI
	a.ApplyDefaults()
	return a
}

// handleAIStatus reports whether the routing layer is provisioned. Cheap:
// no vendor call is made. Key material is never echoed — only presence.
func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	a := s.aiSettings()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":  a.APIKey() != "",
		"provider": a.Provider,
		"model":    a.Model,
		"base_url": a.BaseURL,
		"effort":   a.Effort,
		"key_set":  a.APIKey() != "",
		"key_env":  a.APIKeyEnv,
	})
}

// handleAIRoute routes one natural-language utterance to a tool command via
// the vendor LLM. Stateless prototype: multi-turn context is passed verbatim
// as prior turn strings; the structured session object (design §4) lands later.
func (s *Server) handleAIRoute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Utterance string   `json:"utterance"`
		Context   []string `json:"context"` // prior user/assistant turns, oldest first
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
			"AI 路由未配置：设置环境变量 "+a.APIKeyEnv+"（或 DEEPSEEK_API_KEY），或在配置里加 ai 段")
		return
	}

	var user strings.Builder
	if len(req.Context) > 0 {
		user.WriteString("【对话上文】\n")
		for _, turn := range req.Context {
			user.WriteString("- " + strings.TrimSpace(turn) + "\n")
		}
		user.WriteString("\n")
	}
	user.WriteString("【用户最新一句话】\n")
	user.WriteString(req.Utterance)

	timeout := a.Timeout()
	ctx, cancel := context.WithTimeout(r.Context(), timeout+15*time.Second)
	defer cancel()

	client := ai.NewClient(a.BaseURL, key, a.Model, timeout)
	reply, err := client.Chat(ctx, ai.RouterSystemPrompt,
		[]ai.Message{{Role: "user", Content: user.String()}},
		ai.Options{JSONMode: true, Effort: a.Effort, MaxTokens: a.MaxTokens, Timeout: timeout})
	if err != nil {
		writeError(w, http.StatusBadGateway, "AI 路由失败: "+err.Error())
		return
	}

	raw, err := ai.ExtractJSON(reply.Content)
	if err != nil {
		writeError(w, http.StatusBadGateway, "AI 回复不是合法 JSON: "+err.Error())
		return
	}
	var route struct {
		Route        string   `json:"route"`
		Sub          string   `json:"sub"`
		Confidence   string   `json:"confidence"`
		MissingSlots []string `json:"missing_slots"`
		OutOfScope   bool     `json:"out_of_scope"`
		NeedsClarify bool     `json:"needs_clarify"`
		Reason       string   `json:"reason"`
	}
	if err := json.Unmarshal(raw, &route); err != nil {
		writeError(w, http.StatusBadGateway, "AI 回复 JSON 结构不符: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"route":  route.Route,
		"result": route,
		"usage":  reply.Usage,
		"model":  a.Model,
	})
}
