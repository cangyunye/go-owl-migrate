#!/usr/bin/env python3
"""owl-migrate AI 路由评测 harness（研究分支 feat/ai-chat-router-research）。

用法（API key 从环境变量 DEEPSEEK_API_KEY 或 --key-file 读取，绝不入仓库）：

  # 阶段一：意图路由，两个 API 模型对同一语料作答
  python3 run_router_eval.py --stage route --model deepseek-flash
  python3 run_router_eval.py --stage route --model deepseek-v4-pro

  # 阶段二：配置生成，模型产出 migrate.yaml 后用 owl-migrate 实连容器库验证
  python3 run_router_eval.py --stage config --model deepseek-flash

产物写入本目录：answers-<model>.json / config-<model>/（yaml+验证日志）。
"""
import argparse
import json
import os
import re
import subprocess
import sys
import time
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

HERE = Path(__file__).parent
API_BASE = "https://api.deepseek.com"
BIN = HERE.parent.parent / "build" / f"{os.uname().sysname.lower()}-{os.uname().machine.lower()}" / "owl-migrate"


def load_key(args):
    key = os.environ.get("DEEPSEEK_API_KEY", "")
    if not key and args.key_file:
        key = Path(args.key_file).expanduser().read_text().strip()
    if not key:
        sys.exit("缺少 API key：设 DEEPSEEK_API_KEY 或 --key-file")
    return key


def chat(key, model, messages, json_mode=True, timeout=180, effort="low"):
    body = {"model": model, "messages": messages, "max_tokens": 32768, "effort": effort}
    if json_mode:
        body["response_format"] = {"type": "json_object"}
    # 走 curl 而非 urllib：本机证书链含自签 CA，Python ssl 校验不过而 curl 走系统信任。
    last = None
    for attempt in range(3):
        try:
            import subprocess
            r = subprocess.run(
                ["curl", "-sS", "--max-time", str(timeout), API_BASE + "/chat/completions",
                 "-H", "Content-Type: application/json",
                 "-H", f"Authorization: Bearer {key}",
                 "-d", json.dumps(body)],
                capture_output=True, text=True, timeout=timeout + 10)
            data = json.loads(r.stdout)
            if "error" in data:
                raise RuntimeError(str(data["error"])[:300])
            return data["choices"][0]["message"]["content"], data.get("usage", {})
        except Exception as e:  # 网络抖动重试
            last = e
            time.sleep(2 * (attempt + 1))
    raise RuntimeError(f"API 调用失败（3 次）：{last}")


def extract_json(text):
    text = re.sub(r"^```(?:json)?|```$", "", text.strip(), flags=re.M).strip()
    start, end = text.find("{"), text.rfind("}")
    if start < 0 or end < 0:
        raise ValueError(f"无 JSON：{text[:200]}")
    return json.loads(text[start : end + 1])


def load_corpus():
    items = []
    for line in (HERE / "corpus.jsonl").read_text().splitlines():
        if line.strip():
            items.append(json.loads(line))
    return items


# ── 阶段一：意图路由 ────────────────────────────────────────────
def stage_route(args, key):
    corpus = load_corpus()
    system = (HERE / "router_system.md").read_text()
    answers, usage_total = {}, {"prompt_tokens": 0, "completion_tokens": 0}

    def one(item):
        user = ""
        if item.get("context"):
            user += "【对话上文】\n" + "\n".join(f"- {t}" for t in item["context"]) + "\n\n"
        user += f"【用户最新一句话】\n{item['utterance']}"
        raw, usage = chat(key, args.model, [
            {"role": "system", "content": system},
            {"role": "user", "content": user},
        ])
        ans = extract_json(raw)
        for k in ("prompt_tokens", "completion_tokens"):
            usage_total[k] += usage.get(k, 0)
        return item["id"], ans

    with ThreadPoolExecutor(max_workers=4) as pool:
        for item_id, ans in pool.map(one, corpus):
            answers[item_id] = ans
            print(f"  {item_id}: {ans.get('route')}/{ans.get('sub', '')}")

    out = HERE / f"answers-{args.model}.json"
    out.write_text(json.dumps(
        {"model": args.model, "stage": "route", "usage": usage_total, "answers": answers},
        ensure_ascii=False, indent=1))
    print(f"saved {out}  (usage: {usage_total})")


