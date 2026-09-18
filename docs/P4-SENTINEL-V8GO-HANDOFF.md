# P4 — Sentinel SDK 求解器（v8go）交接说明

> 这是一份**自包含**的任务交接文档。接手者无需阅读本会话的任何上下文，
> 仅凭本文 + 参照路径即可独立完成实现。目标平台：**Linux x86_64 + macOS**（**不跑 Windows**）。
> 约束：本任务只负责「Sentinel token / so_token 求解」这一件事，**不碰 HTTP 传输层、不碰指纹生成**
> （那两块由另一条线并行开发，见「协作边界」）。

---

## 0. 一句话目标

把 codex-auto(Python) 里「用 Node.js 子进程跑真实 `sdk.js` 解 Sentinel PoW」的能力，
在 **GPT-GO(Go)** 里用 **v8go（内嵌真 V8）+ 预编译字节码缓存（CachedData）** 重新实现，
做到 **sdk.js 只解析一次、之后复用编译缓存**，从而消除「每次 fork Node + 重新解析 33KB」的开销。

---

## 1. 背景（为什么这么做）

### 1.1 Python 原版是怎么做的（参照，不要照抄）
- 路径：`/Users/iceman/Downloads/苹果系统无限三代安装器/work/codex_audit/protocol_signup/_runtime/`
- `sentinel.py` → `sentinel_quickjs.py`（名字叫 quickjs，**实际是 Node.js 子进程**）
  - `subprocess.run(["node", "openai_sentinel_quickjs.cjs"], input=json_payload)`
  - `.cjs` 在 `node:vm` 沙箱里加载真实 `sdk.js`，伪造一套浏览器环境（navigator/document/iframe/...），
    两阶段执行：`action=requirements` → 拿到 `request_p`；`action=solve` → 拿到 `token` + `so_token`。
- **痛点（要解决的核心）**：每次求解都 fork 一个 Node 进程并重新 `vm.runInContext(33KB sdk)` 解析一次，
  单 token 成本秒级，且靠信号量限并发。

### 1.2 我们要改成什么
- 用 **v8go** 把真 V8 编进 Go 进程（cgo），
  - `Isolate`/`Context` 常驻，sdk.js **编译一次**，
  - 用 `CompileUnboundScript` + `CreateCodeCache()` 落盘字节码缓存，
  - 之后 `CompileUnboundScript(..., CompileOptions{CachedData: cache})` 直接跳过解析。
- 结果：**解析成本从「每次 1 次」降到「全局 1 次」**，只保留每次必须的 PoW 计算。

### 1.3 平台与 cgo 风险（已评估，结论：低）
- 部署目标 = **Ubuntu x86_64(5.15) + macOS**，**无 Windows**。
- v8go 官方**仅为 Linux 和 macOS 提供预编译 `libv8.a`**，`go build` 自动链接，无需自编译 V8（那要 ~30min + depot_tools）。
- **Windows 是 v8go 的已知软肋**（官方 README 明说暂无 Windows 预编译），但我们不跑 Windows，**该风险不适用**。
- 构建要求：`CGO_ENABLED=1`，二进制体积 +~30MB（可接受）。

### 1.4 必须盯住的两个非 cgo 风险（重要）
1. **内存泄漏**：v8go 返回的 V8 对象**不被 Go GC 回收**（见 v8go issue #105）。
   高频批量求解时必须 **显式 `ctx.Close()` + `iso.Dispose()`**，或维护 Isolate 池、Context 用完即关。
   上线前用 `pprof` 盯 RSS 曲线确认无泄漏。
2. **V8 版本偏旧 = 9.0.257.18（2021-04）**：sdk.js 是 2026 年 8 月版（`20260810913b`）。
   **第一步必须实测**：老 V8 能否无语法错跑通 sdk.js 的 `requirements`。
   - sdk.js 主体是基础 ES（Promise/Proxy/async），大概率兼容；
   - 但若用了 2021 后的 API（`Array.prototype.at` / `Object.hasOwn` / 新 RegExp flag / 新 Intl 数据）会报错。
   - **若实测不兼容 → 立即回退 Plan B（Node 常驻服务，见 §7），不要硬磕。**

---

## 2. 接口契约（这是与主线对齐的**唯一接缝**，必须严格遵守）

提供一个 Go 包（建议路径 `internal/service/signup/sentinel`，**复用/替换现有 goja 实现**，对外签名尽量不变），
暴露如下能力（命名可调，语义不可变）：

