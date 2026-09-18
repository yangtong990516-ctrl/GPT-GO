#!/usr/bin/env bash
# GPT-GO 启动脚本(防孤儿进程)。
#
# 防孤儿三件套:
#   1. PID 文件(.run/server.pid)— 记录本次启动的进程号,stop.sh 据此精准终止;
#   2. 端口占用检测 — 端口已被占用时,先判断是不是「自己人」(有 PID 文件且进程存活),
#      是自己人则提示已运行,是别人(孤儿/外部占用)则报错并给出处理建议,绝不盲目启动第二个;
#   3. 启动前清理 — 若存在「有 PID 文件但进程已死」的陈旧 PID,自动清掉再启动。
#
# 用法:
#   ./start.sh              # 前台构建并后台启动(默认)
#   ./start.sh --fg         # 前台运行(调试用,Ctrl+C 停止)
#   ./start.sh --no-build   # 跳过前端/后端构建,直接用现有产物启动
set -euo pipefail

# ── 路径与常量 ────────────────────────────────────────────────────────────────
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

BIN="bin/gpt-go-server"
CONFIG="config/config.yaml"
PID_DIR=".run"
PID_FILE="$PID_DIR/server.pid"
LOG_FILE="logs/server.log"
PORT=""  # 从 config 读取

FG=0
NO_BUILD=0
for arg in "$@"; do
  case "$arg" in
    --fg) FG=1 ;;
    --no-build) NO_BUILD=1 ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "未知参数: $arg (用 -h 看帮助)" >&2; exit 2 ;;
  esac
done

mkdir -p "$PID_DIR" logs

log() { printf '\033[1;36m[start]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[start]\033[0m %s\n' "$*" >&2; }
err() { printf '\033[1;31m[start]\033[0m %s\n' "$*" >&2; }

# ── 从 config.yaml 读端口(简单 grep,不依赖 yq)────────────────────────────────
read_port() {
  local p
  p="$(grep -E '^\s*port:' "$CONFIG" 2>/dev/null | head -1 | grep -oE '[0-9]+' | head -1 || true)"
  echo "${p:-8000}"
}
PORT="$(read_port)"

# ── 进程存活判断 ──────────────────────────────────────────────────────────────
pid_alive() {  # $1=pid
  [[ -n "${1:-}" ]] && kill -0 "$1" 2>/dev/null
}

# 该 PID 是不是我们的 server(防止 PID 复用误杀)
is_our_server() {  # $1=pid
  local pid="$1"
  pid_alive "$pid" || return 1
  ps -o command= -p "$pid" 2>/dev/null | grep -q "$(basename "$BIN")"
}

# ── 端口占用检测:返回占用者 PID(无则空)──────────────────────────────────────
port_owner() {  # $1=port
  lsof -tiTCP:"$1" -sTCP:LISTEN 2>/dev/null | head -1 || true
}

# ── 1) 陈旧 PID 清理(有 PID 文件但进程已死)─────────────────────────────────
if [[ -f "$PID_FILE" ]]; then
  old_pid="$(cat "$PID_FILE" 2>/dev/null || true)"
  if pid_alive "$old_pid"; then
    if is_our_server "$old_pid"; then
      log "服务已在运行 (PID=$old_pid, 端口=$PORT)。"
      log "如需重启: ./stop.sh && ./start.sh;查看日志: tail -f $LOG_FILE"
      exit 0
    else
      warn "PID 文件指向的进程 $old_pid 不是本服务(PID 复用),清理陈旧 PID 文件。"
      rm -f "$PID_FILE"
    fi
  else
    warn "发现陈旧 PID 文件(进程 $old_pid 已退出),已清理。"
    rm -f "$PID_FILE"
  fi
fi

# ── 2) 端口冲突检测(孤儿/外部占用)─────────────────────────────────────────
owner="$(port_owner "$PORT")"
if [[ -n "$owner" ]]; then
  if is_our_server "$owner"; then
    # 端口被自己人占着但没有有效 PID 文件 → 这是孤儿,回收它。
    warn "检测到本服务孤儿进程 (PID=$owner) 占用端口 $PORT,正在回收…"
    kill "$owner" 2>/dev/null || true
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      pid_alive "$owner" || break
      sleep 0.3
    done
    pid_alive "$owner" && { warn "优雅终止失败,强制 kill -9 $owner"; kill -9 "$owner" 2>/dev/null || true; }
    rm -f "$PID_FILE"
  else
    err "端口 $PORT 被其它进程占用 (PID=$owner):"
    ps -o pid=,command= -p "$owner" 2>/dev/null | sed 's/^/    /' >&2 || true
    err "请先释放该端口,或修改 $CONFIG 的 server.port。"
    exit 1
  fi
fi

# ── 3) 依赖检查 ───────────────────────────────────────────────────────────────
if ! grep -qE '^\s*uri:\s*mongodb://' "$CONFIG" 2>/dev/null; then
  warn "$CONFIG 未配置 mongodb.uri,服务将以内存模式运行(重启丢数据)。"
fi

# ── 4) 构建 ───────────────────────────────────────────────────────────────────
if [[ "$NO_BUILD" -eq 0 ]]; then
  # 前端(web/dist 存在且比 src 新则跳过)
  if [[ -d web ]]; then
    need_web=0
    if [[ ! -d web/dist ]]; then need_web=1
    elif [[ -n "$(find web/src -newer web/dist -name '*.tsx' -o -newer web/dist -name '*.ts' 2>/dev/null | head -1)" ]]; then need_web=1
    fi
    if [[ "$need_web" -eq 1 ]]; then
      log "构建前端…"
      (cd web && npm run build) || { err "前端构建失败"; exit 1; }
    else
      log "前端产物已是最新,跳过构建。"
    fi
  fi
  # 后端
  log "构建后端…"
  GOTOOLCHAIN=auto go build -o "$BIN" ./cmd/server || { err "后端构建失败"; exit 1; }
fi

[[ -x "$BIN" ]] || { err "找不到可执行文件 $BIN(先去掉 --no-build 让脚本构建)"; exit 1; }

# ── 5) 启动 ───────────────────────────────────────────────────────────────────
if [[ "$FG" -eq 1 ]]; then
  log "前台启动 (端口=$PORT)… Ctrl+C 停止"
  exec "$BIN" -config "$CONFIG"
else
  log "后台启动 (端口=$PORT)… 日志: $LOG_FILE"
  nohup "$BIN" -config "$CONFIG" >>"$LOG_FILE" 2>&1 &
  new_pid=$!
  echo "$new_pid" > "$PID_FILE"
  # 启动健康检查:最多等 10s,确认进程存活且端口在监听。
  ok=0
  for _ in $(seq 1 20); do
    if ! pid_alive "$new_pid"; then
      err "服务启动后立即退出,最近日志:"
      tail -n 20 "$LOG_FILE" 2>/dev/null | sed 's/^/    /' >&2 || true
      rm -f "$PID_FILE"
      exit 1
    fi
    [[ -n "$(port_owner "$PORT")" ]] && { ok=1; break; }
    sleep 0.5
  done
  if [[ "$ok" -eq 1 ]]; then
    log "启动成功 (PID=$new_pid)。访问: http://127.0.0.1:$PORT"
    log "停止: ./stop.sh;日志: tail -f $LOG_FILE"
  else
    warn "进程存活但端口 $PORT 尚未监听(可能仍在初始化)。用 tail -f $LOG_FILE 观察。"
  fi
fi
