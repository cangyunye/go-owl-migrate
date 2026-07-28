# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Documentation

User-facing documentation lives in `docs/` — see [docs/index.md](docs/index.md) for the full index.

## Project Overview

go-owl-migrate is a database migration tool for Oracle, PostgreSQL, MySQL, GoldenDB, OceanBase, PanWeiDB, OpenGaussDB. Offline-first: generate DDL, INSERT, and data migration scripts from CSV metadata or live database introspection.

Part of the `owl` family:
- **go-owl** (`github.com/cangyunye/go-owl`) — Main CLI: node management, batch exec, file transfer, playbooks, SSH sessions
- **go-owl-metrics** (`github.com/sinvigil/go-owl-metrics`) — node_exporter metrics scraping and terminal dashboard
- **go-owl-tui** (`github.com/cangyunye/go-owl-tui`) — BubbleTea TUI frontend
- **go-owl-migrate** (`github.com/cangyunye/go-owl-migrate`) — This project

## Build & Test Commands

```bash
# Build
make build                          # Current platform → build/<os>-<arch>/owl-migrate
go build -o owl-migrate ./cmd/migrate/main.go

# Cross-platform
make build/linux                    # Linux AMD64
make build/windows                  # Windows AMD64

# Test
go test ./...                       # All packages
go test -v ./internal/metadata/csv/ # Single package
go test -run TestNewColumnDef -v    # Single test
make test                           # go test -v ./...

# Code quality
go fmt ./... && go vet ./...
make lint                           # golangci-lint (if installed)

# Run CLI
go run ./cmd/migrate/main.go validate -c ./configs/migrate.example.yaml
go run ./cmd/migrate/main.go export ddl -c ./configs/migrate.example.yaml
go run ./cmd/migrate/main.go gen-select -c ./configs/migrate.example.yaml
go run ./cmd/migrate/main.go init --help

# Run end-to-end migration (requires live databases)
go run ./cmd/migrate/main.go migrate -c ./configs/migrate.example.yaml --sql-out ./output/insert/
```

## Project Architecture

```
cmd/migrate/main.go          # Entry point
internal/
  cmd/                       # Cobra commands (init, validate, export ddl/data/insert, gen-select, import, migrate)
    migrate_cmd.go           # End-to-end pipeline with checkpoint/resume, SQL output mode
    init.go                  # Config file generator from CLI parameters
    import.go                # Target table creation with cross-dialect type mapping
  metadata/                  # Unified metadata model (TableDef, ColumnDef, SchemaModel, etc.)
    model.go                 # Also: ViewDef, MViewDef, TriggerDef, FunctionDef, SequenceDef, PackageDef, SynonymDef
    csv/                     # CSV parser, Loader (multi-file→SchemaModel), Validator
    extractor/               # Live database metadata extraction (PG, MySQL, Oracle)
  dialect/                   # Dialect system (composed interfaces)
    dialect.go               # LogicalType, TypeMapper, DDLBuilder, DMLHelper, Dialect, Features
    oracle/                  # Oracle dialect
    postgres/                # PostgreSQL dialect
    mysql/                   # MySQL dialect
    goldendb/                # GoldenDB dialect (embeds mysql + oracle)
    oceanbase/               # OceanBase dialect (embeds oracle + mysql)
  registry/                  # Global dialect registry (auto-registers built-in dialects)
  generator/                 # DDL, SELECT, and INSERT statement generators
    ddl.go                   # DDLGenerator — orchestrates DDL per object type
    select.go                # SelectGenerator — paginated SELECT generation
    insert.go                # InsertGenerator — INSERT SQL from CSV data
  transfer/                  # Data transfer pipeline
    exporter/                # CSV export (cursor-paginated reads, parallel workers, binary encoding)
    importer/                # CSV import (batched transactions, encoding conversion, data transforms, error policies)
  config/                    # YAML config loading (cobra + yaml.v3)
  paths/                     # Centralized path resolution (~/.owl/migrate/, env vars)
  mapping/                   # External type mapping file loader (YAML)
  logger/                    # Structured logging (zap)
```

### Core Types

- **`SchemaModel`** — aggregate container for all metadata (tables, views, indexes, FKs, triggers, functions, sequences)
- **`TableDef`** — table with columns, primary keys, indexes, foreign keys
- **`ColumnDef`** — column with data type, nullable, default, identity info
- **`LogicalType`** — database-independent type (Base enum + Length/Precision/Scale)
- **`Dialect`** — composed struct of TypeMapper + IdentifierQuoter + Features + DDLBuilder + DMLHelper

### Key Design Decisions

- **CSV metadata = data-only migration**. Structure migration requires live database as metadata source
- **Dialect composition**: Oracle→OceanBase-Oracle reuses 95% of code via file-level embedding
- **Owner/Namespace**: SchemaModel stores original Owner; `schema_mapping` converts to target Namespace at output time
- **Type mapping priority**: type_overrides > semantic_overrides > parameterized > exact_mappings > builtin
- **No single-table parallel writes**: write concurrency is table-level only
- **Test data**: `testdata/csv/` contains SCOTT schema (EMP, DEPT, BONUS) for testing
- **Full design doc**: See plan file at `/Users/vigil/.claude/plans/serialized-stirring-hickey.md`

### File Layout & Path Resolution

Tool-level state lives under `~/.owl/migrate/` (isolated from go-owl's `~/.owl/`):

```
~/.owl/migrate/              # Tool state (small, cross-project)
  migrate.yaml               # Global default config
  owl-migrate.db             # serve mode SQLite (job history)
  logs/                      # Application logs
  configs/library/           # Reusable config library (serve)
  temp/                      # Heartbeat, web UI generation temp
```

Project-level artifacts stay CWD-relative (potentially large):

```
./migrate.yaml               # Project config (init default)
./output/ddl/                # DDL generation output
./output/select/             # SELECT generation output
./output/insert/             # INSERT SQL output
./output/data/               # Data export output
./output/temp/               # Checkpoint, CSV intermediates
./output/migration_report.json
```

Config resolution order: `-c` flag > `./migrate.yaml` > `$OWL_MIGRATE_CONFIG` > `~/.owl/migrate/migrate.yaml`

Environment variables (override tool-state defaults):

| Variable | Purpose |
|----------|---------|
| `OWL_MIGRATE_HOME` | Override `~/.owl/migrate` root |
| `OWL_MIGRATE_CONFIG` | Override global config path |
| `OWL_MIGRATE_DB_PATH` | Override SQLite path |
| `OWL_MIGRATE_LOG_DIR` | Override log directory |
