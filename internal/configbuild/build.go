// Package configbuild is the deterministic slot → config assembly layer:
// the same builders back the human CLI (owl-migrate init), the AI plan
// endpoint, and any tool that needs a valid migrate.yaml from structured
// inputs. Builders return ready-to-validate configs; they never connect to
// a database.
package configbuild

import (
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

// isEmbedded reports dialects that open files instead of connecting.
func isEmbedded(dialect string) bool {
	switch strings.ToLower(dialect) {
	case "sqlite3", "duckdb":
		return true
	default:
		return false
	}
}

func BuildScenarioConfig(scenario, srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema, metaType string) *config.Config {
	switch scenario {
	case "export-ddl", "gen-ddl", "validate":
		csvPath := ""
		xlsxPath := ""
		if metaType == "csv" {
			csvPath = "./testdata/csv/"
		}
		if metaType == "xlsx" {
			xlsxPath = "./metadata/schema.xlsx"
		}
		return BuildDDLConfig(metaType, srcType, srcDSN, srcSchema, tgtType, csvPath, xlsxPath)
	case "gen-select":
		return BuildSelectGenConfig(metaType, srcType, srcDSN, srcSchema, tgtType)
	case "export-insert", "gen-insert":
		cfg := &config.Config{
			General: config.GeneralConfig{LogLevel: "info"},
			DDL:     config.DDLConfig{TargetDialect: tgtType},
		}
		switch metaType {
		case "xlsx":
			cfg.Metadata = config.MetadataConfig{
				Type: "xlsx",
				XLSX: config.XLSXConfig{
					Path:          "./metadata/schema.xlsx",
					DataOutputDir: "./output/data/",
				},
			}
		default:
			// csv: export insert reads data dir from CLI -d/--data flag.
			cfg.Metadata = config.MetadataConfig{Type: "csv"}
		}
		return cfg
	case "export":
		return &config.Config{
			General:  config.GeneralConfig{LogLevel: "info"},
			Metadata: config.MetadataConfig{Type: "database"},
			Source:   config.DBConfig{Type: srcType, DSN: srcDSN, Schema: srcSchema},
			Export: config.ExportConfig{
				OutputDir: "./output/data/",
				Format:    "csv",
				CSV: config.ExportCSVConfig{
					Delimiter:          ",",
					QuoteChar:          "\"",
					Header:             true,
					NullRepresentation: "\\N",
				},
				Batch: config.BatchConfig{PageSize: 5000},
				Parallel: config.ParallelConfig{
					Enabled:    true,
					MaxWorkers: 4,
				},
				Tables: config.TableListConfig{Include: []string{"*"}},
			},
		}
	case "import":
		return &config.Config{
			General:  config.GeneralConfig{LogLevel: "info"},
			Metadata: config.MetadataConfig{Type: "csv"},
			Target:   config.DBConfig{Type: tgtType, DSN: tgtDSN, Schema: tgtSchema},
			DDL: config.DDLConfig{
				TargetDialect:      tgtType,
				IncludeIfNotExists: true,
				SchemaMapping:      map[string]string{tgtSchema: tgtSchema},
			},
			Import: config.ImportConfig{
				SourceDir: "./output/data/",
				Format:    "csv",
				CSV:       config.ImportCSVConfig{NullMarker: "\\N"},
				Target:    config.ImportTargetConfig{TruncateBefore: true},
				Batch: config.ImportBatchConfig{
					CommitInterval: 1000,
					ErrorPolicy:    "skip_row",
				},
				Parallel: config.ParallelConfig{
					Enabled:    true,
					MaxWorkers: 4,
				},
				DataTransforms: config.DataTransforms{
					DatetimeFormat: "yyyyMMddHHmmss",
					TrimStrings:    true,
					NullIf:         []string{"NULL", "null", "\\N"},
				},
			},
		}
	case "export-metadata":
		// ddl.target_dialect is unconditional in config.Load, so it must be
		// set even though metadata export generates no DDL; inherit the
		// source dialect (the documented fallback) instead of failing
		// validation on a freshly generated config.
		return &config.Config{
			General:  config.GeneralConfig{LogLevel: "info"},
			Metadata: config.MetadataConfig{Type: "database"},
			Source:   config.DBConfig{Type: srcType, DSN: srcDSN, Schema: srcSchema},
			DDL:      config.DDLConfig{TargetDialect: srcType},
		}
	default:
		return BuildMigrateConfig(srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema)
	}
}

func BuildSelectGenConfig(metaType, srcType, srcDSN, srcSchema, tgtType string) *config.Config {
	cfg := &config.Config{
		General: config.GeneralConfig{LogLevel: "info"},
		SelectGen: config.SelectGenConfig{
			OutputDir: "./output/select/",
			Batch: config.BatchConfig{
				Method:   "cursor",
				PageSize: 5000,
			},
		},
	}
	switch metaType {
	case "csv":
		cfg.Metadata = config.MetadataConfig{Type: "csv", CSV: config.CSVConfig{Path: "./testdata/csv/"}}
	case "xlsx":
		cfg.Metadata = config.MetadataConfig{
			Type: "xlsx",
			XLSX: config.XLSXConfig{Path: "./metadata/schema.xlsx", DataOutputDir: "./output/data/"},
		}
	case "database":
		cfg.Metadata = config.MetadataConfig{Type: "database"}
		cfg.Source = config.DBConfig{Type: srcType, DSN: srcDSN, Schema: srcSchema}
	}
	cfg.DDL = config.DDLConfig{TargetDialect: tgtType}
	return cfg
}

func BuildDDLConfig(metaType, srcType, srcDSN, srcSchema, tgtType, csvPath, xlsxPath string) *config.Config {
	cfg := &config.Config{
		General: config.GeneralConfig{LogLevel: "info"},
		DDL: config.DDLConfig{
			TargetDialect:      tgtType,
			IncludeComments:    true,
			IncludeIfNotExists: true,
		},
	}

	switch metaType {
	case "csv":
		cfg.Metadata = config.MetadataConfig{Type: "csv", CSV: config.CSVConfig{Path: csvPath}}
	case "xlsx":
		cfg.Metadata = config.MetadataConfig{Type: "xlsx", XLSX: config.XLSXConfig{Path: xlsxPath}}
	case "database":
		cfg.Metadata = config.MetadataConfig{Type: "database"}
		cfg.Source = config.DBConfig{Type: srcType, DSN: srcDSN, Schema: srcSchema}
	}

	if srcSchema != "" {
		cfg.DDL.SchemaMapping = map[string]string{srcSchema: srcSchema}
	}

	return cfg
}

// recommendSchemaMapping 生成默认推荐的 schema→目标用户映射：
//   - 未指定目标 schema → 推荐同名（迁移/DDL 通用默认，OB 目标即"默认推荐用户
//     = 源 schema 名"，需在目标侧预建该用户）；
//   - 显式 --target-schema → 直接使用（PG 多用户场景：源 schema 可映射到任意
//     目标用户，如 src_hr → MIG_PG_HR）；
//   - 嵌入式目标无 schema 概念 → 空映射。
func RecommendSchemaMapping(srcSchema, tgtSchema, tgtType string) map[string]string {
	if srcSchema == "" {
		return nil
	}
	if isEmbedded(tgtType) {
		return nil
	}
	effective := tgtSchema
	if effective == "" {
		effective = srcSchema
	}
	return map[string]string{srcSchema: effective}
}

func BuildMigrateConfig(srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema string) *config.Config {
	if tgtSchema == "" {
		tgtSchema = srcSchema
	}
	schemaMapping := RecommendSchemaMapping(srcSchema, tgtSchema, tgtType)

	return &config.Config{
		General:  config.GeneralConfig{LogLevel: "info"},
		Metadata: config.MetadataConfig{Type: "database"},
		Source:   config.DBConfig{Type: srcType, DSN: srcDSN, Schema: srcSchema},
		Target:   config.DBConfig{Type: tgtType, DSN: tgtDSN, Schema: tgtSchema},
		DDL: config.DDLConfig{
			TargetDialect:      tgtType,
			IncludeIfNotExists: true,
			SchemaMapping:      schemaMapping,
		},
		Export: config.ExportConfig{
			CSV: config.ExportCSVConfig{
				Delimiter:          ",",
				Header:             true,
				NullRepresentation: "\\N",
			},
			Batch: config.BatchConfig{PageSize: 5000},
			Parallel: config.ParallelConfig{
				Enabled:    true,
				MaxWorkers: 4,
			},
		},
		Import: config.ImportConfig{
			CSV:    config.ImportCSVConfig{NullMarker: "\\N"},
			Target: config.ImportTargetConfig{TruncateBefore: true},
			Batch: config.ImportBatchConfig{
				CommitInterval: 1000,
				ErrorPolicy:    "skip_row",
			},
			// FK-aware order: parents before children, sequential. Slower than
			// parallel but avoids silent skip_row data loss on FK-linked schemas.
			Parallel: config.ParallelConfig{
				Enabled:            true,
				MaxWorkers:         4,
				RespectForeignKeys: true,
			},
			DataTransforms: config.DataTransforms{
				DatetimeFormat: "yyyyMMddHHmmss",
				TrimStrings:    true,
				NullIf:         []string{"NULL", "null", "\\N"},
			},
		},
	}
}

// buildFullConfig builds a complete config with ALL sections for the "full" scenario.
// Unlike scenario-specific builders, full mode always includes all 8 sections
// (general, metadata, source/target, ddl, select_gen, export, import) with comments
// explaining which commands actually use each section.
func BuildFullConfig(metaType, srcType, srcDSN, srcSchema, tgtType, tgtDSN, tgtSchema, csvPath, xlsxPath string) *config.Config {
	schemaMapping := RecommendSchemaMapping(srcSchema, tgtSchema, tgtType)

	cfg := &config.Config{
		ForceAllSections: true,
		General:          config.GeneralConfig{LogLevel: "info"},
		Metadata:         config.MetadataConfig{Type: metaType},
		// Source always appears in full template; comment explains it's database-only.
		Source: config.DBConfig{
			Type:   srcType,
			DSN:    srcDSN,
			Schema: srcSchema,
		},
		Target: config.DBConfig{
			Type:   tgtType,
			DSN:    tgtDSN,
			Schema: tgtSchema,
		},
		DDL: config.DDLConfig{
			TargetDialect:      tgtType,
			IncludeComments:    true,
			IncludeIfNotExists: true,
			SchemaMapping:      schemaMapping,
		},
		// select_gen always appears; comment explains gen-select only.
		SelectGen: config.SelectGenConfig{
			OutputDir: "./output/select/",
			Batch: config.BatchConfig{
				Method:   "cursor",
				PageSize: 5000,
			},
		},
		Export: config.ExportConfig{
			OutputDir: "./output/data/",
			Format:    "csv",
			CSV: config.ExportCSVConfig{
				Delimiter:          ",",
				QuoteChar:          "\"",
				Header:             true,
				NullRepresentation: "\\N",
			},
			Batch:        config.BatchConfig{PageSize: 5000},
			Parallel:     config.ParallelConfig{Enabled: true, MaxWorkers: 4},
			Tables:       config.TableListConfig{Include: []string{"*"}},
			FiltersCheck: "count", // 条件导出门禁（export.filters 非空时生效）
		},
		Import: config.ImportConfig{
			SourceDir: "./output/data/",
			Format:    "csv",
			CSV:       config.ImportCSVConfig{NullMarker: "\\N"},
			Target:    config.ImportTargetConfig{TruncateBefore: true},
			Batch: config.ImportBatchConfig{
				CommitInterval: 1000,
				ErrorPolicy:    "skip_row",
			},
			// FK-aware order — see buildMigrateConfig.
			Parallel: config.ParallelConfig{Enabled: true, MaxWorkers: 4, RespectForeignKeys: true},
			DataTransforms: config.DataTransforms{
				DatetimeFormat: "yyyyMMddHHmmss",
				TrimStrings:    true,
				NullIf:         []string{"NULL", "null", "\\N"},
			},
		},
	}

	// Populate the active metadata source
	switch metaType {
	case "csv":
		if csvPath == "" {
			csvPath = "./testdata/csv/"
		}
		cfg.Metadata.CSV.Path = csvPath
	case "xlsx":
		if xlsxPath == "" {
			xlsxPath = "./metadata/schema.xlsx"
		}
		cfg.Metadata.XLSX.Path = xlsxPath
		cfg.Metadata.XLSX.DataOutputDir = "./output/data/"
	}

	return cfg
}
