//go:build sqlite3

package registry

import (
	"github.com/cangyunye/go-owl-migrate/internal/dialect/sqlite3"
)

func init() {
	Register("sqlite3", sqlite3.New())
}
