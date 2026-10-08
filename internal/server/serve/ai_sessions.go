package serve

// AI 会话历史 / 恢复 / 复用端点。会话是检索与复用的一等公民：
//   - 列表：确定性关键词检索（keywords 列 + 分词 SQL 匹配，无向量）
//   - 恢复：GET 返回完整恢复包（turns + 最近草案 + credential_slots），
//     前端切页/刷新后据此还原对话
//   - 克隆：继承事实槽位开新一轮（源头未有效则淘汰）
//   - 取用：把会话产物中的加密配置解密并激活为当前配置

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/ai"
	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/configbuild"
	"github.com/cangyunye/go-owl-migrate/internal/dscrypto"
	"github.com/cangyunye/go-owl-migrate/internal/dsnfields"
	"github.com/cangyunye/go-owl-migrate/internal/paths"
)

// configVault lazily builds the AES vault used to encrypt session config
// artifacts. Same key material as the datasource store (the .ds_key in the
// owl home dir) — one secret governs all stored credentials.
func (s *Server) configVault() (*dscrypto.Vault, error) {
	s.vaultOnce.Do(func() {
		dir := s.dataSourcesDir
		if dir == "" {
			dir = paths.DataSourcesDir()
		}
		v, err := dscrypto.New(filepath.Dir(dir))
		if err != nil {
			s.vaultErr = err
			return
		}
		s.vault = v
	})
	return s.vault, s.vaultErr
}

// handleListAISessions: GET /api/v1/ai/sessions?limit=&offset=&q=
// 分页协议：前端每批多取 1 条探测 has_more，本端只管 limit/offset 平移。
func (s *Server) handleListAISessions(w http.ResponseWriter, r *http.Request) {
	store, err := s.aiSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "会话存储不可用: "+err.Error())
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n := atoiDefault(v, 50); n > 0 {
			limit = n
		}
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n := atoiDefault(v, 0); n > 0 {
			offset = n
		}
	}
	items, err := store.ListSessions(limit, offset, r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []ai.SessionSummary{}
	}
	writeJSON(w, http.StatusOK, items)
}

func atoiDefault(v string, dflt int) int {
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return dflt
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return dflt
	}
	return n
}

// handleDeleteAISession: DELETE /api/v1/ai/session/{id} — remove one session
// from the history list (soft delete; rows stay until the TTL purge).
func (s *Server) handleDeleteAISession(w http.ResponseWriter, r *http.Request) {
	store, err := s.aiSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "会话存储不可用: "+err.Error())
		return
	}
	id := r.PathValue("id")
	if _, ok := store.Get(id); !ok {
		writeError(w, http.StatusNotFound, "会话不存在或已删除")
		return
	}
	n, err := store.Delete(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": n})
}

// handleBatchDeleteAISessions: POST /api/v1/ai/sessions/batch-delete
// {"ids":["s-...","s-..."]} — soft-delete a batch; unknown ids are skipped
// and reported through the deleted count.
func (s *Server) handleBatchDeleteAISessions(w http.ResponseWriter, r *http.Request) {
	store, err := s.aiSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "会话存储不可用: "+err.Error())
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids 不能为空")
		return
	}
	if len(req.IDs) > 500 {
		writeError(w, http.StatusBadRequest, "单次最多删除 500 个会话")
		return
	}
	n, err := store.Delete(req.IDs...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "批量删除失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": n})
}

// handleGetAISession: GET /api/v1/ai/session/{id} — full restore package.
func (s *Server) handleGetAISession(w http.ResponseWriter, r *http.Request) {
	store, err := s.aiSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "会话存储不可用: "+err.Error())
		return
	}
	sess, ok := store.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "会话不存在或已过期")
		return
	}
	resp := map[string]any{"session": sess}

	// 最近一版计划：恢复确认卡（YAML 已脱敏可展示；credential_slots 按档案
	// 侧剔除——与计划时同一规则）。
	var lastPlan *ai.Artifact
	var lastJob *ai.Artifact
	var lastConfig *ai.Artifact
	for i := len(sess.Artifacts) - 1; i >= 0; i-- {
		a := &sess.Artifacts[i]
		switch a.Kind {
		case "plan":
			if lastPlan == nil {
				lastPlan = a
			}
		case "job":
			if lastJob == nil {
				lastJob = a
			}
		case "config":
			if lastConfig == nil {
				lastConfig = a
			}
		}
	}
	if lastPlan != nil && sess.Stage == ai.StageConfirming {
		credSlots := remainingPlaceholders(s.stripProfileSentinels(lastPlan.YAML, sess), nil)
		resp["last_plan"] = map[string]any{
			"plan_id":          lastPlan.ID,
			"yaml":             lastPlan.YAML,
			"credential_slots": credSlots,
		}
	}
	if lastJob != nil {
		resp["last_job"] = map[string]any{"job_id": lastJob.ID, "created": lastJob.CreatedAt}
	}
	if lastConfig != nil {
		resp["has_config"] = true
	}
	writeJSON(w, http.StatusOK, resp)
}

