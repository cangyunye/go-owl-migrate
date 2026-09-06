# Agent 通道测试方案（用户指南）

> 面向：使用 owl-migrate 的测试/运维同学
> 适用：`feat/agent-driver` 分支（M1–M4 已完成，`owljdbc/` 嵌套模块 + JVM sidecar）
> 原则：**native 优先，agent 保底**——native 可用时永远不启动 JVM；agent 是
> "没有 native 驱动/驱动没编译"时的兜底通道，或显式指定时使用。

---

## 0. 什么时候走 native，什么时候走 agent（决策规则）

由 `channel` 控制，三选一（yaml `source/target.channel` 或全局 `--channel` flag，
**flag 优先于 yaml**；不填 = native）：

| 你配置的 | 实际行为 |
|---|---|
| 不填 / `native` | **永远 native**，JVM 进程数为 0。现有行为零变化。 |
| `agent` | **永远 owljdbc**（JVM sidecar）。用于 agent 通道验证或手动兜底。 |
| `auto` | native 驱动**存在且已编译** → native（连接失败**报错不回退**）；native 驱动**不存在或没编译进二进制** → 自动回退 agent（stderr 打 `[channel] <type>: native driver not compiled into this binary, using owljdbc agent`）；两边都不支持 → 保持 native 的原始报错。 |

各数据库类型的 native 驱动可用性（决定 `auto` 是否回退）：

| 类型 | native 驱动 | 需要的构建 tag | auto 且缺 tag 时 |
|---|---|---|---|
| mysql / mariadb / goldendb-mysql / oceanbase-mysql | go-sql-driver（内置） | 无 | 不回退，走 native |
| postgres | lib/pq（内置） | 无 | 不回退，走 native |
| oracle | go-ora（内置） | 无 | 不回退，走 native |
| **oceanbase-oracle** | obconnector-go | `-tags ob` | **回退 agent** |
| opengaussdb* / panweidb* | openGauss 驱动 | `-tags og` | **回退 agent** |
| goldendb-oracle | go-ora（内置） | 无 | 不回退 |
| sqlite3 / duckdb | mattn / duckdb | `-tags sqlite3/duckdb` | 不回退（无 JDBC 等价物，保持原报错） |
| dm / kingbase / timesten（无 native） | — | — | **走 agent**（catalog 已登记，dm/kingbase/timesten 未实测） |

**如何确认某次到底走了哪条通道**（三个手段，按可靠度排序）：
1. 回退发生时 stderr 必有 `[channel] …` 行（这是唯一 authoritative 信号）。
2. `channel: agent` 强制时，运行中 `ps -ef | grep owl.agent` 必能看到 JVM。
3. 默认 channel 下，整个迁移过程 `ps` 里**不应**出现 java 进程（出现即 bug）。

---

## 1. 前置准备（一次性）

```bash
# 1) 构建 CLI（按需带 tag；不带 tag 的二进制正好用于测试 auto 回退）
make build                       # 基础版：mysql/postgres/oracle
make build/ob                    # + OceanBase（obconnector）
make build/full                  # + ob og gdb 全家

# 2) 构建 JVM sidecar（产出 owljdbc/jvm/owl-agent/owl-agent.jar；JRE 8+ 可跑）
bash owljdbc/jvm/owl-agent/build.sh

# 3) JDBC 驱动 jar（如果本机还没有）
bash owljdbc/scripts/fetch-jars.sh   # 下载到当前目录：mysql-connector-j / postgresql / oceanbase-client

# 4) Java 可用性
java -version                    # JRE 8+ 即可
```

`jars_dir` 解析顺序：`agent.jars_dir`（yaml）→ `--jars-dir`（flag）→ 当前工作目录。
owl-agent.jar 同理（`agent.agent_jar` 可显式给路径）。

---

## 2. 测试场景与步骤

### T1 默认行为回归（最重要——确认"没碰我的 native"）

```bash
owl-migrate export-metadata -c migrate.yaml --format csv -o /tmp/meta_native
# 另开终端：
ps -ef | grep -c "[o]wl.agent"     # 期望 0：全程无 JVM
```

**通过标准**：结果与升级前一致；无 `[channel]`/`[encoding]` stderr 行；无 java 进程。
这是升级后第一件要做的事——默认路径行为零变化是设计承诺。

### T2 `auto` 的 native 命中

用 `make build/full`（native 齐全）构建，跑：

```bash
owl-migrate export-metadata -c migrate.yaml --channel auto --format csv -o /tmp/meta_auto
```

**通过标准**：无 `[channel]` 行（native 命中不说话）；结果与 T1 一致。

### T3 `auto` 的 agent 回退（保底核心场景）

**故意用不带 `-tags ob` 的二进制**（`make build`）跑 oceanbase-oracle：

```bash
owl-migrate export-metadata -c ob.yaml --channel auto --format csv -o /tmp/meta_auto_ob
```

**期望**：stderr 出现 `[channel] oceanbase-oracle: native driver not compiled into this
binary, using owljdbc agent`；命令成功；`ps` 可见 JVM；结果与带 ob tag 的 native
运行产物**逐字节一致**（见 T5 对拍）。

### T4 `agent` 强制全通道

```bash
owl-migrate export-metadata -c ob.yaml --channel agent --format csv -o /tmp/meta_agent
owl-migrate export data      -c ob.yaml --channel agent -o /tmp/data_agent
owl-migrate migrate          -c ob_agent.yaml --temp-dir /tmp/migtmp
owl-migrate import           -c imp_agent.yaml
```

（yaml 等价写法：`source: {channel: agent, agent: {jars_dir: ./jars}}`，
全局默认放根级 `agent:` 段。）

**通过标准**：四条命令全部成功；中途 kill 掉任何一步不产生"半静默"状态（失败必须
显式报错）。

