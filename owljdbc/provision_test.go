package owljdbc

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureAgentJar_ResolvesExistingFirst(t *testing.T) {
	dir := t.TempDir()
	jar := filepath.Join(dir, "owl-agent-0.1.jar")
	if err := os.WriteFile(jar, []byte("PK\x03\x04 fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := EnsureAgentJar([]string{dir}, "")
	if err != nil {
		t.Fatalf("EnsureAgentJar: %v", err)
	}
	if got != jar {
		t.Fatalf("got %q, want %q", got, jar)
	}
}

func TestEnsureAgentJar_DownloadsWhenMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(append([]byte("PK\x03\x04"), make([]byte, 2048)...))
	}))
	defer srv.Close()
	t.Setenv(AgentJarURLEnv, srv.URL)

	dir := t.TempDir()
	got, err := EnsureAgentJar([]string{dir}, "")
	if err != nil {
		t.Fatalf("EnsureAgentJar: %v", err)
	}
	if got != filepath.Join(dir, "owl-agent.jar") {
		t.Fatalf("downloaded to %q", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatal(err)
	}
	// 第二次调用直接命中已下载文件（服务器关闭也不影响）
	srv.Close()
	if _, err := EnsureAgentJar([]string{dir}, ""); err != nil {
		t.Fatalf("second call should reuse the file: %v", err)
	}
}

func TestEnsureAgentJar_ActionableErrorOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv(AgentJarURLEnv, srv.URL)

	dir := t.TempDir()
	_, err := EnsureAgentJar([]string{dir}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), srv.URL) || !strings.Contains(err.Error(), dir) {
		t.Errorf("error should name the URL and target dir, got: %v", err)
	}
	if !strings.Contains(err.Error(), AgentJarURLEnv) {
		t.Errorf("error should mention the override env, got: %v", err)
	}
}

func TestEnsureAgentJar_RejectsNonJarPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>not a jar</html>"))
	}))
	defer srv.Close()
	t.Setenv(AgentJarURLEnv, srv.URL)

	dir := t.TempDir()
	_, err := EnsureAgentJar([]string{dir}, "")
	if err == nil {
		t.Fatal("expected error for non-jar payload")
	}
}
