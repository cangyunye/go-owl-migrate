# JDBC Agent 通道架构设计（第一阶段：通道 + OB 双租户对拍验证）

> 日期：2026-09-05　分支：`feat/agent-driver`
> 状态：待评审

## 0. 背景与边界（修正后）

go-owl-migrate 已通过 `database/sql` 驱动支撑 MySQL / Oracle / PostgreSQL / OceanBase / GoldenDB 等（含 build-tag 插件化注册）。但**存在一类数据库没有可用的原生 Go 驱动，只提供 JDBC**（无论具体是哪家）。本设计的目标是：为这类数据库提供一条**通用 JDBC 通道**，使其与现有原生路径**行为等价**、上层（dialect / extractor / exporter / importer）零改动。

**明确边界**：

- 交付物是**通用 agent 架构本身**（sidecar 通道 + 薄驱动 + 生命周期 + 对拍验证）。
- 具体目标数据库（达梦、金仓、Yashan 等）**不在本次范围**——它们是未来的扩展点（加驱动 jar + dialect 族注册即可）。
- **OceanBase MySQL / Oracle 双租户仅作验证靶场**：其 native 路径已可用（`go-sql-driver/mysql` + go-ora/obconnector），用同一份库表数据做**等价对拍**，可最大化证明通道的通用性与正确性，避免黑盒验证。
- 分两阶段：**阶段一** = 通道 + 对拍验证（本设计）；**阶段二** = 产品化接入真实 JDBC-only 库（后续单独设计）。

## 1. 总体架构

沿用参考文档 `go_agent_driver.md` 的「统一抽象 + 双引擎」思想，但落进本仓库的 `database/sql` 形态——**不重写分层，只在驱动层补一条通道**。

```plaintext
业务层（零改动）
  dialect/（纯 SQL 生成）  extractor/（MetadataQuerier）  exporter/  importer/
        └── 只消费 *sql.DB
────────────────────────────────────────────
Go 驱动层（新增 internal/agent/）
  driver.go    database/sql Driver "owljdbc" + Conn/Stmt/Rows
  proto.go     帧协议 client（读写 goroutine，req-id 匹配）
  manager.go   agent 进程生命周期（spawn/握手/健康检查/崩溃/关停）
──────────────────────────── 子进程 stdin/stdout 二进制帧 ────────────────────────────
JVM sidecar（新增 jvm/owl-agent/）
  自研 java.sql 封装（约几百行）
  每个 agent 进程 = 一个驱动 classpath profile
  java -cp owl-agent.jar:<driver-jars> owl.agent.Main
```

### 1.1 关键映射关系

| 参考文档概念 | 本仓库对应 |
|---|---|
| `core.DBAgent` 统一接口 | `dbconn.Open(cfg) → *sql.DB`（已存在，无需重写） |
| Native Agent（双协议分发） | 已有（含 build-tag、OB compat 探测） |
| 插件工厂 `driver_plugin/` | `registry/` + `dialect/`（已存在） |
| **JDBC Agent（缺失一环）** | **本设计新增**：`internal/agent/`（Go）+ `jvm/owl-agent/`（Java） |

**核心推论**：迁移工具所有下游（dialect 生成、元数据抽取、导入导出）只消费 `*sql.DB`。只要把 JDBC-only 库伪装成一个注册的 `database/sql` 驱动（如 `owljdbc`），**下游代码零改动**。这是将文档思想落进本仓库的正确姿态，而不是照搬其分层重写。

## 2. 进程与会话模型

- **进程模型**：一个 agent 进程 = 一个 classpath profile（驱动 jar 集合 + agent.jar 唯一）。多个逻辑连接（database/sql 连接池）复用一个 JVM，避免「16 连接 = 16 个 JVM」的内存爆炸。
- **会话**：每逻辑连接一个 session id；同一 session 内语句串行执行（一个 JDBC Connection 同时只跑一条语句），不同 session 可并行（req-id 匹配响应）。
- **生命周期**：
  - 首次 `Open` 惰性 spawn agent 进程 → 握手帧（协议版本 / 能力 / 驱动就绪）。
  - 全部连接 `Close` + 空闲超时 → 优雅关停（发 `SHUTDOWN`，超时强杀）。
  - 进程崩溃 → 该 profile 下所有 session 报错，**不静默重连**（失败快、信息真）。
  - 日志透传：agent stderr → Go zap。
- **凭证安全**：`user` / `password` 只走 spawn 后的 stdin 首帧，**不进 argv / env**；DSN 展示复用 `config/mask.go` 打码。
- **Java 运行时**：从 `PATH` 或配置 `java_home` 取 `java`；因驱动 jar 为 JDK 8 构建，agent 编译目标设为 `release 8`，运行环境要求 **JRE 8+**。本机实测 **java 21** 可连；驱动为 JDK 8 构建，**JRE 8 亦可连接**（向下兼容）。

## 3. 帧协议（Go ↔ Java）

