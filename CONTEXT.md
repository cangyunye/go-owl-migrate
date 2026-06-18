# go-owl-migrate — Domain Glossary

## Core Concepts

### Migration
The end-to-end process of extracting schema metadata + data from a source database and applying them to a target database. May include DDL generation, data export, data transformation, and data import steps. Can run offline (CSV-based) or online (live database).

### Metadata
Structural definition of database objects: tables, columns, primary keys, indexes, foreign keys, views, triggers, functions, sequences, synonyms, packages. The schema without the data.

### Dialect
A composed struct of five interfaces that encapsulates all database-specific behavior:
- **TypeMapper** — maps raw DB types to/from canonical LogicalTypes
- **IdentifierQuoter** — quotes/unquotes identifiers (backtick vs double-quote)
- **Features** — describes DB capabilities (transactional DDL, IF NOT EXISTS, etc.)
- **DDLBuilder** — generates CREATE statements for database objects
- **DMLHelper** — generates pagination, value formatting, and DML syntax

### LogicalType
Database-independent type representation: a LogicalBase enum (LBVarchar, LBInt, LBNumeric, etc.) plus Length/Precision/Scale metadata. Used as the intermediate representation in type mapping.

### SchemaModel
In-memory aggregate container for all metadata about a database schema. Holds `TableDef`, `ViewDef`, `IndexDef`, `ForeignKeyDef`, `TriggerDef`, `FunctionDef`, `SequenceDef`, `SynonymDef`, `PackageDef` collections.

## Database Classification

### Core Dialects
Dialects with full implementation — metadata extraction, type mapping, DDL generation, DML support:
- **Oracle** — `"` double-quote quoting, PL/SQL, go-ora driver
- **PostgreSQL** — `"` double-quote quoting, PL/pgSQL, lib/pq driver
- **MySQL** — `` ` `` backtick quoting, go-sql-driver

### Compound Dialects
Dialects that inherit from a core dialect and override specific behaviors:
- **GoldenDB** — MySQL tenant (inherits MySQL) or Oracle tenant (inherits Oracle)
- **OceanBase** — MySQL tenant (inherits MySQL + SEQUENCE support) or Oracle tenant (inherits Oracle + transactional TRUNCATE)
- **PanWeiDB** — PostgreSQL mode (inherits PG), MySQL B mode (inherits MySQL), or Oracle A mode (inherits Oracle)
- **OpenGaussDB** — inherits PostgreSQL

### Embedded Dialects
Dialects for embedded databases that run in-process with no external server:
- **SQLite3** — double-quote quoting, CGo binding (mattn/go-sqlite3), metadata via sqlite_master + PRAGMA
- **DuckDB** — double-quote quoting, in-process analytical database, information_schema compatible (planned)

## Pipeline Architecture

### Generator (offline code generation)
Produces SQL text files without connecting to any database:
- **DDLGenerator** — per-object SQL files (TABLE, INDEX, VIEW, SEQUENCE, TRIGGER, FUNCTION, SYNONYM, PACKAGE, etc.) from SchemaModel
- **SelectGenerator** — paginated SELECT statements for data export
- **InsertGenerator** — dialect-aware INSERT SQL from CSV data

### Exporter (online data extraction)
Connects to a source database via `database/sql`, reads table data with cursor-based pagination (keyset pagination on PK), and writes to CSV/SQL/XLSX files. Supports parallel table export via worker pool.

### Importer (online data loading)
Reads CSV data files, applies data transforms (datetime format conversion, encoding conversion, boolean mapping, binary hex decode), and inserts into target database in batched transactions. Supports parallel table import and configurable error policies.

## Migration Modes

### Offline mode
Metadata from CSV files → DDL generated for target dialect → Data export from source to CSV → INSERT SQL generated offline → SQL executed later.

### Online mode (migrate command)
End-to-end pipeline: extract metadata from source → create target tables → export data to CSV → import into target. Supports checkpoint/resume and per-table error isolation.

### SQL output mode
`migrate --sql-out ./dir/` — reads from source database, writes dialect-aware INSERT SQL files instead of writing to target DB. No target connection needed.

## Object Types

| Object | SchemaModel Storage | DDLBuilder Method | MySQL | Oracle | PostgreSQL | SQLite3 |
|--------|-------------------|-------------------|-------|--------|------------|---------|
| Table | Tables map (keyed by `SCHEMA.NAME`) | `BuildCreateTable` | ✅ | ✅ | ✅ | ✅ |
| Index | Per-table IndexDef list | `BuildCreateIndex` | ✅ | ✅ | ✅ | ✅ |
| View | SchemaModel.Views slice | `BuildCreateView` | ✅ | ✅ | ✅ | ✅ |
| Sequence | SchemaModel.allSequences | `BuildCreateSequence` | — | ✅ | ✅ | — |
| Synonym | SchemaModel.Synonyms | `BuildCreateSynonym` | — | ✅ | — | — |
| Materialized View | SchemaModel.MViews | `BuildCreateMView` | — | ✅ | ✅ | — |
| Trigger | SchemaModel.allTriggers | `BuildCreateTrigger` | ✅ | ✅ | ✅ | ✅ |
| Function | SchemaModel.allFunctions | `BuildCreateFunction` | ✅ | ✅ | ✅ | — |
| Package spec | SchemaModel.allPackages | `BuildCreatePackage` | — | ✅ | — | — |
| Package body | SchemaModel.allPackageBodies | `BuildCreatePackageBody` | — | ✅ | — | — |
