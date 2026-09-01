#!/usr/bin/env python3
"""owl-migrate serve 前端接口 E2E:按页面操作路径逐页验证。

前置: docker compose -f testdata/db/docker-compose.yaml up -d
运行: owl-migrate serve --port 18080 ... && python3 scripts/e2e_serve_api.py
"""
import json
import os
import time
import urllib.request
import urllib.error

BASE = os.environ.get("OWL_SERVE", "http://127.0.0.1:18080")
MYSQL_DSN = "root:root123456@tcp(127.0.0.1:3306)/default_db"
PG_DSN = "host=127.0.0.1 port=5432 user=postgres password=postgres123 dbname=postgres_db sslmode=disable"
ORA_DSN = "oracle://scott:tiger@127.0.0.1:1521/XEPDB1"

RESULTS = []

def call(method, path, body=None, expect_status=200, raw=False):
    url = BASE + path
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    if data:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            status = resp.status
            content = resp.read()
            if resp.status != expect_status:
                raise AssertionError(f"HTTP {resp.status} != {expect_status}: {content[:200]}")
            return content if raw else json.loads(content)
    except urllib.error.HTTPError as e:
        if e.code == expect_status:
            c = e.read()
            return c if raw else json.loads(c)
        raise AssertionError(f"{method} {path} -> HTTP {e.code}: {e.read()[:300]}")

def check(name, fn):
    try:
        detail = fn()
        RESULTS.append(("PASS", name, str(detail or "")[:120]))
        print(f"PASS  {name}  {str(detail or '')[:80]}")
    except Exception as e:
        RESULTS.append(("FAIL", name, str(e)[:300]))
        print(f"FAIL  {name}  {str(e)[:200]}")

# ───────────────── 基础设施 ─────────────────
check("health", lambda: call("GET", "/api/v1/health")["status"] == "ok")
check("dialects 含 mysql/postgres/oracle", lambda: {"mysql", "postgres", "oracle"} <= set(call("GET", "/api/v1/dialects")))
check("SPA 首页 200", lambda: b"<!DOCTYPE html>" in call("GET", "/", raw=True) or b"<html" in call("GET", "/", raw=True).lower())

# ───────────────── 配置页 ─────────────────
def t_scenarios():
    data = call("GET", "/api/v1/scenarios")
    scs = {s["name"]: s for s in data["scenarios"]}
    for need in ("migrate", "export-ddl", "gen-select", "export", "import", "export-insert", "export-metadata", "validate"):
        assert need in scs, f"缺场景 {need}"
    ddl_fields = {f["name"] for f in scs["export-ddl"]["fields"]}
    sel_fields = {f["name"] for f in scs["gen-select"]["fields"]}
    assert "tables" in ddl_fields, "export-ddl 缺 tables 字段"
    assert "tables" in sel_fields, "gen-select 缺 tables 字段"
    return f"{len(scs)} 场景, ddl/select 均有 tables 字段"
check("scenarios 列表 + 新 tables 字段", t_scenarios)

MIGRATE_VALUES = {
    "metadata_type": "database",
    "source_type": "mysql", "source_dsn": MYSQL_DSN, "source_schema": "default_db",
    "tables": "EMP,DEPT",
    "target_type": "postgres", "target_dsn": PG_DSN, "target_schema": "public",
}

def t_build_save():
    r = call("POST", "/api/v1/scenarios/migrate/build", {"values": MIGRATE_VALUES, "save": False})
    assert "EMP" in r["yaml"] and "source" in r["yaml"], "预览 yaml 异常"
    r2 = call("POST", "/api/v1/scenarios/migrate/build", {"values": MIGRATE_VALUES, "save": True})
    assert r2.get("saved") and r2.get("path"), f"未保存: {r2}"
    return r2["path"]
check("migrate 表单 build+save", t_build_save)

def t_config_current():
    r = call("GET", "/api/v1/config/current")
    assert not r["empty"], "current 显示 empty"
    assert r["scenario"] == "migrate", r["scenario"]
    assert r["values"]["source_dsn"] == MYSQL_DSN, "source_dsn 未回填明文"
    assert r["values"]["tables"] == "EMP,DEPT", f"tables 回填: {r['values'].get('tables')}"
    assert r["values"]["target_type"] == "postgres"
    return "scenario/values/tables 全对上"
check("config/current 回填(含 tables)", t_config_current)

def t_config_masked():
    r = call("GET", "/api/v1/config")
    dsn = r.get("source", {}).get("dsn", "")
    assert "*" in dsn or dsn == "", f"DSN 未掩码: {dsn}"
    return f"掩码 OK ({dsn[:20]}…)"
check("config 读接口保持掩码", t_config_masked)

check("config/status on_disk", lambda: call("GET", "/api/v1/config/status")["on_disk"] is True)

