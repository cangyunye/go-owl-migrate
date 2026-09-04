# CSV Metadata Format

When using `metadata.type: csv`, the tool reads table/column definitions from CSV files in the specified directory.

This document is the single column specification for **both** CSV directions of this tool:

- the **csv metadata loader** (`metadata.type: csv`) — reads table/column definitions from the CSV files in the configured directory; and
- the **metadata exporter writer** — `owl-migrate export-metadata --format csv` writes exactly the same files (`service.ExportMetadataFiles`, 13 canonical files, one per object-type stem, generated in the fixed stem order below; `--objects` selects a subset). The CLI command and the serve endpoint (`POST /api/v1/metadata/export`, page `/export-metadata`) share this single implementation.

## File Inventory

The 13 canonical files are listed in the fixed object-type stem order. The exporter writes all of them by default; the csv loader parses every one that is present in the metadata directory. Only `tables.csv` is load-critical (its absence aborts loading); `columns.csv` is parsed whenever present and is required for column/DDL-aware consumers; all other files may be absent without failing the load.

| File | Required | Description |
|---|---|---|
| `tables.csv` | ✓ | Table definitions — the loader fails when this file is missing (TABLE_TYPE = TABLE / VIEW / MVIEW) |
| `columns.csv` | ✓ | Column definitions with data types (parse errors are fatal to the load) |
| `primary_keys.csv` | — | Primary key constraints |
| `indexes.csv` | — | Index definitions |
| `foreign_keys.csv` | — | Foreign key constraints |
| `views.csv` | — | View definitions (SQL text) |
| `mviews.csv` | — | Materialized view definitions |
| `sequences.csv` | — | Sequence definitions |
| `synonyms.csv` | — | Synonym definitions (Oracle) |
| `triggers.csv` | — | Trigger definitions |
| `functions.csv` | — | Stored functions/procedures |
| `packages.csv` | — | Package specifications (Oracle) |
| `package_bodies.csv` | — | Package bodies (Oracle) |

**Note**: File names are case-insensitive (`tables.csv` or `Tables.csv` both work).

## Column Reference

### tables.csv

| Column | Type | Description |
|---|---|---|
| `TABLE_SCHEMA` | string | Schema/database name (required) |
| `TABLE_NAME` | string | Table name (required) |
| `TABLE_TYPE` | string | TABLE / VIEW / MVIEW (default: TABLE) |
| `TABLE_COMMENT` | string | Table comment |
| `ENGINE` | string | Storage engine (MySQL) |
| `TABLESPACE` | string | Tablespace name |
| `PARTITIONED` | string | YES / NO |
| `PARTITION_INFO` | string | Partition definition |
| `ROW_FORMAT` | string | Row format |
| `TEMPORARY` | string | YES / NO |
| `CHARSET` | string | Character set |
| `COLLATION` | string | Collation |
| `OWNER` | string | Original owner/schema |

### columns.csv

| Column | Type | Description |
|---|---|---|
| `TABLE_SCHEMA` | string | Schema name (required) |
| `TABLE_NAME` | string | Table name (required) |
| `COLUMN_NAME` | string | Column name (required) |
| `ORDINAL_POSITION` | number | Column position >= 1 (required) |
| `DATA_TYPE` | string | Data type name (required) |
| `DATA_LENGTH` | number | Character length (VARCHAR/CHAR) |
| `DATA_PRECISION` | number | Numeric precision |
| `DATA_SCALE` | number | Numeric scale |
| `NULLABLE` | string | YES / NO (default: YES) |
| `DEFAULT_VALUE` | string | Default value expression |
| `COLUMN_COMMENT` | string | Column comment |
| `IS_IDENTITY` | string | YES / NO (default: NO) |
| `IDENTITY_GENERATION` | string | ALWAYS / BY DEFAULT |
| `IDENTITY_START` | number | Identity start value |
| `IDENTITY_INCREMENT` | number | Identity increment |
| `CHAR_USED` | string | CHAR / BYTE (Oracle) |
| `HIDDEN_COLUMN` | string | YES / NO |
| `VIRTUAL_EXPRESSION` | string | Generated column expression |
| `ENUM_VALUES` | string | Comma-separated enum values |
| `CHARACTER_SET` | string | Character set |
| `COLLATION` | string | Column-level collation |
| `ON_UPDATE` | string | ON UPDATE expression (MySQL) |

### primary_keys.csv

