# 剩余死配置、方言特性与测试缺口 — 实施计划

> **For agentic workers:** 自主逐任务执行。每个任务自带测试闭环与提交。决策已全部敲定，无需逐项确认；仅在遇到阻塞性歧义时停下。

**Goal:** 消除初始审计中全部剩余问题——22 项死配置、3 项方言特性、6 处测试缺口——使文档承诺与代码行为一致。

**Architecture:** 沿用既有模式：导入侧配置接入 `internal/transfer/importer`（复用 `isMySQL/isOracle/isPostgres` 方言判定 + sqlmock 测试）；DDL 侧接入各方言 `BuildCreateTable`/`BuildCreateIndex`（读取已存在的 `BuildOptions`）；导出侧接入 `internal/transfer/exporter`；无价值/与架构冲突的配置项直接从 `config.go` 与文档移除。

**Tech Stack:** Go 1.25, cobra, yaml.v3, go-sqlmock（测试）, excelize（xlsx）, zap。

## Global Constraints

- 不新增注释（除非必要）；遵循现有约定（import 分组 stdlib→third-party→internal；`%w` 错误包装；测试文件 `<name>_test.go` 同包）。
- 每个任务结束运行 `gofmt -w`、`go build ./...`、`go vet ./...`、相关 `go test`，全绿后提交。
- 提交风格 Conventional Commits：`feat(scope):` / `fix(scope):` / `test:` / `docs:` / `refactor:`。
- 移除配置项安全：`config.Load` 用 `yaml.Unmarshal`（默认忽略未知字段），删除结构体字段不会破坏含该字段的旧 YAML。
- 架构红线（CLAUDE.md）：「无单表并行写——写并发仅在表级」。凡暗示单表内并行的配置（`max_table_workers`、per-table `overrides`）一律移除而非实现。

## 决策总表（实现 vs 移除）

### 实现（22 项中的 16 项 + 3 方言特性 + partition）
| 区域 | 配置项 | 任务 |
|---|---|---|
| import | `null_identifiers`（strings/case_sensitive/regex）| Phase 3 |
| import | `null_semantics`（oracle_empty_string_is_null / numeric_zero_not_null）| Phase 3 |
| import | `datetime_truncate_to_target` | Phase 3 |
| import | `drop_indexes`（导入前删索引、导入后用方言 DDL 重建）| Phase 3 |
| ddl | `include_drop`（生成 DROP 语句）| Phase 4 |
| ddl | `type_overrides`（覆盖特定类型映射）| Phase 4 |
| ddl | `boolean_mapping`（自定义布尔值映射）| Phase 4 |
| ddl | `empty_string_to_null` | Phase 4 |
| select_gen | `include_row_number` | Phase 4 |
| select_gen | `add_export_columns` | Phase 4 |
| export.csv | `escape_char` | Phase 5 |
| export.csv | `encoding` | Phase 5 |
| export.csv | `null_overrides`（按列 null 覆盖）| Phase 5 |
| export.csv | `empty_string_to_null` | Phase 5 |
| metadata.csv | `column_name_matching` | Phase 5 |
| ddl | `partition.migrate`（分区 DDL 生成）| Phase 9（最大，单独）|
| dialect | oceanbase-mysql：无 FULLTEXT、强制 InnoDB | Phase 6 |
| dialect | oceanbase-oracle：BFILE→BLOB | Phase 6 |
| dialect | panweidb-mysql：忽略 ENGINE= | Phase 6 |

### 移除（6 项，更新 config.go + docs）
| 配置项 | 移除理由 | 任务 |
|---|---|---|
| `import.data_transforms.target_encoding` | 文档自注「future use」，字段从不读取 | Phase 7 |
| `parallel.max_table_workers` | 与「无单表并行写」架构冲突 | Phase 7 |
| `export.tables.overrides`（per-table）| 同上 | Phase 7 |
| `source.charset` | 含义模糊，编码已由 `source_encoding` 处理 | Phase 7 |
| `export.csv.quote_policy` | 枚举无语义定义 | Phase 7 |
| `export.csv.newline_handling` | 枚举无语义定义 | Phase 7 |

