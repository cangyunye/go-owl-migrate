# Configuration Reference

This document describes all configuration options for `go-owl-migrate`.

You can generate an initial config file with the `init` command:

```bash
owl-migrate init --source-type oracle --source-dsn "..." --source-schema SCOTT \
  --target-type postgres --target-dsn "..." --target-schema public \
  -o ./migrate.yaml
```

## Config File Resolution

When no `-c` flag is given, the config file is resolved in this order:

1. `./migrate.yaml` (current directory, if it exists)
2. `$OWL_MIGRATE_CONFIG` (environment variable)
3. `~/.owl/migrate/migrate.yaml` (global default)

The `init` command always writes to `./migrate.yaml` by default (use `-o` to override).

## Environment Variables

Tool-level state defaults to `~/.owl/migrate/` (isolated from go-owl's `~/.owl/`).
Override with environment variables:

| Variable | Purpose | Default |
|----------|---------|---------|
| `OWL_MIGRATE_HOME` | Root directory for tool state | `~/.owl/migrate` |
| `OWL_MIGRATE_CONFIG` | Global config file path | `~/.owl/migrate/migrate.yaml` |
| `OWL_MIGRATE_DB_PATH` | SQLite database (serve mode) | `~/.owl/migrate/owl-migrate.db` |
| `OWL_MIGRATE_LOG_DIR` | Log directory | `~/.owl/migrate/logs` |

Project-level outputs (DDL, SELECT, INSERT, data export, checkpoints) remain
CWD-relative under `./output/` and are controlled by CLI flags (`-o`, `--temp-dir`).

## Full Config Structure

```yaml
general:
  log_level: debug                          # debug | info | warn | error (default: info)
  log_file: /var/log/owl-migrate.log        # Log output file (optional)
  log_format: text                          # text | json

metadata:
  type: database                            # "csv" | "xlsx" | "database"
  csv:
    path: ./metadata/                       # required when type=csv
    delimiter: ","                          # CSV field delimiter (default: ",")
    encoding: "utf-8"                       # CSV file encoding
    has_header: true                        # CSV has header row (default: true)
    null_marker: "\\N"                      # NULL representation in CSV (default: "\N")
    column_name_matching: "case_insensitive" # Column name matching mode
  xlsx:
    path: ./metadata/schema.xlsx             # required when type=xlsx
    data_output_dir: ./output/data/          # @sheet data CSV output

source:
  type: postgres                            # postgres | mysql | oracle | goldendb | oceanbase | panweidb | opengaussdb
  dsn: "host=127.0.0.1 port=5432 dbname=mydb user=u password=p sslmode=disable"
  schema: public
  compat_mode: ""                           # OceanBase 租户兼容模式: "mysql" | "oracle" (留空=连接后自动探测)
  connect_timeout: "30s"                    # Connection/ping timeout (e.g. 10s, 1m)
  query_timeout: ""                         # Overall operation timeout (e.g. 30m, 1h; empty = no limit)
  pool:                                     # Connection pool tuning (optional)
    max_open_conns: 10                      # Max open connections (default: 10)
    max_idle_conns: 5                       # Max idle connections (default: 5)
    conn_max_lifetime: "30m"                # Max connection lifetime (default: 30m)
    conn_max_idle_time: "5m"                # Max idle time before close (default: 5m)

target:
  type: mysql
  dsn: "root:pass@tcp(127.0.0.1:3306)/mydb"
  compat_mode: ""                           # OceanBase 租户兼容模式 (同 source)
  pool:                                     # Same pool options available for target
    max_open_conns: 10

ddl:
  target_dialect: mysql                     # Target DDL dialect (required)
  source_dialect: ""                        # Source dialect for cross-dialect type conversion (CSV/xlsx 元数据时必填)
  output_dir: ./output/ddl/                 # Output directory for DDL files
  include_if_not_exists: true               # Add IF NOT EXISTS
  include_comments: true                    # Include column/table comments
  include_drop: false                       # Generate DROP statements
  split_by_object: true                     # One file per object
  schema_mapping:                           # Map source schema to target schema
    public: myapp
    scott: SCOTT
  table_filter:
    include: ["*"]                          # Tables to include ("*" = all)
    exclude:
      glob: ["*_LOG", "TMP_*"]              # Glob pattern exclusion
      regex: ['^BIN\$']                     # Regex exclusion (e.g., Oracle recycle bin)
      schemas: ["SYS", "SYSTEM"]            # Schema exclusion
      tables: ["SCOTT.TEMP_DATA"]           # Exact table exclusion
  type_overrides: {}                        # Override specific type mappings
  identity_to_serial: false                 # Convert identity columns to SERIAL (PG)
  add_rowid_column: false                   # Add a ROWID column (Oracle targets)
  empty_string_to_null: false               # Convert '' to NULL (Oracle compatibility)
  boolean_mapping: {}                       # Custom boolean value mapping
  no_quote_identifiers: false               # Output bare identifiers without quoting (compatibility)
  partition:
    migrate: false                          # Include partition DDL

select_gen:
  output_dir: ./output/select/              # Output directory for SELECT files
  batch:
    method: cursor                          # pagination method: cursor/offset
    page_size: 5000                         # rows per batch
  include_row_number: false                 # Add ROW_NUMBER() column
  add_export_columns: false                 # Add export helper columns

export:
  output_dir: ./output/data/                # Output directory for exported data files
  format: csv                               # Output format: csv (default), sql, xlsx
  csv:
    delimiter: ","
    quote_char: "\""
    escape_char: ""                         # Escape character
    encoding: "utf-8"                       # CSV file encoding
    header: true
    null_representation: "\\N"
    line_terminator: "\n"
    null_overrides: {}                      # Per-column null value overrides
    empty_string_to_null: false             # Treat empty string as null
  batch:
    method: cursor                          # pagination: cursor/offset
    page_size: 5000
  parallel:
    enabled: true
    max_workers: 4
  tables:
    include: ["*"]                          # table filter list, "*" means all

import:
  source_dir: ./output/data/                # Directory containing CSV data files
  format: csv                               # Input format
  csv:
    delimiter: ","
    encoding: "utf-8"                       # CSV file encoding
    has_header: true
    null_marker: "\\N"
    null_identifiers:                       # Additional null recognition rules
      strings: []                           # Strings treated as null
      case_sensitive: false                 # Case-sensitive comparison
      regex: ""
    null_semantics:                         # Database-specific null semantics
      oracle_empty_string_is_null: false
      numeric_zero_not_null: false
  target:
    truncate_before: true                   # TRUNCATE table before import
    disable_constraints: false              # Disable FK constraints during import
    disable_triggers: false                 # Disable triggers during import
    drop_indexes: false                     # Drop and recreate indexes
  batch:
    commit_interval: 1000                   # rows per transaction
    error_policy: skip_row                  # stop | skip_row | log_only
    max_errors_before_stop: 0               # 0 = unlimited
    use_copy: false                         # PG 族目标启用 COPY 快速通道 (失败自动回退批量 INSERT)
  parallel:
    enabled: true
    max_workers: 4
    respect_foreign_keys: false
  data_transforms:
    datetime_format: "yyyyMMddHHmmss"       # auto-convert compact datetime
    datetime_format_fallback: []            # Additional date format patterns
    datetime_truncate_to_target: false      # Truncate datetime to target precision
    trim_strings: true
    null_if: ["NULL", "null", "\\N"]
    source_encoding: ""                     # Source CSV encoding ("" = UTF-8, supports GBK, LATIN1, etc.)

extensions: {}                              # Custom extension configuration (reserved)
```

## Metadata Types

### `type: csv`

Load table/column definitions from CSV files. Required files in the metadata directory:

- `tables.csv` — table definitions (required)
- `columns.csv` — column definitions (required)
- `primary_keys.csv` — primary key constraints (optional)
- `indexes.csv` — index definitions (optional)
- `foreign_keys.csv` — FK definitions (optional)
- `sequences.csv` — sequence definitions (optional)
- `triggers.csv` — trigger definitions (optional)
- `functions.csv` — stored functions/procedures (optional)
- `views.csv` — view definitions (optional)
- `mviews.csv` — materialized view definitions (optional)
- `synonyms.csv` — synonym definitions (optional, Oracle)

See [CSV Metadata Format](csv-format.md) for detailed column specifications.

### `type: xlsx`

Load metadata from a single Excel (.xlsx) file. Sheets are parsed as follows:

- **Metadata sheets** (`tables`, `columns`, `primary_keys`, `indexes`, `foreign_keys`, `views`, `sequences`, `triggers`, `functions`, `synonyms`) — define the database schema, same format as CSV files
- **Data sheets** (`@TableName`) — provide data for a specific table; first row = column headers, remaining rows = data; the `@` prefix distinguishes data sheets from metadata sheets
- Cell types are converted to CSV values automatically

A `tables` sheet is **required**.

### `type: database`

Connect to the source database specified in `source.*` configuration to extract schema metadata via `information_schema` (PG/MySQL) or `ALL_*` dictionary views (Oracle).

Requires:
- `source.type` — database type
- `source.dsn` — connection string
- `source.schema` — schema name to extract

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

### GoldenDB / OceanBase / PanWeiDB / OpenGaussDB

Use the same DSN format as the underlying dialect (MySQL or PostgreSQL). Set `source.type` to `goldendb`, `goldendb-mysql`, `oceanbase-mysql`, `oceanbase-oracle`, `panweidb`, `opengaussdb`, etc.

**OceanBase 特别说明**：Oracle 租户的驱动路径由 DSN 前缀决定——
`oracle://...` 走 go-ora 的 TNS 协议；其余（如 `obmysql://...` 或 MySQL 风格 DSN）
走 `obconnector-go` 的 MySQL 线协议。`source.compat_mode` / `target.compat_mode`
声明租户兼容模式（`mysql` 或 `oracle`），留空时连接后自动探测
（`SHOW VARIABLES LIKE 'ob_compatibility_mode'`），配置与实际不符会直接报错。

## Table Filtering

The `ddl.table_filter` and `export.tables` sections support multi-level filtering:

```yaml
ddl:
  table_filter:
    include: ["*"]                # Include all (default), or ["SCOTT.*"], or ["SCOTT.EMP"]
    exclude:
      glob: ["*_LOG", "TMP_*"]   # Glob pattern on table name
      regex: ['^BIN\$']          # Regex pattern (e.g., Oracle recycle bin)
      schemas: ["SYS", "SYSTEM"] # Exclude entire schemas
      tables: ["SCOTT.TEMP"]     # Exact schema.table exclusion
```

Priority: includes → glob exclude → regex exclude → schema exclude → table exclude.

## Error Policies

```yaml
import:
  batch:
    error_policy: skip_row  # stop | skip_row | log_only
```

| Policy | Behavior |
|---|---|
| `stop` | Abort the table import on first error |
| `skip_row` | Skip the row, log warning, continue (respects `max_errors_before_stop`) |
| `log_only` | Log and continue inserting (may re-fail) |

## Data Transforms

The `import.data_transforms` section controls per-value transformations during import:

| Setting | Purpose |
|---|---|
| `datetime_format` | Auto-convert compact datetime (14 digits → `YYYY-MM-DD HH24:MI:SS`) |
| `trim_strings` | Trim leading/trailing whitespace from string values |
| `null_if` | String values to treat as SQL NULL |
| `source_encoding` | Decode CSV from source encoding to UTF-8 (GBK, LATIN1, ISO-8859-*, Windows-1252) |

## Extensions

The `extensions` map is a catch-all for custom or future configuration:

```yaml
extensions:
  my_plugin:
    option1: value1
```

This section is not validated by the core config loader — it's available for custom tooling or future plugin support.

## Config Validation

The config loader validates:

1. `metadata.type` must be `csv`, `xlsx`, or `database`
2. When `metadata.type` is `database`, `source.type` and `source.dsn` are required
3. `ddl.target_dialect` must be a valid dialect name
4. `ddl.source_dialect` (if set) must be a valid dialect name
5. `import.batch.error_policy` must be `stop`, `skip_row`, or `log_only`
6. `source.compat_mode` / `target.compat_mode` (if set) must be `mysql` or `oracle`

### Valid Dialects

```
oracle, postgres, mysql,
goldendb, goldendb-mysql, goldendb-oracle,
oceanbase, oceanbase-mysql, oceanbase-oracle,
panweidb, opengaussdb
```

### Valid Metadata Types

```
csv, xlsx, database
```
