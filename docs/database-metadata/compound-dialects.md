# 复合数据库类型的元数据查询（组合方言）

以下数据库类型没有独立的元数据提取器，而是通过 `normalizeDBType()` 映射到基础提取器。

映射逻辑参见 `internal/metadata/extractor/extractor.go`（`normalizeDBType`）:

```go
func normalizeDBType(t string) string {
    t = strings.ToLower(strings.TrimSpace(t))
    switch {
    case isPGWireFamily(t): // opengaussdb*, panweidb*
        return "postgres"
    case strings.HasSuffix(t, "-mysql"):
        return "mysql"
    case t == "oceanbase-oracle-wire":
        return "oracle"
    case strings.HasSuffix(t, "-oracle"):
        return "oracle"
    case t == "goldendb", t == "oceanbase":
        return "mysql"
    default:
        return t
    }
}
```

> 关键点：openGauss 系（openGaussDB / PanWeiDB）的 PG、A（Oracle）、B（MySQL）
> 三种兼容模式跑在**同一个内核**上，**线协议统一为 PG**，因此 `-oracle` / `-mysql`
> 后缀只决定 DDL/类型方言，**绝不参与提取器与占位符的选择**。
> 若按后缀选到 MySQL 提取器，其 `?` 占位符会被 openGauss 解析器当作普通 token，
> 报 `pq 语法错误在"AND"处或附近`（`?` 后面的那个 token）。

## GoldenDB

| 映射类型 | 提取器 | 说明 |
|---|---|---|
| `goldendb` | MySQL | 默认使用 MySQL 兼容模式 |
| `goldendb-mysql` | MySQL | 显式指定 MySQL 模式 |
| `goldendb-oracle` | Oracle | 显式指定 Oracle 兼容模式 |

- 元数据查询 SQL 与 MySQL 或 Oracle 的 `information_schema`/`all_*` 视图一致
- DDL 生成通过 `internal/dialect/goldendb/goldendb.go` 组合 MySQL + Oracle

## OceanBase

| 映射类型 | 提取器 | 说明 |
|---|---|---|
| `oceanbase` | MySQL | 默认使用 MySQL 兼容模式（连接时自动探测租户模式） |
| `oceanbase-mysql` | MySQL | 显式指定 MySQL 模式 |
| `oceanbase-oracle` | Oracle | Oracle 兼容模式，驱动按 DSN 自动选择（见下） |

- **需要注意**: OceanBase Oracle 模式的元数据视图与 Oracle 标准 `all_*` 视图类似但不完全相同
- OceanBase MySQL 模式的 `information_schema.statistics` 可能与 MySQL 有差异
- DDL 生成通过 `internal/dialect/oceanbase/oceanbase.go` 额外处理:
  - OceanBase MySQL 模式的 `CREATE SEQUENCE`（MySQL 原生不支持）
  - OceanBase Oracle 模式禁用 BITMAP 索引（输出注释提醒）

### 租户模式探测（compat_mode）

`internal/dbconn/oceanbase.go` 在连接 `oceanbase`/`oceanbase-mysql` 后自动执行
`SHOW VARIABLES LIKE 'ob_compatibility_mode'` 探测租户兼容模式：

- 探测到 Oracle 模式但类型配置为 MySQL 系 → 报错并指引改用 `oceanbase-oracle`
- 配置项 `compat_mode: mysql|oracle` 可显式声明（见 `config.DBConfig.CompatMode`），
  `compat_mode: oracle` 必须搭配 `type: oceanbase-oracle`

### Oracle 租户的双连接路径

`oceanbase-oracle` 的连接方式由 DSN 协议决定（`dbconn.resolveOceanBaseOracleDriver`）：

| DSN 形式 | 驱动 | 场景 | 占位符 |
|---|---|---|---|
| `oracle://user:pw@host:port/service` | go-ora（TNS） | OBProxy Oracle 监听端口 | `:N` |
| 其他（如 `oboracle://`、`mysql://`） | helingjun/obconnector-go | OceanBase MySQL 线协议直连（2881 等） | `?` |

