# Dialect & Type Mapping

## Supported Dialects

go-owl-migrate supports a dialect system built from composable interfaces. Each dialect is registered by name and provides type mapping, DDL generation, DML syntax, identifier quoting, and feature flags.

### Core Dialects

| Dialect | Identifier | Quoting | DDL Driver | Extractor |
|---|---|---|---|---|
| **Oracle** | `oracle` | `"name"` (double-quote) | go-ora | `ALL_*` dictionary views |
| **PostgreSQL** | `postgres` | `"name"` (double-quote) | lib/pq | information_schema |
| **MySQL** | `mysql` | `` `name` `` (backtick) | go-sql-driver | information_schema |

### Compound Dialects (inherit from core dialects)

| Dialect | Identifier | Inherits From | Differences from Parent |
|---|---|---|---|
| **GoldenDB (MySQL)** | `goldendb`, `goldendb-mysql` | MySQL | Same as MySQL |
| **GoldenDB (Oracle)** | `goldendb-oracle` | Oracle | Same as Oracle |
| **OceanBase (MySQL)** | `oceanbase`, `oceanbase-mysql` | MySQL | TRUNCATE is transactional, no FULLTEXT indexes, no MyISAM engine, supports SEQUENCE |
| **OceanBase (Oracle)** | `oceanbase-oracle` | Oracle | TRUNCATE is transactional, no BFILE support, partition syntax differences, no Bitmap indexes. Driver is auto-selected: `oracle://` DSN → go-ora TNS; otherwise → obconnector-go MySQL wire (`preset=oboracle`). Compat mode is probed at connect and must match `compat_mode` config. |
| **PanWeiDB (PG)** | `panweidb` | PostgreSQL | Uses openGauss driver (lib/pq fork, SHA256/SM3 auth) over PG wire protocol, same DML syntax |
| **PanWeiDB (MySQL B)** | `panweidb-mysql` | MySQL | Dolphin plugin, backtick quoting, ENGINE= ignored, uses openGauss driver/PG port |
| **PanWeiDB (Oracle A)** | `panweidb-oracle` | Oracle | Oracle DDL/DML syntax, TRUNCATE transactional, uses openGauss driver/PG port |
| **OpenGaussDB** | `opengaussdb` | PostgreSQL | Uses openGauss driver (lib/pq fork, SHA256/SM3 auth) over PG wire protocol, same DML syntax |
| **OpenGaussDB (Oracle A)** | `opengaussdb-oracle` | Oracle | Oracle DDL/DML/quoting, TRUNCATE transactional (PG feature), uses openGauss driver/PG port (`$N` placeholders) |
| **OpenGaussDB (MySQL B)** | `opengaussdb-mysql` | MySQL | Dolphin plugin, backtick quoting, ENGINE= ignored, uses openGauss driver/PG port (`$N` placeholders) |

### Embedded Dialects

Serverless, in-process databases:

| Dialect | Identifier | Quoting | DDL Driver | Extractor |
|---|---|---|---|---|
| **SQLite3** | `sqlite3` | `"name"` (double-quote) | mattn/go-sqlite3 | sqlite_master + PRAGMA |
| **DuckDB** | `duckdb` | `"name"` (double-quote) | duckdb/duckdb-go | information_schema |

> **Build tags**: SQLite3 and DuckDB support is compile-time optional. They are gated behind the `sqlite3` and `duckdb` build tags respectively and are **not** included in the default build. Build with `go build -tags "sqlite3 duckdb" ...` (or `make test/full`) to enable them.

### Metadata Extraction Steps

When extracting metadata from a live database (`metadata.type: database`), the pipeline queries:

