# DSN 速查 · 构建口味 · 配置模板

## 方言与构建口味

| 方言名 | 构建口味 | 说明 |
|---|---|---|
| oracle / postgres(postgresql) / mysql(mariadb) | 基础版 `make build` | 二进制 `owl-migrate` |
| oceanbase / oceanbase-mysql / oceanbase-oracle | `make build/ob` | 二进制 `owl-migrate-ob` |
| opengaussdb / opengaussdb-mysql / opengaussdb-oracle / panweidb(-mysql/-oracle) | `make build/og` | 二进制 `owl-migrate-og` |
| goldendb / goldendb-mysql / goldendb-oracle | `make build/gdb` | 二进制 `owl-migrate-gdb` |
| sqlite3 | `make build/sqlite3` | 二进制 `owl-migrate-sqlite3` |
| duckdb | `make build/duckdb` | 二进制 `owl-migrate-duckdb` |
| 全部 | `make build/full` | 二进制 `owl-migrate-full`（ob+og+gdb） |

产物路径：`build/<goos>-<goarch>/<二进制名>`。用基础版操作复合方言会报 unsupported——先构建对应口味。裸名自动归一化：`oceanbase`→oceanbase-mysql、`goldendb`→goldendb-mysql。

不支持的目标方言：Kingbase（金仓）、原生 GaussDB 等未列名字。**但连接层不受此限**：金仓/达梦/TimesTen 可经 agent 通道作为连接类型使用（见下「连接通道」），只是不能当目标方言。

## DSN 速查

| 方言 | 格式 |
|---|---|
| oracle | `oracle://user:pass@host:1521/service_name` |
| mysql | `user:pass@tcp(host:3306)/db?charset=utf8mb4` |
| oceanbase-mysql | `user@tenant:pass@tcp(host:2881)/db` |
| oceanbase-oracle 直连 | `oceanbase-oracle://user@tenant:pass@host:2881/db` |
| oceanbase-oracle OBProxy | `oceanbase-oracle://user@tenant#cluster:pass@host:2883/db`（#集群名紧跟租户，端口 2883） |
| postgres / opengaussdb / panweidb 系 | `host=... port=5432 user=... password=... dbname=... sslmode=disable` |
| sqlite3 / duckdb | `/path/to/database.db`（嵌入库，无 schema） |

OceanBase 租户模式由 `ob_compatibility_mode` 自动探测；不符时报错，可用 `compat_mode: mysql|oracle` 显式指定。

## 连接通道（v0.5.1+：native / agent / auto）

| 取值 | 行为 |
|---|---|
| `native`（默认） | 永远 Go 原生驱动，与旧版一致，不起 JVM |
| `auto` | 有原生驱动走 native（连接失败不回退）；没有则自动走 agent |
| `agent` | 强制走 owljdbc agent（JVM sidecar + JDBC 驱动） |

- 全局 flag：`--channel native|agent|auto`（**flag 优先**于配置 `source.channel`/`target.channel`）、`--jars-dir <dir>`。
- agent 通道前提：PATH 有 java（或配置 `agent.java_home`）；驱动 jar 放 `agent.jars_dir`（默认当前工作目录）；`owl-agent.jar` 缺失时首次连接自动从 owljdbc release 下载（3 次重试；离线用环境变量 `OWLJDBC_AGENT_JAR_URL` 指内网镜像）。常用驱动 jar：`bash owljdbc/scripts/fetch-jars.sh`。
- **agent 通道不受构建口味限制**：任意口味二进制都能经 agent 接入内置 catalog 类型——oceanbase 双租户、mysql、postgres、oracle、goldendb 双族、**dm（达梦，互迁已实测）**、**kingbase（金仓，postgres 族）**、opengaussdb、**timesten**（oracle 族，dsn_raw 型）。dm/timesten→oracle 族、kingbase→postgres 族决定字典/分页/占位符语义。
- 配置段：全局 `agent: {jars_dir, agent_jar, java_home}`；`source/target.agent` 可按连接覆盖。
- 自检：`owl-migrate version`（本二进制已链接驱动与方言）；serve 端 `GET /api/v1/capabilities`（逐类型 native/agent 可用性 + jar 探测）。

## owljdbc.profiles —— 新数据库类型免改代码接入 agent 通道

注册**随配置走**（CLI 读配置即生效；serve 上传 YAML/`PUT /config`/激活即时生效；worker 子进程经同一配置重注册），不是全局注册表。注册同名 type 会**覆盖**内置 profile。

