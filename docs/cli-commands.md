# CLI Commands

Version: `0.1.0`

## Global Flags

```
-c, --config string   Config file path (default "./migrate.yaml")
    --log-level       Override log level (debug/info/warn/error)
```

## owl-migrate init

Generate a configuration file from command-line parameters. Eliminates the need to manually craft YAML.

```
Usage:
  owl-migrate init [flags]

Flags:
  -s, --source-type string    Source database type (oracle/postgres/mysql/goldendb/oceanbase/panweidb/opengaussdb)
      --source-dsn string     Source database DSN
      --source-schema string  Source database schema/database name
  -t, --target-type string    Target database type
      --target-dsn string     Target database DSN (optional for DDL-only workflows)
      --target-schema string  Target database schema (defaults to source-schema if empty)
  -m, --metadata-type string  Metadata source: csv or database (default "database")
  -o, --output string         Output configuration file path (default "./migrate.yaml")
```

Example:

```bash
owl-migrate init \
  --source-type oracle \
  --source-dsn "oracle://user:pass@host:1521/service" \
  --source-schema SCOTT \
  --target-type postgres \
  --target-dsn "postgres://user:pass@localhost:5432/migrate" \
  --target-schema public \
  -o ./migrate.yaml
```

## owl-migrate validate

Validate metadata from CSV files or a live database. Checks referential integrity — FK references, trigger table references, and missing primary keys.

```
Usage:
  owl-migrate validate [flags] (config passed via -c flag)
```

Output:

```
Validation passed: 4 tables, 2 views loaded
```

Or with issues:

```
Validation found 2 issue(s):
  [ERROR] foreign key FK_DEPT references non-existent table SCOTT.DEPT
  [WARNING] table SCOTT.BONUS has no primary key
```

## owl-migrate export

Unified export command with three subcommands for DDL, data, and INSERT SQL generation.

```
Usage:
  owl-migrate export [command]

Available Commands:
  ddl         Generate DDL from metadata
  data        Export data from source database to CSV/SQL/XLSX files
  insert      Generate INSERT SQL from CSV data files (offline)
```

### owl-migrate export ddl

Generate DDL (CREATE TABLE / INDEX / VIEW / SEQUENCE / TRIGGER / FUNCTION / PACKAGE / etc.)
from metadata for the target dialect.

```
Usage:
  owl-migrate export ddl [flags] -c <config>

Flags:
  -o, --output string              Output directory for DDL files (default "./output/ddl/")
      --no-quote-identifiers       Output bare identifiers without quoting (compatibility)
```

The command generates **per-object** SQL files using the naming convention:

```
{schema}.{object_name}.{type}.sql
```

Generated object types (all three core dialects support Table/Index/View/Trigger/Function;
Oracle additionally supports Sequence/Synonym/MView/Package):


| Type | File pattern | Description | MySQL | Oracle | PG | SQLite3 |
|---|---|---|---|---|---|---|---|
| Table | `scott.emp.table.sql` | CREATE TABLE | ✅ | ✅ | ✅ | ✅ |
| Index | `scott.idx_emp_ename.index.sql` | CREATE INDEX / UNIQUE INDEX / BITMAP | ✅ | ✅ | ✅ | ✅ |
| View | `scott.emp_view.view.sql` | CREATE VIEW | ✅ | ✅ | ✅ | ✅ |
| Sequence | `scott.seq_emp_id.sequence.sql` | CREATE SEQUENCE | — | ✅ | ✅ | — |
| Synonym | `scott.emp_syn.synonym.sql` | CREATE [PUBLIC] SYNONYM | — | ✅ | — | — |
| Materialized View | `scott.emp_mv.mview.sql` | CREATE MATERIALIZED VIEW | — | ✅ | ✅ | — |
| Trigger | `scott.trg_emp_sal.trigger.sql` | CREATE [OR REPLACE] TRIGGER | ✅ | ✅ | ✅ | ✅ |
| Function | `scott.get_emp_count.function.sql` | CREATE [OR REPLACE] FUNCTION | ✅ | ✅ | ✅ | — |
| Package | `scott.pkg_emp.package.sql` | CREATE OR REPLACE PACKAGE | — | ✅ | — | — |
| Package Body | `scott.pkg_emp.package_body.sql` | CREATE OR REPLACE PACKAGE BODY | — | ✅ | — | — |

