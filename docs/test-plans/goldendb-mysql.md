# GoldenDB MySQL 租户 E2E 测试方案

## 方言信息

| 属性 | 值 |
|------|-----|
| 注册名 | `goldendb-mysql` (裸别名 `goldendb` → `goldendb-mysql`) |
| 父方言 | MySQL |
| 数据库驱动 | go-sql-driver/mysql |
| 元数据提取器 | MySQL (`normalizeDBType` → `mysql`) |
| 端口 | 3306 (与 MySQL 一致) |

## 已覆盖的 Override

| 组件 | 状态 | 说明 |
|------|------|------|
| TypeMapper | ✅ 继承 MySQL 100% | `gdbMySQLTypeMapper` 仅改 Name |
| IdentifierQuoter | ✅ 继承 MySQL 100% | 反引号引用 |
| Features | ✅ 继承 MySQL 100% | MySQL feature flags |
| DDLBuilder | ✅ 继承 MySQL 100% | 所有 DDL 生成方法继承 MySQL |
| DMLHelper | ✅ 继承 MySQL 100% | LIMIT 分页 |

## 差异点（无，100% 等同于 MySQL）

GoldenDB MySQL 租户当前完全继承 MySQL 方言，无任何 override。

## 容器配置

```yaml
goldendb-mysql:
  image: cescude/goldendb:latest  # 或官方 GoldenDB 镜像
  environment:
    MYSQL_ROOT_PASSWORD: root123456
    MYSQL_DATABASE: default_db
  ports:
    - "3307:3306"
```

## 测试用例矩阵

| 对象类型 | 预期 | 命令行验证 |
|---------|------|-----------|
| TABLE | ✅ 继承 MySQL | `owl-migrate export ddl -t goldendb-mysql` |
| INDEX | ✅ 继承 MySQL | 输出文件含 `` CREATE INDEX `name` ON `schema`.`table` `` |
| VIEW | ✅ 继承 MySQL | 输出文件含 `` CREATE VIEW `schema`.`name` AS `` |
| SEQUENCE | ⚪ MySQL 空桩 | GoldenDB 可能支持序列，需实测 |
| TRIGGER | ✅ 继承 MySQL | 输出文件含 `` CREATE TRIGGER `` |
| FUNCTION | ✅ 继承 MySQL | 输出文件含 `` CREATE FUNCTION `` |

## 验证步骤

```bash
# 1. 启动容器（如使用真实 GoldenDB 实例）
docker compose up -d goldendb-mysql

# 2. 在 GoldenDB 中创建测试对象（用 MySQL 语法）
docker exec -i goldendb-mysql mysql -uroot -proot123456 default_db < testdata/db/mysql/setup.sql

# 3. 生成 DDL
mkdir -p /tmp/e2e/goldendb-mysql/
go run ./cmd/migrate/main.go export ddl \
  -c <config-with-goldendb-mysql> -o /tmp/e2e/goldendb-mysql/

# 4. 验证输出
ls /tmp/e2e/goldendb-mysql/*.sql
# 预期：.table.sql, .index.sql, .view.sql, .trigger.sql, .function.sql
```
