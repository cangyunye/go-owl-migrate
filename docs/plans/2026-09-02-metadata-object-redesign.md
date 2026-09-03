# 元数据对象导出与生成侧统一治理 — 根治设计

> 状态：设计稿（项目未发布，可破坏性重构）
> 背景：六个前端问题的实测验证见会话记录；本文档给出根因与根治架构，不做最小修补。
> 涉及方言：oracle、oceanbase-oracle（含 oboracle-wire / TNS 两种连法）、oceanbase-mysql、mysql、postgresql。

## 决策索引（grill 已确认，详见 docs/plans/adr-metadata-object-redesign.md）

| ADR | 决策 |
|---|---|
| 001 | 生成 DDL/建表标识符默认保真：quote 保留原大小写，统一走方言 `IdentifierQuoter`；`no_quote_identifiers` 显式折叠 |
| 002 | 对象选择边界：附随对象随表自动连带，独立对象须显式选择/整 schema，不做依赖上卷；DDL/SELECT 默认仅表 |
| 003 | config `table_filter` 并入统一 `ObjectSelector`；优先级：显式点名精确表 > exclude > glob include |
| 004 | 能力不支持的选顶：CLI 报错（ErrUnsupported+清单）、UI 不可选、支持但为空 = 正常空结果 |
| 005 | 元数据导出为文件级粒度（13 文件可单独勾、附随默认联动可拆）；生成侧仍按 ADR-002 |

---

## 1. 病根清单（按验证结论归纳）

| # | 现象 | 根因（非症状） |
|---|------|--------------|
| R1 | 元数据导出 `table:` 只过滤"表"，视图/序列/同义词等仍然全 schema 导出；表不在当前 schema 时静默返回 0 张表 | 没有"对象级选择模型"；过滤与序列化耦合在 `serve/exportmetadata.go`；schema 级对象未建模为"可归属对象" |
| R2 | 生成 DDL 输入单表仍输出视图/序列/函数等"其他对象"，输入不存在的表也不报错 | DDL 生成对表做过滤，但对 schema 级对象（view/seq/function/package/synonym/mview）从不按选择收窄；`handleGenerateDDL` 用全局 `cfg.Source.Schema` 而非模型实际 owner |
| R3 | 导出 CSV 缺对象类型：functions/mviews/packages/package_bodies 无文件；tables.csv 丢 OWNER/分区/ENGINE；columns.csv 丢 identity 等 | 导出 writer 是"手写 9 个文件的最小实现"，与 `docs/csv-format.md` 规范列脱节；CSV/Web/CLI 三处重复实现 |
| R4 | 同一个元数据 owner（SCOTT）在 DDL 里：表 `"scott"` 小写、视图/序列 `"SCOTT"` 大写 | PG 方言 `BuildCreateTable` 自带 `strings.ToLower`，而 `BuildCreateView/Sequence/Trigger/…` 各自再写一份 quote 且保留大小写——builder 各自为政，绕过了方言统一的 `IdentifierQuoter` |
| R5 | 三种过滤语义分裂：导出 `table:` 大小写敏感精确串；DDL/SELECT/INSERT 走 `config.MatchTable` glob；表详情 API 大小写敏感 | 同一"选择"概念被实现了三次（exportmetadata / filterSchemaTables / detail handler），没有单一事实源 |
| R6 | INSERT 页缺 `output/data` 目录直接 400 | `detectTablesFromCSVDir` 裸 `os.ReadDir`；导出→导入的目录约定靠用户手工维护，没有生命周期管理 |
| R7 | OB-MySQL 序列、OB-Oracle 分区等方言差异只能靠零散 override | 提取器按 base 方言归一（`normalizeDBType`），能力差异（如 OB-MySQL 有 SEQUENCE）无法声明 |

---

## 2. 根治原则

1. **单一事实源**：对象类型全集 + 各方言能力矩阵 = 一份声明式数据；抽取、导出、UI、校验都由它驱动。
2. **一处过滤**：一个 `ObjectSelector`（对象类型 × schema × 名称匹配），全链路（元数据导出 / DDL / SELECT / INSERT / migrate / 表详情）共用。
3. **一个引号策略**：任何方言的所有 builder 只能通过该方言的 `IdentifierQuoter` 生成标识符，禁止 builder 内再写 quote。
4. **类型在边界转换**：元数据类型属于源方言；渲染到目标方言时经 `TypeMapper` 在逻辑层转换，builder 不得透传原始 `DataType` 字符串。
5. **闭环反馈**：零命中 / 大小写 / owner 不一致不再静默；返回可操作错误与相近名称建议。
6. **目录有生命周期**：输出目录统一由框架创建并回填给前端展示；缺数据目录给出"用哪条命令生成"的可执行指引。

