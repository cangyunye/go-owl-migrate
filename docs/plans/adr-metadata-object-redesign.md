# Architecture Decision Records — Metadata Object Export & Generation Redesign

设计：`docs/plans/2026-09-02-metadata-object-redesign.md`（状态草案，未发布可破坏重构）。
本文件按决策顺序追加；每条满足 ADR 三条件才记录（难逆转 / 无上下文难懂 / 真实取舍）。

---

## ADR-001: 生成 DDL/建表标识符策略默认保真（PreserveIdentifierCase）

**Status**: Accepted（grill Q1 → 方案 A）
**Date**: 2026-09-02

### Decision

生成 DDL 与 `migrate`/`import` 目标建表时，标识符一律经方言 `IdentifierQuoter` 输出，且**默认 quote 并保留元数据原始大小写**（`CREATE TABLE "SCOTT"."EMP"`）。所有 builder（表/视图/序列/触发器/函数/包/同义词/索引）走同一条 quote 路径，禁止 builder 内各自实现 quote 或私自大小写折叠。

`--no-quote-identifiers` 保留为显式放弃引号的选项（目标端因此按目标方言惯例折叠大小写），默认不再折叠。

### Rationale

- 迁移工具语义是"复制源结构"，大小写折叠应属于目标端显式选择，不是 builder 偷偷执行的默认（现状 PG `BuildCreateTable` 独做 `strings.ToLower`，与 view/seq 保留大写分裂，实测产出 `"scott"."emp"` vs `"SCOTT"."EMP_VIEW"` 混用）。
- runtime import 建表路径已按保真引号工作，统一后 `export ddl` 与 `migrate` 建表同一路径。

### Considered Options

- **方案 B：默认按目标方言折叠**（PG 小写、Oracle 大写、无引号或折叠引号）。符合单库惯例，但跨方言迁移（Oracle 大写 owner → PG）时内容与原元数据不一致，且仍需全量重写各 builder 才能消除现状分裂。

### Consequences

- DDL 测试基线改为保真断言（`"SCOTT"."EMP"`）；`no_quote_identifiers` 用例另行覆盖折叠路径。
- `dialect-mapping.md` 需写明"quote 保真 vs 折叠"语义与两选项行为。

---

## ADR-002: 对象选择边界——附随自动连带，独立对象须显式选择，不做依赖上卷

**Status**: Accepted（grill Q2 → 统一）
**Date**: 2026-09-02

### Decision

- **附随对象（attached）**：列、主键、索引、外键、分区（表属性）、表触发器——选中表即自动连带，不可单独挑选。
- **独立对象（standalone）**：视图、物化视图、序列、同义词、函数/存储过程、包、包体——仅在显式选择或选择整个 schema 时产出；**不做"因被依赖而自动上卷"**（选 EMP 不因 EMP_VIEW 引用它而带入 EMP_VIEW）。
- 同表名跨 owner（如 `*:EMP`）命中多张表时，各自附随集分别连带，schema 级对象仍不跟随。
- DDL/SELECT 生成默认"仅表（含附随）"，另提供"全量"整 schema 选项；INSERT 只处理表数据。

### Rationale

- 表与 schema 级对象生命周期不同：表属对象与表共存亡，schema 级对象独立存在；以"依赖"上卷会让单表请求产出不可预测的对象集（issue 2 的根因）。
- 显式选择模型可表达全部场景（单表 / 多表 / 视图 / 全量），选择结果可落在 SchemaModel 视图上供全链路复用。

### Considered Options

- **依赖自动上卷**：选 EMP 自动带出引用它的视图/函数。看似贴心，但选择结果不可预测，且视图/序列可被多表引用导致重复归属。

### Consequences

- schema 级对象在抽取后与表同处 SchemaModel，但"选择"层必须区分两类存储位置；UI 对象类型多选与"附随"联动。
- 术语进 CONTEXT.md：附随对象 / 独立对象 / 对象选择 / 能力。

---

## ADR-003: config 表过滤并入统一 ObjectSelector，显式点名 > exclude > glob include

**Status**: Accepted（grill Q3 → 同意）
**Date**: 2026-09-02

### Decision

