package registry

import (
	"fmt"
	"strings"
	"sync"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	goldendb "github.com/cangyunye/go-owl-migrate/internal/dialect/goldendb"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/mysql"
	oceanbase "github.com/cangyunye/go-owl-migrate/internal/dialect/oceanbase"
	opengaussdb "github.com/cangyunye/go-owl-migrate/internal/dialect/opengaussdb"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/oracle"
	panweidb "github.com/cangyunye/go-owl-migrate/internal/dialect/panweidb"
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
	Register("goldendb-mysql", goldendb.NewMySQL())
	Register("goldendb-oracle", goldendb.NewOracle())
	Register("oceanbase-mysql", oceanbase.NewMySQL())
	Register("oceanbase-oracle", oceanbase.NewOracle())
	Register("panweidb", panweidb.New())
	Register("panweidb-mysql", panweidb.NewMySQL())
	Register("panweidb-oracle", panweidb.NewOracle())
	Register("opengaussdb", opengaussdb.New())
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
//   "goldendb"  → "goldendb-mysql"
//   "oceanbase" → "oceanbase-mysql"
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

// Get returns a registered dialect by name.
// Bare compound names (e.g. "goldendb") are normalized automatically.
func Get(name string) (dialect.Dialect, error) {
	name = Normalize(name)
	mu.RLock()
	defer mu.RUnlock()
	d, ok := reg[name]
	if !ok {
		return dialect.Dialect{}, fmt.Errorf("unknown dialect %q", name)
	}
	return d, nil
}
