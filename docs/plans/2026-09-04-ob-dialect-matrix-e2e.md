# OB 双租户方言矩阵 e2e 验证计划（2026-09-04）

> 状态：设计稿（待实施）
> 环境：仅开发机（WSL）。连接信息与口令见 `testdata/db/.local-dev.env`（git 忽略，**禁止提交**）。
> 版本事实（2026-09-04 实测）：OB Oracle 租户 4.4.2.2；OB MySQL 租户 5.7.25-OceanBase-v4.4.2.2；独立 MySQL 8.4.0；PG 待 Go 驱动探活。
> 目标：mysql+pg 作为源的 docker e2e 仓库已覆盖 → 本计划把验证主方向转为 **"其他数据库 → OB MySQL 租户 / OB Oracle 租户"**，并要求覆盖全部当前子命令、PG 多用户 schema、serve/网页自检，产出与 `e2eob` 同构的**环境变量门控启动方式 + 启动文档**。

## 1. 范围与矩阵

### 1.1 连接（dev-only，统一口令见本地 `testdata/db/.local-dev.env` 的 `OWL_E2E_DEV_PW`）
| 标识 | 连接 | repo dbType | 用途 |
|---|---|---|---|
| OB-Oracle | `oceanbase-oracle://sys@oratest:…@127.0.0.1:2881/oratest` | oceanbase-oracle | 主目标；MIGSRC→MIGTGT 内部迁移 |
| OB-MySQL | `root:…@tcp(127.0.0.1:2881)/obmysql` | oceanbase-mysql | 目标；源（→OB-Oracle） |
| MySQL | `root:…@tcp(172.20.208.1:3306)/` | mysql | 源 |
| PG | `postgres://superme:…@172.20.208.1:5432/test` | postgres | 源（多用户 schema 专项） |

### 1.2 迁移主线（源 × 目标）
| 主线 | 源 → 目标 | 备注 |
|---|---|---|
| M1 | mysql `migsrc_mysql` → OB-Oracle `MIG_MYSQL` | 跨族 mysql→oracle |
| M2 | mysql `migsrc_mysql` → OB-MySQL `migsrc_mysql` | 同族 |
| M3 | pg `src_hr`(+`src_fin`) → OB-Oracle `MIG_PG_HR`/`MIG_PG_FIN` | 跨族 + **多用户 schema 多映射** |
| M4 | pg `src_hr` → OB-MySQL | 跨族 pg→mysql |
| M5 | OB-MySQL `migsrc_obm` → OB-Oracle `MIG_OBM` | 含 sequence 探查（P6-2，已实现 querier） |
| M6 | OB-Oracle 内部 MIGSRC → MIGTGT | 同族同租户新 schema（HANDOFF §三.2 复核项） |
| M7 | OB-Oracle `MIGSRC` → PostgreSQL | OB-Oracle 为源 → PG（2026-09-04 补） |
| M8 | OB-Oracle `MIGSRC` → MySQL | OB-Oracle 为源 → MySQL（2026-09-04 补） |
| E1 | OB-Oracle `export-metadata` CSV 13 文件 / SQL(dba_*) / `--objects` 过滤 | 实库场景（2026-09-04 补） |

### 1.3 显式排除（本轮不纳入 DB e2e）
- `online`（CDC 触发器增量）：依赖源端 changelog/触发器与 OB Oracle 触发器语义，独立专题。
- goldendb / opengauss / duckdb / sqlite / panweidb：无实库连接 → 仅离线单测。

## 2. 命名与凭据约定（统一、可重复、幂等）
- 新建用户/角色口令统一取 `OWL_E2E_DEV_PW`（真实值仅写进 `.local-dev.env` 与测试引导，不硬编码在断言/文档）。
- OB-Oracle（sys@oratest 下建）：user `MIGSRC`（fixture：EMP/DEPT + PK/FK/索引/视图/序列/同义词/触发器/函数/包，后三项视 4.x 实测放宽）、`MIGTGT`、`MIG_MYSQL`、`MIG_PG_HR`、`MIG_PG_FIN`、`MIG_OBM`；缺省 tablespace/默认表空间即可。
- OB-MySQL：库 `migsrc_obm`（+表），目标库同 mysql 主线（同库名重建）。
- MySQL 独立：库 `migsrc_mysql`。
- PG：role `hr_owner`/`fin_owner`（LOGIN，口令同上）各拥有 schema `src_hr`/`src_fin`（`CREATE SCHEMA … AUTHORIZATION …`），连接用户 `superme` 为超管；验证"连接用户 ≠ schema 属主"的抽取/映射语义。
- 全部 fixture 幂等：先 DROP（IF EXISTS / CASCADE）再建，可重跑。

