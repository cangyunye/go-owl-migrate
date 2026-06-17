# OceanBase Oracle 租户 E2E 测试方案

## 方言信息

| 属性 | 值 |
|------|-----|
| 注册名 | `oceanbase-oracle` |
| 父方言 | Oracle |
| 数据库驱动 | go-ora |
| 元数据提取器 | Oracle (`normalizeDBType` → `oracle`) |
| 端口 | 2881 (OceanBase Oracle 默认) |

## 已覆盖的 Override

| 组件 | 状态 | 说明 |
|------|------|------|
| TypeMapper | ✅ `obOracleTypeMapper` | 继承 Oracle，仅改 Name |
| IdentifierQuoter | ✅ 继承 Oracle | 双引号引用，大写 |
| Features | ✅ `obOracleFeatures` | `TruncateIsTransactional: true` |
| DDLBuilder | ✅ `obOracleDDLBuilder` | `BuildCreateIndex` override — BITMAP 检测 |
| DMLHelper | ✅ 继承 Oracle | OFFSET...FETCH 分页 |

## 差异点

| 差异 | OceanBase Oracle | 标准 Oracle | 代码状态 |
|------|-----------------|------------|---------|
| TRUNCATE 事务安全 | ✅ 是 | ❌ 否 | ✅ `obOracleFeatures` |
| BITMAP 索引 | ❌ 不支持 | ✅ 支持 | ✅ `BuildCreateIndex` 输出 `-- MANUAL` |
| BFILE 类型 | ❌ 不支持 | ✅ 支持 | ✅ 测试已验证（映射到 LBVarBinary） |
| 分区语法差异 | 有差异 | 标准 | ❌ 未实现 |
| XML DB 功能 | 部分 | 完整 | ❌ 未追踪 |

## 容器配置

```yaml
oceanbase-oracle:
  image: oceanbase/oceanbase-ce:latest
  environment:
    OB_MODE: oracle         # Oracle 兼容模式
    OB_ROOT_PASSWORD: Oracle123!
    OB_DATABASE: XEPDB1
  ports:
    - "2881:2881"
```

## 测试用例矩阵

| 对象类型 | 预期 | 说明 |
|---------|------|------|
| TABLE | ✅ 继承 Oracle | `CREATE TABLE "SCH"."TBL"` |
| INDEX | ✅ OB override | BITMAP 索引输出 `-- MANUAL` 注释；其他继承 Oracle |
| VIEW | ✅ 继承 Oracle | `CREATE VIEW "SCH"."NAME" AS` |
| SEQUENCE | ✅ 继承 Oracle | `CREATE SEQUENCE "NAME" ...` |
| SYNONYM | ✅ 继承 Oracle | `CREATE SYNONYM "SCH"."NAME" FOR` |
| TRIGGER | ✅ 继承 Oracle | `CREATE OR REPLACE TRIGGER "NAME"` |
| FUNCTION | ✅ 继承 Oracle | `CREATE OR REPLACE FUNCTION` |
| PACKAGE | ✅ 继承 Oracle | `CREATE OR REPLACE PACKAGE` |
| MVIEW | ✅ 继承 Oracle | `CREATE MATERIALIZED VIEW` |

## 验证步骤

```bash
# 1. 准备测试数据
# 使用 Oracle SQL 语法创建 SCOTT 模式对象

# 2. 生成 DDL
mkdir -p /tmp/e2e/oceanbase-oracle/
go run ./cmd/migrate/main.go export ddl \
  -c <config> -o /tmp/e2e/oceanbase-oracle/

# 3. 验证 BITMAP 索引处理
# 检查索引 DDL 文件中是否包含 -- MANUAL 注释（对于 BITMAP 索引）

# 4. 验证 TRUNCATE 事务安全
# 确认 OB Oracle 的 Features.TruncateIsTransactional() 返回 true
```
