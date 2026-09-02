# OceanBase-Oracle 单元验收测试方案

> 环境驱动的验收测试：连上一套真实的 OceanBase Oracle 租户，验证本仓库
> `oceanbase-oracle` 迁移链路的关键功能，并产出一份机器可读的测试报告，用于
> 反馈本仓库的修改。
>
> 代码位置：`internal/e2eob/oceanbase_oracle_e2e_test.go`（build tag `e2e`）

## 设计目标

| 目标 | 说明 |
|------|------|
| 零改码接入 | 只需设置环境变量提供连接串与 owner，不改任何代码 |
| 一份报告 | 每次运行产出 `output/e2e/oboracle_report.json`，含每项检查的 pass/fail/skip 与详情 |
| 可离线跑 | 未提供连接时仍产出报告：方言/DDL 类检查 pass，数据库类检查 skip |
| 覆盖关键路径 | 连接解析 → 兼容模式探测 → 元数据抽取 → 数据读取 → 类型映射 → DDL 生成 |

## 前置条件

```bash
export OWL_E2E_OB_DSN='oceanbase-oracle://user@tenant:pass@host:2881/db'
export OWL_E2E_OB_SCHEMA='SCOTT'   # 租户内实际 owner（Oracle 模式通常大写）

# 可选：自定义报告输出路径（默认 <repo>/output/e2e/oboracle_report.json）
export OWL_E2E_REPORT='./my-report.json'
```

`OWL_E2E_OB_DSN` 支持三种形式，驱动自动识别：

| 形式 | 示例 | 驱动 |
|------|------|------|
| 直连 OBServer (2881) | `oceanbase-oracle://sys@tenant:pass@host:2881/db` | obconnector-go（MySQL wire，`?` 占位符） |
| OBProxy 多集群 (2883) | `oceanbase-oracle://sys@tenant#cluster:pass@host:2883/db` | obconnector-go（cluster 折入用户名） |
| Oracle TNS | `oracle://user:pass@host:2883/service` | go-ora（`:N` 占位符） |

## 运行

```bash
go test -tags e2e -v ./internal/e2eob/
```

成功输出结尾：

```
--- PASS: TestOceanBaseOracleReport (…s)
    oceanbase_oracle_e2e_test.go:…: report: …/output/e2e/oboracle_report.json (pass=N fail=0 skip=0)
PASS
```

`fail > 0` 时测试进程返回非零退出码，适合接入 CI。

## 测试用例清单

| # | 检查项 | 状态条件 | 验证的代码路径 |
|---|--------|---------|---------------|
| 1 | `connect` | DSN 可连通 | `dbconn.Open` → `resolveOceanBaseOracleDriver`（oboracle/go-ora 选择、cluster 折入、`preset=oboracle`、timeout 注入） |
| 2 | `compat_mode` | 仅 MySQL wire，探测值 = `oracle` | `dbconn.ProbeOceanBaseCompatMode`（`SHOW VARIABLES LIKE 'ob_compatibility_mode'`） |
| 3 | `extract` | ≥1 表且每表均有列 | `extractor.Extract("oceanbase-oracle-wire")` → `QueryTables` NULL 扫描（tablespace/temporary/num_rows） |
| 4 | `columns` | 首表列名/类型非空 | `QueryColumns` 无 collation 分支（`oceanbase-oracle-wire` 专用 SQL） |
| 5 | `keys_indexes` | 全库 PK 数 > 0 | PK/FK/索引抽取 |
| 6 | `objects` | 计数不报错（可为零） | 视图/序列/同义词/触发器/函数/包抽取 |
| 7 | `data_read` | `ROWNUM <= ?` 绑定查询返回行 | MySQL wire 的 `?` 占位符 + Oracle 语法混用 |
| 8 | `type_mapping` | 各类型 base 符合预期 | `obOracleTypeMapper`：`BFILE→BLOB`、`NUMBER(4,0)→SMALLINT`、`NUMBER(7,2)→NUMERIC`、`VARCHAR2`、`DATE→DATETIME`、`CLOB` |
| 9 | `ddl_table` | 生成含列名的 `CREATE TABLE` | `obOracleDDLBuilder.BuildCreateTable` |
| 10 | `ddl_bitmap_index` | 输出 `-- MANUAL` 注释 | `BuildCreateIndex` 的 BITMAP 降级 |
| 11 | `ddl_sequence` | 生成 `CREATE SEQUENCE` | `BuildCreateSequence` |
| 12 | `feature_truncate_txn` | `true` | `obOracleFeatures.TruncateIsTransactional`（OB 与标准 Oracle 的差异） |

## 报告格式

`output/e2e/oboracle_report.json`：

```json
{
  "db_type": "oceanbase-oracle",
  "dsn": "oceanbase-oracle://user@tenant:******@host:2881/db",
  "schema": "SCOTT",
  "generated_at": "2026-09-02T23:34:51+08:00",
  "summary": { "pass": 12, "fail": 0, "skip": 0 },
  "results": [
    { "check": "connect", "status": "pass", "detail": "wire=mysql-wire (oboracle)", "elapsed": "12ms" }
  ]
}
```

- `dsn` 中的密码经 `config.MaskDSN` 脱敏。
- 每个 `result.detail` 携带可读的验证细节（行数、列数、映射结果等），可直接摘录进反馈。
- 未提供连接时数据库类检查为 `skip`，报告仍会写入。

## 端到端迁移验证（可选）

单测只覆盖到「数据读取」；完整导出 → 建表 → 导入 → 外键排序走 CLI 端到端：

```bash
# 模板已提交：testdata/db/oceanbase_oracle_to_pg.yaml
# 改 source.dsn / source.schema 后：
go run ./cmd/migrate/main.go migrate -c testdata/db/oceanbase_oracle_to_pg.yaml
cat output/migration_report.json
```

模板使用 `metadata.type: database`，由 `dbconn.MetadataSourceType` 路由到
`oceanbase-oracle-wire` 抽取活库元数据，目标为本地 PostgreSQL
（`docker compose up -d` 起的 `postgres_db`，见 `testdata/db/README.md`）。

## 反馈要点

把报告贴回本仓库时，按 `check` 字段对应到具体代码路径即可定位：

| 失败项 | 对应代码 |
|--------|---------|
| `connect` / `compat_mode` | `internal/dbconn/oceanbase.go` |
| `extract` / `columns` / `keys_indexes` / `objects` | `internal/metadata/extractor/oracle.go`（`OracleMetadataQuerier` / `OceanBaseOracleWireQuerier`） |
| `data_read` | 占位符协议：`OceanBaseOracleWireQuerier` 的 `?` 绑定 |
| `type_mapping` / `ddl_*` / `feature_truncate_txn` | `internal/dialect/oceanbase/oceanbase.go` |