| Column | Type | Description |
|---|---|---|
| `TABLE_SCHEMA` | string | Schema name |
| `TABLE_NAME` | string | Table name |
| `CONSTRAINT_NAME` | string | Constraint name |
| `COLUMN_NAME` | string | Column in the PK |
| `ORDINAL_POSITION` | number | Position within composite PK |

### indexes.csv

| Column | Type | Description |
|---|---|---|
| `TABLE_SCHEMA` | string | Schema name |
| `TABLE_NAME` | string | Table name |
| `INDEX_NAME` | string | Index name |
| `INDEX_TYPE` | string | BTREE / BITMAP / GIN / GIST / FULLTEXT / UNIQUE |
| `UNIQUENESS` | string | UNIQUE / NONUNIQUE |
| `COLUMN_NAME` | string | Indexed column |
| `ORDINAL_POSITION` | number | Position within composite index |
| `EXPRESSION` | string | Function-based index expression |
| `DESCEND` | string | ASC / DESC |
| `WHERE_CLAUSE` | string | Partial index predicate |

### foreign_keys.csv

| Column | Type | Description |
|---|---|---|
| `CONSTRAINT_NAME` | string | FK constraint name |
| `TABLE_SCHEMA` | string | Schema name |
| `TABLE_NAME` | string | Table name |
| `COLUMN_NAME` | string | FK column |
| `REF_SCHEMA` | string | Referenced schema |
| `REF_TABLE` | string | Referenced table |
| `REF_COLUMN` | string | Referenced column |
| `DELETE_RULE` | string | CASCADE / SET NULL / RESTRICT / NO ACTION |
| `UPDATE_RULE` | string | CASCADE / SET NULL / RESTRICT / NO ACTION |
| `DEFERRABLE` | string | DEFERRABLE / NOT DEFERRABLE |

### views.csv

| Column | Type | Description |
|---|---|---|
| `VIEW_SCHEMA` | string | Schema name |
| `VIEW_NAME` | string | View name |
| `VIEW_DEFINITION` | string | Full view SELECT text |
| `VIEW_COMMENT` | string | View comment |

### sequences.csv

| Column | Type | Description |
|---|---|---|
| `SEQUENCE_SCHEMA` | string | Schema name |
| `SEQUENCE_NAME` | string | Sequence name |
| `START_VALUE` | number | Start value |
| `INCREMENT_BY` | number | Increment (default: 1) |
| `MIN_VALUE` | number | Minimum value |
| `MAX_VALUE` | number | Maximum value |
| `CYCLE` | string | YES / NO |
| `CACHE_SIZE` | number | Cache size |
| `ORDER_FLAG` | string | Order guarantee |
| `CURRENT_VALUE` | number | Current value |
| `DATA_TYPE` | string | Numeric data type |

### triggers.csv

| Column | Type | Description |
|---|---|---|
| `TRIGGER_SCHEMA` | string | Trigger schema |
| `TRIGGER_NAME` | string | Trigger name |
| `TABLE_SCHEMA` | string | Associated table schema |
| `TABLE_NAME` | string | Associated table |
| `TRIGGER_TYPE` | string | BEFORE / AFTER / INSTEAD OF / COMPOUND |
| `TRIGGER_EVENT` | string | INSERT / UPDATE / DELETE / TRUNCATE |
| `TRIGGER_BODY` | string | Full trigger source code |
| `STATUS` | string | ENABLED / DISABLED |
| `FOR_EACH` | string | ROW / STATEMENT |
| `WHEN_CLAUSE` | string | Conditional trigger predicate |
| `REFERENCING` | string | REFERENCING clause |
| `DESCRIPTION` | string | Trigger description |
| `LANGUAGE` | string | PLSQL / PLPGSQL |

### synonyms.csv

| Column | Type | Description |
|---|---|---|
| `SYNONYM_NAME` | string | Synonym name (required) |
| `SYNONYM_SCHEMA` | string | Schema that owns the synonym |
| `TARGET_SCHEMA` | string | Schema of the referenced object |
| `TARGET_NAME` | string | Name of the referenced object |
| `IS_PUBLIC` | string | YES / NO — public synonym |
| `TARGET_TYPE` | string | Type of the referenced object (TABLE, VIEW, etc.) |

### mviews.csv

| Column | Type | Description |
|---|---|---|
| `MVIEW_SCHEMA` | string | Schema name |
| `MVIEW_NAME` | string | Materialized view name |
| `MVIEW_QUERY` | string | The SELECT query that defines the view |
| `REFRESH_METHOD` | string | COMPLETE / FAST / FORCE |
| `REFRESH_MODE` | string | DEMAND / COMMIT |
| `REFRESH_INTERVAL` | string | Refresh interval expression |
| `BUILD_MODE` | string | IMMEDIATE / DEFERRED |
| `MVIEW_COMMENT` | string | Description |