```go
// Solver 是 Sentinel 求解器。实现必须是并发安全的（内部用 Isolate 池）。
type Solver interface {
    // GetToken 求解一个 Sentinel token。
    // 入参 env 即「浏览器指纹的 JS 层画像」，由主线（fingerprint 包）喂入，保证与 TLS/UA 自洽。
    // 返回 (sentinelToken, soToken)。soToken 可能为空串（取决于服务端 challenge 是否要求 SO）。
    // 失败返回 error（网络类错误要能被上层归类为 network，不要吞成普通 JS 失败）。
    GetToken(ctx context.Context, env EnvPayload) (token string, soToken string, err error)

    // Close 释放全部 V8 资源（进程退出前调用）。
    Close() error
}
```

### EnvPayload（由主线提供，字段与 Python `_sentinel_fp_kwargs()` 完全对齐）
```go
type EnvPayload struct {
    DeviceID      string // oai-did
    Flow          string // "authorize_continue" / "username_password_create" / "create_account" / ...
    UserAgent     string // 与 HTTP 头完全同一个 UA 串
    ScreenWidth   int
    ScreenHeight  int
    Language      string   // 主语言, 如 "en-US"
    Languages     []string // Accept-Language 展开
    Platform      string   // "Win32" / "MacIntel" / "iPhone"
    Vendor        string   // "Google Inc." / "Apple Computer, Inc." / ""(firefox)
    HardwareConcurrency int
    DeviceMemory  *int     // 仅 Chromium 非 nil; Safari/Firefox 传 nil → JS 侧保持 undefined
    MaxTouchPoints int     // iOS=5, 其余=0
    DevicePixelRatio float64
    Timezone      string   // IANA 时区, 如 "America/New_York" (来自主线 GeoIP 联动)
    BrowserType   string   // "chrome" / "mac_safari" / "firefox" / "ios_safari"
    // 发起 /sentinel/req 用的 HTTP 会话凭据（见 §3 协作边界）
    ChallengeFetcher ChallengeFetcher
}
```

### ChallengeFetcher（HTTP 由主线负责，这里只定义回调）
`solve` 流程中段需要向 `https://sentinel.AI Platform.com/backend-api/sentinel/req` 发一次 POST
（body=`{"p":request_p,"id":device_id,"flow":flow}`），**这次 HTTP 必须用主线带 TLS 指纹+代理的会话发**，
不能在 v8go 里发（否则 TLS 指纹对不上）。因此：
```go
type ChallengeFetcher interface {
    // FetchChallenge 用主线会话发 /sentinel/req, 返回 challenge JSON（含 so 块）。
    FetchChallenge(ctx context.Context, requestP string, deviceID string, flow string) (map[string]any, error)
}
```
主线在构造 `EnvPayload` 时把当前注册的 session 包成 `ChallengeFetcher` 传入。

---

## 3. 协作边界（明确你「不做」什么）

| 模块 | 谁负责 | 说明 |
|---|---|---|
| TLS/HTTP 传输层（httpcloak） | **另一条线（并行）** | 你**不碰** session/proxy/TLS 指纹 |
| 浏览器指纹生成（fingerprint） | **另一条线（并行）** | 你**不生成**指纹，只**消费** `EnvPayload` |
| `/sentinel/req` 的真实 HTTP 发送 | **另一条线** | 你通过 `ChallengeFetcher` 回调触发，不自己发网 |
| sdk.js 的获取与版本落盘 | **你**（见 §4） | 复用主线的缓存目录约定 |
| sdk.js 沙箱环境垫片 + 两阶段求解 | **你** | 本任务核心 |

> 主线当前指纹/session 实现位于 `internal/service/signup/fingerprint` 与 `internal/service/signup/httpclient`（正被另一条线替换为 httpcloak）。你**只面向上面的 `EnvPayload`/`ChallengeFetcher` 接口编程**，不要 import 主线内部包，避免并行开发互相阻塞。联调时由主线把接口接上。

---

## 4. sdk.js 来源与缓存（约定）

- 版本写死：`SENTINEL_VERSION = "20260810913b"`（与 Python 对齐）。
- 下载 URL：`https://sentinel.AI Platform.com/sentinel/{VERSION}/sdk.js`。
- **本地已有真实样本**：`/Users/iceman/Documents/workspace/GPT-GO/internal/service/signup/sentinel/testdata/sdk.js`（33KB，可直接用于开发/测试）。
- 缓存目录约定：`<module_dir>/sdk/<version>/sdk.js`，命中即用、缺失才下载（与 Python `_ensure_sdk_file` 一致）。

---

## 5. 实现要点（参照 Python `.cjs`，翻译成 v8go）

Python 参照文件：`.../protocol_signup/_runtime/openai_sentinel_quickjs.cjs`（681 行）。
**不要逐行翻译**，按下面的「沙箱最小集」实现即可（sdk.js 真正读到的就这些）。

