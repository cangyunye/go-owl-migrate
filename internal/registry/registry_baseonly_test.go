//go:build !ob && !og && !gdb

package registry

import (
	"strings"
	"testing"
)

// TestGetHintsForProductDialects runs only in a base (tag-less) build: product
// dialects are opt-in and the error must explain which tag provides them.
func TestGetHintsForProductDialects(t *testing.T) {
	for name, tag := range map[string]string{
		"goldendb":         "gdb",
		"goldendb-mysql":   "gdb",
		"goldendb-oracle":  "gdb",
		"oceanbase":        "ob",
		"oceanbase-mysql":  "ob",
		"oceanbase-oracle": "ob",
		"opengaussdb":      "og",
		"panweidb":         "og",
	} {
		if _, err := Get(name); err == nil || !strings.Contains(err.Error(), "-tags "+tag) {
			t.Errorf("Get(%q) in base build should hint at -tags %s, got %v", name, tag, err)
		}
	}
}
