# Developer Guide

## Prerequisites

- Go 1.21+
- Make
- golangci-lint (optional, for `make lint`)
- Docker (for integration tests with PostgreSQL/MySQL/Oracle containers)

## Project Layout

```
cmd/migrate/main.go          # Entry point
internal/
  cmd/                       # Cobra commands (validate, export ddl/data/insert, gen-select, import, migrate)
    root.go                  # Root command, flag definitions, subcommand registration
    init.go                  # owl-migrate init — config generation from CLI params
    metadata.go              # Unified metadata loader (CSV ↔ database dispatch)
    validate.go              # owl-migrate validate — metadata validation
    export_root.go           # owl-migrate export — parent command
    export_ddl.go            # owl-migrate export ddl — DDL generation
    export_data.go           # owl-migrate export data — data export (CSV/SQL/XLSX)
    export_insert.go         # owl-migrate export insert — INSERT SQL from CSV
    genddl.go                # owl-migrate gen-ddl (hidden alias for export ddl)
    genselect.go             # owl-migrate gen-select — SELECT statement generation
    geninsert.go             # owl-migrate gen-insert (hidden alias for export insert)
    import.go                # owl-migrate import — data import from CSV
    migrate_cmd.go           # owl-migrate migrate — end-to-end migration
  metadata/                  # Core data types
    model.go                 # SchemaModel, TableDef, ColumnDef, PrimaryKeyDef, IndexDef,
                             # ForeignKeyDef, ViewDef, MViewDef, TriggerDef, FunctionDef,
                             # SequenceDef, SynonymDef, PackageDef
    csv/                     # CSV parser, loader, validator
      parser.go              # CSV row parsers for each metadata entity
      loader.go              # Loader — multi-file CSV reader → SchemaModel
      validator.go           # SchemaModel validator
    extractor/               # Live database metadata extraction (PG, MySQL, Oracle)
  dialect/                   # Dialect system (TypeMapper, DDLBuilder, DMLHelper, Features)
    dialect.go               # Interfaces: LogicalType, TypeMapper, IdentifierQuoter, Features,
                             # DDLBuilder, DMLHelper, Dialect (composed struct)
    oracle/                  # Oracle dialect — type mapping, DDL, pagination, quoting
    postgres/                # PostgreSQL dialect
    mysql/                   # MySQL dialect
    goldendb/                # GoldenDB dialect (embeds mysql + oracle sub-dialects)
    oceanbase/               # OceanBase dialect (embeds oracle + mysql sub-dialects)
  generator/                 # DDL and SELECT/INSERT statement generators
    ddl.go                   # DDLGenerator — orchestrates DDL per object type
    select.go                # SelectGenerator — paginated SELECT generation
    insert.go                # InsertGenerator — INSERT SQL from CSV data
  transfer/                  # Data transfer pipeline
    exporter/                # CSV/SQL/XLSX export (cursor-paginated reads, parallel workers)
      exporter.go            # Exporter with cursor pagination, CSV/SQL/XLSX output via ExportWriter
      writer.go              # ExportWriter interface + csvWriter, sqlWriter, xlsxWriter
    importer/                # CSV import (batched transactions, data transforms)
      importer.go            # Importer with encoding conversion, datetime transform,
                             # boolean mapping, binary decoding, error policies
  config/                    # YAML config loading (cobra + yaml.v3)
    config.go                # Config struct, validation, default values, table filtering
    migration.go             # Migration-specific config helpers
  registry/                  # Global dialect registry
  logger/                    # Structured logging (zap)
  mapping/                   # External type mapping file (YAML) loader
    mapping.go               # TypeMappingFile, rules, semantic overrides, parameterized conditions
testdata/
  csv/                       # SCOTT schema test data (EMP, DEPT, BONUS)
  sql/                       # SQL seed files for test containers
  db/                        # Docker compose for test databases
```

## Core Concepts

### Metadata Model

The central data types are in `internal/metadata/model.go`:

- **SchemaModel**: Aggregate container — holds all tables, views, indexes, FK constraints, triggers, functions, sequences, synonyms
- **TableDef**: A single table with columns, PKs, indexes, FKs, triggers
- **ColumnDef**: Column with data type, length, precision, scale, nullable, default, identity info

Metadata is loaded from CSV files (via `metadata/csv/`) or extracted live from database `information_schema` / `ALL_*` views (via `metadata/extractor/`).

### Dialect System

Each dialect is a composed struct of five interfaces defined in `internal/dialect/dialect.go`:

- **TypeMapper**: Maps raw DB types ↔ `LogicalType` (database-independent type base + length/precision/scale)
- **IdentifierQuoter**: Quotes identifiers per database rules (backtick for MySQL, double-quote for PG/Oracle)
- **Features**: Describes DB capabilities (transactional DDL, IF NOT EXISTS, max identifier length, etc.)
- **DDLBuilder**: Generates DDL (CREATE TABLE/INDEX/VIEW/SEQUENCE/TRIGGER/FUNCTION/etc.)
- **DMLHelper**: Generates pagination clauses, cursor-based pagination, value formatting

