package service

import (
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

// GenerateSelect: CLI gen-select 与 serve /select/generate 的单一实现。
// batchMethod/pageSize 为请求级覆盖（空/0 时回落到 cfg.SelectGen.Batch），
// noQuote 非 nil 时覆盖配置；include 走统一 ObjectSelector 语义（ADR-003）。
func GenerateSelect(sm *md.SchemaModel, cfg *config.Config, include []string, batchMethod string, pageSize int, noQuote *bool, outDir string) ([]string, error) {
	if len(include) > 0 {
		sm = filterSchemaTables(sm, include)
	}
	d, err := registry.Get(cfg.DDL.TargetDialect)
	if err != nil {
		return nil, err
	}

	if batchMethod == "" {
		batchMethod = cfg.SelectGen.Batch.Method
	}
	if batchMethod == "" {
		batchMethod = "cursor"
	}
	if pageSize == 0 {
		pageSize = cfg.SelectGen.Batch.PageSize
	}
	if pageSize == 0 {
		pageSize = 5000
	}

	quoteFn := d.IdentifierQuoter.QuotePreserve // 保真引用，与生成 DDL 一致（ADR-001）
	if noQuote != nil && *noQuote {
		quoteFn = func(s string) string { return s }
	}

	oracleRowNum := strings.Contains(cfg.DDL.TargetDialect, "oracle")
	gen := generator.NewSelectGenerator(batchMethod, pageSize, outDir, quoteFn,
		cfg.SelectGen.IncludeRowNumber, cfg.SelectGen.AddExportColumns, oracleRowNum).
		WithPagination(d.BuildPaginationClause)

	return gen.Generate(sm)
}
