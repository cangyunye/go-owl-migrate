# 数据库元数据查询方案

本文档整理了 go-owl-migrate 项目中支持的数据库元数据查询方案，供后续开发数据库迁移/元数据工具时参考。

## 文档列表

| 文档 | 描述 |
|---|---|
| [Oracle](oracle.md) | Oracle `all_*` 字典视图的 9 大类元数据查询 |
| [PostgreSQL](postgresql.md) | PostgreSQL `information_schema` + `pg_catalog` 的 8 大类查询 |
| [MySQL](mysql.md) | MySQL `information_schema` 的 8 大类查询 |
| [DuckDB](duckdb.md) | DuckDB `information_schema` + `duckdb_indexes()` + `pg_catalog` |
| [SQLite3](sqlite3.md) | SQLite3 `sqlite_master` + PRAGMA 命令 |
| [复合方言](compound-dialects.md) | GoldenDB / OceanBase / PanWeiDB / OpenGaussDB 的映射关系 |
| [运行时数据迁移](runtime-data-migration.md) | 导出/导入阶段使用的 SQL 模式 |

## 查询架构

```
用户指定 dbType + schema
        │
        ▼
 normalizeDBType()
   ├── oracle       → OracleMetadataQuerier  (all_* views)
   ├── postgres     → PGMetadataQuerier      (information_schema + pg_catalog)
   ├── mysql        → MySQLMetadataQuerier   (information_schema)
   ├── duckdb (*)   → DuckDBMetadataQuerier  (information_schema + duckdb_indexes)
   └── sqlite3 (*)  → SQLite3MetadataQuerier (sqlite_master + PRAGMA)
        │
        ▼
 Extract() → SchemaModel
   ├── QueryTables       → []*TableDef
   ├── QueryColumns      → []*ColumnDef
   ├── QueryPrimaryKeys  → []*PrimaryKeyDef
   ├── QueryIndexes      → []*IndexDef
   ├── QueryForeignKeys  → []*ForeignKeyDef
   ├── QueryViews        → []*ViewDef
   ├── QuerySequences    → []*SequenceDef
   ├── QueryTriggers     → []*TriggerDef
   └── QuerySynonyms     → []*SynonymDef
```

> `(*)` DuckDB 和 SQLite3 需要构建标签支持（`//go:build duckdb` 和 `//go:build sqlite3`），不在默认构建中。

## 支持功能矩阵

| 数据库 | 表 | 列 | 主键 | 索引 | 外键 | 视图 | 序列 | 触发器 | 同义词 |
|---|---|---|---|---|---|---|---|---|---|
| Oracle | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| PostgreSQL | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✗ |
| MySQL | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✗ | ✓ | ✗ |
| DuckDB | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✗ | ✗ |
| SQLite3 | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✗ | ✓ | ✗ |

## 源码位置

- 提取器接口: `internal/metadata/extractor/extractor.go`
- Oracle 提取器: `internal/metadata/extractor/oracle.go`
- PostgreSQL 提取器: `internal/metadata/extractor/postgres.go`
- MySQL 提取器: `internal/metadata/extractor/mysql.go`
- DuckDB 提取器: `internal/metadata/extractor/duckdb.go`
- SQLite3 提取器: `internal/metadata/extractor/sqlite3.go`
- 元数据模型: `internal/metadata/model.go`
- 运行时导出: `internal/transfer/exporter/exporter.go`
- 运行时导入: `internal/transfer/importer/importer.go`
