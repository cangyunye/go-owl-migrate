//go:build ob

package registry

import "testing"

func TestGetOceanBase(t *testing.T) {
	for _, name := range []string{"oceanbase-mysql", "oceanbase-oracle", "oceanbase"} {
		if _, err := Get(name); err != nil {
			t.Errorf("Get(%q) error: %v", name, err)
		}
	}
}

func TestGetNormalizesBareOceanBase(t *testing.T) {
	d, err := Get("oceanbase")
	if err != nil {
		t.Fatalf("Get(oceanbase) error: %v", err)
	}
	if d.Name() != "oceanbase-mysql" {
		t.Errorf("Get(oceanbase).Name() = %q, want oceanbase-mysql", d.Name())
	}
}
