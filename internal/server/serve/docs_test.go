package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// From this package's test working directory no on-disk docs-site/ exists,
// so the embedded fallback must serve the portal.
func TestDocs_EmbeddedFallback(t *testing.T) {
	srv := NewServer(Config{})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/docs/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /docs/ status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(strings.ToLower(string(body)), "<html") {
		t.Fatalf("expected HTML portal, got %.80s", body)
	}

	resp2, err := http.Get(ts.URL + "/docs/docs/index.md")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET /docs/docs/index.md status = %d, want 200", resp2.StatusCode)
	}
}
