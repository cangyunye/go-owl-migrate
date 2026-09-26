package serve

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cangyunye/owljdbc"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
)

// handleGetCapabilities exposes deploy-time facts so the UI (and operators)
// can see which database types this deployment can actually reach:
//   - java: whether a JRE is available for the agent sidecar;
//   - agent_jar: whether owl-agent.jar is resolvable (it auto-downloads on
//     the first agent-channel open; here it is a presence probe only);
//   - types: per database type — native driver linked (or its build tag),
//     agent catalog coverage, and driver jar presence.
//
// Pure probes: nothing is downloaded or executed here.
func (s *Server) handleGetCapabilities(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	agentCfg := s.cfg.Agent
	s.mu.RUnlock()

	dirs := owljdbc.JarSearchDirs(agentCfg.JarsDir)

	resp := map[string]any{
		"agent_jars_dir": agentCfg.JarsDir,
		"java":           javaStatus(agentCfg.JavaHome),
		"agent_jar":      agentJarStatus(dirs, agentCfg.AgentJar),
		"types":          dbconn.TypeCapabilities(agentCfg.JarsDir),
	}
	writeJSON(w, http.StatusOK, resp)
}

func javaStatus(javaHome string) map[string]any {
	candidates := []string{"java"}
	if javaHome != "" {
		bin := filepath.Join(javaHome, "bin", "java")
		candidates = []string{bin, bin + ".exe"}
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return map[string]any{"found": true, "path": p}
		}
	}
	return map[string]any{"found": false, "java_home": javaHome}
}

// exec.LookPath 由标准库提供，直接使用。

func agentJarStatus(dirs []string, configured string) map[string]any {
	if configured != "" {
		if _, err := os.Stat(configured); err == nil {
			return map[string]any{"found": true, "path": configured, "configured": true}
		}
		return map[string]any{"found": false, "configured": configured,
			"download_url": owljdbc.AgentJarURL(), "error": "agent_jar 指向的文件不存在"}
	}
	if jar, err := owljdbc.ResolveAgentJar(dirs, ""); err == nil {
		return map[string]any{"found": true, "path": jar}
	}
	return map[string]any{"found": false, "download_url": owljdbc.AgentJarURL(),
		"hint": "缺失时首次 agent 连接会自动下载（3 次重试）；离线环境请手动下载后放到 jars 目录，或用 " + owljdbc.AgentJarURLEnv + " 指向内网镜像"}
}

// globalAgent 返回当前加载配置的全局 agent 段，供 conn/test 这类独立连接
// 作为 jars_dir/agent_jar/java_home 的缺省（与 config.Load 的折叠语义一致）。
func (s *Server) globalAgent() config.AgentConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Agent
}
