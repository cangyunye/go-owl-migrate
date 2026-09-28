#!/usr/bin/env python3
"""对比多个 answers-*.json 与语料 gold 的路由一致性。

用法：python3 compare_answers.py answers-deepseek-flash.json answers-deepseek-v4-pro.json [answers-glm.json]
"""
import json
import sys
from collections import defaultdict
from pathlib import Path

HERE = Path(__file__).parent


def load():
    gold = {}
    for line in (HERE / "corpus.jsonl").read_text().splitlines():
        if line.strip():
            it = json.loads(line)
            gold[it["id"]] = it
    models = {}
    for p in sys.argv[1:]:
        models[p] = json.loads(Path(p).read_text())["answers"]
    return gold, models


def norm(a):
    return (a.get("route", "?"), a.get("sub", "") or "")


def main():
    gold, models = load()
    names = list(models)
    ids = sorted(gold)

    # 每个模型 vs gold
    for name in names:
        per_cat = defaultdict(lambda: [0, 0])
        exact_route = exact_sub = 0
        misses = []
        for i in ids:
            g = gold[i]["expected"]
            m = models[name].get(i, {})
            hit_r = m.get("route") == g["route"]
            hit_s = hit_r and norm(m)[1] == (g.get("sub", "") or "")
            exact_route += hit_r
            exact_sub += hit_s
            c = gold[i]["category"]
            per_cat[c][0] += hit_r
            per_cat[c][1] += 1
            if not hit_r:
                misses.append((i, gold[i]["utterance"][:38], g["route"], m.get("route")))
        print(f"\n== {name} ==")
        print(f"route 命中 {exact_route}/{len(ids)}   route+sub 全对 {exact_sub}/{len(ids)}")
        for c in sorted(per_cat):
            h, n = per_cat[c]
            print(f"  {c:16s} {h}/{n}")
        if misses:
            print("  route 误判：")
            for i, u, ge, gm in misses:
                print(f"   {i}  {u}…  gold={ge} got={gm}")

    # 模型间一致率
    print("\n== 模型间一致率（route）==")
    for x in range(len(names)):
        for y in range(x + 1, len(names)):
            agree = sum(1 for i in ids
                        if models[names[x]].get(i, {}).get("route")
                        == models[names[y]].get(i, {}).get("route"))
            print(f"{names[x]}  vs  {names[y]}: {agree}/{len(ids)}")


if __name__ == "__main__":
    main()