# ── 阶段二：配置生成 + 实连验证 ─────────────────────────────────
STAGE2 = [
    {"id": "c1", "request": "导出 mysql 数据库 owl_demo 里 users 表的数据为 csv 格式。连接信息：host 127.0.0.1 端口 3306 用户 root 密码 root123456。输出目录用默认。",
     "asserts": ["source.type == mysql", "source.dsn 含 tcp(127.0.0.1:3306)/owl_demo", "metadata.type == database"],
     "validate": "{bin} export data -c {yaml} --tables owl_demo.users --format csv -o {dir}out/",
     "verify": "test -f {dir}out/owl_demo.users.csv && grep -c ',' {dir}out/owl_demo.users.csv"},
    {"id": "c2", "request": "导出 postgres 里 public schema 的 owl_users 表为 insert sql（目标方言 postgres）。连接：host=127.0.0.1 port=5432 user=postgres dbname=postgres sslmode=disable，密码用占位符 __PWD_pg__。",
     "asserts": ["source.type == postgres", "source.schema == public"],
     "validate": "{bin} export data -c {yaml} --tables public.owl_users --format sql -o {dir}out/",
     "verify": "ls {dir}out/*.insert.sql 2>/dev/null | grep -q ."},
    {"id": "c3", "request": "从 oracle 抽取 appuser 用户的元数据为 csv。连接串：oracle://appuser:App123!@127.0.0.1:1521/XEPDB1（注意密码含特殊字符按 url 语法转义）。",
     "asserts": ["source.type == oracle", "source.dsn 含 App123%21", "source.schema == APPUSER"],
     "validate": "{bin} export-metadata -c {yaml} --format csv --scope schema:APPUSER -o {dir}meta/",
     "verify": "test -f {dir}meta/tables.csv"},
    {"id": "c4", "request": "把 mysql 的 owl_demo.users 表迁移到 postgres（host=127.0.0.1 port=5432 user=postgres dbname=postgres sslmode=disable，密码用占位符 __PWD_pg__），schema 映射 owl_demo → public。",
     "asserts": ["source.type == mysql", "target.type == postgres", "ddl.schema_mapping 含 owl_demo: public"],
     "validate": "{bin} migrate -c {yaml} --tables owl_demo.users --temp-dir {dir}temp/ -r {dir}report.json",
     "verify": "psql 不适用——用 {pgcheck}"},
    {"id": "c5", "request": "给 oracle 的 APPUSER.OWL_USERS 生成分页 SELECT（cursor 方式，每批 100 行）。连接：oracle://appuser:__PWD_oracle__@127.0.0.1:1521/XEPDB1，schema APPUSER。",
     "asserts": ["source.type == oracle", "source.dsn 含 XEPDB1"],
     "validate": "{bin} gen-select -c {yaml} --batch-method cursor -n 100 -o {dir}sel/",
     "verify": "ls {dir}sel/*.sql 2>/dev/null | grep -q ."},
    {"id": "c6", "request": "生成导入配置：把 ./output/data/ 下的 csv 导入 mysql（host 127.0.0.1 端口 3306 用户 root 密码 root123456 库名 owl_demo），导入前清空目标表，csv 是 GBK 编码。",
     "asserts": ["target.type == mysql", "import.target.truncate_before == true", "import.data_transforms.source_encoding == GBK", "import.source_dir 含 output/data"],
     "validate": None, "verify": None},
    {"id": "c7", "request": "生成配置：源是达梦 dm（SYSDBA/SYSDBA001@10.3.0.8:5236，走 JDBC agent 通道，jar 目录 ./jars），仅抽元数据场景，schema SYSDBA。",
     "asserts": ["source.type == dm", "source.channel == agent", "agent.jars_dir == ./jars 或 source.agent.jars_dir == ./jars"],
     "validate": None, "verify": None},
]


CREDS = {"__PWD_mysql__": "root123456", "__PWD_oracle__": "App123%21", "__PWD_pg__": "postgres123"}


