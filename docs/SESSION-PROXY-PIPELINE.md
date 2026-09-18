# 会话型代理轮转注册管线（SESSION-PROXY-PIPELINE）

> **一句话**：在同国家前提下，每个账号用**不同的出口 IP** 注册；邮箱从池取、协议注册、成功落账号池。
> 本文档给审核者与后续接入的 AGENT 看：讲清楚「为什么这么设计 + 怎么接 + 红线 + 已知待办」。

---

## 0. 目标与约束（不可偏离）

| 目标 | 实现 |
|---|---|
| **同国家** | 每次注册拨号后**实测出口 IP 的国家**（GeoIP + CF trace），不是 VN 就重拨。国家**动态实测，绝不锁死**。 |
| **每账号不同 IP** | 会话型住宅代理 = 「IP 生成器」；每账号现场拨新 session → 实测出口 IP → **进程内注册表去重**，同 IP 重拨。 |
| **邮箱池 → 账号池** | `ReserveEmails` 预留 → 协议注册 → 成功 `account.Create` + 邮箱 `consume`；失败按错误码归还/标记。 |
| **协议与 codex-auto 一致** | authflow 10 步逐步对齐 `_runtime/auth_flow.py`；TLS↔UA↔SO 三路自洽走 core 地基。 |

---

## 1. 核心认知：会话型住宅代理 ≠ 静态代理池

你给的代理：

```
prem.country.iprocket.io:9595:com44840385-res-VN-Lsid-901244458-TTL-1800:PASS
sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-U0nM1dHxwW:PASS
```

- **host:port 固定**（服务商网关），**username 里嵌 session-id**，**改 session-id 就拨出不同出口 IP**。
- 所以**一条凭证 = 一个「可无限拨号的 IP 生成器」（通道）**，不是一个固定 IP。
- 10 条 iprocket + 10 条 ipipbright = **20 条独立拨号通道**，每条能拨出海量 IP。

> 这与 codex-auto 的「租一个静态代理、用后即焚」是**两种模型**。本项目走**模型 B：通道 = IP 生成器**。

### 会话标记（兼容两家，`model/proxy.go`）

| 厂商 | username 片段 | 正则 |
|---|---|---|
| iprocket | `-Lsid-<数字>` | `(?i)(-Lsid-)([0-9]+)` |
| ipipbright | `_session-<字母数字>` | `(?i)(_session-)([A-Za-z0-9]+)` |
| 其它 | 无 | → `SessionKindStatic`（静态，IP 固定） |

`model.ClassifySession(username)` 分类；`model.RewriteSession(username, kind, newID)` 替换 session-id（保留前缀，只改 id，session-id 限定 `[A-Za-z0-9]` 防注入）。

---

## 2. 架构总览（数据流）

```
                    ┌─────────────────────────────────────────────────────┐
   代理池(通道)      │  signup.Service.Run(ctx, RunParams)                  │
        │           │                                                     │
        ▼           │  1) acquireProxy  租一条通道(租约互斥)               │
  ProxyStore        │  2) reserveEmail  邮箱池预留                        │
   .AcquireProxy    │  3) dialForRun    【拨号】通道→新出口IP(本文件核心)  │
        │           │        └─ SessionDialer.Dial:                       │
        ▼           │           随机session→拼临时proxy→probe实测         │
  SessionDialer     │           →国家不符/同IP→重拨(≤4次)                 │
   .Dial            │  4) flowRunner    【authflow 10步】(注入,解包循环)  │
        │           │        └─ core.Bootstrap(三路自洽) + sentinel(V8)   │
        ▼           │  5) persistOnSuccess  落账号池+邮箱consume          │
  authflow.Flow     │  6) cleanup         按错误码分流代理/邮箱            │
   .RunRegister     │                                                     │
        │           └─────────────────────────────────────────────────────┘
        ▼
   账号池(account.Create) + 邮箱consume
```

### 涉及文件（本次新增/改动）