- config 的 `table_filter`（include/exclude：glob/regex/按 schema 排除/精确表）语义并入统一 `ObjectSelector` 数据结构与单一解析器；YAML 字段形态保留（老配置不破坏）。
- Web/CLI 请求层只表达 include；exclude 仅来自 config（基线偏好，不应每次请求重复）。
- 优先级：**显式点名的精确表 > exclude > glob include**——用户明确写 `SCOTT.EMP` 时强制包含（点名 = 我要，不被 config 排除挡住）；`*`/glob 匹配受 exclude 约束。

### Rationale

- 现状过滤被实现三次（config.MatchTable / exportmetadata / detail handler），migrate、import、export data、DDL/SELECT/INSERT、Web 各消费其中一版，语义漂移是 issue 1/2/4/5 的共同根。
- exclude 是"基线偏好"（如排除日志表）应只落在 config；请求级重复表达没有意义。
- 显式点名优先避免"全量被挡、点名也被挡"的反直觉，保留 config 排除的防呆价值。

### Considered Options

- 请求级也要能表达 exclude：增加交互复杂度，且与"基线偏好"职责重复。
- exclude 优先于一切（含点名）：点名意图无法表达，用户只能改 config 绕行。

### Consequences

- `ObjectSelector` 成为 config 与请求层共用的唯一结构；`config.MatchTable`/`filterTableDefs`/`filterSchemaTables`/exportmetadata 的 tableFilter 全部收敛到它。

---

## ADR-004: 能力不支持的选顶行为——CLI 报错、UI 不可选、支持但为空属正常

**Status**: Accepted（grill Q4 → 同意）
**Date**: 2026-09-02

### Decision

- 方言 querier 的"原生不支持"桩从静默返回 nil 改为显式 `ErrUnsupported`（附支持清单）。CLI 选到不支持对象类型 → 报错并列出该方言支持的对象，**绝不静默产出空文件**。
- Web/表单按能力矩阵**直接隐藏/禁用**不支持的对象类型选项（用户选不到，也就不报错）。
- "能力支持但对象为空"（如选了序列、库里确实没有）→ 正常空结果：导出给空文件 + 注明 0 个；DDL/SELECT 生成 0 文件。
- 能力探测：注册表静态能力为主；仅 OB（兼容模式运行时可变）等少数场景连库探测。

### Rationale

- issue 1/2 的共同病灶之一是"静默空结果"：选错对象/大小写/不存在时返回 0 且无提示。显式错误与可区分的"空"是闭环反馈（根治原则 5）的落地。
- UI 前置禁用让大多数用户根本走不到错误路径，CLI 错误兜底给脚本用户明确指引。

### Considered Options

- 静默跳过（现状）：无反馈，用户不知道自己的选择被忽略。
- UI 也报错：正常用户被本不该出现的选项打扰。

### Consequences

- 需要区分"不支持"与"为空"：querier 错误类型 + 模型计数共同决定导出结果文案。

---

## ADR-005: 元数据导出为文件级粒度——附随文件可单独勾选（与生成侧语义分离）

**Status**: Accepted（grill Q5 → A）
**Date**: 2026-09-02

### Decision

元数据导出（CLI `--objects` 与 Web 多选）按**文件级**可选：13 个文件（tables/columns/primary_keys/indexes/foreign_keys/views/mviews/sequences/synonyms/triggers/functions/packages/package_bodies）各自可勾，默认随表全勾、可拆（如只导 tables+columns）。UI 呈现依赖关系：勾"表"默认带出附随文件，取消不受限。

生成侧（DDL/SELECT/INSERT）语义不受影响，仍按 ADR-002 严格 attached。

### Rationale

- 导出是**可裁剪的元数据交接**（columns.csv 单独用于类型映射等场景）；生成是**一致复刻**。两个职责不同，粒度规则不同是合理的，而不是语义漂移。
- 文件级粒度直接对应 CSV 文件与 csv-format.md 规范，CLI/Web/离线三处共享同一枚举。

### Considered Options

- 严格 attached（B）：附随文件不可拆，导出后需人工删文件才能得到"只要列"。
- 按对象大类粗粒度（表/视图/…）：无法表达"只要 columns"。

### Consequences

- 对象类型枚举即文件词干：`tables,columns,primary_keys,indexes,foreign_keys,views,mviews,sequences,synonyms,triggers,functions,packages,package_bodies`；UI 分组为附随（默认联动可拆）与独立两类。