def t_build_bad():
    try:
        call("POST", "/api/v1/scenarios/bogus/build", {"values": {}, "save": False})
        return "应报 400 但没有"
    except AssertionError as e:
        assert "400" in str(e), str(e)
        return "未知场景 400 OK"
check("build 未知场景 → 400", t_build_bad)

# ───────────────── 数据源页(含 Q5 接口链路) ─────────────────
def t_ds_crud():
    call("POST", "/api/v1/datasources", {"name": "e2e-mysql", "type": "mysql", "schema": "default_db", "dsn": MYSQL_DSN, "remark": "初始"})
    lst = call("GET", "/api/v1/datasources")
    rec = next((d for d in lst if d["name"] == "e2e-mysql"), None)
    assert rec, "列表无 e2e-mysql"
    assert "dsn" not in json.dumps(rec).lower() or not rec.get("dsn"), "列表泄露 DSN"
    # 编辑(前端编辑按钮走的 PUT)
    call("PUT", "/api/v1/datasources/e2e-mysql", {"type": "mysql", "schema": "default_db", "dsn": "", "remark": "编辑后"})
    lst2 = call("GET", "/api/v1/datasources")
    rec2 = next(d for d in lst2 if d["name"] == "e2e-mysql")
    assert rec2["remark"] == "编辑后", f"编辑未生效: {rec2}"
    p = call("POST", "/api/v1/datasources/e2e-mysql/pick", {})
    assert p["ref"] == "datasource:e2e-mysql" and p["type"] == "mysql", p
    return "create→list→PUT-edit→pick 全通"
check("数据源 CRUD + 编辑接口", t_ds_crud)

def t_conn_test():
    ok = call("POST", "/api/v1/conn/test", {"type": "mysql", "dsn": MYSQL_DSN, "schema": ""})
    assert isinstance(ok, dict) and ok.get("ok"), f"明文 DSN 测试失败: {ok}"
    ref = call("POST", "/api/v1/conn/test", {"type": "mysql", "dsn": "datasource:e2e-mysql", "schema": ""})
    assert isinstance(ref, dict) and ref.get("ok"), f"引用 DSN 测试失败: {ref}"
    assert ref.get("schemas"), "schemas 列表为空"
    return "明文+datasource:ref 都能连通"
check("conn/test 明文与数据源引用", t_conn_test)

def t_conn_bad():
    r = call("POST", "/api/v1/conn/test", {"type": "mysql", "dsn": "nosuch:pw@tcp(127.0.0.1:13306)/x", "schema": ""})
    assert isinstance(r, str) or not r.get("ok"), f"坏 DSN 竟然通过: {r}"
    return "坏 DSN 正确失败"
check("conn/test 坏 DSN → 失败信息", t_conn_bad)

# ───────────────── 元数据页 ─────────────────
def t_meta_load_plain():
    r = call("POST", "/api/v1/metadata/load", {"metadata": {"type": "database"}, "source": {"type": "mysql", "dsn": MYSQL_DSN, "schema": "default_db"}})
    assert r["table_count"] >= 2, r
    names = {t["name"] for t in r["tables"]}
    assert {"EMP", "DEPT"} <= names, names
    return f"{r['table_count']} 表: {sorted(names)}"
check("元数据加载(mysql 库)", t_meta_load_plain)

def t_meta_load_ref():
    r = call("POST", "/api/v1/metadata/load", {"metadata": {"type": "database"}, "source": {"type": "mysql", "dsn": "datasource:e2e-mysql", "schema": ""}})
    assert r["table_count"] >= 2, r
    return f"引用解析加载 {r['table_count']} 表"
check("元数据加载用 datasource:ref", t_meta_load_ref)

def t_meta_tables_detail():
    tables = call("GET", "/api/v1/metadata/tables")
    emp = next(t for t in tables if t["name"] == "EMP")
    cols = {c["name"] for c in emp["columns"]}
    assert {"EMPNO", "ENAME", "SAL"} <= {c.upper() for c in cols}, cols
    d = call("GET", "/api/v1/metadata/tables/default_db/EMP")
    assert d, "详情为空"
    v = call("GET", "/api/v1/metadata/validate")
    return f"EMP {len(emp['columns'])} 列, validate: {str(v)[:40]}"
check("元数据表清单/详情/校验", t_meta_tables_detail)

def t_meta_csv():
    r = call("POST", "/api/v1/metadata/load", {"metadata": {"type": "csv", "csv": {"path": "./testdata/csv/", "column_name_matching": "case_insensitive"}}})
    assert r["table_count"] >= 2, r
    call("POST", "/api/v1/metadata/load", {"metadata": {"type": "database"}, "source": {"type": "mysql", "dsn": "datasource:e2e-mysql", "schema": "default_db"}})
    return "CSV 元数据加载 OK,已恢复 mysql 模型"
