# owl-migrate 命令参考（技能内核底稿）

数据库迁移工具。核心概念：元数据（表结构等信息，可从活库或 CSV 读取）→ 按元数据导出数据 / 生成 DDL / 迁移到目标库。

## 第 0 步：信息收集与澄清（先于一切路由）

从用户描述中提取**路由关键要素**：

1. 源库：类型（方言）+ 连接串 DSN（或数据源名）
2. 目标库：类型 + DSN（仅"生成 SQL/导出"类任务可无目标库）
3. 范围：哪个 schema/用户、哪些表（精确名/glob）、还是整库
4. 产出：迁移到库 / 导出数据（CSV|INSERT SQL|XLSX）/ 导出结构（元数据|DDL）/ 生成脚本
5. 约束：是否停机、编码（GBK）、错误容忍度

**何时必须拷问**：要素 1/2/3 缺失或指代不明（"那张表""用户表""迁移失败了"）——不问清不动手，一次把缺的要素问全，不要挤牙膏。**何时可用默认**：输出目录、CSV 默认格式、--require-key 之类可选 flag——给出默认值并明确告知即可，不要为小事打断用户。**故障诊断类**先要证据（migration_report.json / 日志 / 终端输出），不要凭空猜。

## 二进制与方言

- 命令名 `owl-migrate`。基础构建只含 oracle/postgres/mysql。
- 复合方言按构建口味编译：OceanBase 系需 `make build/ob`（产物 owl-migrate-ob）；OpenGaussDB/PanWeiDB 系需 `og`；GoldenDB 系需 `gdb`；全量 `make build/full`（owl-migrate-full）；SQLite3 需 `make build/sqlite3`（owl-migrate-sqlite3）。方言未编译时报 unsupported，先构建对应口味。
- 有效方言名：oracle、postgres(postgresql)、mysql(mariadb)、sqlite3、duckdb、goldendb(-mysql/-oracle)、oceanbase(-mysql/-oracle)、panweidb(-mysql/-oracle)、opengaussdb(-mysql/-oracle)。其余（如 Kingbase、GaussDB 原生）不支持。
- 配置解析顺序：`-c` flag > `./migrate.yaml` > 环境变量 `$OWL_MIGRATE_CONFIG` > `~/.owl/migrate/migrate.yaml`。

## DSN 速查

- oracle: `oracle://user:pass@host:1521/service_name`
- mysql/goldendb-mysql: `user:pass@tcp(host:3306)/db?charset=utf8mb4`
- oceanbase-mysql: `user@tenant:pass@tcp(host:2881)/db`
- oceanbase-oracle: 直连 `oceanbase-oracle://user@tenant:pass@host:2881/db`；OBProxy `oceanbase-oracle://user@tenant#cluster:pass@host:2883/db`（#集群名 紧跟租户，端口 2883）
- postgres/opengaussdb/panweidb 系: `host=... port=5432 user=... password=... dbname=... sslmode=disable`
- sqlite3/duckdb: `/path/to/db`（嵌入库，无 schema）

## 表范围选择（真实机制）

- CLI `--tables`（export data / migrate / import）逗号分隔，**覆盖**配置 `export.tables.include`；支持 `SCOTT.EMP` 精确、`SCOTT.*` 整 schema、`*.EMP` 任意 owner、`T_*` glob、`*` 全量；大小写不敏感。
- **没有排除语法**：要"排除 log_ 开头的表"只能用白名单收敛（列出要的）。
- 配置里的 `ddl.table_filter`（include/exclude）**当前未接线**，不要使用。

## 命令

### owl-migrate init — 生成 YAML 配置
非交互要求：给 `--scenario` 且（给了 `--target-type` 或该场景可默认 target）且 source 输入齐全。
flags：`-s/--source-type`、`--source-dsn`、`--source-schema`、`-t/--target-type`（可省，DDL-only 场景默认 postgres）、`--target-dsn`、`--target-schema`（默认=源 schema）、`-m/--metadata-type`(database|csv|xlsx)、`-S/--scenario`(migrate|export|import|export-ddl|export-insert|gen-select|export-metadata|validate|full)、`-o`(默认 ./migrate.yaml)。

### owl-migrate validate — 校验元数据（CSV 或活库），`-c` 指定配置

### owl-migrate export-metadata — 从活库提取元数据
- `--format`：csv（默认，13 张规范表：tables/columns/primary_keys/indexes/foreign_keys/views/mviews/sequences/synonyms/triggers/functions/packages/package_bodies）| xlsx（单工作簿 3 sheet）| sql（生成针对 oracle 系统视图的 INSERT 脚本，仅 oracle 家族，用于元数据入库留存）
- `--scope`：all | schema:NAME | table:GLOB[,GLOB] | schema:NAME:table:GLOB[,GLOB]
- `--objects`：逗号分隔对象类型（tables,views,sequences,...），按方言能力校验，不支持会报错
- `-o` 默认 ./output/metadata/

