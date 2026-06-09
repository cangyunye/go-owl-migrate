# go-owl-migrate

Database migration tool for Oracle → MySQL, PostgreSQL → MySQL, and cross-database schema & data migration. Part of the **owl** family of database tools.

## Features

- **Offline-first**: Generate DDL and data migration scripts from CSV metadata without connecting to any database
- **Live extraction**: Extract schema metadata directly from PostgreSQL, MySQL, or Oracle
- **Cross-dialect DDL generation**: Convert source types (NUMBER→DECIMAL, VARCHAR2→VARCHAR, BOOLEAN→TINYINT, etc.)
- **Data migration**: Export source data to CSV, import into target with batched transactions
- **End-to-end migration**: Single `migrate` command does schema + data in one pass
- **Migration report**: Detailed per-table row count, errors, and duration summary

## Quick Start

```bash
# Install
go install github.com/cangyunye/go-owl-migrate/cmd/migrate@latest

# Validate config
owl-migrate validate -c ./migrate.yaml

# Generate DDL scripts only
owl-migrate gen-ddl -c ./migrate.yaml -o ./output/ddl/

# Full end-to-end migration
owl-migrate migrate -c ./migrate.yaml --temp-dir ./temp/ -r ./report.json
```

## Usage

### 1. Configuration

Create a YAML config file (see [docs/config.md](docs/config.md) for full reference):

```yaml
metadata:
  type: database        # or "csv" for offline mode

source:
  type: postgres
  dsn: "host=127.0.0.1 port=5432 dbname=mydb sslmode=disable"
  schema: public

target:
  type: mysql
  dsn: "user:pass@tcp(127.0.0.1:3306)/mydb"

export:
  batch:
    page_size: 5000
import:
  batch:
    commit_interval: 1000
```

### 2. Commands

| Command | Description |
|---------|-------------|
| `validate` | Validate config and metadata source (CSV or database) |
| `gen-ddl` | Generate CREATE TABLE DDL for the target dialect |
| `gen-select` | Generate SELECT queries for data export |
| `export` | Export source data to CSV files |
| `import` | Import CSV data into target database |
| `migrate` | End-to-end: export → create tables → import → report |

### 3. Metadata Sources

- **Database** (`metadata.type: database`): Live schema introspection from PostgreSQL, MySQL, Oracle, GoldenDB, OceanBase
- **CSV** (`metadata.type: csv`): Pre-defined table/column definitions in CSV files (see `testdata/csv/` for format)

## Supported Dialects

| Source | Target |
|--------|--------|
| PostgreSQL 15+ | MySQL |
| MySQL 8+ | MySQL (same-dialect) |
| Oracle 19c/21c | MySQL |
| GoldenDB (MySQL mode) | MySQL |
| OceanBase (MySQL/Oracle mode) | MySQL |

## Migration Report

After `migrate`, a JSON report is generated:

```json
{
  "source_dialect": "postgres",
  "target_dialect": "mysql",
  "status": "SUCCESS",
  "duration": "1.2s",
  "tables": [
    {"schema": "public", "table": "dept", "expected": 4, "actual": 4}
  ],
  "total_expected": 18,
  "total_actual": 18
}
```

## License

MIT