def inject_creds(yaml_text):
    for k, v in CREDS.items():
        yaml_text = yaml_text.replace(k, v)
    return yaml_text


def stage_config(args, key):
    system = """你是 owl-migrate 的配置生成器。根据用户请求输出一份**完整的** migrate.yaml，只输出 YAML 本体，不要 markdown 代码块，不要解释。

## 字段参考（只允许这些字段名与值域，不确定就省略，禁止编造）

metadata:                                  # 在线连库场景必填
  type: database                           # 合法值仅 database|csv|xlsx
source:                                    # 源库
  type: oracle|mysql|postgres|...          # 内置方言
  dsn: ...                                 # 密码一律写占位符 __PWD_mysql__ / __PWD_pg__ / __PWD_oracle__，
                                           # 禁止猜测真实密码，禁止写 password 等字面占位词；免密则完全省略密码字段
  schema: ...
  channel: native|agent|auto               # 缺省 native；dm/kingbase/timesten 必须 agent
target:                                    # 目标库（字段同 source）
ddl:
  schema_mapping:                          # 子键形式
    owl_demo: public
export:
  format: csv|sql|xlsx
  parallel: {enabled: true, max_workers: 4}
import:
  source_dir: ./output/data/
  target: {truncate_before: true|false, disable_constraints: ...}
  batch: {error_policy: stop|skip_row|log_only, use_copy: true|false}
  data_transforms: {source_encoding: GBK, datetime_format: yyyyMMddHHmmss|yyyyMMdd, null_if: [...], trim_strings: bool}
agent:                                     # agent 通道全局段
  jars_dir: ""                             # 目录路径
  agent_jar: ""
  java_home: ""

## DSN 语法

- mysql 系: user:pass@tcp(host:port)/db
- oracle 系: oracle://user:pass@host:port/service （密码含 @ : / # ? ! 等需百分号转义）
- pg 系: host=... port=... user=... password=... dbname=... sslmode=disable
- dm（达梦）: dm://user:pass@host:port
- OceanBase oracle 租户走 OBProxy: oceanbase-oracle://user@tenant#cluster:pass@host:2883/db

导出为 insert sql 等涉及目标方言的场景必须提供 ddl.target_dialect（17 个内置方言之一）。
金仓/达梦/TimesTen 只能作源（channel: agent）。"""
    outdir = HERE / f"config-{args.model}"
    outdir.mkdir(exist_ok=True)
    results = {}
    for item in STAGE2:
        cid = item["id"]
        raw, usage = chat(key, args.model, [
            {"role": "system", "content": system},
            {"role": "user", "content": item["request"]},
        ], json_mode=False, effort="high")
        yaml_text = re.sub(r"^```yaml\s*|```\s*$", "", raw.strip(), flags=re.M).strip()
        d = outdir / cid
        d.mkdir(exist_ok=True)
        if args.reuse and (d / "migrate.yaml").exists():
            yaml_text = (d / "migrate.yaml").read_text()
        yaml_text = inject_creds(yaml_text)
        (d / "migrate.yaml").write_text(yaml_text)
        entry = {"yaml_ok": True, "asserts": [], "validate": None, "verify": None}

        # 结构断言（grep 级，避免 PyYAML 依赖）
        for a in item["asserts"]:
            ok = any(_assert_yaml(yaml_text, *parse_assert(x)) for x in a.split(" 或 "))
            entry["asserts"].append({"assert": a, "pass": ok})

        # CLI 实连验证
        if item["validate"]:
            cmd = item["validate"].format(bin=BIN, yaml=d / "migrate.yaml", dir=str(d) + "/")
            verify_cmd = item["verify"].replace("{dir}", str(d) + "/") if item["verify"] else None
            if cid == "c4":  # 迁移后行数用容器内 psql 验证
                verify_cmd = ("docker exec postgres psql -U postgres -tAc "
                              '"select count(*) from public.users" | grep -q 5')
            def run_cli():
                return subprocess.run(cmd, shell=True, capture_output=True, text=True,
                                      timeout=600, cwd=str(HERE.parent.parent))
            r = run_cli()
            rounds = 0
            while r.returncode != 0 and rounds < 3:  # 失败 → 报错回喂修复，最多 3 轮
                rounds += 1
                fix_raw, _ = chat(key, args.model, [
                    {"role": "system", "content": system},
                    {"role": "user", "content": item["request"]},
                    {"role": "assistant", "content": yaml_text},
                    {"role": "user", "content": "这份 YAML 执行报错如下，修正后重新输出完整 YAML（只输出 YAML 本体）：\n"
                                               + (r.stdout + r.stderr)[-1500:]},
                ], json_mode=False, effort="high")
                yaml_text = inject_creds(
                    re.sub(r"^```yaml\s*|```\s*$", "", fix_raw.strip(), flags=re.M).strip())
                (d / "migrate.yaml").write_text(yaml_text)
                r = run_cli()
                entry["repaired"] = True
            entry["validate"] = {"cmd": cmd, "rc": r.returncode,
                                 "tail": (r.stdout + r.stderr)[-800:]}
            if r.returncode == 0 and verify_cmd:
                rv = subprocess.run(verify_cmd, shell=True, capture_output=True, text=True, timeout=120)
                entry["verify"] = {"cmd": verify_cmd, "rc": rv.returncode, "tail": (rv.stdout + rv.stderr)[-300:]}
        results[cid] = entry
        marks = "".join("+" if a["pass"] else "-" for a in entry["asserts"])
        vrc = entry["validate"]["rc"] if entry["validate"] else "n/a"
        print(f"  {cid}: asserts[{marks}] validate_rc={vrc}")

    (HERE / f"config-{args.model}" / "results.json").write_text(
        json.dumps({"model": args.model, "results": results}, ensure_ascii=False, indent=1))
    print(f"saved {outdir}/results.json")