DDL generation behavior is controlled by `ddl.*` config options — see [Configuration Reference](config.md).

The old `gen-ddl` command is preserved as a hidden alias for backward compatibility.

### owl-migrate export data

Export data from the source database to CSV (default), SQL, or XLSX files.

```
Usage:
  owl-migrate export data [flags] -c <config>

Flags:
  -o, --output string   Output directory for export files (default "./output/data/")
      --no-quote-identifiers       Output bare identifiers without quoting (compatibility)
```

Key features:

- **Multi-format output**: CSV (default), SQL (INSERT statements), XLSX (Excel workbook).
- **Cursor-based pagination**: When primary keys are available, uses keyset pagination (WHERE pk > last_value) for efficient large-table export.
- **Fallback to LIMIT**: Tables without primary keys use LIMIT-only pagination (less efficient but works for any table).
- **Parallel export**: Multiple tables exported concurrently (controlled by `export.parallel.max_workers`).
- **Binary data handling**: BLOB/BYTEA/RAW columns are hex-encoded in CSV.
- **Datetime formatting**: Timestamps are exported in compact `yyyyMMddHHmmss` format.
- **RFC 4180 CSV**: Values containing delimiters, quotes, or newlines are properly quoted.
- **Continue on error**: By default, one failing table doesn't abort the entire export (table-level error isolation).

Output files by format:

| Format | File pattern |
|---|---|
| CSV | `{schema}.{table}.csv` |
| SQL | `{schema}.{table}.insert.sql` |
| XLSX | `{schema}.{table}.xlsx` |

The format is controlled by `export.format` in the config file (`csv`, `sql`, or `xlsx`).

The old `export` command is now `export data`.

### owl-migrate export insert

Generate INSERT SQL statements from CSV data files. **Offline mode** — no database connection required.

```
Usage:
  owl-migrate export insert [flags]

Flags:
  -o, --output string    Output directory for INSERT SQL files (default "./output/insert/")
  -d, --data string      Directory containing CSV data files (default "./output/data/")
      --dialect string   Target dialect: oracle/postgres/mysql (default "postgres")
  -n, --batch-size int   VALUES rows per INSERT statement (default 100)
      --truncate                  Add TRUNCATE TABLE before INSERT
      --no-quote-identifiers       Output bare identifiers without quoting (compatibility)
```

The command reads CSV data files named `{schema}.{table}.csv` and produces dialect-specific INSERT SQL:

- **PostgreSQL/Oracle**: Wraps INSERT batches in `BEGIN`/`COMMIT` transactions; commits every `--batch-size` rows.
- **MySQL**: Uses single INSERT statements without explicit transaction wrappers.
- **Dialect-specific escaping**: Oracle treats empty strings as NULL; MySQL uses backtick quoting.
- **NULL detection**: The null marker (default `\N`) is converted to SQL `NULL`.
- **Numeric detection**: Numeric values are not quoted in the generated SQL.

Example output:

```sql
BEGIN;

INSERT INTO "SCOTT"."EMP" ("EMPNO", "ENAME", "JOB")
VALUES
  (7369, 'SMITH', 'CLERK'),
  (7499, 'ALLEN', 'SALESMAN');

COMMIT;
```

The old `gen-insert` command is preserved as a hidden alias for backward compatibility.

## owl-migrate gen-select

Generate paginated SELECT statements for data export. Supports two pagination methods.

```
Usage:
  owl-migrate gen-select [flags] -c <config>

Flags:
  -o, --output string        Output directory for SELECT files (default "./output/select/")
      --batch-method string        Pagination method: cursor/offset (default "cursor")
  -n, --page-size int              Rows per batch (default 5000)
      --no-quote-identifiers       Output bare identifiers without quoting (compatibility)
```

Generated SQL files contain one SELECT statement per table with:

- **Cursor-based pagination** (default): Uses PK columns in ORDER BY with WHERE pk > last_value. Efficient for large tables.
- **Offset-based pagination**: Uses LIMIT/OFFSET. Simpler but less efficient for deep pages.

Example output (`./output/select/scott.emp.select.sql`):