### functions.csv

| Column | Type | Description |
|---|---|---|
| `FUNCTION_SCHEMA` | string | Schema name |
| `FUNCTION_NAME` | string | Function or procedure name |
| `FUNCTION_TYPE` | string | FUNCTION / PROCEDURE |
| `RETURN_TYPE` | string | Return data type (for functions) |
| `FUNCTION_BODY` | string | Full function source code |
| `LANGUAGE` | string | PLSQL / PLPGSQL |
| `STATUS` | string | ENABLED / DISABLED |
| `ARGUMENTS` | string | JSON-formatted argument list |
| `AUTH_ID` | string | DEFINER / CURRENT_USER |
| `DETERMINISTIC` | string | YES / NO |
| `PARALLEL` | string | YES / NO |

### packages.csv

| Column | Type | Description |
|---|---|---|
| `PACKAGE_SCHEMA` | string | Schema name |
| `PACKAGE_NAME` | string | Package name |
| `PACKAGE_SPEC` | string | Full package specification (header) |
| `STATUS` | string | ENABLED / DISABLED |
| `AUTH_ID` | string | DEFINER / CURRENT_USER |
| `DESCRIPTION` | string | Description |

### package_bodies.csv

| Column | Type | Description |
|---|---|---|
| `PACKAGE_SCHEMA` | string | Schema name |
| `PACKAGE_NAME` | string | Package name |
| `PACKAGE_BODY` | string | Full package body (implementation) |
| `STATUS` | string | ENABLED / DISABLED |

## Example: SCOTT Schema

### tables.csv

```csv
TABLE_SCHEMA,TABLE_NAME,TABLE_TYPE,TABLE_COMMENT
SCOTT,EMP,TABLE,Employee table
SCOTT,DEPT,TABLE,Department table
SCOTT,BONUS,TABLE,Bonus table
```

### columns.csv

```csv
TABLE_SCHEMA,TABLE_NAME,COLUMN_NAME,ORDINAL_POSITION,DATA_TYPE,DATA_LENGTH,DATA_PRECISION,DATA_SCALE,NULLABLE
SCOTT,EMP,EMPNO,1,NUMBER,22,4,0,NO
SCOTT,EMP,ENAME,2,VARCHAR2,10,,,YES
SCOTT,EMP,JOB,3,VARCHAR2,9,,,YES
SCOTT,EMP,MGR,4,NUMBER,22,4,0,YES
SCOTT,EMP,HIREDATE,5,DATE,,,,YES
SCOTT,EMP,SAL,6,NUMBER,22,7,2,YES
SCOTT,EMP,COMM,7,NUMBER,22,7,2,YES
SCOTT,EMP,DEPTNO,8,NUMBER,22,2,0,YES
SCOTT,DEPT,DEPTNO,1,NUMBER,22,2,0,NO
SCOTT,DEPT,DNAME,2,VARCHAR2,14,,,YES
SCOTT,DEPT,LOC,3,VARCHAR2,13,,,YES
SCOTT,BONUS,ENAME,1,VARCHAR2,10,,,YES
SCOTT,BONUS,JOB,2,VARCHAR2,9,,,YES
SCOTT,BONUS,SAL,3,NUMBER,,,,YES
SCOTT,BONUS,COMM,4,NUMBER,,,,YES
```

### primary_keys.csv

```csv
TABLE_SCHEMA,TABLE_NAME,CONSTRAINT_NAME,COLUMN_NAME,ORDINAL_POSITION
SCOTT,EMP,PK_EMP,EMPNO,1
SCOTT,DEPT,PK_DEPT,DEPTNO,1
```

### indexes.csv

```csv
TABLE_SCHEMA,TABLE_NAME,INDEX_NAME,INDEX_TYPE,UNIQUENESS,COLUMN_NAME,ORDINAL_POSITION
SCOTT,EMP,IDX_EMP_ENAME,BTREE,NONUNIQUE,ENAME,1
SCOTT,DEPT,IDX_DEPT_DNAME,BTREE,NONUNIQUE,DNAME,1
```

### foreign_keys.csv

```csv
CONSTRAINT_NAME,TABLE_SCHEMA,TABLE_NAME,COLUMN_NAME,REF_SCHEMA,REF_TABLE,REF_COLUMN,DELETE_RULE
FK_DEPTNO,SCOTT,EMP,DEPTNO,SCOTT,DEPT,DEPTNO,CASCADE
```