### 5.1 必做的 JS 环境垫片（沙箱最小集）
在 v8go Context 里注入（可用 Go 侧 `ObjectTemplate`/`FunctionTemplate`，或预置一段 JS shim）：
1. **navigator**:`userAgent/language/languages/hardwareConcurrency/platform/vendor/maxTouchPoints/webdriver=false/onLine/cookieEnabled/appVersion/product/vendorSub/connection/plugins/mimeTypes/getBattery/sendBeacon/permissions`。
   - `deviceMemory` **仅当 `EnvPayload.DeviceMemory != nil` 才挂**（Safari/Firefox 保持 undefined）。
   - **原型链补齐**：sdk 会用 `Object.keys(Object.getPrototypeOf(navigator))` 取方法名并 `toString()`，
     需让方法返回 `function xxx() { [native code] }` 形态（Python 用 `_nativeFn` 补 vibrate/canShare/share/javaEnabled/requestMediaKeySystemAccess/registerProtocolHandler/unregisterProtocolHandler）。
2. **时区（关键，最易翻车）**:
   - `process.env.TZ = EnvPayload.Timezone`（Node 专有；v8go 无 process，需伪造一个全局 `process={env:{TZ:...}}`）。
   - patch `Intl.DateTimeFormat.prototype.resolvedOptions`，强制 `timeZone = EnvPayload.Timezone`。
   - 目的：`new Date().toString()` 与 `Intl...resolvedOptions().timeZone` 都必须与代理出口时区一致，否则 OTP silent-drop。
3. **performance**:`now()/timeOrigin/memory.jsHeapSizeLimit`。
   - `jsHeapSizeLimit` 按 `DeviceMemory` 联动：约内存一半 + 0~256MB 随机抖动（Python `_jsHeapSizeLimit()`）。
4. **screen / window**:`width/height/availWidth/availHeight/colorDepth=24/innerWidth/outerWidth/devicePixelRatio/scrollX/scrollY/pageXOffset/pageYOffset`。
5. **document（最小）**:`readyState='complete'/cookie='oai-did='+encode(deviceID)/createElement('canvas'|'iframe')/getElementsByTagName('script')/currentScript/referrer/URL/location`。
   - **iframe + postMessage 是 requirements 拿 `capturedProof` 的关键通道**（见 Python `createElement('iframe')` 与 `MessageChannel` 逻辑）：sdk 通过 iframe.contentWindow.postMessage 回传 proof，需捕获为 `request_p`。
