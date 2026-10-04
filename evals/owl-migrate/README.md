# owl-migrate AI 技能回归评测

以真实用户语料为基准，对「技能触发后能否把需求正确映射到 owl-migrate 命令」做回归测试。语料即 `corpus.jsonl`，模型答案与判定的权威数据是 `answers-<tag>.json`，对应人读报告为 `report-<tag>-<日期>.md`。

## 运行

```bash
export DEEPSEEK_API_KEY=sk-xxx        # key 只走环境变量，不落盘
python3 run_eval.py \
  --context ../../.agents/skills/owl-migrate/SKILL.md \
  --tag regression$(date +%m%d)
```

- `--context` 是给模型的唯一参考文本：回归技能改动的效果就传 SKILL.md，测内核知识就传 `context-ref.md`。
- `--model` 默认 `deepseek-flash`；`--batch-size` 默认 15（回复截断会自动对半拆批重试）。
- key 不存在/模型不在 `/models` 列表会直接报错退出。

## 判定规则（judge）

把预期步骤与模型步骤各自归一化为签名（`owl-migrate <子命令>` / `METHOD /api/v1/<端点>` / `config` / `build` / `manual-step` / `report`）后比对：

- **MATCH**：预期签名全部命中（`make build/*` 构建步不强制；「人工审核」写在 notes 里也算）。
- **PARTIAL**：部分命中——通常值得看一眼。
- **MISMATCH**：完全未命中。
- **澄清/拒答类**（预期 steps 为空、notes 带【澄清】/【不支持】/【限制】）：要求模型 steps 为空且 notes 表达了澄清或拒答语义。

## 语料设计原则（65 条）

- **信息完整**的请求 → 预期具体命令序列（连接串、schema、表名齐全）。
- **信息不全**的请求（amb-02/03/04/05、s8-01）→ 预期先澄清/取证，不许动手——对应 SKILL.md「第 0 步：信息收集与拷问」。
- **越界**请求（neg-01..04）→ 预期明确说明不支持：存储过程改写、两库数据比对、不支持方言、CLI 无数据源命令。
- 覆盖 9 大场景：迁移执行、数据源、元数据提取、DDL、数据导出、导入、Web/校验/报告、配置生成、嵌入式库。

## 结果

| 轮次 | 参考文本 | 自动通过率 | 说明 |
|---|---|---|---|
| baseline（初版） | context-ref v1 | 40% | 判定脚本噪声为主，暴露 show-query 缺失、s3-08/s2-07 模型错误 |
| baseline2 | context-ref v2 | 71% | 修复判定；暴露「拷问原则 vs 语料前提」不一致 |
| baseline3/final | context-ref 定稿 | 97% | s1-03 遗留：模型预检后未继续迁移 |
| regression1 | SKILL.md v1 | 87% | 4 个细节只在 references 未上浮 SKILL.md |
| **regression2** | **SKILL.md 定稿** | **100%（63/63 + 2 条 MANUAL 人工核验通过）** | 定稿 |

已知等价项（判 MISMATCH 但语义正确，人工复核应放行）：s1-06 用 `export data --format sql` 替代 `migrate --sql-out` 产出 INSERT 脚本（前者无 DDL/导入步骤，后者更完整，二者均可接受）。

## 交叉验证记录

语料标注经两路独立验证：**自审**（逐条对照源码事实复核）与 **deepseek-flash 映射测试**。抓出并修正的问题：

- 标注错误：`ddl.table_filter` 实际未接线（export ddl 真实读 `export.tables.include`，见 `internal/cmd/export_ddl.go:37`）；s1-02/s9-01 缺构建口味步骤；`--require-key` 并非必填。
- 内核参考缺失：`show-query` 命令、`datetime_format` 紧凑模板语法（`internal/transfer/importer/importer.go:1206`）、`conn/test` 请求体。
- 模型真实错误（保留为回归考题）：s3-08 未识别 `--format sql`；s2-07 OBProxy DSN `#cluster` 位置写错；s1-03 预检后未续跑迁移（SKILL.md 已加「预检不是终点」）。

## 注意

- 本目录**不随技能分发**（技能本体在 `.agents/skills/owl-migrate/`），属于开发侧回归资产；是否入库自行决定。
- 历史报告中 verdict 快照可能滞后于 answers-*.json（判定脚本中途修过），以 `answers-*.json` 离线重算为准；`report-baseline-final` 与 `report-regression2` 已重生成一致。
