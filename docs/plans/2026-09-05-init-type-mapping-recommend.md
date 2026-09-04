# init 类型映射推荐（兼容向）—— 规划

> 状态：规划稿（未实施，用户将后续决定是否完成）
> 日期：2026-09-05
> 背景：用户强化 `owl-migrate init`：确定源数据源与目标数据库后，默认生成一套
> **字段类型映射**写入 migrate.yaml。原则：**目标兼容向**——推荐的目标类型必须
> 能覆盖源类型（值域/精度/长度无损），宁可少给也不给有损项；无法覆盖的源类型
> 显式列为 gap 供人工处理，不做静默有损映射。
> 现有基础（本规划全部基于现状，不改运行时默认行为）：
> - 跨族转换已有 IR：`dialect.ToLogicalType(raw,length,prec,scale)` → 目标方言
>   `FromLogicalType`（internal/dialect/dialect.go:57-68）；
> - 用户覆盖入口已存在并被生成路径消费：`ddl.type_overrides`（config.go:266），
>   消费点 `dialect.ApplyTypeOverride`（dialect.go:169），键=源原始类型名（大写），
>   值=目标类型串（verbatim 含参数）；
> - `internal/mapping` 有完整的 type-mapping 文件 schema（source_db/target_db/
>   exact_mappings/parameterized/semantic_overrides/default_transforms）与 loader，
>   **但未接线到任何生成/校验路径**；
> - `init` 已能推荐 schema 映射（recommendSchemaMapping，含 golden 测试），类型
>   映射推荐与之并列、不互相干扰。

## 1. 目标与非目标

### 目标
1. `init` 在"源数据库（类型+DSN，可选 schema）与目标方言已知"时，产出默认
   `ddl.type_overrides` 推荐条目（或经评估后选用独立 type-mapping 文件，见 §4）。
2. 推荐集满足**兼容约束**：对每条推荐 `src → tgt`，tgt 能无损覆盖 src；约束以
   可执行断言表达并有测试守护。
3. 输出自解释：注释/日志说明每条推荐的原因与"未覆盖源类型 gap 清单"。

### 非目标
- 不改变既有跨族默认转换语义（无 type_overrides 时行为与现在一致）。
- 不做逐列人工差异 UI；只做 init 阶段生成 + gap 提示。
- 本轮不实现（规划文档）；实施拆解见 §9。

## 2. 关键设计决策

### D1 兼容向的判据（覆盖关系 coverable）
以目标方言实现为准、不另造规则表两套真相：
- 推荐候选生成 = 对给定源 `(rawType, len, prec, scale)`，枚举该目标族的
  `FromLogicalType` 可达集合（各 LogicalType 的该族渲染 + 可选参数），选取
  **覆盖源**（值域⊇、精度/长度可容纳、语义等值）者；
- 判据函数 `coverable(srcCol, tgtType, targetFamily) bool` 做成单一事实源，
  供：① 单测属性测试（参数边界枚举）② 生成器 ③ docs 一致性测试（沿用
  P6 csv-format 的"导出列==文档列"测试模式：映射表 doc == 判据实现）。
- 同族（源族==目标族，含 OB 继承 base）默认不生成条目（恒等），除非显式
  `--type-map=full`。

### D2 推荐落点：先 `ddl.type_overrides`，type-mapping 文件作为阶段二
- 阶段一（最小可用）：写 `ddl.type_overrides`（已消费、零接线成本）；条目
  键大写源类型名；值含参数目标串（如 `VARCHAR(20 CHAR)`）。
- 阶段二（可选，另行决策）：把 `internal/mapping.TypeMappingFile` 接线
  （新 config 键如 `ddl.type_mapping_file` + 生成/校验读取点），以承载
  parameterized/semantic_overrides；接线前不对外宣称其可用。
- 决策依据：避免"先造 schema 后接线"的悬空资产（现状 mapping 包即此类，需
  在阶段二落地消费或标注 deprecated）。

### D3 两种生成模式
- **模板模式**（默认，不连库）：按 源族×目标族 的预置判据输出该组合全量
  兼容推荐。适合"先立配置再准备库"。
- **探针模式**（可选 `--probe`，源 metadata=database 时）：连源库取实际
  出现的数据类型集合（含参数分布）→ 只生成用到的条目 + 真实 gap 清单。
  探针不连目标库（映射按目标方言推导，无需目标可达）。

### D4 与内置默认的关系（避免噪音/破坏）
- 若某 `src→tgt` 与现有跨族默认转换**语义一致且参数无差异**，则不写条目
  （保持 yaml 精简，行为不漂移）；
- 仅当 ① 默认转换有损/丢失信息而兼容候选存在（写兼容候选修正）或
  ② 用户需显式锁定 时，才生成条目；
- gap（无可无损目标）：不进 type_overrides，在注释/终端给"人工处理"清单。

## 3. 输出形态（阶段一示意）

```yaml
# init 生成（示例：mysql → oceanbase-oracle，兼容向）
ddl:
  target_dialect: oceanbase-oracle
  type_overrides:
    # 源 int → 目标 NUMBER 可无损覆盖（10 位内）
    INT: NUMBER(10)
    # 源 varchar(255) → VARCHAR2(255 CHAR)（长度按字符计，等值）
    VARCHAR: VARCHAR2(255 CHAR)
    # 源 tinyint(1)（布尔语义列 emp.active 之类）→ NUMBER(1)（语义覆盖见探针+规则）
    TINYINT: NUMBER(3)
  # type_mapping_notes: 由 init 打印，不落 yaml 注释（yaml 注释能力有限）
```
> gap 示例（提示而非覆盖）：mysql `json`/`geometry`/`enum` → oracle 族默认
> 有损（CLOB 语义不等值）→ 列入"需人工确认"，不自动写覆盖。

