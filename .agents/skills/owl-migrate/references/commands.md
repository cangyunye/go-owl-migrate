# owl-migrate 命令参考

配置解析顺序：`-c` flag > `./migrate.yaml` > `$OWL_MIGRATE_CONFIG` > `~/.owl/migrate/migrate.yaml`。建议始终显式 `-c`。

## 全局 flag（所有子命令）

| flag | 说明 |
|---|---|
| `--channel native\|agent\|auto` | 连接通道覆盖（优先于配置 `source/target.channel`）。agent=owljdbc JVM sidecar，见 references/dsn-and-config.md「连接通道」 |
| `--jars-dir <dir>` | owl-agent.jar 与驱动 jar 检索目录（agent 通道；默认当前工作目录） |

## version — 能力自检

`owl-migrate version`：打印版本、commit、本二进制已链接的 database/sql 驱动、编译进的方言、缺失驱动对应需要的 build tag。**不连库**，用于回答"这个二进制能不能连 XX"。

## 表范围选择（migrate / export data / import / export ddl 通用）

- CLI `--tables`：逗号分隔，**覆盖**配置 `export.tables.include`（export ddl 也读它；import 仅 flag）。
- 模式语义（大小写不敏感）：`*`=全量；`SCOTT.EMP`=精确；`SCOTT.*`=整 schema；`*.EMP`=任意 owner 的同名表；`T_*`/`EM?`=表名 glob。
- **没有排除语法**。"排除 log_ 开头"→白名单收敛。
- `ddl.table_filter`（include/exclude）当前**未接线**，不要使用。
- 显式点名精确表优先于一切 glob。

## init — 生成 YAML 配置

无 flag 交互式；带 flag 非交互。非交互要求：`--scenario` 给出 且（`--target-type` 给出 或 场景可默认 target）且 source 输入齐全。

| flag | 说明 |
|---|---|
| `-s/--source-type` | 源方言（`--metadata-type database` 时必填） |
| `--source-dsn` / `--source-schema` | 源连接串 / schema（嵌入库可省 schema） |
| `-t/--target-type` / `--target-dsn` / `--target-schema` | 目标；DDL-only 场景 target-type 可省（默认 postgres）；target-schema 默认=源 schema |
| `-m/--metadata-type` | database(默认) / csv / xlsx |
| `-S/--scenario` | migrate / export / import / export-ddl / export-insert / gen-select / export-metadata / validate / full |
| `-o` | 输出路径，默认 ./migrate.yaml（带中文注释） |

## validate — 校验元数据

`owl-migrate validate -c migrate.yaml`。CSV 或活库元数据均可；报告表/视图数量。

## export-metadata — 活库元数据提取

```
owl-migrate export-metadata -c migrate.yaml -o ./output/metadata/ --format csv --scope all
```

| flag | 值 |
|---|---|
| `--format` | csv（默认）\| xlsx \| sql（仅 oracle 家族：生成针对系统视图的 INSERT 脚本） |
| `--scope` | all \| schema:NAME[,NAME] \| table:GLOB[,GLOB] \| schema:NAME:table:GLOB[,GLOB] |
| `--objects` | tables, columns, primary_keys, indexes, foreign_keys, views, mviews, sequences, synonyms, triggers, functions, packages, package_bodies（按方言能力校验，不支持即报错并列清单） |
| `-o` | csv 模式=目录（默认 ./output/metadata/）；xlsx/sql=文件路径 |

CSV 模式产出 13 张规范表：tables / columns / primary_keys / indexes / foreign_keys / views / mviews / sequences / synonyms / triggers / functions / packages / package_bodies。xlsx 为单工作簿（tables/columns/primary_keys 三个 sheet）。零命中时报错并列出可用 schema 与相近表名建议。

## show-query — 查看元数据抽取 SQL

`owl-migrate show-query <方言> [对象类型]`。对象类型：tables, columns, pk, indexes, fk, views, sequences, triggers, synonyms。只读展示不执行。

## export ddl — 生成 DDL

`owl-migrate export ddl -c migrate.yaml [-o ./output/ddl/] [--no-quote-identifiers]`

生成 CREATE TABLE + CREATE INDEX + CREATE VIEW（以元数据为准）。相关配置：`ddl.target_dialect`（默认继承 target.type）、`ddl.include_drop`、`ddl.include_comments`、`ddl.schema_mapping`（仅 schema 级，如 `SCOTT: public`）、`ddl.no_quote_identifiers`、`ddl.identity_to_serial`、`ddl.type_overrides`、`ddl.partition.migrate`。表范围读 `export.tables.include`。