### 测试补缺（Phase 8）
| 包 | 现状 | 方案 |
|---|---|---|
| `metadata/extractor` | 0% | 纯函数单测（`normalizeDBType`/`GetQuerySQL`/`Get`）+ sqlmock 验证 PG/MySQL/Oracle 查询可执行 |
| `internal/cmd` | ~9% | 纯辅助函数单测（`toBuildOptions`、`buildPKMap`、`MatchTable` 已在 config）|
| `dialect/opengaussdb` | 0% | 镜像 postgres 测试（嵌入正确性 + 类型映射）|
| `metadata/xlsx` | 0% | 用 excelize 构造测试 xlsx，验证元数据/@数据表解析 |
| `registry` | 0% | `Get`/`Normalize`/重复 `Register` panic |
| `cmd/migrate` | 0% | 跳过（薄入口，间接覆盖）|

---

## Phase 3: 导入侧死配置

### Task 3.1: null_identifiers + null_semantics

**Files:**
- Modify: `internal/transfer/importer/importer.go`（`Config` 加字段；扩展 `isNullValue`）
- Modify: `internal/cmd/import.go`, `internal/cmd/migrate_cmd.go`（接线）
- Test: `internal/transfer/importer/importer_test.go`

**Approach:**
- `Config` 增 `NullIdentifiers NullIdentifiers`（含 `Strings []string`、`CaseSensitive bool`、`Regex string`，编译一次 `*regexp.Regexp`）与 `NullSemantics NullSemantics`（`OracleEmptyStringIsNull bool`、`NumericZeroNotNull bool`）。
- `isNullValue(v, colType)` 扩展：依次匹配 `CSVNullMarker` → `NullIf` → `NullIdentifiers.Strings`（按 CaseSensitive）→ `NullIdentifiers.Regex`；`NullSemantics.OracleEmptyStringIsNull && isOracle() && v==""` → null；`NumericZeroNotNull && v=="0"`（数值列）→ null。
- 接线 `cfg.Import.CSV.NullIdentifiers` / `NullSemantics`。

**Test:** 表驱动覆盖各 null 识别路径（大小写敏感/不敏感、regex、Oracle 空串、数值 0）。

**Commit:** `feat(importer): implement null_identifiers and null_semantics`

### Task 3.2: datetime_truncate_to_target

**Files:** `internal/transfer/importer/importer.go`, `internal/cmd/{import,migrate_cmd}.go`, test 同上。

**Approach:** `Config` 增 `DatetimeTruncateToTarget bool`；在 `convertCompactDatetime`/日期输出后，若启用则按目标列类型截断精度（如 DATE 截到日、TIMESTAMP(0) 截到秒）。需列类型信息——在 `transformValue` 传入目标列 `ColumnDef`（调整签名 `transformValueWithCol`）。

**Test:** 14 位 datetime 截断到 DATE → `YYYY-MM-DD`。

**Commit:** `feat(importer): implement datetime_truncate_to_target`

### Task 3.3: drop_indexes

**Files:** `internal/transfer/importer/importer.go`, `internal/cmd/{import,migrate_cmd}.go`, test `importer_policy_test.go`。

**Approach:**
- `Config` 增 `DropIndexes bool` 与 `IndexDDL func(schema, table string) (drop []string, recreate []string)`（由 cmd 层用方言 `BuildCreateIndex` + DROP INDEX 语法构造，避免 importer 直接依赖 dialect）。
- `importOneTable`：导入前执行 drop（在 truncate 后），`defer` 导入后 recreate（用 `context.WithoutCancel`，复用 guard 模式）。
- cmd 层依据 `TargetDBType` 生成 `DROP INDEX <name>`（PG: `DROP INDEX schema.idx`；MySQL: `DROP INDEX idx ON table`；Oracle: `DROP INDEX schema.idx`）与重建 DDL。

**Test:** sqlmock 验证 drop→insert→recreate 顺序（PG/MySQL 各一）。

**Commit:** `feat(importer): implement drop_indexes with dialect-specific drop/recreate`

---

## Phase 4: DDL 侧死配置

### Task 4.1: include_drop

**Files:** `internal/generator/ddl.go`（或各方言 builder 入口）, test `internal/generator/ddl_test.go`。

**Approach:** `BuildOptions.IncludeDrop` 已存在；在 DDL 生成器为每个对象前置 `DROP <OBJECT> [IF EXISTS]`（Oracle 无 IF EXISTS，用 PL/SQL 忽略异常或直接 DROP）。

**Test:** 生成含 DROP 前缀的 DDL。

**Commit:** `feat(generator): emit DROP statements when include_drop is set`

### Task 4.2: type_overrides

