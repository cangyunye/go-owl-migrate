#!/usr/bin/env bash
# 从 testdata/db/.local-dev.env 注入 DSN，生成验收用 yaml 到 gen/（gitignored）。
# 用法：bash gen.sh   （在仓库任意目录可执行；产物在脚本同目录 gen/ 下）
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
ENVFILE="$HERE/../db/.local-dev.env"
if [ ! -f "$ENVFILE" ]; then
  echo "缺少 $ENVFILE" >&2; exit 1
fi
# shellcheck disable=SC1090
set -a; . "$ENVFILE"; set +a

: "${OWL_E2E_MYSQL_DSN:?需 OWL_E2E_MYSQL_DSN}"
: "${OWL_E2E_OB_MYSQL_DSN:?需 OWL_E2E_OB_MYSQL_DSN}"
: "${OWL_E2E_OB_ORACLE_MIGSRC_DSN:?需 OWL_E2E_OB_ORACLE_MIGSRC_DSN}"
: "${OWL_E2E_PG_DSN:?需 OWL_E2E_PG_DSN}"
: "${OWL_E2E_OPGAUSS_DSN:?需 OWL_E2E_OPGAUSS_DSN}"

OUT="$HERE/gen"
mkdir -p "$OUT"
render() { sed -e "s|__DSN__|$OWL_E2E_MYSQL_DSN|" \
              -e "s|__MIGSRC_DSN__|$OWL_E2E_OB_ORACLE_MIGSRC_DSN|" \
              -e "s|__PG_DSN__|$OWL_E2E_PG_DSN|" \
              -e "s|__OG_DSN__|$OWL_E2E_OPGAUSS_DSN|" \
              "$HERE/$1" > "$OUT/${1%.yaml.tmpl}.yaml"; }

for t in source.mysql source.obmysql source.oboracle source.pg source.opengauss \
         migrate.mysql2pg migrate.mysql2oboracle; do
  render "$t.yaml.tmpl"
done
echo "generated: $OUT"
ls "$OUT"