| 文件 | 作用 | 状态 |
|---|---|---|
| `internal/model/proxy.go` | 会话型分类 `ClassifySession` + 重写 `RewriteSession` + `SessionKind` | 新增 |
| `internal/service/proxy/probe.go` | `ProbeEgress`/`ProbeEgressOn`：实测出口 IP+国家+时区 | 新增 |
| `internal/service/signup/dialer.go` | **模型 B 核心**：`SessionDialer.Dial` 现场拨号 + 出口 IP 去重注册表 | 新增 |
| `internal/service/signup/egress_adapter.go` | `CDNProber`：把 proxy 探测适配给拨号器 | 新增 |
| `internal/service/signup/resource_adapter.go` | `ResourceAdapter`：邮箱池↔账号池桥接 + `PersistAccount` | 新增 |
| `internal/service/signup/proxy_adapter.go` | `ProxyServiceAdapter`：proxy.Service → signup.ProxyStore 桥接（签名/类型/ReturnProxy 映射） | 新增 |
| `internal/service/signup/assembly_test.go` | 端到端装配编译验证（§5 示例真能拼起来） | 新增 |
| `internal/service/signup/service.go` | 编排：租通道→拨号→authflow→落库→清理 | 重写 Run |
| `internal/service/signup/authflow/steps.go` | 10 步协议实现（对齐 auth_flow.py） | 新增 |
| `internal/service/signup/authflow/helpers.go` | uuid/随机姓名生日/嵌套JSON/sentinel头 | 新增 |
| `internal/service/signup/authflow/authflow.go` | `RunRegister` 主流程 + `NewWithEgress` | 重写 |
| `internal/service/signup/authflow/wire.go` | 装配层：`NewFlowRunner` + core fetcher 注入 solver | 新增 |
| `internal/service/signup/core/session.go` | `PostWithHeaders`：注入 sentinel/oai-device-id 额外头 | 新增方法 |
| `internal/service/signup/sentinel/v8_solver.go` | `SetFetcher`（运行期注入 core 会话 fetcher）+ 并发安全读 | 新增方法 |

---

## 3. 关键设计决策（为什么这么做）

### 3.1 国家「动态实测，不锁死」
- username 里的 `-res-VN`/`_area-VN` **只是「期望国家」提示**，用于租通道时粗筛。
- 住宅代理 IP 池是流动的，同一条通道今天拨 VN 明天可能拨别国。
- **所以国家不入库、不当通道固有属性**；每次拨号后用 `probe.ProbeEgress` 实测 `exit_ip` 的国家（GeoIP 优先，CF trace `loc=` 兜底），当次有效。
- 实测国家 ≠ 目标国家 → 换 session 重拨。
- 这也喂给 core：`BootstrapOptions.EgressIP` → GeoIP 解真实国家+时区 → 指纹时区/语言联动，**全用当次真实值**。

### 3.2 每账号不同 IP 的硬保证：出口 IP 去重注册表
- **改 session 不一定换 IP**（住宅代理同 session 粘性，如 ipipbright `life-5`=5 分钟保持；服务商也可能把多 session 调度到同一出口）。
- 所以拨号器内置 `exitIPRegistry`（对齐 codex-auto `_try_claim_exit_ip`）：进程内记录本批次已用的出口 IP + TTL，撞到就重拨。
- 单测已验证：同 IP 会自动跳到新 IP（`TestDialSameIPRedials`）。

### 3.3 sentinel challenge 必须与 TLS 会话同源
- `sentinel.DefaultChallengeFetcher` 走**旧 httpclient(tls-client)**——这会造成「sdk.js 画像是 chrome-148、challenge 请求却是另一个 TLS 指纹」的**三路裂缝**。
- 修复：`authflow/wire.go` 的 `coreChallengeFetcher` 用**当前注册的 core.Session** 发 `/sentinel/req`（复用同一 TLS 会话+cookie jar）。
- 注入时机：core.Session 每次注册才创建，故给 `V8Solver` 加 `SetFetcher`（并发安全），注册前 `injectCoreFetcher`。

### 3.4 service → authflow 解包循环
- authflow import signup 根包（用 Config/AuthResult/RegistrationError），故 signup 不能反向 import authflow。
- 解法：`signup.FlowRunner` 函数类型 + `WithFlowRunner` 注入；装配层调 `authflow.NewFlowRunner(solver)` 得到 FlowRunner。

---

## 4. 红线（codex-auto 血泪教训，已逐条落地）

