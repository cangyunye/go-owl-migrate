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
