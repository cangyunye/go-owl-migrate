# DuckDB 元数据查询方案

> 源文件: `internal/metadata/extractor/duckdb.go`
> 构建标签: `//go:build duckdb`
> 目标视图: `information_schema.tables`, `information_schema.columns`, `information_schema.table_constraints`, `information_schema.key_column_usage`, `information_schema.referential_constraints`, `information_schema.constraint_column_usage`, `information_schema.views`, `duckdb_indexes()`, `pg_catalog.pg_sequences`
> 功能: `DuckDBMetadataQuerier`

查询参数通过 `?` 占位符传入 schema 名称（默认 `"main"`）。

---

## 1. 表信息 (QueryTables)

```sql
SELECT table_name FROM information_schema.tables
WHERE table_schema = ? AND table_type = 'BASE TABLE'
ORDER BY table_name
```

- 仅返回表名，没有 engine、comment 等额外信息

## 2. 列信息 (QueryColumns)

```sql
SELECT table_name, column_name, data_type, is_nullable, column_default
FROM information_schema.columns
WHERE table_schema = ?
ORDER BY table_name, ordinal_position
```

- `ordinal_position` 未从 SQL 查询获取，代码中通过 `len(columns)+1` 计算
- 不获取 `character_maximum_length`/`numeric_precision`/`numeric_scale`/注释/字符集等信息

## 3. 主键信息 (QueryPrimaryKeys)

```sql
SELECT kcu.table_name, kcu.constraint_name, kcu.column_name, kcu.ordinal_position
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
    ON tc.constraint_name = kcu.constraint_name
    AND tc.table_schema = kcu.table_schema
WHERE tc.table_schema = ? AND tc.constraint_type = 'PRIMARY KEY'
ORDER BY kcu.table_name, kcu.ordinal_position
```

- DuckDB 的 JOIN 不需要 `constraint_catalog` 匹配

## 4. 索引信息 (QueryIndexes)

```sql
SELECT index_name, table_name, is_unique, expressions
FROM duckdb_indexes()
WHERE schema_name = ?
ORDER BY index_name
```

- 使用 DuckDB 专有的表函数 `duckdb_indexes()`（非标准 `information_schema`）
- 索引类型硬编码为 `"BTREE"`
- 返回的 `expressions` 格式为 `"[col1, col2]"`，代码使用 `splitCSL` 函数解析
- 通过 `strings.Contains(indexName, "pk_")` 跳过主键索引（启发式规则，可能有误判）

## 5. 外键信息 (QueryForeignKeys)

```sql
SELECT
    kcu.table_name, kcu.column_name, kcu.constraint_name,
    ccu.table_name AS ref_table, ccu.column_name AS ref_column,
    COALESCE(r.update_rule, 'NO ACTION'), COALESCE(r.delete_rule, 'NO ACTION')
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
    ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
JOIN information_schema.referential_constraints r
    ON tc.constraint_name = r.constraint_name AND tc.table_schema = r.constraint_schema
JOIN information_schema.constraint_column_usage ccu
    ON r.unique_constraint_name = ccu.constraint_name
WHERE tc.table_schema = ? AND tc.constraint_type = 'FOREIGN KEY'
```

- 使用 `r.unique_constraint_name` 关联 `constraint_column_usage`（而非直接引用约束名）
- `ref_schema` 硬编码为当前 schema（不查询 `ccu.table_schema`）
- 不查询 `deferrable`

## 6. 视图信息 (QueryViews)

```sql
SELECT table_name, view_definition
FROM information_schema.views
WHERE table_schema = ?
    AND table_name NOT LIKE 'duckdb_%'
    AND table_name NOT LIKE 'pragma_%'
    AND table_name NOT LIKE 'sqlite_%'
ORDER BY table_name
```

- 排除 DuckDB 内部视图（`duckdb_*`、`pragma_*`、`sqlite_*`）

## 7. 序列信息 (QuerySequences)

```sql
SELECT sequencename, start_value, increment_by, min_value, max_value, cycle
FROM pg_catalog.pg_sequences
WHERE schemaname = ?
ORDER BY sequencename
```

- DuckDB 通过 PostgreSQL 兼容的 `pg_catalog.pg_sequences` 暴露序列信息
- `cache_size` 硬编码为 1
- `cycle` 返回布尔值（代码内通过 `strings.EqualFold` 处理）

## 8. 触发器信息 (QueryTriggers)

```sql
-- DuckDB 不支持触发器，返回 (nil, nil)
```

## 9. 同义词 (QuerySynonyms)

```sql
-- DuckDB 不支持同义词，返回 (nil, nil)
```

---

## 验证说明

以上 SQL 提取自 `internal/metadata/extractor/duckdb.go` 的各方法实现。DuckDB 元数据查询较为简单，因 DuckDB 作为嵌入式 OLAP 数据库功能有限。

DuckDB 的 `duckdb_indexes()` 表函数是 DuckDB 特有的 API，在编写通用迁移工具时需要注意此兼容性问题。

## 被复用的数据库类型

DuckDB 提取器当前是独立的，未作为其他数据库类型的默认提取器。
