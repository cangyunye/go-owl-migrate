# 测试数据库环境（docker-compose）

为 `go-owl-migrate` 的 E2E 测试提供 Oracle / PostgreSQL / MySQL / OpenGauss 四个数据库，
**首次启动自动初始化测试数据**（SCOTT / DEPT / EMP / BONUS），无需手动导数。

## 一键启动

```bash
cd testdata/db
docker compose up -d            # 启动 Oracle / PostgreSQL / MySQL

# 等待各容器变为 (healthy)
docker compose ps
```

或在仓库根目录：

```bash
docker compose -f testdata/db/docker-compose.yaml up -d
```

> **OpenGauss 默认不启动**：官方镜像仅 amd64，macOS (Apple Silicon) 下不可用。
> Linux 用户启用：`docker compose --profile opengauss up -d`

重置环境（删除所有数据，下次启动重新初始化）：

```bash
docker compose down -v
```

## 服务一览

| 服务 | 镜像 | 宿主端口 | 用户 / 密码 | 库 / PDB |
|------|------|---------|-------------|----------|
| Oracle | gvenzl/oracle-free:23-slim（私有源镜像） | 1521 | `scott` / `tiger`（初始化脚本自动创建） | PDB `XEPDB1` |
| PostgreSQL | postgres:15-alpine | 5432 | `postgres` / `postgres123` | `postgres_db` |
| MySQL | mysql:8.4 | 3306 | `root` / `root123456` | `default_db` |
| OpenGauss（profile `opengauss`，仅 Linux） | opengauss/opengauss-server | **5433** → 容器内 5432 | `gaussdb` / `OpenGauss@123` | `postgres` |

## owl-migrate 配置 DSN 速查

```yaml
# Oracle
source:
  type: oracle
  dsn: "oracle://scott:tiger@127.0.0.1:1521/XEPDB1"

# PostgreSQL
target:
  type: postgres
  dsn: "host=127.0.0.1 port=5432 user=postgres password=postgres123 dbname=postgres_db sslmode=disable"

# MySQL
source:
  type: mysql
  dsn: "root:root123456@tcp(127.0.0.1:3306)/default_db"

# OpenGauss（注意端口是 5433，用户是 gaussdb）
source:
  type: opengaussdb
  dsn: "host=127.0.0.1 port=5433 user=gaussdb password=OpenGauss@123 dbname=postgres sslmode=disable"
```

完整示例配置见本目录 `oracle_to_pg.yaml`、`mysql_to_pg.yaml`、`opengauss_to_pg.yaml` 等。

## 初始化行为

仅在**数据卷为空的首次启动**时执行，之后启动直接复用数据卷：

| 数据库 | 初始化脚本 | 内容 |
|--------|-----------|------|
| Oracle | `oracle/scott_seed.sql`（由 `init-scott.sh` 以 SYS 身份执行） | SCOTT 用户 + DEPT/EMP 数据 + 视图/序列/同义词/触发器/函数/包 |
| PostgreSQL | `postgres/seed_tables.sql` + `postgres/setup.sql` | DEPT/EMP/BONUS 数据 + 索引/视图/序列/函数/触发器 |
| MySQL | `mysql/setup.sql` | DEPT/EMP 表与数据 |
| OpenGauss | 无自动初始化 | 需手动导入，见下节 |

### OpenGauss 手动导入（可选）

OpenGauss 镜像不支持 init 脚本目录，如需测试表请手动导入：

```bash
docker exec -i opengauss gsql -U gaussdb -d postgres < postgres/seed_tables.sql
docker exec -i opengauss gsql -U gaussdb -d postgres < postgres/setup.sql
```

## 注意事项

- **OpenGauss 仅限 Linux**：官方无 ARM 镜像，且 macOS 下模拟运行不可用，故默认不启动（需 `--profile opengauss`）。
- **Oracle 镜像来自私有源** `docker.xuanyuan.run`：首次拉取需网络可达；已有本地镜像则直接使用。
  本环境 Docker Hub 直连不通时，可经镜像源拉取后打回官方 tag，例如：
  `docker pull docker.xuanyuan.run/library/mysql:8.4 && docker tag docker.xuanyuan.run/library/mysql:8.4 mysql:8.4`
- **端口冲突**：若宿主 3306/5432/1521/5433 被占用，修改 compose 的 `ports` 映射后同步修改配置中的 DSN。
- 历史遗留：旧版 compose 曾把 MySQL/PG 数据挂到外置硬盘绝对路径（`/Volumes/...`），现已统一改为
  named volume（`mysql-data` / `postgres-data`），旧目录不再使用，可自行清理。
