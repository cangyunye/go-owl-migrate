# Oracle 元数据查询方案

> 源文件: `internal/metadata/extractor/oracle.go`
> 目标视图: `all_tables`, `all_tab_columns`, `all_col_comments`, `all_cons_columns`, `all_constraints`, `all_indexes`, `all_ind_columns`, `all_views`, `all_tab_comments`, `all_sequences`, `all_triggers`, `all_synonyms`
> 功能: `OracleMetadataQuerier`

查询参数通过 `:1` 占位符传入 schema（OWNER），统一使用 `UPPER(:1)`。

---

## 1. 表信息 (QueryTables)

```sql
SELECT table_name, tablespace_name, num_rows
FROM all_tables
WHERE owner = UPPER(:1)
ORDER BY table_name
```

- `tablespace_name` 可为 NULL（非分区表）
- `num_rows` 为统计信息中的估算行数，可为 NULL

## 2. 列信息 (QueryColumns)

```sql
SELECT
    table_name,
    column_name,
    column_id AS ordinal_position,
    data_type,
    COALESCE(data_length, 0) AS data_length,
    COALESCE(data_precision, 0) AS data_precision,
    COALESCE(data_scale, 0) AS data_scale,
    nullable,
    data_default,
    NVL(comments, '') AS comments,
    COALESCE(char_used, '') AS char_used,
    COALESCE(character_set_name, '') AS charset,
    COALESCE(collation, '') AS collation,
    identity_column
FROM all_tab_columns c
LEFT JOIN all_col_comments USING (owner, table_name, column_name)
WHERE owner = UPPER(:1)
ORDER BY table_name, column_id
```

- `nullable` 返回 `Y`/`N`，代码内映射为 `YES`/`NO`
- `identity_column` 返回 `YES`/`NO`，为 `YES` 时设置 `IdentityGeneration = "ALWAYS"`
- `char_used` 表示 CHAR 语义使用 `CHAR` 还是 `BYTE`
- `data_length` 对于 VARCHAR2 表示字节数，对于 CHAR 表示字符数与 `char_used` 有关

## 3. 主键信息 (QueryPrimaryKeys)

```sql
SELECT
    cc.table_name,
    cc.constraint_name,
    cc.column_name,
    cc.position
FROM all_cons_columns cc
JOIN all_constraints c
    ON cc.owner = c.owner
    AND cc.constraint_name = c.constraint_name
    AND cc.table_name = c.table_name
WHERE c.constraint_type = 'P'
    AND cc.owner = UPPER(:1)
ORDER BY cc.table_name, cc.constraint_name, cc.position
```

- `constraint_type = 'P'` 表示 PRIMARY KEY
- `position` 是列在复合主键中的序号

## 4. 索引信息 (QueryIndexes)

```sql
SELECT
    i.table_name,
    i.index_name,
    CASE WHEN i.uniqueness = 'UNIQUE' THEN 'UNIQUE' ELSE 'NONUNIQUE' END AS uniqueness,
    ic.column_name,
    ic.column_position,
    CASE WHEN i.index_type = 'BITMAP' THEN 'BITMAP' ELSE 'BTREE' END AS index_type
FROM all_indexes i
JOIN all_ind_columns ic
    ON i.owner = ic.index_owner
    AND i.index_name = ic.index_name
    AND i.table_name = ic.table_name
WHERE i.owner = UPPER(:1)
ORDER BY i.table_name, i.index_name, ic.column_position
```

- 注意 JOIN 条件：`index_owner` 而非 `owner`
- `uniqueness` 返回 `UNIQUE`/`NONUNIQUE`
- `index_type` 简化为 `BITMAP`/`BTREE`（其他类型如 FUNCTION-BASED NORMAL 被归为 BTREE）

## 5. 外键信息 (QueryForeignKeys)

```sql
SELECT
    cc.table_name,
    cc.constraint_name,
    cc.column_name,
    c.r_owner AS ref_owner,
    (SELECT table_name FROM all_constraints WHERE owner = c.r_owner AND constraint_name = c.r_constraint_name) AS ref_table,
    (SELECT column_name FROM all_cons_columns WHERE owner = c.r_owner AND constraint_name = c.r_constraint_name AND position = cc.position) AS ref_column,
    COALESCE(c.delete_rule, 'NO ACTION') AS delete_rule,
    COALESCE(c.deferrable, 'NOT DEFERRABLE') AS deferrable
FROM all_cons_columns cc
JOIN all_constraints c
    ON cc.owner = c.owner
    AND cc.constraint_name = c.constraint_name
    AND cc.table_name = c.table_name
WHERE c.constraint_type = 'R'
    AND cc.owner = UPPER(:1)
ORDER BY cc.table_name, cc.constraint_name, cc.position
```

- `constraint_type = 'R'` 表示 REFERENTIAL（外键）
- 引用表和引用列通过子查询从 `all_constraints`/`all_cons_columns` 中根据 `r_owner` + `r_constraint_name` 查询
- 子查询使用 `position = cc.position` 确保复合外键的多列匹配
- 不查询 `update_rule`（Oracle 不支持 UPDATE CASCADE）

## 6. 视图信息 (QueryViews)

