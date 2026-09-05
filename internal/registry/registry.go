// Package registry holds the dialect registry shared by every command.
// The base build always registers oracle/postgres/mysql. Product dialects are
// opt-in via build tags and register from per-tag plugin files:
//
//	-tags ob  — oceanbase (oceanbase-mysql, oceanbase-oracle)
//	-tags og  — opengaussdb, panweidb (each with -mysql/-oracle variants)
//	-tags gdb — goldendb (goldendb-mysql, goldendb-oracle)
package registry

import (
	"fmt"
	"strings"
	"sync"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/mysql"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/oracle"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/postgres"
)

var (
	mu  sync.RWMutex
	reg = make(map[string]dialect.Dialect)
)

func init() {
	Register("oracle", oracle.New())
	Register("postgres", postgres.New())
	Register("mysql", mysql.New())
}

// Register adds a dialect to the global registry.
func Register(name string, d dialect.Dialect) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := reg[name]; exists {
		panic(fmt.Sprintf("dialect %q already registered", name))
	}
	reg[name] = d
}

// Normalize maps bare compound dialect names to their qualified form.
// Returns the name unchanged if no mapping exists.
//
//	"goldendb"  → "goldendb-mysql"
//	"oceanbase" → "oceanbase-mysql"
func Normalize(name string) string {
	switch strings.ToLower(name) {
	case "goldendb":
		return "goldendb-mysql"
	case "oceanbase":
		return "oceanbase-mysql"
	default:
		return name
	}
}

// dialectTag maps a dialect name to the build tag that provides it. Used to
// make "unknown dialect" errors actionable when a product was not compiled in.
var dialectTag = map[string]string{
	"goldendb-mysql": "gdb", "goldendb-oracle": "gdb",
	"oceanbase-mysql": "ob", "oceanbase-oracle": "ob",
	"panweidb": "og", "panweidb-mysql": "og", "panweidb-oracle": "og",
	"opengaussdb": "og", "opengaussdb-mysql": "og", "opengaussdb-oracle": "og",
}

// Get returns a registered dialect by name.
// Bare compound names (e.g. "goldendb") are normalized automatically.
func Get(name string) (dialect.Dialect, error) {
	name = Normalize(name)
	mu.RLock()
	defer mu.RUnlock()
	d, ok := reg[name]
	if !ok {
		if tag, known := dialectTag[name]; known {
			return dialect.Dialect{}, fmt.Errorf("unknown dialect %q: %s support is not compiled into this binary; rebuild with -tags %s", name, name, tag)
		}
		return dialect.Dialect{}, fmt.Errorf("unknown dialect %q", name)
	}
	return d, nil
}
