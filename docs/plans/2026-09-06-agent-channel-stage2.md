# Agent 通道产品化接入计划（阶段二：native 优先、agent 保底、跨项目复用）

> 日期：2026-09-06　分支：`feat/agent-driver`　状态：草案（待评审）
> 前置：`2026-09-05-jdbc-agent-architecture.md`（阶段一架构）+ `2026-09-05-jdbc-agent-stage1.md`（阶段一实现，已合入 e7b3982）

## 0. 目标与原则

阶段一交付了通用 JDBC 通道（owljdbc 驱动 + JVM sidecar + 对拍 harness），但产品 CLI 尚未接线。本计划回答三个问题：

1. **怎么接线**：`export-metadata` / `migrate` / `import` / `export data` / `export ddl` / `online` 全部经 `internal/cmd/metadata.go openDB → dbconn.Open` 开连接，只需在 `dbconn.Open` 单点做通道分发，命令层零改动。
2. **怎么控制**：**native 优先，agent 保底**——native 可用时绝不启动 JVM；默认行为与本计划合入前完全一致（现有 OceanBase / MySQL / PostgreSQL / Oracle / openGauss / PanWeiDB / GoldenDB / sqlite3 / duckdb 用户零感知）。
3. **怎么复用**：通道代码抽为独立 Go module，owl-migrate 与其他项目共同依赖。

## 1. 现状：已实现的 agent 流程（阶段一，e7b3982）

| 能力 | 调用方 | 验证状态 |
|---|---|---|
| 驱动注册 / JSON DSN / profile 复用 / 帧协议 / BindRewriter / 值编码 / CANCEL / 崩溃 fail-fast | 单测 + `testdata/fake_agent` | ✅ `go test ./internal/agent/` |
| 元数据抽取（`extractor.Extract`，TableDef/Column/Index/FK） | e2eagent harness 双通道对拍 | ✅ OB 双租户逐项一致 |
| CSV 导出（`exporter.New`） | harness 双通道对拍 | ✅ 50k 行逐字节一致 |
| 导入 + savepoint（`importer.New`） | harness shadow 表 | ✅ 行数 + 内容一致 |
| 扩展冒烟（MySQL 8.4 / PG 16.3 导出导入） | `extension_e2e_test.go`（`-tags e2e`） | ✅ |
| Oracle / TimesTen / GoldenDB profile | 仅目录登记 | ⚠️ 未测（库未起） |
| **产品 CLI 接线** | **无** | ❌ 阶段二本计划 |

关键结论：agent 通道对「导出元数据」等能力**能力齐备且已实测**，缺的只是产品路径的路由。

## 2. 通道选择策略（核心决策）

### 2.1 配置面

`config.DBConfig` 新增一个字段 + 一个可选段：

```yaml
source:
  type: oceanbase-oracle
  dsn: oceanbase://user@tenant:***@host:2881
  channel: auto        # native(默认) | agent | auto；空值 = native
agent:                 # 全局段（缺省可全部不填）
  jars_dir: ./jars     # 驱动 jar 搜索目录；agent jar 内置相对路径解析
  java_home: ""        # 缺省取 PATH 中的 java
  fallback_on_error: false  # native 连接失败是否回退 agent（默认 false，见 2.3）
```

CLI 同名 flag（`--channel`、`--jars-dir`）覆盖配置，便于临时验证。

### 2.2 决策表（dbconn.Open 单点实现）

| channel | type 有 native 驱动 | 行为 |
|---|---|---|
| `native`（默认） | 任意 | 现行为不变；**agent 永不启动，零 JVM 开支** |
| `native` | 无（未知 type） | 报错 `unsupported database type`（现行为） |
| `agent` | 任意 | 直接走 owljdbc（验证 / 手动兜底） |
| `agent` | 无 | 走 owljdbc，按内置目录解析 driverClass/URL/family |
| `auto` | 有且已链接 | **native**；连接失败报错不回退（保真，除非 `fallback_on_error: true`） |
| `auto` | 有但未编译（缺 `-tags ob/og/...`） | 回退 **agent** + warn 日志「native driver not compiled, fallback to agent channel」 |
| `auto` | 无（JDBC-only 新库） | 走 **agent**（保底主场景） |

### 2.3 fallback 语义边界（防歧义）

- **"没有 native" = 驱动不存在或未编译进本二进制**——确定性判断，`dbconn.Open` 现有 `driverLinked` / `driverBuildTag` 即可判定，零连接开销。
- **native 连接失败默认不回退**：连接错误多数是 DSN/网络/权限问题，静默换通道会双倍失败延迟并掩盖真实错误。`fallback_on_error: true` 时回退，但最终错误信息必须保留 native 原始错误（`native: <err>; agent fallback: <err2>`）。
- 回退发生时打结构化日志（channel 决策 + 原因），保证「为什么启动了 JVM」可追溯。

