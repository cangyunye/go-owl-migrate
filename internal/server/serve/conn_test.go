package serve

import (
	"net/http"
	"strings"
	"testing"
)

func TestE2E_ConnTest_Validation(t *testing.T) {
	ts, _, _ := newE2ERig(t)

	// Missing dsn -> 400.
	resp, _ := e2ePost(t, ts, "/api/v1/conn/test", `{"type":"postgres"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing dsn: status %d, want 400", resp.StatusCode)
	}

	// Unsupported type -> connect fails, reported in a 200 body with error.
	_, body := e2ePost(t, ts, "/api/v1/conn/test", `{"type":"bogus","dsn":"x"}`)
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "connect failed") && !strings.Contains(msg, "unsupported") {
		t.Errorf("expected connect/unsupported error, got %q", body)
	}
}
