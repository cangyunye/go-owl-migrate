# Developer Guide

## Prerequisites

- Go 1.21+
- Make
- golangci-lint (optional, for `make lint`)

## Project Layout

```
cmd/migrate/main.go          # Entry point
internal/
  cmd/                       # Cobra commands (validate, gen-ddl, gen-select, export, import, migrate)
    metadata.go              # Unified metadata loader (CSV ↔ database dispatch)
  metadata/                  # Core data types
    metadata.go              # SchemaModel, TableDef, ColumnDef, etc.
    csv/                     # CSV parser, loader, validator
    extractor/               # Live database metadata extraction (PG, MySQL, Oracle)
  dialect/                   # Dialect system (TypeMapper, DDLBuilder, DMLHelper)
    oracle/                  # Oracle dialect
    postgres/                # PostgreSQL dialect
    mysql/                   # MySQL dialect
    goldendb/                # GoldenDB dialect (embeds mysql)
    oceanbase/               # OceanBase dialect (embeds oracle/mysql)
  generator/                 # DDL and SELECT statement generators
  transfer/                  # Data transfer pipeline
    exporter/                # CSV export (cursor-paginated reads)
    importer/                # CSV import (batched transactions)
  config/                    # YAML config loading
  registry/                  # Global dialect registry
  logger/                    # Structured logging (zap)
```

## Adding a New Source Dialect

1. Create a querier in `internal/metadata/extractor/` implementing `MetadataQuerier`
2. Register in `extractor.go` `init()`
3. Add dialect alias to `normalizeDBType()` if needed
4. Update `openDB()` in `internal/cmd/metadata.go` for the new driver

## Adding a New Target Dialect

1. Create a dialect in `internal/dialect/` implementing the `Dialect` interface
2. Register in `internal/registry/registry.go`
3. Update the type map in `buildCreateTableSQL()` in `internal/cmd/import.go`

## Testing

```bash
# Unit tests
go test ./...

# Single package
go test -v ./internal/metadata/csv/

# Full test with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Logging

The project uses `go.uber.org/zap`. Set `general.log_level: debug` in config for verbose output.

## Conventions

- **Import ordering**: stdlib → third-party → internal (separated by blank lines)
- **Receiver naming**: single-letter for struct methods
- **Error wrapping**: `fmt.Errorf("context: %w", err)` throughout
- **Config access**: pass `*config.Config` through call chain, avoid global state