### 2.4 JVM 开支控制（既有机制，无需新做）

- 1 classpath profile = 1 JVM，连接池多连接复用（`Manager` refs 计数）；`MaxOpenConns` 调大不会多起 JVM。
- 默认 `channel=native` 下 JVM 进程数为 0；`auto` 仅在 native 不可用那一刻才 spawn。
- 全部连接关闭 refs 归零即 Kill，不留常驻。

### 2.5 编码不变量（连接层字符集控制，新增）

**背景（现状核查）**：工程内现有 `encoding` 配置全部是 CSV/XLSX 文件层（`metadata.csv.encoding`、导出/导入文件编码、`DataTransforms.source_encoding`）；数据库连接层无任何字符集识别、注入或校验，数据正确性依赖驱动隐式行为：

| 驱动（已核实源码） | 隐式行为 | 风险 |
|---|---|---|
| go-sql-driver/mysql v1.10（mysql/mariadb/oceanbase-mysql/goldendb-mysql） | 握手默认 utf8mb4_general_ci，服务端转换后返回；驱动不做转换，字节原样进 Go string | DSN 显式 `charset=gbk` 时 GBK 字节被当 Go string 用 → 乱码，无检测 |
| lib/pq v1.12（postgres/opengaussdb 全变体/panweidb） | `client_encoding` 仅在 DSN 显式给定时发送，默认跟随服务端 server_encoding | GBK server_encoding 实例 + 未设参数 → 非 UTF-8 字节进 Go string |
| go-ora v2.9 / obconnector-go v0.4（oracle/oceanbase-oracle） | 连接协商读服务端字符集 id，自建 converter 转 UTF-8，不支持报错 | 基本安全，无需注入 |
| agent：JDBC（帧协议/值编码已全程 UTF-8） | 源端字符集→String 由 JDBC 驱动转换（Connector/J 8 默认 UTF-8；PgJDBC 按服务端；Oracle JDBC 按 NLS） | URL 未显式化编码参数 |

**方案：管线不变量 = 进程内与 CSV 中间表示一律 UTF-8**（Go string 语言保证），据此做注入/校验/告警三件事（仿现有 `InjectOracleParams` / `InjectPGSearchPath` 先例）：

1. **Native 注入/校验**：mysql 族校验 DSN `charset` 为 utf8mb4/utf8 系（缺省不注入，依赖驱动默认；显式非 UTF8 值 warn+覆盖，可配置报错）；postgres 族注入 `client_encoding=UTF8`；oracle 族不注入（驱动自转换），连接后探测 `NLS_CHARACTERSET` 打日志。
2. **Agent URL 注入**：mysql 族 JDBC URL 幂等追加 `characterEncoding=UTF-8`（已有则不覆盖）；PG/Oracle JDBC 保持默认。
3. **探测告警（可选开关）**：连接建立后单次探测（mysql `character_set_server/connection`；pg `server_encoding`；oracle `nls_database_parameters`），非 Unicode 时 warn；列级字符集元数据已由 extractor 捕获（`ColumnDef.CharacterSet`），可复用做列级提示。

## 3. 类型目录（保底对象清单）

沿用架构文档 §11 扩展矩阵，落成代码内目录（type → driverClass / URL 模板 / family / 推荐 jar）：

| 类别 | type | 默认通道 |
|---|---|---|
| native 已覆盖 | mysql / mariadb / postgres / oracle / sqlite3† / duckdb† / opengaussdb* / panweidb* / goldendb-mysql† / oceanbase-mysql† / oceanbase-oracle† | native |
| agent 保底（新增 type） | dm / kingbase / yashandb / timesten / goldendb-oracle | agent（无 native 可言） |

†=build-tag 门控（sqlite3/duckdb/ob/og/gdb）；`auto` 下未编译时回退 agent。

`export-metadata` 等命令里的 `dbconn.MetadataSourceType` 抽取类型映射不变——family 语义由 owljdbc 连接级 family 声明承接（oracle `:N→?` 改写在 sidecar，`$N` 直通 PG）。

## 4. 验证策略（在本项目保留每个能力的验证路径）

1. **通道级（已有，保留）**：`internal/e2eagent` harness 双通道对拍——它继续是通道正确性的最高裁判。
2. **产品级（新增，M3）**：`channel: agent` 下跑产品 CLI（export-metadata / migrate / import / export），输出与 native 跑一份做 diff（复用 harness 的 CSV/计数断言）。挂 `-tags e2e`，按需执行。
3. **逻辑级（新增，M1）**：dbconn 通道决策表单测（native/agent/auto × 有/无/未编译驱动），纯逻辑无 JVM、无 DB，CI 常跑。
4. **回归护栏**：默认 `channel=native` 时全量现有测试行为不变（含 `-tags` 报错文案）。

