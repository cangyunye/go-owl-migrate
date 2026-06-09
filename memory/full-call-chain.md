---
name: full-call-chain
description: Complete call flow from CLI entry point to metadata extraction, DDL generation, and data migration
metadata:
  type: reference
---

# Complete Call Chain Reference

> 每个命令/场景的完整调用链路。元数据来源（CSV / 数据库）在 `loadSchemaModel()` 分派，下游完全透明。

## 1. Entry Point

```
cmd/migrate/main.go
  → internal/cmd/root.go: Execute()
      → cobra 根据子命令分派
```

子命令注册在 `root.go:43-48`:

| 命令 | cobra Handler | 文件 |
|------|---------------|------|
| `validate` | `validateCmd()` | `internal/cmd/validate.go` |
| `gen-ddl` | `genDDLCmd()` | `internal/cmd/genddl.go` |
| `gen-select` | `genSelectCmd()` | `internal/cmd/genselect.go` |
| `gen-insert` | `genInsertCmd()` | `internal/cmd/geninsert.go` |
| `export` | `exportCmd()` | `internal/cmd/export.go` |
| `import` | `importCmd()` | `internal/cmd/import.go` |
| `migrate` | `migrateCmd()` | `internal/cmd/migrate_cmd.go` |

共享标志 (`root.go:39-40`):
- `--config, -c` → `cfgFile` (默认 `./migrate.yaml`)
- `--log-level` → `logLevel`

## 2. Config 加载

所有命令第一步: `config.Load(cfgFile)`

```
config.Load(path)
  → os.ReadFile(path)                   读取 YAML
  → yaml.Unmarshal(data, &cfg)          解析到 Config
  → cfg.applyDefaults()                 设置默认值
  → cfg.validate()                      校验合法性
      → metadata.type 必须是 csv / database
      → ddl.target_dialect 必须是合法方言
      → 如果 type=database: 校验 source.type + source.dsn 必填
```

关键配置结构: `internal/config/config.go`

```go
type Config struct {
    General    GeneralConfig    `yaml:"general"`
    Metadata   MetadataConfig   `yaml:"metadata"`   // type: csv | database
    Source     DBConfig         `yaml:"source"`      // type, dsn, schema
    Target     DBConfig         `yaml:"target"`
    DDL        DDLConfig        `yaml:"ddl"`         // target_dialect, schema_mapping...
    SelectGen  SelectGenConfig  `yaml:"select_gen"`
    Export     ExportConfig     `yaml:"export"`
    Import     ImportConfig     `yaml:"import"`
}
```

## 3. 元数据加载（核心分派点）

```
loadSchemaModel(cfg)    [internal/cmd/metadata.go:17]
  |
  ├── cfg.Metadata.Type == "csv"
  │     → loadCSVModel(cfg.Metadata.CSV.Path)
  │         → csvpkg.NewLoader()
  │         → 遍历目录 .csv 文件 → loader.AddReader(name, f)
  │         → loader.Load()
  │             → parseTables()    [必选，tables.csv]
  │             → parseColumns()   [可选，columns.csv]
  │             → parsePrimaryKeys()...
  │             → parseIndexes()...
  │             → parseForeignKeys()...
  │             → parseViews()...
  │             → parseTriggers()...
  │             → parseFunctions()...
  │             → parseSequences()...
  │         → *md.SchemaModel
  │
  └── cfg.Metadata.Type == "database"
        → loadDBModel(cfg.Source.Type, cfg.Source.DSN, cfg.Source.Schema)
            → openDB(dbType, dsn)          [internal/cmd/metadata.go:89]
            → db.Ping()
            → extractor.Extract(db, dbType, schema)   [internal/metadata/extractor/extractor.go:76]
                |
                → normalizeDBType(dbType)
                → extractor.Get(dbType)   查找注册的 MetadataQuerier
                → sm := md.NewSchemaModel()
                → querier.QueryTables(db, schema)      [#1. 表]
                → sm.AddTable(t) for each
                → querier.QueryColumns(db, schema)     [#2. 列]
                → tbl.AddColumn(col) for each
                → querier.QueryPrimaryKeys(db, schema) [#3. 主键]
                → tbl.AddPrimaryKey() for each
                → querier.QueryIndexes(db, schema)     [#4. 索引]
                → sm.AddIndex(idx) for each
                → querier.QueryForeignKeys(db, schema) [#5. 外键]
                → sm.AddForeignKey(fk) for each
                → querier.QueryViews(db, schema)       [#6. 视图]
                → sm.AddView(v) for each
                → querier.QuerySequences(db, schema)   [#7. 序列]
                → sm.AddSequence(seq) for each
                → querier.QueryTriggers(db, schema)    [#8. 触发器]
                → sm.AddTrigger(trg) for each
                → *md.SchemaModel
```

