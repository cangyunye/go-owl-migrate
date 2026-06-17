# PanWeiDB MySQL 兼容模式 (B 模式) E2E 测试方案

## 方言信息

| 属性 | 值 |
|------|-----|
| 注册名 | `panweidb-mysql` |
| 父方言 | MySQL (Dolphin 插件) |
| 数据库驱动 | lib/pq (PG 协议，非 MySQL 协议) |
| 元数据提取器 | MySQL (`normalizeDBType` → `mysql`) |
| 端口 | 5432 (PG 协议) |

## 已覆盖的 Override

| 组件 | 状态 | 说明 |
|------|------|------|
| TypeMapper | ✅ `pdbMySQLTypeMapper` | 继承 MySQL 类型映射 |
| IdentifierQuoter | ✅ 继承 MySQL | 反引号引用 |
| Features | ✅ `postgres.PGFeatures{}` | ⚠️ 使用 PG 而非 MySQL 的 feature flags |
| DDLBuilder | ✅ 继承 MySQL 100% | 所有 DDL 方法继承 MySQL |
| DMLHelper | ✅ 继承 MySQL | LIMIT 分页 |

## 差异点

| 差异 | PanWeiDB MySQL | 标准 MySQL | 代码状态 |
|------|----------------|------------|---------|
| 数据库驱动 | lib/pq (PG 协议) | go-sql-driver | ✅ `openDB` 已区分 |
| TRUNCATE 事务安全 | ✅ openGauss 内核 | ❌ 否 | ✅ Features 用 PG |
| 标识符引用 | 反引号 | 反引号 | ✅ 继承 MySQL |
| ENGINE 子句 | ❌ 忽略 | ✅ 支持 | ⚠️ 无代码差异（Engine 字段为空则不输出） |

## 容器配置

```yaml
panweidb-mysql:
  image: enmotech/panweidb:latest
  environment:
    PANWEIDB_MODE: mysql   # B 模式
    POSTGRES_PASSWORD: panweidb123
  ports:
    - "5434:5432"
```

## 测试用例矩阵

| 对象类型 | 预期 | 说明 |
|---------|------|------|
| TABLE | ✅ 继承 MySQL | 反引号引用，但 ENGINE= 无意义 |
| INDEX | ✅ 继承 MySQL | `` CREATE INDEX `name` ON `sch`.`tbl` `` |
| VIEW | ✅ 继承 MySQL | `` CREATE VIEW `sch`.`name` AS `` |
| SEQUENCE | ⚪ MySQL 空桩 | 但 PanWeiDB 基于 openGauss 可能支持序列，需实测 |
| TRIGGER | ✅ 继承 MySQL | `` CREATE TRIGGER `` |
| FUNCTION | ✅ 继承 MySQL | `` CREATE FUNCTION `` |

## 验证步骤

同 MySQL E2E 验证流程，指向 PanWeiDB B 模式服务端。注意 DSN 使用 PG 协议格式而非 MySQL DSN。
