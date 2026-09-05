//go:build og

package registry

import (
	"github.com/cangyunye/go-owl-migrate/internal/dialect/opengaussdb"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/panweidb"
)

func init() {
	Register("opengaussdb", opengaussdb.New())
	Register("opengaussdb-oracle", opengaussdb.NewOracle())
	Register("opengaussdb-mysql", opengaussdb.NewMySQL())
	Register("panweidb", panweidb.New())
	Register("panweidb-mysql", panweidb.NewMySQL())
	Register("panweidb-oracle", panweidb.NewOracle())
}
