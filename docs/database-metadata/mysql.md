# MySQL 元数据查询方案

> 源文件: `internal/metadata/extractor/mysql.go`
> 目标视图: `information_schema.tables`, `information_schema.columns`, `information_schema.table_constraints`, `information_schema.key_column_usage`, `information_schema.statistics`, `information_schema.referential_constraints`, `information_schema.views`, `information_schema.triggers`
> 功能: `MySQLMetadataQuerier`

查询参数通过 `?` 占位符传入 database（schema）名称。

---

## 1. 表信息 (QueryTables)

```sql
SELECT table_name, engine, table_comment, row_format, table_collation,
    COALESCE(create_options, ''),
    COALESCE(IF(row_format = 'TEMPORARY', 'YES', 'NO'), 'NO') AS temporary
FROM information_schema.tables
WHERE table_schema = ?
    AND table_type = 'BASE TABLE'
ORDER BY table_name
```

- 额外获取 `engine`（InnoDB/MyISAM 等）、`table_comment`、`row_format`、`collation`
- `create_options` 用于检测分区信息（`strings.Contains("PARTITION", ...)`）
- `temporary` 通过 `row_format = 'TEMPORARY'` 判断（实际 MySQL 中 `row_format` 不存储 TEMPORARY，此逻辑可能有误，需验证）
- 每个表还会独立执行一次行数估算查询:

```sql
SELECT table_rows FROM information_schema.tables WHERE table_schema = ? AND table_name = ?
```

## 2. 列信息 (QueryColumns)

```sql
SELECT
    table_name,
    column_name,
    ordinal_position,
    data_type,
    COALESCE(character_maximum_length, 0) AS char_length,
    COALESCE(numeric_precision, 0) AS num_precision,
    COALESCE(numeric_scale, 0) AS num_scale,
    is_nullable,
    COALESCE(column_default, '') AS column_default,
    COALESCE(column_comment, '') AS column_comment,
    COALESCE(extra, '') AS extra,
    COALESCE(character_set_name, '') AS charset,
    COALESCE(collation_name, '') AS collation,
    COALESCE(column_type, '') AS column_type_raw
FROM information_schema.columns
WHERE table_schema = ?
ORDER BY table_name, ordinal_position
```

- `extra` 包含 `auto_increment`、`on update CURRENT_TIMESTAMP` 等额外信息
- `column_type` 是原始类型定义字符串（如 `enum('a','b')`、`varchar(255)`），用于检测 ENUM
- 代码通过 `strings.Contains(extra, "auto_increment")` 检测自增列，设置 `IsIdentity = "YES"`, `IdentityGeneration = "BY DEFAULT"`
- 代码通过 `strings.HasPrefix(column_type, "enum(")` 检测枚举类型

## 3. 主键信息 (QueryPrimaryKeys)

```sql
SELECT
    tc.table_name,
    tc.constraint_name,
    kcu.column_name,
    kcu.ordinal_position
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
    ON tc.constraint_schema = kcu.constraint_schema
    AND tc.constraint_name = kcu.constraint_name
    AND tc.table_name = kcu.table_name
WHERE tc.constraint_type = 'PRIMARY KEY'
    AND tc.table_schema = ?
ORDER BY tc.table_name, kcu.ordinal_position
```

- MySQL 的 JOIN 条件不需要 `constraint_catalog`（与 PG 不同）

## 4. 索引信息 (QueryIndexes)

```sql
SELECT
    table_name,
    index_name,
    CASE WHEN non_unique = 0 THEN 'UNIQUE' ELSE 'NONUNIQUE' END AS uniqueness,
    column_name,
    seq_in_index,
    COALESCE(index_type, 'BTREE') AS index_type,
    COALESCE(expression, '') AS expression
FROM information_schema.statistics
WHERE table_schema = ?
ORDER BY table_name, index_name, seq_in_index
```

- `information_schema.statistics` 是 MySQL 的索引统计视图（相当于 `SHOW INDEX`）
- `non_unique = 0` 表示唯一索引
- `index_type` 返回 `BTREE`、`FULLTEXT`、`HASH` 等
- `expression` 列（MySQL 8.0+ 支持函数索引）
- 注意: `statistics` 视图对每个表的所有索引逐行返回，主键索引也会被包含；需要在消费端区分

## 5. 外键信息 (QueryForeignKeys)