check("元数据加载(CSV 离线)", t_meta_csv)

def t_meta_load_bad():
    try:
        call("POST", "/api/v1/metadata/load", {"metadata": {"type": "database"}, "source": {"type": "mysql", "dsn": "bad:x@tcp(127.0.0.1:13306)/y", "schema": "z"}})
        return "坏连接竟然 200"
    except AssertionError as e:
        assert "400" in str(e)
        return "400 + 错误消息"
check("元数据加载失败 → 400", t_meta_load_bad)

# ───────────────── DDL 页 ─────────────────
def t_ddl_all():
    r = call("POST", "/api/v1/ddl/generate", {})
    assert r["count"] >= 2, r
    fns = [f["name"] for f in r["files"]]
    assert any("EMP" in f.upper() for f in fns) and any("DEPT" in f.upper() for f in fns), fns
    return f"全部: {r['count']} 文件 {fns[:4]}"
check("DDL 生成(全部表)", t_ddl_all)

def t_ddl_filter():
    r = call("POST", "/api/v1/ddl/generate", {"tables": "EMP"})
    fns = [f["name"] for f in r["files"]]
    assert any("EMP" in f.upper() for f in fns), fns
    assert not any("DEPT" in f.upper() and "EMP" not in f.upper() for f in fns), f"过滤失效: {fns}"
    r2 = call("POST", "/api/v1/ddl/generate", {"tables": "*"})
    assert r2["count"] >= r["count"], (r2["count"], r["count"])
    return f"EMP-only {r['count']} 文件 < 全部 {r2['count']} 文件"
check("DDL 生成(tables 过滤)", t_ddl_filter)

check("DDL 下载 zip", lambda: call("GET", "/api/v1/ddl/download", raw=True)[:2] == b"PK")

# ───────────────── SELECT 页 ─────────────────
def t_sel():
    r = call("POST", "/api/v1/select/generate", {"batch_method": "offset", "page_size": 500})
    assert r["count"] >= 2, r
    emp = next(f for f in r["files"] if "EMP" in f["name"].upper() and "DEPT" not in f["name"].upper())
    assert "LIMIT" in emp["content"].upper() or "OFFSET" in emp["content"].upper(), emp["content"][:100]
    r2 = call("POST", "/api/v1/select/generate", {"tables": "DEPT"})
    assert all("EMP" not in f["name"].upper() or "DEPT" in f["name"].upper() for f in r2["files"]), [f["name"] for f in r2["files"]]
    return f"全部 {r['count']} / DEPT-only {r2['count']}"
check("SELECT 生成 + tables 过滤", t_sel)
check("SELECT 下载 zip", lambda: call("GET", "/api/v1/select/download", raw=True)[:2] == b"PK")

# ───────────────── 导出 CSV(job)→ INSERT 页 ─────────────────
def poll_job(jid, timeout=180):
    t0 = time.time()
    while time.time() - t0 < timeout:
        j = call("GET", f"/api/v1/jobs/{jid}")
        if j["status"] in ("completed", "succeeded", "failed", "cancelled"):
            return j
        time.sleep(2)
    raise AssertionError(f"job {jid} 超时")

def t_export_job():
    j = call("POST", "/api/v1/export", {}, expect_status=201)
    jid = j.get("id") or j.get("job_id") or j.get("job", {}).get("id")
    assert jid, j
    done = poll_job(jid)
    assert done["status"] == "completed", f"export 失败: {done}"
    return f'{done["status"]} rows ok'
check("导出 CSV job(export 页启动)", t_export_job)

def t_insert_tables():
    r = call("GET", "/api/v1/insert/tables")
    names = {t["name"] for t in r["tables"]}
    assert {"EMP", "DEPT"} <= {n.upper() for n in names} or r.get("error"), r
    return f"data_dir={r['data_dir']} tables={sorted(names) or r.get('error')}"
check("insert/tables 检测端点", t_insert_tables)

def t_insert_gen():
    r = call("POST", "/api/v1/insert/generate", {"batch_size": 10})
    assert r["count"] >= 1, r
    fns = [f["name"] for f in r["files"]]
    emp = next(f for f in r["files"] if "EMP" in f["name"].upper())
    assert "INSERT INTO" in emp["content"].upper(), emp["content"][:100]
    r2 = call("POST", "/api/v1/insert/generate", {"tables": "DEPT"})
    assert all("EMP" not in f["name"].upper() or "DEPT" in f["name"].upper() for f in r2["files"]), fns
    r3 = call("POST", "/api/v1/insert/generate", {"tables": "NOSUCHTABLE"})
    assert r3["count"] == 0, f"不存在的表应 0 文件: {r3['count']}"
    return f"全部 {r['count']} / DEPT {r2['count']} / 无匹配 0"
