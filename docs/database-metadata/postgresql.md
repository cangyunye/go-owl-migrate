# PostgreSQL 元数据查询方案

> 源文件: `internal/metadata/extractor/postgres.go`
> 目标视图: `information_schema.tables`, `information_schema.columns`, `information_schema.table_constraints`, `information_schema.key_column_usage`, `information_schema.constraint_column_usage`, `information_schema.referential_constraints`, `information_schema.views`, `pg_catalog.pg_description`, `pg_catalog.pg_class`, `pg_catalog.pg_index`, `pg_catalog.pg_namespace`, `pg_catalog.pg_attribute`, `pg_catalog.pg_am`, `pg_catalog.pg_sequences`, `pg_catalog.pg_trigger`, `pg_catalog.pg_proc`
> 功能: `PGMetadataQuerier`

查询参数通过 `$1` 占位符传入 schema 名称。

---

## 1. 表信息 (QueryTables)

```sql
SELECT table_name, table_type
FROM information_schema.tables
WHERE table_schema = $1
    AND table_type = 'BASE TABLE'
ORDER BY table_name
```

- 仅返回 `BASE TABLE`，排除视图
- 不获取行数估算

## 2. 列信息 (QueryColumns)

```sql
SELECT
    c.table_name,
    c.column_name,
    c.ordinal_position,
    c.data_type,
    COALESCE(c.character_maximum_length, 0) AS char_length,
    COALESCE(c.numeric_precision, 0) AS num_precision,
    COALESCE(c.numeric_scale, 0) AS num_scale,
    c.is_nullable,
    COALESCE(c.column_default, '') AS column_default,
    COALESCE(pgd.description, '') AS column_comment,
    COALESCE(c.identity_generation, '') AS identity_generation,
    COALESCE(c.character_set_name, '') AS character_set_name,
    COALESCE(c.collation_name, '') AS collation_name
FROM information_schema.columns c
LEFT JOIN pg_catalog.pg_description pgd
    ON pgd.objsubid = c.ordinal_position
    AND pgd.objoid = (quote_ident(c.table_schema) || '.' || quote_ident(c.table_name))::regclass::oid
WHERE c.table_schema = $1
ORDER BY c.table_name, c.ordinal_position
```

- 列注释通过 `pg_description` 获取，使用 `::regclass::oid` 转换表名到 OID
- `identity_generation` 为 `'ALWAYS'` 或 `'BY DEFAULT'`
- `character_maximum_length` 适用于 character 类型；numeric 类型使用 `numeric_precision`/`numeric_scale`

## 3. 主键信息 (QueryPrimaryKeys)

```sql
SELECT
    tc.table_name,
    tc.constraint_name,
    kcu.column_name,
    kcu.ordinal_position
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
    ON tc.constraint_catalog = kcu.constraint_catalog
    AND tc.constraint_schema = kcu.constraint_schema
    AND tc.constraint_name = kcu.constraint_name
    AND tc.table_name = kcu.table_name
WHERE tc.constraint_type = 'PRIMARY KEY'
    AND tc.table_schema = $1
ORDER BY tc.table_name, kcu.ordinal_position
```

- JOIN 条件需要匹配 `constraint_catalog`、`constraint_schema`、`constraint_name`、`table_name` 四个字段
- `ordinal_position` 已在 `key_column_usage` 中提供

## 4. 索引信息 (QueryIndexes)

```sql
SELECT
    n.nspname AS schema_name,
    t.relname AS table_name,
    i.relname AS index_name,
    am.amname AS index_type,
    CASE WHEN ix.indisunique THEN 'UNIQUE' ELSE 'NONUNIQUE' END AS uniqueness,
    a.attname AS column_name,
    a.attnum AS ordinal_position,
    COALESCE(pg_get_expr(ix.indexprs, ix.indrelid), '') AS expression
FROM pg_class t
JOIN pg_index ix ON t.oid = ix.indrelid
JOIN pg_class i ON ix.indexrelid = i.oid
JOIN pg_am am ON i.relam = am.oid
JOIN pg_namespace n ON t.relnamespace = n.oid
LEFT JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(ix.indkey)
WHERE n.nspname = $1
    AND t.relkind = 'r'
    AND NOT ix.indisprimary
ORDER BY t.relname, i.relname, a.attnum
```

