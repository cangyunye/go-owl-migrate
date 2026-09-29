# AI 对话路由设计稿：自然语言 → 工具命令/配置（2026-09-28）

状态：草案（待评审）。本文回答两个问题：**工具边界内的 AI 对话层应该长什么样**；**值不值得内置，还是只优化 skill 交给外部 agent（ZCode/Claude Code 等）使用**。

## 0. 一句话定位

在工具边界内做一层"自然语言 → 意图路由 → 槽位收集 → 配置生成 → 确认执行"的对话能力；**LLM 只做理解与填槽，一切执行走确定性代码路径**。本工具是配置驱动的——只要意图能路由到子命令，而每个子命令有例句（examples）与字段明确的 references，就能生成正确配置。这就是路由化的可行性依据。

## 1. 目标与非目标

**目标**

- 用户说"把 xx 迁移到 xx"、"导出 xx 库 xx 表 100 条，条件为 …，输出 csv"，能走通到正确的命令 + config.yaml，人工确认后执行。
- 意图**限定在工具能力以内**：路由表之外的请求拒答，并引导到最近似的可用能力（引用硬约束清单：无表级重命名、无数据比对、表范围无排除语法等）。
- AI 的理解**基于现有文本**：路由表从 SKILL.md 场景决策表、references/、docs/config.md、`evals/owl-migrate/corpus.jsonl`（65 条例句）提炼，**不新写第二份说明文档**；发现现有文本覆盖不了的功能，先补文档（文档补全同时服务人类用户），是功能本身缺口的上报产品决策（见 §6）。
- 按**一个命令一个命令**的节奏切片落地，首切片 export data。

**非目标**

- 不做自由 SQL 问答/DBA 助手；不做工具能力之外的迁移咨询。
- 不让 LLM 直接拼最终命令执行——生成的命令与配置必须过既有校验（`config.Load`、preflight）并经用户确认。

## 2. 三层架构

```
用户话语
  │
  ▼
① 意图路由层   utterance → (route_id, slots已提取, slots缺失)     ← 路由表（§3）
  ▼
② 槽位→配置层  slots → migrate.yaml 片段 + 命令行模板实例化        ← 每命令一模板
  ▼
③ 执行与反馈层 dry-run 展示 → 用户确认 → 执行 → 产物/报错回贴       ← 确定性代码
```

- **路由层**只输出结构化意图与槽位，允许 LLM 参与；命中错误由用户在确认环节兜底。
- **配置层**是模板 + 校验，纯代码：槽位 → YAML 片段 → `config.Load` 校验失败则把报错原文回贴给 LLM 做**一轮**修复建议（只允许改槽位值，不允许换意图/改模板结构）。
- **执行层**完全复用现有 CLI/serve job 机制（`POST /migrate` → `GET /jobs/{id}`），不新造执行器。

## 3. 路由表：机器可读的能力清单

形态：`docs/ai/router.json`（由现有文本提炼生成，人审后固化）。每条路由：

```json
{
  "id": "export.data",
  "intent": "导出表数据（CSV/INSERT SQL/Excel）",
  "examples": ["导出 oracle scott 下 emp 表数据为 csv", "把 mysql 的 sales 库 orders 表导成 insert sql"],
  "negative_examples": ["导出表结构（→ export-metadata）", "导入 csv（→ import）"],
  "required_slots": ["source_type", "source_dsn|datasource_ref", "schema", "tables"],
  "optional_slots": {"format": "csv", "output_dir": "./output/data/", "parallel_max_workers": 4},
  "command_template": "owl-migrate export data -c {config} --tables {tables} --format {format} -o {output_dir}",
  "config_fragment_ref": "docs/config.md#export",
  "example_config_ref": "references/commands.md#export-data",
  "out_of_scope_note": "不支持 WHERE 条件过滤与行数上限，见 §6"
}
```

要点：