---

## 3. 对象模型（第一版全集）

以 schema（owner/namespace）为根，对象分两类：

- **表属对象（依附 table，随表选择自动连带）**：列、主键、索引、外键、分区、表触发器
- **schema 级对象（独立选择）**：视图、物化视图、序列、同义词、函数/存储过程、包、包体

新增/完善模型要点（`internal/metadata/model.go`）：

- `SchemaModel` 增加 `Schemas() []string`（按 owner 聚合，供生成侧按 schema 迭代，替代 R2 里"全局单个 schema"）。
- `TableDef` 已有 `Owner / Partitioned / PartitionInfo / Engine / …`；**导出与生成都必须携带**（R3）。
- `ColumnDef` 已有 `IsIdentity/IdentityGeneration/CharUsed/Collation/…`；导出补齐（R3）。
- 分区按"表属性"建模（不做独立 CSV）：分区定义文本（`PARTITION_INFO`）由各方言 querier 重建，跟随 tables.csv 导出；DDL 生成用 `dialect.PartitionClause` 渲染。
- 校验服务（`metadata/csv/Validator`）增加"导出文件完整性"检查：导出结果 reload 后应与抽取结果一致（往返测试）。

### 每方言导出能力矩阵（回答"分别怎么设计"）

图例：✅ 实现且验证 · 🟡 继承 base 但需覆写/复核 · ⬜ 原生不支持（桩返回空）· 🆕 待新增 · ❓ 依赖 OB 版本待实测

| 对象类型 | oracle | oceanbase-oracle | oceanbase-mysql | mysql | postgresql |
|---|---|---|---|---|---|
| 表 + 列/主键/索引/外键 | ✅ | ✅（wire 用 `?` 占位；catalog 缺 collation/identity 分支已处理） | ✅（继承 mysql） | ✅ | ✅ |
| 分区（表属性） | ✅ `all_tab_*` 重建 clause | 🟡 语法与 OB 版本（3.x/4.x）相关，继承 oracle 重建后需复核 | 🟡 继承 mysql `information_schema.partitions`；分区子句用 OB MySQL 方言渲染 | ✅ `information_schema.partitions` | ✅ `pg_partitioned_table` 内建 |
| 视图 | ✅ | ✅ | ✅ | ✅ | ✅ |
| 物化视图 | ✅ | 🟡 版本门控已加（低于某版本 mview 不查询） | ⬜ | ⬜ | ✅ |
| 序列 | ✅ | ✅ | 🆕 OB 原生支持 SEQUENCE，目前被 mysql 桩吞掉（`QuerySequences` 返回 nil）→ 需独立 OB-MySQL querier | ⬜（MySQL 无序列对象；自增走列 `auto_increment`，属列元数据） | ✅（含归属列/数据类型属性） |
| 同义词 | ✅ | ✅ | ⬜ | ⬜ | ⬜ |
| 触发器 | ✅ | ✅ | ✅（继承 mysql 查询；注意 OB 触发体语义） | ✅ | ✅ |
| 函数/存储过程 | ✅ | ✅（OB 区分 F/P 需复核 catalog） | 🟡 继承 mysql 查询，OB-MySQL 可存过程与函数 | ✅ | ✅（含重载/语言/返回类型） |
| 包 / 包体 | ✅ | ✅ | ⬜ | ⬜ | ⬜ |
| wire/占位符 | `:N` (go-ora) | `?`（oboracle wire）/ `:N`（TNS）两套 | `?`（MySQL wire） | `?` | `$N` |

> 设计模式（每个方言怎么接入）：**能力声明 + base 继承 + 差异覆写**，与现有 dialect 组合风格一致——
> ① 每个 querier 声明 `Capabilities() ObjectSet`（上表某行）；② 缺省继承 base（`normalizeDBType`），差异点在自己的文件里覆写单个 Query 方法与能力位（如 `OBMySQLOracleWire` 类文件）；③ 抽取、导出、UI 全部只消费 `Capabilities ∩ 用户选择`，方言层不必关心序列化。
> 据此，现有 `QueryX` 桩（mysql 的 seq/synonym/pkg、pg 的 synonym/pkg…）应显式返回 `(nil, ErrUnsupported)` 而不是静默 nil，让"不支持"可区分"为空"。

---

## 4. 对象选择模型（统一过滤，R1/R2/R5 根治）

