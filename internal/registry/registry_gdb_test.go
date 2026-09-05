//go:build gdb

package registry

import "testing"

func TestGetGoldenDB(t *testing.T) {
	for _, name := range []string{"goldendb-mysql", "goldendb-oracle", "goldendb"} {
		if _, err := Get(name); err != nil {
			t.Errorf("Get(%q) error: %v", name, err)
		}
	}
}

func TestGetNormalizesBareGoldenDB(t *testing.T) {
	d, err := Get("goldendb")
	if err != nil {
		t.Fatalf("Get(goldendb) error: %v", err)
	}
	if d.Name() != "goldendb-mysql" {
		t.Errorf("Get(goldendb).Name() = %q, want goldendb-mysql", d.Name())
	}
}
