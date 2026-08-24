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
    respect_foreign_keys: false             # true = 按外键依赖排序（父表先插入，自动串行）
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

## Connection Strings (DSN)

`source.type` / `target.type` / `ddl.target_dialect` 支持的方言及对应连接串格式如下，
可直接复制替换占位符后使用：

| `type` 取值 | 连接串示例（可直接抄写） |
|---|---|
| `oracle` | `oracle://user:pass@host:1521/service_name` |
| `postgres` / `postgresql` | `host=127.0.0.1 port=5432 user=postgres password=pass dbname=mydb sslmode=disable` |
| `mysql` | `user:pass@tcp(host:3306)/dbname?charset=utf8mb4` |
| `sqlite3` | `/path/to/database.db` |
| `duckdb` | `/path/to/database.db` |
| `goldendb` / `goldendb-mysql` | `user:pass@tcp(host:3306)/dbname?charset=utf8mb4`（MySQL 兼容模式） |
| `goldendb-oracle` | `oracle://user:pass@host:1521/service_name`（Oracle 兼容模式） |
| `oceanbase` / `oceanbase-mysql` | `user:pass@tcp(host:2881)/dbname`（MySQL 兼容模式；2881 直连 OBServer，2883 走 OBProxy） |
| `oceanbase-oracle` | `oceanbase-oracle://user:pass@host:2881/db`（MySQL 线协议直连）或 `oracle://user:pass@host:2883/service_name`（OBProxy TNS） |
| `panweidb` / `panweidb-mysql` / `panweidb-oracle` | `host=127.0.0.1 port=5432 user=postgres password=pass dbname=mydb sslmode=disable`（始终走 PG 协议） |
| `opengaussdb` | `host=127.0.0.1 port=5432 user=gaussdb password=pass dbname=postgres sslmode=disable`（默认用户 `gaussdb`；testdata/db 测试环境端口映射为 **5433**） |

### 各方言要点

- **Oracle 系**（`oracle`、`goldendb-oracle`）：go-ora 驱动，URL 格式 `oracle://user:pass@host:port/service_name`。
- **MySQL 系**（`mysql`、`goldendb`、`goldendb-mysql`、`oceanbase`、`oceanbase-mysql`）：go-sql-driver/mysql 格式 `user:pass@tcp(host:port)/dbname`。
- **PG 系**（`postgres`、`opengaussdb`、`panweidb` 全系）：`host=... port=... user=... password=... dbname=... sslmode=...` 键值对格式。**注意 PanWeiDB 即使声明 `panweidb-mysql` / `panweidb-oracle`，通信协议仍是 PostgreSQL**。
- **OceanBase Oracle 租户**：驱动路径由 DSN 前缀决定——`oracle://...` 走 go-ora 的 TNS 协议（连 OBProxy Oracle 监听端口，通常 2883）；其余前缀（如 `oceanbase-oracle://`、`oboracle://` 或 MySQL 风格）走 `obconnector-go` 的 MySQL 线协议（直连 2881）。
  `source.compat_mode` / `target.compat_mode` 声明租户兼容模式（`mysql` 或 `oracle`），留空时连接后自动探测
  （`SHOW VARIABLES LIKE 'ob_compatibility_mode'`），配置与实际不符会直接报错；`type=oceanbase`（MySQL 模式）连到 Oracle 租户也会报错，需改用 `oceanbase-oracle`。

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

## 外键处理（truncate_before / respect_foreign_keys）

导入前清空与插入顺序均基于**目标库中实际存在的外键**（实时探查），不依赖元数据里是否定义了外键：

- `target.truncate_before: true`：
  - **PG 族**（postgres / opengaussdb / panweidb）：本批所有表用一条 `TRUNCATE TABLE a, b, ...` 清空，批内外键互引自动成立；
  - **MySQL / Oracle** 及单表回退场景：逐表 TRUNCATE，被外键阻断时自动回退 `DELETE FROM`（只清本批表，不影响批外数据）。
- `parallel.respect_foreign_keys: true`：按目标库外键依赖排序（父表先插入），并自动强制串行导入；
  **目标表带外键时必须开启**，否则并行插入可能违反 FK 顺序。
- 探查不到外键时自动退化为原行为；被迁移集之外的表引用的表无法 TRUNCATE，会告警并改用 `DELETE FROM`。

## Data Transforms

The `import.data_transforms` section controls per-value transformations during import:

| Setting | Purpose |
|---|---|
| `datetime_format` | Auto-convert compact datetime (14 digits → `YYYY-MM-DD HH24:MI:SS`) |
| `trim_strings` | Trim leading/trailing whitespace from string values |
| `null_if` | String values to treat as SQL NULL |
| `source_encoding` | Decode CSV from source encoding to UTF-8 (GBK, LATIN1, ISO-8859-*, Windows-1252) |

## NULL 识别（三处配置的关系）

导入时有三处配置都会把字段识别为 NULL，作用阶段不同、不冲突：

| 配置 | 阶段 | 作用 |
|---|---|---|
| `import.csv.null_marker` | CSV 解析 | **主配置**：字段文本与该标记完全相等 → NULL（默认 `\N`） |
| `import.csv.null_identifiers` | CSV 解析 | 扩展匹配规则：额外字符串列表、大小写敏感开关、正则 |
| `import.data_transforms.null_if` | 值转换 | 常见字面量便捷入口（如 `"NULL"`、`"null"`） |

一般场景只配 `null_marker` 即可；三者的命中结果一致（都写入 SQL NULL）。

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
