# OB 双租户开发环境 e2e 启动手册（e2edev + cmd 迁移矩阵）

> 依赖实库：OceanBase Oracle 租户（oratest）、OceanBase MySQL 租户（obmysql）、
> 独立 MySQL、PostgreSQL（可选）。设计详见
> `docs/plans/2026-09-04-ob-dialect-matrix-e2e.md`。
> 连接凭据**不入库**：放 `testdata/db/.local-dev.env`（git 忽略）或导出同名 OS 环境变量。

## 1. 环境变量（OS 导出优先；缺省读 testdata/db/.local-dev.env）

| 变量 | 示例 | 用途 |
|---|---|---|
| `OWL_E2E_OB_ORACLE_DSN` | `oceanbase-oracle://sys@oratest:REDACTED@127.0.0.1:2881/` | OB Oracle 引导连接（sys） |
| `OWL_E2E_OB_MYSQL_DSN` | `root@obmysql:REDACTED@tcp(127.0.0.1:2881)/` | OB MySQL 租户 |
| `OWL_E2E_MYSQL_DSN` | `root:REDACTED@tcp(172.20.208.1:3306)/` | 独立 MySQL |
| `OWL_E2E_PG_DSN` | `postgres://superme:REDACTED@172.20.208.1:5432/test?connect_timeout=5` | PostgreSQL（本机不可达时自动 skip） |
| `OWL_E2E_DEV_PW` | `REDACTED` | 引导新建用户/库统一口令 |

口令编码规则：URL 型 DSN 中 `@`→`%40`、`#`→`%23`、`%`→`%25`；mysql-wire DSN
（`user:pass@tcp(...)`）口令含 `@` 原样可解析（driver 按末位 `@` 切分）。

## 2. 运行

```bash
# OB Oracle/MySQL 字典族与源侧抽取探针（Phase A-D，含 OB-MySQL SEQUENCE 验证）
go test -tags e2e -v ./internal/e2edev/ -count=1

# OB-Oracle 为源的出口：→ PG / MySQL / export-metadata(CSV 13 文件·SQL·objects 过滤)
go test -tags e2e -v ./internal/cmd/ -run 'TestDevMigrate_M7_ObOracleToPostgres|TestDevMigrate_M8_ObOracleToMySQL|TestExportMetadata_Live_ObOracle' -count=1

# H1 serve 层真库 e2e（HTTP API：load(database)/tables/详情大小写/ddl 生成/export csv+sql）
go test -tags e2e -v ./internal/server/serve/ -run 'TestH1_LiveServe' -count=1

# CLI 命令本体 × 真库冒烟（export data / export ddl / gen-select / validate）
go test -tags e2e -v ./internal/cmd/ -run 'TestCLISmoke' -count=1

# 离线全量回归（无实库依赖）
go test ./internal/... -count=1
```

用例对未配置/不可达的连接自动 skip（PASS 但显示 SKIP），不失败。

## 3. 种子与清理（用例自动完成，幂等可重跑）
- OB Oracle：重建用户 `MIGSRC/MIGTGT/MIG_MYSQL/MIG_PG_HR/MIG_PG_FIN/MIG_OBM`
  （口令 `OWL_E2E_DEV_PW`），MIGSRC 建 EMP/DEPT(+分区表 PART_SALES/视图/序列/
  同义词/触发器/函数/包)；
- OB MySQL：建库 `migsrc_obm`/`migtgt_m2` 等；独立 MySQL：`migsrc_mysql`；
- PG（需超管可达）：role `hr_owner/fin_owner` + schema `src_hr/src_fin`
  （owner≠连接用户，多用户 schema 专项）。

清理：`DROP USER ... CASCADE` / `DROP DATABASE ...` 由各用例先行执行，重跑安全。

## 4. 已实测结论（2026-09-04，OB 4.4.2.2）
- OB Oracle 字典 13 族全通（含 synonym/trigger/function/package、`DBMS_METADATA`
  可用、分区信息可重建）——HANDOFF §三.2 复核项通过；
- 迁移 M1(mysql→OB-Oracle)、M2(mysql→OB-MySQL)、M3(pg 多用户 src_hr→OB-Oracle)、
  M4(pg src_fin→OB-MySQL)、M5(OB-MySQL→OB-Oracle)、M6(OB-Oracle 内部
  MIGSRC→MIGTGT)、M7(OB-Oracle→PG)、M8(OB-Oracle→MySQL) 全绿
  （PG 需 sslmode=disable）；OB-Oracle 为源的通路已与 PG/MySQL 打通；
- export-metadata 实库场景（OB-Oracle，`TestExportMetadata_Live_ObOracle`）：
  CSV 13 文件全出（含 MIGSRC 表/SEQ_EMP/V_EMP 内容断言）、`--format sql`
  产出 dba_tables/dba_tab_columns INSERT、`--objects views,sequences` 过滤
  只出 2 文件 —— 三个子场景均绿；
- CLI 命令本体冒烟（`TestCLISmoke_*`，mysql + OB-Oracle）：`export data` 落
  CSV 含表头+行、`export ddl` 落文件含映射建表、`gen-select` 每表一 SELECT、
  `validate` 真库模型 0 错误 —— 命令级出口补齐；
- 新落地：OB-MySQL SEQUENCE querier（`oceanbase.__all_sequence_object`，
  Capabilities 放开 sequences，含单测）；oracle 序列大数(10^28-1)溢出修复；
  init 默认推荐 schema 映射 `recommendSchemaMapping` + golden 单测。

## 5. H2 进展与浏览器人工项
- 已完成（2026-09-05）：serve 作业端点×真库全链路（POST /api/v1/migrate → master →
  真实 worker 子进程，mysql migsrc_mysql → OB-Oracle MIG_UI 2/2+3/3，SUCCESS；SPA 壳 /、/ui 200）。
  完整步骤见 `docs/scenario-cli-web-matrix.md` §migrate。
- 剩余半自动/人工：
  1. serve `/export`、`/import` 作业端点按同一流程各跑一次（与 /migrate 同通路，未执行）；
  2. 浏览器像素级点检（导出页填写、迁移任务进度 WebSocket、ZIP 下载）需带 GUI 环境人工执行，
     清单执行时另附。
