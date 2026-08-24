#!/bin/bash
# gvenzl/oracle-free 首次启动（数据卷为空）时自动执行：
# 以 SYS 身份创建 SCOTT 用户及 EMP/DEPT 测试数据。
# 等效于手动执行：
#   docker exec -i oracle sqlplus -s sys/Oracle123!@//localhost:1521/XEPDB1 as sysdba < testdata/db/oracle/scott_seed.sql
set -euo pipefail

DSN="sys/${ORACLE_PASSWORD}@//localhost:1521/${ORACLE_DATABASE}"

sqlplus -s -L "${DSN}" as sysdba <<'EOSQL'
WHENEVER SQLERROR EXIT FAILURE
@/initdb/scott_seed.sql
EOSQL

count="$(sqlplus -s -L "${DSN}" as sysdba <<'EOSQL'
WHENEVER SQLERROR EXIT FAILURE
SET HEADING OFF FEEDBACK OFF PAGESIZE 0
SELECT COUNT(*) FROM dba_users WHERE username = 'SCOTT';
EXIT;
EOSQL
)"

if [ "$(echo "${count}" | tr -d '[:space:]')" = "1" ]; then
  echo "[init-scott] SCOTT schema seeded successfully"
else
  echo "[init-scott] ERROR: SCOTT user not found after seeding" >&2
  exit 1
fi
