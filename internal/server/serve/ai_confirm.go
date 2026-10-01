package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/ai"
	"github.com/cangyunye/go-owl-migrate/internal/config"
)

// intentJobType maps a routed intent to the job type its confirm can launch.
// Generation-style intents are not jobs — they only activate the config.
func intentJobType(intent string) (string, bool) {
	switch intent {
	case "migrate":
		return "migrate", true
	case "export-data":
		return "export", true
	case "import":
		return "import", true
	default:
		return "", false
	}
}

// handleAIPlanConfirm turns a confirmed plan into reality: activate the plan's
// config as the server's active configuration and — optionally — launch the
// migration/export/import job. The plan YAML (with real credentials, stored
// server-side only) is re-validated before activation; the browser never sees
// it unmasked.
//
// Body: {"session_id", "plan_id", "execute": bool}
//   - execute=false → activate only; the response names the next endpoint.
//   - execute=true  → activate + start the job for the plan's intent
//     (migrate / export-data / import). Generation-style intents
//     (export-ddl, gen-select, …) only activate — they are generation
//     endpoints, not jobs.
func (s *Server) handleAIPlanConfirm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID   string            `json:"session_id"`
		PlanID      string            `json:"plan_id"`
		Execute     bool              `json:"execute"`
		Credentials map[string]string `json:"credentials,omitempty"`
	}
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}
	if req.SessionID == "" || req.PlanID == "" {
		writeError(w, http.StatusBadRequest, "session_id and plan_id are required")
		return
	}
	store, err := s.aiSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "会话存储不可用: "+err.Error())
		return
	}
	sess, ok := store.Get(req.SessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "会话不存在或已过期（TTL 24h）；请重新生成计划")
		return
	}
	if sess.Stage != ai.StageConfirming {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("会话阶段为 %q，只有 confirming 状态可确认（请先用 /ai/plan 生成计划）", sess.Stage))
		return
	}
	var plan *ai.Artifact
	for i := range sess.Artifacts {
		a := &sess.Artifacts[i]
		if a.Kind == "plan" && a.ID == req.PlanID {
			plan = a
			break
		}
	}
	if plan == nil {
		writeError(w, http.StatusNotFound, "该会话下没有此 plan_id 的计划草案")
		return
	}

	// 双重校验：激活前重新走 config.Load——与会话存储隔离，防止存储内容被
	// 手工改动后带病激活。
	tmp, err := os.CreateTemp("", "owl-confirm-*.yaml")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 凭据注入：计划草案携带哨兵占位符，确认时由调用方再次提供真实值，
	// 注入后立即使用（激活的配置/worker 拿到真实 DSN；会话与日志仍存哨兵版）。
	tmp.WriteString(ai.InjectCredentials(plan.YAML, req.Credentials))
	tmp.Close()
	cfg, loadErr := config.Load(tmp.Name())
	os.Remove(tmp.Name())
	if loadErr != nil {
		writeError(w, http.StatusConflict, "计划配置未通过校验，已拒绝激活: "+loadErr.Error())
		return
	}

	// 激活：替换活动配置并持久化（与 PUT /config 同一持久化路径）。
	// ai: 段是部署级配置（供应商/密钥环境名），不属于迁移计划——保留现值，
	// 否则激活后 /ai 端点会回退到默认供应商。
	s.mu.Lock()
	cfg.AI = s.cfg.AI
	s.cfg = cfg
	s.mu.Unlock()
	if _, err := s.persistConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}

	jobType, launchable := intentJobType(routeIntentOfSession(sess))
	if req.Execute {
		if !launchable {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("意图 %q 不是任务型路由（生成器/校验类），只激活了配置；请使用对应生成端点", routeIntentOfSession(sess)))
			return
		}
		status, body, err := s.launchJob(jobType, nil)
		if err != nil || status >= 400 {
			msg := string(body)
			if err != nil {
				msg = err.Error()
			}
			writeError(w, status, "配置已激活，但任务启动失败: "+msg)
			return
		}
		var jobResp struct {
			JobID  string `json:"job_id"`
			Status string `json:"status"`
		}
		if uerr := json.Unmarshal(body, &jobResp); uerr != nil {
			writeError(w, http.StatusBadGateway, "master 响应解析失败: "+uerr.Error()+" body="+string(body))
			return
		}
		if jobResp.JobID == "" {
			writeError(w, http.StatusBadGateway, "master 响应缺 job_id: "+string(body))
			return
		}
		store.SetStage(sess, ai.StageExecuting)
		store.AddArtifact(sess, ai.Artifact{Kind: "job", ID: jobResp.JobID})
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"session_id": sess.ID,
			"plan_id":    plan.ID,
			"activated":  true,
			"job_id":     jobResp.JobID,
			"job_type":   jobType,
			"next":       "进度：GET /api/v1/jobs/" + jobResp.JobID + "（或任务页 #/jobs）",
		})
		return
	}

	// 激活即确认流程终结（无论是否启动任务），防重复确认。
	store.SetStage(sess, ai.StageDone)
	store.AddTurn(sess, "assistant", "计划 "+plan.ID+" 已激活为当前配置（未执行）")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"session_id": sess.ID,
		"plan_id":   plan.ID,
		"activated": true,
		"job_type":  jobType, // 空表示该意图非任务型
		"next": nextStepHint(jobType),
	})
}

// routeIntentOfSession returns the session's routed intent (lowercase).
func routeIntentOfSession(sess *ai.Session) string {
	return strings.ToLower(strings.TrimSpace(sess.Intent))
}

func nextStepHint(jobType string) string {
	if jobType == "" {
		return "配置已激活；该意图走生成端点（/ddl/generate、/select/generate 等）"
	}
	return "配置已激活；执行：POST /api/v1/" + jobType + "（或任务页 #/jobs）"
}
