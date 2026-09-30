---
name: owl-migrate
description: 使用 owl-migrate 处理数据库迁移类任务。触发场景：把表/整库从一个数据库迁到另一个（Oracle/MySQL/PostgreSQL/OceanBase/GoldenDB/PanWeiDB/OpenGaussDB/SQLite/DuckDB，以及经 JDBC agent 通道接入的达梦 dm/金仓 Kingbase/TimesTen）、导出表结构/元数据、提取某用户下的表信息、导出表数据（CSV/INSERT SQL/Excel）、生成建表 DDL、把 CSV 导入目标库、生成 INSERT 脚本、分页拉取大表、配置数据源、测试连接、迁移预检、断点续传、在线增量迁移（CDC）、JDBC 连接通道（native/agent/auto）与 owljdbc.profiles 自定义类型注册。当用户提出数据库迁移、导出、导入、表结构提取或数据源配置需求时使用本技能。
---

# owl-migrate 数据库迁移技能

owl-migrate 是本仓库的数据库迁移 CLI（+Web API）。核心模型：**元数据**（从活库或 CSV 读取的表结构信息）→ 按元数据**导出数据 / 生成 DDL / 端到端迁移**。

**先做一件事**：确认二进制可用。仓库内构建产物在 `build/<goos>-<goarch>/owl-migrate*`（`make build` 产出基础版）。方言与构建口味的对应关系见 references/dsn-and-config.md——用户要操作 OceanBase/GoldenDB/PanWeiDB/OpenGaussDB/SQLite 时，基础版二进制会报 unsupported，需先构建对应口味并改用 `owl-migrate-ob` / `-gdb` / `-og` / `-sqlite3` / `-full`。

先跑一次 `owl-migrate version` 自检：它会打印版本、本二进制已链接的驱动与方言（构建口味决定 native 驱动集）。**agent 通道不受构建口味限制**：任意口味二进制都能经 JDBC agent 通道（JVM sidecar）接入达梦 dm、金仓 Kingbase、TimesTen 等没有 Go 原生驱动的库，前提是环境有 Java 和驱动 jar（详见 references/dsn-and-config.md「连接通道」）。

## 第 0 步：信息收集与拷问（先于一切路由）

从用户描述中提取**路由关键要素**：

| # | 要素 | 缺失时 |
|---|---|---|
| 1 | 源库：方言 + DSN（或数据源名） | **必须问** |
| 2 | 目标库：方言 + DSN（"只导出不入库"的任务可无） | 入库类必须问 |
| 3 | 范围：schema/用户、表清单（精确/glob）、整库？ | **必须问** |
| 4 | 产出：迁到目标库 / 数据（CSV\|SQL\|XLSX）/ 结构（元数据\|DDL）/ 脚本 | 模糊时问 |
| 5 | 约束：停机窗口、编码（GBK）、容错要求 | 可给默认并告知 |

**拷问规则**：
- 要素 1/2/3 缺失或指代不明（"那张表""用户表""把数据迁一下"）→ 一次把缺的要素问全再动手，不要挤牙膏式追问，也不要编造连接信息。
- 描述差但意图清楚、只缺非关键细节（输出目录、具体格式）→ 用合理默认值并明确告知，不为小事打断。
- "导出"一词歧义（数据 or 结构）→ 必须澄清，这是最常见的误判点。
- 故障诊断类（"迁移失败了"）→ 先取证：`./output/migration_report.json`、终端输出、失败表名；拿不到证据不猜。
- 用户给了一串连接串但没说干什么 → 问意图，不要擅自开始迁移。

**分类路由**：按要素 4 把请求归入下方七大场景，再按对应 SOP 执行。

## 场景决策表