## 3. 测试载体：`internal/e2edev`（新增，`//go:build e2e`）
与 `e2eob` 同构：
- **环境变量门控**：`OWL_E2E_OB_ORACLE_DSN` / `OWL_E2E_OB_MYSQL_DSN` / `OWL_E2E_MYSQL_DSN` / `OWL_E2E_PG_DSN`（及各自 SCHEMA/USER 覆盖变量）；未设时回退读 `testdata/db/.local-dev.env`（KEY=VALUE/export 行解析）；仍缺 → 对应用例 skip 并计入报告。
- 统一报告 `output/e2e/dev_matrix_report.json`（按 e2eob 的 checkResult/report 结构）。
- 辅助：`connect(t, type, dsn)`（ping 失败 skip）、幂等 fixture bootstrap/drop（驱动直连，无 docker）、seed 常量内联于 `fixture_*.go`。
- 启动方式（文档见 §7）：`export OWL_E2E_OB_ORACLE_DSN=…` 或直接 `go test -tags e2e -v ./internal/e2edev/`（自动读 dotenv）。

## 4. 用例矩阵（按阶段）

### Phase A — 连接与探活（无种子依赖）
A1 四连接 open+ping；A2 OB-Oracle compat_mode=oracle；A3 `v$version` banner 落报告（4.4.2.2）；A4 OB-MySQL `version()`；A5 PG role/schema 授权引导幂等创建（hr_owner/fin_owner/src_hr/src_fin）并校验属主；A6 MySQL 建库建表引导。

### Phase B — OB-Oracle 元数据查询测通（种子=MIGSRC）
B1 `extractor.Extract` 全字典族逐项断言：tables/all_tab_columns(含 NULL collation 分支、identity)/all_constraints+all_cons_columns（PK/FK）/all_indexes/views/mviews/synonyms/triggers/functions/packages；B2 `DBMS_METADATA.GET_DDL` 探针（函数/包体抽取依赖；4.x 若不可用记 gap 并走降级断言）；B3 分区表字典 + 分区重建 clause 复核（HANDOFF §三.2）；B4 能力矩阵对照：`Capabilities(oceanbase-oracle)` ∩ 实测对象族 = 全量；B5 引号/owner 大小写（`"MIGSRC"."EMP"`）与 P6 A2 单表 include 语义在实库复核。

### Phase C — 源侧抽取测通
C1 mysql `migsrc_mysql` 全字典族（含视图/函数/触发器，无序列按 Capabilities）；C2 PG `src_hr`/`src_fin` 双 schema 抽取（schema 过滤按配置 schema，验证属主≠连接用户时信息可得）；C3 OB-MySQL `migsrc_obm` 抽取（mysql base）；C4 与各 `Capabilities` 对照。

### Phase D — OB-MySQL SEQUENCE 探查（P6-2，只探不实现，除非评审要求落地）
D1 系统字典/视图探查（如 `oceanbase` 库、`information_schema`、`SELECT … FROM oceanbase.__all_sequence_object` 候选），D2 3.x/4.x 差异记录，D3 结论写报告：可用查询 → 排期实现 querier + 放开 Capabilities；不可用 → 记 gap。

### Phase E — 子命令全覆盖（每条至少在一个主线组合上断言）
| 子命令 | 覆盖方式 |
|---|---|
| `init` | 非交互 `--source-type/--target-type/…` 生成 yaml；断言含推荐 `ddl.schema_mapping`（见 §5 增强）；golden 结构 |
| `validate` | 对 export-metadata 产物 CSV 目录 validate（0 错误） |
| `export-metadata` | 每个源 × `--format csv`（13 文件全列断言，复用 csv 规范测试）、`xlsx`、`sql`（仅 oracle 族源） |
| `export ddl` | 源→目标方言 DDL 生成（每对象文件 + owner 分组 + 引号保真） |
| `export data` | 源数据 → CSV（行数/值抽查） |
| `export insert` | CSV → INSERT（缺目录指引路径已在单测） |
| `gen-select` | 源 metadata → 分页 SELECT（引用与 DDL 一致） |
| `import` | CSV → OB 目标库（NULL/unicode/小数/时间/bool 保真） |
| `migrate` | M1–M6 主线端到端（抽取→DDL→导出→导入→计数） |
| `show-query` | 各方言对象查询输出非空（无 DB） |
| `serve` | §6 网页端自检 |

