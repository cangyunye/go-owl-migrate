# Dameng (达梦 DM8) Agent 通道互迁测试方案与结果

> 2026-09-26　owl-migrate v0.6.0　状态：**四条链路实测通过**

## 方言信息

| 属性 | 值 |
|------|-----|
| 注册名 | `dm`（connection-layer type；registry 里是 oracle 方言别名） |
| 原生 Go 驱动 | **无** —— 只能走 agent 通道（`channel: agent` / `auto`） |
| JDBC 驱动 | `DmJdbcDriver18-8.1.3.140.jar`（Maven Central `com.dameng:DmJdbcDriver18`，匹配 `DmJdbcDriver*.jar`） |
| sidecar profile | owljdbc catalog `dm`：`dm.jdbc.driver.DmDriver`，URL `jdbc:dm://host:port`，family `oracle` |
| 元数据提取器 | `DamengQuerier`（Oracle 窄字典变体：无 `collation`/identity 列；周边对象查询失败降级跳过） |
| DDL 目标方言 | **无 dm 方言** —— 目标端必须显式 `ddl.target_dialect: oracle` |
| 端口 | 5236 |
| 连接串示例 | `dm://SYSDBA:SYSDBA001@127.0.0.1:5236`（postgres 族解析器兼容该 URL 形式） |

## 测试环境

| 实例 | 版本/模式 | 说明 |
|------|----------|------|
| dameng（本机 docker `kongxr7/dameng`） | DM Database Server 64 V8 | SYSDBA/SYSDBA001（`SYSDBA_PWD` 注入）；测试用户 `OWLE2E`（GRANT DBA） |
| oracle-19c（本机 docker） | 19c 非 CDB，服务名 ORCLCDB | ⚠️ 库字符集 `WE8MSWIN1252`（单字节），存不了中文——oracle 侧种子用 ASCII，中文保真由 og_ora 链路验证 |
| openGauss og_ora（远程 172.20.214.44:6432） | A 模式（Oracle 兼容） | ogadmin；UTF8，中文保真验证主力 |

测试数据：每库独立 schema（`OWLE2E`/`owle2e` + 回程 `OWLE2E_RT`/`owle2e_rt`），3 表 × 15 行，覆盖 NUMBER/定点/CHAR/VARCHAR2/DATE/TIMESTAMP(6 微秒)/NULL/负数/大整数(2^53 附近)/中文。

## 四条链路结果（v0.6.0）

| # | 链路 | 通道 | 结果 |
|---|------|------|------|
| 1 | oracle → dameng | native → agent | ✅ 45/45 |
| 2 | dameng → oracle（回程 schema） | agent → native | ✅ 45/45 |
| 3 | og_ora → dameng | native → agent | ✅ 45/45 |
| 4 | dameng → og_ora（回程） | agent → native | ✅ 45/45 |

对拍：12 组导出 CSV 比较，**数据值全部逐值相等**（含中文、微秒时间戳、大整数、负数、NULL）。残差均为渲染差异，非数据差异：

- 小数前导零/尾零：pq 输出 `.0135`、go-ora float64 输出 `246.9`，达梦 DECIMAL 输出 `0.0135`/`246.90`（数值等价）。
- 表头大小写：og 字典小写 vs 达梦字典大写。
- 时区墙钟：naive 时间戳经 agent 绑定用编码端时区偏移渲染（`DateTimeValue`），往返墙钟不漂移。

## 本轮修复清单（dameng 接入暴露）

1. `owljdbc/client.go`：classpath 分隔符 `:` → `os.PathListSeparator`（Windows 必修）。
2. `extractor`：`DamengQuerier` 窄字典变体 + 周边对象（触发器/视图/序列/同义词/包/物化视图）失败降级。
3. `service.BuildCreateTableViaDialect`：尊重 `ddl.target_dialect`；源≠目标方言一律走 LogicalType IR。
4. `dm`/`timesten` 归一 oracle 族：`TargetTypeFamily` / `importer.isOracle` / `exporter.isOracle` / `dbconn.Family`（批量 TRUNCATE、占位符族）。
5. `config.ApplyDefaults`：补 `export.csv.header` / `import.csv.has_header` 默认 true（否则 importer 把首行数据当表头）。
6. owljdbc DATETIME 帧协议追加 4B 纳秒（亚毫秒保真，Go/Java 对齐，**需与 sidecar jar 同版本**）。
7. Java `Session.bind`：Timestamp + 编码端时区偏移 Calendar（naive 墙钟不漂移）。
8. Java `ValueCodec`：Clob/Blob 读取内容（修复 `DmdbNClob@hash`）。
9. openGauss/PanWeiDB A 模式 mapper：PG 风格类型名归一（`numeric`/`timestamp without time zone` → oracle mapper，修复落成 CLOB）。
10. oracle mapper NUMBER 两处：precision=0 不再误判 SmallInt；>18 经 LBNumeric 保留精度（scale=0 不再退化为裸 NUMBER）。
11. `registry.Register("dm", oracle.New())`（dameng 源抽取类型可直接映射）。
12. `ensureOneTable` 打印实际建表 SQL（诊断）。

## 使用要点

- 目标端为 dameng 时：`target.type: dm` + `channel: agent` + **`ddl.target_dialect: oracle`**（ValidDialects 不含 dm）+ `agent.jars_dir`（owl-agent.jar 与 DmJdbcDriver18 同目录）。
- 标识符：og 侧建表建议裸小写（`no_quote_identifiers: true` 使达梦端折叠为大写，往返字典一致）。
- 达梦 `auto` 通道即可（无 native 驱动自动落 agent）；owl-agent.jar 缺失时首次连接自动下载（内网用 `OWLJDBC_AGENT_JAR_URL`）。

## 已知边界 / 后续

- migrate 建表不带主键/索引（所有方言既有设计，DDL 保真走 export-ddl）；导出端有 OFFSET 分页告警。
- dameng 元数据的 identity/序列不采集（与 OceanBase 同模式）；DM 的 `ALL_TRIGGERS` 等周边字典降级为 0 条。
- og-mysql（B 模式）mapper 存在与 og-oracle 同型的 PG 风格类型归一缺口（本轮未测，见 dts 备忘）。
- `./jars/owl-agent.jar` 为本地重建版（含协议扩展），与 release v0.1.0 的 jar 不兼容；发版需同步发布新 owl-agent.jar。
