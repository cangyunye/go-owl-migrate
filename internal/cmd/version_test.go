package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

// TestVersionCmdListsBuildFlavor asserts the version output mirrors the two
// runtime ground truths: the selectable drivers this binary links and the
// compiled dialect registry. A build flavor can then be checked without
// connecting.
func TestVersionCmdListsBuildFlavor(t *testing.T) {
	out := runVersion(t)

	for _, want := range []string{
		"owl-migrate " + version,
		"commit:", "built:", "drivers:", "dialects:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("version output missing %q:\n%s", want, out)
		}
	}

	drivers := lineAfter(out, "drivers:")
	linked := strings.Join(linkedDrivers(), ", ")
	if drivers != linked {
		t.Errorf("drivers line = %q, want %q", drivers, linked)
	}
	// The base dialects' drivers always come from package cmd's blank imports.
	for _, d := range []string{"postgres", "mysql", "oracle"} {
		if !dbconn.DriverLinked(d) {
			t.Errorf("driver %q not linked into the command package", d)
		}
		if !strings.Contains(drivers, d) {
			t.Errorf("drivers line %q missing %q", drivers, d)
		}
	}

	dialects := lineAfter(out, "dialects:")
	for _, name := range registry.Names() {
		if !strings.Contains(dialects, name) {
			t.Errorf("dialects line %q missing compiled dialect %q", dialects, name)
		}
	}
}

// TestVersionCmdHintsMissingProductDrivers asserts that drivers excluded by a
// build tag are reported with the tag that would add them — the hint that turns
// "driver not compiled into this binary" into an actionable fix.
func TestVersionCmdHintsMissingProductDrivers(t *testing.T) {
	out := runVersion(t)
	missing := missingDrivers()
	if len(missing) == 0 {
		if strings.Contains(out, "not linked:") {
			t.Errorf("every driver is linked, but output reports a missing one:\n%s", out)
		}
		return
	}
	hint := lineAfter(out, "not linked:")
	for _, m := range missing {
		if !strings.Contains(hint, m) {
			t.Errorf("not-linked line %q missing %q", hint, m)
		}
	}
	for _, d := range dbconn.DriverReport() {
		if !d.Linked && strings.Contains(lineAfter(out, "drivers:"), d.Driver) {
			t.Errorf("unlinked driver %q listed as linked", d.Driver)
		}
	}
}

// runVersion executes the version command and returns its output.
func runVersion(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := versionCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	return buf.String()
}

// lineAfter returns the value of a "  key: a, b, c" output line.
func lineAfter(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, key) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, key))
		}
	}
	return ""
}