### 4.4 字符集矩阵（GBK ↔ UTF-8，native/agent 双通道，新增）

**目的**：现有对拍矩阵全部是 UTF-8 系库，native（驱动转换栈）与 agent（JDBC 转换栈）在非 UTF-8 源上是否逐字节一致属于盲区；同时验证 2.5 注入项的正确性（不注入时 PG GBK 实例现行为即乱码）。矩阵重心按**入口解析优先**设计：只要源端字节被正确解码为进程内 UTF-8（native 各转换栈 + agent JDBC 转换栈都验证到），出口转码由目标端服务端/驱动承担，可控性随之成立——因此源角色的覆盖比目标角色组合更重要，目标端不追求 7×7 全配对。覆盖用户指定 7 个类型（`opengauss-postgresql` 对应仓库 type `opengaussdb`）：

> **待处理（2026-09-06）**：OB GBK 租户因资源不足暂缓构建。替代先行项：① fake_agent 增加探针剧本（假定 OB-Oracle UTF8 租户 / MySQL utf8mb4 返回），锁 mock 契约；② 在现有 **UTF8 租户**上用真实 e2e 确认 `dbconn.ProbeServerEncoding` 判断服务端编码（native/agent 双通道同值）且 GBK 出口转码逐字节一致（`charset_e2e_test.go`）。GBK 租户到位后补跑 S1 的真 GBK 入口场景与 S2-o。

| 类型 | 角色 | GBK 实例准备 |
|---|---|---|
| mysql | 源/目标 | `CREATE DATABASE … CHARACTER SET gbk`（e2edev fixture 机制已支持建库，`fixture_sources.go` 现为 utf8mb4，扩展 gbk 即可） |
| oceanbase-mysql | 源/目标 | gbk 库（租户 character_set_server=gbk 或按库指定） |
| oceanbase-oracle | 源/目标 | GBK 字符集专用租户（建租户时指定），探测 `NLS_CHARACTERSET` |
| postgresql | 源/目标 | 独立 GBK/GB18030 初始化实例（`initdb -E GB18030`；UTF8 集群内不能建 GBK 库） |
| opengaussdb（=opengauss-postgresql） | 源/目标 | 同上，独立 GBK/GB18030 实例 |
| opengaussdb-mysql | 源/目标 | 同上（兼容模式影响方言，wire 仍 postgres 协议，client_encoding 路径不变） |
| opengaussdb-oracle | 源/目标 | 同上 |

**场景定义**（每类型 T 遍历；源/目标各含 GBK、UTF8 两份实例）：

| 场景 | 源 | 目标 | 考察点 |
|---|---|---|---|
| S1 | T 的 GBK 实例 | T 的 GBK 实例 | 同编码双端往返；GBK 全字符集保真 |
| S2 | mysql（utf8mb4，固定基准源） | T 的 GBK 实例 | UTF8→GBK 落地：转换发生在目标端服务端；GBK 不可表示字符的预期语义（报错/替换）按引擎断言 |
| S3 | T 的 GBK 实例 | postgresql（UTF8，固定基准目标） | GBK→UTF8 抽取：源端正确转出 UTF-8（2.5 注入的核心受益场景） |

**补充交叉源场景 S2-o（源端 OceanBase-Oracle GBK）**：源=oceanbase-oracle(GBK) → 目标 ∈ {mysql(GBK), opengaussdb(GBK)}，通道组合仅 {native×native, agent×agent}。理由：oceanbase-oracle 的 GBK 源已在 S3（→postgresql UTF8）覆盖「入口解码 + UTF8 出口」，S2-o 补「入口解码 + GBK 出口」跨栈一对即可；两套源端转换栈（native=go-ora、agent=JDBC）各验证一次，无需 4 组合全跑。

**通道组合**（每场景 4 组，体现保底切换）：`native×native`、`native×agent`、`agent×native`、`agent×agent`（源通道 × 目标通道独立选择）。mixed 组合（native×agent / agent×native）正是真实保底部署形态——一端无 native 时。

**数据集与断言**：

- 数据集分两档：GBK 安全集（常用中文、全角标点、GBK 范围生僻字、LATIN1 区）用于全部场景；UTF8 超集集（emoji、CJK 扩展 B）仅进 UTF8 端，对 GBK 端断言明确失败/替换语义（native 与 agent 必须一致）。
- 断言链：目标端回读 == 期望值（逐行逐列）→ 目标端再导出 CSV 与基准 CSV 逐字节比对（复用 harness 断言）→ 同场景 4 个通道组合的回读结果彼此逐字节一致。

**分层执行（控制规模）**：