- **范围限定即路由表**：请求在表中无命中 → 拒答模板 = "工具不支持 X；最近似能力是 Y（命令 Z）；已知硬约束：…"。硬约束清单直接引用 SKILL.md，保持单源。
- 路由表与 `evals/owl-migrate/corpus.jsonl` 同源演化：corpus 的 utterance/expected 就是路由表的例句与期望命令，**评测即回归**。
- 首批路由 ≈ SKILL.md 七大场景 + 各命令变体（resume/skip-ddl/sql-offline 等），约 20 条。

## 4. 会话与上下文设计（回应"重复提问/上下文长度/会话保存"）

核心决策：**上下文不依赖聊天记录堆积，依赖结构化会话对象**。

```json
{
  "session_id": "s-20260928-001",
  "intent": "export.data",
  "slots": {
    "source_type": "oracle", "source_endpoint": "datasource:生产库",
    "schema": "SCOTT", "tables": ["SCOTT.EMP"], "format": "csv"
  },
  "missing": [], "confirmed": false,
  "draft_config_path": "~/.owl/migrate/sessions/s-20260928-001/migrate.yaml",
  "created_at": "2026-09-28T10:00:00+08:00", "ttl": "24h"
}
```

| 问题 | 设计 |
|---|---|
| 重复提问是否带上下文 | 带。槽位已确认的直接引用（"还是导到 SCOTT.EMP 吗？"），用户显式改值才覆盖并复述确认 |
| 上下文限制多少 | 每轮 LLM 输入 = 系统提示 + **命中的那一条**路由表 + 会话对象 + 最近 K=8 轮原文。有界（约 10–30KB），与对话总轮数解耦；不把全量路由表塞进上下文，先本地粗路由再加载命中条目 |
| 应不应该保存会话 | 应该，但**服务端保存对象而非对话原文**：serve 复用任务 SQLite（`~/.owl/migrate/owl-migrate.db`）加一张 sessions 表，TTL 24h，凭 `session_id` 续聊；CLI 单轮场景可无会话 |
| 敏感信息 | 会话对象里 DSN 密码只存"已设置"布尔或 `datasource:<名>` 引用，不存明文（对齐数据源 AES-256-GCM 设计）；草稿配置落盘前同样脱敏 |
| 换意图/打断 | 检测到意图冲突或用户说"重新开始"→ 新建会话对象，旧会话保留可回退；同会话内改槽位 → 增量更新并重新走确认 |
| 幻觉防护 | 生成的配置必过 `config.Load`；命令行参数白名单来自路由表 template；执行前 dry-run 展示"将执行的命令 + 配置 diff" |

## 5. 首个垂直切片：export data

用户例句："导出 xx 数据库 xx 表的数据 100 条，条件为完整 select 语法或 where 子句，输出 csv。"

槽位提取与落点：

| 槽位 | 例句值 | 落点 | 现状 |
|---|---|---|---|
| source 三件套 | xx 数据库 | `source.type/dsn/schema` | ✅ |
| 表 | xx 表 | `--tables` / `export.tables.include` | ✅ |
| 行数上限 | 100 条 | — | ❌ **无对应能力** |
| 过滤条件 | WHERE / SELECT | — | ❌ **无对应能力** |
| 输出格式 | csv | `--format csv` | ✅ |

对话状态机：识别意图 → 提取已知槽位 → 必填缺失时**一次问全**（复用 SKILL.md「拷问规则」，不挤牙膏）→ 生成配置 + 命令 → dry-run 确认 → 执行 → 回报产物路径（`{schema}.{table}.csv`）。

## 6. 切片暴露的能力缺口（需产品决策，AI 层不得掩盖）

