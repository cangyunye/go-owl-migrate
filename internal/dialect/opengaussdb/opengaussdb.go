//go:build og

package opengaussdb

import (
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	mysql "github.com/cangyunye/go-owl-migrate/internal/dialect/mysql"
	oracle "github.com/cangyunye/go-owl-migrate/internal/dialect/oracle"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/postgres"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// ── OpenGaussDB ──
// OpenGaussDB 基于 PostgreSQL 内核，PG 协议兼容
// 文件级组合复用 PostgreSQL 方言 100%

type ogTypeMapper struct{ postgres.PGTypeMapper }

func (m ogTypeMapper) Name() string { return "opengaussdb" }

// New creates an OpenGaussDB dialect.
func New() dialect.Dialect {
	pgD := postgres.New()
	return dialect.Dialect{
		TypeMapper:       ogTypeMapper{},
		IdentifierQuoter: pgD.IdentifierQuoter,
		Features:         pgD.Features,
		DDLBuilder:       pgD.DDLBuilder,
		DMLHelper:        pgD.DMLHelper,
	}
}

// ── OpenGaussDB MySQL 兼容模式 (B, dolphin) ──
// 文件级组合复用 MySQL 方言，差异：
//   - TRUNCATE 是事务安全的（openGauss 内核特性）
//   - Features 基于 PG 而非 MySQL
//   - 通信协议仍是 PG（$N 占位符）

type ogMySQLTypeMapper struct{ mysql.MySQLTypeMapper }

func (m ogMySQLTypeMapper) Name() string { return "opengaussdb-mysql" }

type ogMySQLDDLBuilder struct{ mysql.MySQLDDLBuilder }

func (b ogMySQLDDLBuilder) BuildCreateTable(t *md.TableDef, opts dialect.BuildOptions) (string, error) {
	sql, err := b.MySQLDDLBuilder.BuildCreateTable(t, opts)
	if err != nil {
		return "", err
	}
	partition := dialect.PartitionClause(t, opts)
	sql = strings.TrimSuffix(sql, partition)
	if idx := strings.LastIndex(sql, " ENGINE="); idx >= 0 {
		sql = sql[:idx]
	}
	if opts.IncludeComments && t.TableComment != "" {
		sql += " COMMENT='" + strings.ReplaceAll(t.TableComment, "'", "''") + "'"
	}
	return sql + partition, nil
}

// NewMySQL creates an OpenGaussDB MySQL-compatible (B mode) dialect.
func NewMySQL() dialect.Dialect {
	mysqlD := mysql.New()
	return dialect.Dialect{
		TypeMapper:       ogMySQLTypeMapper{},
		IdentifierQuoter: mysqlD.IdentifierQuoter,
		Features:         postgres.PGFeatures{},
		DDLBuilder:       ogMySQLDDLBuilder{},
		DMLHelper:        mysqlD.DMLHelper,
	}
}

// ── OpenGaussDB Oracle 兼容模式 (A) ──
// 文件级组合复用 Oracle 方言，差异：
//   - TRUNCATE 是事务安全的（openGauss 内核特性）
//   - Features 基于 PG 而非 Oracle
//   - 通信协议仍是 PG（$N 占位符）

type ogOracleTypeMapper struct{ oracle.OracleTypeMapper }

func (m ogOracleTypeMapper) Name() string { return "opengaussdb-oracle" }

// NewOracle creates an OpenGaussDB Oracle-compatible (A mode) dialect.
func NewOracle() dialect.Dialect {
	oracleD := oracle.New()
	return dialect.Dialect{
		TypeMapper:       ogOracleTypeMapper{},
		IdentifierQuoter: oracleD.IdentifierQuoter,
		Features:         postgres.PGFeatures{},
		DDLBuilder:       oracleD.DDLBuilder,
		DMLHelper:        oracleD.DMLHelper,
	}
}