MySQL 线协议路径下：
- DSN 自动改写为 `oboracle://` 并注入 `preset=oboracle`
- 元数据提取使用 `oceanbase-oracle-wire` 提取器（Oracle SQL + `?` 占位符，
  `internal/dbconn.MetadataSourceType` 自动路由）
- 该提取器为 OceanBase 兼容模式：列查询去掉 `all_tab_columns.collation`
  （OB 中没有该列，报 ORA-00904），并**不做 identity 列提取**——
  `all_tab_identity_cols` 视图在 OceanBase 中不存在（v3.2.4 / v4.4 均无），
  依赖它的 identity 元数据无法从 `ALL_*` 字典视图可靠获取
- 数据导入/导出通过 `PlaceholderFamily: "qmark"` 覆盖方言占位符

> 教训（来自 GoNavi 实践）：直连 OBServer/OBProxy 的 MySQL 协议端口时，
> 裸 go-ora（TNS）无法连通；Oracle 租户必须走 obconnector-go 的
> `CLIENT_SUPPORT_ORACLE_MODE` 握手能力，否则报 OB Error 1235。

## PanWeiDB

| 映射类型 | 提取器 | 说明 |
|---|---|---|
| `panweidb` | PostgreSQL | 默认使用 PG 兼容模式 |
| `panweidb-mysql` | PostgreSQL | **通信协议使用 PG 的 `$N` 占位符** |
| `panweidb-oracle` | PostgreSQL | **通信协议使用 PG 的 `$N` 占位符** |

- 所有 PanWeiDB 变体的**运行时查询**都使用 PG 元数据提取器
- 通信协议细节（占位符、标识符引用）保持一致:
  - 在 exporter 中: `isPostgres()` 对 `panweidb`、`panweidb-mysql`、`panweidb-oracle` 均返回 `true`
  - 在 importer 中: `isMySQL()` 和 `isOracle()` 对 PanWeiDB 变体均返回 `false`（使用 PG 风格的 `$N`）
- DDL 生成通过 `internal/dialect/panweidb/panweidb.go` 组合 PG + MySQL + Oracle
- 2026-09-23 修复：`normalizeDBType` 曾按 `-mysql` / `-oracle` 后缀把 `panweidb-mysql`、
  `panweidb-oracle` 分别路由到 MySQL / Oracle 提取器，与其 PG 线协议冲突
  （`panweidb-mysql` 报 `query tables: pq 语法错误在"AND"处或附近`）。
  现统一按线协议家族路由，与 `opengaussdb*` 一致；回归测试见
  `internal/metadata/extractor/extractor_test.go`（`TestResolveQuerierPGWireFamily`、
  `TestQueryTablesPanWeiDBMySQLUsesPGBinds`）。

> B 模式的类型名差异仍是已知问题：dolphin 在 `information_schema` 把 `DECIMAL(9,2)`
> 上报为 `number(9,2)`，PG 提取器如实记录原类型名，迁往 MySQL/PG 目标时需
> `ddl.type_overrides` 兜底（见 `docs/test-plans/opengaussdb.md`）。

## OpenGaussDB

| 映射类型 | 提取器 | 说明 |
|---|---|---|
| `opengaussdb` | PostgreSQL | 100% PG 兼容 |
| `opengaussdb-mysql` | PostgreSQL | B 模式（dolphin）：MySQL 方言/引用/DDL，但线协议与元数据走 PG（`$N`） |
| `opengaussdb-oracle` | PostgreSQL | A 模式：Oracle 方言/引用/DDL，但线协议与元数据走 PG（`$N`） |

- OpenGaussDB 直接复用 PostgreSQL 的所有元数据查询和 DDL 生成
- DDL 生成通过 `internal/dialect/opengaussdb/opengaussdb.go` 纯组合 PG 方言（无任何 override）