def parse_assert(a):
    """'path == v' → (path, '==', v)；'path 含 v' → (path, 'contains', v)。"""
    if " == " in a:
        k, v = a.split(" == ", 1)
        return k.strip(), "==", v.strip()
    if " 含 " in a:
        k, v = a.split(" 含 ", 1)
        return k.strip(), "contains", v.strip()
    return a.strip(), "exists", ""


def _assert_yaml(text, key_path, op, expected):
    """极简 YAML 语义断言：沿缩进建 key 路径，按算子判定（宽松，研究用）。"""
    stack = []  # (indent, key)
    rows = []
    for ln in text.splitlines():
        if not ln.strip() or ln.strip().startswith("#"):
            continue
        m = re.match(r"^(\s*)([\w.$-]+)\s*:\s*(.*)$", ln)
        if not m:
            continue
        indent, k, v = len(m.group(1)), m.group(2), m.group(3).strip()
        while stack and stack[-1][0] >= indent:
            stack.pop()
        stack.append((indent, k))
        rows.append((".".join(s[1] for s in stack), v))
    hit = [(path, v) for path, v in rows if path == key_path or path.endswith("." + key_path)]
    if op == "exists":
        return bool(hit)
    if op == "==":
        return any(expected.lower().strip('"\'') in v.lower() for _, v in hit)
    # contains：值内含、或是映射的子键、或「子键: 值」复合（schema_mapping 含 owl_demo: public）
    low = expected.lower()
    for path, v in hit:
        if low in v.lower():
            return True
    prefix = key_path + "."
    if ":" in expected:
        ck, cv = (x.strip() for x in expected.split(":", 1))
        return any(p.lower() == (key_path + "." + ck).lower() and cv.lower() in v.lower()
                   for p, v in rows)
    return any(p.lower() == low for p, _ in rows)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--stage", choices=["route", "config"], required=True)
    ap.add_argument("--model", default="deepseek-flash")
    ap.add_argument("--key-file", default=str(Path.home() / ".owl/ai/deepseek.key"))
    ap.add_argument("--reuse", action="store_true", help="复用已生成的 YAML，只重跑验证")
    args = ap.parse_args()
    key = load_key(args)
    (stage_route if args.stage == "route" else stage_config)(args, key)


if __name__ == "__main__":
    main()
