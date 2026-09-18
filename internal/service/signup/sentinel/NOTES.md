# Sentinel v8go 求解器 — 实现笔记

> 对应交接文档 `docs/P4-SENTINEL-V8GO-HANDOFF.md`。
> 本文记录：V8 版本兼容性实测结论、内存治理策略、构建方法、与主线联调的对接点、已知的坑。

## 1. 结论（TL;DR）

**走 v8go 主路，Plan B（Node 常驻服务）未启用。**
`github.com/robomotionio/v8go`（V8 14.7.173.21，Chrome 147 stable，2026-04）
比 sdk.js（`20260810913b`）还新，交接文档 §1.4 担心的「V8 9.0 跑不动新 sdk」
风险不存在。验收 §6 全部实测通过（见 §4）。

## 2. 构建

### 2.1 依赖与预编译库

v8go 是 cgo 库，预编译 `libv8.a` 不进 git，需手动拉取一次（每个 GOPATH 一次）：

```sh
# go get 之后（版本以 go.mod 为准）
V8DIR="$(go env GOMODCACHE)/github.com/robomotionio/v8go@$(go list -m -f '{{.Version}}' github.com/robomotionio/v8go)"
chmod -R u+w "$V8DIR/deps"          # GOMODCACHE 默认只读
(cd "$V8DIR" && go run ./scripts/fetch-libv8.go)
```

覆盖平台：linux x86_64/arm64、darwin x86_64/arm64（及 Windows，本项目不用）。

### 2.2 构建命令

v8go 实现**只在 `v8` build tag 下编译**（默认构建不含 cgo 重依赖，goja 实现仍是默认）：

```sh
# macOS（本机 clang 即可）
CGO_ENABLED=1 go build -tags v8 ./...

# Linux：必须用 clang（V8 14.x 公开头文件跨 ABI 用了 libc++ 类型）
apt install clang
CC=clang CXX=clang++ CGO_ENABLED=1 go build -tags v8 ./...
```

测试：

```sh
GOTOOLCHAIN=auto CGO_ENABLED=1 go test -tags v8 ./internal/service/signup/sentinel/ -v
# 时区全量验证（Date.toString 依赖进程级 TZ，见 §5.4）：
TZ=America/New_York GOTOOLCHAIN=auto CGO_ENABLED=1 go test -tags v8 ./internal/service/signup/sentinel/ -run TestV8Timezone -v
```

二进制体积：+~30MB（V8 monolith 静态链接）。

## 3. 架构

```
V8Solver（并发安全）
├── sdkSource: patch 后的 sdk.js（进程内一份）
├── pool: []*v8Worker          ← Isolate 池（sync.Mutex 保护）
│     └── v8Worker{ iso, script, uses, fromCache }
│           ├── iso:    *v8.Isolate       （复用 maxSolvesPerIsolate=20 次后 Dispose 重建）
│           └── script: *v8.UnboundScript （优先用磁盘 CodeCache 编译）
├── js/shim.js   ← embed：浏览器环境垫片 + 定时器事件循环（__drainTimers）
├── js/flow.js   ← embed：两阶段求解主流程（requirements / solve）
└── v8_cache.go  ← CodeCache 落盘：$SENTINEL_CACHE_DIR 或 UserCacheDir/gpt-go/sdk/<ver>/sdk.v8cache
                   （文件头 64B 是 sdk 源码 sha256 hex，加载时校验防版本错配；
                   目录仅在写缓存时创建，只读命中不建目录）
```

每次求解：`acquire` 取 worker → 新建 `v8.Context`（用完 `Close`）→
注入 `__input` → 跑 shim.js → `script.Run(ctx)` 跑 sdk → 跑 flow.js →
`pump` 驱动事件循环直到 `__output` 就绪 → 解析输出。

## 4. 验收实测（macOS arm64, V8 14.7.173.21, sdk 20260810913b）

| # | 验收项 | 结果 |
|---|---|---|
| 1 | requirements 返回非空 request_p | ✅ 533-577 chars，`gAAAAAC` 前缀，payload 指纹字段齐全 |
| 2 | solve 注入桩 challenge 返回 token | ✅ `{"p","t","c","id","flow"}` 结构正确 |
| 2b | so.required=false 时 soToken="" 不报错 | ✅ |
| 3 | 时区：Intl patch + Date.toString | ✅（Date 部分需进程级 TZ env，见 §5.4） |
| 4 | CodeCache 第二次编译命中 | ✅ 2808B，warm 比 cold 快 ~17x（测试进程内），`Rejected=false` |
| 5 | 200 次连续求解 RSS 增长 | ✅ +26.7MB（< 50MB 阈值），每 20 次重建 Isolate |