1. **Tables** — via `information_schema.tables` (PG/MySQL) or `all_tables` (Oracle); table comments via `obj_description`/`all_tab_comments`; temporary-table / partitioned-parent flags where available
2. **Columns** — via `information_schema.columns` or `all_tab_columns`; identity start/increment via `pg_get_serial_sequence`/`all_tab_identity_cols` (native Oracle only — the OceanBase Oracle tenant has no `all_tab_identity_cols` view)
3. **Primary Keys** — via `information_schema.table_constraints` or `all_constraints`
4. **Indexes** — via `information_schema.statistics` / `pg_index` or `all_indexes`
5. **Foreign Keys** — via `information_schema.key_column_usage` or `all_cons_columns`
6. **Views** — via `information_schema.views` or `all_views`
7. **Sequences** — via `pg_sequences` or `all_sequences` (START WITH = `last_number`, avoids post-migration PK collisions)
8. **Triggers** — via `information_schema.triggers` / `pg_trigger` or `all_triggers`
9. **Functions/Procedures** — via `pg_get_functiondef` (PG10+ `prokind` fallback) or `DBMS_METADATA.GET_DDL` (Oracle, `all_source` fallback)
10. **Packages/Bodies** — via `DBMS_METADATA` (Oracle only)
11. **Materialized Views** — via `pg_matviews` (PG) or `all_mviews` (Oracle)
12. **Partitions** — via `pg_get_partkeydef` (PG) / `all_part_tables` (Oracle) / `information_schema.partitions` (MySQL); reconstructs `PARTITION BY` text
13. **Synonyms** — via `all_synonyms` (Oracle/OceanBase-Oracle only); PG/MySQL return empty

> The 13-step list is exhaustive; MySQL covers steps 1–8 plus partitions, PG 1–11 plus partitions,
> Oracle 1–13. Steps 9/10/11 are Oracle-only queries where the PG/MySQL side is empty.

### Querying a Dialect

Dialects are retrieved from a global registry by name:

```go
d, err := registry.Get("oceanbase-mysql")
// d.Name() → "oceanbase-mysql"
// d.Quote("table") → "`table`" (inherits MySQL backtick quoting)
```

## Dialect Architecture

Each dialect is a composed struct of five interfaces:

```
Dialect {
  TypeMapper        — Maps raw DB types ↔ logical types
  IdentifierQuoter  — Quotes/unquotes identifiers per DB rules
  Features          — Describes DB capabilities (transactional DDL, IF NOT EXISTS, max identifier length, etc.)
  DDLBuilder        — Generates DDL (CREATE TABLE/INDEX/VIEW/SEQUENCE/TRIGGER/etc.)
  DMLHelper         — Generates pagination clauses, value formatting
}
```

### Identifier Quoting

| Dialect | Style | Example |
|---|---|---|
| MySQL, GoldenDB, OceanBase-MySQL, **PanWeiDB-MySQL** | Backtick | `` `table_name` `` |
| Oracle, PostgreSQL, GoldenDB-Oracle, OceanBase-Oracle, **PanWeiDB-Oracle** | Double-quote (UPPER) | `"TABLE_NAME"` |
| PanWeiDB (PG) | Double-quote (lower) | `"table_name"` |

### Feature Flags

| Database | Transactional DDL | IF NOT EXISTS | Max ID Length | Transactional TRUNCATE |
|---|---|---|---|---|
| Oracle | ✓ | ✗ | 128 | ✗ |
| PostgreSQL | ✓ | ✓ | 63 | ✓ |
| MySQL | ✗ | ✓ | 64 | ✗ |
| GoldenDB (MySQL) | ✗ | ✓ | 64 | ✗ |
| GoldenDB (Oracle) | ✗ | ✗ | — | ✗ |
| OceanBase (MySQL) | ✗ | ✓ | — | ✓ |
| OceanBase (Oracle) | ✗ | ✗ | — | ✓ |
| **PanWeiDB (PG)** | **✓** | **✓** | **63** | **✓** |
| **PanWeiDB (MySQL B)** | **✓** | **✓** | **63** | **✓** |
| **PanWeiDB (Oracle A)** | **✓** | **✓** | **63** | **✓** |

## Type Mapping

### Logical Type System

All database-specific types are normalized to a database-independent `LogicalType`:

