# Scenario × CLI × Web（serve）双端矩阵审计

> 更新：2026-09-05。范围：`owl-migrate init --scenario` 全部场景（外加 online 系列说明）。
> 判定原则：P1 收编后，CLI 与 serve 调用**同一 `service.*` 实现**（同源）；下表按四层给证据：
> ① 离线/单测 · ② CLI 命令本体 × 真库 · ③ serve API × 真库（H1）· ④ serve 作业端点 × 真库全链路（master→worker→真库）。
> 真库 = 开发机 `.local-dev.env` 四库（OB-Oracle 4.4.2.2 / OB-MySQL / MySQL 8.4 / PG）。

## 图例
✅ 已通过（对应层）· ◻️ 未做/部分 · — 不适用（设计使然）

| scenario | CLI 命令 | serve 端点 | ①离线/单测 | ②CLI×真库 | ③serve API×真库 | ④serve 作业×真库 |
|---|---|---|---|---|---|---|
| migrate | `migrate` | `POST /api/v1/migrate`（master→worker） | ✅ 生命周期/取消/ws | ✅ M1–M8 | —（走作业端点） | ✅ **H2**（mysql→OB-Oracle 2/2+3/3，`job-1ebe6100`） |
| export-ddl | `export ddl` | `POST /ddl/generate` | ✅ 生成/下载 | ✅ CLI smoke（mysql+OB） | ✅ H1（映射建表断言） | — |
| gen-select | `gen-select` | `POST /select/generate` | ✅ 单测 | ✅ CLI smoke | ✅ H1（每表 SELECT） | — |
| export(data) | `export data` | `POST /export`（job）/`/export/offline` | ✅ 桩/下载 | ✅ CLI smoke（CSV 落盘断言） | — | ◻️ 可跑（同 migrate 流程，未执行） |
| export-insert | `export insert` | `POST /insert/generate`+`/insert/tables` | ✅ 同源离线（CSV 驱动） | —（CSV 语义无需真库） | — | — |
| import | `import` | `POST /import`（job） | ✅ 桩 | ✅ pipeline 内 importer 真库 | — | ◻️ 可跑（目标 truncate 语义注意） |
| export-metadata | `export-metadata` | `POST /metadata/export` | ✅ 13 文件规范测试 | ✅ 实库 CSV/SQL/objects | ✅ H1（mysql csv+sql 门 / OB-Oracle sql） | — |
| validate | `validate` | `GET /metadata/validate` | ✅ csv 校验 | ✅ CLI smoke（真库模型 0 错误） | ✅ H1 | — |
| full | `init -S full`（模板） | `/scenarios/{name}/build` | ✅ 场景构建 | — | — | — |
| online 系列 | `online init/sync/archive/status` | **无 serve 接口** | ✅ 单测 | ◻️（需源端 CDC 触发器，专题外） | — | — |

## 每场景证据与复现命令（真库 e2e）

### migrate
- CLI 真库：`go test -tags "e2e ob" ./internal/cmd/ -run TestDevMigrate`（M1–M8：mysql/pg/obmysql/OB-Oracle 为源 × OB-Oracle/OB-MySQL/PG/MySQL 为目标，行数与值保真全绿；OB 方言按 build tag 编译控制）。
- serve 作业全链路（H2，2026-09-05 实测）：
  1. `go build -o /tmp/owl-migrate ./cmd/migrate`
  2. `OWL_MIGRATE_HOME=/tmp/owlhome /tmp/owl-migrate serve --host 127.0.0.1 --port 18991 --temp-dir /tmp/owltmp`
  3. `PUT /api/v1/config`（JSON 顶层配置，migrate 场景，源 mysql `migsrc_mysql` → 目标 OB-Oracle `MIG_UI`，含推荐映射）
  4. `POST /api/v1/migrate` `{}` → 201 `{"job_id":…,"status":"running"}`
  5. 轮询 job store（`/tmp/owlhome/owl-migrate.db` 的 `jobs.status`）→ `completed`
  6. 验证：`SELECT COUNT(*) FROM "dept"`=2、`"emp"` 中 empno=7369→SMITH（引号小写表）。
- worker 即真实 `owl-migrate migrate` 子进程（execSpawner re-exec 本二进制），日志含
  `Created table MIG_UI.dept/emp`、`✅ MIG_UI.dept: 2/2 rows`、`Status: SUCCESS`。

### export ddl / gen-select / export data / validate
- 命令本体真库冒烟：`go test -tags "e2e ob" ./internal/cmd/ -run TestCLISmoke`
  （export data 落 CSV 含表头+行；export ddl 落映射建表；gen-select 每表一 SELECT；validate 0 错误）。
- serve API 真库：`go test -tags "e2e ob" ./internal/server/serve/ -run TestH1_LiveServe`
  （/metadata/load(database) → /metadata/tables → 详情大小写 → /ddl/generate → /select/generate → /metadata/validate → /metadata/export csv+sql 能力门）。

### export-metadata（CLI 与 serve 双端实库）
- CLI：`go test -tags "e2e ob" ./internal/cmd/ -run TestExportMetadata_Live_ObOracle`
  （CSV 13 文件含 MIGSRC 表/SEQ_EMP/V_EMP；--format sql 产出 dba_tables/dba_tab_columns；--objects views,sequences 只出 2 文件）。
- serve：`go test -tags "e2e ob" ./internal/server/serve/ -run TestH1_LiveServe_ObOracleSQLExport`。

## 已确认缺口的性质
1. serve `/export` 与 `/import` 作业端点：与 `/migrate` 完全同一条 master→worker 通路
   （execSpawner 分别以 `export data` / `import` 子命令 re-exec），H2 已证明该通路可用；
   仅差把这两个 job type 按第 4 节步骤各跑一次并核对产物/行数（import 需目标 truncate 语义注意）。
2. 浏览器像素级点检（导出页联动、任务页进度、ZIP 下载）：本机无 headless chromium，
   SPA 壳（`/`、`/ui`）已确认 200 且含标题；真机浏览器人工清单见 e2e-ob-dev.md §5。

## 分层测试资产（文件）
- 离线：`internal/server/serve/*_test.go`（lifecycle/生成/下载/configs/scenarios）、
  `internal/service/*_test.go`、`internal/metadata/extractor`、`internal/cmd/init_mapping_test.go`
- CLI×真库：`internal/cmd/e2e_devmigrate_test.go`（M1–M8）、`internal/cmd/e2e_cli_smoke_test.go`、
  `internal/cmd/e2e_exportmetadata_test.go`
- serve API×真库：`internal/server/serve/h1_live_test.go`
- 抽取/字典/SEQUENCE：`internal/e2edev/`
- 文档：`docs/e2e-ob-dev.md`（runbook）、`docs/plans/2026-09-04-ob-dialect-matrix-e2e.md`