```sql
SELECT
    v.view_name,
    v.text AS view_definition,
    NVL(t.comments, '') AS view_comment,
    'NO' AS is_updatable,
    '' AS check_option,
    v.owner
FROM all_views v
LEFT JOIN all_tab_comments t
    ON v.owner = t.owner AND v.view_name = t.table_name
WHERE v.owner = UPPER(:1)
ORDER BY v.view_name
```

- `v.text` 是视图定义文本（CLOB）
- `is_updatable` 固定返回 `'NO'`（Oracle 不直接通过系统视图暴露此信息）
- `check_option` 固定返回空字符串
- 视图注释从 `all_tab_comments` 获取（`table_name = view_name`）

## 7. 序列信息 (QuerySequences)

```sql
SELECT
    sequence_name,
    COALESCE(increment_by, 1) AS increment_by,
    COALESCE(min_value, 1) AS min_value,
    COALESCE(max_value, 9999999999999999999999999999) AS max_value,
    CASE WHEN cycle_flag = 'Y' THEN 'YES' ELSE 'NO' END AS cycle_flag,
    COALESCE(cache_size, 20) AS cache_size,
    COALESCE(last_number, 0) AS last_number,
    COALESCE(order_flag, 'NO') AS order_flag
FROM all_sequences
WHERE sequence_owner = UPPER(:1)
ORDER BY sequence_name
```

- `last_number` 是序列缓存中最后一个值的记录（不等于当前实际值）
- `cycle_flag` 映射为 `YES`/`NO`
- `StartValue` 硬编码为 1（Oracle 不直接从 `all_sequences` 暴露 start value）

## 8. 触发器信息 (QueryTriggers)

```sql
SELECT
    trigger_name,
    table_owner,
    table_name,
    trigger_type,
    triggering_event,
    trigger_body,
    status,
    CASE WHEN trigger_type LIKE '%EACH ROW%' THEN 'ROW' ELSE 'STATEMENT' END AS for_each,
    COALESCE(when_clause, '') AS when_clause,
    COALESCE(description, '') AS description
FROM all_triggers
WHERE owner = UPPER(:1)
ORDER BY trigger_name
```

- `trigger_type` 格式如 `BEFORE EACH ROW`、`AFTER STATEMENT`、`INSTEAD OF`
- `triggering_event` 格式如 `INSERT OR UPDATE OR DELETE`
- `for_each` 通过 `LIKE '%EACH ROW%'` 解析
- `Language` 硬编码为 `"PLSQL"`

## 9. 同义词信息 (QuerySynonyms)

```sql
SELECT
    synonym_name,
    owner,
    table_owner,
    table_name,
    CASE WHEN owner = 'PUBLIC' THEN 'YES' ELSE 'NO' END AS is_public
FROM all_synonyms
WHERE owner = UPPER(:1)
    OR table_owner = UPPER(:1)
ORDER BY synonym_name
```

- `owner = 'PUBLIC'` 表示公有同义词
- WHERE 条件同时匹配 owner 和 table_owner，确保覆盖所有相关同义词

---

## 与运行时数据导出的配合

exporter (`internal/transfer/exporter/exporter.go`) 中 Oracle 使用的 SQL 模式：

```sql
-- 游标分页（首次）：
SELECT cols FROM schema.table ORDER BY pk_cols FETCH NEXT 5000 ROWS ONLY

-- 游标分页（后续）：
SELECT cols FROM schema.table WHERE pk > :1 ORDER BY pk_cols FETCH NEXT 5000 ROWS ONLY

-- 无主键回退：
SELECT cols FROM schema.table FETCH NEXT 5000 ROWS ONLY
```

- 占位符: `:N`（`:1`, `:2`, ...）
- 标识符引号: `"identifier"`
- LIMIT 语法: `FETCH NEXT n ROWS ONLY`（Oracle 12c+）

importer (`internal/transfer/importer/importer.go`) 中 Oracle 使用的 SQL 模式：

```sql
INSERT INTO schema.table (col1, col2) VALUES (:1, :2)
TRUNCATE TABLE schema.table
ALTER SESSION SET NLS_DATE_FORMAT = 'YYYY-MM-DD HH24:MI:SS'
ALTER SESSION SET NLS_TIMESTAMP_FORMAT = 'YYYY-MM-DD HH24:MI:SS'
ALTER SESSION SET NLS_TIMESTAMP_TZ_FORMAT = 'YYYY-MM-DD HH24:MI:SS TZH:TZM'
```

---

## 验证说明

以上 SQL 提取自 `internal/metadata/extractor/oracle.go:13-135` 的常量定义及各方法实现。使用 `all_*` 字典视图，需确保连接用户对目标 schema 的 `all_*` 视图有 SELECT 权限。

> 注意: `all_*` 视图只返回当前用户有权限访问的对象。如果需要查看所有对象，应使用 `dba_*` 视图。当前实现使用 `all_*`。

## 被复用的数据库类型

| 数据库类型 | 归一化映射 |
|---|---|
| `oceanbase` | `mysql`（默认） |
| `oceanbase-oracle` | `oracle` |
| `goldendb` | `mysql`（默认） |
| `goldendb-oracle` | `oracle` |
| `panweidb-oracle` | `oracle` |