### MetadataQuerier 注册表

`internal/metadata/extractor/extractor.go` `init():32-36`

| 数据库类型 | 实现 | 系统视图 | 占位符 |
|-----------|------|----------|--------|
| `postgres`/`postgresql` | `PGMetadataQuerier` | `information_schema.*` + `pg_catalog.*` | `$1` |
| `mysql` | `MySQLMetadataQuerier` | `information_schema.*` | `?` |
| `oracle` | `OracleMetadataQuerier` | `ALL_*` 字典视图 | `:1` |
| `goldendb`/`goldendb-mysql`/`oceanbase-mysql` | → normalize 为 `mysql` | 同 MySQL | `?` |
| `goldendb-oracle`/`oceanbase-oracle` | → normalize 为 `oracle` | 同 Oracle | `:1` |

### 扩展新增数据库类型

1. 实现 `MetadataQuerier` 接口
2. 在 `internal/metadata/extractor/` 下新建文件
3. 在 `internal/metadata/extractor/extractor.go` 的 `init()` 中 `Register(&YourQuerier{})`
4. 可选: 在 `normalizeDBType()` 添加别名映射

### 连接管理

`openDB()` (`internal/cmd/metadata.go:89`)

```go
func openDB(dbType, dsn string) (*sql.DB, error) {
    case "mysql":    sql.Open("mysql", dsn)         // go-sql-driver/mysql
    case "postgres": sql.Open("postgres", dsn)      // lib/pq
    case "oracle":   sql.Open("oracle", dsn)         // sijms/go-ora/v2
}
```

Driver 引入:
- `export.go` → `_ "github.com/go-sql-driver/mysql"`, `_ "github.com/lib/pq"`, `_ "github.com/sijms/go-ora/v2"`
- `import.go` → `_ "github.com/go-sql-driver/mysql"`, `_ "github.com/lib/pq"`
- `migrate_cmd.go` → 全部三个 driver

注意: `import.go` 缺少 Oracle driver。`openDB("oracle", ...)` 在 import.go 中会 panic，因为 driver 未注册。

## 4. gen-ddl 调用链

```
genDDLCmd()       [internal/cmd/genddl.go]
  → config.Load(cfgFile)
  → loadSchemaModel(cfg)               → *md.SchemaModel
  → registry.Get(cfg.DDL.TargetDialect) → dialect.Dialect
  → toBuildOptions(cfg)                → dialect.BuildOptions
  → generator.NewDDLGenerator(d, opts, outputDir)
  → gen.GenerateTables(sm)             [internal/generator/ddl.go]
      → sm.GetTables()
      → for each table: dialect.BuildCreateTable(tbl, opts)
      → writeFile() to {outputDir}/{schema}.{table}.table.sql
  → gen.GenerateIndexes(sm)
      → sm.GetTables() → tbl.GetIndexes()
      → dialect.BuildCreateIndex(idx)
      → writeFile()
  → gen.GenerateViews(sm)
      → sm.GetViews()
      → dialect.BuildCreateView(v)
      → writeFile()
```

## 5. gen-select 调用链

```
genSelectCmd()    [internal/cmd/genselect.go]
  → config.Load(cfgFile)
  → loadSchemaModel(cfg)               → *md.SchemaModel
  → registry.Get(cfg.DDL.TargetDialect) → dialect.Dialect (用于 Quote())
  → generator.NewSelectGenerator(method, pageSize, outputDir, d.Quote)
  → gen.Generate(sm)                   [internal/generator/select.go]
      → sm.GetTables()
      → for each table: build SELECT column_list + pagination_WHERE
      → writeFile()
```