### Phase F — 迁移主线细节（M1–M6）
每主线：F1 metadata 载入计数与类型记录；F2 目标 DDL（schema_mapping 生效、owner 引号、LogicalType 跨族转换、能力外对象按 ADR-004 报错）；F3 数据行数相等 + 值保真抽查；F4 视图/序列/函数按 Capabilities 迁移；F5 幂等重跑（drop 重建）；F6 生成/保存"高兼容 migrate.yaml"（含推荐映射，供复跑与人工审阅）。

### Phase G — init 推荐映射增强（代码 + 单测）
- 需求：确定源/目标类型与 schema 后，init 生成的 yaml **默认带推荐 schema/user 映射**。
- 规则表（单测 golden）：
  - pg schema `X`（属主 role 任意）→ OB-Oracle/MySQL 目标 user/db 推荐 `X`（小写安全化）；
  - mysql db `X` → OB-Oracle 推荐 user `X`（大写化按 OB 约束）；→ OB-MySQL 同 `X`；
  - OB-Oracle schema `X` → 同租户目标 user 推荐 `X`；
  - oracle/pg/mysql → postgres 保持 `public` 惯例（既有行为不破坏）。
- 落点：`internal/cmd/init.go` 非交互写 yaml 处按源/目标组合填 `DDL.SchemaMapping` 默认 + 注释可改；交互场景默认值同步。

### Phase H — serve/网页端自检（先自动，后手动）
H1 serve httptest：`/metadata/load`(csv)+`/metadata/tables`+详情（大小写）、`/metadata/validate`、`/ddl/generate`、`/select/generate`、`/insert/generate`、`/metadata/export`（objects/scope/能力报错）、`/migrate` 走 M1 最小集——**H1 尽量自动化**（httptest + 真库 DSN 通过 serve 配置注入）。
H2 浏览器人工项（留到最后，我给出逐项操作清单）：OB 源/目标 DSN 页内填写、导出页对象勾选联动、迁移任务进度页、ZIP 下载。H2 需要你手动配合时再叫你。

## 5. 产物
1. `internal/e2edev/`：env 门控 harness + 各 Phase 测试文件 + `fixture_*.go`（幂等引导/种子）。
2. `configs/` 或 `testdata/db/e2e/`：各主线"高兼容 migrate.yaml"模板（占位 DSN，无凭据；运行期由 init 按 env 生成实际文件到临时目录）。
3. init 推荐映射增强 + 单测。
4. 启动文档（§7，可并入本文件或独立 `docs/e2e-ob-dev.md`）。

## 6. 验收
- `go test ./internal/... -count=1`（无 e2e tag）保持全绿（离线回归基线）。
- `go test -tags e2e -v ./internal/e2edev/` 输出 `output/e2e/dev_matrix_report.json`，M1–M6 全 pass；缺 env 时逐项 skip 不失败。
- 单元测试：init 映射规则 golden；任何实库暴露的查询/类型缺口回补 sqlmock/单测。

## 7. 启动方式（runbook）
前置：本机可达 4 库；`testdata/db/.local-dev.env` 存在（或导出等价环境变量）。
```bash
# 全量（自动读 .local-dev.env；也可逐项 export OWL_E2E_* 覆盖）
go test -tags e2e -v ./internal/e2edev/ -count=1
# 单 Phase/用例
go test -tags e2e -v ./internal/e2edev/ -run 'TestPhaseA|TestM1_MysqlToObOracle' -count=1
# 报告
cat output/e2e/dev_matrix_report.json
# 手工引导先行（仅建对象，幂等；也可由 A5/A6/B 用例自动完成）
go test -tags e2e -run 'TestFixtureBootstrap' ./internal/e2edev/
```
清理：各用例自带 drop/teardown；整租户清理命令见 fixture 注释（不删除 MIGSRC 外的预置对象）。

## 8. 风险与未决
- OB 4.4 的 DBMS_METADATA / 分区 DDL / synonym-package 支持度 → Phase B 实测后决定断言口径（记 gap 或改代码）。
- OB-MySQL SEQUENCE 字典在 4.4 的存在形式未知 → Phase D 探查后给结论。
- PG `superme` 是否超管（可建 role/schema）→ A5 探测，不足则降级为仅抽取已存在 schema。
- 各主线耗时：总量控制（每主线独立 t.Run，报告可定位）。
