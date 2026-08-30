# QA：陌生数据库数据导出

> 记录日期：2026-08-30。内容经源码核实（`internal/dbconn`、`internal/metadata/extractor`、
> `internal/registry`、`internal/cmd/export_data.go`、`internal/transfer/exporter/writer.go`），
> 对应版本 `0.4.0`。

## 场景

拿到一个不熟悉的数据库，需要确认：

1. 导出数据需要提供哪些东西（连接方式、元数据、映射关系）？
2. 除 DDL 外，能否直接导出 DELETE/INSERT 语句或 CSV？
3. 具体怎么做，文档在哪里？

## 结论

工具导出数据只认两样输入：

- **结构元数据（SchemaModel）**：表/列/主键等 — 来自在线抽取（`metadata.type: database`）
  或 CSV/XLSX 文件（`metadata.type: csv | xlsx`）；
- **一个可用的实时连接**：`export data` 必须对源库执行 SELECT（除非走离线 CSV→SQL 路径，
  见 Q6）。

连接方式、元数据查询、映射关系分别对应 `source.*`、`metadata.*`、`ddl.*`/外部映射文件三类配置。

## Q1: 导出数据需要提供哪些东西？

| 输入 | 配置位置 | 说明 |
|---|---|---|
| 源连接 | `source.type` / `source.dsn` / `source.schema` | 驱动 + 方言行为 + 连接串 |
| 结构元数据 | `metadata.type: database` | 在线抽取，方言硬编码（见 Q3） |
| 结构元数据（离线） | `metadata.type: csv` / `xlsx` | 用户按 `docs/csv-format.md` 提供 |
| 表过滤 | `export.tables.include` | 默认 `["*"]` 全表 |
| 输出格式 | `export.format` | `csv`（默认）/ `sql` / `xlsx` |

## Q2: 连接方式与自定义连接串

- `source.type` **必须是内置集合之一**（`internal/dbconn/dbconn.go` 的 `knownTypes`：
  `mysql`/`mariadb`/`postgres`/`postgresql`/`oracle`/`sqlite3`/`duckdb`/
  `opengaussdb`/`panweidb`/`panweidb-mysql`/`panweidb-oracle`/
  `goldendb-mysql`/`goldendb-oracle`/`oceanbase-mysql`/`oceanbase-oracle`），
  type 决定驱动与方言行为（分页语法、占位符、标识符引号）。
- DSN **原样透传**给驱动（`dbconn.Open`：`dsn = cfg.DSN`），仅两处后处理：
  - Oracle 系自动注入 `PREFETCH_ROWS=25`、`LOB FETCH=POST`（LOB 流式读取）；
  - PG 系自动注入 `search_path=<schema>`。
- **没有任意 "custom" type**。新增工具不认识的数据类型 = 改代码注册
  （`registry.Register` 注册方言、`extractor.Register` 注册元数据查询器，均为编译期；
  仅 sqlite3/duckdb 通过 build tag 可选启用）。
- 线协议只支持四族：MySQL wire / PG wire / Oracle TNS / 嵌入式（sqlite3、duckdb）。
- 各方言 DSN 格式见 `docs/config.md`「Connection Strings (DSN)」一节。

## Q3: 元数据表与查询方式

在线抽取的查询 SQL 按方言硬编码（`internal/metadata/extractor/`，`normalizeDBType` 归并复合方言）：

| 类型 | 元数据来源 |
|---|---|
| oracle 系 | `all_*` 字典视图（9 大类对象） |
| postgres 系 | `information_schema` + `pg_catalog` |
| mysql 系 | `information_schema` |
| sqlite3 | `sqlite_master` + PRAGMA |
| duckdb | `information_schema` + `duckdb_indexes()` |

- 直接查看每条查询 SQL：`owl-migrate show-query <dialect> [object-type]`，
  例如 `owl-migrate show-query oracle tables`（`internal/cmd/showquery.go`）。
- 完整 SQL 参考：`docs/database-metadata/{oracle,postgresql,mysql,duckdb,sqlite3,compound-dialects}.md`。
- 用户提供的"元数据表和查询方式"若不在内置集合 → 落成 CSV 元数据文件（见 Q4），
  工具不做自定义查询 SQL 注入。

## Q4: 元数据类型表（metadata 来源与 CSV 文件清单）

- `metadata.type` 取值：`database` | `csv` | `xlsx`。
- CSV 元数据文件清单与逐列定义见 `docs/csv-format.md`：

| 文件 | 必需 | 说明 |
|---|---|---|
| `tables.csv` | ✓ | 表/视图/物化视图定义 |
| `columns.csv` | ✓ | 列定义（数据类型、长度、精度、可空等） |
| `primary_keys.csv` | — | 主键约束 |
| `indexes.csv` | — | 索引定义 |
| `foreign_keys.csv` | — | 外键约束 |
| `sequences.csv` | — | 序列定义 |
| `triggers.csv` | — | 触发器 |
| `functions.csv` | — | 函数/存储过程 |
| `views.csv` / `mviews.csv` | — | 视图 / 物化视图 SQL |
| `synonyms.csv` | — | 同义词（Oracle） |
| `packages.csv` / `package_bodies.csv` | — | 包 / 包体（Oracle） |