```yaml
owljdbc:
  profiles:
    mydb:                                   # 自定义 type 名
      driver_class: com.mydb.jdbc.Driver    # 必填：JDBC 驱动类
      jar_globs: ["mydb-jdbc-*.jar"]        # 必填：驱动 jar glob（放 agent.jars_dir）
      family: mysql                         # 必填：oracle | mysql | postgres
      url_template: "jdbc:mydb://{host}:{port}/{database}?p1=v1&p2=v2"
      dsn_syntax: url                       # 建议：url | pg-kv | mysql-tcp | kv
    # dsn 即完整 JDBC URL 时（如 TimesTen 手工 client DSN）：
    # tt: {driver_class: ..., jar_globs: [...], family: oracle, dsn_raw: true}
```

- 模板规则：**必须含 `{host}`**；占位符只有 `{host}` `{port}` `{database}`；其余是字面量（自定义 JDBC 参数全写在 `?` 后）；**凭据不走模板**（经连接参数直传 sidecar，不进 URL/日志）。`dsn_raw: true` 时忽略模板，dsn 原样透传，**账密需含在 URL 中**。
- 内置类型的 JDBC URL 由工具按内置模板拼死（含默认参数如 `useSSL=false`），DSN 上写的 query 参数**不会**并入；要自定义 JDBC 参数（编码、兼容模式、压缩等）就必须走本节注册（或同名覆盖内置 type）。
- `dsn_syntax` 四语法：`url`=`scheme://user:pass@host:port/db`；`mysql-tcp`=`user:pass@tcp(host:port)/db`；`pg-kv`=libpq 键值；`kv`=分号 `KEY=VALUE`（键名大小写不敏感，识别 host/port/user/password/database 等常见别名）。
- 密码特殊字符（`@ : / # ?`）：`url` 语法需百分号转义（`AA@1122#`→`AA%401122%23`）；`mysql-tcp`/`kv` 不用。
- 目标端注意：注册的是**连接层**类型；`ddl.target_dialect` 仍须指向兼容的内置方言（与 `family` 通常一致）。
- 排查：`unsupported database type`=该配置没有注册段；`no jar matching`=glob 没匹配到 jars_dir；认证失败但账密正确=dsn_syntax 选错或未转义。详见 `docs/config.md`「外部 profile 注册」。

## 配置文件搜索顺序

`-c` flag > `./migrate.yaml`（当前目录）> `$OWL_MIGRATE_CONFIG` > `~/.owl/migrate/migrate.yaml`。
`~/migrate.yaml`（家目录根）不在搜索路径——要么挪到以上位置，要么 `-c` 显式指定。

## 模板 1：最小源库配置（仅元数据提取/导出用）

```yaml
source:
  type: oracle
  dsn: "oracle://scott:tiger@10.1.1.5:1521/XEPDB1"
  schema: SCOTT
metadata:
  type: database
```

## 模板 2：完整迁移配置

```yaml
general:
  log_level: info
source:
  type: oracle
  dsn: "oracle://scott:tiger@10.1.1.5:1521/XEPDB1"
  schema: SCOTT
target:
  type: postgres
  dsn: "host=10.1.1.6 port=5432 user=postgres password=pg123 dbname=app sslmode=disable"
ddl:
  target_dialect: postgres      # 默认继承 target.type
  schema_mapping:
    SCOTT: public               # 仅 schema 级映射（无表级重命名）
  include_if_not_exists: true
  table_filter:                 # 注意：当前未接线，表范围请用 --tables / export.tables.include
    include: ["*"]
export:
  output_dir: ./output/data/
  format: csv
  parallel:
    enabled: false
    max_workers: 4
  tables:
    include: ["*"]              # 表白名单：SCOTT.EMP / SCOTT.* / *.T / T_*
  filters:                      # WHERE 条件导出（字面片段；执行前条件 COUNT 门禁）
    "SCOTT.EMP": "deptno = 20"
  columns:                      # 列投影/改名（include 列表顺序=输出顺序；PK 不可丢）
    include: {"SCOTT.EMP": ["empno", "sal", "ename"]}
    rename: {"SCOTT.EMP": {SAL: salary}}
import:
  source_dir: ./output/data/
  batch:
    error_policy: stop          # skip_row | stop | log_only
    use_copy: false             # PG COPY 提速
  data_transforms:
    source_encoding: ""         # 如 GBK
    datetime_format: ""         # 紧凑模板：yyyyMMddHHmmss / yyyyMMdd
    trim_strings: false
    null_if: []
```

## 模板 3：离线 CSV 元数据（不连源库）

```yaml
metadata:
  type: csv
  csv:
    path: ./meta_csv/           # 目录下放 tables.csv（必需）、columns.csv 等 13 张规范表
ddl:
  target_dialect: postgres
```

## 数据源引用

数据源（Web 端配置，存 `~/.owl/migrate/datasources/<name>.yaml`，DSN AES-256-GCM 加密）可在配置里引用，serve 端解析后替换真实 DSN：

```yaml
source:
  type: oracle
  dsn: "datasource:生产库"
```
