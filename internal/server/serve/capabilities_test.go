package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testGet(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// 能力端点是纯探测：任何部署都应返回结构完整的类型能力表，且不依赖任何
// 数据库。断言跨环境成立的事实（基础驱动、catalog 覆盖、build tag 标注）。
func TestE2E_CapabilitiesShape(t *testing.T) {
	ts, _, _ := newE2ERig(t)
	code, body := testGet(t, ts.URL+"/api/v1/capabilities")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	types, _ := body["types"].([]any)
	if len(types) == 0 {
		t.Fatal("types empty")
	}
	byType := map[string]map[string]any{}
	for _, raw := range types {
		m, _ := raw.(map[string]any)
		byType[m["type"].(string)] = m
	}
	if m := byType["mysql"]; m == nil || m["native"] != true {
		t.Errorf("mysql capability = %v, want native=true", m)
	}
	if m := byType["dm"]; m == nil || m["agent"] != true {
		t.Errorf("dm capability = %v, want agent=true（catalog-only 类型）", m)
	}
	if m := byType["sqlite3"]; m == nil || m["agent"] != false {
		t.Errorf("sqlite3 capability = %v, want agent=false（无 JDBC 等价物）", m)
	}
	// oceanbase-oracle 在未带 ob tag 的测试二进制里 native=false 但必须标注 tag。
	if m := byType["oceanbase-oracle"]; m == nil {
		t.Fatal("oceanbase-oracle capability missing")
	} else if m["native"] == false && m["native_tag"] == "" {
		t.Errorf("oceanbase-oracle unlinked native should carry native_tag, got %v", m)
	}
	if _, ok := body["agent_jar"].(map[string]any); !ok {
		t.Errorf("agent_jar section missing: %v", body["agent_jar"])
	}
	if _, ok := body["java"].(map[string]any); !ok {
		t.Errorf("java section missing: %v", body["java"])
	}
}

// conn/test 的 agent 通道打到真库：channel=agent + 全局 agent 段（jars_dir）
// 由配置提供，连接走 JVM sidecar。依赖 OWL_E2E_ORACLE_DSN 与 owl-agent.jar。
func TestE2E_ConnTest_AgentChannel(t *testing.T) {
	dsn := os.Getenv("OWL_E2E_ORACLE_DSN")
	if dsn == "" {
		t.Skip("OWL_E2E_ORACLE_DSN 未设置")
	}
	jarsDir := os.Getenv("OWL_E2E_JARS_DIR")
	agentJar := filepath.Join(jarsDir, "owl-agent.jar")
	if jarsDir == "" {
		if _, err := os.Stat("../../owljdbc/jvm/owl-agent/owl-agent.jar"); err != nil {
			t.Skip("owl-agent.jar 缺失（先跑 owljdbc/jvm/owl-agent/build.sh）")
		}
		jarsDir = "../../"
		agentJar = "../../owljdbc/jvm/owl-agent/owl-agent.jar"
	}

	ts, _, _ := newE2ERig(t)
	yamlCfg := "agent:\n  jars_dir: " + jarsDir + "\n  agent_jar: " + agentJar + "\n"
	resp, body := e2ePost(t, ts, "/api/v1/config/upload", `{"yaml":`+quoteJSON(yamlCfg)+`}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config upload: %d %v", resp.StatusCode, body)
	}

	resp, body = e2ePost(t, ts, "/api/v1/conn/test",
		`{"type":"oracle","dsn":`+quoteJSON(dsn)+`,"schema":"SYSTEM","channel":"agent"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, body)
	}
	if msg, _ := body["error"].(string); msg != "" {
		t.Fatalf("agent conn/test failed: %s", msg)
	}
}

// 通道参数非法时应在测试连接处直接 400，而不是吞掉语义。
func TestE2E_ConnTest_InvalidChannel(t *testing.T) {
	ts, _, _ := newE2ERig(t)
	resp, _ := e2ePost(t, ts, "/api/v1/conn/test", `{"type":"mysql","dsn":"u:p@tcp(127.0.0.1:1)/d","channel":"always-jvm"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// startJob 预检：agent 通道缺驱动 jar 时应在启动前 400，且错误可操作
// （URL 指向、jars 目录、覆盖手段）。用假死下载 URL 避免测试真连外网。
func TestE2E_StartJob_AgentPreflightFailsFast(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "offline", http.StatusNotFound)
	}))
	defer dead.Close()
	t.Setenv("OWLJDBC_AGENT_JAR_URL", dead.URL)

	ts, _, _ := newE2ERig(t)
	emptyDir := t.TempDir()
	// jars_dir 是存在的空目录：sidecar jar 缺失 → 触发下载（假死 URL）→ 失败。
	yamlCfg := "target:\n  type: dm\n  dsn: \"dm://SYSDBA:p@h:5236\"\n  channel: agent\nagent:\n  jars_dir: " + emptyDir + "\n"
	resp, body := e2ePost(t, ts, "/api/v1/config/upload", `{"yaml":`+quoteJSON(yamlCfg)+`}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config upload: %d %v", resp.StatusCode, body)
	}
	resp, body = e2ePost(t, ts, "/api/v1/migrate", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("migrate start: %d %v（agent 预检应在启动前失败）", resp.StatusCode, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "owl-agent.jar") {
		t.Errorf("preflight error should mention the sidecar jar, got: %v", body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, emptyDir) {
		t.Errorf("preflight error should carry the effective jars_dir（含全局折叠）, got: %v", body)
	}
}

// 驱动 jar 缺失（sidecar 已就绪）时同样启动前失败，错误点名驱动 jar。
func TestE2E_StartJob_AgentPreflightMissingDriverJar(t *testing.T) {
	ts, _, _ := newE2ERig(t)
	emptyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyDir, "owl-agent.jar"), []byte("PK\x03\x04x"), 0o644); err != nil {
		t.Fatal(err)
	}
	yamlCfg := "target:\n  type: dm\n  dsn: \"dm://SYSDBA:p@h:5236\"\n  channel: agent\nagent:\n  jars_dir: " + emptyDir + "\n"
	resp, body := e2ePost(t, ts, "/api/v1/config/upload", `{"yaml":`+quoteJSON(yamlCfg)+`}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config upload: %d %v", resp.StatusCode, body)
	}
	resp, body = e2ePost(t, ts, "/api/v1/migrate", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("migrate start: %d %v", resp.StatusCode, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "JDBC 驱动 jar") {
		t.Errorf("preflight error should name the driver jar, got: %v", body)
	}
}
