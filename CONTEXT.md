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

## Pipeline Architecture

### Generator (offline code generation)
Produces SQL text files without connecting to any database:
- **DDLGenerator** — `CREATE TABLE/INDEX/VIEW` files from SchemaModel
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

| Object | SchemaModel Storage | DDLBuilder Method | Status |
|--------|-------------------|-------------------|--------|
| Table | Tables map (keyed by `SCHEMA.NAME`) | `BuildCreateTable` | ✅ Implemented in all dialects |
| Index | Per-table IndexDef list | `BuildCreateIndex` | ⚪ Stub (P0 priority) |
| View | SchemaModel.Views slice | `BuildCreateView` | ⚪ Stub (P0) |
| Sequence | SchemaModel.allSequences | `BuildCreateSequence` | ⚪ Stub (P1) |
| Trigger | SchemaModel.allTriggers | `BuildCreateTrigger` | ⚪ Stub (P3) |
| Function | SchemaModel.allFunctions | `BuildCreateFunction` | ⚪ Stub (P3) |
| Materialized View | SchemaModel.MViews | `BuildCreateMView` | ⚪ Stub (P2) |
| Synonym | SchemaModel.Synonyms | `BuildCreateSynonym` | ⚪ Stub (Oracle only, P2) |
| Package spec | SchemaModel (via Functions) | `BuildCreatePackage` | ⚪ Stub (Oracle only, P2) |
| Package body | SchemaModel (via Functions) | `BuildCreatePackageBody` | ⚪ Stub (Oracle only, P2) |