- 使用 `pg_catalog` 视图而非 `information_schema`，因为 `information_schema` 不暴露索引类型
- `NOT ix.indisprimary` 排除主键索引
- `pg_get_expr(ix.indexprs, ...)` 获取表达式索引（如 `lower(name)`）
- `index_type` 从 `pg_am.amname` 获取（`btree`、`gin`、`gist`、`hash`、`brin` 等）
- `LEFT JOIN pg_attribute` 可能返回 NULL（函数索引），代码中 `columnName == ""` 时跳过

## 5. 外键信息 (QueryForeignKeys)

```sql
SELECT
    tc.table_name,
    tc.constraint_name,
    kcu.column_name,
    ccu.table_schema AS ref_schema,
    ccu.table_name AS ref_table,
    ccu.column_name AS ref_column,
    COALESCE(rc.delete_rule, '') AS delete_rule,
    COALESCE(rc.update_rule, '') AS update_rule,
    COALESCE(tc.is_deferrable, 'NOT DEFERRABLE') AS deferrable
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
    ON tc.constraint_catalog = kcu.constraint_catalog
    AND tc.constraint_schema = kcu.constraint_schema
    AND tc.constraint_name = kcu.constraint_name
    AND tc.table_name = kcu.table_name
JOIN information_schema.constraint_column_usage ccu
    ON tc.constraint_catalog = ccu.constraint_catalog
    AND tc.constraint_schema = ccu.constraint_schema
    AND tc.constraint_name = ccu.constraint_name
LEFT JOIN information_schema.referential_constraints rc
    ON tc.constraint_catalog = rc.constraint_catalog
    AND tc.constraint_schema = rc.constraint_schema
    AND tc.constraint_name = rc.constraint_name
WHERE tc.constraint_type = 'FOREIGN KEY'
    AND tc.table_schema = $1
ORDER BY tc.table_name, tc.constraint_name, kcu.ordinal_position
```

- 四表 JOIN 囊括完整的 FK 元数据
- `referential_constraints` 提供 `delete_rule` 和 `update_rule`
- `table_constraints.is_deferrable` 提供可延迟信息

## 6. 视图信息 (QueryViews)

```sql
SELECT
    table_name,
    view_definition,
    '' AS view_comment,
    CASE WHEN is_updatable = 'YES' THEN 'YES' ELSE 'NO' END AS is_updatable,
    CASE WHEN is_insertable_into = 'YES' THEN 'YES' ELSE 'NO' END AS check_option
FROM information_schema.views
WHERE table_schema = $1
ORDER BY table_name
```

- 视图注释硬编码为空字符串（`information_schema.views` 不直接暴露注释）
- `is_updatable` 和 `is_insertable_into` 来自标准 `information_schema`

## 7. 序列信息 (QuerySequences)

```sql
SELECT
    sequencename,
    start_value,
    increment_by,
    min_value,
    max_value,
    CASE WHEN cycle THEN 'YES' ELSE 'NO' END AS cycle,
    COALESCE(cache_size, 1) AS cache_size,
    COALESCE(last_value, start_value) AS last_value,
    data_type::text AS data_type
FROM pg_sequences
WHERE schemaname = $1
ORDER BY sequencename
```

- `pg_sequences` 是 PG 10+ 的系统视图
- `data_type` 返回如 `integer`、`bigint`、`smallint` 等

## 8. 触发器信息 (QueryTriggers)

