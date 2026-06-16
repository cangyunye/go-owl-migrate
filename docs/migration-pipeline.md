# Migration Pipeline

The `migrate` command runs an end-to-end pipeline: extract → export → create target tables → import. This document covers data transformation, error handling, checkpoint/resume, encoding support, and parallel processing.

## Pipeline Flow

```
┌──────────────────────────────────────────────────────────┐
│                    migrate Command                        │
├──────────────────────────────────────────────────────────┤
│ Step 1: Load Metadata (CSV or live source DB)             │
│ Step 2: Connect to Source Database                        │
│ Step 3: Connect to Target Database (skip if --sql-out)   │
│ Step 4: Create Target Tables (skip if --skip-ddl)         │
│ Step 5: Export Data from Source to CSV                    │
│ Step 6: Generate INSERT SQL (only if --sql-out)           │
│ Step 7: Import CSV Data into Target                       │
│ Step 8: Generate Migration Report (JSON)                  │
└──────────────────────────────────────────────────────────┘
```

## Export Behavior (`export` / `migrate` Step 5)

### Pagination Strategies

| PK Available | Strategy | Description |
|---|---|---|
| Yes (single-column) | Keyset / Cursor | `WHERE pk > $1 ORDER BY pk LIMIT N` — efficient for large tables |
| Yes (composite) | Composite cursor | `WHERE pk1 > $1 AND pk2 > $2 ... ORDER BY pk1, pk2` |
| No | Limit-only | `SELECT ... LIMIT N` — returns same page repeatedly if data changes |

### CSV Output Format

- **File name**: `{schema}.{table}.csv` (lowercase)
- **Header**: Column names (RFC 4180)
- **Delimiter**: Configurable (default `,`)
- **Quote char**: Configurable (default `"`)
- **Null representation**: Configurable (default `\N`)
- **Line terminator**: Configurable (default `\n`)
- **Timestamp format**: Compact `yyyyMMddHHmmss` (e.g., `19801217000000`)
- **Binary data**: Hex-encoded (BLOB/BYTEA/RAW columns)
- **Quote policy**: Values containing delimiter, quote, or newlines are quoted per RFC 4180

### Parallel Export

Tables are exported concurrently using a worker pool. Configuration:

```yaml
export:
  parallel:
    enabled: true        # Enable parallel export
    max_workers: 4       # Max concurrent table exports
```

If `max_workers` exceeds the number of tables, it's capped to `len(tables)`.

### Table Filtering

```yaml
export:
  tables:
    include: ["*"]       # Export all tables
    # include: ["SCOTT.EMP", "SCOTT.DEPT"]  # Specific tables only
```

## Import Behavior (`import` / `migrate` Step 7)

### CSV Input Format

- Expects files named `{schema}.{table}.csv` (lowercase) in the source directory
- First row is the header with column names
- Lazy quotes and trimmed leading spaces are enabled

### Data Transformations

Transformations are applied per-value during import, controlled by `import.data_transforms`:

```yaml
import:
  data_transforms:
    datetime_format: "yyyyMMddHHmmss"     # Auto-convert compact datetime
    datetime_format_fallback: ["..."]     # Additional format patterns
    datetime_truncate_to_target: false    # Truncate datetime to target precision
    trim_strings: true                    # Trim whitespace from string values
    null_if: ["NULL", "null", "\\N"]     # Values treated as NULL
    source_encoding: ""                   # Source CSV encoding ("" = UTF-8)
    target_encoding: ""                   # Target DB encoding ("" = UTF-8)
```

#### DateTime Conversion

| Source Format | Length | Converted To | Example |
|---|---|---|---|
| `yyyyMMddHHmmss` | 14 digits | `YYYY-MM-DD HH24:MI:SS` | `19801217000000` → `1980-12-17 00:00:00` |
| `yyyyMMdd` | 8 digits | `YYYY-MM-DD` | `19801217` → `1980-12-17` |

#### Boolean Conversion (MySQL/Oracle targets)

When a column's data type is `BOOLEAN` and the target is MySQL or Oracle, the importer converts text boolean values to numeric:

| Input | Output |
|---|---|
| `true`, `t`, `yes`, `y` | `1` |
| `false`, `f`, `no`, `n` | `0` |

#### String Trimming

When `trim_strings: true`, all string values have leading/trailing whitespace removed before insert.

#### Numeric NULL Handling

Values matching `null_if` entries (case-sensitive comparison) or the CSV null marker are inserted as SQL `NULL`.

### Encoding Conversion

The importer supports decoding CSV files from non-UTF-8 encodings to UTF-8 during import.

```yaml
import:
  data_transforms:
    source_encoding: "GBK"    # Decode from this encoding to UTF-8
```

#### Supported Encodings

| Config Value | Encoding | Library |
|---|---|---|
| `""` (empty) | UTF-8 (no conversion) | — |
| `GBK`, `GB2312`, `GB18030` | GBK | `golang.org/x/text/encoding/simplifiedchinese` |
| `LATIN1`, `ISO-8859-1` | ISO 8859-1 | `golang.org/x/text/encoding/charmap` |
| `LATIN9`, `ISO-8859-15` | ISO 8859-15 | `golang.org/x/text/encoding/charmap` |
| `WINDOWS-1252` | Windows-1252 | `golang.org/x/text/encoding/charmap` |