- 传输：子进程 stdin/stdout 字节流；**4B 小端长度前缀 + 类型字节 + payload**；stderr 仅透传日志，不参与协议。
- 消息：`请求{req-id, conn-id, op, params}` / `响应{req-id, ok, err}`。Go 侧每物理连接一个读 goroutine，按 `req-id` 匹配，支持并发。
- **Ops 最小集**（覆盖全流程所需）：
  `CONNECT` / `CLOSE` / `QUERY` / `EXEC` / `BEGIN` / `COMMIT` / `ROLLBACK` / `SAVEPOINT` / `RELEASE` / `PING` / `CANCEL` / `SHUTDOWN`。
- **查询流式**：`QUERY` 响应后跟 `ROW` 批帧（流式）+ `END` 帧；`ctx` 取消时发 `CANCEL` 中止语句。
- **值编解码**：类型化 tag（NULL / bool / int / long / double / decimal(string) / string / bytes / datetime / date / time）。
  - `decimal` 走 **string** 防精度丢失（对齐 exporter 现有处理）。
  - 时间带时区语义，由连接级 family 约定（mysql vs oracle）。
- **LOB 流式**：BLOB / CLOB 以 chunk 帧流式传输（Java `getBinaryStream` / `getCharacterStream`），对齐 native go-ora `LOB FETCH=POST` 的有界内存语义；大字段不整体进内存。

## 4. 占位符处理（正确性核心）

已核实：importer 按 dialect 家族发不同绑定风格——oracle 族 `:N`、mysql 族 `?`；且 oracle FK 内省 SQL 硬编码 `UPPER(:1)`（`internal/transfer/importer/importer.go` 的 `oracleFKConstraints`）。因此 **agent 通道必须同时消化 `?` 和 `:N`**。

- 连接级声明 family（`mysql` / `oracle`）。
- agent 侧做**有界 tokenizer 改写**：把 oracle `:N` → JDBC `?`（跳过单双引号字符串字面量、行/块注释；拒绝 `::` 与 `:=` 边界）。
- **安全断言**：改写后 `?` 数量必须 == 实参个数，不等即拒绝执行——防静默错位。
- tokenizer 为 Java 纯函数；Go / Java 两侧都喂**对抗 SQL** 测试（含字面量含 `:1`、注释含 `:1`、`::`、`:=` 等）。

## 5. 驱动 jar 按 profile 隔离（新确认事实）

- 已确认驱动类：`com.oceanbase.jdbc.Driver`（ServiceLoader 注册；`Class.forName` 可加载）。
- jar：`oceanbase-client-2.4.1.jar`，Maven 坐标 `com.oceanbase:oceanbase-client`，构建于 JDK 8。官方驱动仓库：<https://github.com/oceanbase/obconnector-j>（本地 `~/obconnector-j`）。
- **实测结论（2026-09-05）**：`oceanbase-client` **一个 jar 同时支持 OB Oracle 与 OB MySQL 租户**——`jdbc:oceanbase://host:port?useSSL=false` + `user=用户名@租户名`，无需单独的 Oracle 兼容 JDBC（ojdbc）。
  - 驱动 `acceptsURL` 判定前缀为 `jdbc:oceanbase:`。
  - 两租户均上报 product name（Oracle / MySQL），可用于判定租户模式；版本号统一为 `5.7.25`（驱动内部版本，非 OB 真实版本）。
- 因此 agent 的连接请求只需携带 `driverClass` + `url` + `user@tenant`；驱动 jar 仍按库分 profile（不同库用不同 jar），但 OB 单 jar 即可覆盖双租户。

### 5.1 实测记录（OB 双租户探针）

| 租户 | 登录 user | 查询 | 结果 |
|---|---|---|---|
| OB Oracle `oratest` | `sys@oratest` | `SELECT ... FROM DUAL` | ✅ product=Oracle |
| OB Oracle `oratest` | `MIGSRC@oratest` | `user_tables` → `DEPT` | ✅ product=Oracle |
| OB MySQL `obmysql` | `root@obmysql` | `SELECT 1` | ✅ product=MySQL |

端点 `127.0.0.1:2881`（`172.20.214.44:2881` 亦可达）；口令含 `@@` 经 `getConnection(url, user, pass)` 传入无需编码。

## 6. 接入面（阶段一）

- 仅新增：Go `internal/agent/`、Java `jvm/owl-agent/`、对拍 harness `internal/e2eagent/`。
- **`dbconn` / `registry` / CLI / dialect 零改动**。harness 直接 `sql.Open("owljdbc", agentDSN)`，并以库方式复用 `extractor` 的 mysql/oracle 两族 `MetadataQuerier`、`exporter`、`importer`。
- 对拍 harness 按仓库 e2e 惯例（参照 `internal/e2eob/`、`tools/ogtest`）落地，接用户提供的 OB 实例。

## 7. 对拍验证矩阵（验收）

OB 双租户各跑一次：