| LogicalBase | Description | Example DB Types Mapped |
|---|---|---|
| `LBVarchar` | Variable string | VARCHAR, VARCHAR2, NVARCHAR2 |
| `LBChar` | Fixed string | CHAR, NCHAR |
| `LBInt` | Integer (32-bit) | INT, INTEGER |
| `LBBigInt` | Large integer (64-bit) | BIGINT, NUMBER(10+) |
| `LBSmallInt` | Small integer (16-bit) | SMALLINT, NUMBER(≤4) |
| `LBNumeric` | Exact numeric | DECIMAL, NUMERIC, NUMBER |
| `LBFloat` | Floating point (32-bit) | FLOAT, REAL |
| `LBDouble` | Double precision | DOUBLE, BINARY_DOUBLE |
| `LBDate` | Date only | DATE |
| `LBTime` | Time only | TIME |
| `LBDatetime` | Date + time | DATETIME |
| `LBTimestamp` | Timestamp | TIMESTAMP |
| `LBTimestampTZ` | Timestamp with TZ | TIMESTAMP WITH TIME ZONE |
| `LBInterval` | Interval | INTERVAL |
| `LBBoolean` | Boolean | BOOLEAN, TINYINT(1), NUMBER(1) |
| `LBCLOB` | Character LOB | CLOB, TEXT, LONGTEXT |
| `LBBLOB` | Binary LOB | BLOB, BYTEA, LONGBLOB |
| `LBJSON` | JSON | JSON, JSONB |
| `LBXML` | XML | XML, XMLTYPE |
| `LBEnum` | Enumeration | ENUM |
| `LBBinary` | Fixed binary | BINARY |
| `LBVarBinary` | Variable binary | VARBINARY, RAW, BFILE |

### Oracle Type Mapping (logical → concrete)

| Logical Type | Oracle Target Type |
|---|---|
| LBVarchar | `VARCHAR2(%l)` |
| LBChar | `CHAR(%l)` |
| LBInt | `NUMBER(%p,0)` |
| LBBigInt | `NUMBER(%p,0)` |
| LBSmallInt | `NUMBER(%p,0)` |
| LBNumeric | `NUMBER(%p,%s)` |
| LBFloat | `BINARY_FLOAT` |
| LBDouble | `BINARY_DOUBLE` |
| LBCLOB | `CLOB` |
| LBBLOB | `BLOB` |
| LBVarBinary | `RAW(%l)` |
| LBDate | `DATE` |
| LBTimestamp | `TIMESTAMP` |
| LBTimestampTZ | `TIMESTAMP WITH TIME ZONE` |
| LBJSON | `CLOB` |
| LBXML | `XMLTYPE` |
| LBBoolean | `NUMBER(1)` |

### PostgreSQL Type Mapping (logical → concrete)

| Logical Type | PostgreSQL Target Type |
|---|---|
| LBVarchar | `VARCHAR(%l)` |
| LBChar | `CHAR(%l)` |
| LBInt | `INTEGER` |
| LBBigInt | `BIGINT` |
| LBSmallInt | `SMALLINT` |
| LBNumeric | `NUMERIC(%p,%s)` |
| LBFloat | `REAL` |
| LBDouble | `DOUBLE PRECISION` |
| LBCLOB | `TEXT` |
| LBBLOB | `BYTEA` |
| LBVarBinary | `BYTEA` |
| LBDate | `DATE` |
| LBTimestamp | `TIMESTAMP` |
| LBTimestampTZ | `TIMESTAMPTZ` |
| LBJSON | `JSONB` |
| LBBoolean | `BOOLEAN` |

### MySQL Type Mapping (logical → concrete)

| Logical Type | MySQL Target Type |
|---|---|
| LBVarchar | `VARCHAR(%l)` |
| LBChar | `CHAR(%l)` |
| LBInt | `INT` |
| LBBigInt | `BIGINT` |
| LBSmallInt | `SMALLINT` |
| LBNumeric | `DECIMAL(%p,%s)` |
| LBFloat | `FLOAT` |
| LBDouble | `DOUBLE` |
| LBCLOB | `LONGTEXT` |
| LBBLOB | `LONGBLOB` |
| LBDate | `DATE` |
| LBDatetime | `DATETIME` |
| LBTimestamp | `TIMESTAMP` |
| LBJSON | `JSON` |
| LBBoolean | `TINYINT(1)` |

## External Type Mapping Files

In addition to built-in dialect type mapping, you can use external YAML mapping files for custom transformations.