新增 `internal/metadata/select.go`（替代散落的 `config.TableFilterConfig` 过滤实现 + exportmetadata 的 tableFilter + detail handler 精确匹配）：

```go
// 语法（CLI/API/UI 同一份，解析后落成 selector）：
//   objects=tables,views,sequences          对象类型白名单；空 = 方言支持全集
//   scope=*                                全部 schema/表
//   scope=schema:SCOTT                      单 schema 全对象
//   scope=schema:SCOTT:table:EMP,DEPT       指定 schema 内指定表（及表属对象）
//   scope=table:T_*                         跨 schema 按表名 glob（匹配任意 owner）
//   scope=schema:SCOTT,HR:table:EMP         多 schema + 多表/glob
type ObjectSelector struct {
    ObjectTypes ObjectSet          // 用户选的对象类型
    Schemas     []SchemaTableMatch // 每 schema 一个表名 glob（""=该 schema 全部）
}
type SchemaTableMatch struct{ Schema string; TableGlob string }
```

规则：

- **大小写策略统一**：所有匹配对标识符做 fold 比较（Oracle 大写、PG/MySQL/CSV 小写都要命中）；**输出保留元数据原 owner**。导出 `table:` 不再做精确大小写匹配（R5）。
- **连带规则**：选中表 ⇒ 自动连带 列/主键/索引/外键/分区/表触发器；选中视图/序列等为独立项。选择结果落回 `SchemaModel` 的一个视图（`Selected(model)`），DDL/SELECT/INSERT/导出/详情全用同一视图（R1/R2 根治：schema 级对象也进选择，不再"永远全量"）。
- **零命中反馈**：命中 0 时返回错误，附相近名 Top-N 建议与可用名称前缀列表；不再静默 200（R2/R5）。
- 兼容层：旧语法 `schema:NAME` / `table:T1,T2`（导出页与现有配置）解析到新 selector；`*`、`schema.*`、`T_*`、`EM?` 全部按 glob 语义支持并在帮助文案写清（"通配符 = glob：`*`任意、`?`单字符、`[]`字符集，schema 前缀须写真实 owner，无 `$schema` 变量"）。

---

## 5. 元数据导出（对象类型可选 + 规范文件，R3 根治）

### 5.1 输出结构

`元数据导出`（CLI `export-metadata` 与 Web `metadata/export` 共用同一个 `service.ExportMetadata(ctx, selector, format, outDir)`）：

- 文件 = **每个选中对象类型一个文件**：`tables.csv / columns.csv / primary_keys.csv / indexes.csv / foreign_keys.csv`、`views.csv / mviews.csv / sequences.csv / synonyms.csv / triggers.csv / functions.csv / packages.csv / package_bodies.csv`（分区为表属性，随 tables.csv 的 `PARTITIONED/PARTITION_INFO` 列导出，不单独成文件）。
- 列集 = **csv-format.md 的规范列**（现实现只写了子集），tables.csv 必须含 `OWNER/PARTITIONED/PARTITION_INFO/ENGINE/TABLESPACE/TEMPORARY/CHARSET/COLLATION`；columns.csv 补 identity/collation 等。
- format 支持 `csv / xlsx（每对象一 sheet）/ sql`；`sql` 仅 oracle-family 有意义（写入其数据字典表），其余方言选择 sql 时明确报"不支持"。
- 往返自检：导出后由 loader 读回并与抽取结果 diff，差异写进报告（接 R1 的闭环）。

### 5.2 Web UI

- 导出参数增加"对象类型"多选（默认 = 当前方言 `Capabilities` 全集），由 `/api/v1/scenarios`/新增 `/api/v1/dialects/{name}/capabilities` 下发可选项。
- Schema + 表 + 对象类型三要素即 issue 1 的"同时指定 schema 和 table"——scope 语法见 §4。
- 结果面板按对象类型分组展示文件；历史记录（generations）保留选择参数便于回放。

---

## 6. 生成侧根治（R2/R4 + issue 4/5）

### 6.1 引用与大小写（R4）

- 所有 builder 标识符一律走方言 `IdentifierQuoter`（已存在，`dialect.Dialect.IdentifierQuoter`），删除 PG `BuildCreateTable` 内 `strings.ToLower` 与 view/seq/trigger 内各自实现的 quote。
- **默认保真（ADR-001）**：quote 且保留元数据原始大小写（`CREATE TABLE "SCOTT"."EMP"`）；`no_quote_identifiers` 是显式的折叠选项，默认不再折叠。
- `schema_mapping` 只在 DDL 输出/导入时应用一次（render 边界），owner 本体不变。