```sql
SELECT
    tg.tgname AS trigger_name,
    n.nspname AS table_schema,
    t.relname AS table_name,
    CASE
        WHEN tg.tgtype & 2 = 2 THEN 'BEFORE'
        WHEN tg.tgtype & 64 = 64 THEN 'INSTEAD OF'
        ELSE 'AFTER'
    END AS trigger_type,
    string_agg(DISTINCT CASE
        WHEN tg.tgtype & 4 = 4 THEN 'INSERT'
        WHEN tg.tgtype & 16 = 16 THEN 'UPDATE'
        WHEN tg.tgtype & 32 = 32 THEN 'DELETE'
        WHEN tg.tgtype & 128 = 128 THEN 'TRUNCATE'
    END, '/') AS trigger_event,
    pg_get_functiondef(tgfoid) AS trigger_body,
    CASE WHEN tg.tgenabled = 'O' THEN 'ENABLED' ELSE 'DISABLED' END AS status,
    CASE WHEN tg.tgtype & 1 = 1 THEN 'ROW' ELSE 'STATEMENT' END AS for_each,
    COALESCE(pg_get_expr(tg.tgqual, tg.tgrelid), '') AS when_clause,
    COALESCE(obj_description(tg.oid, 'pg_trigger'), '') AS description,
    'PLPGSQL' AS language
FROM pg_trigger tg
JOIN pg_class t ON tg.tgrelid = t.oid
JOIN pg_namespace n ON t.relnamespace = n.oid
WHERE n.nspname = $1
    AND NOT tg.tgisinternal
GROUP BY tg.oid, tg.tgname, n.nspname, t.relname, tg.tgtype, tg.tgfoid, tg.tgenabled, tg.tgrelid, tg.tgqual
ORDER BY tg.tgname
```

- `tgtype` 使用位掩码解析:
  - Bit 0 (1): `ROW` / `STATEMENT`
  - Bit 1 (2): `BEFORE` / Bit 6 (64): `INSTEAD OF` / default: `AFTER`
  - Bit 2 (4): `INSERT` / Bit 4 (16): `UPDATE` / Bit 5 (32): `DELETE` / Bit 7 (128): `TRUNCATE`
- `pg_get_functiondef(tgfoid)` 获取完整的触发器函数定义
- `pg_get_expr(tg.tgqual, tg.tgrelid)` 获取 WHEN 条件
- `NOT tg.tgisinternal` 排除系统内部触发器
- `Language` 硬编码为 `"PLPGSQL"`

## 9. 同义词 (QuerySynonyms)

```sql
-- PostgreSQL 不支持同义词，返回 (nil, nil)
```

---

## 与运行时数据导出的配合

exporter (`internal/transfer/exporter/exporter.go`) 中 PostgreSQL 使用的 SQL 模式：

```sql
-- 游标分页（首次）：
SELECT cols FROM "schema"."table" ORDER BY "pk_cols" LIMIT 5000

-- 游标分页（后续）：
SELECT cols FROM "schema"."table" WHERE "pk" > $1 ORDER BY "pk_cols" LIMIT 5000

-- 无主键回退：
SELECT cols FROM "schema"."table" LIMIT 5000
```

- 占位符: `$N`（`$1`, `$2`, ...）
- 标识符引号: `"identifier"`（同 Oracle，双引号）

importer (`internal/transfer/importer/importer.go`) 中 PostgreSQL 使用的 SQL 模式：

```sql
INSERT INTO "schema"."table" ("col1", "col2") VALUES ($1, $2)
TRUNCATE TABLE "schema"."table"
```

---

## 验证说明

以上 SQL 提取自 `internal/metadata/extractor/postgres.go:12-153` 的常量定义及各方法实现。PG 使用 `information_schema` 标准视图 + `pg_catalog` 扩展获取元数据。

> 注意: PostgreSQL 的 `information_schema.columns` 中对 array 类型返回 `ARRAY` 作为 data_type，如需具体元素类型需要额外处理。

## 被复用的数据库类型

| 数据库类型 | 归一化映射 |
|---|---|
| `opengaussdb` | `postgres`（100% 复用，参见 `internal/dialect/opengaussdb/`） |
| `panweidb` | `postgres`（默认） |
| `panweidb-mysql` | `postgres`（通信用 PG 协议） |
| `panweidb-oracle` | `postgres`（通信用 PG 协议，仅 DDL 使用 Oracle 方言） |
