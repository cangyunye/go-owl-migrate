# TimesTen Agent 通道接入计划（oracle → timesten 迁移 + timesten → export data）

> 日期：2026-09-26　状态：**草案（待确认，确认后再实现）**
> 前置：v0.6.0 dameng agent 通道互迁已实测通过（`docs/test-plans/dameng.md`）
> 范围：本轮**只做方案确认，不写代码**

## 0. 目标与范围

| 场景 | 方向 | 说明 |
|---|---|---|
| 迁移 | oracle → timesten | 源端 native go-ora，目标端 agent 通道（JDBC），建表 + 导数据 |
| 导出 | timesten → export data | 元数据抽取 + 数据导出 **CSV / SQL**（复用既有 export data / migrate --sql-out 管线） |

**明确不做（本轮）**：timesten → oracle 回程迁移、og/timesten 互迁、online/CDC、direct 模式性能调优、TimesTen Scaleout。

## 1. 现状盘点（可复用件）

| 组件 | 现状 | 结论 |
|---|---|---|
| owljdbc catalog `timesten` profile | 已登记：`com.timesten.jdbc.TimesTenDriver`、family `oracle`、`ttjdbc*.jar`、`URLFromDSN: true`（DSN 即完整 JDBC URL 原样使用） | 桩可用，但**缺 BuildURL**（DSN-less 形态），见 §2 |
| oracle 兼容字典视图 | TimesTen 官方提供 `ALL_TABLES`/`ALL_TAB_COLUMNS` 等，"列名与数据类型与 Oracle 相同"（[22.1 System Tables](https://docs.oracle.com/en/database/other-databases/timesten/22.1/system-tables/system-tables-and-views1.html)、[18.1](https://docs.oracle.com/database/timesten-18.1/TTSYS/systemtables.htm)） | 抽取层可复用 oracle querier，按 DM 模式做**窄字典降级变体**（§4） |
| Oracle 兼容类型 | TT 22.1 支持 `NUMBER/CHAR/VARCHAR2/NCHAR/NVARCHAR2/DATE/TIMESTAMP/ROWID/CLOB/NCLOB/BLOB/BINARY/VARBINARY/TIME/BINARY_FLOAT/DOUBLE` + TT_* 原生类型 | oracle DDL 方言输出与 TT 类型体系高度重合，**目标 DDL 复用 oracle 方言**（同 dm 策略，§5） |
| dm 本轮沉淀 | 窄字典 Querier 模式、PG 风格类型归一、DATETIME 纳秒协议、时区墙钟语义、Clob/Blob 解码、ApplyDefaults header 默认、NUMBER 精度修复 | 全部直接受益；timesten 接入主要是"接线 + 实测调参" |
| JDBC 驱动 jar | **不在 Maven Central**（Oracle 商业驱动），随 TimesTen 安装介质分发：`<install>/lib/ttjdbc8.jar`（JDK8）/ `ttjdbc11.jar` | 用户自备放入 `jars_dir`，catalog glob `ttjdbc*.jar` 已匹配 |

## 2. 连接方案（关键设计点）

### 2.1 TimesTen 的三种连接形态（官方 [22.1 Connection Options](https://docs.oracle.com/en/database/other-databases/timesten/22.1/introduction/timesten-connection-options.html)）

| 形态 | 说明 | 工具适用性 |
|---|---|---|
| **direct** | 应用与库同主机，共享内存直连（JDBC 依赖 ODBC direct driver + 本机原生库） | 迁移工具与 TT 同机部署时最优；本机原生库要求使它不适合通用分发 |
| **client/server** | 远程 TCP：client driver → TT Server 守护进程（每连接一个 server 子进程） | **主推形态**：与现有 agent 通道"远程 JDBC sidecar"模型完全一致 |
| driver manager | ODBC 管理器混用多驱动 | 不适用 |

### 2.2 JDBC URL（官方属性名，[TTC_SERVER](https://docs.oracle.com/en/database/other-databases/timesten/22.1/database-reference/ttc_server-or-ttc_server1.html)、[Oracle TT blog](https://blogs.oracle.com/timesten/timesten-xe-client-server)）

```
# DSN-less client（主推，从 Endpoint 构造）
jdbc:timesten:client:TTC_SERVER=<host>;TCP_PORT=<port>;TTC_SERVER_DSN=<serverDSN>

# 客户端 DSN（用户已配好 sys.odbc.ini 时）
jdbc:timesten:client:dsn=<clientDSN>

# direct（同主机；DSN 为本机 sys.odbc.ini 里的 DSN 名）
jdbc:timesten:direct:dsn=<localDSN>

uid/pwd 建议走连接参数（DriverManager.getConnection(url, user, pwd)），
owljdbc 现有 Endpoint 机制即如此，不进 URL。
```

### 2.3 方案：新增 `BuildURL` + 保留 `URLFromDSN` 直通

- `Endpoint` 字段映射：`host → TTC_SERVER`、`port → TCP_PORT`（TT Server 默认 6624/6625 端口段）、**`database → TTC_SERVER_DSN`（服务端 DSN 名，不是库名——TT 单库实例，"库"即 server DSN 指向的 data store）**。
- 用户 DSN 写 `jdbc:timesten:` 开头时按现 `URLFromDSN` 语义原样直通（覆盖 client DSN / direct / 特殊属性三种手工场景）。
- `dsnfields` 需新增 timesten family：解析 `TTC_SERVER=h;TCP_PORT=p;TTC_SERVER_DSN=d`（分号 KV）与 `jdbc:timesten:client:` 前缀，供 web 表单结构化填写与 `Decompose→Endpoint` 反解。**kv 语法已在 v0.7.0 的 dsnfields 中实现**（识别 TTC_SERVER/TTC_SERVER_DSN/TCP_PORT 等别名，未识别键进 Extra）。
- **连接属性透传（GBK 适配）**：`url_template` 支持 `{extra}` 占位符——kv 解析出的未识别键（如 `Charset=ZHS16GBK`）以 `;` 拼接代入：

  ```yaml
  url_template: "jdbc:timesten:client:TTC_SERVER={host};TCP_PORT={port};TTC_SERVER_DSN={database}{extra}"
  ```

  是否需要 `Charset` 属性属待实测（JDBC String 接口理论上由驱动在 Java Unicode 与库字符集间直接转换，Charset 主要影响客户端字符数据接口）；实测确认后在文档定型。

### 2.4 环境前置（远端迁移的硬要求）

- TT 主机必须运行 **TimesTen Server 守护进程**（`ttDaemonAdmin -startserver`）并定义**服务端 DSN**（`sys.odbc.ini`）；客户端只需 `ttjdbc*.jar`（Instant Client 即可）。
- 工具侧无需安装 TT 客户端库（纯 JDBC client driver 走 TCP）——与 dameng 接入体验一致。
- **库字符集核对（GBK 环境必做）**：TimesTen 的 database character set 在 `dbcreate` 时固定，合法值只有 **AL32UTF8**（UTF-8）与 US7ASCII/ISO8859-1 等单字节集——**不支持 GBK 作为库字符集**。GBK 的标准形态是「库 AL32UTF8 + 客户端 `Charset=ZHS16GBK`（连接属性）由驱动转换」。实施前先确认环境属于哪种：
  - 库 = AL32UTF8：正常走方案全流程（中文种子可测）；
  - 库 = 单字节（US7ASCII/ISO8859-1）：**中文存不进去**——测试数据降级 ASCII（同 ORCLCDB 教训），或 `dbcreate -charset AL32UTF8` 重建库；
  - Oracle 源侧 ZHS16GBK：多字节字符集，中文可正常存取，go-ora/ojdbc 双通道均按 NLS 自动转 UTF-8，无需注入。

## 3. 类型映射方案（oracle → timesten）

目标 DDL **复用 oracle 方言**（`ddl.target_dialect: oracle`，同 dm），辅以实测校准：

| Oracle 源类型 | TT 目标 | 备注 |
|---|---|---|
| NUMBER(p,s) | NUMBER(p,s) | TT 支持到精度 38；NUMBER(p,0) 建议映射 TT_INTEGER/TT_BIGINT（**待实测确认**，先保守 NUMBER） |
| VARCHAR2(n) | VARCHAR2(n) | n ≤ 4194304；> INLINE 阈值自动 NOT INLINE |
| NVARCHAR2/NCHAR | NVARCHAR2/NCHAR | 长度按字符；依赖库字符集（AL32UTF8 建议） |
| DATE | DATE | TT DATE 含时间部分（同 Oracle） |
| TIMESTAMP(p) | TIMESTAMP(p) | TT 默认精度 6 |
| CLOB/BLOB | CLOB/BLOB | TT 限制：CLOB 不可比较/DISTINCT/ORDER BY/GROUP BY——迁移建表不受影响，含 CLOB 表的源端分页若依赖 CLOB 排序需走 PK（现 exporter 即 PK 游标 ✓） |
| RAW/BLOB | VARBINARY/BLOB | 待实测 |

标识符大小写：TT 未加引号标识符折叠规则预期同 Oracle（大写）——**待实测**；若一致，沿用 dm 的 `no_quote_identifiers: true` 套路。

## 4. 元数据抽取方案

- **复用 `OracleMetadataQuerier`**（ALL_* 视图官方声明与 Oracle 同构），按 DM 经验注册 `TimestenQuerier{OracleMetadataQuerier{OceanBase: true}}` 窄变体（`normalizeDBType: timesten → "timesten"`）：
  - 预期缺失需降级的项（实测确认后落定）：`all_tab_columns.collation` / `char_used`（TT 无字符集语义细分）、identity 列（`all_tab_identity_cols` 预期不存在）、`ALL_TRIGGERS.TRIGGER_TYPE` 等周边对象列集差异。
  - 核心对象（tables/columns/pk/indexes/fk）TT 均有对应 ALL_* 视图，保持严格报错。
- `dbconn.knownTypes` + `ValidDialects` 处理：`source.type: timesten` 不需要进 ValidDialects（源类型不校验）；`registry.Register("timesten", oracle.New())` 供源方言映射（同 dm）。
- schema 语义：TT 用户即 schema（Oracle 风格），`source.schema` 传 TT 用户名（大写）。

## 5. 数据路径（既有管线核对）

| 环节 | 现状 | timesten 适配点 |
|---|---|---|
| 源端分页导出 | PK 游标分页，oracle 族 `:N` 占位符经 sidecar BindRewriter 改写 `?` | family=oracle ✓ 无需改；exporter `isOracle` 需加 `timesten`（同 dm 修复） |
| 目标端导入 | importer `:N` → sidecar → `?`；`time.Time` 绑定（DATETIME tag + 纳秒） | TT JDBC 标准 `setTimestamp` ✓；纳秒协议已就绪 |
| export data CSV | exporter → CSV（compact datetime 格式） | 无需改 |
| export SQL | `migrate --sql-out` / gen-insert 从 CSV 生成 INSERT | 方言传 `oracle`（TT 接受 Oracle 风格 INSERT；`dbconn.Family("timesten")` 归 oracle 后自动成立） |
| TRUNCATE/回程 | oracle 族单表 TRUNCATE | ✓（dm 修复已覆盖） |

## 6. 验证策略

1. **环境前置（唯一硬依赖）**：TT 22.1（或 18.1）实例 + TimesTen Server + 服务端 DSN；无官方 docker 镜像，需用户提供虚机/物理机或自建容器（XE 版免费）。**测试环境需要你提供或确认自建方式。**
2. 通道级：conn test / validate（`source.type: timesten` + agent 通道），验证 URL 拼装、驱动 jar 解析、窄字典抽取逐对象通过。
3. 迁移 e2e：oracle → timesten（OWLE2E 模式种子，含中文/微秒/大整数），对拍 CSV。
4. 导出 e2e：timesten → export data CSV、→ export SQL，与源端导出对拍。
5. 默认通道回归：`channel` 缺省时现有测试零 diff（timesten 仅显式/auto 且无 native 驱动时才进 agent）。
6. **GBK 环境数据集与断言**（两档，参照 charset 矩阵既有做法）：
   - **GBK 安全集**（常用中文、全角标点、GBK 范围生僻字、LATIN1 区）：全链路逐值一致；
   - **UTF-8 超集集**（emoji、CJK 扩展 B）：在 oracle GBK 端**预期失败/替换**，断言 native(go-ora) 与 agent(ojdbc) 双通道失败语义一致，不静默吞掉；
   - 管线不变量（进程内/CSV 一律 UTF-8）不变；GBK 环境文本工具看 CSV 乱码属查看器编码问题，不是数据问题（写进测试指南防误判）。
7. **编码探测告警**：oracle 族 `ProbeServerEncoding`（NLS_CHARACTERSET）与 TT 库字符集查询在连接后打日志——提前暴露单字节库存不了中文/编码不匹配，本条从遗留清单提级为 M2 交付项。

## 7. 里程碑（确认后执行）

| # | 内容 | 验收 |
|---|---|---|
| M1 | catalog `BuildURL`（含 `{extra}` 占位符透传 Charset 等连接属性）+ sidecar 拉起补 `-Dfile.encoding=UTF-8`（GBK console 下错误消息不乱码）+ dsnfields kv（v0.7.0 已有）+ TT jar 就位 | conn test 通（需 TT 环境；先核对库字符集形态，见 §2.4） |
| M2 | TimestenQuerier 窄字典 + 降级策略实测校准 + oracle 族编码探测告警接线（§6.7） | export-metadata 逐对象通过；探测日志可见 |
| M3 | oracle → timesten 迁移 e2e（DDL + 数据 + 对拍） | 45 行全量一致 |
| M4 | timesten → export data（CSV/SQL）e2e | CSV/SQL 与源对拍一致 |

## 8. 待实测确认清单（进入实现后第一优先）

1. `ALL_TAB_COLUMNS` 实际列集（char_used/charset/identity/collation 缺哪些）→ 窄变体裁剪范围。
2. TT 标识符未加引号的大小写折叠方向。
3. TT Server 端口/连接属性在目标版本的准确拼写（18.1 vs 22.1 差异）。
4. NUMBER(p,0) → TT_INTEGER/TT_BIGINT 是否更优（约束/索引/空间）；先保守 NUMBER。
5. TT JDBC 对 `setTimestamp(i, ts, Calendar)` 的支持（纳秒协议依赖）与 CLOB 绑定读取。
6. 测试环境：TT 版本、字符集（建议 dbcreate 时 AL32UTF8）、Server 端口。
7. **GBK 环境专项**：JDBC 链路是否需要 `Charset=ZHS16GBK` 连接属性（经 `{extra}` 透传实测）；GBK 生僻字在 oracle GBK 端经 go-ora/ojdbc 双通道的失败语义是否一致；TT 端 GBK 全集→AL32UTF8 往返是否逐值无损。
8. 已实测基线（配置化注册机制侧，2026-09-27）：orax(oracle 族)→dameng 45/45、pgx(postgres 族)/myx(mysql 族) 导出逐值全对——M1 剩余工作仅为 TT 专属的连接与字典校准。