**Files:** `internal/dialect/dialect.go`（`BuildOptions.TypeOverrides map[string]string` 已存在？需确认；否则加）, 各方言 `FromLogicalType` 调用点, test。

**Approach:** 在 DDL 生成列类型时，先查 `opts.TypeOverrides[源类型大写]`，命中则用覆盖值（支持 `%l/%p/%s` 占位符），否则走方言默认映射。

**Test:** override `NUMBER→DECIMAL(%p,%s)` 生效。

**Commit:** `feat(dialect): apply ddl.type_overrides to column type mapping`

### Task 4.3: boolean_mapping + empty_string_to_null (DDL)

**Files:** 各方言 `BuildCreateTable`, test。

**Approach:**
- `boolean_mapping`（`map[string]bool`）：DDL 默认值渲染时，将自定义布尔字面量映射到目标布尔表示。
- `empty_string_to_null`：列默认值 `''` 渲染为 `NULL`（Oracle 兼容）。

**Test:** 各行为单测。

**Commit:** `feat(dialect): implement boolean_mapping and empty_string_to_null DDL options`

### Task 4.4: include_row_number + add_export_columns (select_gen)

**Files:** `internal/generator/select.go`, test。

**Approach:**
- `include_row_number`：SELECT 增 `ROW_NUMBER() OVER (ORDER BY pk) AS rn`（Oracle 用 `ROWNUM`）。
- `add_export_columns`：增导出辅助列（如 `__export_ts`）。

**Test:** 生成的 SELECT 含相应列。

**Commit:** `feat(generator): implement include_row_number and add_export_columns`

---

## Phase 5: 导出 CSV 与元数据配置

### Task 5.1: export.csv escape_char / encoding / null_overrides / empty_string_to_null

**Files:** `internal/transfer/exporter/exporter.go`（`Config` + `formatCSVValue`/writer）, `internal/cmd/export_data.go`, test `writer_test.go`。

**Approach:**
- `escape_char`：CSV 写出时用其转义引号（替代默认双写）。
- `encoding`：导出文件按目标编码编码（UTF-8/GBK，复用 `golang.org/x/text`）。
- `null_overrides`（`map[col]marker`）：按列自定义 null 表示。
- `empty_string_to_null`：空串写为 null 表示。

**Test:** 各行为单测（构造 DataTable 走 ExportTablesFromData）。

**Commit:** `feat(exporter): implement escape_char, encoding, null_overrides, empty_string_to_null`

### Task 5.2: column_name_matching

**Files:** `internal/metadata/csv/parser.go`（或 loader）, `internal/cmd/metadata.go`, test `parser_test.go`。

**Approach:** `CSVConfig.ColumnNameMatching`（`case_insensitive`/`case_sensitive`）控制 CSV 表头与预期列名的匹配方式；默认 case_insensitive。

**Test:** 两种模式下表头匹配行为。

**Commit:** `feat(metadata): implement csv column_name_matching mode`

---

## Phase 6: 方言特性

### Task 6.1: oceanbase-mysql 无 FULLTEXT / 强制 InnoDB

**Files:** `internal/dialect/oceanbase/oceanbase.go`, test `oceanbase_test.go`。

**Approach:** `NewMySQL()` 覆盖 `BuildCreateIndex`（FULLTEXT→普通或跳过+注释）与 `BuildCreateTable`（ENGINE 强制 InnoDB，忽略 MyISAM）。

**Test:** FULLTEXT 索引降级；ENGINE=InnoDB。

**Commit:** `feat(oceanbase): strip FULLTEXT and force InnoDB for oceanbase-mysql`

### Task 6.2: oceanbase-oracle BFILE→BLOB

**Files:** `internal/dialect/oceanbase/oceanbase.go`, test。

**Approach:** `NewOracle()` 覆盖类型映射，`BFILE`→`BLOB`（不支持 BFILE）。

**Test:** BFILE 映射为 BLOB。

**Commit:** `feat(oceanbase): map BFILE to BLOB for oceanbase-oracle`

### Task 6.3: panweidb-mysql 忽略 ENGINE=

**Files:** `internal/dialect/panweidb/panweidb.go`, test `panweidb_test.go`。

**Approach:** `NewMySQL()` 覆盖 `BuildCreateTable`，省略 `ENGINE=` 子句。

**Test:** 输出不含 ENGINE=。

**Commit:** `feat(panweidb): omit ENGINE clause for panweidb-mysql`

---

