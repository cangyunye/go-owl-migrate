# evals/ai-router — AI 对话路由评测

自然语言 → owl-migrate 命令路由 + migrate.yaml 配置生成的模型评测。设计方案见
`docs/plans/2026-09-28-ai-chat-router-design.md`，最新结果见
`report-ai-router-2026-09-29.md`。

## 文件

| 文件 | 说明 |
|---|---|
| `corpus.jsonl` | 62 条用户语料：七大场景全子命令变体 + out-of-scope（8）+ 歧义（5）+ 多轮上下文（4），每条带 gold 路由 |
| `router_system.md` | 路由系统提示词（路由词表 + 硬约束 + 输出 JSON schema） |
| `run_router_eval.py` | harness：`--stage route`（意图路由）/ `--stage config`（配置生成 + CLI 实连验证） |
| `compare_answers.py` | 多模型 × gold 三方对比报告 |
| `answers-*.json` | 各模型作答（flash / pro / glm） |
| `config-<model>/` | 阶段二产物：生成的 migrate.yaml + 验证日志 + results.json |

## 用法

```bash
# API key 不入仓库：环境变量或 key 文件（默认 ~/.owl/ai/deepseek.key）
export DEEPSEEK_API_KEY=sk-...

python3 run_router_eval.py --stage route --model deepseek-flash
python3 run_router_eval.py --stage route --model deepseek-v4-pro
python3 compare_answers.py answers-deepseek-flash.json answers-deepseek-v4-pro.json [answers-glm.json]

# 阶段二：需要本机容器库（mysql:3306 root/root123456 owl_demo、pg:5432 postgres/postgres123、
# oracle:1521 appuser/App123! XEPDB1）和已构建二进制 build/<goos>-<goarch>/owl-migrate
python3 run_router_eval.py --stage config --model deepseek-flash
python3 run_router_eval.py --stage config --model deepseek-flash --reuse   # 复用 YAML 只重跑验证
```

## 阶段二机制（评测验证过的架构）

1. LLM 按字段参考生成完整 YAML，**密码一律写占位符** `__PWD_mysql__` / `__PWD_pg__` / `__PWD_oracle__`（凭据不过 LLM 输出层）；
2. harness 注入真实凭据（生产实现 = 配置层确定性代码 / 数据源引用解析）；
3. CLI 实连验证；失败把报错回喂 LLM 修复（最多 3 轮），只许改配置不许改意图。

## 模型与参数

- 首选 `deepseek-flash`（V4.1-Flash，1M 上下文，effort 支持 low/high/max）：路由用 `effort=low` + `max_tokens=32768`（推理模型思考 token 计入 max_tokens，默认值会截断）。
- `deepseek-v4-pro` 作为对照；更贵且倾向过度澄清，见报告。