### Format

```yaml
name: "Oracle to PostgreSQL"
version: "1.0"
source_db: oracle
target_db: postgresql

# Simple 1:1 type mappings
exact_mappings:
  "VARCHAR2": "VARCHAR"
  "CHAR": "CHAR"
  "NUMBER": "NUMERIC"
  "DATE": "TIMESTAMP"
  "CLOB": "TEXT"
  "BLOB": "BYTEA"

# Conditional mappings based on precision/scale/length
parameterized:
  "NUMBER":
    - condition: { scale: 0, max_precision: 4 }
      target: "SMALLINT"
    - condition: { scale: 0, max_precision: 9 }
      target: "INTEGER"
    - condition: { scale: 0, max_precision: 18 }
      target: "BIGINT"
    - condition: { scale_gt: 0 }
      target: "NUMERIC(%p,%s)"
    - condition: { default: true }
      target: "NUMERIC"

# Column-name-based overrides
semantic_overrides:
  - pattern: ".*_FLAG$"
    condition: { type: "CHAR", length: 1 }
    target_type: "BOOLEAN"
  - pattern: ".*_TIME$|.*_DATE$"
    condition: { type: "DATE" }
    target_type: "TIMESTAMP"
```

### Mapping Priority Chain

When mapping a type, the system checks in this order:

1. **Parameterized rules** — Most specific; match on precision, scale, length thresholds
2. **Exact mappings** — Catch-all for a source type name
3. **Semantic overrides** — Matched by column name regex pattern
4. **Type-only fallback** — Returns the source type name unchanged

### Placeholder Variables

In target type strings:

| Placeholder | Replaced With |
|---|---|
| `%l` | Length (e.g., `VARCHAR(%l)` → `VARCHAR(100)`) |
| `%p` | Precision (e.g., `NUMERIC(%p,%s)` → `NUMERIC(10,2)`) |
| `%s` | Scale |

## DDL Generation Options

```yaml
ddl:
  target_dialect: postgres                # Target dialect for DDL
  source_dialect: oracle                  # Source dialect (CSV/xlsx 元数据时用于跨方言类型转换)
  include_if_not_exists: true             # Add IF NOT EXISTS (not available for Oracle)
  include_comments: true                  # Column/table comments as COMMENT ON
  include_drop: false                     # Generate DROP statements
  split_by_object: true                   # One file per object
  schema_mapping:                         # Map source schemas to target
    public: myapp
    scott: SCOTT
  type_overrides: {}                      # Override specific type mappings
  identity_to_serial: false               # Convert identity columns to SERIAL (PG)
  add_rowid_column: false                 # Add a ROWID column (Oracle targets)
  empty_string_to_null: false             # Convert '' to NULL (Oracle compatibility)
  boolean_mapping: {}                     # Custom boolean value mapping
  no_quote_identifiers: false              # Output bare identifiers without quoting (compatibility)
  partition:
    migrate: false                        # Include partition DDL
  table_filter:
    include: ["*"]                        # Tables to include
    exclude:
      glob: ["*_LOG", "TMP_*"]           # Glob patterns
      regex: ['^BIN\$']                   # Regex patterns
      schemas: ["SYS", "SYSTEM"]          # Schema names
      tables: ["SCOTT.TEMP_DATA"]         # Exact table names
```

### Table Filtering

The `table_filter` system supports include lists and multiple exclusion mechanisms:

| Mechanism | Syntax | Description |
|---|---|---|
| Include wildcard | `["*"]` | All tables |
| Include specific | `["SCOTT.EMP", "HR.DEPT"]` | Specific schema.table combos |
| Include schema glob | `["SCOTT.*"]` | All tables in a schema |
| Glob exclude | `["*_LOG", "TMP_*"]` | Table name glob patterns |
| Regex exclude | `['^BIN\$']` | Regex pattern (Oracle recycle bin) |
| Schema exclude | `["SYS", "SYSTEM"]` | Exclude entire schemas |
| Table exclude | `["SCOTT.TEMP_DATA"]` | Exact table matches |

Priority: includes → glob exclude → regex exclude → schema exclude → table exclude.
