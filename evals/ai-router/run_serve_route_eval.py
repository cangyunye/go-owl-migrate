#!/usr/bin/env python3
"""语料 e2e 回归：起 serve → POST /api/v1/ai/route 跑完整语料 → 对比 gold。

用法：
  python3 run_serve_route_eval.py                     # 自动起/停 serve，实连 DeepSeek
  python3 run_serve_route_eval.py --base http://127.0.0.1:8080   # 复用已运行的 serve
  python3 run_serve_route_eval.py --keep              # 跑完不关 serve（配合 --base 调试）

API key 读取顺序同 CLI：DEEPSEEK_API_KEY 环境变量 → --key-file（默认 ~/.owl/ai/deepseek.key）。
"""
import argparse
import json
import os
import signal
import subprocess
import sys
import tempfile
import time
import urllib.request
from collections import defaultdict
from pathlib import Path

HERE = Path(__file__).parent
REPO = HERE.parent.parent
BIN = REPO / "build" / f"{os.uname().sysname.lower()}-{os.uname().machine.lower()}" / "owl-migrate"


def load_key(args):
    key = os.environ.get("DEEPSEEK_API_KEY", "")
    if not key and args.key_file:
        key = Path(args.key_file).expanduser().read_text().strip()
    if not key:
        sys.exit("缺少 API key：设 DEEPSEEK_API_KEY 或 --key-file")
    return key


def post(base, path, payload, timeout=180):
    req = urllib.request.Request(base + path, data=json.dumps(payload).encode(),
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read())


def start_serve(args, key):
    tmp = tempfile.mkdtemp(prefix="owl-ai-eval-")
    env = dict(os.environ, DEEPSEEK_API_KEY=key)
    proc = subprocess.Popen(
        [str(BIN), "serve", "--port", str(args.port), "--host", "127.0.0.1",
         "--db", f"{tmp}/jobs.db", "--temp-dir", f"{tmp}/temp", "--config-dir", f"{tmp}/configs"],
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


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=18234)
    ap.add_argument("--base", default="", help="复用已运行的 serve（如 http://127.0.0.1:8080）")
    ap.add_argument("--keep", action="store_true")
    ap.add_argument("--key-file", default=str(Path.home() / ".owl/ai/deepseek.key"))
    args = ap.parse_args()

    proc = tmp = None
    if args.base:
        base = args.base.rstrip("/")
    else:
        proc, base, tmp = start_serve(args, load_key(args))
        print(f"serve ready at {base} (pid {proc.pid})")

    try:
        status = json.loads(urllib.request.urlopen(base + "/api/v1/ai/status", timeout=10).read())
        print(f"ai/status: enabled={status['enabled']} model={status['model']} effort={status['effort']}")
        if not status["enabled"]:
            sys.exit("AI 未启用（key 缺失）")

        gold = {}
        results = {}
        per_cat = defaultdict(lambda: [0, 0])
        hit_r = hit_s = 0
        for line in (HERE / "corpus.jsonl").read_text().splitlines():
            if not line.strip():
                continue
            item = json.loads(line)
            gold[item["id"]] = item
            payload = {"utterance": item["utterance"]}
            if item.get("context"):
                payload["context"] = item["context"]
            try:
                resp = post(base, "/api/v1/ai/route", payload)
                r = resp["result"]
                results[item["id"]] = r
            except Exception as e:
                results[item["id"]] = {"route": "ERROR", "sub": "", "error": str(e)[:120]}
            m = results[item["id"]]
            g = item["expected"]
            ok_r = m.get("route") == g["route"]
            ok_s = ok_r and (m.get("sub") or "") == (g.get("sub") or "")
            hit_r += ok_r
            hit_s += ok_s
            c = item["category"]
            per_cat[c][0] += ok_r
            per_cat[c][1] += 1
            mark = "✓" if ok_s else ("~" if ok_r else "✗")
            print(f"  {mark} {item['id']}: {m.get('route')}/{m.get('sub', '') or '∅'}")

        print(f"\nroute 命中 {hit_r}/{len(gold)}   route+sub 全对 {hit_s}/{len(gold)}")
        for c in sorted(per_cat):
            h, n = per_cat[c]
            print(f"  {c:16s} {h}/{n}")
        out = HERE / "answers-serve-endpoint.json"
        out.write_text(json.dumps({"model": "serve:/ai/route", "stage": "route", "answers": results},
                                  ensure_ascii=False, indent=1))
        print(f"saved {out}")
    finally:
        if proc and not args.keep:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
        print(f"serve {'kept running' if args.keep and proc else 'stopped'}; tmp={tmp}")


if __name__ == "__main__":
    main()