### Compound Dialects

GoldenDB and OceanBase dialects inherit from core dialects via file-level embedding. For example, `oceanbase-mysql` inherits from `mysql` and overrides specific behaviors:

```go
// OceanBase MySQL — inherits MySQL, differs on:
//   - TRUNCATE is transactional (vs MySQL where it's not)
//   - No FULLTEXT index support
//   - No MyISAM engine (only InnoDB)
//   - Supports SEQUENCE (MySQL 8.0 doesn't have native SEQUENCE)
```

### Transfer Pipeline

**Exporter** (`internal/transfer/exporter/`):
- Reads data from source DB using cursor-based pagination (keyset pagination on PK)
- Falls back to simple LIMIT pagination for tables without PKs
- Supports multiple output formats via ExportWriter interface: CSV, SQL (INSERT), XLSX (Excel)
- Configurable delimiter, quoting, null representation for CSV; dialect-aware formatting for SQL
- Hex-encodes binary columns (BLOB/BYTEA/RAW)
- Supports parallel table export via worker pool

**Importer** (`internal/transfer/importer/`):
- Reads CSV files and inserts into target DB using batched transactions
- Applies data transformations: datetime format conversion, string trimming, encoding decode
- Handles boolean conversion (text → numeric) for MySQL/Oracle targets
- Decodes hex-encoded binary columns back to bytes
- Supports configurable error policies (stop/skip/log)
- Supports parallel table import via worker pool
- Sets Oracle session date format parameters for proper conversion

### Checkpoint/Resume

The `migrate` command persists per-table state to a JSON checkpoint file, tracking export and import status separately. On `--resume`:
- SUCCESS tables are skipped
- FAIL tables are truncated and re-imported
- Partially-exported tables restart from scratch

## Testing

```bash
# All unit tests
go test ./...

# Single package
go test -v ./internal/metadata/csv/
go test -v ./internal/dialect/oceanbase/
go test -v ./internal/mapping/
go test -v ./internal/transfer/exporter/

# Single test function
go test -run TestNewColumnDef -v
go test -run TestParseColumns_Basic -v

# Full test with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# With external database (requires Docker containers in testdata/db/)
# Export tests connect to PostgreSQL on localhost:5432
```

### Integration Tests

The exporter test (`internal/transfer/exporter/exporter_test.go`) connects to a live PostgreSQL instance:

```bash
# Start test database containers
cd testdata/db && docker-compose up -d

# Run all tests including integration
go test -v ./internal/transfer/exporter/
```

Key integration test: `TestExportTables_ContinueOnError` — verifies that when one table in the export list doesn't exist (fails), the other tables still export successfully and results are collected for all tables.

## Adding a New Source Dialect

1. Create a querier in `internal/metadata/extractor/` implementing `MetadataQuerier`
2. Register in `extractor.go` `init()`
3. Add dialect alias to `normalizeDBType()` if needed
4. Update `openDB()` in `internal/cmd/metadata.go` for the new driver

## Adding a New Target Dialect

1. Create a dialect in `internal/dialect/` implementing the `Dialect` interface:
   - Type mapper (raw types → logical types → target types)
   - DDL builder (CREATE TABLE/INDEX/VIEW statements)
   - DML helper (pagination clauses, placeholder syntax)
   - Feature flags (transactional DDL, IF NOT EXISTS support, etc.)
   - Identifier quoter (backtick vs double-quote)
2. Register in `internal/registry/registry.go`
3. Add to `ValidDialects` in `internal/config/config.go`
4. Update `openDB()` in `internal/cmd/metadata.go`
5. Update `buildCreateTableSQL()` in `internal/cmd/import.go`
6. Write tests in `internal/dialect/<name>/` package covering:
   - Type mapping consistency with parent dialect (for compound dialects)
   - DDL generation for each object type
   - Feature flag correctness
   - Pagination syntax

## Logging

The project uses `go.uber.org/zap`. Set `general.log_level: debug` in config for verbose output.

The logger is configured per-command:
- Development config with color output by default
- JSON format available via `general.log_format: json`
- File output available via `general.log_file`

## Conventions

- **Import ordering**: stdlib → third-party → internal (separated by blank lines)
- **Receiver naming**: single-letter for struct methods
- **Error wrapping**: `fmt.Errorf("context: %w", err)` throughout
- **Config access**: pass `*config.Config` through call chain, avoid global state
- **Dialect registry**: single `init()` registration; no global state beyond the registry map
- **File naming**: `internal/dialect/<name>/<name>.go` for dialect implementations
- **Test file naming**: `<name>_test.go` in the same package; integration tests may use build tags

## Build

```bash
# Current platform
make build

# Cross-platform
make build/linux   # Linux AMD64
make build/windows # Windows AMD64

# Manual build
go build -o owl-migrate ./cmd/migrate/main.go

# Code quality
go fmt ./...
go vet ./...
make lint          # golangci-lint (if installed)
```
