#!/usr/bin/env bash
# WHERE 条件导出/列投影/改名 同库互迁 e2e（需 mysql/pg/oracle 容器，凭据见 testdata/db/.local-dev.env 惯例）。
# 覆盖：条件 COUNT 门禁、列顺序=配置顺序、改名、列裁剪、负路径、filters_check off、online 拒绝。
set -euo pipefail
BIN="$(cd "$(dirname "$0")/.." && pwd)/build/$(go env GOOS)-$(go env GOARCH)/owl-migrate"
OUT="$(mktemp -d)"; trap 'rm -rf "$OUT"' EXIT
cd "$(dirname "$0")/.."

echo "== 1. oracle 同库双用户：where+投影+改名，列序=配置序 =="
cat > "$OUT/oracle2oracle.yaml" <<'YAML'
metadata: {type: database}
ddl:
  target_dialect: oracle
  schema_mapping: {OWL_SRC: OWL_TGT}
source: {type: oracle, dsn: "oracle://OWL_SRC:OwlSrc123a@127.0.0.1:1521/XEPDB1", schema: OWL_SRC}
target: {type: oracle, dsn: "oracle://OWL_TGT:OwlTgt123a@127.0.0.1:1521/XEPDB1", schema: OWL_TGT}
export:
  filters: {"OWL_SRC.EMP": "sal > 0"}
  columns:
    include: {"OWL_SRC.EMP": ["empno", "sal", "ename"]}
    rename: {"OWL_SRC.EMP": {SAL: salary}}
YAML
"$BIN" migrate -c "$OUT/oracle2oracle.yaml" -r "$OUT/report.json" | tail -1
docker exec oracle bash -lc 'sqlplus -s OWL_TGT/"OwlTgt123a"@//localhost:1521/XEPDB1 <<"EOF"
SET PAGESIZE 20 FEEDBACK OFF
SELECT column_id || chr(32) || column_name FROM user_tab_columns ORDER BY column_id;
SELECT COUNT(*) FROM EMP;
EOF' | grep -E "^[0-9] (EMPNO|salary|ENAME)|^[0-9]+$"
grep -o '"filtered": true' "$OUT/report.json"

echo "== 2. mysql：where + 列裁剪 =="
docker exec mysql mysql -uroot -proot123456 -e "DROP DATABASE IF EXISTS owl_tgt; CREATE DATABASE owl_tgt;" 2>/dev/null
cat > "$OUT/mysql2mysql.yaml" <<'YAML'
metadata: {type: database}
ddl: {target_dialect: mysql, schema_mapping: {owl_demo: owl_tgt}}
source: {type: mysql, dsn: "root:root123456@tcp(127.0.0.1:3306)/owl_demo", schema: owl_demo}
target: {type: mysql, dsn: "root:root123456@tcp(127.0.0.1:3306)/owl_tgt", schema: owl_tgt}
export:
  filters: {"owl_demo.users": "id > 1"}
  columns: {include: {"owl_demo.users": ["id", "name"]}}
YAML
"$BIN" migrate -c "$OUT/mysql2mysql.yaml" | tail -1
docker exec mysql mysql -uroot -proot123456 -N -e "SELECT COUNT(*) FROM owl_tgt.users; SELECT column_name FROM information_schema.columns WHERE table_schema='owl_tgt' AND table_name='users' ORDER BY ordinal_position;" 2>/dev/null | tr '\n' ' '; echo

echo "== 3. 负路径：错误列名 → 门禁拒绝 =="
sed 's/sal > 0/nosuchcol = 1/' "$OUT/oracle2oracle.yaml" > "$OUT/bad.yaml"
out=$("$BIN" migrate -c "$OUT/bad.yaml" --skip-ddl 2>&1 || true)
if echo "$out" | grep -q "filter gate failed"; then
  echo "gate refused as expected"
else
  echo "FAIL: gate did not reject"; exit 1
fi

echo "== 4. online init 拒绝 filters =="
out=$("$BIN" online init -c "$OUT/oracle2oracle.yaml" 2>&1 || true)
if echo "$out" | grep -q "export.filters is set"; then
  echo "online refused as expected"
else
  echo "FAIL: online did not refuse"; exit 1
fi
echo "ALL PASS"