## 6. gen-insert 调用链

```
genInsertCmd()    [internal/cmd/geninsert.go]
  → config.Load(cfgFile)
  → loadSchemaModel(cfg)               → *md.SchemaModel
  → generator.NewInsertGenerator(cfg)
  → gen.Generate(tables, dataDir)      [internal/generator/insert.go]
      → 读取 {sourceDir}/{schema}.{table}.csv
      → 生成批量 INSERT 语句
      → 事务控制 (BEGIN / COMMIT)
      → writeFile() to outputDir
```

## 7. export 调用链 (source DB → CSV)

```
exportCmd()       [internal/cmd/export.go]
  → config.Load(cfgFile)
  → loadSchemaModel(cfg)               → *md.SchemaModel (仅用于表结构信息)
  → buildPKMap(sm)                     → map[tableKey][]colName (游标分页用)
  → openDB(cfg.Source.Type, cfg.Source.DSN)
  → exporter.New(db, exporter.Config{...})   [internal/transfer/exporter/exporter.go]
  → exp.ExportTables(ctx, tables, pkMap)
      → 按表并发导出 (worker pool)
      → exportOneTable():
          1. getColumns(): "SELECT * FROM schema.table WHERE 1=0" → rows.ColumnTypes()
          2. fetchBatch(): 分页 SELECT (游标/LIMIT)
          3. rowToCSV(): 行 → CSV (RFC 4180)
          4. 写入 {outputDir}/{schema}.{table}.csv
```

关键: export 通过 `SELECT * WHERE 1=0` + `rows.ColumnTypes()` 在运行时发现列信息，不依赖 SchemaModel 的 ColumnDef。

## 8. import 调用链 (CSV → target DB)

```
importCmd()       [internal/cmd/import.go]
  → config.Load(cfgFile)
  → loadSchemaModel(cfg)               → *md.SchemaModel (用于建表 + INSERT 列信息)
  → openDB(cfg.Target.Type, cfg.Target.DSN)
  → ensureTables(ctx, db, sm, cfg, schemaMapping)
      → for each table:
          → 查询 information_schema.tables 检查表是否存在
          → 如果不存在: buildCreateTableSQL(tbl, schema, cfg) → CREATE TABLE
  → importer.New(db, importer.Config{...})  [internal/transfer/importer/importer.go]
  → imp.ImportTables(ctx, tables, schemaMapping)
      → 按表并发导入 (worker pool)
      → importOneTable():
          1. 读 CSV 文件 {sourceDir}/{schema}.{table}.csv
          2. 构建 INSERT 语句 (列来自 SchemaModel)
          3. 可选的 TRUNCATE BEFORE
          4. 批量执行 + 事务提交
```

注意: `buildCreateTableSQL()` 使用硬编码类型映射，不是通过 Dialect 系统的 DDLBuilder。

## 9. migrate 调用链 (源 DB → target DB 一站式)

```
migrateCmd()      [internal/cmd/migrate_cmd.go]
  │
  ├── Step 1: loadSchemaModel(cfg)     → *md.SchemaModel
  │            buildPKMap(sm)          → PK map
  │
  ├── Step 2: openDB(Source) → srcDB
  ├── Step 3: openDB(Target) → tgtDB
  │
  ├── Step 4: ensureTablesForMigrate(ctx, tgtDB, sm, cfg)
  │     → CREATE SCHEMA IF NOT EXISTS (PG)
  │     → buildCreateTableSQL() + CREATE TABLE
  │
  ├── Step 5: exporter.ExportTables(ctx, tables, pkMap)
  │     → 从源 DB 导出 CSV 到临时目录
  │
  ├── Step 6: importer.ImportTables(ctx, tables, schemaMapping)
  │     → 从临时目录 CSV 导入到目标 DB
  │
  └── Step 7: MigrationReport (JSON)
        → NewMigrationReport + AddTable + Print + writeFile
```

## 10. validate 调用链

