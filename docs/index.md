# go-owl-migrate Documentation

Offline-first database migration tool for Oracle, PostgreSQL, MySQL, and derivative databases (GoldenDB, OceanBase, PanWeiDB, OpenGaussDB).

## Documents

| Document | Description |
|---|---|
| [Getting Started](getting-started.md) | Installation, quick start, first migration |
| [CLI Commands](cli-commands.md) | Full command reference (export ddl, export data, export insert, gen-select, import, migrate) |
| [Configuration](config.md) | All configuration options with examples |
| [CSV Metadata Format](csv-format.md) | CSV file format for offline schema definition |
| [Migration Pipeline](migration-pipeline.md) | End-to-end export/import pipeline, checkpoint/resume, error handling, encoding |
| [Dialect & Type Mapping](dialect-mapping.md) | Supported dialects, type mapping system, database-specific behavior |
| [Developer Guide](development.md) | Project structure, testing, extending dialects |
| [Database Metadata Queries](database-metadata/index.md) | Full metadata query SQL reference per database type (Oracle, PostgreSQL, MySQL, DuckDB, SQLite3) |

## Quick Start

See the [README](../README.md#quick-start) for the quick-start commands, or
walk through the guided interview in [Getting Started](getting-started.md). The minimal
one-liner:

```bash
owl-migrate init --source-type oracle --source-dsn "oracle://user:pass@host:1521/service" \
  --source-schema SCOTT --target-type postgres \
  --target-dsn "postgres://user:pass@localhost:5432/migrate" --target-schema public
owl-migrate migrate -c ./migrate.yaml
```

## Supported Databases

| Database | Source Metadata Extraction | Target DDL Generation | Data Export | Data Import | Compound Dialect |
|---|---|---|---|---|---|
| Oracle | ✓ | ✓ | ✓ | ✓ | — |
| PostgreSQL | ✓ | ✓ | ✓ | ✓ | — |
| MySQL | ✓ | ✓ | ✓ | ✓ | — |
| GoldenDB (MySQL) | ✓ | ✓ | ✓ | ✓ | ✓ (embeds MySQL) |
| GoldenDB (Oracle) | ✓ | ✓ | ✓ | ✓ | ✓ (embeds Oracle) |
| OceanBase (MySQL) | ✓ | ✓ | ✓ | ✓ | ✓ (embeds MySQL) |
| OceanBase (Oracle) | ✓ | ✓ | ✓ | ✓ | ✓ (embeds Oracle) |
| PanWeiDB | ✓ | ✓ | ✓ | ✓ | ✓ (same as PG driver) |
| OpenGaussDB | ✓ | ✓ | ✓ | ✓ | ✓ (same as PG driver) |
| SQLite3 | ✓ | ✓ | ✓ | ✓ | — (embedded) |
| DuckDB | ✓ | ✓ | ✓ | ✓ | — (embedded) |