- 对已支持的库，可用 `owl-migrate export-metadata -c cfg.yaml -o ./metadata/ --format csv|xlsx|sql`
  先把在线元数据 dump 成 CSV，再离线复用（`internal/cmd/exportmetadata.go`）。

## Q5: 映射关系

- 内置 LogicalType IR 做跨方言类型转换（NUMBER↔DECIMAL↔INTEGER、VARCHAR2↔VARCHAR、
  CLOB↔TEXT↔LONGTEXT 等），见 `docs/dialect-mapping.md`。
- 覆盖手段：
  - `ddl.type_overrides`（`%l/%p/%s` 占位符，优先级最高）；
  - 外部 YAML 映射文件（`exact_mappings` / `parameterized` / `semantic_overrides`）。
- 注意：**数据导出本身不依赖类型映射**（只读值、BLOB hex 编码）；映射只影响 DDL 生成
  与目标建表。

## Q6: 除 DDL 外，能否直接导出 DELETE/INSERT 语句或 CSV？

| 需求 | 支持 | 方式 |
|---|---|---|
| CSV | ✅ | `export data` 默认 `--format csv`（`export.format: csv`）→ `{schema}.{table}.csv` |
| INSERT SQL（在线） | ✅ | `export data --format sql` → `{schema}.{table}.insert.sql`（每批 BEGIN/COMMIT，MySQL 无事务包裹） |
| INSERT SQL（离线） | ✅ | `export insert -d <csv目录> --dialect <mysql\|postgres\|oracle> [--truncate]` |
| DELETE 语句 | ❌ 无批量导出 | `DELETE FROM` 只出现在 CDC 增量回放（`internal/cdc/replay.go`，按主键删，属 online 增量迁移）与 importer TRUNCATE 失败回退 |
| 清空 + 插入 | ✅ | `export insert --truncate` 生成 `TRUNCATE TABLE` 前缀；`import.target.truncate_before` 导入前清空（TRUNCATE 被 FK 阻断时自动回退 DELETE） |
| XLSX | ✅ | `export.format: xlsx` |

`internal/transfer/exporter/writer.go` 的 sqlWriter 只写 `BEGIN; INSERT INTO … VALUES …; COMMIT;`，
无 DELETE/TRUNCATE 生成 — 需要逐行 `DELETE` 脚本时当前版本没有现成命令。

## 操作流程（陌生库接入 checklist）

```
① 判断线协议：MySQL / PG / Oracle / 嵌入式 四族之一？
   ├─ 是 → source.type 选对应族，DSN 写自定义连接串
   │       metadata.type: database 在线抽取（或按 csv-format.md 手供 CSV）
   │       owl-migrate export data -c cfg.yaml --format csv|sql
   └─ 否 → 完全离线路径：
           自己导出表数据为 {schema}.{table}.csv（首行列名）
           owl-migrate export insert -d ./data/ --dialect <mysql|postgres|oracle> [--truncate]
           （元数据可选：metadata.type: csv + tables.csv/columns.csv → 还可用 export ddl 出建表脚本）
```

## 相关文档

| 文档 | 内容 |
|---|---|
| `docs/config.md` | 全部配置、各方言连接串格式、metadata 类型 |
| `docs/cli-commands.md` | `export ddl/data/insert`、`gen-select`、`import`、`migrate --sql-out` |
| `docs/csv-format.md` | 元数据 CSV 文件/字段（元数据类型表） |
| `docs/dialect-mapping.md` | 类型映射（映射关系） |
| `docs/database-metadata/index.md` | 各库元数据查询（元数据表和查询方式） |
| `docs/migration-pipeline.md` | 导出/导入运行时行为（分页、CSV 格式、批插入） |
| `docs/getting-started.md` | 快速上手与离线工作流 |

## 源码位置（验证依据）

| 文件 | 职责 |
|---|---|
| `internal/dbconn/dbconn.go` | `knownTypes`、驱动选择、DSN 透传与后处理 |
| `internal/metadata/extractor/extractor.go` | 提取器注册表、`normalizeDBType` |
| `internal/registry/registry.go` | 方言注册表（编译期） |
| `internal/cmd/export_data.go` | 在线/离线导出入口 |
| `internal/transfer/exporter/writer.go` | CSV/SQL/XLSX 写出（sqlWriter 仅 INSERT） |
| `internal/generator/insert.go` | 离线 INSERT 生成（含 `--truncate`） |
| `internal/cmd/showquery.go` | `show-query` 打印元数据查询 SQL |
| `internal/cmd/exportmetadata.go` | 元数据 dump 为 CSV/XLSX/SQL |