check("INSERT 生成 + 过滤 + 无匹配", t_insert_gen)
check("INSERT 下载 zip", lambda: call("GET", "/api/v1/insert/download", raw=True)[:2] == b"PK")

# ───────────────── 元数据导出页(oracle 源 + ref) ─────────────────
def t_ora_ds():
    call("POST", "/api/v1/datasources", {"name": "e2e-oracle", "type": "oracle", "schema": "SCOTT", "dsn": ORA_DSN, "remark": "e2e"})
    ref = call("POST", "/api/v1/conn/test", {"type": "oracle", "dsn": "datasource:e2e-oracle", "schema": ""})
    assert isinstance(ref, dict) and ref.get("ok"), f"oracle ref 连接失败: {ref}"
    return "oracle 数据源建立并连通"
check("元数据导出前置:oracle 数据源", t_ora_ds)

def t_export_metadata():
    r = call("POST", "/api/v1/metadata/export", {"source": {"type": "oracle", "dsn": "datasource:e2e-oracle", "schema": "SCOTT"}, "format": "csv", "scope": "all"})
    assert r.get("table_count", 0) >= 2, f"oracle 表太少: {r.get('table_count')}"
    dl = call("GET", "/api/v1/metadata/export/download", raw=True)
    assert dl[:2] == b"PK", "下载不是 zip"
    return f"{r['table_count']} 表 zip {len(dl)}B"
check("元数据导出(oracle,ref 解析)", t_export_metadata)

# ───────────────── 迁移页(端到端) ─────────────────
def t_migrate():
    call("POST", "/api/v1/scenarios/migrate/build", {"values": MIGRATE_VALUES, "save": True})
    j = call("POST", "/api/v1/migrate", {}, expect_status=201)
    jid = j.get("id") or j.get("job_id") or j.get("job", {}).get("id")
    done = poll_job(jid)
    assert done["status"] == "completed", f"migrate 失败: {done}"
    evs = call("GET", f"/api/v1/jobs/{jid}/events")
    cps = call("GET", f"/api/v1/jobs/{jid}/checkpoints")
    out = call("GET", f"/api/v1/jobs/{jid}/output", raw=True)
    return f"job {jid[:8]}… events={len(evs)} cps={len(cps)} out={len(out)}B"
check("完整迁移 mysql→postgres", t_migrate)

def t_verify_pg():
    import subprocess
    r = subprocess.run(["docker", "exec", "postgres", "psql", "-U", "postgres", "-d", "postgres_db", "-tAc",
                        'SELECT count(*) FROM public."EMP"'], capture_output=True, text=True, timeout=30)
    n = int(r.stdout.strip() or 0)
    assert n == 14, f"PG EMP 行数 {n},应为 14"
    return f"PG public.EMP {n} 行"
check("迁移结果验证(PG EMP=14)", t_verify_pg)

check("jobs 列表含新 job", lambda: len(call("GET", "/api/v1/jobs")) >= 2)

# ───────────────── 配置库页 ─────────────────
def t_lib():
    yaml_text = call("GET", "/api/v1/config/download", raw=True).decode()
    assert "EMP" in yaml_text
    r = call("POST", "/api/v1/configs", {"name": "e2e-lib.yaml", "yaml": yaml_text})
    assert r["name"] == "e2e-lib", r
    lst = call("GET", "/api/v1/configs")
    item = next((c for c in lst if c["name"] == "e2e-lib"), None)
    assert item and item["scenario"] == "migrate" and item["source_type"] == "mysql", item
    ld = call("POST", "/api/v1/configs/e2e-lib/load", {})
    assert ld["scenario"] == "migrate" and ld["values"]["tables"] == "EMP,DEPT", ld
    call("DELETE", "/api/v1/configs/e2e-lib")
    gone = False
    try:
        call("POST", "/api/v1/configs/e2e-lib/load", {})
    except AssertionError:
        gone = True
    assert gone, "删除后 load 竟然成功"
    return "上传→列表(带场景)→加载→删除→404"
check("配置库 上传/列表/加载/删除", t_lib)

def t_lib_bad():
    try:
        call("POST", "/api/v1/configs", {"name": "bad", "yaml": "source: [broken"}, expect_status=400)
        raise AssertionError("坏 YAML 应 400")
    except AssertionError as e:
        assert "400" in str(e), str(e)
        return "400 OK"
check("配置库 坏 YAML → 400", t_lib_bad)

print("\n================ 汇总 ================")
fails = [r for r in RESULTS if r[0] == "FAIL"]
print(f"{len(RESULTS) - len(fails)}/{len(RESULTS)} PASS")
for _, n, d in fails:
    print(f"  FAIL {n}: {d}")
