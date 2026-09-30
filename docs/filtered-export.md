# 条件导出与列映射

针对"不全量、不全列"的场景：**WHERE 条件导出**（含执行前条件 COUNT 校验）、**列投影与改名**（输出列的个数、顺序、名称完全由配置决定）、**按列类型转换**。`migrate` 与 `export data` 共用同一导出层，以下能力对两者同时生效；`import` 读 CSV 无需配置。

版本要求：v0.7.x（分支 feat/ai-chat-router-research 起）。

## WHERE 条件导出

### 配置

```yaml
export:
  filters:                                   # 表选择模式 → WHERE 片段
    "SCOTT.EMP": "deptno = 20 AND sal > 1000"   # 精确点名（大小写不敏感）
    "*.LOG_*":   "created >= DATE '2026-01-01'" # glob（*.T 整名 / 裸键匹配表名）
  filters_check: count                       # 条件 COUNT 门禁：count(默认) | off
```

匹配语义与 `export.tables.include` 一致：**精确点名优先于 glob**；同一张表命中多个 glob 直接报错（不隐式合并，避免重叠模式拼出意外条件）。只想给个别表加条件、其余全量？只写那几条 filter 即可，未命中的表不受影响。

### CLI

```bash
# export data：逗号分隔多条，PATTERN 与片段用第一个冒号分隔
owl-migrate export data -c migrate.yaml \
  --where 'SCOTT.EMP: deptno = 20, *.LOG_*: created >= DATE ''2026-01-01'''

# migrate 同位覆盖（flag 优先于配置）
owl-migrate migrate -c migrate.yaml --where 'SCOTT.EMP: deptno = 20'
```

### 条件 COUNT 门禁（执行前校验）

带条件的表在导出第一步执行 `SELECT COUNT(*) FROM <表> WHERE <条件>`：

- **先校验后导出**：条件里的列名/语法/权限错误在这里暴露，任务中止，错误信息指名表与条件——
  ```
  Error: filter gate failed for OWL_SRC.EMP (filter "nosuchcol = 1"):
         ORA-00904: "NOSUCHCOL": invalid identifier — fix export.filters and retry
  ```
  按提示修改 filter 后重跑即可。工具**不解析**条件语句，数据库就是校验器。
- **顺带产出源侧行数**：COUNT 值作为该表的源侧 expected；导出行数与之不符会告警（并发写入或非确定性条件），报告可做 **源库 vs CSV vs 导入** 三方对账。
- `filters_check: off` 可跳过门禁（超大表 escape hatch），代价是报告失去源侧基准。serve 的迁移预检（`POST /api/v1/migrate/preflight`）在 filters 非空时执行同一套逐表 COUNT 检查，失败项进入 checks。

### 片段规则

| 规则 | 说明 |
|---|---|
| 字面 SQL 片段 | 原样进入 WHERE 位置，工具不解析；**列名/表名书写必须符合源库语法** |
| 禁止 | `;`（多语句）、`--` 与 `/*`（注释截断）、`?` 与 `:1`（绑定占位符，与游标分页参数位冲突）——配置校验与导出前双重拒绝 |
| 确定性 | keyset 游标分页要求条件对同一行恒定：含 `NOW()/SYSDATE/CURRENT_DATE/RANDOM()` 等易变函数会告警（批间可能漂移漏行） |
| 大小写 | 过滤键匹配大小写不敏感；片段内的标识符按源库习惯书写（Oracle 大写、PG 小写或加引号） |
| 通道 | native 与 agent（JDBC）同权——条件在 SQL 层注入 |

### 迁移语义与边界

- **migrate + filters = 子集迁移**：目标表只含满足条件的行。报告带 `"filtered": true` 与所用条件（`filters` 字段），避免把子集误读为全量。
- **`--resume` 指纹**：条件变更后断点续跑会被拒绝（旧检查点描述的是另一个子集），请重新开始迁移。
- **`online init` 硬拒绝**：触发器 CDC 按行回放该表**所有**变更（含条件外的行），过滤初始装载会被增量慢慢"长全"，子集语义静默失效。要条件装载 + 不增量，用一次性 migrate；要全量 + 增量，清掉 filters。
- 无主键表走 OFFSET 分页，叠加非确定性条件时漂移风险更高（有告警）。

## 列投影与改名

```yaml
export:
  columns:
    include:                    # 列表顺序 = CSV/目标表列顺序（不是源表列序）
      "SCOTT.EMP": ["empno", "sal", "ename"]
    rename:                     # 就位改名（不改顺序）
      "SCOTT.EMP": {SAL: salary}
```