```sql
SELECT "EMPNO", "ENAME", "JOB", "MGR", "HIREDATE", "SAL", "COMM", "DEPTNO"
FROM "SCOTT"."EMP"
ORDER BY "EMPNO"
LIMIT 5000;
```

## owl-migrate import

Import CSV data files into the target database.

```
Usage:
  owl-migrate import [flags] -c <config>
Flags:
      --no-quote-identifiers       Output bare identifiers without quoting (compatibility)
```

Key features:

- **Automatic table creation**: Creates target tables if they don't exist (with type mapping).
- **Batched transactions**: Configurable `commit_interval` controls rows per transaction.
- **Parallel import**: Multiple tables imported concurrently (controlled by `import.parallel.max_workers`).
- **Data transforms**: DateTime format conversion, string trimming, NULL value normalization.
- **Encoding conversion**: Supports GBK, LATIN1, ISO-8859-*, Windows-1252 to UTF-8 conversion.
- **Error policies**: Per-row error handling — stop, skip, or log only.
- **Truncate before import**: Optional TRUNCATE TABLE before inserting data.
- **Boolean mapping**: Converts 'true'/'false'/'yes'/'no' strings to numeric (1/0) for MySQL/Oracle boolean columns.
- **Binary data**: Hex-encoded BLOB/BYTEA columns are decoded during import.
- **Oracle session setup**: Automatically sets NLS_DATE_FORMAT and NLS_TIMESTAMP_FORMAT for Oracle targets.

## owl-migrate migrate

End-to-end migration: source database → target database in a single command.

```
Usage:
  owl-migrate migrate [flags] -c <config>

Flags:
      --temp-dir string        Temporary directory for CSV files (default "./output/temp/")
      --skip-ddl               Skip table creation in target (data-only migration)
      --continue-on-error      Continue processing remaining tables even if some fail
      --sql-out string         Output directory for INSERT SQL files (offline mode, skips target DB)
      --resume                    Resume from previous migration state (skips completed tables)
  -r, --report string             Migration report output path (default "./output/migration_report.json")
      --no-quote-identifiers       Output bare identifiers without quoting (compatibility)
```

### Migration Steps

```
1. Load metadata (CSV or live source database)
2. Connect to source database
3. Connect to target database (skipped in --sql-out mode)
4. Create target tables (skipped with --skip-ddl or --sql-out)
5. Export data from source to CSV (temporary files)
6. (Optional) Generate INSERT SQL files (when --sql-out is set)
7. Import CSV data into target database (skipped in --sql-out mode)
8. Generate migration report
```

### SQL Output Mode

Use `--sql-out` to generate dialect-specific INSERT SQL files instead of writing directly to the target database. This is useful for:

- Reviewing and editing data before applying
- Offline migration (no network to target)
- Auditing what data will be inserted
- Applying data changes manually or through a separate process

### Checkpoint/Resume

The `migrate` command maintains a checkpoint file (`migrate_progress.json`) in `--temp-dir`. Each table's export and import status is recorded. When resuming:

- **SUCCESS** tables are skipped (no re-export or re-import)
- **FAIL** tables are truncated and re-imported from scratch
- The export is tracked separately from import, so tables that were exported but not yet imported in a previous run resume from the import phase

```bash
# Initial run (fails partway)
owl-migrate migrate -c ./migrate.yaml

# Resume — skips completed tables, retries failed ones
owl-migrate migrate -c ./migrate.yaml --resume
```

### Continue on Error

With `--continue-on-error`, per-table export or import failures are logged but the migration continues with remaining tables. The exit code is 0 even if some tables failed. Tables that fail are marked FAIL in the checkpoint for retry on the next `--resume` run.

### Migration Report

By default, a JSON report is written to `./output/migration_report.json`:

```json
{
  "source_dialect": "oracle",
  "target_dialect": "postgres",
  "status": "PARTIAL",
  "total_expected": 142,
  "total_actual": 140,
  "total_skipped": 2,
  "total_errors": 0,
  "tables": [
    { "schema": "SCOTT", "table": "EMP", "expected": 14, "actual": 14, "skipped": 0, "errors": 0 },
    { "schema": "SCOTT", "table": "DEPT", "expected": 4, "actual": 4, "skipped": 0, "errors": 0 }
  ]
}
```