| 用户意图 | 命令 | 细节 |
|---|---|---|
| **A. 迁移到目标库**（整库或指定表） | `init` 生成配置 → `migrate` | SOP-A |
| **B. 导出表结构/元数据**（某库/某用户/某表的表信息） | `export-metadata`（或 `show-query` 看抽取 SQL） | SOP-B |
| **C. 由元数据生成建表 DDL** | `export ddl` | SOP-C |
| **D. 导出表数据**（CSV / INSERT SQL / Excel） | `export data` / `export insert` / `gen-select` | SOP-D |
| **E. 把 CSV 导入目标库** | `import` | SOP-E |
| **F. 配置与数据源**（生成配置/数据源 CRUD/连接测试/预检/校验） | `init` / `validate` / serve API | SOP-F |
| **G. 在线增量迁移**（不停机） | `online init` → `online sync` | SOP-G |

## SOP 精要

### SOP-A 端到端迁移
1. `owl-migrate init -s <源方言> --source-dsn <DSN> --source-schema <SCHEMA> -t <目标方言> --target-dsn <DSN> -o migrate.yaml`
2. 预检（可选但推荐）：serve 启动后 `POST /api/v1/migrate/preflight`；**预检通过后要继续执行迁移，预检不是终点**。
3. `owl-migrate migrate -c migrate.yaml [--tables S.T1,S.T2]`
- 整库省略 `--tables`；表选择支持 `S.TABLE`/`S.*`/`*.T`/`T_*` glob（详见 references/commands.md「表范围」）。
- 变体：断点续传 `--resume`；只要 SQL 不连目标 `--sql-out <dir>`；单表失败不停 `--continue-on-error`；目标表已建好 `--skip-ddl`。
- 报告：`./output/migration_report.json`。

### SOP-B 元数据提取（表结构/表信息）
1. 确保配置里 `source` 指向目标库（没有就用 `init --scenario export-metadata` 生成）。
2. `owl-migrate export-metadata -c migrate.yaml --scope <范围> --format <csv|xlsx|sql>`
- 范围：`all`（配置的 schema）| `schema:USERA`（某用户全部）| `table:T1,T2`（指定表）| `schema:S:table:T*`。
- 对象过滤 `--objects tables,views,...`（按方言能力校验）。
- 产出：csv=13 张规范表（tables/columns/pk/indexes/fk/views/mviews/sequences/synonyms/triggers/functions/packages/package_bodies）到 `./output/metadata/`；xlsx=单工作簿；sql=仅 oracle 家族（元数据入库留存用）。

### SOP-C 生成 DDL
`owl-migrate export ddl -c migrate.yaml -o ./output/ddl/`（生成 CREATE TABLE+INDEX+VIEW）。常用配置：`ddl.schema_mapping`（SCOTT: public，仅 schema 级）、`ddl.include_drop`、`--no-quote-identifiers`。CSV 离线元数据同样可用（`metadata.type: csv`）。

### SOP-D 数据导出
- 在线导出：`owl-migrate export data -c migrate.yaml --tables USERA.TABLEA --format csv|sql|xlsx -o ./output/data/`
- **条件导出**：`--where 'SCOTT.EMP: deptno=20'`（或配置 `export.filters`）——WHERE 片段按字面下发，执行前有条件 COUNT 门禁（列名/语法错会中止并指名 filter）；`export.filters_check: off` 可跳过。**列投影/改名**：`export.columns.include`（列表顺序=输出顺序）+ `rename`；migrate 自动建表列集与 CSV 一致。**无行数上限**——限量导出走 `gen-select` 或导出后截取，如实告知。
- **在线增量（online）与 filters 互斥**：触发器回放不按条件，`online init` 会硬拒绝。
- 手头有 `{schema}.{table}.csv` → INSERT：`owl-migrate export insert -d <csv目录> --dialect <目标方言> [-n 500] [--truncate]`
- 大表分页拉取脚本：`owl-migrate gen-select -c migrate.yaml --batch-method cursor -n 10000`
- 导出慢：配置 `export.parallel: {enabled: true, max_workers: N}`。

### SOP-E CSV 导入
`owl-migrate import -c migrate.yaml`（数据目录=配置 `import.source_dir`，缺失表自动补建）。常用：
- 导入前清空：`import.target.truncate_before: true`；禁外键约束导完再恢复：`import.target.disable_constraints: true`
- 编码：`import.data_transforms.source_encoding: GBK`
- 清洗：`import.data_transforms` 下 `null_if`（空串转 NULL）、`datetime_format`（紧凑模板 `yyyyMMddHHmmss`/`yyyyMMdd`，可配 `datetime_format_fallback`）、`trim_strings`
- 容错：`import.batch.error_policy: skip_row|stop|log_only`；PG 提速：`import.batch.use_copy: true`