### 6.2 类型边界（R4 关联）

- DDL 生成收到的是**源方言元数据**：builder 渲染列类型前先 `TypeMapper(source).ToLogicalType(...)` → `TypeMapper(target).FromLogicalType(...)`，`type_overrides` 最后覆盖；禁止 `col.DataType` 字符串透传。同源同目标时为恒等映射，向后兼容 CSV 离线模式。
- 目标方言 = `cfg.DDL.TargetDialect`；方言注册表补齐 `registry` 别名以便与 extractor 归一一致。

### 6.3 schema 级对象按选择与按 owner 分组（R2）

- `handleGenerateDDL` 改为：先 `sel.Selected(sm)` 得到与 §4 一致的模型视图，再按 `sm.Schemas()` 逐 owner 生成 sequence/function/package/synonym/mview；不再使用 `cfg.Source.Schema` 这一个全局值。
- SELECT / INSERT / 表详情/校验全部切到同一 selector（大小写、通配、连带规则天然一致 → issue 4/5 根治）。

---

## 7. 目录与导出数据生命周期（R6 根治）

- 所有"产物目录"（`output/ddl|select|insert|data|temp|e2e`）由框架 `paths` 统一 `EnsureDir()` 并在生成前创建；web 已走 `paths.TempDir()`，离线 CLI 输出目录同样自建。
- `insert`（离线 CSV → INSERT SQL）的数据源目录是"输入"，缺失时**不静默 400**：返回明确指引——
  错误文案固定格式：`未找到数据目录 <dir>：请先执行「数据导出」或 owl-migrate export data -c <cfg> 生成 {schema}.{table}.csv 后重试`；
  `insert/tables` 与 `insert/generate` 共用该错误，UI 引导跳转导出页（预填同数据目录）。
- `export data` 默认写 `cfg.Import.SourceDir`（即 `output/data`），保证"导出→生成 INSERT→导入"默认闭环，无需用户手工建目录。

---

## 8. 校验与回归（接既有测试资产）

- 方言矩阵可测化：扩展现有 `internal/e2eob` 报告与 `extractor_e2e_test.go`，每方言（oracle/ob-oracle/ob-mysql/mysql/pg）断言 `Capabilities` 与实测抽取对象一致、往返 diff 为零。
- `docs/csv-format.md` 作为规范唯一来源，新增一致性测试：导出列 == 文档列、loader 可读回。
- 把 §1 的 6 个复现用例固化为用例（选择零命中报错、单表 DDL 不含其他对象、owner 大小写一致、表详情大小写不敏感、缺目录指引文案）。

---

## 9. CLI 功能同步（必须与 Web 同步改造，不能只在 Web 侧根治）

### 9.1 现状与原则

CLI 与 Web 当前是**两套并行实现**，只是行为接近：

| 能力 | CLI | Web | 关系 |
|---|---|---|---|
| 元数据导出 | `internal/cmd/exportmetadata.go` | `serve/exportmetadata.go`（另写 exportMetadataCSV） | 各自一套 |
| 生成 DDL | `export_ddl.go` | `generate.go` handleGenerateDDL | 各自一套 |
| 生成 SELECT | `genselect.go` | `generate.go` handleGenerateSelect | 各自一套 |
| 生成 INSERT | `export_insert.go` | `generate.go` handleGenerateInsert + detectTablesFromCSVDir | 各自一套 |
| migrate / export data / import data | CLI 命令 | worker = CLI 子进程（同一份） | 同一份 |

**根治原则**：唯一实现放 `internal/service`（新增 `MetadataService.ExportMetadata / GenerateDDL / GenerateSelect / GenerateInsert`），CLI 命令与 Web handler 都只做"参数解析 → 调 service → 输出"的薄壳。这延续 ADR-016 既有模式（loadSchemaModel/openDB/filterTables/toBuildOptions 已迁入 service）。CLI 不是二等公民：

- **语法同源**：`--scope`/表选择/对象类型的解析在 `internal/metadata/select.go`，CLI flag 与 Web 表单共用同一 parser 与同一帮助文案（含通配符说明），不再各自写过滤。
- **能力同源**：`Capabilities()` 在 dialect/extractor 声明，`export-metadata` 的 `--objects` 白名单与 Web 对象类型多选由同一矩阵驱动；方言不支持的对象在 CLI 同样报错而不是静默跳过。
- **输出同源**：13 个规范 CSV 文件/列、往返自检、目录 EnsureDir，CLI 与 Web 完全一致；`docs/serve-cli-coverage-report.md` 的 10/10 覆盖表新增"同一 service 实现"的验证列。