### T5 双通道产物对拍（字节级等价）

```bash
owl-migrate export-metadata -c ob.yaml --format csv -o /tmp/p_native            # native
owl-migrate export-metadata -c ob.yaml --channel agent --format csv -o /tmp/p_agent
diff -r /tmp/p_native /tmp/p_agent          # 期望：无差异

owl-migrate export data -c ob.yaml -o /tmp/d_native
owl-migrate export data -c ob.yaml --channel agent -o /tmp/d_agent
diff -r /tmp/d_native /tmp/d_agent          # 期望：无差异
```

已知等价性结论（实测背书）：元数据 13 文件、数据 CSV、GBK 导出在两通道逐字节一致；
DATETIME 渲染已按 family 分治（mysql 原样字符串 / oracle LocalDateTime 格式化）。

### T6 字符集场景（GBK）

前置：目标库用 GBK 字符集建（MySQL/OB-MySQL：`CREATE DATABASE … CHARSET gbk`
即可，无需新实例/租户；PG/openGauss 需要 GBK locale 初始化的实例；OB-Oracle
需要 GBK 租户——暂缓）。

| 场景 | 内容 | 预期 |
|---|---|---|
| S1 GBK→GBK | gbk 源库 → gbk 目标库 | 全字符保真 |
| S2 UTF8→GBK | utf8mb4 源（GBK 安全数据）→ gbk 目标 | 服务端转换，内容一致 |
| S3 GBK→UTF8 | gbk 源 → utf8mb4 目标 | 中文正确转出 |
| 超集字符 | emoji 写 GBK 目标 | **必须失败**（native 3988 / agent 1366），不允许静默替换 |

通道维度：每场景用 `--channel` 跑 native 与 agent 两遍，目标回读一致。
（自动化版已内置：`go test -tags "e2e ob" ./internal/e2eagent/ -run TestE2E_CharsetMatrix`。）

### T7 故障路径

```bash
# agent 通道跑一个较慢的导出，中途杀 JVM：
kill -9 <owl.agent 的 java 进程>
```

**预期**：当前命令立即报错（fail-fast，错误含 `agent process closed`），**不重连、
不悬挂**；该 profile 全部连接退出后，下一次 agent 通道操作自动重建 sidecar。
另测：kill 掉 java 后立刻重跑同一命令 → 应成功（自动重建）。

### T8 编码告警与覆盖

给 MySQL 源 DSN 故意写 `?charset=gbk`：

```bash
owl-migrate export data -c mysql_gbk_dsn.yaml -o /tmp/x
# 期望 stderr：[encoding] mysql: mysql dsn charset "gbk" is not UTF-8; overridden to utf8mb4 …
```

**通过标准**：告警出现、导出仍成功（连接以 utf8mb4 说话）。

### T9 自动化套件（回归兜底）

```bash
go test ./... && (cd owljdbc && go test ./...)          # 单测：无 JVM/真库
go test -tags "e2e ob" ./internal/cmd/ ./internal/e2eagent/ -count=1   # 产品级对拍全套
```

已知非问题：`internal/cmd` 里 `TestOpenDB_*` / `TestMigrateE2E_PGToMySQL` 硬编码
`127.0.0.1:3306/5432`，本机无该实例时必失败（存量问题，待改造成读 .local-dev.env）。

---

## 3. 配置速查

```yaml
# 全局 agent 默认（source/target 未单独配置时生效）
agent:
  jars_dir: ./jars          # 驱动 jar 与 owl-agent.jar 搜索目录
  agent_jar: ./jars/owl-agent.jar
  java_home: ""             # 空 = 用 PATH 里的 java

source:
  type: oceanbase-oracle
  dsn: oceanbase-oracle://user:pass@host:2881/svc
  channel: auto             # native(默认) | agent | auto
  agent:                    # 仅本连接覆盖（优先于全局）
    jars_dir: /opt/owl-jars

target:
  type: mysql
  dsn: "user:pass@tcp(host:3306)/db?charset=utf8mb4"
  channel: native
```

命令行覆盖：`--channel agent|native|auto`、`--jars-dir <dir>`。

---

## 4. 边界与注意事项（测试时的已知边界）

1. **性能预期**：agent 通道吞吐约 0.14–0.30× native（row batching 是后续优化项）。
   性能敏感的大表迁移保持 native；agent 用于小众库/保底。
2. **LOB 大字段**：agent 通道暂未做 LOB 流式分块（阶段二优化项），超大 BLOB/CLOB
   受单帧 16 MiB 上限约束。
3. **提交语义**：Oracle 系驱动不自动提交；经产品命令（migrate/import）时框架已处理，
   自研脚本直连时记得 COMMIT。
4. **凭证安全**：数据库口令只经子进程 stdin 首帧传递，不出现在命令行/环境变量；
   日志里 DSN 走 config/mask 打码。
5. **报错语义**：auto 下 native 连接失败**不会**静默切 agent（错误保真）；若确需
   "连不上也换通道兜底"，属规划中的 `fallback_on_error` 开关（M5）。

## 5. 验收清单（打勾即完成）

- [ ] T1 默认路径：结果与升级前一致，全程无 java 进程
- [ ] T2 auto + 全功能二进制：native 命中，无 [channel] 行
- [ ] T3 auto + 缺 tag 二进制：出现 [channel] 回退行，结果正确
- [ ] T4 四条命令 --channel agent 全部成功
- [ ] T5 export-metadata / export data 双通道 diff 为空
- [ ] T6 GBK 三场景 + emoji 必败语义
- [ ] T7 kill JVM 后 fail-fast 且可自动重建
- [ ] T8 gbk DSN 告警出现
- [ ] T9 自动化套件通过（含 6 个已知 localhost 失败的豁免说明）
