# GPT-GO 构建/部署 Makefile
#
# 【v8 是硬依赖，默认构建】Sentinel PoW 求解用 v8go（真 V8, CGO + 预编译 libv8.a）。
# 不带 -tags v8 会编译出「无 solver」的二进制，运行时 sentinel 步骤报明确错误——
# 这是部署事故。故本 Makefile 把 v8 设为默认：所有 build/test 目标一律带 -tags v8。
#
# 部署目标（契约）：Linux x86_64 + macOS（不跑 Windows）。v8go 官方对这两个平台
# 提供预编译 libv8.a，`go build` 自动链接，无需自编译 V8（那要 ~30min + depot_tools）。
#
# 注意：libv8.a 的下载/链接发生在「构建机打包期」，部署的是已含 V8 的成品二进制，
# 部署启动时不编译。你要的只是「打包命令带 v8 tag」——本 Makefile 帮你固定它。

# 工具链：go.mod 要求 go >= 1.26；本机 go 版本较低时由 GOTOOLCHAIN=auto 自动切换。
export GOTOOLCHAIN := auto
# v8go 必须 CGO。
export CGO_ENABLED := 1

# 构建产物与入口。
BIN      := bin/gpt-go-server
PKG      := ./cmd/server
# v8 build tag（硬依赖，勿删）。
TAGS     := -tags v8

# 操作系统检测：Linux 下 V8 14.x 公开头用 libc++，必须用 clang（gcc 链不上）。
UNAME_S  := $(shell uname -s)
ifeq ($(UNAME_S),Linux)
  export CC  := clang
  export CXX := clang++
endif

.PHONY: all build run test test-race vet fmt clean help

# 默认目标：构建（带 v8）。
all: build

## build: 构建服务二进制（默认带 v8 + CGO；Linux 自动用 clang）。
build:
	@mkdir -p bin
	go build $(TAGS) -o $(BIN) $(PKG)
	@echo "built: $(BIN)  (v8 已编入, CGO_ENABLED=$(CGO_ENABLED), OS=$(UNAME_S))"

## run: 本地启动（带 v8 构建后直接跑；用 config/config.yaml）。
run: build
	$(BIN) -config config/config.yaml

## test: 全部测试（带 v8 tag，确保 sentinel/authflow 装配路径被覆盖）。
test:
	go test $(TAGS) -count=1 ./...

## test-race: 全部测试 + race（含 SetFetcher 并发安全）。
test-race:
	go test $(TAGS) -count=1 -race ./...

## vet: 静态检查（带 v8 tag，与构建口径一致）。
vet:
	go vet $(TAGS) ./...

## fmt: 格式化检查（列出未格式化文件；空输出 = 干净）。
fmt:
	@test -z "$$(gofmt -l internal/ cmd/)" || { gofmt -l internal/ cmd/; echo "存在未格式化文件"; exit 1; }
	@echo "fmt clean"

## verify: 交付前一键自检（fmt + vet + build + test）。
verify: fmt vet build test
	@echo "verify OK (v8 默认构建)"

## clean: 删除构建产物。
clean:
	rm -rf bin

## help: 列出所有目标。
help:
	@grep -E '^## ' Makefile | sed 's/## //'