1. **条件导出**：`export data` 按表全量导，无 WHERE/自定义 SELECT。选项 A（推荐）：新增 `export.tables.<t>.where`（甚至 `query` 覆盖），迁移/抽数场景高频，属工具价值而非 AI 价值；选项 B：AI 拒答该子意图，引导 `gen-select` 生成 SELECT 语句人工执行，或导出后离线过滤。
2. **行数上限**："导出 100 条"无 `--limit` 可落。选项 A（推荐）：`export.limit`；选项 B：`gen-select` 页大小近似（语义不准）。
3. **agent 通道环境前置**：dm/金仓/TimesTen 走 agent 通道需要 Java + 驱动 jar。路由表对这类 source type 附加前置检查步骤（`owl-migrate version` / `GET /api/v1/capabilities`），缺 jar 时给出 `fetch-jars.sh` / `OWLJDBC_AGENT_JAR_URL` 指引，而不是让连接报错兜圈子。

> 与任务书"现有文本不够充分就补全"的对应：1/2 是**功能**缺口（补文档解决不了，需决策）；路由表本身、各命令 examples/字段表是**文档**缺口（按 P0 补齐，见 §8）。`ddl.table_filter` 配置字段存在但未接线，属于"文本会误导 AI"的典型——路由表必须显式标注"勿用"，避免 LLM 看到配置模板就推荐。

## 7. 安全与确认分级

| 级别 | 命令 | 策略 |
|---|---|---|
| 只读 | version / validate / show-query / capabilities / 元数据查看 | 直接执行 |
| 产文件 | export data / ddl / insert / gen-select / export-metadata | dry-run 展示命令+配置，确认后执行 |
| 写目标库 | migrate / import / online init --apply | dry-run + 配置 diff 高亮破坏性项（`truncate_before` 默认关，开启必须显式高亮确认）+ 二次确认 |

## 8. 评估：内置 AI 还是优化 skill 交给外部工具？

**结论：本轮不内置 LLM。做"确定性底座 + 外部 agent 宿主"，底座三件事按价值排序：**

1. **路由表 artifact 化**（`docs/ai/router.json` + 每命令 examples/字段表补全）。这是给外部 AI 的燃料，也是给人类用户的文档，非 AI 专属投入。
2. **补齐 §6 功能缺口**（条件导出、行数上限）。没有它们，任何宿主的 AI 都只能拒答或绕路——这是工具自身的能力问题。
3. **（可选）serve 确定性 plan 端点**：`POST /api/v1/command/plan`，入参 `{intent, slots}` → 校验 + 生成配置 + preflight 结果。把"AI 说的话"在落地前用确定性代码验证；外部 agent 与未来的内置 UI 都调它，责任边界清晰。

**不内置的理由**：

- **发行与部署形态**：交付物是二进制 + 内网部署（国产库客户常态）。内置 LLM 意味着模型/API key 管理、离线不可用、私有化小模型的算力要求，全是新成本中心。
- **迭代速度不对称**：外部 agent 生态（skill/提示词/模型）以天迭代，内置功能以版本迭代；AI 理解层的正确姿势是快速试错，不是发版节奏。
- **重复建设**：会话管理、上下文窗口、确认 UI、多轮交互，ZCode/Claude Code 已做且更好；内置等于重做一遍更差的。
- **已有资产对位**：`.agents/skills/owl-migrate/`（路由化 SOP + 硬约束 + references）与 `evals/owl-migrate/`（65 条例句 + run_eval.py 回归）正是"路由化 + 评测"的雏形，扩充即可，不需要新系统。

**翻转条件**（何时重新考虑内置）：产品主打面向非技术用户的 Web 控制台一体化交付（用户机器上没有 agent）；或客户要求私有化一体机内置小模型。届时 §2/§3/§8.3 的底座原样复用，LLM 只是换宿主（serve 加 `/ai` 端点）。

## 9. 决策修订（2026-09-29）：接供应商 API，首轮单供应商 DeepSeek

用户拍板：不训练/内嵌模型，直接接供应商 API；首轮单供应商（DeepSeek），预留多供应商与自定义供应商扩展；可配置项包括模型、上下文、思考强度等。评测已完成并验证架构（见 §10 与 `evals/ai-router/report-ai-router-2026-09-29.md`），要点：

