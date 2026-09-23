//go:build og

package dbconn

import (
	"testing"
)

// TestOpenGaussDriverLinked guards the -tags og wiring: driver_og.go's blank
// import is the only thing that registers the openGauss/PanWeiDB driver, and a
// missing driver otherwise only surfaces as a runtime connect error.
func TestOpenGaussDriverLinked(t *testing.T) {
	if !DriverLinked("opengauss") {
		t.Fatal("opengauss driver not registered in an -tags og build")
	}
	found := false
	for _, d := range DriverReport() {
		if d.Driver == "opengauss" {
			found = d.Linked
		}
	}
	if !found {
		t.Errorf("DriverReport() does not list opengauss as linked: %+v", DriverReport())
	}
	if got, err := driverName("panweidb-mysql"); err != nil || got != "opengauss" {
		t.Errorf("driverName(panweidb-mysql) = %q, %v; want opengauss", got, err)
	}
}