| # | 红线 | 落地 |
|---|---|---|
| 1 | **device_id 三处同源**：oai-did cookie == oai-device-id 头 == SentinelEnv.DeviceID | `Bootstrap.DeviceID()` 从 cookie 读；`sentinelHeaders()` 把 `oai-device-id` 恒设为 deviceID；auth_oauth_init 拿不到 cookie 时写回 cookie |
| 2 | **重定向链去 sec-fetch-user**（302 自动跟随非用户点击） | `core.GetNavigationFollowRedirect`（删 sec-fetch-user + 按同站/跨站调 sec-fetch-site） |
| 3 | **中后段 TLS 错只原 session 重试不重建**（重建丢 cookie→409） | core.Session 内建原 session 重试；早期才允许 `RotateTLSIdentity`（且**仅同版本变体**，不跨大版本） |
| 4 | **sentinel SO 只在服务端本 flow 要求时带**（不拿别的 flow 凑数） | `sentinelHeaders()` so 为空不带；`username_password_create` flow 显式不传 SO |
| 5 | **oai-device-id 头必带于 auth.AI Platform.com 域** | `core.PostWithHeaders` + `sentinelHeaders()` 统一注入 |

---

## 5. 怎么接入（装配层示例）

```go
// 1) 存储
emailStore := store.NewMockEmailStore()      // 生产换 Mongo 实现
accStore  := store.NewMockAccountStore()
proxyStore := store.NewMockProxyStore()      // 生产换 Mongo 实现

// 2) 服务
emailSvc := email.NewService(emailStore)
accSvc   := account.NewService(accStore)
proxySvc := proxy.NewService(proxyStore)     // 实现 signup.ProxyStore

// 3) Sentinel V8 求解器（P4）
solver, _ := sentinel.NewV8Solver(sdkBytes) // sdkBytes 由 sentinel.SDKSource(ctx) 加载

// 4) 适配器（关键：proxy.Service 签名 ≠ signup.ProxyStore，必须经适配器桥接）
proxyPort := signup.NewProxyServiceAdapter(proxySvc)  // proxy.Service → signup.ProxyStore
adapter   := signup.NewResourceAdapter(emailStore, accSvc) // 邮箱池 + 账号池桥接
dialer    := signup.NewSessionDialer(signup.NewCDNProber())

// 5) 注册服务（注入 FlowRunner + 拨号器 + 适配器 + solver）
svc := signup.NewService(proxyPort, adapter, mailboxProvider,
    signup.WithDialer(dialer),
    signup.WithResourceAdapter(adapter),
    signup.WithSolver(solver),
    signup.WithFlowRunner(authflow.NewFlowRunner(solver)),
)

// 6) 跑一个号
res, err := svc.Run(ctx, signup.RunParams{
    Country: "VN", Group: "default", RunID: "run-001",
    EmailSource: "mailcode", OTPTimeout: 60, LeaseSeconds: 1800,
})
```

> **代理池导入**：把代理文本（每行 `host:port:user:pass`）经 `proxySvc.ImportProxies` 导入；
> 国家会从 username 的 `-res-VN`/`_area-VN` 自动推断（仅作粗筛提示，真实国家拨号实测）。
> 拨号时 session-id 会被随机替换（模型 B），**无需为每个 IP 单独导入一条**。

---

## 6. 测试

| 测试 | 覆盖 |
|---|---|
| `dialer_test.go`（7 项） | session 分类/重写、拨号替换 session、国家不符重拨、同 IP 重拨、重拨耗尽、静态通道 |
| `resource_adapter_test.go`（4 项） | 邮箱预留、成功落账号池+consume、失败归还、国家动态落库 |
| `core`（19 项） | 三路自洽、同版本轮换、platform-version 随机、GeoIP、SentinelEnv 同源 |
| `sentinel`（V8） | P4 的求解器测试（未回归） |
| `authflow/wire_smoke_test.go` | 装配层冒烟：V8Solver 构造 + SetFetcher 注入点 + FlowRunner 类型闭环 |

**全项目 `go build ./...` / `go vet ./...` / `go test ./...` 全绿（无 FAIL）。**

### 构建/测试的 build tag（重要）

