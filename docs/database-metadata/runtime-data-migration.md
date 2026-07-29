# 运行时数据迁移 SQL 模式

除了元数据提取，数据迁移（导出/导入）运行时也使用了一系列
数据库特有的 SQL 模式。

## 1. 数据导出 (Exporter)

> 源文件: `internal/transfer/exporter/exporter.go`

### 游标分页查询

基于主键（或行号）的游标分页，用于大数据量导出:

```sql
-- Oracle:
SELECT col1, col2 FROM "SCHEMA"."TABLE" WHERE "PK" > :1 ORDER BY "PK" FETCH NEXT 5000 ROWS ONLY

-- PostgreSQL:
SELECT col1, col2 FROM "schema"."table" WHERE "pk" > $1 ORDER BY "pk" LIMIT 5000

-- MySQL:
SELECT `col1`, `col2` FROM `schema`.`table` WHERE `pk` > ? ORDER BY `pk` LIMIT 5000
```

### 分页语法对照

| 数据库 | LIMIT 语法 | 占位符风格 | 标识符引号 |
|---|---|---|---|
| Oracle | `FETCH NEXT n ROWS ONLY` | `:1`, `:2`, ... | `"identifier"` |
| PostgreSQL | `LIMIT n` | `$1`, `$2`, ... | `"identifier"` |
| MySQL | `LIMIT n` | `?` | `` `identifier` `` |

### 查询列信息

```sql
SELECT * FROM schema.table WHERE 1=0  -- 返回空结果集，仅用于获取列元数据
```

## 2. 数据导入 (Importer)

> 源文件: `internal/transfer/importer/importer.go`

### INSERT 语句

```sql
-- Oracle:
INSERT INTO "SCHEMA"."TABLE" ("COL1", "COL2") VALUES (:1, :2)

-- PostgreSQL:
INSERT INTO "schema"."table" ("col1", "col2") VALUES ($1, $2)

-- MySQL:
INSERT INTO `schema`.`table` (`col1`, `col2`) VALUES (?, ?)
```

### TRUNCATE 语句

```sql
TRUNCATE TABLE "schema"."table"  -- PG / Oracle
TRUNCATE TABLE `schema`.`table`  -- MySQL
```

### Oracle NLS Session 配置

导入前设置日期格式（仅 Oracle）:

```sql
ALTER SESSION SET NLS_DATE_FORMAT = 'YYYY-MM-DD HH24:MI:SS'
ALTER SESSION SET NLS_TIMESTAMP_FORMAT = 'YYYY-MM-DD HH24:MI:SS'
ALTER SESSION SET NLS_TIMESTAMP_TZ_FORMAT = 'YYYY-MM-DD HH24:MI:SS TZH:TZM'
```

## 3. 目标表创建

> 源文件: `internal/cmd/import.go`

### 表存在性检查

```sql
-- PostgreSQL:
SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2

-- MySQL:
SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?
```

### Schema 创建

```sql
CREATE SCHEMA IF NOT EXISTS schema_name
```

## 4. 通信协议对照

| 场景 | Oracle | PostgreSQL | MySQL |
|---|---|---|---|
| 占位符 | `:N` | `$N` | `?` |
| 标识符引号 | `"id"` | `"id"` | `` `id` `` |
| 分页 | `FETCH NEXT n ROWS` | `LIMIT n` | `LIMIT n` |
| `isMySQL()` 判断 | — | — | `mysql`, `goldendb`, `*-mysql` (不含 `panweidb-mysql`) |
| `isOracle()` 判断 | `oracle`, `*-oracle` (不含 `panweidb-oracle`) | — | — |
| `isPostgres()` 判断 | — | `postgres`, `opengaussdb`, `panweidb`, `panweidb-mysql`, `panweidb-oracle` | — |

> 注意: PanWeiDB 的所有变体（包括 `panweidb-mysql` 和 `panweidb-oracle`）
> 都使用 PG 风格的 `$N` 占位符，因为 PanWeiDB 通信用 PG 协议。
