# PanWeiDB PG 模式 E2E 测试方案

## 方言信息

| 属性 | 值 |
|------|-----|
| 注册名 | `panweidb` |
| 父方言 | PostgreSQL |
| 数据库驱动 | lib/pq (PG 协议) |
| 元数据提取器 | PostgreSQL (`normalizeDBType` → `postgres`) |
| 端口 | 5432 (PG 协议) |

## 已覆盖的 Override

| 组件 | 状态 | 说明 |
|------|------|------|
| TypeMapper | ✅ `pdbTypeMapper` | 继承 PG，仅改 Name |
| IdentifierQuoter | ✅ 继承 PG | 双引号引用，case-preserving |
| Features | ✅ 继承 PG | PG feature flags (Transactional DDL, IF NOT EXISTS, etc.) |
| DDLBuilder | ✅ 继承 PG 100% | 所有 DDL 方法继承 PG |
| DMLHelper | ✅ 继承 PG | LIMIT...OFFSET 分页 |

## 差异点

PanWeiDB PG 模式基于 openGauss/PostgreSQL 内核，当前 100% 继承 PG 方言。

| 差异 | PanWeiDB PG | 标准 PostgreSQL | 状态 |
|------|------------|----------------|------|
| TRUNCATE 事务安全 | ✅ PG 内核特性 | ✅ 事务安全 | — |
| 序列语法 | 可能使用 `NOCYCLE` | `NO CYCLE` | ❌ 待实测确认 |
| 元数据提取 | openGauss 系统表可能不同 | information_schema | ❌ 待实测确认 |

## 容器配置

```yaml
panweidb:
  image: enmotech/panweidb:latest  # 或官方 PanWeiDB 镜像
  environment:
    POSTGRES_PASSWORD: panweidb123
    POSTGRES_DB: testdb
  ports:
    - "5433:5432"
```

## 测试用例矩阵

| 对象类型 | 预期 |
|---------|------|
| TABLE | ✅ 继承 PG |
| INDEX | ✅ 继承 PG |
| VIEW | ✅ 继承 PG |
| SEQUENCE | ✅ 继承 PG |
| TRIGGER | ✅ 继承 PG (`EXECUTE FUNCTION`) |
| FUNCTION | ✅ 继承 PG ($$ 美元引用 + LANGUAGE) |
| MVIEW | ✅ 继承 PG |

## 验证步骤

同 PostgreSQL E2E 验证流程，指向 PanWeiDB 服务端。
