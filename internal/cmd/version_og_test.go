//go:build og

package cmd

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
)

// TestVersionOGFlavorLinksOpenGauss pins the flavor a PanWeiDB/openGaussDB
// source needs: the driver must show up as linked, the og dialects must be
// listed, and the output must not ask for "-tags og" again.
func TestVersionOGFlavorLinksOpenGauss(t *testing.T) {
	if !dbconn.DriverLinked("opengauss") {
		t.Fatal("opengauss driver not linked in an -tags og build")
	}

	out := runVersion(t)
	if drivers := lineAfter(out, "drivers:"); !strings.Contains(drivers, "opengauss") {
		t.Errorf("drivers line lacks opengauss: %q", drivers)
	}
	if dialects := lineAfter(out, "dialects:"); !strings.Contains(dialects, "panweidb-mysql") {
		t.Errorf("dialects line lacks panweidb-mysql: %q", dialects)
	}
	if hint := lineAfter(out, "not linked:"); strings.Contains(hint, "-tags og") {
		t.Errorf("-tags og build still hints at og: %q", hint)
	}
}
