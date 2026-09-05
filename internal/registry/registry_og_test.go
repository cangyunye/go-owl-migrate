//go:build og

package registry

import "testing"

func TestGetOpenGaussAndPanWeiDB(t *testing.T) {
	for _, name := range []string{
		"opengaussdb", "opengaussdb-oracle", "opengaussdb-mysql",
		"panweidb", "panweidb-mysql", "panweidb-oracle",
	} {
		if _, err := Get(name); err != nil {
			t.Errorf("Get(%q) error: %v", name, err)
		}
	}
}