性能：sdk 编译 cold ~1.3ms / warm(cache) ~0.6ms；eval ~2.5ms；
requirements 纯计算 ~8ms；solve 在 behavior=50ms 测试配置下 ~2.65s
（其中 ~2.1s 是行为模拟 sleep；sdk 的 se=5s prefetch 空等已由 pump 跳过，见 §5.7）。

## 5. 移植中踩到的坑（重要）

### 5.1 SDK patch 锚点随版本漂移
Python `.cjs` 的锚点基于旧 bundle，20260810913b 版标识符已变：

| Python 锚点 | 本版 bundle |
|---|---|
| `var P=new _;` | `var E=new O;` |
| `n(63)` / `ce` / `ye`（EXPOSE 锚点） | `n(5)` / `me` / `je` |
| `const Xn=5e3,`（提速 patch） | `const se=5e3,`（**未打**：只影响预取节奏，不影响正确性） |

`patchSDKV8` 对未命中锚点打 `WARN` 日志（`anchorMissLogger`）。
**sdk 版本升级时必须检查锚点命中率**（巡检项）。

### 5.2 v8go 无内建 Web API
`URL / URLSearchParams / TextEncoder / TextDecoder / btoa / atob / performance / setTimeout`
全部要在 shim.js 里用 JS 补齐（Node 里都是现成的）。
**URLSearchParams 必须实现 `Symbol.iterator`**，否则 sdk 报错并把错误信息
编进 token（静默劣化，极难排查——token 看着正常但服务端会拒）。

### 5.3 v8go 无事件循环
shim.js 实现了 ~40 行 FIFO 定时器队列（`setTimeout/setInterval/__drainTimers`），
Go 侧 `pump` 循环交替 `PerformMicrotaskCheckpoint()` 与到期定时器抽干，
连续 5 轮「无定时器且无产出」判定事件循环结束。语义对齐 goja_nodejs eventloop。

### 5.4 时区（最易翻车，设计决策点）
- `process.env.TZ = X` 在 v8go 里只改了 JS 对象，**不影响** ICU 时区。
- `Intl.DateTimeFormat` 的 patch 只影响 `resolvedOptions().timeZone`，
  **不影响** `new Date().toString()`（token 的 p[1] 字段）。
- **Date.toString 的时区由进程级 `TZ` env 决定**（V8/ICU 在 Isolate 创建时读取）。

含义：**一个进程内所有求解只能用同一个时区**。当前实现依赖部署时设置
`TZ` env。若主线指纹是「每个账号一个时区」，需要确认：
服务端校验的是 `Intl.resolvedOptions().timeZone`（已 patch，每账号可对）
还是 `Date.toString()` 的 GMT 偏移（进程级，全账号一致）。
**→ 联调时需主线确认这一点**（Python 方案每账号 fork 一个 Node 进程所以天然没这个问题）。

### 5.5 RunScript 顶层 const/let 是 script-scoped
shim.js 与 flow.js 是两个独立 `RunScript`，顶层 `const`/`let` 互不可见
（function 声明会挂 globalThis，const/let 不会）。
跨 script 共享状态（`__input/__listeners/__timers/capturedProof` 存取器）
**必须显式挂 `globalThis.__*`**，否则 promise 链静默断裂——不报错，
只是事件循环永远等不到结果。

### 5.6 performance.now() 自引用
shim 里写 `now: () => performance.now()` 会无穷递归
（v8go 里 `performance` 这个名字本身不存在，不像 Node 引用内建对象）。
必须用捕获的 `Date.now() - _t0`。

### 5.7 sdk prefetch 定时器空等（影响吞吐，已修）
sdk 在 token 算出后注册一个 `se`（默认 5s）的「下个 token 预取」setTimeout。
Python 方案在拿到 token 后直接 `process.exit(0)` 跳过它；我们的事件循环
模型下它会和行为模拟的短 sleep 在同一 timer 队列里被 pump 的 2s sleep cap
互相推后空转（实测 behavior=50ms 的 solve 从 2.6s 拖到 7.1s）。
**修复**：`pump` 每轮清掉延迟超过 `behaviorMs+3000ms` 的长定时器——
保留行为模拟的短 sleep，跳过 prefetch 空等（对齐 Python 语义，无风控影响）。

## 6. 内存治理

v8go 返回的 V8 对象**不被 Go GC 回收**（rogchap#105 类问题）。策略：

1. **每次求解新建 `v8.Context`，用完 `defer ctx.Close()`**（轻量，~µs 级）。
2. **Isolate 池化复用，每 `maxSolvesPerIsolate=20` 次求解后 `Dispose` 重建**——
   即使单次 Context 有残留，20 次后整个 Isolate 堆连根释放。