### 9.2 各 CLI 命令需同步点

| 命令 | 同步点 |
|---|---|
| `export-metadata` | 新增 `--objects`（逗号分隔对象类型，默认方言支持全集）；`--scope` 扩展为统一选择语法；输出 13 个规范文件（含 functions/mviews/packages/package_bodies，tables.csv 含 OWNER/分区列）；大小写不敏感；零命中报错+相近名建议；`--format sql` 仅 oracle-family 允许 |
| `export ddl` | 走统一 selector（对象类型+多 schema 多表）；schema 级对象按 `sm.Schemas()` 分组输出而非全局单 schema；标识符经方言 `IdentifierQuoter`；类型经 TypeMapper 边界转换；`--no-quote-identifiers` 语义不变 |
| `gen-select` | 同一 selector 与大小写规则；输出内容标识符策略与 DDL 一致（不再正文小写、注释大写互相矛盾） |
| `export insert` | 复用 selector（含多 owner 表名 glob）；数据目录缺失给出与 Web 相同格式的可执行指引；文件名/大小写与 detectTables 一致 |
| `migrate` / `import` / `export data` | 表选择从统一 selector 解析（config 表 filter 的 include/exclude 语义并入 §4 语法，保留 exclude 能力）；目标建表 DDL 与 `export ddl` 同一类型边界/引号路径；产物目录 `EnsureDir`；`export data` 默认写 `cfg.Import.SourceDir`，形成导出→INSERT→导入闭环 |
| `init` / scenario builder | 场景表单/帮助新增 objects 选项与新 scope 语法示例 |
| `validate` | 增加"导出往返一致性"校验入口（CLI 与 Web 共用） |

### 9.3 需要同步的非代码资产

- `docs/cli-commands.md`、`docs/config.md`（新配置键与 `--objects`/`--scope` 语法）、`docs/csv-format.md`（规范即实现，加一致性测试）、`docs/serve-cli-coverage-report.md`、`configs/migrate.example.yaml`。
- CLI/Web 双端 e2e：每个 web 功能有一个等价 CLI 调用（同一 service 函数），加入 §10 回归矩阵。

---

## 10. 实施顺序（破坏性重构，逐阶段可提交）

1. **P0 对象模型与选择器**：`ObjectSet/ObjectSelector` + `Schemas()` + `ErrUnsupported`；全链路过滤迁移到 selector（含 config 表 filter 语义并入）；往返校验器。
2. **P1 service 抽壳（CLI/Web 同源）**：`service.MetadataService` 收编 export-metadata/DDL/SELECT/INSERT 四组逻辑（按 §9.1），CLI 命令与 serve handler 全部改为薄壳调 service；先保行为不变再重构，CLI/Web 双端冒烟通过。
3. **P2 引号与类型边界**：方言 `IdentifierQuoter` 统一；builder 去本地 quote、去类型透传；补齐 oracle/ob/pg/mysql 往返测试。
4. **P3 导出重写**：service 层 13 个规范 CSV + xlsx/sql（含 functions/mviews/packages/package_bodies 与 OWNER/分区列）；CLI `--objects`/`--scope` 与 Web 对象多选用同一 parser/能力矩阵；UI 更新。
5. **P4 方言能力补齐**：OB-MySQL 序列 querier、OB-Oracle 分区复核、桩改 `ErrUnsupported`、sql 格式方言限制。
6. **P5 生命周期与指引**：目录 Ensure、insert 缺目录指引、CLI 与 Web 文案同源。
7. **P6 回归固化**：6 个用例 + CLI/Web 等价性用例 + e2eob 矩阵扩展 + csv-format 一致性测试；同步 docs（cli-commands/config/csv-format/coverage-report/migrate.example.yaml）。

**关键文件**（重构落点）：`internal/metadata/model.go`、`internal/metadata/select.go`(新)、`internal/metadata/extractor/*.go`、`internal/dialect/{dialect,postgres,oracle,mysql,oceanbase,goldendb}/`、`internal/service/{metadata,exportmetadata,generate}.go`(新)、`internal/cmd/{exportmetadata,export_ddl,genselect,export_insert}.go`、`internal/server/serve/{exportmetadata,generate,scenarios}.go`、`docs/{cli-commands,config,csv-format,serve-cli-coverage-report}.md`、`configs/migrate.example.yaml`、`web/static/ui/views/{exportMetadata,generator,ddl,select,insert}.js`。