### owl-migrate show-query <方言> [对象类型] — 只读展示元数据抽取 SQL（tables/columns/pk/indexes/fk/views/sequences/triggers/synonyms）

### owl-migrate export ddl — 由元数据（活库或 CSV）生成 CREATE TABLE + INDEX + VIEW
`-o` 默认 ./output/ddl/；`--no-quote-identifiers`。表范围用 export.tables.include。相关配置：ddl.target_dialect（默认继承 target.type）、ddl.include_drop、ddl.schema_mapping（如 SCOTT: public，仅 schema 级）、ddl.no_quote_identifiers。

### owl-migrate export data — 导出表数据
- 在线：`-c` 配置；`--tables`；`--format` csv(默认，{schema}.{table}.csv)|sql({schema}.{table}.insert.sql)|xlsx；`-o` 默认 ./output/data/
- 离线：`-d <csv目录>` 或 `--xlsx <文件>`
- 并行：配置 export.parallel {enabled, max_workers}

### owl-migrate export insert — 离线 CSV→INSERT SQL（零配置可用）
`-d <csv目录>`（文件名 {schema}.{table}.csv）、`--dialect`、`-n` 批大小(默认100)、`--truncate`、`-o` 默认 ./output/insert/

### owl-migrate gen-select — 生成分页 SELECT 脚本
`--batch-method` cursor|offset、`-n` 每批行数、`-o` 默认 ./output/select/

### owl-migrate import — CSV→目标库（缺失表自动补建）
数据目录取配置 import.source_dir；`--tables` 可选。配置：import.target.{truncate_before,disable_constraints,disable_triggers,drop_indexes}、import.batch.{use_copy(PG COPY 提速),error_policy(skip_row|stop|log_only),max_errors_before_stop}、import.data_transforms.{source_encoding(GBK 等),datetime_format(紧凑模板 yyyyMMddHHmmss/yyyyMMdd,另有 datetime_format_fallback 列表),trim_strings,null_if}。

### owl-migrate migrate — 端到端迁移：源库导出 CSV→目标建表→导入→报告
`--tables`、`--temp-dir`(默认 ./output/temp/)、`--resume`（断点续传，状态 <temp-dir>/migrate_progress.json）、`--continue-on-error`、`--skip-ddl`（目标表已建好只导数据）、`--sql-out <dir>`（只产 INSERT SQL 不连目标库）、`-r/--report`(默认 ./output/migration_report.json)

### owl-migrate online — 触发器 CDC 在线增量迁移
- `online init`：装 changelog 表+触发器；`--apply` 直接在源库执行（不加则只出 online_cdc_NNN.sql 脚本供人工审核）；`--tables`；`--require-key`（要求主键）
- `online sync`：轮询 changelog 回放目标库；`--once` 单轮
- `online status` 看检查点与积压 / `online init-runner` / `online archive` 归档 done 批次
- 配置 online.sync.on_error: skip|stop|retry、error_table

### owl-migrate serve — Web 控制台 + REST API
`--port`(8080)、`--host`(127.0.0.1)、`--token`（绑定非环回地址时必填）。
API（前缀 /api/v1）：
- 数据源：POST/GET/PUT/DELETE `/datasources[/{name}]`，body {name,type,schema,dsn,remark}；DSN AES-256 加密存 ~/.owl/migrate/datasources/
- 连接测试：POST `/conn/test` body {type,dsn,schema,connect_timeout}
- 预检：POST `/migrate/preflight`（只读，返回 {ok,checks,warnings}）
- 任务：POST `/migrate`|`/export`|`/import` 启动，GET `/jobs`、`/jobs/{id}`、`/jobs/{id}/events`、`/jobs/{id}/ws` 查进度
- 其余：config/metadata/scenarios/ddl|select|insert/generate 等

## 硬约束

1. **无表级重命名**：只有 schema 级映射（ddl.schema_mapping），目标表名=源表名。改名需求→导入后 RENAME 或预建目标表再 import。
2. **数据源管理仅 Web API**（serve），CLI 无对应命令；配置文件可用 `source.dsn: "datasource:<名字>"` 引用（serve 端解析）。
3. **过程逻辑不改写**：functions/packages 只导出元数据与原始定义，不做存储过程语法转换。
4. **无数据比对**：没有两个库数据差异对比命令。
5. 复合方言/嵌入式方言需对应构建口味二进制，用错会报 unsupported。
