# SQLite3 元数据查询方案

> 源文件: `internal/metadata/extractor/sqlite3.go`
> 构建标签: `//go:build sqlite3`
> 目标: `sqlite_master` 系统表 + `PRAGMA` 命令
> 功能: `SQLite3MetadataQuerier`

SQLite3 没有 schema 概念（所有对象在同一个 namespace），`schema` 参数被忽略。

**重要**: PRAGMA 命令不支持 `?` 占位符，需要使用 `fmt.Sprintf` 拼接表名。

---

## 1. 表信息 (QueryTables)

```sql
SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name
```

- `sqlite_master` 是 SQLite3 的系统目录表
- 排除 `sqlite_*` 系统表（`sqlite_sequence`、`sqlite_stat1` 等）

## 2. 列信息 (QueryColumns)

```sql
PRAGMA table_info('<table_name>')
```

返回列: `cid, name, type, notnull, dflt_value, pk`

- 遍历所有表逐一执行 PRAGMA
- `notnull = 1` 映射为 `nullable = "NO"`
- `dflt_value` 使用 `sql.NullString` 处理可能的 NULL
- `ordinal_position` 使用 `cid + 1`
- 不获取 `data_length`/`data_precision`/`data_scale`（SQLite3 无此概念）

## 3. 主键信息 (QueryPrimaryKeys)

```sql
PRAGMA table_info('<table_name>')
```

- 与列信息使用相同的 PRAGMA，筛选 `pk > 0` 的行
- `constraint_name` 硬编码为 `"pk_" + tableName`
- `ordinal_position` 使用 `pk` 值（PRAGMA 返回的 `pk` 即为主键列在复合主键中的序号）

## 4. 索引信息 (QueryIndexes)

```sql
PRAGMA index_list('<table_name>')
```

返回列: `seq, name, unique, origin, partial`

- `origin = 'pk'` 的跳过（自动生成的主键索引）
- `unique` 值为 `"1"`/`"0"` 或 `"yes"`/`"no"`（不同 SQLite3 版本格式不同）

对每个索引再执行:

```sql
PRAGMA index_info('<index_name>')
```

返回列: `seqno, cid, name`

- 遍历每个表的每个索引
- `index_type` 硬编码为 `"BTREE"`（SQLite3 仅支持 BTREE）

## 5. 外键信息 (QueryForeignKeys)

```sql
PRAGMA foreign_key_list('<table_name>')
```

返回列: `id, seq, table, from, to, on_update, on_delete, match`

- 遍历每个表逐一执行 PRAGMA
- `ref_schema` 硬编码为 `"main"`
- `constraint_name` 未设置（SQLite3 不暴露外键约束名）

## 6. 视图信息 (QueryViews)

```sql
SELECT name, sql FROM sqlite_master WHERE type='view' ORDER BY name
```

- `sql` 列包含完整的 `CREATE VIEW` 语句

## 7. 触发器信息 (QueryTriggers)

```sql
SELECT name, sql, tbl_name FROM sqlite_master WHERE type='trigger' ORDER BY name
```

- `sql` 列包含完整的 `CREATE TRIGGER` 语句
- 触发器类型和事件通过 `parseTriggerInfo()` 函数解析 SQL 文本

```go
// parseTriggerInfo 解析 CREATE TRIGGER 语句提取 trigger_type 和 trigger_event
// 示例: "CREATE TRIGGER ... BEFORE INSERT ON ..." → ("BEFORE", "INSERT")
```

- `status` 硬编码为 `"ENABLED"`
- `for_each` 硬编码为 `"ROW"`
- `Language` 硬编码为 `"SQL"`

## 8. 序列信息 (QuerySequences)

```sql
-- SQLite3 不支持序列，返回 (nil, nil)
```

## 9. 同义词 (QuerySynonyms)

```sql
-- SQLite3 不支持同义词，返回 (nil, nil)
```

---

## 验证说明

以上 SQL/PRAGMA 提取自 `internal/metadata/extractor/sqlite3.go` 的各方法实现。

> 注意:
> - SQLite3 的 PRAGMA 不保证跨版本兼容性
> - `parseTriggerInfo()` 是简单的字符串搜索，可能无法处理复杂或格式不标准的触发定义
> - 索引查询的双层循环（table → index_list → index_info）在大 schema 中性能较差
> - `PRAGMA foreign_key_list` 不返回约束名，`constraint_name` 字段为空

## 被复用的数据库类型

SQLite3 提取器当前是独立的，未作为其他数据库类型的默认提取器。