## Phase 7: 配置清理（移除 6 项）

### Task 7.1: 移除死配置字段 + 更新文档

**Files:** `internal/config/config.go`（删 `TargetEncoding`、`MaxTableWorkers`、`TableListConfig.Overrides`/`TableOverride`、`DBConfig.Charset`、`ExportCSVConfig.QuotePolicy`/`NewlineHandling` 及相应 `isZero`）, `docs/config.md`（删除对应行）, `docs/migration-pipeline.md`（target_encoding 行）, 移除 importer.go 的 `TargetEncoding` 死字段。

**Approach:** 删除字段与文档条目；`grep` 确认无残留引用；`go build` 验证。

**Test:** `go build ./...` + `go test ./internal/config/`。

**Commit:** `refactor(config): remove vestigial config options (target_encoding, max_table_workers, per-table overrides, charset, quote_policy, newline_handling)`

---

## Phase 8: 测试补缺

### Task 8.1: extractor 单测

**Files:** Create `internal/metadata/extractor/extractor_test.go`。

**Approach:** 测 `normalizeDBType`（goldendb→mysql、panweidb→postgres 等）、`Get`（已注册/未注册）、`GetQuerySQL`（各方言各对象类型返回非空）。可选 sqlmock 验证查询字符串可被准备。

**Commit:** `test(extractor): add unit tests for querier registry and query SQL`

### Task 8.2: opengaussdb 方言测试

**Files:** Create `internal/dialect/opengaussdb/opengaussdb_test.go`。

**Approach:** 镜像 postgres 测试：嵌入正确性、类型映射、quoting、features。

**Commit:** `test(opengaussdb): add dialect tests`

### Task 8.3: registry 测试

**Files:** Create `internal/registry/registry_test.go`。

**Approach:** `Get` 已注册方言、`Normalize`（goldendb→goldendb-mysql）、未知方言报错、重复 `Register` panic。

**Commit:** `test(registry): add registry tests`

### Task 8.4: xlsx loader 测试

**Files:** Create `internal/metadata/xlsx/loader_test.go`（用 excelize 在 TempDir 构造 xlsx）。

**Approach:** 构造含 `tables`/`columns` 元数据表 + `@EMP` 数据表的 xlsx，验证解析为 SchemaModel + 数据 CSV 输出。

**Commit:** `test(xlsx): add loader tests`

### Task 8.5: internal/cmd 辅助函数测试

**Files:** Create/extend `internal/cmd/*_test.go`（非 e2e）。

**Approach:** 测纯辅助：`toBuildOptions`（字段映射）、`buildPKMap`、`newLogger`（json/file 配置不 panic）。

**Commit:** `test(cmd): add unit tests for build options and helpers`

---

## Phase 9: partition.migrate（最大）

### Task 9.1: 分区 DDL 生成

**Files:** 各方言 `BuildCreateTable`（读取 `TableDef.PartitionInfo`，`opts.SkipPartitions` 已存在）, test。

**Approach:** 当 `!opts.SkipPartitions` 且 `t.Partitioned=="YES"`，在 CREATE TABLE 后追加方言分区子句（Oracle/PG 语法各异；MySQL 用 `PARTITION BY`）。`PartitionInfo` 已由元数据解析。

**Test:** Oracle/PG/MySQL 各一分区 DDL 用例。

**Commit:** `feat(dialect): generate partition DDL when partition.migrate is enabled`

---

## 验证与收尾

- 每 Phase 结束：`gofmt -l internal/`（应只剩预存在的 duckdb.go/sqlite3.go）、`go build ./...`、`go vet ./...`、`go test ./...` 全绿。
- 全部完成后更新 `docs/config.md`/`docs/migration-pipeline.md`/`docs/dialect-mapping.md` 使文档与实现一致；跑 `go test -cover ./...` 记录覆盖率提升。
- 按 Phase 分提交（已含各任务 commit）；不 push（除非要求）。

## Self-Review

- **Spec 覆盖：** 22 死配置（16 实现 + 6 移除）、3 方言特性、partition、6 测试缺口均有对应任务。✓
- **占位符：** 各任务含文件/方法/测试/提交；复杂实现给出 approach，执行时读码补全精确代码。✓
- **类型一致：** `BuildOptions` 字段（IncludeDrop/TypeOverrides/Partition.SkipPartitions）沿用 `dialect.go` 既有定义；importer `Config` 字段命名与既有风格一致。✓
