package serve

import (
	"fmt"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cangyunye/owljdbc"

	"github.com/cangyunye/go-owl-migrate/internal/owlagent"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
)

var masterClient = &http.Client{Timeout: 10 * time.Second}

// startJob relays a job-start request to the master IPC server using the
// currently loaded config. The master spawns a worker child process and
// returns the job descriptor.
func (s *Server) startJob(w http.ResponseWriter, r *http.Request, jobType string) {
	if s.masterURL == "" {
		writeError(w, http.StatusServiceUnavailable, "master IPC not configured")
		return
	}

	var body struct {
		Mode            string `json:"mode"`
		SkipDDL         bool   `json:"skip_ddl"`
		ContinueOnError bool   `json:"continue_on_error"`
	}
	if !decodeJSON(w, r, &body, maxBodyBytes) {
		return
	}

	extra := map[string]any{}
	if body.Mode != "" {
		extra["mode"] = body.Mode
	}
	if body.SkipDDL {
		extra["skip_ddl"] = true
	}
	if body.ContinueOnError {
		extra["continue_on_error"] = true
	}
	if resumeFrom := r.URL.Query().Get("resume_from"); resumeFrom != "" {
		extra["resume_from"] = resumeFrom
	}

	status, respBody, err := s.launchJob(jobType, extra)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(respBody)
}

// launchJob submits the ACTIVE config as a job to the master. It carries the
// agent-channel preflight (sidecar/driver jars resolved at launch, not mid-run)
// and is shared by the HTTP job endpoints and the AI plan-confirm flow.
func (s *Server) launchJob(jobType string, extra map[string]any) (int, []byte, error) {
	if s.masterURL == "" {
		return http.StatusServiceUnavailable, nil, fmt.Errorf("master IPC not configured")
	}

	s.mu.RLock()
	cfgMap, err := configToMap(s.cfg)
	sides := []config.DBConfig{s.cfg.Source, s.cfg.Target}
	s.mu.RUnlock()
	if err != nil {
		return http.StatusInternalServerError, nil, fmt.Errorf("serialize config: %w", err)
	}

	// agent 通道预检：解析每侧通道；选中 agent 时先备好 sidecar jar（缺失会
	// 自动下载）并确认驱动 jar 可解析——让配置错误在启动时失败，而不是任务
	// 跑到连接阶段。
	for _, side := range sides {
		ch, err := dbconn.ResolveChannel(side)
		if err != nil {
			return http.StatusBadRequest, nil, err
		}
		if ch != dbconn.ChannelAgent {
			continue
		}
		if _, err := owlagent.EnsureAgentJar(owljdbc.JarSearchDirs(side.Agent.JarsDir), side.Agent.AgentJar); err != nil {
			return http.StatusBadRequest, nil, err
		}
		if _, ok := owljdbc.FindProfileJars(dbconn.AgentProfileType(strings.ToLower(strings.TrimSpace(side.Type))), owljdbc.JarSearchDirs(side.Agent.JarsDir)); !ok {
			return http.StatusBadRequest, nil, fmt.Errorf("agent 通道缺少 %s 的 JDBC 驱动 jar（jars_dir=%s），请下载放入或调整 agent.jars_dir", side.Type, side.Agent.JarsDir)
		}
	}

	payload := map[string]any{
		"type":   jobType,
		"config": cfgMap,
	}
	for k, v := range extra {
		payload[k] = v
	}
	status, respBody, err := s.callMaster("POST", "/api/v1/jobs", payload)
	if err != nil {
		return http.StatusBadGateway, nil, fmt.Errorf("master unreachable: %w", err)
	}
	return status, respBody, nil
}

func (s *Server) handleStartMigrate(w http.ResponseWriter, r *http.Request) {
	s.startJob(w, r, "migrate")
}

func (s *Server) handleStartExport(w http.ResponseWriter, r *http.Request) {
	s.startJob(w, r, "export")
}

func (s *Server) handleStartImport(w http.ResponseWriter, r *http.Request) {
	s.startJob(w, r, "import")
}

// handleCancelJob relays a cancel request to the master, which signals the
// worker process.
func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if s.masterURL == "" {
		writeError(w, http.StatusServiceUnavailable, "master IPC not configured")
		return
	}
	id := r.PathValue("id")
	status, body, err := s.callMaster("DELETE", "/api/v1/jobs/"+id, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "master unreachable: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

func (s *Server) callMaster(method, path string, payload any) (int, []byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, s.masterURL+path, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := masterClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}