- **输出列的个数、顺序、名称完全由 include 列表决定**；未列出的列丢弃。不配 `columns` = 全列、源表列序（与旧行为一致）。
- 键匹配同 filters 语义（精确 > glob）；引用不存在的列报错并列出可用列。
- **主键列不可被投影丢弃**（keyset 分页与 CDC 都依赖键），配置校验直接拒绝。
- **migrate 自动建表**：目标表的列集/顺序/名称与投影后的 CSV 完全一致（含改名，如 `SAL → "salary"`）；改名后索引/外键的列引用同步更新。
- **目标表已存在**：启用投影时做列集预检——投影列必须都存在于目标表（多出的目标列允许，需可空），缺失则 fail-fast，不再逐行报错。目标表列比 CSV 少时（未启用投影）仍由数据库逐行报错。
- `ddl.column_types` 与投影组合使用：`column_types` 的键写**改名后**的输出列名。

## 按列类型转换

```yaml
ddl:
  column_types:                     # 按列覆盖目标类型（键 SCHEMA.TABLE.COLUMN，大小写不敏感）
    "SCOTT.EMP.SAL": "number(10,2)"
```

- 生效于**自动建表**（migrate/import 的 ensure）与 `export ddl`；列级优先于按类型名的 `ddl.type_overrides`；支持 `%l/%p/%s` 长度/精度/标度占位符。
- 数据级转换：`import.data_transforms.column_datetime_formats` 按列覆盖紧凑日期模板（键同为 `SCHEMA.TABLE.COLUMN`）：

```yaml
import:
  data_transforms:
    datetime_format: yyyyMMddHHmmss            # 全局默认
    column_datetime_formats:
      "SCOTT.EMP.HIREDATE": yyyyMMdd           # 该列单独用日期
```

字符串→数值的解析转换暂不支持（数值按原样绑定，由目标库隐式转换）。

## 完整示例（同库双用户，oracle → oracle）

```yaml
metadata: {type: database}
ddl:
  target_dialect: oracle
  schema_mapping: {OWL_SRC: OWL_TGT}
source: {type: oracle, dsn: "oracle://OWL_SRC:***@127.0.0.1:1521/XEPDB1", schema: OWL_SRC}
target: {type: oracle, dsn: "oracle://OWL_TGT:***@127.0.0.1:1521/XEPDB1", schema: OWL_TGT}
export:
  filters: {"OWL_SRC.EMP": "sal > 0"}
  columns:
    include: {"OWL_SRC.EMP": ["empno", "sal", "ename"]}
    rename:  {"OWL_SRC.EMP": {SAL: salary}}
```

```bash
owl-migrate migrate -c oracle2oracle.yaml -r report.json
```

结果契约（e2e 已验证，脚本 `scripts/e2e_where_filter.sh`）：

- CSV 表头 `EMPNO,salary,ENAME` —— 配置顺序、配置个数、改名后名称；
- 目标表 `OWL_TGT.EMP` 列序同为 `(EMPNO, salary, ENAME)`，行数 = 门禁 COUNT（3）；
- 报告 `"filtered": true`、`"filters": {"OWL_SRC.EMP": "sal > 0"}`。

## AI 对话用法

`POST /api/v1/ai/plan` 生成含 filters/columns 的配置草案（凭据走占位符注入）。多轮会话下"继续导出 Where 条件 2 的数据"会沿用上一轮的库/表/格式槽位、仅覆盖条件槽位（`continuity.mode=continued`）；条件字段由 `config.Load` 校验兜底幻觉。注意 AI 不改变门禁语义——生成后仍要过条件 COUNT 才执行。

## 排查

| 现象 | 原因与处理 |
|---|---|
| `filter gate failed for X (filter "…"): <DB错误>` | 条件里的列名/语法/权限错误；按 DB 错误修正 filter |
| `matches multiple export.filters keys` | 同表命中多个 glob 模式；把模式改互斥或用精确点名 |
| `filter fragment contains forbidden construct` | 片段含 `;`/注释/绑定占位符；改写为纯谓词 |
| `projection drops primary key column(s)` | include 丢了主键列；把 PK 加回 include |
| `target table … already exists but lacks projected column(s)` | 已存在目标表与投影列集不一致；对齐目标表或投影配置 |
| `metadata column "…" not found in the live source` | 元数据与活库列集漂移（或改名映射写错）；重抽元数据或修正 include |
| 导出行数 ≠ 门禁 COUNT（有告警） | 条件非确定性或并发写入；改用确定性谓词或接受快照差异 |
| `online init: export.filters is set …` | 条件装载与触发器 CDC 语义互斥；二选一 |
