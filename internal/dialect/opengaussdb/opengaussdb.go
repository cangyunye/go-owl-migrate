package opengaussdb

import (
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/postgres"
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
