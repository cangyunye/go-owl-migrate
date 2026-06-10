# go-owl-migrate Documentation

Offline-first database migration tool for Oracle, PostgreSQL, MySQL, and derivative databases (GoldenDB, OceanBase, PanWeiDB, OpenGaussDB).

## Documents

| Document | Description |
|---|---|
| [Getting Started](getting-started.md) | Installation, quick start, first migration |
| [CLI Commands](cli-commands.md) | Full command reference (validate, gen-ddl, gen-select, gen-insert, export, import, migrate) |
| [Configuration](config.md) | All configuration options with examples |
| [CSV Metadata Format](csv-format.md) | CSV file format for offline schema definition |
| [Migration Pipeline](migration-pipeline.md) | End-to-end export/import pipeline, checkpoint/resume, error handling, encoding |
| [Dialect & Type Mapping](dialect-mapping.md) | Supported dialects, type mapping system, database-specific behavior |
| [Developer Guide](development.md) | Project structure, testing, extending dialects |

## Quick Summary

```bash
# 0. Generate a config from CLI parameters (no manual YAML editing)
owl-migrate init --source-type oracle --source-dsn "oracle://user:pass@host:1521/service" \
  --source-schema SCOTT --target-type postgres \
  --target-dsn "postgres://user:pass@localhost:5432/migrate" --target-schema public

# 1. Validate metadata
owl-migrate validate -c ./migrate.yaml

# 2. Generate DDL for target database
owl-migrate gen-ddl -c ./migrate.yaml

# 3. Generate SELECT statements for data export
owl-migrate gen-select -c ./migrate.yaml

# 4. Run end-to-end migration (export + import)
owl-migrate migrate -c ./migrate.yaml

# 5. Or use offline mode: export to CSV, then generate INSERT SQL
owl-migrate migrate -c ./migrate.yaml --sql-out ./output/insert/
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
