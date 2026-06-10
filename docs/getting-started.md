# Getting Started

## Installation

```bash
# Build from source
go build -o owl-migrate ./cmd/migrate/main.go

# Or use make
make build

# Cross-platform
make build/linux
make build/windows
```

## Workflows

### Workflow A: Config-Driven (Recommended)

Use `init` to generate a complete config file from CLI parameters, then run:

```bash
# 1. Generate configuration
owl-migrate init \
  --source-type oracle \
  --source-dsn "oracle://user:pass@host:1521/service" \
  --source-schema SCOTT \
  --target-type postgres \
  --target-dsn "postgres://user:pass@localhost:5432/migrate" \
  --target-schema public \
  --metadata-type database \
  -o ./migrate.yaml

# 2. Validate metadata (checks FK references, missing tables, etc.)
owl-migrate validate -c ./migrate.yaml

# 3. Generate DDL for the target database
owl-migrate gen-ddl -c ./migrate.yaml -o ./output/ddl/

# 4. Run full migration (export data → create tables → import)
owl-migrate migrate -c ./migrate.yaml

# 5. Or step by step: export data, then import separately
owl-migrate export -c ./migrate.yaml -o ./output/data/
owl-migrate import -c ./migrate.yaml
```

### Workflow B: Offline-Only (No Database Connections)

Use CSV files for both metadata and data — no source or target database needed:

```bash
# 1. Place CSV metadata files in a directory (see csv-format.md)
# 2. Place CSV data files in a data directory
# 3. Generate INSERT SQL files (offline)
owl-migrate gen-insert \
  -c ./migrate.yaml \
  -d ./output/data/ \
  -o ./output/insert/ \
  --dialect postgres
```

### Workflow C: CSV Metadata + SQL Output Mode

Use the `migrate` command with `--sql-out` to generate INSERT SQL files instead of writing directly to the target database:

```bash
owl-migrate migrate -c ./migrate.yaml --sql-out ./output/insert/
```

This produces ready-to-execute SQL files that can be reviewed and applied manually.

### Workflow D: Quick Start with Test Data

The project includes SCOTT schema test data (EMP, DEPT, BONUS tables):

```bash
# Validate built-in test metadata
owl-migrate validate -c ./configs/migrate.example.yaml

# Generate DDL from test metadata
owl-migrate gen-ddl -c ./configs/migrate.example.yaml

# Generate SELECT statements
owl-migrate gen-select -c ./configs/migrate.example.yaml
```

## Configuration File

The minimal config requires:

```yaml
metadata:
  type: csv                              # "csv" or "database"
  csv:
    path: ./metadata/

ddl:
  target_dialect: postgres               # oracle | postgres | mysql | goldendb | oceanbase | ...

source:
  type: oracle
  dsn: "oracle://user:pass@host:1521/service"
  schema: SCOTT

target:
  type: postgres
  dsn: "host=localhost port=5432 dbname=migrate user=postgres password=pass sslmode=disable"
```

See [Configuration Reference](config.md) for the full config structure.

## Next Steps

- [CLI Commands](cli-commands.md) — Detailed command flags and options
- [Migration Pipeline](migration-pipeline.md) — Export/import, checkpoint/resume, error handling
- [CSV Metadata Format](csv-format.md) — Defining schemas offline
- [Dialect & Type Mapping](dialect-mapping.md) — Database-specific details
