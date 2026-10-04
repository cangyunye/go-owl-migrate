#!/usr/bin/env python3
"""owl-migrate AI 技能语料回归评测.

用法:
  export DEEPSEEK_API_KEY=sk-xxx   # key 只走环境变量，不落盘
  python3 run_eval.py --context context-ref.md --tag baseline
  python3 run_eval.py --context ../../.agents/skills/owl-migrate/SKILL.md --tag regression

流程: 校验 key/模型 → 语料分批发给模型(仅给 --context 参考文本) → 解析 JSON 映射
     → 与 corpus.jsonl 的 expected 比对 → 生成 report-<tag>-<日期>.md + answers-<tag>.json

判定:
  MATCH    预期步骤的命令签名全部出现在模型步骤中
  PARTIAL  部分命中（如命令对但子命令/端点错）
  MISMATCH 完全没命中
  MANUAL   预期 steps 为空（负例/澄清/纯说明类），需人工读模型回答判定
"""
import argparse
import json
import os
import re
import subprocess
import sys
from datetime import date

API_TIMEOUT = 180


def http_json(url, key, payload=None):
    # 本机 Python CA 包不含代理的自签名证书，curl 可正常校验，统一走 curl
    cmd = ["curl", "-sS", "--max-time", str(API_TIMEOUT), url,
           "-H", f"Authorization: Bearer {key}",
           "-H", "Content-Type: application/json"]
    if payload is not None:
        cmd += ["-d", json.dumps(payload, ensure_ascii=False)]
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        raise RuntimeError(f"curl 失败: {r.stderr.strip()}")
    return json.loads(r.stdout)


def chat(base_url, key, model, system, user, json_mode=False):
    payload = {
        "model": model,
        "messages": [{"role": "system", "content": system},
                     {"role": "user", "content": user}],
        "temperature": 0,
        "max_tokens": 12000,
    }
    if json_mode:
        payload["response_format"] = {"type": "json_object"}
    last_err = None
    for attempt in range(3):
        try:
            resp = http_json(base_url + "/chat/completions", key, payload)
            return resp["choices"][0]["message"]["content"]
        except (RuntimeError, KeyError, json.JSONDecodeError) as e:
            last_err = e
    raise RuntimeError(f"chat 调用失败（重试 3 次）: {last_err}")


def parse_items(text):
    """从模型回答里宽容地提取 items 数组."""
    text = re.sub(r"```(?:json)?|```", "", text)
    start = text.find("{")
    if start < 0:
        start = text.find("[")
    if start < 0:
        raise ValueError(f"回答里找不到 JSON: {text[:200]}")
    decoder = json.JSONDecoder()
    obj, _ = decoder.raw_decode(text[start:])
    if isinstance(obj, dict):
        obj = obj.get("items") or obj.get("results") or next(iter(obj.values()))
    if not isinstance(obj, list):
        raise ValueError("JSON 里没有 items 数组")
    return {str(it.get("id")): it for it in obj if isinstance(it, dict)}