| 层级 | 范围 | 数量级 |
|---|---|---|
| P0（必跑） | mysql、oceanbase-oracle × S1/S2/S3 × 4 通道组合；**S2-o**（oceanbase-oracle GBK 源 → mysql/opengaussdb GBK，2 组合） | 24 + 4 |
| P1（全量覆盖） | 其余 5 类型 × S1/S2/S3 × {native×native, agent×agent} | 30 |
| P2（抽样） | 其余类型 × mixed 通道组合 + 跨族配对（如 oceanbase-oracle(GBK) → opengaussdb(GBK)） | 按需 |

## 5. 跨项目复用（模块抽取）

阶段一已保证 `internal/agent` 零上层依赖（不 import config/dbconn/CLI）。抽取方案：

- 新模块 `github.com/cangyunye/owljdbc`（独立 go.mod）：
  - Go 包 `owljdbc`：driver / Config / EncodeDSN（现 internal/agent 平移，`init()` 注册语义不变）
  - `jvm/owl-agent/`：Java sidecar 源码 + build.sh，与 Go 驱动同仓同版本（协议靠 PING 握手兼容）
  - `catalog/`：每库接入目录（driverClass / URL 模板 / family / jar 坐标）+ jar 下载脚本
- owl-migrate：`go.mod` require + 开发期 `replace`；dbconn 的通道分发与目录查询指向该模块。
- 其他项目复用 = import `owljdbc` + `sql.Open("owljdbc", cfg)` 两行，或仅引 catalog 数据。

## 6. 里程碑

| # | 内容 | 验收 |
|---|---|---|
| M1 | `DBConfig.Channel` 字段 + dbconn.Open 通道分发 + 内置类型目录 + 决策表单测 | 决策表全绿；默认路径现有测试零 diff |
| M2 | CLI/config 面：`--channel` / `agent:` 配置段 / `--jars-dir` / DSN 打码复用 config/mask；**编码不变量（§2.5）**：mysql 族 charset 校验、postgres 族 `client_encoding=UTF8` 注入、agent URL `characterEncoding` 注入、可选探测告警 | `owl-migrate export-metadata --channel agent` 可对 OB 跑通；对 GBK 实例的非 UTF8 DSN 告警/拦截有单测 |
| M3 | 产品级对拍 e2e：export-metadata / migrate / import / export 在 OB 双租户 + MySQL/PG 上 agent vs native；**字符集矩阵（§4.4）P0 先行，P1 跟进** | 元数据一致、CSV 逐字节一致、导入计数一致（复用 harness 断言）；P0 字符集矩阵 4 通道组合回读彼此一致 |
| M4 | 模块抽取 `owljdbc` 独立仓库 + owl-migrate 切依赖 | owl-migrate 构建/测试全绿；新项目两行接入 demo |
| M5（可选） | `fallback_on_error` 开关 + 回退结构化日志 + 吞吐优化（row batching，见风险） | 回退路径有日志有断言；吞吐 ≥ native/3 目标重新评估 |

## 7. 风险与对策

| 风险 | 对策 |
|---|---|
| auto 回退语义被误解（连接失败静默换通道） | 回退仅限「native 不可用」；连接失败回退默认关、显式开、错误保真 |
| agent 吞吐 0.14–0.30× native | 保底场景多为小众库，吞吐可接受；性能敏感路径保持 native；M5 row batching |
| GBK 实例准备成本（PG 系需独立 initdb 实例、OB 需专用 GBK 租户） | 复用 e2edev fixture 机制（现成建库先例）；GBK 实例/租户清单进 e2e 环境说明；缺实例按惯例 skip |
| GBK 不可表示字符（emoji/CJK 扩展 B）语义因引擎而异 | 数据集分档（GBK 安全集 / UTF8 超集集）；失败/替换语义按引擎在用例中显式断言，且要求 native 与 agent 行为一致 |
| driverClass/URL 目录漂移（厂商 jar 升级） | 目录与 jar 坐标集中在 owljdbc 模块 catalog，版本化维护；实测矩阵随动 |
| JVM 环境缺失 | 缺 JRE 时报错带安装指引（阶段一已有约定）；auto 下作为「agent 不可用」原样报错 |
| sqlite3/duckdb 等嵌入式库无 JDBC 等价物 | 目录中标记 `agent: unsupported`，auto 时不回退、保持原报错 |

## 8. 明确不做（YAGNI）

- 双通道同时连接做实时对比/自动择优（对拍只在 e2e）。
- agent 连接池独立调参（复用 PoolConfig，profile 复用已摊薄成本）。
- online/CDC adapter 与 agent 通道合并（沿用阶段一边界，仅因走 dbconn 而「顺带可用」）。
- 多 JVM 负载均衡 / agent 集群。
