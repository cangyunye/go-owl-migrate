package owljdbc

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// DefaultAgentJarURL is the released owl-agent.jar the sidecar needs. The
// jar is deliberately not bundled with binaries; it is downloaded on demand
// (or dropped in manually) into one of the jar search dirs.
const DefaultAgentJarURL = "https://github.com/cangyunye/owljdbc/releases/download/v0.1.0/owl-agent.jar"

// AgentJarURLEnv overrides the download source (e.g. an intranet mirror).
const AgentJarURLEnv = "OWLJDBC_AGENT_JAR_URL"

// AgentJarURL returns the download source for owl-agent.jar.
func AgentJarURL() string {
	if u := os.Getenv(AgentJarURLEnv); u != "" {
		return u
	}
	return DefaultAgentJarURL
}

// EnsureAgentJar resolves owl-agent.jar exactly like ResolveAgentJar; when it
// is not present anywhere it downloads the released jar (3 attempts) into the
// first search dir so subsequent opens find it. configured (agent_jar) always
// wins when it points at an existing file. The error on failure tells the
// user exactly what to download and where to put it.
func EnsureAgentJar(dirs []string, configured string) (string, error) {
	if jar, err := ResolveAgentJar(dirs, configured); err == nil {
		return jar, nil
	}
	url := AgentJarURL()
	dir := "."
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			dir = d
			break
		}
	}
	target := filepath.Join(dir, "owl-agent.jar")
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			time.Sleep(time.Duration(attempt-1) * time.Second)
		}
		if err := downloadFile(url, target); err != nil {
			lastErr = err
			continue
		}
		return target, nil
	}
	return "", fmt.Errorf("owljdbc: owl-agent.jar 不存在且自动下载失败（已重试 3 次）：%v\n"+
		"请手动下载 %s 并放到 %s（或用 %s 指向内网镜像，或用 agent_jar 配置显式指定已有 jar 路径）",
		lastErr, url, target, AgentJarURLEnv)
}

func downloadFile(url, target string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	// 最小完整性校验：非空且是 zip（jar 即 zip）。
	if len(data) < 1024 || !(data[0] == 'P' && data[1] == 'K') {
		return fmt.Errorf("下载内容不是有效的 jar（%d 字节）", len(data))
	}
	tmp := target + ".part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