## export data — 导出表数据

```
在线:  owl-migrate export data -c migrate.yaml [--tables S.T1,...] [--where 'S.T: where片段'] --format csv|sql|xlsx|tsv -o ./output/data/
离线:  owl-migrate export data -d <csv目录> -o ./output/sql/ --format sql     # CSV→SQL
       owl-migrate export data --xlsx <文件> -o ./output/xlsx/ --format xlsx
```

- 输出命名：`{schema}.{table}.csv` / `{schema}.{table}.insert.sql` / `{schema}.{table}.tsv`（tsv 固定 tab 分隔 + `\n` 换行，引号转义与 null 表示沿用 `export.csv.*` 配置）。
- 条件导出：`--where 'SCOTT.EMP: deptno=20'`（逗号分隔多条；配置 `export.filters`，`filters_check: count|off`）——执行前条件 COUNT 门禁，列名/语法错中止并指名 filter；片段禁 `;`、注释、绑定占位符，且须确定性（keyset 分页）。
- 列投影/改名：`export.columns.include`（**列表顺序=输出顺序**）+ `rename`；migrate 自动建表与 CSV 列集一致；PK 列不可被投影丢弃。
- 迁移同样支持 `migrate --where`；`online init` 遇 filters 硬拒绝；报告 `filtered: true` 标注。
- 并行：配置 `export.parallel: {enabled: true, max_workers: N}`。
- CSV 细节可配：`export.csv.{delimiter,...}`。

## export insert — 离线 CSV→INSERT SQL（零配置）

`owl-migrate export insert -d ./data/ --dialect postgres [-n 100] [--truncate] [-o ./output/insert/]`

`-n` 每批行数（默认 100）；`--truncate` 脚本开头加 TRUNCATE；CSV 文件名必须是 `{schema}.{table}.csv`。

## gen-select — 生成分页 SELECT

`owl-migrate gen-select -c migrate.yaml [--batch-method cursor|offset] [-n 5000] [-o ./output/select/]`

## import — CSV→目标库

`owl-migrate import -c migrate.yaml [--tables ...]`。数据目录=配置 `import.source_dir`；缺失目标表自动补建（ensure）。

| 配置段 | 键 |
|---|---|
| import.target | truncate_before / disable_constraints / disable_triggers / drop_indexes |
| import.batch | commit_interval / error_policy(skip_row\|stop\|log_only) / max_errors_before_stop / use_copy（PG COPY 提速） |
| import.data_transforms | source_encoding(如 GBK) / datetime_format(紧凑模板 yyyyMMddHHmmss\|yyyyMMdd，另有 datetime_format_fallback 列表) / trim_strings / null_if |
| import.parallel | max_workers / respect_foreign_keys |

每表输出 `✅ schema.table: actual/expected rows`。

## migrate — 端到端迁移

`owl-migrate migrate -c migrate.yaml [--tables ...] [flags]`

流程：加载元数据 → 源库导出 CSV（中转目录）→ 目标建表 → 导入 → 报告。

| flag | 说明 |
|---|---|
| `--temp-dir` | CSV 中转目录，默认 ./output/temp/ |
| `--tables` | 表选择（覆盖 export.tables.include） |
| `--resume` | 断点续传（状态 <temp-dir>/migrate_progress.json） |
| `--continue-on-error` | 单表失败不停整体 |
| `--skip-ddl` | 目标表已建好，只导数据 |
| `--sql-out <dir>` | 离线模式：只产 INSERT SQL 不连目标库 |
| `-r/--report` | 报告路径，默认 ./output/migration_report.json |

## online — 触发器 CDC 在线增量

| 子命令 | 说明 |
|---|---|
| `online init` | 生成 changelog 表+触发器 DDL；`--apply` 直接在源库执行（不加则只出 `online_cdc_NNN.sql` 脚本）；`--tables`；`--require-key` 要求主键；`-o` 脚本目录 |
| `online sync` | 轮询 changelog 回放目标库；`--once` 单轮 |
| `online status` | 检查点与 pending/done/failed 计数 |
| `online init-runner` | 生成 file-batch 目标的 runner 脚本 |
| `online archive` | done 批次压缩 tar.gz（默认 ./online/archive/） |

容错配置：`online.sync.on_error: skip|stop|retry`、`online.sync.error_table`。changelog 表前缀 `owl_chg_`。

## serve — Web 控制台

`owl-migrate serve [--port 8080] [--host 127.0.0.1] [--token <t>]`。绑定非环回地址必须 `--token`。其余见 references/web-api.md。
