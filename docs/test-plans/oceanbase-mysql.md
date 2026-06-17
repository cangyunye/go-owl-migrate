# OceanBase MySQL 租户 E2E 测试方案

## 方言信息

| 属性 | 值 |
|------|-----|
| 注册名 | `oceanbase-mysql` (裸别名 `oceanbase` → `oceanbase-mysql`) |
| 父方言 | MySQL |
| 数据库驱动 | go-sql-driver/mysql |
| 元数据提取器 | MySQL (`normalizeDBType` → `mysql`) |
| 端口 | 2881 (OceanBase MySQL 默认) |

## 已覆盖的 Override

| 组件 | 状态 | 说明 |
|------|------|------|
| TypeMapper | ✅ `obMySQLTypeMapper` | 继承 MySQL，仅改 Name |
| IdentifierQuoter | ✅ 继承 MySQL | 反引号引用 |
| Features | ✅ `obMySQLFeatures` | `TruncateIsTransactional: true` |
| DDLBuilder | ✅ `obMySQLDDLBuilder` | 继承 MySQL + `BuildCreateSequence` override |
| DMLHelper | ✅ 继承 MySQL | LIMIT 分页 |

## 差异点

| 差异 | OceanBase MySQL | 标准 MySQL | 代码状态 |
|------|----------------|------------|---------|
| TRUNCATE 事务安全 | ✅ 是 | ❌ 否 | ✅ 已实现 |
| SEQUENCE 支持 | ✅ 支持 | ❌ 无原生序列 | ✅ `BuildCreateSequence` override |
| FULLTEXT 索引 | ❌ 不支持 | ✅ 支持 | ⚠️ Features 接口无对应方法 |
| MyISAM 引擎 | ❌ 仅 InnoDB | ✅ 支持 | ⚠️ 无代码差异（Engine 字段为空则不输出） |

## 容器配置

```yaml
oceanbase-mysql:
  image: oceanbase/oceanbase-ce:latest
  environment:
    OB_ROOT_PASSWORD: root123456
    OB_DATABASE: test
  ports:
    - "2881:2881"
```

## 测试用例矩阵

| 对象类型 | 预期 | 命令行验证 |
|---------|------|-----------|
| TABLE | ✅ 继承 MySQL | `` CREATE TABLE `sch`.`tbl` `` |
| INDEX | ✅ 继承 MySQL | `` CREATE INDEX `name` ON `sch`.`tbl` `` |
| VIEW | ✅ 继承 MySQL | `` CREATE VIEW `sch`.`name` AS `` |
| SEQUENCE | ✅ OB 特有 | `` CREATE SEQUENCE `sch`.`name` START WITH n `` — 验证 OceanBase 独有功能 |
| TRIGGER | ✅ 继承 MySQL | `` CREATE TRIGGER `` |
| FUNCTION | ✅ 继承 MySQL | `` CREATE FUNCTION `` |

## 验证步骤

```bash
# 1. 准备测试数据（MySQL 语法兼容）
docker exec -i oceanbase mysql -h127.0.0.1 -P2881 -uroot -proot123456 test < testdata/db/mysql/setup.sql

# 2. 生成 DDL
mkdir -p /tmp/e2e/oceanbase-mysql/
go run ./cmd/migrate/main.go export ddl \
  -c <config> -o /tmp/e2e/oceanbase-mysql/

# 3. 验证序列 DDL 产出
cat /tmp/e2e/oceanbase-mysql/*.sequence.sql
# 预期输出包含: CREATE SEQUENCE `testdb`.`seq_orders` START WITH 1 ...
```
