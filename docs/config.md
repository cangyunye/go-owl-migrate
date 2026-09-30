# Configuration Reference

This document describes all configuration options for `go-owl-migrate`.

You can generate an initial config file with the `init` command:

```bash
owl-migrate init --source-type oracle --source-dsn "..." --source-schema SCOTT \
  --target-type postgres --target-dsn "..." --target-schema public \
  -o ./migrate.yaml
```

## Config File Resolution

When no `-c` flag is given, the config file is resolved in this order:

1. `./migrate.yaml` (current directory, if it exists)
2. `$OWL_MIGRATE_CONFIG` (environment variable)
3. `~/.owl/migrate/migrate.yaml` (global default)

The `init` command always writes to `./migrate.yaml` by default (use `-o` to override).

## Environment Variables

Tool-level state defaults to `~/.owl/migrate/` (isolated from go-owl's `~/.owl/`).
Override with environment variables:

| Variable | Purpose | Default |
|----------|---------|---------|
| `OWL_MIGRATE_HOME` | Root directory for tool state | `~/.owl/migrate` |
| `OWL_MIGRATE_CONFIG` | Global config file path | `~/.owl/migrate/migrate.yaml` |
| `OWL_MIGRATE_DB_PATH` | SQLite database (serve mode) | `~/.owl/migrate/owl-migrate.db` |
| `OWL_MIGRATE_LOG_DIR` | Log directory | `~/.owl/migrate/logs` |

Project-level outputs (DDL, SELECT, INSERT, data export, checkpoints) remain
CWD-relative under `./output/` and are controlled by CLI flags (`-o`, `--temp-dir`).

## Full Config Structure

```yaml
general:
  log_level: debug                          # debug | info | warn | error (default: info)
  log_file: /var/log/owl-migrate.log        # Log output file (optional)
  log_format: text                          # text | json

metadata:
  type: database                            # "csv" | "xlsx" | "database"
  csv:
    path: ./metadata/                       # required when type=csv
    delimiter: ","                          # CSV field delimiter (default: ",")
    encoding: "utf-8"                       # CSV file encoding
    has_header: true                        # CSV has header row (default: true)
    null_marker: "\\N"                      # NULL representation in CSV (default: "\N")
    column_name_matching: "case_insensitive" # Column name matching mode
  xlsx:
    path: ./metadata/schema.xlsx             # required when type=xlsx
    data_output_dir: ./output/data/          # @sheet data CSV output

agent:                                      # 全局 agent 通道默认（source/target 未单独配置时生效）
  jars_dir: ""                              # owl-agent.jar 与驱动 jar 的检索目录（留空 = 当前目录）
  agent_jar: ""                             # owl-agent.jar 显式路径（可选；缺失时首次 agent 连接自动下载）
  java_home: ""                             # JRE 路径（留空 = 用 PATH 里的 java）
  # jar 自动下载源可用环境变量 OWLJDBC_AGENT_JAR_URL 覆盖（内网镜像）。

owljdbc:                                    # 外部 agent 通道 profile 注册(新数据库类型免改代码接入)
  profiles:
    mydb:                                   # 自定义 type 名(注册后即可用于 source.type/target.type + channel: agent/auto)
      driver_class: com.mydb.jdbc.Driver    # JDBC 驱动类(sidecar 加载)
      jar_globs: ["mydb-jdbc-*.jar"]        # 驱动 jar 文件名 glob(放 agent.jars_dir,任一匹配)
      family: oracle                        # 连接语义族: oracle | mysql | postgres(决定字典/占位符/分页行为)
      url_template: "jdbc:mydb://{host}:{port}/{database}"   # 支持 {host} {port} {database};凭据不走模板
      dsn_syntax: url                       # source/target.dsn 的语法: url | pg-kv | mysql-tcp | kv(分号 KEY=VALUE)
    # tt:                                   # DSN 本身就是完整 JDBC URL 时(如手工 client DSN):
    #   driver_class: com.timesten.jdbc.TimesTenDriver
    #   jar_globs: ["ttjdbc*.jar"]
    #   family: oracle
    #   dsn_raw: true

source:
  type: postgres                            # postgres | mysql | oracle | goldendb | oceanbase | panweidb | opengaussdb + owljdbc.profiles 注册的 type
  dsn: "host=127.0.0.1 port=5432 dbname=mydb user=u password=p sslmode=disable"
  schema: public
  channel: ""                               # 连接通道: native(默认) | agent | auto（见「连接通道」一节）
  agent:                                    # 仅本连接覆盖全局 agent 段（可选）
    jars_dir: ""
    agent_jar: ""
    java_home: ""
  compat_mode: ""                           # OceanBase 租户兼容模式: "mysql" | "oracle" (留空=连接后自动探测)
  connect_timeout: "30s"                    # Connection/ping timeout (e.g. 10s, 1m)
  query_timeout: ""                         # Overall operation timeout (e.g. 30m, 1h; empty = no limit)
  pool:                                     # Connection pool tuning (optional)
    max_open_conns: 10                      # Max open connections (default: 10)
    max_idle_conns: 5                       # Max idle connections (default: 5)
    conn_max_lifetime: "30m"                # Max connection lifetime (default: 30m)
    conn_max_idle_time: "5m"                # Max idle time before close (default: 5m)

ai:                                         # 可选：AI 对话路由层（serve /api/v1/ai/*）。密钥只存环境变量，绝不入配置文件
  provider: deepseek                        # 首轮单供应商 deepseek；custom 走 OpenAI 兼容 base_url
  base_url: ""                              # 留空 = 供应商预设根地址
  api_key_env: OWL_AI_API_KEY               # 密钥所在环境变量名（回退读 DEEPSEEK_API_KEY）
  model: deepseek-flash                     # deepseek-flash（V4.1-Flash，推理模型）/ deepseek-v4-pro
  context_window: 1048576                   # 仅本地预截断/会话预算用，不发给供应商
  effort: low                               # 路由思考强度 low|high|max（仅推理模型生效；路由用 low）
  plan_effort: high                         # 配置生成的思考强度（默认 high，生成质量优先）
  max_tokens: 32768                         # 思考 token 计入此预算，勿设过小
  timeout: 2m                               # 单次尝试 HTTP 超时
  max_repair_rounds: 3                      # 配置生成修复回路上限

target:
  type: mysql
  dsn: "root:pass@tcp(127.0.0.1:3306)/mydb"
  channel: ""                               # 同 source.channel
  agent: ""                                 # 同 source.agent（本连接覆盖）
  compat_mode: ""                           # OceanBase 租户兼容模式 (同 source)
  pool:                                     # Same pool options available for target
    max_open_conns: 10

ddl:
  target_dialect: mysql                     # Target DDL dialect（可省略：缺省时继承 target.type，
                                            # postgresql/mariadb 等别名自动归一）
  source_dialect: ""                        # Source dialect for cross-dialect type conversion (CSV/xlsx 元数据时必填)
  output_dir: ./output/ddl/                 # Output directory for DDL files
  include_if_not_exists: true               # Add IF NOT EXISTS
  include_comments: true                    # Include column/table comments
  include_drop: false                       # Generate DROP statements
  split_by_object: true                     # One file per object
  schema_mapping:                           # Map source schema to target schema
    public: myapp
    scott: SCOTT
  table_filter:
    include: ["*"]                          # Tables to include ("*" = all)
    exclude:
      glob: ["*_LOG", "TMP_*"]              # Glob pattern exclusion
      regex: ['^BIN\$']                     # Regex exclusion (e.g., Oracle recycle bin)
      schemas: ["SYS", "SYSTEM"]            # Schema exclusion
      tables: ["SCOTT.TEMP_DATA"]           # Exact table exclusion
  type_overrides: {}                        # Override specific type mappings
  column_types: {}                          # 按列类型覆盖，键 "SCHEMA.TABLE.COLUMN"（大小写不敏感，列级优先；支持 %l/%p/%s）
  identity_to_serial: false                 # Convert identity columns to SERIAL (PG)
  add_rowid_column: false                   # Add a ROWID column (Oracle targets)
  empty_string_to_null: false               # Convert '' to NULL (Oracle compatibility)
  boolean_mapping: {}                       # Custom boolean value mapping
  no_quote_identifiers: false               # Output bare identifiers without quoting (compatibility)
  partition:
    migrate: false                          # Include partition DDL

select_gen:
  output_dir: ./output/select/              # Output directory for SELECT files
  batch:
    method: cursor                          # pagination method: cursor/offset
    page_size: 5000                         # rows per batch
  include_row_number: false                 # Add ROW_NUMBER() column
  add_export_columns: false                 # Add export helper columns
  # 注意：select_gen.batch 只影响 gen-select 命令（生成 SELECT 语句）；
  # 真实数据导出走 export.batch。二者互不继承、默认值相同（cursor/5000），
  # 如需改动请同步修改，避免生成语句与实际导出的分页行为不一致。

export:
  output_dir: ./output/data/                # Output directory for exported data files
  format: csv                               # Output format: csv (default), sql, xlsx
  filters:                                  # WHERE 条件导出（glob 键 → 字面 SQL 片段；精确点名 > glob，多命中报错）
    "SCOTT.EMP": "deptno = 20 AND sal > 1000"
  filters_check: count                      # 条件 COUNT 门禁: count(默认，执行前校验条件并产源侧 expected) | off
  columns:                                  # 列投影/改名（include 列表顺序 = 输出顺序；PK 不可丢弃）
    include: {"SCOTT.EMP": ["empno", "sal", "ename"]}
    rename: {"SCOTT.EMP": {SAL: salary}}
  csv:
    delimiter: ","
    quote_char: "\""
    escape_char: ""                         # Escape character
    encoding: "utf-8"                       # CSV file encoding
    header: true
    null_representation: "\\N"
    line_terminator: "\n"
    null_overrides: {}                      # Per-column null value overrides
    empty_string_to_null: false             # Treat empty string as null
  batch:
    method: cursor                          # pagination: cursor/offset
    page_size: 5000
  parallel:
    enabled: true
    max_workers: 4
  tables:
    include: ["*"]                          # table filter list, "*" means all

import:
  source_dir: ./output/data/                # Directory containing CSV data files
  format: csv                               # Input format
  csv:
    delimiter: ","
    encoding: "utf-8"                       # CSV file encoding
    has_header: true
    null_marker: "\\N"
    null_identifiers:                       # Additional null recognition rules
      strings: []                           # Strings treated as null
      case_sensitive: false                 # Case-sensitive comparison
      regex: ""
    null_semantics:                         # Database-specific null semantics
      oracle_empty_string_is_null: false
      numeric_zero_not_null: false
  target:
    truncate_before: true                   # TRUNCATE table before import
    disable_constraints: false              # Disable FK constraints during import
    disable_triggers: false                 # Disable triggers during import
    drop_indexes: false                     # Drop and recreate indexes
  batch:
    commit_interval: 1000                   # rows per transaction
    error_policy: skip_row                  # stop | skip_row | log_only
    max_errors_before_stop: 0               # 0 = unlimited
    use_copy: false                         # PG 族目标启用 COPY 快速通道 (失败自动回退批量 INSERT)
  parallel:
    enabled: true
    max_workers: 4
    respect_foreign_keys: false             # true = 按外键依赖排序（父表先插入，自动串行）
  data_transforms:
    datetime_format: "yyyyMMddHHmmss"       # auto-convert compact datetime
    datetime_format_fallback: []            # Additional date format patterns
    datetime_truncate_to_target: false      # Truncate datetime to target precision
    trim_strings: true
    null_if: ["NULL", "null", "\\N"]
    source_encoding: ""                     # Source CSV encoding ("" = UTF-8, supports GBK, LATIN1, etc.)

extensions: {}                              # Custom extension configuration (reserved)
```

### 元数据对象导出（export-metadata）—— 非 yaml 配置键

元数据对象导出（`owl-migrate export-metadata`，serve 端 `POST
/api/v1/metadata/export` / 页面 `/export-metadata`）的**对象类型与范围不是
yaml 配置键**（`export` 段即上面的 `ExportConfig`，不含 `objects`/`scope`
字段），而是：

- CLI：`--objects`（13 个对象类型词干）与 `--scope`
  （`all | schema:NAME | table:GLOB[,GLOB] | schema:NAME:table:GLOB[,GLOB]`）flag；
- serve：请求体 `objects` / `scope` 字段，语法与 CLI 相同。

完整语法见 [CLI 命令参考](cli-commands.md) 的 `owl-migrate export-metadata`
一节。`export.tables.include` 语义不变，继续用于**数据导出（export data）与
DDL 生成**的表过滤；它不控制元数据对象导出的范围。

## Metadata Types

### `type: csv`

Load table/column definitions from CSV files. Required files in the metadata directory:

- `tables.csv` — table definitions (required)
- `columns.csv` — column definitions (required)
- `primary_keys.csv` — primary key constraints (optional)
- `indexes.csv` — index definitions (optional)
- `foreign_keys.csv` — FK definitions (optional)
- `sequences.csv` — sequence definitions (optional)
- `triggers.csv` — trigger definitions (optional)
- `functions.csv` — stored functions/procedures (optional)
- `views.csv` — view definitions (optional)
- `mviews.csv` — materialized view definitions (optional)
- `synonyms.csv` — synonym definitions (optional, Oracle)

See [CSV Metadata Format](csv-format.md) for detailed column specifications.

### `type: xlsx`

Load metadata from a single Excel (.xlsx) file. Sheets are parsed as follows:

- **Metadata sheets** (`tables`, `columns`, `primary_keys`, `indexes`, `foreign_keys`, `views`, `sequences`, `triggers`, `functions`, `synonyms`) — define the database schema, same format as CSV files
- **Data sheets** (`@TableName`) — provide data for a specific table; first row = column headers, remaining rows = data; the `@` prefix distinguishes data sheets from metadata sheets
- Cell types are converted to CSV values automatically

A `tables` sheet is **required**.

### `type: database`

Connect to the source database specified in `source.*` configuration to extract schema metadata via `information_schema` (PG/MySQL) or `ALL_*` dictionary views (Oracle).

Requires:
- `source.type` — database type
- `source.dsn` — connection string
- `source.schema` — schema name to extract

## Connection Strings (DSN)

`source.type` / `target.type` / `ddl.target_dialect` 支持的方言及对应连接串格式如下，
可直接复制替换占位符后使用：

| `type` 取值 | 连接串示例（可直接抄写） |
|---|---|
| `oracle` | `oracle://user:pass@host:1521/service_name` |
| `postgres` / `postgresql` | `host=127.0.0.1 port=5432 user=postgres password=pass dbname=mydb sslmode=disable` |
| `mysql` | `user:pass@tcp(host:3306)/dbname?charset=utf8mb4` |
| `sqlite3` | `/path/to/database.db` |
| `duckdb` | `/path/to/database.db` |
| `goldendb` / `goldendb-mysql` | `user:pass@tcp(host:3306)/dbname?charset=utf8mb4`（MySQL 兼容模式） |
| `goldendb-oracle` | `oracle://user:pass@host:1521/service_name`（Oracle 兼容模式） |
| `oceanbase` / `oceanbase-mysql` | `user@tenant:pass@tcp(host:2881)/dbname`（MySQL 兼容模式；用户名须带租户，如 `root@test`；2881 直连 OBServer，2883 走 OBProxy） |
| `oceanbase-oracle` | 直连 OBServer(2881):`oceanbase-oracle://sys@tenant:pass@host:2881/db`（**无需集群**）；OBProxy(2883) 多集群:用户名须带集群 `user@tenant#cluster`（`#` 由工具自动编码），如 `oceanbase-oracle://sys@tenant#cluster:pass@host:2883/db`；TNS:`oracle://user:pass@host:2883/service_name` |
| `panweidb` / `panweidb-mysql` / `panweidb-oracle` | `host=127.0.0.1 port=5432 user=postgres password=pass dbname=mydb sslmode=disable`（始终走 PG 协议） |
| `opengaussdb` | `host=127.0.0.1 port=5432 user=gaussdb password=pass dbname=postgres sslmode=disable`（默认用户 `gaussdb`；testdata/db 测试环境端口映射为 **5433**） |

### 各方言要点

- **Oracle 系**（`oracle`、`goldendb-oracle`）：go-ora 驱动，URL 格式 `oracle://user:pass@host:port/service_name`。
- **MySQL 系**（`mysql`、`goldendb`、`goldendb-mysql`、`oceanbase`、`oceanbase-mysql`）：go-sql-driver/mysql 格式 `user:pass@tcp(host:port)/dbname`。
- **PG 系**（`postgres`、`opengaussdb`、`panweidb` 全系）：`host=... port=... user=... password=... dbname=... sslmode=...` 键值对格式。**注意 PanWeiDB 即使声明 `panweidb-mysql` / `panweidb-oracle`，通信协议仍是 PostgreSQL**。
- **OceanBase Oracle 租户**：驱动路径由 DSN 前缀决定——`oracle://...` 走 go-ora 的 TNS 协议（连 OBProxy Oracle 监听端口，通常 2883）；其余前缀（如 `oceanbase-oracle://`、`oboracle://` 或 MySQL 风格）走 `obconnector-go` 的 MySQL 线协议（直连 2881）。
  `source.compat_mode` / `target.compat_mode` 声明租户兼容模式（`mysql` 或 `oracle`），留空时连接后自动探测
  （`SHOW VARIABLES LIKE 'ob_compatibility_mode'`），配置与实际不符会直接报错；`type=oceanbase`（MySQL 模式）连到 Oracle 租户也会报错，需改用 `oceanbase-oracle`。

## 连接通道（native / agent / auto）

`source.channel` / `target.channel`（或 CLI `--channel`，flag 优先）选择数据库连接通道：

| 取值 | 行为 |
|---|---|
| 留空 / `native` | **永远 native**（原生 Go 驱动），与旧版行为完全一致，JVM 进程数为 0 |
| `auto` | native 驱动已编译 → native（连接失败报错不回退）；驱动未编译或该类型无 native 驱动 → 自动走 owljdbc agent |
| `agent` | 强制走 owljdbc agent（JVM sidecar + JDBC 驱动） |

**Agent 通道需要两样东西**（放到 `agent.jars_dir`，默认当前工作目录）：

1. `owl-agent.jar`（JVM sidecar）——**缺失时首次 agent 连接自动从
   [owljdbc release](https://github.com/cangyunye/owljdbc/releases) 下载**（3 次重试）；
   无网络环境会打印手动下载指引，也可用 `OWLJDBC_AGENT_JAR_URL` 指向内网镜像。
2. 目标数据库的 JDBC 驱动 jar（如 `ojdbc8.jar`、`mysql-connector-j-*.jar`、
   `opengauss-jdbc-*.jar`、`oceanbase-client-*.jar`）——自备或
   `bash owljdbc/scripts/fetch-jars.sh` 下载常用三个。

`dm` / `kingbase` / `timesten` 等没有 Go 原生驱动的数据库**只能走 agent 通道**
（`auto` 会自动落到 agent）；`sqlite3` / `duckdb` 无 JDBC 等价物，不参与回退。
serve 端 `GET /api/v1/capabilities` 可查询当前部署对每个类型的实际可用性。

### 外部 profile 注册（owljdbc.profiles）—— 新数据库免改代码接入

**适用**：没有 Go 原生驱动、但提供标准 JDBC 驱动，且 SQL/字典与 Oracle、MySQL、
PostgreSQL 三大族之一兼容的数据库（国产库绝大多数属于此类）。在配置里注册一段
profile + 放一个驱动 jar 即可接入 agent 通道，无需改代码、重新编译。

#### 注册字段

| 字段 | 必填 | 说明 |
|------|------|------|
| `driver_class` | ✅ | JDBC 驱动类名，sidecar JVM 加载，如 `oracle.jdbc.OracleDriver` |
| `jar_globs` | ✅ | 驱动 jar 文件名 glob（放 `agent.jars_dir` 下，任一匹配即可），如 `["ojdbc*.jar"]` |
| `family` | ✅ | 连接语义族：`oracle` \| `mysql` \| `postgres`。决定元数据字典、绑定占位符、分页语法、标识符引用与批量 TRUNCATE 行为 |
| `url_template` | 二选一 | JDBC URL 模板，支持 `{host}` `{port}` `{database}` 占位符。**凭据不走模板**——用户名/密码经连接参数直传 sidecar，不出现在 URL/日志里 |
| `dsn_raw` | 二选一 | `true` 时忽略模板，`dsn` 本身就是完整 JDBC URL，原样透传 |
| `dsn_syntax` | 建议 | 声明 `source.dsn`/`target.dsn` 的语法（见下表），供结构化解析与表单回填 |

#### 三族注册示例（均已实测）

```yaml
owljdbc:
  profiles:
    # Oracle 兼容库（示例 type 名 orax，任意取名）
    orax:
      driver_class: oracle.jdbc.OracleDriver
      jar_globs: ["ojdbc*.jar"]
      family: oracle
      url_template: "jdbc:oracle:thin:@//{host}:{port}/{database}"
      dsn_syntax: url
    # MySQL 兼容库
    myx:
      driver_class: com.mysql.cj.jdbc.Driver
      jar_globs: ["mysql-connector-j-*.jar"]
      family: mysql
      url_template: "jdbc:mysql://{host}:{port}/{database}?useSSL=false&allowPublicKeyRetrieval=true&characterEncoding=UTF-8"
      dsn_syntax: mysql-tcp
    # PostgreSQL 兼容库
    pgx:
      driver_class: org.postgresql.Driver
      jar_globs: ["postgresql-*.jar"]
      family: postgres
      url_template: "jdbc:postgresql://{host}:{port}/{database}"
      dsn_syntax: url
```

注册后即可像内置类型一样使用（`channel` 置 `agent` 或 `auto`）：

```yaml
source:
  type: orax
  dsn: "oracle://user:pass@db-host:1521/ORCL"
  schema: APPUSER
  channel: agent
```

#### dsn_syntax 取值（source.dsn / target.dsn 的写法）

| 取值 | DSN 形式 | 示例 |
|------|----------|------|
| `url` | `scheme://user:pass@host:port/db` | `oracle://scott:tiger@h:1521/ORCL`、`postgresql://u:p@h:5432/test` |
| `mysql-tcp` | `user:pass@tcp(host:port)/db` | `root:pw@tcp(127.0.0.1:3306)/mydb` |
| `pg-kv` | libpq 键值对 | `host=h port=5432 user=u password=p dbname=d` |
| `kv` | 分号 `KEY=VALUE`（键名大小写不敏感，识别 host/hostname/server、port/tcp_port、user/uid、password/pwd、database/db/dbname/sid/service_name 等常见别名，其余原样保留） | `TTC_SERVER=h;TCP_PORT=6625;TTC_SERVER_DSN=sampledb` |

**密码含特殊字符**（`@ : / # ?` 等）时，`url` 语法按 RFC3986 百分号转义后书写，
工具自动还原：如密码 `AA@1122#` 写作 `postgresql://u:AA%401122%23@h:5432/test`。
`mysql-tcp` 与 `kv` 语法无需转义。

#### 生效位置与 Web 端

- **CLI**：所有命令在读取配置时注册，随用随生效。
- **serve**：上传 YAML、`PUT /api/v1/config`、激活配置即时生效；迁移任务的
  worker 子进程经同一配置文件重新注册，跨进程一致。
- **Web 表单**：注册的类型自动出现在「源/目标数据库类型」下拉中；**结构化填写**
  弹窗按内置类型推断字段，自定义类型建议直接把 DSN 粘贴到 DSN 输入框。
- `GET /api/v1/capabilities` 会列出注册类型的驱动 jar 探测结果。

#### 目标端注意事项

`owljdbc.profiles` 注册的是**连接层**类型；目标端建表的 **DDL 方言**仍由
`ddl.target_dialect` 决定，应指向该库兼容的内置方言（Oracle 兼容库填 `oracle`、
MySQL 兼容库填 `mysql`、PG 兼容库填 `postgres`）。`family` 与 `ddl.target_dialect`
通常一致。

#### 排查

| 现象 | 原因与处理 |
|------|-----------|
| `unsupported database type` | 该命令的配置里没有 `owljdbc.profiles` 注册段（注册随配置走，不是全局注册表） |
| `no jar matching ...` | `jar_globs` 没匹配到 `agent.jars_dir` 里的文件；核对文件名与 glob |
| `owl.agent.Main` 找不到 / agent jar 相关报错 | owl-agent.jar 缺失；首次连接会自动下载，离线环境按报错指引手动放置或用 `OWLJDBC_AGENT_JAR_URL` 指向内网镜像 |
| 连接认证失败但账密正确 | 检查 `dsn_syntax` 是否选对；`url` 语法下密码特殊字符需百分号转义 |
| 语法错（如 MySQL 报双引号标识符错） | `family` 选错族；确认该库兼容的是 oracle/mysql/postgres 哪一族 |
| 元数据抽取报列不存在（如 collation） | 属字典差异；三大族内置 querier 已含窄字典降级，若仍报错请反馈库型号 |

## Table Filtering

The `ddl.table_filter` and `export.tables` sections support multi-level filtering:

```yaml
ddl:
  table_filter:
    include: ["*"]                # Include all (default), or ["SCOTT.*"], or ["SCOTT.EMP"]
    exclude:
      glob: ["*_LOG", "TMP_*"]   # Glob pattern on table name
      regex: ['^BIN\$']          # Regex pattern (e.g., Oracle recycle bin)
      schemas: ["SYS", "SYSTEM"] # Exclude entire schemas
      tables: ["SCOTT.TEMP"]     # Exact schema.table exclusion
```

Priority: includes → glob exclude → regex exclude → schema exclude → table exclude.

## Error Policies

```yaml
import:
  batch:
    error_policy: skip_row  # stop | skip_row | log_only
```

| Policy | Behavior |
|---|---|
| `stop` | Abort the table import on first error |
| `skip_row` | Skip the row, log warning, continue (respects `max_errors_before_stop`) |
| `log_only` | Log and continue inserting (may re-fail) |

## 外键处理（truncate_before / respect_foreign_keys）

导入前清空与插入顺序均基于**目标库中实际存在的外键**（实时探查），不依赖元数据里是否定义了外键：

- `target.truncate_before: true`：
  - **PG 族**（postgres / opengaussdb / panweidb）：本批所有表用一条 `TRUNCATE TABLE a, b, ...` 清空，批内外键互引自动成立；
  - **MySQL / Oracle** 及单表回退场景：逐表 TRUNCATE，被外键阻断时自动回退 `DELETE FROM`（只清本批表，不影响批外数据）。
- `parallel.respect_foreign_keys: true`：按目标库外键依赖排序（父表先插入），并自动强制串行导入；
  **目标表带外键时必须开启**，否则并行插入可能违反 FK 顺序。
- 探查不到外键时自动退化为原行为；被迁移集之外的表引用的表无法 TRUNCATE，会告警并改用 `DELETE FROM`。

## Data Transforms

The `import.data_transforms` section controls per-value transformations during import:

| Setting | Purpose |
|---|---|
| `datetime_format` | Auto-convert compact datetime (14 digits → `YYYY-MM-DD HH24:MI:SS`) |
| `trim_strings` | Trim leading/trailing whitespace from string values |
| `null_if` | String values to treat as SQL NULL |
| `source_encoding` | Decode CSV from source encoding to UTF-8 (GBK, LATIN1, ISO-8859-*, Windows-1252) |

## NULL 识别（三处配置的关系）

导入时有三处配置都会把字段识别为 NULL，作用阶段不同、不冲突：

| 配置 | 阶段 | 作用 |
|---|---|---|
| `import.csv.null_marker` | CSV 解析 | **主配置**：字段文本与该标记完全相等 → NULL（默认 `\N`） |
| `import.csv.null_identifiers` | CSV 解析 | 扩展匹配规则：额外字符串列表、大小写敏感开关、正则 |
| `import.data_transforms.null_if` | 值转换 | 常见字面量便捷入口（如 `"NULL"`、`"null"`） |

一般场景只配 `null_marker` 即可；三者的命中结果一致（都写入 SQL NULL）。

## Extensions

The `extensions` map is a catch-all for custom or future configuration:

```yaml
extensions:
  my_plugin:
    option1: value1
```

This section is not validated by the core config loader — it's available for custom tooling or future plugin support.

## Config Validation

The config loader validates:

1. `metadata.type` must be `csv`, `xlsx`, or `database`
2. When `metadata.type` is `database`, `source.type` and `source.dsn` are required
3. `ddl.target_dialect` must be a valid dialect name; when omitted it is
   inherited from `target.type`（`postgresql`/`mariadb` 等别名自动归一），
   二者都缺时才报 `ddl.target_dialect is required`
4. `ddl.source_dialect` (if set) must be a valid dialect name
5. `import.batch.error_policy` must be `stop`, `skip_row`, or `log_only`
6. `source.compat_mode` / `target.compat_mode` (if set) must be `mysql` or `oracle`

### Valid Dialects

```
oracle, postgres, mysql,
goldendb, goldendb-mysql, goldendb-oracle,
oceanbase, oceanbase-mysql, oceanbase-oracle,
panweidb, opengaussdb
```

### Valid Metadata Types

```
csv, xlsx, database
```
