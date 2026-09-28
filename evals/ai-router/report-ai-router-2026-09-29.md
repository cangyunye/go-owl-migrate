# AI 路由评测报告（阶段一路由 + 阶段二配置生成）

日期：2026-09-29 · 分支：`feat/ai-chat-router-research` · 语料：`corpus.jsonl` 62 条 · 被测模型：deepseek-flash（V4.1-Flash）、deepseek-v4-pro、GLM（本机作者模型）

## 结论摘要

1. **路由可行**：deepseek-flash 在 62 条语料上 route 命中 59/62（95%），七大场景 + 范围外/歧义/多轮全部分类正确（除 3 条边界）。路由表 + 系统提示词（`router_system.md`）的形态被验证。
2. **模型选型**：flash 强于 pro 且便宜得多。pro 的主要失分模式是**过度澄清**（12 条把依赖上下文的延续句判成 clarify），flash 更贴合"延续句默认复用上下文"的路由约定。
3. **配置生成必须有字段参考**：不给字段值域时模型大量编造字段（`metadata.type: extract`、`import.encoding`、`channel: jdbc`）；把字段参考注入系统提示词后断言全过。印证"路由到的子命令必须有 references"的前提。
4. **凭据绝不能经 LLM 输出**：实测模型会把请求里的真实密码"脱敏"成字面量 `password`，甚至修复轮收到认证失败后把密码整个删掉。最终方案 = **LLM 输出 `__PWD_*__` 占位符，确定性代码注入真实凭据**，7/7 全绿。
5. **评测发现并修复了一个真工具 bug**：在线 `export data --format sql|xlsx` 静默降级输出 CSV（`exportOneTable` 硬编码 CSV 路径），已修复并回归通过（`internal/transfer/exporter/exporter.go`）。

## 阶段一：意图路由（同语料 × 3 模型）

| 模型 | route 命中 | route+sub 全对 | token 消耗（completion） | 备注 |
|---|---|---|---|---|
| deepseek-flash | **59/62 (95%)** | 41/62 | 19.3k | effort=low，快且便宜 |
| deepseek-v4-pro | 48/62 (77%) | 34/62 | 56.3k | 过度澄清为主因 |
| GLM（作者） | 62/62 | 62/62 | — | 作者即出题人，存在偏向，仅供参考 |

模型间一致率：flash vs pro 49/62；flash vs GLM 59/62；pro vs GLM 48/62。

### flash 的 3 条 route 误判

| id | 语料 | gold | flash | 分析 |
|---|---|---|---|---|
| r48 | 金仓做目标方言建表 | migrate（纠正 target_dialect→postgres） | out-of-scope | 系统提示词把"金仓仅连接类型"写成了禁令，模型按字面拒答；应改写为"可迁但需纠正"的示例 |
| r54 | 两库同步保证一直一致 | clarify | online-sync | 可辩护："一直一致"暗示持续增量；gold 偏严 |
| r61 | oracle 导表为 csv（DSN 含 `!`） | export-data | clarify | 密码特殊字符+缺 schema 触发保守澄清；可在提示词加示例消除 |

### sub 级偏差（18 条，多为良性）

- out-of-scope 6 条全部漏填 sub 标签（rename/compare 等）——sub 词表对 out-of-scope 未强制，改进项。
- 多标签倾向：`schema-mapping,drop`、`parallel,format:csv`——**sub 应改为数组或闭集枚举**。
- 语义性差异仅 2 条：r12 `unsupported-format` vs gold `format:sql`（模型认为 mysql 源不支持 sql 格式，其实 gold 也要求告知不支持）、r55 `unsupported-filter` vs `exclude-note`（同上，都是"路由对但备注需更准"）。

### pro 的失分模式（改进输入）

12 条 clarify 集中在"延续句"（`导入前把目标表清空`、`改用 copy 提速`）——pro 严格按"缺上下文即澄清"，flash 按"延续句默认有上下文"路由。**系统提示词需要显式声明这一约定**（已有，但 pro 权重更高仍保守）；生产上应传会话对象兜底。

## 阶段二：配置生成（deepseek-flash，7 项 × 三轮迭代）

| 项 | 场景 | 终态 |
|---|---|---|
| c1 | mysql→csv 导出 | CLI rc=0，csv 5 行 ✓ |
| c2 | pg→insert sql | rc=0，.insert.sql ✓（依赖工具 bug 修复） |
| c3 | oracle 元数据（密码含 `!` 转义） | rc=0，tables.csv ✓ |
| c4 | mysql→pg 真实迁移 | rc=0，pg 实测 5 行 ✓ |
| c5 | oracle gen-select | rc=0 ✓ |
| c6 | 导入配置（truncate+GBK） | 结构断言 4/4 ✓ |
| c7 | 达梦 agent 通道 | 结构断言 3/3 ✓ |

### 三轮迭代记录（每轮都是一个可复用结论）

1. **裸提示词**：模型编造字段值域（`metadata.type: file/sqlite/extract`、`import.encoding`、`channel: jdbc`、`agent.jarDir`）、漏必填段。→ **字段参考必须进提示词**（用户预设"路由化 + 字段明确 references 就能理解"被直接验证）。
2. **字段参考注入后**：字段幻觉清零；新失败模式浮出——**凭据脱敏**（c4 把真实密码写成 `password` 字面量；修复轮回喂"认证失败"后模型把密码整个删掉，`using password: NO`）。同批 c3 的内联 DSN 密码却原样通过——行为不稳定，不可依赖。
3. **凭据占位符协议**（LLM 输出 `__PWD_*__`，代码注入真实凭据）+ 修复轮扩到 3 次：**7/7 全绿**。这是本评测最重要的架构结论：**凭据永远不过 LLM 输出层**。

## 工具 bug（评测副产物，已修复）

- **现象**：在线 `owl-migrate export data --format sql|xlsx` 静默输出 CSV（文件名 `.csv`、内容 CSV）。根因：`exportOneTable` 硬编码 CSV 流式写盘（文件名拼死 `.csv`、行走 `rowToCSV`），`Config.Format` 在该路径未被消费。
- **修复**：`exportOneTable` 改用 `createWriter` 分发（writer 层本就是流式 `WriteHeader/WriteRow`，分页语义与内存特性不变）；csvWriter/sqlWriter 补 `time.Time` 格式化对齐旧行为（csv 紧凑 `20060102150405`，sql 标准 `2006-01-02 15:04:05`）。
- **回归**：`go test ./internal/...` 26 包全过；三格式 × pg 实连手测通过。

## 局限

- 语料由 GLM 作者编写，gold 与其作答同源；双模型对比的主要信号在 **flash vs pro 的独立一致性**（49/62）与 flash vs gold。
- 阶段二仅 7 项、单模型、单供应商；c6/c7 仅结构断言（无真实达梦容器）。
- 路由提示词的 sub 词表是开集，精确匹配偏严；生产化前应收敛为闭集或数组。