- **默认构建**用 goja solver（无 cgo 重依赖）：`go build ./...`。
- **V8 主路**需 `-tags v8` + cgo（P4 NOTES.md §2）：
  ```sh
  # macOS
  GOTOOLCHAIN=auto CGO_ENABLED=1 go build -tags v8 ./...
  # Linux（V8 14.x 公开头用 libc++，必须 clang）
  CC=clang CXX=clang++ CGO_ENABLED=1 go build -tags v8 ./...
  # V8 测试（含 race，验证 SetFetcher 并发安全）
  GOTOOLCHAIN=auto CGO_ENABLED=1 go test -tags v8 -race ./internal/service/signup/sentinel/
  ```
- **装配冒烟真跑**（慢，~200s 真实 warmup 重试，默认跳过）：
  ```sh
  WIRE_SMOKE=1 GOTOOLCHAIN=auto CGO_ENABLED=1 go test -tags v8 -run TestNewFlowRunnerAssembles ./internal/service/signup/authflow/ -v
  ```
  已实测：装配链 `NewFlowRunner → core.Bootstrap → RunRegister → warmup` 走通，warmup 硬门槛按 codex-auto 语义正确返回 `warmup_failed`。

---

## 7. 已知待办 / 裂缝（审核重点）

| # | 项 | 状态 | 说明 |
|---|---|---|---|
| T1 | **OAuth Codex refresh_token** | ⏳ 留 TODO | `run_register` 末尾的 `oauth_codex_rt_exchange`（独立 authorize 链 + PKCE）未实现；骨架返回已含 session_token/access_token，refresh_token 列入下一版。 |
| T2 | **已有账号登录分支** | ⏳ 部分 | `RunRegister` 实现「新账号」全链路；`login_password`/`passwordless_login`/TOTP/add-phone 分支返回 `existing_account` 错误码由上层决策，未实现完整登录流。 |
| T3 | **邮箱预留原子性** | ⚠️ Mock 安全 | `ResourceAdapter.ReserveEmails` 用「List→SetStatus」两步 + 进程内 mutex；**多实例部署需 store 层 `findOneAndUpdate` 原子占位**（Mongo）。 |
| T4 | **纯净度复检（sliver)** | ⏳ 可叠加 | 拨号器现在只校验国家+IP去重；codex-auto 还查 CF `sliver` 脏代理。`probe.ProbeEgress` 可加 sliver 判定（脏→重拨），当前未启用。 |
| T5 | **sentinel 默认 fetcher 裂缝** | ✅ 已由 wire 修复 | `NewFlowRunner` 注册前注入 core fetcher；**若绕过 wire 直接用 solver，仍会走 tls-client 旧 fetcher**（别这么用）。 |
| T6 | **humanize/Datadog 头** | ⏳ 暂缓 | 行为节奏（human_action_pause）、Datadog trace 头未加；SDK 内行为模拟由 P4 solver 处理。 |
| T7 | **RestoreUsedProxies** | ⚠️ 适配器返回 0 | 耗尽兜底调了接口但 `ResourceAdapter` 不持有 proxy store；需装配层把 proxy store 的 `RestoreUsed` 接进 `ResourceStore.RestoreUsedProxies`。 |
| T8 | ~~proxy.Service → signup.ProxyStore 适配~~ | ✅ 已补 | `ProxyServiceAdapter` 桥接（方法名/excluded 类型/租约类型/ReturnProxy→ReleaseProxy 映射），`assembly_test.go` 编译验证通过。 |

---

## 8. 给审核者的快速验证清单

1. `go build ./... && go vet ./... && go test ./...` 全绿。
2. `dialer_test.go`：`TestDialSameIPRedials`（同 IP 跳新 IP）、`TestDialCountryMismatchRedials`（国家不符重拨）。
3. `resource_adapter_test.go`：`TestPersistAccountConsumesEmail`（落账号池+邮箱 consume）、`TestPersistAccountCountryDynamic`（国家动态）。
4. 拨号链路：`service.Run` 里 `dialForRun` → `SessionDialer.Dial` 实测出口 IP → `FlowRequest.EgressIP` → `core.NewBootstrap(EgressIP)`。
