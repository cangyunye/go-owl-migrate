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

### Workflow A: Interactive Config Generation (Recommended)

Run `init` without arguments for a guided interview:

```text
$ owl-migrate init

=== owl-migrate Configuration Generator ===

What is your metadata source (csv/xlsx/database): csv
Path to CSV metadata directory (default: ./testdata/csv/): ./my_metadata/
Target database dialect for DDL generation (oracle/postgres/mysql/...): postgres
Target database DSN (optional, leave blank for DDL-only): 
What do you want to generate (ddl/data/all): all

Configuration written to ./migrate.yaml
```

### Workflow B: Flag-Driven Config

Use `init` with flags for automation or CI:

```bash
# Online mode — live database metadata
owl-migrate init \
  --source-type oracle --source-dsn "oracle://user:pass@host:1521/service" \
  --source-schema SCOTT --target-type postgres -o ./migrate.yaml

# Offline mode — CSV metadata, no database needed
owl-migrate init --metadata-type csv --target-type postgres -o ./migrate.yaml

# Offline mode — Excel metadata
owl-migrate init --metadata-type xlsx --target-type postgres -o ./migrate.yaml

# 2. Validate metadata (checks FK references, missing tables, etc.)
owl-migrate validate -c ./migrate.yaml

# 3. Generate DDL for the target database
owl-migrate export ddl -c ./migrate.yaml -o ./output/ddl/

# 4. Run full migration (export data → create tables → import)
owl-migrate migrate -c ./migrate.yaml

# 5. Or step by step: export data, then import separately
owl-migrate export -c ./migrate.yaml -o ./output/data/
owl-migrate import -c ./migrate.yaml
```

### Workflow C: Offline-Only (No Database Connections)

Use CSV files for both metadata and data — no source or target database needed:

```bash
# 1. Place CSV metadata files in a directory (see csv-format.md)
# 2. Place CSV data files in a data directory
# 3. Generate INSERT SQL files (offline)
owl-migrate export insert \
  -c ./migrate.yaml \
  -d ./output/data/ \
  -o ./output/insert/ \
  --dialect postgres
```

### Workflow D: CSV → INSERT SQL (Zero Config)

This is the simplest way to generate INSERT SQL from CSV data files.
**No configuration file, no database connection needed.**

```bash
# 1. Prepare your CSV data files
#    File naming: {schema}.{table}.csv
#    Example: scott.emp.csv, scott.dept.csv
#    First row = column headers, remaining rows = data
#    (see docs/csv-format.md for format details)

# 2. Generate INSERT SQL (standalone mode)
owl-migrate export insert \
  -d ./data/ \                    # Directory with CSV files
  -o ./sql/ \                     # Output directory for INSERT SQL
  --dialect postgres               # oracle | postgres | mysql

# 3. Review the generated SQL files
cat ./sql/scott.emp.insert.sql
# BEGIN;
# INSERT INTO "scott"."emp" ("id", "name")
# VALUES
#   (1, 'foo'),
#   (2, 'bar');
# COMMIT;

# With a config file (for precise type mapping):
owl-migrate init --metadata-type csv --target-type postgres -o ./migrate.yaml
owl-migrate export insert -c ./migrate.yaml -d ./data/ -o ./sql/
```

### Workflow E: CSV Metadata + SQL Output Mode

Use the `migrate` command with `--sql-out` to generate INSERT SQL files instead of writing directly to the target database:

```bash
owl-migrate migrate -c ./migrate.yaml --sql-out ./output/insert/
```

This produces ready-to-execute SQL files that can be reviewed and applied manually.

### Workflow F: Quick Start with Test Data

The project includes SCOTT schema test data (EMP, DEPT, BONUS tables):

```bash
# Validate built-in test metadata
owl-migrate validate -c ./configs/migrate.example.yaml

# Generate DDL from test metadata
owl-migrate export ddl -c ./configs/migrate.example.yaml

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
