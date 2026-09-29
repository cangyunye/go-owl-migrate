#!/usr/bin/env python3
"""两轮会话 e2e：/api/v1/ai/plan 实连 DeepSeek。

场景（对应设计稿 §4 会话设计）：
  轮 1  「导出 mysql owl_demo 库 users 表为 csv」         → 新会话 + 生成配置
  轮 2  同 session_id：「再导一份 xlsx 格式的」            → 同意图沿用会话，格式槽位被覆盖

验证点：continuity.mode、槽位继承与覆盖、yaml 结构校验通过（服务端
config.Load）、凭据占位符已注入且响应脱敏。

用法：python3 run_serve_plan_eval.py [--keep] [--base http://...]
"""
import argparse
import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

HERE = Path(__file__).parent
REPO = HERE.parent.parent
BIN = REPO / "build" / f"{os.uname().sysname.lower()}-{os.uname().machine.lower()}" / "owl-migrate"
CREDS = {"__PWD_mysql__": "root123456"}


def load_key(args):
    key = os.environ.get("DEEPSEEK_API_KEY", "")
    if not key and args.key_file:
        key = Path(args.key_file).expanduser().read_text().strip()
    if not key:
        sys.exit("缺少 API key：设 DEEPSEEK_API_KEY 或 --key-file")
    return key


def post(base, path, payload, timeout=300):
    req = urllib.request.Request(base + path, data=json.dumps(payload).encode(),
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read())


def start_serve(args, key):
    tmp = tempfile.mkdtemp(prefix="owl-ai-plan-")
    env = dict(os.environ, DEEPSEEK_API_KEY=key)
    proc = subprocess.Popen(
        [str(BIN), "serve", "--port", str(args.port), "--host", "127.0.0.1",
         "--db", f"{tmp}/jobs.db", "--temp-dir", f"{tmp}/temp",
         "--config-dir", f"{tmp}/configs", "--config-out", f"{tmp}/active.yaml"],
        env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    base = f"http://127.0.0.1:{args.port}"
    for _ in range(60):
        try:
            urllib.request.urlopen(base + "/api/v1/health", timeout=2)
            return proc, base, tmp
        except Exception:
            time.sleep(0.5)
    proc.terminate()
    sys.exit("serve 未能在 30s 内就绪")


def check(name, cond, detail=""):
    print(f"  {'✓' if cond else '✗'} {name}" + (f" —— {detail}" if detail and not cond else ""))
    return cond


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=18235)
    ap.add_argument("--base", default="")
    ap.add_argument("--keep", action="store_true")
    ap.add_argument("--key-file", default=str(Path.home() / ".owl/ai/deepseek.key"))
    args = ap.parse_args()

    proc = tmp = None
    if args.base:
        base = args.base.rstrip("/")
    else:
        proc, base, tmp = start_serve(args, load_key(args))
        print(f"serve ready at {base}")

    passed = 0
    try:
        # ── 轮 1 ──
        r1 = post(base, "/api/v1/ai/plan", {
            "utterance": "导出 mysql 数据库 owl_demo 里 users 表的数据为 csv，host 127.0.0.1 端口 3306 用户 root",
            "credentials": CREDS,
        })
        if r1["route"] != "export-data" or not r1.get("yaml"):
            # LLM 采样波动：本轮被判成 clarify/其他意图。如实报告后退出，
            # 重跑通常即恢复（模型非确定性，非端点缺陷）。
            print(f"轮1 被判为 {r1['route']}（reason={r1.get('result', {}).get('reason', '')}），未生成配置；重跑本脚本再试")
            sys.exit(2)
        sid = r1["session_id"]
        y1 = r1.get("yaml") or ""
        print(f"轮1: route={r1['route']} mode={r1['continuity']['mode']} session={sid[:14]}… "
              f"repairs={r1['repair_rounds']} warnings={r1.get('warnings')}")
        passed += check("轮1 route=export-data", r1["route"] == "export-data", r1["route"])
        passed += check("轮1 新会话", r1["continuity"]["mode"] == "new")
        passed += check("轮1 yaml 含 source.type mysql", "type: mysql" in y1)
        passed += check("轮1 yaml 凭据已注入且脱敏",
                        "root123456" not in y1 and "__PWD_" not in y1 and "******" in y1)
        passed += check("轮1 无未填占位符", not r1.get("warnings"))
        passed += check("轮1 会话进入 confirming", r1["session"]["stage"] == "confirming")

        # ── 轮 2：同会话续问 ──
        r2 = post(base, "/api/v1/ai/plan", {
            "utterance": "再导一份 xlsx 格式的",
            "session_id": sid,
            "credentials": CREDS,
        })
        y2 = r2.get("yaml") or ""
        print(f"轮2: route={r2['route']} mode={r2['continuity']['mode']} repairs={r2['repair_rounds']}")
        passed += check("轮2 同意图沿用会话", r2["continuity"]["mode"] == "continued",
                        str(r2["continuity"]))
        passed += check("轮2 session_id 不变", r2["session_id"] == sid)
        passed += check("轮2 format 槽位更新为 xlsx", r2["session"]["slots"].get("format") == "xlsx",
                        str(r2["session"]["slots"]))
        passed += check("轮2 yaml format: xlsx", "format: xlsx" in y2, y2[:120])

        # ── 轮 3：意图切换 → 自动开新一轮 ──
        r3 = post(base, "/api/v1/ai/plan", {
            "utterance": "把刚才那张表迁移到 postgres，host 127.0.0.1 port 5432 user postgres",
            "session_id": sid,
            "credentials": {**CREDS, "__PWD_pg__": "postgres123"},
        })
        print(f"轮3: route={r3['route']} mode={r3['continuity']['mode']} "
              f"new_session={r3['session_id'][:14]}… reason={r3['continuity'].get('reason','')[:40]}")
        passed += check("轮3 意图切换自动开新一轮", r3["continuity"]["mode"] == "new_round",
                        str(r3["continuity"]))
        passed += check("轮3 旧会话被引用", r3["continuity"].get("from_session") == sid)
        passed += check("轮3 继承轮1事实（owl_demo）",
                        "owl_demo" in (r3.get("yaml") or "") or
                        r3["session"]["slots"].get("source_dsn", "").find("owl_demo") >= 0)

        total = 13  # check() 调用数
        print(f"\ne2e 通过 {passed}/{total}")
        out = HERE / "answers-serve-plan-e2e.json"
        out.write_text(json.dumps({"turns": [r1, r2, r3]}, ensure_ascii=False, indent=1, default=str))
        print(f"saved {out}")
        sys.exit(0 if passed == total else 1)
    finally:
        if proc and not args.keep:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()


if __name__ == "__main__":
    main()
