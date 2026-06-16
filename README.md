# go-owl-migrate

Database migration tool for cross-database schema & data migration: Oracle, PostgreSQL, MySQL, GoldenDB, OceanBase, PanWeiDB, OpenGaussDB. Part of the **owl** family of database tools.

> 📚 **Full documentation**: See [docs/index.md](docs/index.md) for the complete documentation index.

## Features

- **Offline-first**: Generate DDL, SELECT, and INSERT SQL from CSV metadata — no database connection required
- **Live extraction**: Extract schema metadata directly from PostgreSQL, MySQL, or Oracle
- **Cross-dialect DDL generation**: NUMBER↔DECIMAL↔INTEGER, VARCHAR2↔VARCHAR, BOOLEAN↔TINYINT(1), CLOB↔TEXT↔LONGTEXT, etc.
- **Compound dialects**: GoldenDB (MySQL/Oracle mode), OceanBase (MySQL/Oracle mode), PanWeiDB, OpenGaussDB
- **Data migration**: Export source data to CSV with cursor-based pagination, import with batched transactions
- **Checkpoint/Resume**: Per-table state persists to disk — interrupted migrations pick up where they left off
- **Continue on error**: Per-table error isolation — one failing table doesn't abort the whole migration
- **SQL output mode**: Generate INSERT SQL files instead of writing directly to the target database
- **Encoding conversion**: GBK, LATIN1, ISO-8859-*, Windows-1252 to UTF-8 conversion during import
- **Data transforms**: Compact datetime (yyyyMMddHHmmss) auto-formatting, string trimming, boolean mapping, binary hex encoding
- **Error policies**: Per-row error handling — stop, skip_row, or log_only
- **Migration report**: Detailed JSON report with per-table row counts, errors, and duration
- **Parallel export/import**: Concurrent table processing via worker pool

## Quick Start

```bash
# Install
go install github.com/cangyunye/go-owl-migrate/cmd/migrate@latest

# Or build from source
make build
go build -o owl-migrate ./cmd/migrate/main.go

# Generate config from CLI parameters
owl-migrate init --source-type oracle --source-dsn "oracle://u:p@host:1521/service" \
  --source-schema SCOTT --target-type postgres \
  --target-dsn "postgres://u:p@localhost:5432/migrate" --target-schema public \
  -o ./migrate.yaml

# Validate metadata
owl-migrate validate -c ./migrate.yaml

# Generate DDL scripts for the target database
owl-migrate export ddl -c ./migrate.yaml -o ./output/ddl/

# Run end-to-end migration (export + create tables + import)
owl-migrate migrate -c ./migrate.yaml

# Or use SQL output mode (no target database connection needed)
owl-migrate migrate -c ./migrate.yaml --sql-out ./output/insert/
```

## Commands

| Command | Description |
|---------|-------------|
| `init`       | Generate config file from CLI parameters |
| `validate`   | Validate metadata (CSV or database) |
| `export ddl`    | Generate CREATE TABLE/INDEX/VIEW DDL for target dialect |
| `export data`   | Export source database data to CSV/SQL/XLSX files |
| `export insert` | Generate INSERT SQL from CSV data (offline mode) |
| `gen-select`    | Generate paginated SELECT queries for data export |
| `import`        | Import CSV data into target database |
| `migrate`       | End-to-end: export → create tables → import → report |

## Supported Dialects

| Database | Source | Target | DDL | Export | Import |
|---|---|---|---|---|---|
| Oracle | ✓ | ✓ | ✓ | ✓ | ✓ |
| PostgreSQL | ✓ | ✓ | ✓ | ✓ | ✓ |
| MySQL | ✓ | ✓ | ✓ | ✓ | ✓ |
| GoldenDB (MySQL mode) | ✓ | ✓ | ✓ | ✓ | ✓ |
| GoldenDB (Oracle mode) | ✓ | ✓ | ✓ | ✓ | ✓ |
| OceanBase (MySQL mode) | ✓ | ✓ | ✓ | ✓ | ✓ |
| OceanBase (Oracle mode) | ✓ | ✓ | ✓ | ✓ | ✓ |
| PanWeiDB | ✓ | ✓ | ✓ | ✓ | ✓ |
| OpenGaussDB | ✓ | ✓ | ✓ | ✓ | ✓ |

## Documentation

| Document | Description |
|---|---|
| [Getting Started](docs/getting-started.md) | Installation, quick start, workflows |
| [CLI Commands](docs/cli-commands.md) | Full command reference with flags and examples |
| [Configuration](docs/config.md) | All YAML configuration options |
| [CSV Metadata Format](docs/csv-format.md) | CSV file format for offline schema definition |
| [Migration Pipeline](docs/migration-pipeline.md) | Export/import, checkpoint/resume, encoding, error handling |
| [Dialect & Type Mapping](docs/dialect-mapping.md) | Dialect system, type mapping, feature flags |
| [Developer Guide](docs/development.md) | Project structure, testing, adding dialects |

## License

MIT
