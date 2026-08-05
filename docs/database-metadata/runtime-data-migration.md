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

> 源文件: `internal/cmd/tableddl.go`、`internal/cmd/import.go`

建表语句通过 dialect 系统生成（`buildCreateTableViaDialect`）。跨方言时列类型经
`LogicalType` IR 转换（源 `ToLogicalType` → 目标 `FromLogicalType`）；`type_overrides`
优先级最高；同方言或未知源方言时为裸类型补全长度/精度限定。

### 表存在性检查

```sql
-- PostgreSQL / OpenGaussDB / PanWeiDB:
SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2

-- MySQL / GoldenDB / OceanBase-MySQL:
SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?

-- Oracle / OceanBase-Oracle:
SELECT COUNT(*) FROM all_tables WHERE owner = UPPER(:1) AND table_name = UPPER(:2)

-- SQLite3:
SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?

-- DuckDB:
SELECT COUNT(*) FROM duckdb_tables() WHERE schema_name = ? AND table_name = ?
```

### Schema 创建

仅 PostgreSQL 族目标执行：

```sql
CREATE SCHEMA IF NOT EXISTS "schema_name"
```

## 4. 通信协议对照

| 场景 | Oracle | PostgreSQL | MySQL |
|---|---|---|---|
| 占位符 | `:N` | `$N` | `?` |
| 标识符引号 | `"id"` | `"id"` | `` `id` `` |
| 分页 | `FETCH NEXT n ROWS`（11g 自动回退 ROWNUM 包装） | `LIMIT n` | `LIMIT n` |
| `isMySQL()` 判断 | — | — | `mysql`, `goldendb`, `*-mysql` (不含 `panweidb-mysql`) |
| `isOracle()` 判断 | `oracle`, `*-oracle` (不含 `panweidb-oracle`) | — | — |
| `isPostgres()` 判断 | — | `postgres`, `opengaussdb`, `panweidb`, `panweidb-mysql`, `panweidb-oracle` | — |

> 注意: PanWeiDB 的所有变体（包括 `panweidb-mysql` 和 `panweidb-oracle`）
> 都使用 PG 风格的 `$N` 占位符，因为 PanWeiDB 通信用 PG 协议。

## 5. 元数据在线抽取覆盖

| 对象 | Oracle | PostgreSQL | MySQL |
|---|---|---|---|
| 表（含注释、临时表标志） | `all_tables` + `all_tab_comments` | `information_schema.tables` + `obj_description`（含分区父表检测 `relkind='p'`） | `information_schema.tables` |
| 列（含 identity 起始/步长） | `all_tab_columns` + `all_tab_identity_cols` + `all_sequences` | `information_schema.columns` + `pg_get_serial_sequence`/`pg_sequences` | `information_schema.columns` |
| 序列（START WITH = last_number，避免迁移后主键冲突） | `all_sequences` | `pg_sequences` | —（OB MySQL 模式由方言生成） |
| 分区（重建 `PARTITION BY` 文本） | `all_part_tables` + `all_part_key_columns` + `all_tab_partitions` | `pg_get_partkeydef` | `information_schema.partitions` |
| 函数/存储过程 | `all_objects` + `DBMS_METADATA.GET_DDL`（回退 `all_source`） | `pg_proc` + `pg_get_functiondef`（PG11+ `prokind`，旧版回退） | `information_schema.routines` |
| 物化视图 | `all_mviews` | `pg_matviews` | — |
| 包 / 包体 | `DBMS_METADATA.GET_DDL('PACKAGE'/'PACKAGE_BODY')`（回退 `all_source`） | — | — |
| 触发器语言 | PLSQL | PLPGSQL 等（`pg_trigger`） | SQL |

> Oracle 注意：`''` 在 Oracle 中即 NULL，所有注释类字段通过
> `sql.NullString` 扫描；`NVL(x, '')` 不产生空串。
> 分区重建为尽力而为：INTERVAL 子句与子分区不重建。

## 6. 数据导入引擎

- MySQL 族 / PostgreSQL：多行 `INSERT … VALUES`，占位符按方言编号
  （PG 的 `$N` 跨行连续递增；`?` 族重复），批大小 = `min(commit_interval, 65535/列数)`
- Oracle（TNS）：预编译单行语句复用
- 批失败 → `ROLLBACK TO SAVEPOINT` 后逐行抢救，只丢真正坏的行
  （PostgreSQL 语句级错误中止事务，savepoint 是必需的）
- 占位符超限错误触发批二分重试
- `import.batch.use_copy: true`：PG 族目标启用 `COPY` 快速通道
  （all-or-nothing，失败自动回退批量 INSERT 引擎）
