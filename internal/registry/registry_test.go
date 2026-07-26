package registry

import (
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
)

func TestGet(t *testing.T) {
	for _, name := range []string{"oracle", "postgres", "mysql", "goldendb-mysql", "oceanbase-oracle", "panweidb", "opengaussdb"} {
		if _, err := Get(name); err != nil {
			t.Errorf("Get(%q) error: %v", name, err)
		}
	}
	if _, err := Get("does-not-exist"); err == nil {
		t.Error("Get(does-not-exist) expected error")
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"goldendb", "goldendb-mysql"},
		{"oceanbase", "oceanbase-mysql"},
		{"GOLDENDB", "goldendb-mysql"},
		{"postgres", "postgres"},
	}
	for _, tt := range tests {
		if got := Normalize(tt.in); got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGetNormalizesBareNames(t *testing.T) {
	d, err := Get("goldendb")
	if err != nil {
		t.Fatalf("Get(goldendb) error: %v", err)
	}
	if d.Name() != "goldendb-mysql" {
		t.Errorf("Get(goldendb).Name() = %q, want goldendb-mysql", d.Name())
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate Register")
		}
	}()
	Register("oracle", dialect.Dialect{})
}
