# OpenGaussDB E2E 测试方案

## 方言信息

| 属性 | 值 |
|------|-----|
| 注册名 | `opengaussdb` |
| 父方言 | PostgreSQL |
| 数据库驱动 | lib/pq (PG 协议) |
| 元数据提取器 | PostgreSQL (`normalizeDBType` → `postgres`) |
| 端口 | 5432 (容器映射 5433) |

## 已覆盖的 Override

| 组件 | 状态 | 说明 |
|------|------|------|
| TypeMapper | ✅ `ogTypeMapper` | 继承 PG，仅改 Name |
| IdentifierQuoter | ✅ 继承 PG | 双引号引用，case-preserving |
| Features | ✅ 继承 PG 100% | PostgreSQL feature flags |
| DDLBuilder | ✅ 继承 PG 100% | 所有 DDL 方法继承 PG |
| DMLHelper | ✅ 继承 PG | LIMIT...OFFSET 分页 |

## 差异点

OpenGaussDB 基于 PostgreSQL 内核，当前 100% 继承 PG 方言。

| 差异 | OpenGaussDB | 标准 PostgreSQL | 状态 |
|------|------------|----------------|------|
| 元数据提取 | 可能使用不同系统表 | information_schema | ❌ 待实测确认 |
| 序列语法 | 可能使用 `NOCYCLE` | `NO CYCLE` | ❌ 待实测确认 |
| MOT 引擎 | 支持内存优化表 | 无 | ❌ 未实现 |
| 特定 PG 扩展 | 部分不支持 | 完整 | ❌ 待实测 |
| 类型系统 | 可能有差异 | 标准 | ❌ 待实测 |

## 容器配置（已在 docker-compose.yaml 中）

```yaml
opengauss:
  image: opengauss/opengauss-server:latest
  pull_policy: never
  container_name: opengauss
  privileged: true
  environment:
    GS_PASSWORD: OpenGauss@123
  volumes:
    - opengauss-data:/var/lib/opengauss
  ports:
    - "5433:5432"
```

## 测试用例矩阵

| 对象类型 | 预期 | 说明 |
|---------|------|------|
| TABLE | ✅ 继承 PG | 双引号引用 |
| INDEX | ✅ 继承 PG | CREATE [UNIQUE] INDEX "name" |
| VIEW | ✅ 继承 PG | CREATE VIEW "name" AS |
| SEQUENCE | ✅ 继承 PG | CREATE SEQUENCE — 需验证 `NOCYCLE` vs `NO CYCLE` |
| TRIGGER | ✅ 继承 PG | EXECUTE FUNCTION |
| FUNCTION | ✅ 继承 PG | $$ LANGUAGE plpgsql |
| MVIEW | ✅ 继承 PG | CREATE MATERIALIZED VIEW |

## 验证步骤

```bash
# 1. 启动容器
docker compose -f testdata/db/docker-compose.yaml up -d opengauss

# 2. 等待就绪
sleep 30
docker exec opengauss gsql -U gaussdb -d postgres -c "SELECT 1"

# 3. 创建测试数据
docker exec -i opengauss gsql -U gaussdb -d postgres < testdata/db/postgres/setup.sql

# 4. 生成 DDL
mkdir -p /tmp/e2e/opengaussdb/
go run ./cmd/migrate/main.go export ddl \
  -c <config-with-opengaussdb> -o /tmp/e2e/opengaussdb/

# 5. 验证输出
ls /tmp/e2e/opengaussdb/*.sql
```

### 注意

- 连接 OpenGaussDB 需要使用 PG 协议驱动，DSN 格式与 PG 相同
- OpenGaussDB 默认用户为 `gaussdb`（非 `postgres`）
- 容器启动可能较慢（MOT 引擎初始化）
- 某些 `information_schema` 查询可能与 PG 有差异，需实际验证提取器是否工作
