#!/usr/bin/env bash
# AI 对话页 Playwright 验收一键入口（aichat_scenarios.py 头注释引用的 runner）。
#
# 流程：
#   1. 临时 OWL_MIGRATE_HOME 隔离（jobs DB / 数据源 vault / 会话库均不落真实用户目录）
#   2. 起 mock vendor（OpenAI 兼容脚本化回复，见 mock_vendor.py）
#   3. 写入带 ai.base_url 的 serve 配置并启动 serve（自带本地 master，
#      场景 H 的「确认并执行」可真启动任务）
#   4. 经 API 预置数据源档案 oracle-scott / p2（场景 E/G/H/K 引用）
#   5. 跑 aichat_scenarios.py，透传其退出码
#
# 用法：
#   bash scripts/acceptance/run_aichat_acceptance.sh [port]   # 默认 18095
#   task web-aichat                                           # Taskfile 入口
#
# 环境变量：
#   OWL_MIGRATE_BIN  指定 owl-migrate 二进制（默认自动找 dist/ 下最新构建）
set -euo pipefail

PORT="${1:-18095}"
VENDOR_PORT=18096
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

command -v python3 >/dev/null || { echo "需要 python3" >&2; exit 1; }
python3 -c 'import playwright' 2>/dev/null || {
  echo "缺少 playwright：python3 -m pip install playwright && python3 -m playwright install chromium" >&2
  exit 1
}
command -v curl >/dev/null || { echo "需要 curl" >&2; exit 1; }

BIN="${OWL_MIGRATE_BIN:-}"
if [ -z "$BIN" ]; then
  for cand in "$ROOT/dist/owl-migrate" "$ROOT/dist/$(go env GOOS 2>/dev/null || uname | tr '[:upper:]' '[:lower:]')-$(go env GOARCH 2>/dev/null || echo amd64)/owl-migrate"; do
    [ -x "$cand" ] && BIN="$cand" && break
  done
fi
[ -n "$BIN" ] && [ -x "$BIN" ] || {
  echo "找不到 owl-migrate 二进制 —— 先构建（task build / make build），或设 OWL_MIGRATE_BIN=/path/to/owl-migrate" >&2
  exit 1
}

HOME_DIR=$(mktemp -d /tmp/owl-aichat.XXXXXX)
CFG="$HOME_DIR/migrate.yaml"
VENDOR_LOG=/tmp/owl-aichat.vendor.log
SRV_LOG=/tmp/owl-aichat.serve.log
SRV_PID=""
VENDOR_PID=""

cleanup() {
  [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null || true
  [ -n "$VENDOR_PID" ] && kill "$VENDOR_PID" 2>/dev/null || true
  rm -rf "$HOME_DIR"
}
trap cleanup EXIT

# serve 恢复的激活配置：AI 段指向 mock vendor；API key 走环境变量，永不落盘。
cat > "$CFG" <<EOF
general:
  log_level: error
ai:
  provider: deepseek
  base_url: http://127.0.0.1:${VENDOR_PORT}
  api_key_env: OWL_AI_API_KEY
  model: mock-model
EOF

# ── 1. mock vendor ──────────────────────────────────────────────────────────
python3 "$ROOT/scripts/acceptance/mock_vendor.py" "$VENDOR_PORT" >"$VENDOR_LOG" 2>&1 &
VENDOR_PID=$!
for _ in $(seq 1 50); do
  curl -sf "http://127.0.0.1:$VENDOR_PORT/health" >/dev/null 2>&1 && break
  sleep 0.2
done
curl -sf "http://127.0.0.1:$VENDOR_PORT/health" >/dev/null || {
  echo "mock vendor 启动失败（见 $VENDOR_LOG）" >&2; exit 1; }

# ── 2. serve（隔离 HOME） ───────────────────────────────────────────────────
export OWL_MIGRATE_HOME="$HOME_DIR"
export OWL_AI_API_KEY="test-key"
"$BIN" serve --port "$PORT" --config-out "$CFG" >"$SRV_LOG" 2>&1 &
SRV_PID=$!
for _ in $(seq 1 50); do
  curl -sf "http://127.0.0.1:$PORT/api/v1/health" >/dev/null 2>&1 && break
  sleep 0.2
done
curl -sf "http://127.0.0.1:$PORT/api/v1/health" >/dev/null || {
  echo "serve 启动失败（见 $SRV_LOG）" >&2; exit 1; }

# ── 3. 预置数据源档案（密码入临时 HOME 的 vault，不入库不入页面） ────────────
api() { curl -s -H 'Content-Type: application/json' "$@"; }
api -X POST "http://127.0.0.1:$PORT/api/v1/datasources" \
  -d '{"name":"oracle-scott","type":"oracle","schema":"SCOTT","dsn":"oracle://scott:SeedSecret1@127.0.0.1:1521/XEPDB1","remark":"验收档案"}' >/dev/null
api -X POST "http://127.0.0.1:$PORT/api/v1/datasources" \
  -d '{"name":"p2","type":"mysql","schema":"appdb","dsn":"root:SeedSecret2@tcp(127.0.0.1:3306)/appdb","remark":"验收档案"}' >/dev/null

# ── 4. 验收 ─────────────────────────────────────────────────────────────────
cd "$ROOT"
python3 scripts/acceptance/aichat_scenarios.py "http://127.0.0.1:$PORT"
