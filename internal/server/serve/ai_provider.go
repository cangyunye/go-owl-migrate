package serve

// AI 供应商自管理端点：配置段更新、API Key 入 vault、模型探测、连通性测试。
// 与会话/计划的只读消费互补——本文件让供应商配置在 web UI 里自洽闭环：
//   - Key 永不进配置文件/HTTP 响应/日志：保存即经 vault 加密落本机
//     ~/.owl/migrate/ai/api_key.json（0600），只在服务端发请求那一刻解密；
//   - Key 解析优先级：vault（UI 保存）> api_key_env 环境变量 > DEEPSEEK_API_KEY；
//   - 模型探测打 OpenAI 兼容 {base_url}/models，失败可手填兜底。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/ai"
	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/paths"
)

// aiKeyFilePath is the local vault-encrypted key store, kept next to the
// sessions DB under ~/.owl/migrate/ai/ so tests isolate it the same way.
func (s *Server) aiKeyFilePath() string {
	dir := s.aiSessionsDir
	if dir == "" {
		dir = paths.AISessionsDir()
	}
	return filepath.Join(filepath.Dir(filepath.Clean(dir)), "api_key.json")
}

// aiVaultKey reads the saved key (decrypting locally); empty when absent.
func (s *Server) aiVaultKey() string {
	data, err := os.ReadFile(s.aiKeyFilePath())
	if err != nil {
		return ""
	}
	var rec struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(data, &rec) != nil || rec.Key == "" {
		return ""
	}
	vault, err := s.configVault()
	if err != nil {
		return ""
	}
	plain, err := vault.Decrypt(rec.Key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(plain)
}

// aiKeyMaterial resolves the effective key and its source:
// "vault" (UI-saved) > "env" (api_key_env / DEEPSEEK_API_KEY) > "none".
func (s *Server) aiKeyMaterial() (key, source string) {
	if k := s.aiVaultKey(); k != "" {
		return k, "vault"
	}
	a := s.aiSettings()
	if k := a.APIKey(); k != "" {
		return k, "env"
	}
	return "", "none"
}

// handleAISaveKey: POST /api/v1/ai/key {"key":"..."} — encrypt and persist
// locally. 更新即覆盖；响应与日志都不含 key 本体。
func (s *Server) handleAISaveKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		writeError(w, http.StatusBadRequest, "key 不能为空")
		return
	}
	vault, err := s.configVault()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "凭据库不可用: "+err.Error())
		return
	}
	ct, err := vault.Encrypt(req.Key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "key 加密失败: "+err.Error())
		return
	}
	data, _ := json.Marshal(map[string]string{"key": ct})
	// ai/ 目录可能尚未被会话库初始化——写入前确保存在。
	if err := os.MkdirAll(filepath.Dir(s.aiKeyFilePath()), 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "key 保存失败: "+err.Error())
		return
	}
	if err := os.WriteFile(s.aiKeyFilePath(), data, 0600); err != nil {
		writeError(w, http.StatusInternalServerError, "key 保存失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key_source": "vault"})
}

// handleAIDeleteKey: DELETE /api/v1/ai/key — remove the saved key; resolution
// falls back to the env chain (or the AI layer becomes unprovisioned).
func (s *Server) handleAIDeleteKey(w http.ResponseWriter, r *http.Request) {
	err := os.Remove(s.aiKeyFilePath())
	if err != nil && !os.IsNotExist(err) {
		writeError(w, http.StatusInternalServerError, "key 删除失败: "+err.Error())
		return
	}
	_, source := s.aiKeyMaterial()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key_source": source})
}