- 路由层提示词即 `evals/ai-router/router_system.md`（词表 + 硬约束 + JSON schema），与 serve `/ai` 端点共用同一份。
- **凭据不过 LLM 输出层**（实测模型会脱敏真实密码）：LLM 输出 `__PWD_*__` 占位符，配置层代码注入真实凭据或解析 `datasource:` 引用。
- **字段参考必须进提示词**（实测缺字段参考时模型编造 `metadata.type: extract` 等）：每命令的槽位表/字段值域作为提示词附录，单源取自 docs/config.md。
- 修复回路：CLI 校验失败 → 报错回喂 → 最多 3 轮，只许改配置不许改意图。
- 推理模型注意：思考 token 计入 `max_tokens`（默认 4096 会截断答案），路由用 `effort=low` + `max_tokens=32768`。

**落地状态（2026-09-29）**：`POST /api/v1/ai/route` 与 `GET /api/v1/ai/status` 已实现（`internal/server/serve/ai.go` + `internal/ai` 包，提示词 go:embed 与 `evals/ai-router/router_system.md` 有 parity 测试锁定）；`ai:` 配置段见 `docs/config.md`。语料 e2e 回归：serve 端点实连 DeepSeek 62 条 route 命中 60/62（`evals/ai-router/run_serve_route_eval.py`）。下一步：配置生成端点（`/ai/plan`，凭据占位符协议 + 修复回路）与会话对象存储。

### 供应商接入 schema（配置段草案）

```yaml
ai:
  provider: deepseek            # 首轮仅 deepseek；预留 openai|anthropic|qwen|doubao|custom
  base_url: ""                  # 留空 = 供应商默认；custom 必填（OpenAI 兼容 /v1）
  api_key_env: OWL_AI_API_KEY   # 密钥只从环境变量读，绝不入配置文件/日志/会话
  model: deepseek-flash         # 如 deepseek-flash / deepseek-v4-pro
  context_window: 1048576       # 仅用于本地预截断与会话预算估算，不参与请求
  effort: low                   # 思考强度 low|high|max，仅推理模型生效（deepseek-flash 实测支持）
  max_tokens: 32768
  timeout: 120s
  max_repair_rounds: 3          # 配置修复回路上限
```

多供应商演进：`ai.providers: {deepseek: {...}, qwen: {...}}` + `ai.default`；自定义供应商 = OpenAI 兼容 `base_url` + `model` + `auth header` 形式。会话存储沿用 §4（结构化会话对象 + TTL 24h），模型可配意味着提示词模板需按供应商做一层薄适配（chat/completions vs messages API）。

## 10. 评测结论（详见 evals/ai-router/report-ai-router-2026-09-29.md）

| 阶段 | 结果 |
|---|---|
| 路由（62 条语料 × 3 模型） | deepseek-flash 59/62；v4-pro 48/62（过度澄清）；GLM 62/62（作者偏向仅供参考） |
| 配置生成（7 项 × 3 轮迭代） | 字段参考注入 + 凭据占位符协议 + 3 轮修复 → **7/7 CLI 实连全绿**（含 mysql→pg 真实迁移） |
| 副产物 | 修复真 bug：在线 `export data --format sql/xlsx` 静默降级 CSV（exportOneTable 硬编码 CSV 写盘） |

## 11. 分阶段落地

| 阶段 | 内容 | 验收 |
|---|---|---|
| P0（随本次） | 技能文档同步 v0.7.0；路由表 v0 提炼（七大场景 + 变体，含"勿用 ddl.table_filter"标注） | router.json 评审通过 |
| P1 | export data 切片 + §6 缺口决策落地；corpus 扩充多轮上下文用例（追问补槽、改值、换意图） | 例句端到端走通，eval 回归通过 |
| P2 | 逐命令切片（migrate → export-metadata → import → online），每命令：例句 + 槽位表 + 模板 + corpus 用例；可选 plan 端点 | 每命令切片合入即评 |
| P3（条件触发） | 若翻转条件成立：serve `/ai` 端点接宿主 LLM，复用 plan 端点与路由表 | — |