```sql
SELECT
    kcu.table_name,
    kcu.constraint_name,
    kcu.column_name,
    kcu.referenced_table_schema,
    kcu.referenced_table_name,
    kcu.referenced_column_name,
    COALESCE(rc.delete_rule, '') AS delete_rule,
    COALESCE(rc.update_rule, '') AS update_rule
FROM information_schema.key_column_usage kcu
LEFT JOIN information_schema.referential_constraints rc
    ON kcu.constraint_schema = rc.constraint_schema
    AND kcu.constraint_name = rc.constraint_name
WHERE kcu.table_schema = ?
    AND kcu.referenced_table_name IS NOT NULL
ORDER BY kcu.table_name, kcu.constraint_name, kcu.ordinal_position
```

- `kcu.referenced_table_name IS NOT NULL` 过滤出外键（普通列此字段为 NULL）
- `referential_constraints` 提供 `delete_rule` 和 `update_rule`
- 注意: `key_column_usage` 在 MySQL 8.0 中可能返回重复行（需要 DISTINCT）

## 6. 视图信息 (QueryViews)

```sql
SELECT
    v.table_name,
    v.view_definition,
    COALESCE(t.table_comment, '') AS view_comment,
    CASE WHEN v.is_updatable = 'YES' THEN 'YES' ELSE 'NO' END AS is_updatable,
    CASE WHEN v.check_option = 'NONE' THEN '' ELSE COALESCE(v.check_option, '') END AS check_option
FROM information_schema.views v
LEFT JOIN information_schema.tables t
    ON v.table_schema = t.table_schema AND v.table_name = t.table_name
WHERE v.table_schema = ?
ORDER BY v.table_name
```

- 视图注释从 `information_schema.tables.table_comment` 获取
- `check_option` 映射：`'NONE'` → `''`（空字符串）

## 7. 序列信息 (QuerySequences)

```sql
-- MySQL 8.0 没有原生序列对象，返回 (nil, nil)
-- (MySQL 8.0.3+ 有 CREATE SEQUENCE 但实际走的是内部表实现，不在 information_schema 中以标准方式暴露)
```

## 8. 触发器信息 (QueryTriggers)

```sql
SELECT
    trigger_name,
    event_object_table AS table_name,
    action_timing AS trigger_type,
    event_manipulation AS trigger_event,
    action_statement AS trigger_body,
    action_condition AS when_clause,
    '',
    'PLSQL' AS language
FROM information_schema.triggers
WHERE trigger_schema = ?
ORDER BY trigger_name
```

- `action_timing`: `BEFORE` / `AFTER`
- `event_manipulation`: `INSERT` / `UPDATE` / `DELETE`
- MySQL 触发器总是 `FOR EACH ROW`（代码中硬编码）
- MySQL 触发器总是 `ENABLED`（`information_schema.triggers` 不暴露状态）
- `Language` 硬编码为 `"PLSQL"`（尽管 MySQL 使用 SQL 标准语法）

## 9. 同义词 (QuerySynonyms)

```sql
-- MySQL 不支持同义词，返回 (nil, nil)
```

---

## 与运行时数据导出的配合

exporter (`internal/transfer/exporter/exporter.go`) 中 MySQL 使用的 SQL 模式：

```sql
-- 游标分页（首次）：
SELECT `cols` FROM `schema`.`table` ORDER BY `pk_cols` LIMIT 5000

-- 游标分页（后续）：
SELECT `cols` FROM `schema`.`table` WHERE `pk` > ? ORDER BY `pk_cols` LIMIT 5000

-- 无主键回退：
SELECT `cols` FROM `schema`.`table` LIMIT 5000
```

- 占位符: `?`（位置无关）
- 标识符引号: `` `identifier` ``（反引号）
- LIMIT 语法: `LIMIT n`

importer (`internal/transfer/importer/importer.go`) 中 MySQL 使用的 SQL 模式：

```sql
INSERT INTO `schema`.`table` (`col1`, `col2`) VALUES (?, ?)
TRUNCATE TABLE `schema`.`table`
```

---

## 验证说明

以上 SQL 提取自 `internal/metadata/extractor/mysql.go:12-105` 的常量定义及各方法实现。

> 注意: MySQL 的 `information_schema.statistics` 在所有 MySQL 兼容数据库中可用；但 `information_schema.triggers` 在 OceanBase MySQL 模式中可能返回不同的 `action_statement` 格式。

## 被复用的数据库类型

| 数据库类型 | 归一化映射 | 备注 |
|---|---|---|
| `goldendb` | `mysql` | GoldenDB MySQL 模式 |
| `goldendb-mysql` | `mysql` | |
| `oceanbase` | `mysql` | OceanBase MySQL 模式（默认） |
| `oceanbase-mysql` | `mysql` | |
