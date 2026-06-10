# Migration Test Report

## Environment

| Item | Details |
|------|---------|
| Test Date | 2026-06-10 |
| Source PG | PostgreSQL (Docker: gvenzl/postgres) |
| Source MySQL | MySQL 8+ (Docker) |
| Source Oracle | Oracle XE 21c (Docker: gvenzl/oracle-xe:21-slim-faststart) |
| Test Data | SCOTT schema: DEPT(4), EMP(14), BONUS(0) = 18 rows total |

## Test Results Summary

| # | Source → Target | Tables | Rows | Status |
|---|----------------|--------|------|--------|
| 1 | PostgreSQL → MySQL | bonus, dept, emp | 0 + 4 + 14 = 18 | ✅ SUCCESS |
| 2 | Oracle → MySQL | dept, emp | 4 + 14 = 18 | ✅ SUCCESS |
| 3 | PostgreSQL → Oracle | bonus, dept, emp | 0 + 4 + 14 = 18 | ✅ SUCCESS |
| 4 | MySQL → PostgreSQL | dept, emp | 4 + 14 = 18 | ✅ SUCCESS |
| 5 | Oracle → PostgreSQL | dept, emp | 4 + 14 = 18 | ✅ SUCCESS |

**Total: 5/5 paths passing, 90 rows migrated (cumulative, 18 unique per path) with 0 errors.**

## Issues Fixed

### Importer — MySQL compatibility
- Added `TargetDBType` config to `importer.Config`
- MySQL uses backtick quoting and `?` placeholders (vs `"` double quotes + `$N` for PG)
- Oracle uses `"` double quotes and `:N` placeholders (`:1, :2, ...`)
- File: `internal/transfer/importer/importer.go`

### Importer — Oracle date format
- Oracle rejects string literals like `1980-12-17 00:00:00` as DATE values
- Fix: Set `ALTER SESSION SET NLS_DATE_FORMAT = 'YYYY-MM-DD HH24:MI:SS'` at start of each import
- File: `internal/transfer/importer/importer.go`

### DDL generation — Oracle support
- Oracle doesn't support `IF NOT EXISTS` in CREATE TABLE
- Oracle type map: NUMBER(10/19/5) for int types, VARCHAR2 for varchar, CLOB for text, BINARY_FLOAT/BINARY_DOUBLE for floats, XMLTYPE for XML, TIMESTAMP WITH TIME ZONE for timestamptz
- NUMBER/DECIMAL precision/scale: uses `NUMBER(p,s)` for Oracle vs `DECIMAL(p,s)` for others
- File: `internal/cmd/import.go`

### DDL generation — MySQL specifics
- MySQL requires explicit VARCHAR length (default VARCHAR(255) when unspecified)
- BOOLEAN → TINYINT(1), TEXT → LONGTEXT, BLOB → LONGBLOB, JSON/JSONB → JSON
- File: `internal/cmd/import.go`

### DDL generation — PG target
- NUMBER → NUMERIC, VARCHAR2 → VARCHAR, BOOLEAN → BOOLEAN, CLOB → TEXT, BLOB → BYTEA, JSON → JSONB
- File: `internal/cmd/import.go`

### MySQL extractor — QueryTables
- Removed `tablespace_name` (not present in MySQL 8 information_schema.tables)
- File: `internal/metadata/extractor/mysql.go`

### MySQL extractor — QueryViews
- Fixed ambiguous `table_name` column by prefixing with `v.` table alias
- Fixed ambiguous `is_updatable`/`check_option` with `v.` prefix
- File: `internal/metadata/extractor/mysql.go`

### MySQL extractor — QueryTriggers
- Removed `trigger_comment` column (not present in MySQL 8 information_schema.triggers)
- File: `internal/metadata/extractor/mysql.go`

### Oracle extractor — QueryColumns
- Removed `HIDDEN_COLUMN`, `VIRTUAL_COLUMN`, `DATA_TYPE_OWNER` (not in Oracle 21c all_tab_columns)
- Changed `COALESCE(data_default, '')` to plain `data_default` (LONG type incompatible with COALESCE)
- Used `sql.NullString` for nullable fields (data_default, comments, char_used, charset, collation)
- Use `NVL(comments, '')` instead of `COALESCE` for LONG compatibility
- File: `internal/metadata/extractor/oracle.go`

### Oracle extractor — QueryViews
- Fixed ambiguous `owner`/`comments` columns by adding `v.` / `t.` table aliases
- File: `internal/metadata/extractor/oracle.go`

### Oracle extractor — QuerySequences
- Removed `START_VALUE` (not in all_sequences)
- Changed `LAST_VALUE` to `LAST_NUMBER` (column name difference)
- File: `internal/metadata/extractor/oracle.go`