def sig(step):
    """步骤命令签名: CLI 取到子命令层级, API 取 METHOD+一级路径, 配置类归为 config.
    容忍前缀噪声(执行/在配置中设置/编写/curl 等)与中文后缀, 在任意位置匹配."""
    s = step.strip()
    low = s.lower()
    m = re.search(r"(?i)\b(get|post|put|delete)\s+(?:https?://\S+)?/?api/v1/([a-z_]+)", s)
    if m:
        return f"{m.group(1).upper()} /api/v1/{m.group(2)}"
    if "migration_report.json" in low:
        return "report"
    # 带点配置键 (import.batch.use_copy / export.parallel / ddl.xxx / online.sync.xxx)
    # 按已知配置段前缀识别，容忍「在 migrate.yaml 中设置 xxx」等中文表述
    if re.search(r"\b(ddl|import|export|online|metadata|source|target|general)\.[a-z_]+", low) \
            and "owl-migrate" not in low:
        return "config"
    if re.search(r"\b(owl_)?[a-z][a-z0-9_]*(\.[a-z0-9_]+)+\s*[:=]", low) and "owl-migrate" not in low:
        return "config"
    if re.match(r"(?i)^(配置|config|设置|在\s*配置)", low) and "owl-migrate" not in low:
        return "config"
    m = re.search(r"\bmake\s+build(?:/(\w+))?", low)
    if m:
        return "build"
    if re.match(r"^(人工|手动|审核)", s):
        return "manual-step"
    if re.match(r"^启动\s*(serve|web)?", s) or low.startswith("owl-migrate serve"):
        return "owl-migrate serve"
    # owl-migrate[-flavor] 二进制: 去掉口味后缀取到子命令层级
    m = re.search(r"\bowl-migrate(?:-[a-z0-9]+)?\s+((?:export|online)\s+\S+|\S+)", low)
    if m:
        sub = m.group(1).split()[0]
        a = re.match(r"[a-z0-9-]+", sub)  # 截掉粘连的中文注释
        sub = a.group(0) if a else sub
        if sub in ("export", "online"):
            rest = m.group(1).split()
            if len(rest) > 1:
                a2 = re.match(r"[a-z0-9-]+", rest[1])
                if a2:
                    return f"owl-migrate {sub} {a2.group(0)}"
        return "owl-migrate " + sub
    return low[:40]


def judge(expected, got):
    exp_sigs = [sig(s) for s in expected.get("steps", [])]
    got_sigs = [sig(s) for s in (got.get("steps") or [])] if got else []
    got_notes = (got or {}).get("notes", "")
    if not exp_sigs:
        notes = expected.get("notes", "")
        if "【澄清】" in notes:
            ok = (not got_sigs) and any(k in got_notes for k in (
                "澄清", "确认", "问清", "明确", "需要知道", "先问", "补充",
                "提供", "缺少", "缺源", "缺目标", "缺失", "待确认"))
            return "MATCH" if ok else "MISMATCH"
        if "【不支持】" in notes or "【限制】" in notes:
            ok = (not got_sigs) and any(k in got_notes for k in (
                "不支持", "无法", "限制", "没有", "无", "仅 Web", "仅 serve",
                "不在", "unsupported", "不存在", "未提供", "未接线", "未包含", "超出"))
            return "MATCH" if ok else "MISMATCH"
        return "MANUAL"
    # make build 视为前置准备: 模型没写不算失分
    exp_sigs = [s for s in exp_sigs if s != "build"]
    got_notes = (got or {}).get("notes", "")
    def hit(e):
        if e in got_sigs:
            return True
        # 人工审核步骤写在 notes 里也算
        return e == "manual-step" and any(k in got_notes for k in ("人工", "手动", "审核"))
    hits = sum(1 for e in exp_sigs if hit(e))
    if hits == len(exp_sigs):
        return "MATCH"
    return "PARTIAL" if hits else "MISMATCH"


