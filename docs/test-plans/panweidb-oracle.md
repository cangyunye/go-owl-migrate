# PanWeiDB Oracle 兼容模式 (A 模式) E2E 测试方案

## 方言信息

| 属性 | 值 |
|------|-----|
| 注册名 | `panweidb-oracle` |
| 父方言 | Oracle (A 模式兼容) |
| 数据库驱动 | lib/pq (PG 协议) |
| 元数据提取器 | Oracle (`normalizeDBType` → `oracle`) |
| 端口 | 5432 (PG 协议) |

## 已覆盖的 Override

| 组件 | 状态 | 说明 |
|------|------|------|
| TypeMapper | ✅ `pdbOracleTypeMapper` | 继承 Oracle 类型映射 |
| IdentifierQuoter | ✅ 继承 Oracle | 双引号引用，大写 |
| Features | ✅ `postgres.PGFeatures{}` | ⚠️ 使用 PG 而非 Oracle 的 feature flags |
| DDLBuilder | ✅ 继承 Oracle 100% | 所有 DDL 方法继承 Oracle |
| DMLHelper | ✅ 继承 Oracle | OFFSET...FETCH 分页 |

## 差异点

| 差异 | PanWeiDB Oracle | 标准 Oracle | 代码状态 |
|------|----------------|------------|---------|
| 数据库驱动 | lib/pq (PG 协议) | go-ora | ✅ `openDB` 已区分 |
| TRUNCATE 事务安全 | ✅ openGauss 内核 | ❌ 否 | ✅ Features 用 PG |
| IF NOT EXISTS | 可能支持（PG 特性） | ❌ 不支持 | ✅ Features 用 PG 值 |
| Oracle 特有类型 | BFILE 可能不支持 | 支持 | ⚠️ 待实测 |

## 容器配置

```yaml
panweidb-oracle:
  image: enmotech/panweidb:latest
  environment:
    PANWEIDB_MODE: oracle   # A 模式
    POSTGRES_PASSWORD: panweidb123
  ports:
    - "5435:5432"
```

## 测试用例矩阵

| 对象类型 | 预期 | 说明 |
|---------|------|------|
| TABLE | ✅ 继承 Oracle | 大写双引号引用 |
| INDEX | ✅ 继承 Oracle | CREATE [UNIQUE] INDEX "NAME" |
| VIEW | ✅ 继承 Oracle | CREATE VIEW "NAME" AS |
| SEQUENCE | ✅ 继承 Oracle | CREATE SEQUENCE "NAME" ... |
| SYNONYM | ✅ 继承 Oracle | CREATE [PUBLIC] SYNONYM |
| TRIGGER | ✅ 继承 Oracle | CREATE OR REPLACE TRIGGER |
| FUNCTION | ✅ 继承 Oracle | CREATE OR REPLACE FUNCTION |
| PACKAGE | ✅ 继承 Oracle | CREATE OR REPLACE PACKAGE |
| MVIEW | ✅ 继承 Oracle | CREATE MATERIALIZED VIEW |

## 验证步骤

同 Oracle E2E 验证流程，指向 PanWeiDB A 模式服务端。
注意 DSN 使用 PG 协议格式而非 Oracle DSN。
