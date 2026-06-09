# Configuration Reference

This document describes all configuration options for `go-owl-migrate`.

## Full Config Structure

```yaml
general:
  log_level: debug                          # debug | info | warn | error (default: info)

metadata:
  type: database                            # "csv" | "database"
  csv:
    path: ./metadata/                       # required when type=csv

source:
  type: postgres                            # postgres | mysql | oracle | goldendb | oceanbase
  dsn: "host=127.0.0.1 port=5432 dbname=mydb user=u password=p sslmode=disable"
  schema: public

target:
  type: mysql
  dsn: "root:pass@tcp(127.0.0.1:3306)/mydb"

ddl:
  target_dialect: mysql                     # target DDL dialect
  include_if_not_exists: true               # add IF NOT EXISTS
  include_comments: true                    # include column/table comments
  include_collation: true                   # include character set / collation
  schema_mapping:                           # map source schema to target schema
    public: myapp
    scott: SCOTT

export:
  csv:
    delimiter: ","
    quote_char: "\""
    header: true
    null_representation: "\\N"
    line_terminator: "\n"
  batch:
    page_size: 5000
  parallel:
    enabled: true
    max_workers: 4
  tables:
    include: ["*"]                          # table filter list, "*" means all

import:
  csv:
    delimiter: ","
    has_header: true
    null_marker: "\\N"
  target:
    truncate_before: true                   # TRUNCATE table before import
  batch:
    commit_interval: 1000                   # rows per transaction
    error_policy: stop                      # stop | skip_row | log_only
    max_errors_before_stop: 0               # 0 = unlimited
  parallel:
    enabled: true
    max_workers: 2
    respect_foreign_keys: false
  data_transforms:
    datetime_format: "yyyyMMddHHmmss"       # auto-convert compact datetime
    trim_strings: true
    null_if: ["NULL", "null", "\\N"]
```

## Metadata Types

### `type: csv`

Load table/column definitions from CSV files. Required files in the metadata directory:

- `tables.csv` — table definitions
- `columns.csv` — column definitions
- `indexes.csv` — index definitions (optional)
- `foreign_keys.csv` — FK definitions (optional)
- `sequences.csv` — sequence definitions (optional)
- `triggers.csv` — trigger definitions (optional)

### `type: database`

Connect to the source database specified in `source.*` configuration to extract schema metadata via `information_schema` (PG/MySQL) or `ALL_*` dictionary views (Oracle).

## Connection Strings

### PostgreSQL
```
host=127.0.0.1 port=5432 user=postgres password=pass dbname=mydb sslmode=disable
```

### MySQL
```
user:password@tcp(127.0.0.1:3306)/mydb
```

### Oracle (go-ora driver)
```
oracle://user:password@host:port/service_name
```

### GoldenDB / OceanBase

Use the same DSN format as the underlying dialect (MySQL or Oracle). Set `source.type` to `goldendb`, `goldendb-mysql`, `oceanbase-mysql`, `oceanbase-oracle`, etc.
