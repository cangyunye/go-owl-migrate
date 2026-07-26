package panweidb

import (
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	mysql "github.com/cangyunye/go-owl-migrate/internal/dialect/mysql"
	oracle "github.com/cangyunye/go-owl-migrate/internal/dialect/oracle"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/postgres"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// ── PanWeiDB PostgreSQL 原生模式 (PG) ──
// 磐维数据库 (PanWeiDB) 基于 openGauss/PostgreSQL 内核，PG 全兼容
// 文件级组合复用 PostgreSQL 方言 100%

type pdbTypeMapper struct{ postgres.PGTypeMapper }

func (m pdbTypeMapper) Name() string { return "panweidb" }

// New creates a PanWeiDB PG-mode dialect.
func New() dialect.Dialect {
	pgD := postgres.New()
	return dialect.Dialect{
		TypeMapper:       pdbTypeMapper{},
		IdentifierQuoter: pgD.IdentifierQuoter,
		Features:         pgD.Features,
		DDLBuilder:       pgD.DDLBuilder,
		DMLHelper:        pgD.DMLHelper,
	}
}

// ── PanWeiDB MySQL 兼容模式 (B) ──
// 文件级组合复用 MySQL 方言，差异：
//   - TRUNCATE 是事务安全的（openGauss 内核特性）
//   - Features 基于 PG 而非 MySQL

type pdbMySQLTypeMapper struct{ mysql.MySQLTypeMapper }

func (m pdbMySQLTypeMapper) Name() string { return "panweidb-mysql" }

type pdbMySQLDDLBuilder struct{ mysql.MySQLDDLBuilder }

func (b pdbMySQLDDLBuilder) BuildCreateTable(t *md.TableDef, opts dialect.BuildOptions) (string, error) {
	sql, err := b.MySQLDDLBuilder.BuildCreateTable(t, opts)
	if err != nil {
		return "", err
	}
	partition := dialect.PartitionClause(t, opts)
	sql = strings.TrimSuffix(sql, partition)
	if idx := strings.LastIndex(sql, " ENGINE="); idx >= 0 {
		sql = sql[:idx]
	}
	return sql + partition, nil
}

// NewMySQL creates a PanWeiDB MySQL-compatible (B mode) dialect.
func NewMySQL() dialect.Dialect {
	mysqlD := mysql.New()
	return dialect.Dialect{
		TypeMapper:       pdbMySQLTypeMapper{},
		IdentifierQuoter: mysqlD.IdentifierQuoter,
		Features:         postgres.PGFeatures{},
		DDLBuilder:       pdbMySQLDDLBuilder{},
		DMLHelper:        mysqlD.DMLHelper,
	}
}

// ── PanWeiDB Oracle 兼容模式 (A) ──
// 文件级组合复用 Oracle 方言，差异：
//   - TRUNCATE 是事务安全的（openGauss 内核特性）
//   - Features 基于 PG 而非 Oracle

type pdbOracleTypeMapper struct{ oracle.OracleTypeMapper }

func (m pdbOracleTypeMapper) Name() string { return "panweidb-oracle" }

// NewOracle creates a PanWeiDB Oracle-compatible (A mode) dialect.
func NewOracle() dialect.Dialect {
	oracleD := oracle.New()
	return dialect.Dialect{
		TypeMapper:       pdbOracleTypeMapper{},
		IdentifierQuoter: oracleD.IdentifierQuoter,
		Features:         postgres.PGFeatures{},
		DDLBuilder:       oracleD.DDLBuilder,
		DMLHelper:        oracleD.DMLHelper,
	}
}