// stripProfileSentinels removes sentinel DSNs that came from datasource
// profiles (recorded as source_profile/target_profile slots) so the UI does
// not prompt for passwords the vault already holds.
func (s *Server) stripProfileSentinels(yamlText string, sess *ai.Session) string {
	out := yamlText
	for _, slot := range []string{"source_profile", "target_profile"} {
		name := strings.TrimSpace(sess.Slots[slot])
		if name == "" {
			continue
		}
		dsStore, err := s.dsStore()
		if err != nil {
			continue
		}
		typ, _, dsn, err := dsStore.Resolve(name)
		if err != nil {
			continue
		}
		if sentinel := configbuild.PasswordSentinelFor(typ); sentinel != "" {
			if masked := config.ReplaceDSNPassword(dsn, sentinel); masked != dsn {
				out = strings.ReplaceAll(out, masked, "")
			}
		}
	}
	return out
}

// handleCloneAISession: POST /api/v1/ai/session/{id}/clone
func (s *Server) handleCloneAISession(w http.ResponseWriter, r *http.Request) {
	store, err := s.aiSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "会话存储不可用: "+err.Error())
		return
	}
	sess, ok := store.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "会话不存在或已过期")
		return
	}
	var req struct {
		Intent string `json:"intent"`
		Sub    string `json:"sub"`
	}
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	intent := req.Intent
	if intent == "" {
		intent = sess.Intent
	}
	next, err := store.CloneForRound(sess, intent, req.Sub)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "克隆会话失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"session_id":   next.ID,
		"from_session": sess.ID,
		"intent":       next.Intent,
		"slots":        next.Slots,
		"next":         "已继承事实槽位，直接描述这一轮要做什么",
	})
}

// handleApplyAISession: POST /api/v1/ai/session/{id}/apply — decrypt the
// session's stored config and activate it as the current configuration.
func (s *Server) handleApplyAISession(w http.ResponseWriter, r *http.Request) {
	store, err := s.aiSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "会话存储不可用: "+err.Error())
		return
	}
	sess, ok := store.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "会话不存在或已过期")
		return
	}
	var enc *ai.Artifact
	for i := len(sess.Artifacts) - 1; i >= 0; i-- {
		if sess.Artifacts[i].Kind == "config" {
			enc = &sess.Artifacts[i]
			break
		}
	}
	if enc == nil {
		writeError(w, http.StatusBadRequest, "该会话没有可取用的配置（只有成功启动过任务的会话才会保存）")
		return
	}
	vault, err := s.configVault()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "凭据库不可用: "+err.Error())
		return
	}
	plain, err := vault.Decrypt(enc.YAML)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "配置解密失败: "+err.Error())
		return
	}
	// 残缺配置（手填密码从未注入）拒绝激活。
	if remaining := remainingPlaceholders(plain, nil); len(remaining) > 0 {
		writeError(w, http.StatusBadRequest,
			"该会话的配置缺少连接凭据（"+strings.Join(remaining, ", ")+"）；请重新生成计划并填写密码")
		return
	}
	if err := s.activateConfigYAML(w, plain); err != nil {
		return // activateConfigYAML already wrote the error
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"next": "配置已激活；可在配置页微调或直接去执行页运行",
	})
}

// activateConfigYAML validates and activates a full config YAML (same path
// as confirm). Writes the error response and returns it on failure.
func (s *Server) activateConfigYAML(w http.ResponseWriter, yamlText string) error {
	tmp, err := os.CreateTemp("", "owl-apply-*.yaml")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return err
	}
	tmp.WriteString(yamlText)
	tmp.Close()
	cfg, loadErr := config.Load(tmp.Name())
	os.Remove(tmp.Name())
	if loadErr != nil {
		writeError(w, http.StatusConflict, "配置未通过校验，已拒绝激活: "+loadErr.Error())
		return loadErr
	}
	s.mu.Lock()
	cfg.AI = s.cfg.AI
	s.cfg = cfg
	s.mu.Unlock()
	if _, err := s.persistConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return err
	}
	return nil
}

// resolveProfileIdentity renders the masked connection identity of a stored
// profile ("appuser@127.0.0.1:1521/XEPDB1") for facts_used display — the
// user can see WHO a profile connects as, without the password.
func resolveProfileIdentity(typ, dsn string) string {
	if f, err := dsnfields.Decompose(typ, dsn); err == nil && f != nil {
		id := f.Username
		if id != "" {
			id += "@"
		}
		id += f.Host
		if f.Port != "" {
			id += ":" + f.Port
		}
		if f.Database != "" {
			id += "/" + f.Database
		}
		return id
	}
	// Fallback: masked DSN with credentials stripped.
	masked := config.MaskDSN(dsn)
	masked = strings.ReplaceAll(masked, ":******", "")
	return strings.TrimPrefix(masked, typ+"://")
}