SYSTEM_TMPL = (
    "你是数据库迁移助手。你只能依据下面的《命令参考》回答，不得使用参考之外的工具能力。\n"
    "对给每条用户请求输出映射，严格返回 JSON 对象（不要多余文字）：\n"
    '{{"items":[{{"id":"原样返回","intent":"一句话意图","steps":["每项一条可执行命令/API调用/配置修改"],'
    '"notes":"不超过80字的关键说明"}}]}}\n'
    "规则：请求超出参考范围或不被支持 → steps 留空并在 notes 说明；请求有歧义 → steps 留空并在 notes 列出要澄清的点；"
    "前置依赖（如 serve 未启动）作为第一步写进 steps。\n\n"
    "《命令参考》：\n{context}"
)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--corpus", default=os.path.join(os.path.dirname(__file__), "corpus.jsonl"))
    ap.add_argument("--context", required=True, help="给模型的参考文本（技能内核或 SKILL.md）")
    ap.add_argument("--model", default="deepseek-flash")
    ap.add_argument("--base-url", default="https://api.deepseek.com")
    ap.add_argument("--batch-size", type=int, default=15)
    ap.add_argument("--tag", required=True, help="报告标签，如 baseline / regression")
    ap.add_argument("--json-mode", action="store_true", help="开启 API response_format json_object")
    args = ap.parse_args()

    key = os.environ.get("DEEPSEEK_API_KEY")
    if not key:
        sys.exit("错误：请先 export DEEPSEEK_API_KEY")

    # 校验模型存在
    models = http_json(args.base_url + "/models", key)["data"]
    ids = [m["id"] for m in models]
    print(f"可用模型: {ids}")
    if args.model not in ids:
        sys.exit(f"错误：模型 {args.model} 不在可用列表里")

    corpus = [json.loads(line) for line in open(args.corpus, encoding="utf-8") if line.strip()]
    context = open(args.context, encoding="utf-8").read()
    system = SYSTEM_TMPL.format(context=context)

    answers, batches = {}, 0

    def run_chunk(chunk):
        nonlocal batches
        user = "请映射以下请求：\n" + json.dumps(
            [{"id": c["id"], "utterance": c["utterance"]} for c in chunk],
            ensure_ascii=False, indent=1)
        batches += 1
        print(f"批次 {batches}: {len(chunk)} 条 ...", flush=True)
        text = chat(args.base_url, key, args.model, system, user, args.json_mode)
        try:
            return parse_items(text)
        except (ValueError, json.JSONDecodeError):
            if len(chunk) == 1:
                print(f"  警告: {chunk[0]['id']} 单条仍解析失败，跳过", flush=True)
                return {}
            print(f"  回复截断/解析失败，对半拆批重试", flush=True)
            mid = len(chunk) // 2
            out = {}
            out.update(run_chunk(chunk[:mid]))
            out.update(run_chunk(chunk[mid:]))
            return out

    for i in range(0, len(corpus), args.batch_size):
        answers.update(run_chunk(corpus[i:i + args.batch_size]))

    rows, counts = [], {"MATCH": 0, "PARTIAL": 0, "MISMATCH": 0, "MANUAL": 0}
    for c in corpus:
        got = answers.get(c["id"])
        v = judge(c["expected"], got)
        counts[v] += 1
        rows.append({
            "id": c["id"], "verdict": v,
            "expected_steps": c["expected"]["steps"], "expected_notes": c["expected"]["notes"],
            "model_intent": (got or {}).get("intent", ""),
            "model_steps": (got or {}).get("steps", []),
            "model_notes": (got or {}).get("notes", ""),
        })

    out = os.path.dirname(os.path.abspath(args.corpus))
    with open(os.path.join(out, f"answers-{args.tag}.json"), "w", encoding="utf-8") as f:
        json.dump(rows, f, ensure_ascii=False, indent=1)

    total = len(rows)
    lines = [
        f"# owl-migrate 技能语料评测报告（{args.tag}）",
        f"",
        f"- 日期: {date.today().isoformat()}  模型: `{args.model}`  参考文本: `{args.context}`",
        f"- 语料: {total} 条  判定: MATCH={counts['MATCH']} PARTIAL={counts['PARTIAL']} "
        f"MISMATCH={counts['MISMATCH']} MANUAL={counts['MANUAL']}（人工复核）",
        f"- 自动通过率(非 MANUAL 中 MATCH 占比): "
        f"{counts['MATCH'] / max(1, total - counts['MANUAL']):.0%}",
        "",
        "| id | 判定 | 预期步骤 | 模型步骤 | 模型 notes |",
        "|---|---|---|---|---|",
    ]
    for r in rows:
        exp = "<br>".join(x.replace("|", "\\|") for x in r["expected_steps"]) or "（空）"
        got = "<br>".join(x.replace("|", "\\|") for x in r["model_steps"]) or "（空）"
        lines.append(f"| {r['id']} | {r['verdict']} | {exp} | {got} | "
                     f"{r['model_notes'].replace('|', '\\|')[:120]} |")
    report = os.path.join(out, f"report-{args.tag}-{date.today().isoformat()}.md")
    with open(report, "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")
    print(f"完成: {counts}  报告: {report}")


if __name__ == "__main__":
    main()
