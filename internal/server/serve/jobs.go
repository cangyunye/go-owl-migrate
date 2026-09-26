package serve

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cangyunye/owljdbc"

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

	s.mu.RLock()
	cfgMap, err := configToMap(s.cfg)
	s.mu.RUnlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "serialize config: "+err.Error())
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

	// agent 通道预检：解析每侧通道；选中 agent 时先备好 sidecar jar（缺失会
	// 自动下载）并确认驱动 jar 可解析——让配置错误在启动时失败，而不是任务
	// 跑到连接阶段。
	s.mu.RLock()
	sides := []config.DBConfig{s.cfg.Source, s.cfg.Target}
	s.mu.RUnlock()
	for _, side := range sides {
		ch, err := dbconn.ResolveChannel(side)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if ch != dbconn.ChannelAgent {
			continue
		}
		if _, err := owljdbc.EnsureAgentJar(owljdbc.JarSearchDirs(side.Agent.JarsDir), side.Agent.AgentJar); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, ok := owljdbc.FindProfileJars(dbconn.AgentProfileType(strings.ToLower(strings.TrimSpace(side.Type))), owljdbc.JarSearchDirs(side.Agent.JarsDir)); !ok {
			writeError(w, http.StatusBadRequest, "agent 通道缺少 "+side.Type+" 的 JDBC 驱动 jar（jars_dir="+side.Agent.JarsDir+"），请下载放入或调整 agent.jars_dir")
			return
		}
	}

	payload := map[string]any{
		"type":   jobType,
		"config": cfgMap,
	}
	if body.Mode != "" {
		payload["mode"] = body.Mode
	}
	if body.SkipDDL {
		payload["skip_ddl"] = true
	}
	if body.ContinueOnError {
		payload["continue_on_error"] = true
	}
	if resumeFrom := r.URL.Query().Get("resume_from"); resumeFrom != "" {
		payload["resume_from"] = resumeFrom
	}

	status, respBody, err := s.callMaster("POST", "/api/v1/jobs", payload)
	if err != nil {
		writeError(w, http.StatusBadGateway, "master unreachable: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(respBody)
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
