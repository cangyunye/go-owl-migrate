package registry

import (
	"fmt"
	"sync"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	goldendb "github.com/cangyunye/go-owl-migrate/internal/dialect/goldendb"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/mysql"
	oceanbase "github.com/cangyunye/go-owl-migrate/internal/dialect/oceanbase"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/oracle"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/postgres"
)

var (
	mu   sync.RWMutex
	reg  = make(map[string]dialect.Dialect)
)

func init() {
	Register("oracle", oracle.New())
	Register("postgres", postgres.New())
	Register("mysql", mysql.New())
	Register("goldendb-mysql", goldendb.NewMySQL())
	Register("goldendb-oracle", goldendb.NewOracle())
	Register("oceanbase-mysql", oceanbase.NewMySQL())
	Register("oceanbase-oracle", oceanbase.NewOracle())
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

// Get returns a registered dialect by name.
func Get(name string) (dialect.Dialect, error) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := reg[name]
	if !ok {
		return dialect.Dialect{}, fmt.Errorf("unknown dialect %q", name)
	}
	return d, nil
}

// List returns all registered dialect names.
func List() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	return names
}
