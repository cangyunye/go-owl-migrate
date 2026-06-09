package goldendb

import (
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	mysql "github.com/cangyunye/go-owl-migrate/internal/dialect/mysql"
	oracle "github.com/cangyunye/go-owl-migrate/internal/dialect/oracle"
)

// ── GoldenDB MySQL 租户 ──
// 文件级组合复用 MySQL 方言 100%，类型语义完全兼容 MySQL

type gdbMySQLTypeMapper struct{ mysql.MySQLTypeMapper }

func (m gdbMySQLTypeMapper) Name() string { return "goldendb-mysql" }

// NewMySQL creates a GoldenDB MySQL tenant dialect.
func NewMySQL() dialect.Dialect {
	mysqlD := mysql.New()
	return dialect.Dialect{
		TypeMapper:       gdbMySQLTypeMapper{},
		IdentifierQuoter: mysqlD.IdentifierQuoter,
		Features:         mysqlD.Features,
		DDLBuilder:       mysqlD.DDLBuilder,
		DMLHelper:        mysqlD.DMLHelper,
	}
}

// ── GoldenDB Oracle 租户 ──
// 文件级组合复用 Oracle 方言 100%，类型语义完全兼容 Oracle
// 差异点（未来按需覆盖）：
//   - TruncateIsTransactional 可能不同（需实测确认）

type gdbOracleTypeMapper struct{ oracle.OracleTypeMapper }

func (m gdbOracleTypeMapper) Name() string { return "goldendb-oracle" }

// NewOracle creates a GoldenDB Oracle tenant dialect.
func NewOracle() dialect.Dialect {
	oracleD := oracle.New()
	return dialect.Dialect{
		TypeMapper:       gdbOracleTypeMapper{},
		IdentifierQuoter: oracleD.IdentifierQuoter,
		Features:         oracleD.Features,
		DDLBuilder:       oracleD.DDLBuilder,
		DMLHelper:        oracleD.DMLHelper,
	}
}
