//go:build duckdb

package registry

import (
	"github.com/cangyunye/go-owl-migrate/internal/dialect/duckdb"
)

func init() {
	Register("duckdb", duckdb.New())
}
