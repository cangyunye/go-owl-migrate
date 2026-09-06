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
| M2 | CLI/config 面：`--channel` / `agent:` 配置段 / `--jars-dir` / DSN 打码复用 config/mask | `owl-migrate export-metadata --channel agent` 可对 OB 跑通 |
| M3 | 产品级对拍 e2e：export-metadata / migrate / import / export 在 OB 双租户 + MySQL/PG 上 agent vs native | 元数据一致、CSV 逐字节一致、导入计数一致（复用 harness 断言） |
| M4 | 模块抽取 `owljdbc` 独立仓库 + owl-migrate 切依赖 | owl-migrate 构建/测试全绿；新项目两行接入 demo |
| M5（可选） | `fallback_on_error` 开关 + 回退结构化日志 + 吞吐优化（row batching，见风险） | 回退路径有日志有断言；吞吐 ≥ native/3 目标重新评估 |

## 7. 风险与对策

| 风险 | 对策 |
|---|---|
| auto 回退语义被误解（连接失败静默换通道） | 回退仅限「native 不可用」；连接失败回退默认关、显式开、错误保真 |
| agent 吞吐 0.14–0.30× native | 保底场景多为小众库，吞吐可接受；性能敏感路径保持 native；M5 row batching |
| driverClass/URL 目录漂移（厂商 jar 升级） | 目录与 jar 坐标集中在 owljdbc 模块 catalog，版本化维护；实测矩阵随动 |
| JVM 环境缺失 | 缺 JRE 时报错带安装指引（阶段一已有约定）；auto 下作为「agent 不可用」原样报错 |
| sqlite3/duckdb 等嵌入式库无 JDBC 等价物 | 目录中标记 `agent: unsupported`，auto 时不回退、保持原报错 |

## 8. 明确不做（YAGNI）

- 双通道同时连接做实时对比/自动择优（对拍只在 e2e）。
- agent 连接池独立调参（复用 PoolConfig，profile 复用已摊薄成本）。
- online/CDC adapter 与 agent 通道合并（沿用阶段一边界，仅因走 dbconn 而「顺带可用」）。
- 多 JVM 负载均衡 / agent 集群。
