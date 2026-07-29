# 复合数据库类型的元数据查询（组合方言）

以下数据库类型没有独立的元数据提取器，而是通过 `normalizeDBType()` 映射到基础提取器。

映射逻辑参见 `internal/metadata/extractor/extractor.go:63-76`:

```go
func normalizeDBType(t string) string {
    switch {
    case strings.HasSuffix(t, "-mysql"):
        return "mysql"
    case strings.HasSuffix(t, "-oracle"):
        return "oracle"
    case t == "goldendb", t == "oceanbase":
        return "mysql"
    case t == "panweidb", t == "opengaussdb":
        return "postgres"
    default:
        return t
    }
}
```

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
| `oceanbase` | MySQL | 默认使用 MySQL 兼容模式 |
| `oceanbase-mysql` | MySQL | 显式指定 MySQL 模式 |
| `oceanbase-oracle` | Oracle | 显式指定 Oracle 兼容模式 |

- **需要注意**: OceanBase Oracle 模式的元数据视图与 Oracle 标准 `all_*` 视图类似但不完全相同
- OceanBase MySQL 模式的 `information_schema.statistics` 可能与 MySQL 有差异
- DDL 生成通过 `internal/dialect/oceanbase/oceanbase.go` 额外处理:
  - OceanBase MySQL 模式的 `CREATE SEQUENCE`（MySQL 原生不支持）
  - OceanBase Oracle 模式禁用 BITMAP 索引（输出注释提醒）

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

## OpenGaussDB

| 映射类型 | 提取器 | 说明 |
|---|---|---|
| `opengaussdb` | PostgreSQL | 100% PG 兼容 |

- OpenGaussDB 直接复用 PostgreSQL 的所有元数据查询和 DDL 生成
- DDL 生成通过 `internal/dialect/opengaussdb/opengaussdb.go` 纯组合 PG 方言（无任何 override）
