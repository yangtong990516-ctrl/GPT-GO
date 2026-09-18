#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════════════
# GPT-GO 线上服务器环境一键准备(Ubuntu 22.04 / Debian 系)
# ═══════════════════════════════════════════════════════════════════════════
# 用途:在新服务器上一次装好 GPT-GO 编译/运行所需的全部环境依赖。
#       后续只需 `git pull && ./start.sh`,无需再重复本脚本。
#
# 已固化的依赖(本次部署实测踩坑后固化):
#   1. Go 1.26+           — 后端语言
#   2. Node.js 20+ + npm  — 前端构建
#   3. MongoDB 5+         — 存储(程序用独立库,不影响同机其它项目)
#   4. clang-19           — 【关键】Linux 编译 v8go 必须;v8go 内置 libc++
#                            用到 clang-19 才有的内建(__builtin_clzg 等),
#                            Ubuntu 22.04 默认 clang-14/15 都编不过(实测)。
#   5. libv8.a            — v8go 预编译 V8 静态库(~143MB),不随 go module
#                            下发,由 Makefile 的 fetch-libv8 目标自动下载。
#
# 用法(服务器上,root):
#   git clone https://github.com/yangtong990516-ctrl/GPT-GO.git /opt/gpt-go
#   cd /opt/gpt-go && bash deploy/server-setup.sh
#   cp config/config.example.yaml config/config.yaml   # 按需改 mongodb/port
#   ./start.sh                                          # 构建前端+后端并启动
# ═══════════════════════════════════════════════════════════════════════════
set -euo pipefail

log()  { printf '\033[1;36m[setup]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[setup]\033[0m %s\n' "$*" >&2; }
ok()   { printf '\033[1;32m[setup]\033[0m %s\n' "$*"; }

[[ "$(id -u)" == "0" ]] || warn "建议以 root 运行(需要 apt 装包)"

export DEBIAN_FRONTEND=noninteractive

# ── 1. Go 1.26+ ─────────────────────────────────────────────────────────────
if command -v go >/dev/null 2>&1 || [[ -x /usr/local/go/bin/go ]]; then
  ok "Go 已存在: $(/usr/local/go/bin/go version 2>/dev/null || go version)"
else
  log "安装 Go 1.26 …"
  GOVER=1.26.7
  wget -q "https://go.dev/dl/go${GOVER}.linux-amd64.tar.gz" -O /tmp/go.tgz
  rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz
  grep -q '/usr/local/go/bin' /etc/profile || echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
  export PATH=$PATH:/usr/local/go/bin
  ok "Go 装好: $(go version)"
fi
export PATH=$PATH:/usr/local/go/bin

# ── 2. Node.js 20+ ─────────────────────────────────────────────────────────
if command -v node >/dev/null 2>&1 && [[ "$(node -v | grep -oE '[0-9]+' | head -1)" -ge 18 ]]; then
  ok "Node 已存在: $(node -v)"
else
  log "安装 Node.js 20 …"
  curl -fsSL https://deb.nodesource.com/setup_20.x | bash - >/dev/null 2>&1
  apt-get install -y -qq nodejs >/dev/null 2>&1
  ok "Node 装好: $(node -v)"
fi

# ── 3. MongoDB(仅检测,不强制装——可指向外部库)──────────────────────────────
if pgrep -x mongod >/dev/null 2>&1; then
  ok "MongoDB 运行中"
else
  warn "未检测到运行中的 mongod。若用本机 mongo 请先启动;若用外部库,在 config.yaml 填 mongodb.uri 即可。"
fi

# ── 4. clang-19(Linux 编 v8 关键依赖)──────────────────────────────────────
if command -v clang-19 >/dev/null 2>&1; then
  ok "clang-19 已存在: $(clang-19 --version | head -1)"
else
  log "安装 clang-19(加 LLVM 官方源)…"
  apt-get update -qq
  apt-get install -y -qq wget gnupg lsb-release software-properties-common >/dev/null 2>&1
  CODENAME="$(lsb_release -cs 2>/dev/null || echo jammy)"
  wget -qO- https://apt.llvm.org/llvm-snapshot.gpg.key | apt-key add - >/dev/null 2>&1 || true
  add-apt-repository -y "deb http://apt.llvm.org/${CODENAME}/ llvm-toolchain-${CODENAME}-19 main" >/dev/null 2>&1
  apt-get update -qq
  apt-get install -y -qq clang-19 >/dev/null 2>&1
  ok "clang-19 装好: $(clang-19 --version | head -1)"
fi

# ── 5. git / make / 构建工具 ────────────────────────────────────────────────
apt-get install -y -qq git make build-essential >/dev/null 2>&1
ok "git/make/build-essential 就绪"

# ── 6. libv8.a(交由 Makefile 在 build 时自动下载;此处仅提示)───────────────
log "libv8.a 将在首次 'make build' / './start.sh' 时自动下载(约 143MB)"

echo
ok "环境准备完成。下一步:"
echo "    cd $(pwd)"
echo "    cp config/config.example.yaml config/config.yaml   # 改 mongodb.uri / server.port / host"
echo "    ./start.sh"