## 4. 兼容矩阵（判据层）草案

矩阵最终由 D1 判据实现 + 测试生成；规划期先列覆盖规则类别（实施时逐族补全）：

| 源族 | 目标族 | 兼容推荐方向（示意，非终稿） |
|---|---|---|
| mysql | oracle/ob-oracle | int→NUMBER、bigint→NUMBER(19)、varchar→VARCHAR2(…CHAR)、decimal→NUMBER(p,s)、datetime→TIMESTAMP/DATE、date→DATE、time→?、blob→BLOB、tinyint(1)→NUMBER(1)；json/enum/geometry→gap |
| mysql | postgres | int→integer/bigint 视位宽、varchar→varchar、decimal→numeric、datetime→timestamp、tinyint(1)→boolean?（语义需用户选择）→ 默认 gap 或 boolean_mapping |
| postgres | oracle/ob-oracle | integer→NUMBER(10)、bigint→NUMBER(19)、numeric→NUMBER(p,s)、text→CLOB/VARCHAR2(4000 CHAR)、timestamptz→TIMESTAMP WITH TIME ZONE、boolean→NUMBER(1)/CHAR(1)（约定化→gap）、jsonb→CLOB(gap 有损)、uuid→VARCHAR2(36)?、数组→gap |
| postgres | mysql | text→LONGTEXT、jsonb→JSON、boolean→tinyint(1)（约定化）、timestamptz→datetime?（时区丢失→gap） |
| oracle | mysql/pg | NUMBER(p,0)≤10→int、NUMBER≤19→bigint、VARCHAR2→varchar(n)、CLOB→longtext/text、DATE→datetime（时区/粒度差异→标注）、同族 oracle→ob-oracle 恒等不生成 |
| 无符号/自增/identity | 各目标 | 不属类型覆盖，属建表选项（identity→serial 等已有独立 config：identity_to_serial），矩阵只覆盖"类型" |

## 5. init 交互与 UX

- 新 flag：`--type-map`（`recommend`（默认，随 migrate/export-ddl 场景）/`none`/
  `full`（含与默认一致项，调试用）/`probe` 需要 metadata=database 且连源库）；
- 输出位置：并入既有 `buildMigrateConfig/buildDDLConfig` 生成结果（与
  `recommendSchemaMapping` 并列的新函数 `recommendTypeMapping(srcType,tgtType,srcSchema?,probeDB?)`），
  纯函数可测；
- 终端打印：`[type-map] 生成 N 条兼容映射；gap: mysql.json/geometry → 需人工确认`；
- `--scenario export-ddl / migrate / full` 默认开启；`export-insert / gen-select`
  等不涉及类型映射的场景不生成。

## 6. 测试设计

1. **判据属性测试**（无 DB）：枚举每 源族类型×参数边界，断言覆盖集合内条目
   都满足 `coverable`；同族为空集。
2. **golden 测试**：`recommendTypeMapping(mysql, oceanbase-oracle, …)` →
   yaml 片段与 golden 一致（新增 mysql→ob-oracle、pg→ob-oracle、pg→mysql、
   oracle→postgres 四组）。
3. **文档一致性测试**：docs 映射表 == 判据实现（复用 exportedColumnSets/
   csv-format 的"单事实源"模式）。
4. **e2e 真库回归（延伸）**：init 生成 yaml → `export ddl` 目标产物列类型
   属于该条目的覆盖断言（挂 M1/M7 样式）；`validate` 通过；不改默认路径
   基线（`go test ./internal/...` 恒绿）。
5. **gap 完整性**：对源族全集-覆盖集合，均有 gap 条目（不静默漏类型）。

## 7. 实施落点清单（后续完成时用）

1. `internal/service` 或新 `internal/cmd/typemap.go`：`recommendTypeMapping` +
   `coverable` 判据 + 各族规则实现（先 mysql/oracle/postgres 三族互转）。
2. `internal/cmd/init.go`：flag + 场景开关 + 打印 gap；buildMigrate/DDL/full
   集成（与 recommendSchemaMapping 并列）。
3. （阶段二，独立评审）mapping 包接线或标注。
4. docs：dialect-mapping.md/新"类型映射推荐"章节 + golden 表；init 帮助文案。
5. 测试：§6 全部 + 探针模式（--probe 连源库取实际类型）。

## 8. 验收

- `init --source-type mysql --source-schema X --target-type oceanbase-oracle`
  生成 yaml 含 type_overrides 且每条满足 coverable；同族不生成；
  无 DB 也可用（模板模式）。
- gap 类型不出现自动覆盖；有日志提示。
- 全量离线测试绿；docs 与判据一致性测试绿。
- 现有默认转换行为零回归（无 type_overrides 的旧配置产物不变）。

## 9. 风险与未决

- 无损判据对部分类型无"标准答案"（pg boolean、jsonb、uuid、数组；mysql enum/
  json）：默认归 gap，避免拍脑袋有损映射——若用户要求可加"约定化映射"开关。
- 参数化语义差异（字符 vs 字节 CHAR/BYTE、时区、小数舍入）必须进判据，不能只比
  类型名。
- 探针模式需源库可达且查询类型分布（information_schema/all_tab_columns 已有
  querier），注意大 schema 的采样成本。
- identity/自增/分区等非类型维度由既有 config 处理，本方案不越界。