3. `V8Solver.Close()` 释放池中全部 Isolate（进程退出前调用）。
4. robomotionio fork 已修 FunctionTemplate 回调返回值泄漏（PR #4），
   本实现当前路径不依赖 Go 侧 FunctionTemplate 回调（垫片全 JS），暴露面小。
5. 回归保障：`TestV8NoLeak_RepeatedSolve` 200 次求解 RSS 增长 < 50MB。

## 6.1 并发安全（批量注册硬需求）

- **V8 Isolate 线程亲和**：v8go 约定「同一时间一个 Isolate 只被一个线程访问，
  不同 Isolate 可跨线程并行」（C++ 层每次调用加 `v8::Locker`，串行使用跨
  goroutine 安全，不需 `LockOSThread`）。本实现的池恰好满足：worker 被
  `acquire` 取出即独占，`release` 才归还。
- **独占不变式的运行时断言**：worker 带 `inUse atomic.Bool`，acquire 置位 /
  release 复位，任何「同一 worker 被并发持有」的破坏会立即 panic（fail-fast，
  防未来改动破坏隐式约定）。
- **Close 等待 in-flight**：`Close()` 先销毁池中存货，再 `wg.Wait()` 等所有
  在途求解的 `release` 把它们各自的 worker 销毁后才返回。
- **ctx 取消能打断同步阻塞**：每次求解起 `context.AfterFunc` watchdog，
  ctx 取消时 `iso.TerminateExecution()` 强制中断该 Isolate 的 JS 执行
  （RunScript 是同步 cgo 调用，sdk 同步死循环时 pump 的 ctx 检查进不去），
  并标记该 worker 不可复用（TerminateExecution 后 Isolate 残废）。
- **并发测试**（`v8_race_test.go`，全部 `-race` 通过）：
  `TestV8ConcurrentSolve`（8 goroutine 竞争池）、
  `TestV8ConcurrentFullToken`（12 goroutine × 3 次完整三阶段，36/36 成功）、
  `TestV8ConcurrentWorkerChurn`（压垮 maxSolvesPerIsolate 强制 Dispose+重建交错）、
  `TestV8ConcurrentClose`（在途求解与 Close 竞态 + 二次 Close 幂等）。

## 7. 与主线联调的对接点

| 对接点 | 状态 | 说明 |
|---|---|---|
| `Solver` 接口 | ✅ 不变 | `NewV8Solver(src, WithV8Fetcher(f), ...)` 替代 `NewGojaSolverWithFetcher` |
| `EnvPayload` | ✅ 不变 | 字段全映射（`buildInput`），`DeviceMemory` 为 nil 时不注入（Firefox 语义） |
| `ChallengeFetcher` | ✅ 不变 | `/sentinel/req` 仍由主线带 TLS 指纹的会话发，v8 内 `fetch=throw` |
| `Challenge.Raw` | ✅ 使用 | solve 阶段优先透传完整原始 challenge JSON 给 sdk |
| challenge 校验 | ✅ fail-fast | `challenge.Token==""` 在 challenge 阶段直接报错（残缺 challenge 注入沙箱会产出"结构正常但服务端必拒"的 token） |
| env 校验 | ✅ fail-fast | `UserAgent/Screen 尺寸/DeviceID` 零值直接报错（防指纹趋同被风控聚类） |
| **时区策略** | ⚠️ 待确认 | 见 §5.4：进程级 TZ vs 每账号时区 |
| SDK 版本巡检 | ⚠️ 新增项 | 版本升级时检查 patch 锚点命中（`WARN sdk patch anchor` 日志） |

## 8. 切换与回退

- **启用 v8go**：构建加 `-tags v8`，构造处改用 `NewV8Solver`。
- **回退 goja**：去掉 `-tags v8`，用 `NewGojaSolverWithFetcher`（默认构建即是）。
- **Plan B（Node 常驻服务）**：未实现。若未来 sdk 用了 shim 仿不像的行为，
  按交接文档 §7 起一个 `node sentinel_server.cjs` 复用同一份 shim/flow JS，
  接口对上层等价（同样实现 `Solver`）。

## 9. 已知限制

1. 单进程单时区（§5.4）。
2. `crypto.getRandomValues/randomUUID` 用 `Math.random` 实现（非密码学强度）——
   与 Python 的 `crypto.randomFillSync` 不同，但只用于 uuid/jitter，无安全语义。
3. `performance.memory.jsHeapSizeLimit` 是伪造值（对齐 Python 的 device_memory 联动公式）。
4. solve 的 `se=5e3` 预取等待未提速（§5.1），生产单次 solve 仍有 ~5s sdk 内部等待。
