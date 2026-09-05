//go:build gdb

package registry

import (
	"github.com/cangyunye/go-owl-migrate/internal/dialect/goldendb"
)

func init() {
	Register("goldendb-mysql", goldendb.NewMySQL())
	Register("goldendb-oracle", goldendb.NewOracle())
}
