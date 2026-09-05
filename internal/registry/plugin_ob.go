//go:build ob

package registry

import (
	"github.com/cangyunye/go-owl-migrate/internal/dialect/oceanbase"
)

func init() {
	Register("oceanbase-mysql", oceanbase.NewMySQL())
	Register("oceanbase-oracle", oceanbase.NewOracle())
}
