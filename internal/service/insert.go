package service

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// InsertOptions 是 INSERT 生成的请求级覆盖；零值回落配置/内建默认。
type InsertOptions struct {
	Dialect   string // 空 = cfg.DDL.TargetDialect → postgres
	BatchSize int    // <=0 = 100
	Truncate  bool
	NoQuote   *bool // 非 nil 覆盖 cfg.DDL.NoQuoteIdentifiers
}

// InsertDataDir 返回 INSERT 数据源目录（cfg.Import.SourceDir，默认 ./output/data/）。
func InsertDataDir(cfg *config.Config) string {
	if cfg != nil && cfg.Import.SourceDir != "" {
		return cfg.Import.SourceDir
	}
	return "./output/data/"
}

// DetectTablesFromCSVDir infers TableDefs from {schema}.{table}.csv headers。
// CLI export insert 与 serve /insert/* 共用（P1 收敛点）。
func DetectTablesFromCSVDir(dir string) ([]*md.TableDef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read data directory %q: %w; 请先执行「数据导出」或 owl-migrate export data -c <cfg> 生成 {schema}.{table}.csv 后重试", dir, err)
	}
	var tables []*md.TableDef
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".csv") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".csv")
		parts := strings.SplitN(name, ".", 2)
		if len(parts) != 2 {
			continue
		}
		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		r := csv.NewReader(f)
		header, err := r.Read()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("read header from %s: %w", entry.Name(), err)
		}
		tbl, err := md.NewTableDef(parts[0], parts[1])
		if err != nil {
			return nil, err
		}
		for i, colName := range header {
			col, err := md.NewColumnDef(parts[0], parts[1], colName, i+1, "VARCHAR")
			if err != nil {
				return nil, err
			}
			tbl.AddColumn(col)
		}
		tables = append(tables, tbl)
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("no {schema}.{table}.csv files found in %q; 请先执行「数据导出」或 owl-migrate export data -c <cfg> 生成 CSV 后重试", dir)
	}
	return tables, nil
}

// GenerateInsert: CLI export insert 与 serve /insert/generate 的单一实现。
// 从 dataDir 检测 {schema}.{table}.csv，按 include 过滤（ADR-003），
// 输出方言化 INSERT SQL 到 outDir；缺失数据目录给出可操作指引。
func GenerateInsert(cfg *config.Config, include []string, dataDir, outDir string, o InsertOptions) ([]string, error) {
	tables, err := DetectTablesFromCSVDir(dataDir)
	if err != nil {
		return nil, err
	}
	if len(include) > 0 {
		tables = md.FilterTablesByInclude(tables, include)
	}

	dialect := o.Dialect
	if dialect == "" {
		dialect = cfg.DDL.TargetDialect
	}
	if dialect == "" {
		dialect = "postgres"
	}

	noQuote := cfg.DDL.NoQuoteIdentifiers
	if o.NoQuote != nil {
		noQuote = *o.NoQuote
	}

	gen := generator.NewInsertGenerator(generator.InsertConfig{
		OutputDir:          outDir,
		BatchSize:          o.BatchSize,
		TruncateBefore:     o.Truncate,
		Dialect:            dialect,
		NullMarker:         cfg.Import.CSV.NullMarker,
		CSVDelimiter:       cfg.Import.CSV.Delimiter,
		NoQuoteIdentifiers: noQuote,
	})
	return gen.Generate(tables, dataDir)
}
