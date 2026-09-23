package registry

import (
	"sort"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
)

func TestGet(t *testing.T) {
	for _, name := range []string{"oracle", "postgres", "mysql"} {
		if _, err := Get(name); err != nil {
			t.Errorf("Get(%q) error: %v", name, err)
		}
	}
	if _, err := Get("does-not-exist"); err == nil {
		t.Error("Get(does-not-exist) expected error")
	}
}

// TestNames asserts Names mirrors the registry: sorted, and every advertised
// name resolves (it backs `owl-migrate version`, so a lie here is visible).
func TestNames(t *testing.T) {
	names := Names()
	if !sort.StringsAreSorted(names) {
		t.Errorf("Names() not sorted: %v", names)
	}
	for _, want := range []string{"oracle", "postgres", "mysql"} {
		if !slicesContain(names, want) {
			t.Errorf("Names() missing always-registered dialect %q: %v", want, names)
		}
	}
	if len(names) != len(reg) {
		t.Errorf("Names() = %d entries, registry holds %d", len(names), len(reg))
	}
	for _, name := range names {
		if _, err := Get(name); err != nil {
			t.Errorf("Get(%q) from Names() error: %v", name, err)
		}
	}
}

func slicesContain(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
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

func TestRegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate Register")
		}
	}()
	Register("oracle", dialect.Dialect{})
}
