package panweidb

import (
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/postgres"
)

// ── PanWeiDB ──
// 磐维数据库 (PanWeiDB) 基于 PostgreSQL 内核，PG 全兼容
// 文件级组合复用 PostgreSQL 方言 100%

type pdbTypeMapper struct{ postgres.PGTypeMapper }

func (m pdbTypeMapper) Name() string { return "panweidb" }

// New creates a PanWeiDB dialect.
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