1. **元数据抽取**：native vs agent → `TableDef` / `Column` / `Index` / `FK` 逐项 diff（含 LOB / NUMBER / 时间类型列）。
2. **导出**：分页导出 CSV 逐字节 diff，覆盖 LOB 大字段 + NULL / 负 decimal / 二进制 hex / 超大字符串等特殊值。
3. **导入**：同一批 CSV → native 目标 vs agent 目标（建表 + 导入），行数 / savepoint / 错误隔离行为一致，再导出回读 diff。
4. **大表性能基线**：百万行级一张表，native 导出 vs agent 导出、native 导入 vs agent 导入的吞吐（rows/s、MB/s）；agent 不低于 native 的 ~1/3 视为通道可用（先量化，不做死承诺）。
5. **故障路径**：杀 agent 进程 → Go 报错语义；`continue-on-error` 行策略行为一致；`ctx` 取消 / 超时。

## 8. 明确不做（YAGNI）

- 产品 config / CLI / 新 dialect 注册（阶段二）。
- 驱动插件 API 抽象层（先把一条通道做扎实）。
- 具体目标库（达梦 / 金仓 / Yashan）接入。
- PostgreSQL-family JDBC（`pq` COPY 语义冲突）。
- online / CDC adapter 合并。
- 多 JVM 负载均衡 / agent 集群。

## 9. 风险清单

| 风险 | 缓解 |
|---|---|
| 占位符改写正确性 | 计数断言 + 对抗测试（Go/Java 双侧） |
| JDBC ↔ Go 扫描值类型映射偏差（Oracle NUMBER / NVARCHAR2 / CLOB、OB 特有） | 先列类型映射表；对拍矩阵 1/3 兜住，差异暴露再修 |
| LOB 内存 | chunk 流 + fetchSize，压测观察 |
| JVM 多会话并发 | 每会话串行 + 每进程多会话并行，driver 层保序 |
| 时区 / 时间精度 | 显式 `getTimestamp` 带时区语义；对拍验证 |
| agent 进程是外部依赖 | 版本握手 / 健康检查 / 明确报错；缺 JRE 时给出明确指引 |
| **OB Oracle 租户驱动 jar 未确定**（`oceanbase-client` 为 MySQL 协议） | **已消解**：实测 `oceanbase-client` 单 jar 连 OB Oracle/MySQL 双租户（见 §5） |
| 文档版本（V2.4.13）与 jar（2.4.1）不一致 | 以实际 jar 为准；记录版本对账 |
| Windows / WSL 下 java spawn 与 stdin 二进制 | go `exec` 跨平台；本机 WSL 先验证 |

## 10. 待确认（阶段一启动前）

1. ~~OB Oracle 租户的 JDBC 驱动 jar（ojdbc / OB Oracle 模式 JDBC）来源与版本。~~ **已核销**：`oceanbase-client-2.4.1.jar` 单 jar 支持双租户（见 §5），官方仓库 <https://github.com/oceanbase/obconnector-j>（本地 `~/obconnector-j`）。
2. 本机 `java` 版本：**已确认 java 21**；JRE 8+ 均可（驱动 JDK 8 构建，向下兼容）。
3. OB 实例连接信息：**已获取**（Oracle 租户 `oratest`、MySQL 租户 `obmysql`，均 `127.0.0.1:2881`；凭据见 `testdata/db/.local-dev.env`，git 忽略）。

## 11. 扩展矩阵（通道通用性验证与登记）

|库|状态|driverClass|URL 模板|family|验证|
|---|---|---|---|---|---|
|OceanBase MySQL/Oracle 租户|已实测（单 jar 双租户）|com.oceanbase.jdbc.Driver|jdbc:oceanbase://host:port?useSSL=false|mysql/oracle|e2e 冒烟+全流程对拍 ✅|
|MySQL|已实测|com.mysql.cj.jdbc.Driver|jdbc:mysql://host:port/?useSSL=false&allowPublicKeyRetrieval=true|mysql|e2e 冒烟 ✅（jar: mysql-connector-j-8.0.33）|
|PostgreSQL|已实测|org.postgresql.Driver|jdbc:postgresql://host:port/db|postgres（? 绑定，$N 文本直通）|e2e 冒烟 ✅（jar: postgresql-42.7.13；$N 直通经 `PREPARE … AS SELECT $1::int` + `EXECUTE` 实测）|
|Oracle|登记，未测（库未启动）|oracle.jdbc.OracleDriver|jdbc:oracle:thin:@//host:1521/service|oracle|—|
|TimesTen|登记，未测（库未启动）|com.timesten.jdbc.TimesTenDriver|jdbc:timesten:client:dsn=<dsn>|oracle（就近）|—|
|GoldenDB MySQL 模式|登记，未测（库未启动）|厂商 JDBC（MySQL 协议系）|jdbc:goldendb://… 或 mysql 兼容 URL|mysql|—|
|GoldenDB Oracle 模式|登记，未测（库未启动）|厂商 JDBC（Oracle 兼容系，待厂商确认）|厂商 URL|oracle|—|

family 决定占位符语义（oracle `:N`→`?` 由 agent 改写；mysql `?` 直通；postgres `?` 直通、`$N` 作为 SQL 文本原样透传）与 Go 侧类型处理约定；接入新库 = jar + family + URL，无需改通道代码。注：PgJDBC 不接受 `$N` 作为客户端绑定位（对 jar 直连实测同样报 "column index is out of range"），postgres 族经通道的参数绑定使用 `?`，`$N` 语义由服务端 `PREPARE`/`EXECUTE` 承载。
