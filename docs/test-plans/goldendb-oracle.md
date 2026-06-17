# GoldenDB Oracle 租户 E2E 测试方案

## 方言信息

| 属性 | 值 |
|------|-----|
| 注册名 | `goldendb-oracle` |
| 父方言 | Oracle |
| 数据库驱动 | go-ora |
| 元数据提取器 | Oracle (`normalizeDBType` → `oracle`) |
| 端口 | 1521 (与 Oracle 一致) |

## 已覆盖的 Override

| 组件 | 状态 | 说明 |
|------|------|------|
| TypeMapper | ✅ 继承 Oracle 100% | 仅改 Name |
| IdentifierQuoter | ✅ 继承 Oracle 100% | 双引号引用，大写 |
| Features | ✅ 继承 Oracle 100% | Oracle feature flags (`TruncateIsTransactional: false`) |
| DDLBuilder | ✅ 继承 Oracle 100% | 所有 DDL 方法继承 Oracle |
| DMLHelper | ✅ 继承 Oracle 100% | OFFSET...FETCH 分页 |

## 差异点

| 差异 | GoldenDB Oracle | 标准 Oracle | 状态 |
|------|----------------|-------------|------|
| TRUNCATE 事务安全 | 需实测 | ❌ 非事务 | ❌ 待验证 |
| IF NOT EXISTS | ❌ 不支持 | ❌ 不支持 | — |

## 容器配置

当前无 GoldenDB Oracle 租户 Docker 镜像，需使用真实环境。

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

同 Oracle E2E 验证流程，指向 GoldenDB 服务端。