When encoding conversion fails for a value, the original bytes are used as fallback (log warning only).

### Error Handling

Three error policies control per-row behavior within a table import:

```yaml
import:
  batch:
    error_policy: skip_row       # stop | skip_row | log_only
    max_errors_before_stop: 0    # 0 = unlimited
```

| Policy | Behavior |
|---|---|
| `stop` | Abort the current table import immediately on the first error. The table is rolled back to the last commit. |
| `skip_row` | Skip the failing row, increment the skip counter, and continue. If `max_errors_before_stop > 0`, switches to abort after that many errors. |
| `log_only` | Log the error at WARN level and continue inserting the row (may fail again). |

**Table-level error isolation**: When one table's export or import encounters errors, the `migrate` command continues with remaining tables by default. Use `--continue-on-error` to return exit code 0 even when some tables fail.

### Target Table Creation

When the target table does not exist, the importer creates it automatically using a cross-dialect type mapping:

```go
// Oracle target types
NUMBER(10)    = INT
VARCHAR2(N)   = VARCHAR/CHAR
CLOB          = TEXT
BLOB          = BLOB
TIMESTAMP     = TIMESTAMP

// MySQL target types
INTEGER       = INT
VARCHAR(N)    = VARCHAR/VARCHAR2
DECIMAL       = NUMBER/NUMERIC
LONGTEXT      = TEXT/CLOB
LONGBLOB      = BLOB/BYTEA
TINYINT(1)    = BOOLEAN

// PostgreSQL target types
VARCHAR       = VARCHAR2
NUMERIC       = NUMBER
TEXT          = CLOB
BYTEA         = BLOB
BOOLEAN       = BOOLEAN
```

See [Import/DDL type mapping source](../internal/cmd/import.go) for the full map.

### Parallel Import

Tables are imported concurrently using a worker pool:

```yaml
import:
  parallel:
    enabled: true             # Enable parallel import
    max_workers: 4            # Max concurrent table imports
    respect_foreign_keys: false  # Order tables by FK dependencies
```

### Oracle-Specific Session Settings

When importing into Oracle, the importer sets these session parameters for proper string-to-date conversion:

```sql
ALTER SESSION SET NLS_DATE_FORMAT = 'YYYY-MM-DD HH24:MI:SS';
ALTER SESSION SET NLS_TIMESTAMP_FORMAT = 'YYYY-MM-DD HH24:MI:SS';
ALTER SESSION SET NLS_TIMESTAMP_TZ_FORMAT = 'YYYY-MM-DD HH24:MI:SS TZH:TZM';
```

## Checkpoint/Resume

The `migrate` command persists per-table migration state to a checkpoint file, enabling resumption after interruption.

### State File

Location: `{temp-dir}/migrate_progress.json`

```json
{
  "version": 1,
  "source": "oracle",
  "target": "postgres",
  "started_at": "2026-06-10T10:00:00+08:00",
  "tables": {
    "scott.emp": {
      "exported": true,
      "exported_rows": 14,
      "imported": true,
      "status": "SUCCESS",
      "error": ""
    },
    "scott.dept": {
      "exported": true,
      "exported_rows": 4,
      "imported": false,
      "status": "FAIL",
      "error": "ORA-00001: unique constraint violated"
    }
  }
}
```

### Resume Behavior

When running with `--resume`:

| Previous Status | Phase | Behavior |
|---|---|---|
| **SUCCESS** (both exported and imported) | All | Skipped entirely |
| **Exported only** (not imported) | Export | Skipped (data exists from previous export) |
| **Exported only** (not imported) | Import | Import runs (target is truncated to avoid duplicates) |
| **FAIL** | Export | Re-exported |
| **FAIL** | Import | Table truncated, re-imported from existing CSV |
| **No state** | All | Processed fresh |

```bash
# Initial run (interrupted after 2 of 5 tables)
owl-migrate migrate -c ./migrate.yaml

# Resume — picks up where it left off
owl-migrate migrate -c ./migrate.yaml --resume
```

The checkpoint file is saved after each phase (export and import), so partial progress is never lost.

### Truncate on Resume

When resuming, the importer performs `TRUNCATE TABLE` before re-importing to avoid duplicate key violations from previously imported rows.

## SQL Output Mode (`--sql-out`)

Instead of writing directly to the target database, the pipeline generates dialect-specific INSERT SQL files:

```bash
owl-migrate migrate -c ./migrate.yaml --sql-out ./output/insert/
```

Behavior changes:

| Step | Behavior |
|---|---|
| Database connections | Target DB is NOT connected |
| Target table creation | Skipped |
| Data import | INSERT SQL files generated instead |
| Checkpoint | Tables marked as imported after SQL generation |
| Migration report | Generated normally (all rows considered imported) |

Generated SQL files follow the same format as `export insert`:

- `{schema}.{table}.insert.sql`
- Batched INSERT statements with transaction wrappers (BEGIN/COMMIT for PG/Oracle)
- Configurable batch size (default 100 rows per INSERT)
- Dialect-specific quoting and placeholder syntax
- Optional TRUNCATE TABLE prefix

## Binary Data

During export, binary columns (BLOB, BYTEA, RAW, BINARY, VARBINARY) are hex-encoded in the CSV file.
During import, hex-encoded values are automatically decoded back to binary before insertion. The encoding is detected from the column's data type in the metadata.