// handleAIUpdateProvider: PUT /api/v1/ai/provider — update the active ai
// config section (provider preset / base_url / model / advanced params) and
// persist. 只写用户提供的字段，空值继续走读取时 ApplyDefaults。
func (s *Server) handleAIUpdateProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider        *string `json:"provider"`
		BaseURL         *string `json:"base_url"`
		Model           *string `json:"model"`
		APIKeyEnv       *string `json:"api_key_env"`
		Effort          *string `json:"effort"`
		PlanEffort      *string `json:"plan_effort"`
		MaxTokens       *int    `json:"max_tokens"`
		ContextWindow   *int    `json:"context_window"`
		Timeout         *string `json:"timeout"`
		MaxRepairRounds *int    `json:"max_repair_rounds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}

	s.mu.Lock()
	a := s.cfg.AI
	if req.Provider != nil {
		p := strings.TrimSpace(*req.Provider)
		if !config.ValidAIProvider(p) {
			s.mu.Unlock()
			writeError(w, http.StatusBadRequest, "未知供应商预设: "+p)
			return
		}
		// 换预设且未显式给 base_url：重置为空，ApplyDefaults 吃新预设默认
		// （上一家的端点地址不该悄悄留给新供应商）。
		if p != a.Provider && req.BaseURL == nil {
			a.BaseURL = ""
		}
		a.Provider = p
	}
	if req.BaseURL != nil {
		u := strings.TrimSpace(*req.BaseURL)
		if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			s.mu.Unlock()
			writeError(w, http.StatusBadRequest, "base_url 必须以 http(s):// 开头")
			return
		}
		a.BaseURL = u
	}
	if req.Model != nil {
		a.Model = strings.TrimSpace(*req.Model)
	}
	if req.APIKeyEnv != nil {
		a.APIKeyEnv = strings.TrimSpace(*req.APIKeyEnv)
	}
	if req.Effort != nil && *req.Effort != "" && !config.ValidAIEfforts[*req.Effort] {
		s.mu.Unlock()
		writeError(w, http.StatusBadRequest, "effort 只支持 low/high/max")
		return
	}
	if req.Effort != nil {
		a.Effort = strings.TrimSpace(*req.Effort)
	}
	if req.PlanEffort != nil && *req.PlanEffort != "" && !config.ValidAIEfforts[*req.PlanEffort] {
		s.mu.Unlock()
		writeError(w, http.StatusBadRequest, "plan_effort 只支持 low/high/max")
		return
	}
	if req.PlanEffort != nil {
		a.PlanEffortStr = strings.TrimSpace(*req.PlanEffort)
	}
	if req.MaxTokens != nil {
		a.MaxTokens = *req.MaxTokens
	}
	if req.ContextWindow != nil {
		a.ContextWindow = *req.ContextWindow
	}
	if req.Timeout != nil {
		a.TimeoutStr = strings.TrimSpace(*req.Timeout)
	}
	if req.MaxRepairRounds != nil {
		a.MaxRepairRounds = *req.MaxRepairRounds
	}
	a.ApplyDefaults()
	// model 允许为空（分步保存：先选预设存 URL/key，再探测填模型）；
	// 未就绪态由 /ai/status 的 enabled=false 表达，路由/计划另有守卫。
	s.cfg.AI = a
	s.mu.Unlock()

	if _, err := s.persistConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}
	s.writeAIProvider(w)
}

// handleAIProvider: GET /api/v1/ai/provider — the editable form values
// (defaults applied) plus key provenance; never the key itself.
func (s *Server) handleAIProvider(w http.ResponseWriter, r *http.Request) {
	s.writeAIProvider(w)
}

func (s *Server) writeAIProvider(w http.ResponseWriter) {
	a := s.aiSettings()
	_, keySource := s.aiKeyMaterial()
	writeJSON(w, http.StatusOK, map[string]any{
		"provider":          a.Provider,
		"base_url":          a.BaseURL,
		"model":             a.Model,
		"api_key_env":       a.APIKeyEnv,
		"key_source":        keySource,
		"key_set":           keySource != "none",
		"effort":            a.Effort,
		"plan_effort":       a.PlanEffort(),
		"max_tokens":        a.MaxTokens,
		"context_window":    a.ContextWindow,
		"timeout":           a.Timeout().String(),
		"max_repair_rounds": a.MaxRepairRounds,
		"presets":           config.AIProviderPresets,
		"preset_base_urls":  config.DefaultAIBaseURL,
	})
}

// effectiveAIKey picks the key for one-shot vendor calls (test/models):
// 请求体里的 key（输入框已粘贴但未保存的）> vault > env。来源标注给前端展示。
func effectiveAIKey(reqKey string, a config.AIConfig, s *Server) (key, source string) {
	if k := strings.TrimSpace(reqKey); k != "" {
		return k, "input"
	}
	return s.aiKeyMaterial()
}

// handleAIModels: POST /api/v1/ai/models {"base_url":"...","key":"..."} — probe the
// OpenAI-compatible /models listing with the effective key. base_url 缺省用
// 当前配置；endpoint 不支持时前端允许手填模型兜底。
func (s *Server) handleAIModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL string `json:"base_url"`
		Key     string `json:"key"`
	}
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}
	a := s.aiSettings()
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		baseURL = a.BaseURL
	}
	if baseURL == "" {
		writeError(w, http.StatusBadRequest, "base_url 未设置")
		return
	}
	key, keySource := effectiveAIKey(req.Key, a, s)
	if key == "" {
		writeError(w, http.StatusServiceUnavailable,
			"API Key 未设置：先在配置页保存 Key，或设置环境变量 "+a.APIKeyEnv)
		return
	}

	ctx := r.Context()
	client := ai.NewClient(strings.TrimRight(baseURL, "/"), key, "", 15*time.Second)
	models, err := client.ListModels(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway,
			fmt.Sprintf("模型列表获取失败（%s key）: %s", keySource, err.Error()))
		return
	}
	sort.Strings(models)
	resp := map[string]any{"ok": true, "models": models}
	if client.EffectiveBase() != strings.TrimRight(baseURL, "/") {
		resp["effective_base_url"] = client.EffectiveBase()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleAITest: POST /api/v1/ai/test {"base_url":"...","model":"..."} — one
// minimal JSON-mode chat call. 路由器硬依赖 response_format，探针把不兼容
// 端点在保存时暴露成明确错误，而不是等到第一次 /ai/plan 才炸。
func (s *Server) handleAITest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL string `json:"base_url"`
		Model   string `json:"model"`
		Key     string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	a := s.aiSettings()
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		baseURL = a.BaseURL
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = a.Model
	}
	if baseURL == "" || model == "" {
		writeError(w, http.StatusBadRequest, "base_url / model 未设置")
		return
	}
	key, keySource := effectiveAIKey(req.Key, a, s)
	if key == "" {
		writeError(w, http.StatusServiceUnavailable,
			"API Key 未设置：先在配置页保存 Key，或设置环境变量 "+a.APIKeyEnv)
		return
	}

	client := ai.NewClient(strings.TrimRight(baseURL, "/"), key, model, 30*time.Second)
	start := time.Now()
	_, err := client.Chat(r.Context(), "只回复 JSON。",
		[]ai.Message{{Role: "user", Content: `ping，请回复 {"ok":true}`}},
		ai.Options{JSONMode: true, MaxTokens: 512, Timeout: 30 * time.Second})
	latency := time.Since(start).Milliseconds()
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "response_format") || strings.Contains(msg, "json_object") {
			msg += "（该端点可能不支持 JSON 模式：AI 路由依赖它，请换用支持的模型/端点）"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "model": model, "latency_ms": latency, "error": msg,
		})
		return
	}
	okResp := map[string]any{
		"ok": true, "model": model, "latency_ms": latency, "key_source": keySource,
	}
	if client.EffectiveBase() != strings.TrimRight(baseURL, "/") {
		okResp["effective_base_url"] = client.EffectiveBase()
	}
	writeJSON(w, http.StatusOK, okResp)
}
