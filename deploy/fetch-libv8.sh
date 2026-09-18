#!/usr/bin/env bash
# 下载 v8go 的预编译 V8 静态库 libv8.a 到 deps/{os}_{arch}/。
# v8go(robomotionio 新版)不随 go module 下发该库,缺失时链接报 `cannot find -lv8`。
# 本脚本被 Makefile 的 build 目标自动调用(检测到 libv8.a 缺失才下载)。
set -euo pipefail

export GOTOOLCHAIN=auto

# 精确拿 v8go 模块在 module cache 的真实目录(含版本)。
MOD_DIR="$(go list -f '{{.Dir}}' -m github.com/robomotionio/v8go 2>/dev/null || true)"
if [[ -z "$MOD_DIR" || ! -d "$MOD_DIR" ]]; then
  echo "[fetch-libv8] 解析 v8go 模块目录失败,先 go mod download" >&2
  go mod download github.com/robomotionio/v8go
  MOD_DIR="$(go list -f '{{.Dir}}' -m github.com/robomotionio/v8go)"
fi

GOOS="$(go env GOOS)"; GOARCH="$(go env GOARCH)"
TARGET_DIR="$MOD_DIR/deps/${GOOS}_${GOARCH}"
TARGET="$TARGET_DIR/libv8.a"

if [[ -f "$TARGET" ]]; then
  echo "[fetch-libv8] libv8.a 已存在(${GOOS}_${GOARCH}),跳过下载"
  exit 0
fi

echo "[fetch-libv8] 下载 ${GOOS}_${GOARCH}/libv8.a(约 143MB)…"
# 模块 cache 只读,拷到临时目录 fetch 后再拷回。
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
cp -r "$MOD_DIR" "$TMP/v8go"
chmod -R u+w "$TMP/v8go"
( cd "$TMP/v8go" && go run scripts/fetch-libv8.go )
mkdir -p "$TARGET_DIR" 2>/dev/null || true
chmod -R u+w "$TARGET_DIR" 2>/dev/null || true
cp "$TMP/v8go/deps/${GOOS}_${GOARCH}/libv8.a" "$TARGET"
chmod 0644 "$TARGET" 2>/dev/null || true
echo "[fetch-libv8] 完成: $TARGET"
