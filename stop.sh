#!/usr/bin/env bash
# GPT-GO 终止脚本(精准、不误杀、不留孤儿)。
#
# 终止顺序:
#   1. 优先按 PID 文件(.run/server.pid)终止——只杀我们启动的那个进程;
#   2. PID 文件缺失/失效时,回退按「端口 + 进程名」识别本服务,避免误杀别的程序;
#   3. 先 SIGTERM 优雅退出,超时再 SIGKILL;
#   4. 收尾清理 PID 文件,并报告是否有残留。
#
# 用法:
#   ./stop.sh          # 优雅终止(默认 10s 超时后强杀)
#   ./stop.sh --force  # 直接 SIGKILL
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

BIN_NAME="gpt-go-server"
CONFIG="config/config.yaml"
PID_FILE=".run/server.pid"
FORCE=0
[[ "${1:-}" == "--force" ]] && FORCE=1

log() { printf '\033[1;36m[stop]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[stop]\033[0m %s\n' "$*" >&2; }

pid_alive() { [[ -n "${1:-}" ]] && kill -0 "$1" 2>/dev/null; }
is_our_server() {
  pid_alive "$1" || return 1
  ps -o command= -p "$1" 2>/dev/null | grep -q "$BIN_NAME"
}
read_port() {
  local p
  p="$(grep -E '^\s*port:' "$CONFIG" 2>/dev/null | head -1 | grep -oE '[0-9]+' | head -1 || true)"
  echo "${p:-8000}"
}

kill_pid() {  # $1=pid
  local pid="$1"
  if [[ "$FORCE" -eq 1 ]]; then
    kill -9 "$pid" 2>/dev/null || true
    return 0
  fi
  kill "$pid" 2>/dev/null || true
  for _ in $(seq 1 20); do  # 最多 10s
    pid_alive "$pid" || return 0
    sleep 0.5
  done
  warn "优雅退出超时,强制 SIGKILL $pid"
  kill -9 "$pid" 2>/dev/null || true
}

stopped_any=0

# ── 1) 按 PID 文件终止 ────────────────────────────────────────────────────────
if [[ -f "$PID_FILE" ]]; then
  pid="$(cat "$PID_FILE" 2>/dev/null || true)"
  if pid_alive "$pid"; then
    if is_our_server "$pid"; then
      log "终止服务 (PID=$pid)…"
      kill_pid "$pid"
      stopped_any=1
    else
      warn "PID 文件指向的 $pid 不是本服务(PID 复用),不杀,仅清理 PID 文件。"
    fi
  fi
  rm -f "$PID_FILE"
fi

# ── 2) 兜底:按端口+进程名找残留(覆盖 PID 文件丢失的孤儿)────────────────────
PORT="$(read_port)"
orphans="$(lsof -tiTCP:"$PORT" -sTCP:LISTEN 2>/dev/null || true)"
for pid in $orphans; do
  if is_our_server "$pid"; then
    warn "发现端口 $PORT 上的本服务残留进程 (PID=$pid),一并终止。"
    kill_pid "$pid"
    stopped_any=1
  fi
done

# ── 3) 收尾报告 ───────────────────────────────────────────────────────────────
# 再扫一次,确认没有本服务进程残留(不限端口,防止换过端口的情况)。
leftover="$(pgrep -f "$BIN_NAME" 2>/dev/null || true)"
if [[ -n "$leftover" ]]; then
  # 只报告,不滥杀(可能是别的同名进程/测试进程)。
  warn "仍检测到 $BIN_NAME 相关进程: $leftover(非本脚本启动,未动它)。"
fi

if [[ "$stopped_any" -eq 1 ]]; then
  log "已停止。"
else
  log "没有正在运行的本服务实例。"
fi