### SOP-F 配置与数据源
- 生成配置：见 SOP-A 第 1 步；纯元数据场景 `--scenario export-metadata`（target 可省）。
- 配置搜索顺序：`-c` > `./migrate.yaml` > `$OWL_MIGRATE_CONFIG` > `~/.owl/migrate/migrate.yaml`；`~/migrate.yaml` 不在搜索路径。
- 校验元数据（CSV 或活库均可）：`owl-migrate validate -c migrate.yaml`。
- **连接通道**：`--channel native|agent|auto`（全局 flag，优先于配置 `source/target.channel`）。`native`=Go 驱动（默认）；`auto`=有原生驱动走 native，否则自动 agent；`agent`=强制走 owljdbc JVM sidecar（需 Java + 驱动 jar，`owl-agent.jar` 缺失自动下载）。新数据库类型用配置 `owljdbc.profiles` 注册免改代码接入（详见 references/dsn-and-config.md）。
- **数据源增删改查/连接测试仅 Web API，CLI 无此命令**：serve 启动后 `POST/GET/PUT/DELETE /api/v1/datasources[/{name}]`、`POST /api/v1/conn/test`（可带 `channel`）；DSN 加密落盘。配置里可用 `source.dsn: "datasource:<名字>"` 引用。请求体见 references/web-api.md。
- 能力探测：serve 端 `GET /api/v1/capabilities`（逐类型 native/agent 可用性、owl-agent.jar 与驱动 jar 探测，含注册类型）。
- REST 起任务与查进度：`POST /migrate`（或 `/export`、`/import`）拿 job id → `GET /jobs/{id}` 轮询（另有 `/jobs/{id}/events`、`/jobs/{id}/ws`）。

### SOP-G 在线增量迁移
1. 全量迁移先行（SOP-A）。
2. `owl-migrate online init --apply --require-key`（装 changelog 表+触发器；不加 `--apply` 只出脚本供人工审核）。
3. `owl-migrate online sync`（持续回放；`--once` 单轮）；`online status` 看积压；`online archive` 归档。
- 容错：`online.sync.on_error: skip|stop|retry`。前提：表有主键。

## 硬约束（必须遵守，不要答错）

1. **无表级重命名**：仅 schema 级映射（`ddl.schema_mapping`），目标表名=源表名。用户要改名 → 说明限制，替代：导入后 RENAME，或预建目标表再 import。
2. **数据源管理仅 Web API**（serve），CLI 没有 datasource add/list 子命令。
3. **存储过程/函数不做语法改写**：functions/packages 只导出元数据与原始定义，转换需人工。
4. **无两库数据比对**能力。
5. **表范围没有排除语法**："排除 xx 表"只能用白名单收敛；配置里的 `ddl.table_filter` 未接线，不要使用。**WHERE 条件导出已支持**（export.filters，v0.7.x），行数上限仍不支持。
6. **目标方言只有 17 个内置**（oracle/postgres/mysql/sqlite3/duckdb/goldendb 系/oceanbase 系/panweidb 系/opengaussdb 系）。`dm`（达梦）、`kingbase`（金仓）、`timesten` 可作为**连接类型**使用（仅 agent 通道；金仓属 postgres 族、达梦/TimesTen 属 oracle 族），但**不能**填 `ddl.target_dialect` 或目标方言下拉——目标端指向兼容的内置方言。除此之外的库（如原生 GaussDB）不支持，明确告知并列清单。

## 深入参考（按需加载）

- `references/commands.md` — 全命令 flags、输出产物路径、表范围 glob 语义
- `references/dsn-and-config.md` — 各方言 DSN 速查、构建口味、migrate.yaml 模板
- `references/web-api.md` — serve 参数、数据源/连接测试/预检/任务 API 请求体
- 回归语料与评测：`evals/owl-migrate/`（corpus.jsonl + run_eval.py）