```
validateCmd()     [internal/cmd/validate.go]
  → config.Load(cfgFile)
  → loadSchemaModel(cfg)               → *md.SchemaModel
  → csv.Validate(sm)                   [internal/metadata/csv/validator.go]
      → 检查每表至少一列
      → PK 列必须在列中存在
      → 索引列必须在列中存在
      → 非 PK 标识列警告
      → FK 引用完整性检查
      → 触发器表存在性检查
```

## 11. 关键数据类型

```
*md.SchemaModel        [internal/metadata/model.go]
  ├── Tables   map[string]*TableDef    key: "SCHEMA.TABLE"
  ├── Views    []*ViewDef
  ├── MViews   []*MViewDef
  ├── Synonyms []*SynonymDef
  ├── allForeignKeys []*ForeignKeyDef
  ├── allTriggers    []*TriggerDef
  ├── allFunctions   []*FunctionDef
  ├── allSequences   []*SequenceDef
  └── tableSet       map[string]bool

*md.TableDef
  ├── TableSchema, TableName, TableType
  ├── Engine, Tablespace, TableComment, Partitioned, ...
  ├── Owner               // 源 schema/owner 名称
  ├── Columns     []*ColumnDef
  ├── PrimaryKeys []*PrimaryKeyDef
  ├── Indexes     []*IndexDef
  ├── ForeignKeys []*ForeignKeyDef
  └── Triggers    []*TriggerDef

*md.ColumnDef
  ├── TableSchema, TableName, ColumnName, OrdinalPosition
  ├── DataType, DataLength, DataPrecision, DataScale
  ├── Nullable, DefaultValue, ColumnComment
  ├── IsIdentity, IdentityGeneration
  ├── CharUsed, HiddenColumn, VirtualExpression, EnumValues
  └── CharacterSet, Collation, OnUpdate
```

## 12. 依赖关系图

```
                     YAML Config
                         │
                    config.Load()
                         │
                    loadSchemaModel()
                    ┌────┴────┐
                    │         │
                   CSV     Database
                    │         │
               csv/loader  extractor.Extract()
                    │         └─ MetadataQuerier
                    │             ├─ QueryTables
                    │             ├─ QueryColumns
                    │             ├─ QueryPrimaryKeys
                    │             ├─ QueryIndexes
                    │             ├─ QueryForeignKeys
                    │             ├─ QueryViews
                    │             ├─ QuerySequences
                    │             └─ QueryTriggers
                    │
                    ▼
              *md.SchemaModel
                    │
         ┌──────────┼──────────┬──────────┐
         ▼          ▼          ▼          ▼
   DDLGenerator  SelectGen  InsertGen  Exporter/Importer
         │          │          │          │
    registry.Get()  │          │     database/sql
         │          │          │          │
   dialect.Dialect  │          │     openDB()
    ┌─ TypeMapper   │          │          │
    ├─ DDLBuilder   │          │     sql.DB
    ├─ DMLHelper    │          │
    └─ Quoter      ─┘          │
                                │
                     CSV 数据文件 / 数据库数据
```

## 13. 扩展指南

### 新增元数据来源（如 xlsx）

1. 在 `internal/config/config.go` 的 `ValidMetadataTypes` 添加 `"xlsx"`
2. 在 `internal/cmd/metadata.go` 的 `loadSchemaModel()` 添加 case
3. 新建 loader（如 `internal/metadata/xlsx/`），填充 `*md.SchemaModel`

### 新增数据库类型（如 SQL Server）

1. 新建 `internal/metadata/extractor/mssql.go`，实现 `MetadataQuerier`
2. 在 `internal/metadata/extractor/extractor.go:init()` 中 `Register(&MSSQLMetadataQuerier{})`
3. 在 `normalizeDBType()` 添加别名（如 `"azuresql"` → `"mssql"`）
4. 如需 CLI 连接，在 `openDB()` 添加 case 并 import driver

### 新增 DDL 对象类型

1. `internal/metadata/model.go` 添加 struct（如 `EventDef`）
2. `SchemaModel` 添加 `AddEvent()`/`GetEvents()` 方法
3. `MetadataQuerier` 接口添加 `QueryEvents()`
4. `Extract()` 函数添加第 9 步调用
5. `dialect.DDLBuilder` 添加 `BuildCreateEvent()`