6. **其它**:`localStorage/sessionStorage`(Map 实现）、`crypto.getRandomValues/randomUUID`（含 `subtle` 若可得）、`TextEncoder/TextDecoder`、`atob/btoa`、`location.origin='https://auth.AI Platform.com'`、`fetch=()=>{throw}`（禁止真实发网）、`Event/CustomEvent/MessageChannel`、`addEventListener/dispatchEvent`。
7. **行为模拟（solve 前）**：派发 `pointermove×12~16 + click + scroll×3~4 + wheel + keydown('L','u','Tab')`，
   总时长受 `behavior_duration_ms`（默认 1200ms）控制（Python `dispatchBehavior`）。

### 5.2 两阶段求解流程（与 Python 完全一致）
```
requirements 阶段:
  1. vm 里跑 sdk.js（已加载）
  2. 调 SentinelSDK.init(flow)（沙箱里会超时,走 __debugP.getRequirementsToken() 兜底）
     或捕获 iframe postMessage 的 proof
  3. 输出 request_p

  → 主线用 ChallengeFetcher 发 /sentinel/req(request_p) 拿回 challenge

solve 阶段:
  1. 把 challenge + request_p + env 注入
  2. 调 SentinelSDK.token(flow)（或 __debugP.getEnforcementToken(challenge)）
  3. 跑行为模拟
  4. 调 SentinelSDK.sessionObserverToken(flow) 拿 so_token（可能为空）
  5. 输出 (token, so_token)
```
- **so_token 是否必须**：由服务端 challenge 里的 `so.required===true && typeof so.collector_dx==='string'` 决定。
  服务端没要求时 `so_token=""` 是**正常**的，**不要误判为失败**（对齐 Python `sentinel_quickjs.py` 的判定）。

### 5.3 sdk.js 需要的几处源码 patch（与 Python `.cjs` 顶部一致）
- `var SentinelSDK=` → `globalThis.SentinelSDK=`（暴露全局）
- `var P=new _;` → `var P=new _;globalThis.__debugP=P;`（暴露实例，兜底用）
- 暴露 token 的 patch（`EXPOSE_PATCH`/`EXPOSE_REPLACEMENT`，含 `__debug_n/__debug_bindProof`）
- 提速：`const Xn=5e3,` → `const Xn=+(globalThis.__SENTINEL_INIT_MS||5e3),`（可调 init 等待）

### 5.4 预编译缓存（核心诉求）
```go
// 首次:
script, _ := iso.CompileUnboundScript(sdkSource, "sdk.js", v8.CompileOptions{})
cache := script.CreateCodeCache()      // 落盘 <cache>/sdk/<ver>/sdk.v8cache
// 之后每次:
script, _ := iso.CompileUnboundScript(sdkSource, "sdk.js", v8.CompileOptions{CachedData: cache})
val, _ := script.Run(ctx)
```
- 缓存随 sdk.js 版本一起失效（版本变 → 重编译）。

---

## 6. 验收标准（Acceptance Criteria）

1. **语法兼容**：v8go 能加载真实 `sdk.js` 且 `requirements` 返回非空 `request_p`（用 §4 testdata 样本，不需联网）。
2. **单元测试**:
   - `TestRequirements_ReturnsRequestP`
   - `TestSolve_ReturnsToken`（可用录制/桩的 ChallengeFetcher 返回固定 challenge）
   - `TestSoTokenOptional_WhenServerNotRequire`（so 未要求时 soToken="" 且不报错）
   - `TestCodeCache_SkipsReparse`（第二次 Compile 命中缓存，可用计时就绪性/或注入计数器验证）
   - `TestNoLeak_RepeatedSolve`（循环 N 次求解，强制 Close/Dispose，RSS 增长有界）
3. **时区正确**：注入 `Timezone="America/New_York"` 后，沙箱内 `Intl.DateTimeFormat().resolvedOptions().timeZone==="America/New_York"` 且 `new Date().toString()` 含 `GMT-0[45]00`。
4. **内存**：连续 200 次求解后 RSS 增长 < 阈值（如 50MB），证明 Close/Dispose 生效。

---

## 7. Plan B（若 V8 9.0 跑不动新 sdk.js，立即回退，不要硬磕）

实现一个 **Node 常驻服务**（复用 Python `.cjs` 垫片，几乎零改动）：
- Go 侧 `os/exec` 启动**一个**常驻 `node sentinel_server.cjs`，启动时 `vm.runInContext(sdk)` 一次。
- 通过 **stdio 行协议 / Unix socket / 本地 HTTP** 传 `{action, env, challenge?}`，回 `{request_p|token,so_token}`。
- Go 维护连接池，单 token IPC 开销 ~5–10ms，**解析成本同样降到全局 1 次**。
- 接口对上层**完全等价**（同样实现 `Solver`），可随时切回 v8go。

---

## 8. 交付物清单

- [ ] `internal/service/signup/sentinel/` 下的 v8go 实现（`Solver` 接口 + 沙箱垫片 + 预编译缓存）
- [ ] sdk.js 缓存管理（含 `.v8cache` 落盘/失效）
- [ ] §6 全部单元测试通过（`go test ./internal/service/signup/sentinel/...`）
- [ ] 一份 `NOTES.md`：记录 V8 版本兼容性实测结论、内存治理策略、与主线联调需要的接口对接点
- [ ] **不动**主线 fingerprint/httpclient/authflow 的任何文件（并行开发）

---

## 9. 关键参照路径（只读，勿改）

| 内容 | 路径 |
|---|---|
| Python 求解编排 | `/Users/iceman/Downloads/苹果系统无限三代安装器/work/codex_audit/protocol_signup/_runtime/sentinel_quickjs.py` |
| Python JS 沙箱垫片（最重要） | `/Users/iceman/Downloads/苹果系统无限三代安装器/work/codex_audit/protocol_signup/_runtime/openai_sentinel_quickjs.cjs` |
| Python 对外入口 | `/Users/iceman/Downloads/苹果系统无限三代安装器/work/codex_audit/protocol_signup/_runtime/sentinel.py` |
| Python 指纹→sentinel 字段映射 | 同目录 `auth_flow.py` 的 `_sentinel_fp_kwargs()`（约 2136 行） |
| 本地 sdk.js 样本（测试用） | `/Users/iceman/Documents/workspace/GPT-GO/internal/service/signup/sentinel/testdata/sdk.js` |
| 待替换的 goja 旧实现（参照接口） | `/Users/iceman/Documents/workspace/GPT-GO/internal/service/signup/sentinel/` |

---

**交接完毕。开工前请先跑「验收标准 #1（语法兼容实测）」，它决定走 v8go 主路还是 Plan B。**