## Test Config Files

- `testoutput/pg_to_mysql.yaml`
- `testoutput/oracle_to_mysql.yaml`
- `testoutput/pg_to_oracle.yaml`
- `testoutput/mysql_to_pg.yaml`
- `testoutput/oracle_to_pg.yaml`

## OpenGaussDB Compatibility Tests

Actual OpenGaussDB Docker startup was blocked in this local environment. The `enmotech/opengauss:latest` container repeatedly failed during MOT engine initialization with errors including:

- `Failed to allocate highest thread identifier on node 0`
- `PANIC: Failed to allocate loader thread identifier`
- `FATAL: Failed to initialize MOT engine`

Because the container never reached a stable listening state, these tests used the existing PostgreSQL 15 container as an OpenGauss-compatible source while setting `source.type: opengaussdb`. This verifies the application code paths for:

- `openDB("opengaussdb", dsn)` → PostgreSQL driver
- `normalizeDBType("opengaussdb")` → PostgreSQL metadata extractor
- exporter source branch for `opengaussdb`
- DDL generation/importer behavior for PostgreSQL, MySQL, and Oracle targets

### Complex Dataset

Schema: `og_complex_run`

| Table | Rows | Coverage |
|-------|------|----------|
| `departments` | 4 | numeric, boolean, varchar, timestamp, unicode |
| `employees` | 4 | nullable fields, date/timestamp, text with comma/quote/newline, jsonb, bytea |
| `project_assignments` | 4 | numeric scale, nullable timestamp, multilingual text |
| `audit_events` | 5205 | pagination over 5000 rows, jsonb, timestamp |

### OpenGauss-Compatible Migration Results

| # | Source → Target | Tables | Rows | Status |
|---|----------------|--------|------|--------|
| 6 | OpenGaussDB-compatible → PostgreSQL | audit_events, departments, employees, project_assignments | 5205 + 4 + 4 + 4 = 5217 | ✅ SUCCESS |
| 7 | OpenGaussDB-compatible → MySQL | audit_events, departments, employees, project_assignments | 5205 + 4 + 4 + 4 = 5217 | ✅ SUCCESS |
| 8 | OpenGaussDB-compatible → Oracle | audit_events_o3, departments_o3, employees_o3, project_assignments_o3 | 5205 + 4 + 4 + 4 = 5217 | ✅ SUCCESS |

**Updated total: 8/8 tested paths passing, 15,741 additional OpenGauss-compatible rows migrated, 0 final errors.**

### Issues Found and Fixed During OpenGauss-Compatible Testing

- Exporter PostgreSQL-compatible cursor pagination now uses `$1`, `$2`, ... placeholders instead of MySQL-style `?`.
- Compound Oracle-compatible target names such as `goldendb-oracle` and `oceanbase-oracle` no longer match MySQL-compatible prefix checks in `importer.go`, `import.go`, and `exporter.go`.
- PostgreSQL metadata types such as `timestamp without time zone` are normalized before target DDL generation.
- MySQL and Oracle imports convert source `BOOLEAN` values (`true`/`false`) into numeric `1`/`0` for `TINYINT(1)` / `NUMBER(1)` targets.
- Oracle imports set `NLS_TIMESTAMP_FORMAT` and `NLS_TIMESTAMP_TZ_FORMAT`, in addition to `NLS_DATE_FORMAT`.
- Exported binary columns are hex-encoded only when the source column is truly binary (`BYTEA`, `BLOB`, `RAW`, `BINARY`, `VARBINARY`), avoiding accidental hex encoding of PostgreSQL `NUMERIC`/`JSONB` values returned as `[]byte` by the driver.
- Binary target columns are hex-decoded during import.
- Exporter `fetchBatch` resolves PK column casing against actual DB column names via `resolvePK()`, preventing `ORDER BY` failures when metadata returns lowercased PK names but the table has been created with quoted uppercase identifiers (e.g. after Oracle→PG migration).

### Remaining Risks Without Real OpenGaussDB Runtime

- OpenGauss-specific catalog differences beyond PostgreSQL-compatible `information_schema` were not validated against a live OpenGauss instance.
- OpenGauss-specific data types, sequences, partitions, indexes, stored procedures, and compatibility flags may require additional extraction rules.
- Driver-level behavior may differ from PostgreSQL 15 for timestamps, booleans, bytea encoding, large objects, and identifier case folding.
- Query pagination and transaction semantics were validated through PostgreSQL protocol behavior, not a real OpenGauss backend.
- OceanBase routes remain untested because no OceanBase environment is currently available.
