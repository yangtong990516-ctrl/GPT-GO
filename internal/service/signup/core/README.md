# core — 注册基础骨架（TLS ↔ UA ↔ SO 三路自洽的唯一权威）

> 本包是 codex-auto(Python) `_runtime` 三件套（`fingerprint.py` + `http_client.py` + sentinel 指纹喂入）
> 在 GPT-GO(Go) 的对应实现，但按 2026-09 的最终决策做了两处关键升级：
>
> 1. **传输层从 tls-client 换成 httpcloak v1.7.2**（全家族 Chrome/Firefox/Safari/iOS，0-RTT/ECH/QUIC 更贴真机）。
> 2. **时区从「固定映射表」升级为「GeoIP(git-lfs GeoLite2-City.mmdb) 按出口 IP 解真实 IANA 时区」**。
>
> 本包是**地基**：被 `authflow`（协议状态机）调用，本身不感知业务、不碰邮箱/账号/数据库。

---

## 1. 一句话理解本包

> **产出并维系「同一个虚拟浏览器」的身份（Identity），并用 httpcloak 承载它的传输层。**

风控判断「你是不是真浏览器」看三个层面，本包的全部设计只为让这三层**指向同一台机器**：

| 层 | 内容 | 谁保证 |
|---|---|---|
| **① TLS** | TCP/TLS 握手的 JA3/JA4、HTTP/2 SETTINGS、QUIC、ECH、0-RTT | **httpcloak preset**（如 `chrome-148-windows`）。本包只传 preset 名，**不参与、也无法修改指纹细节**。 |
| **② HTTP** | `User-Agent` / `sec-ch-ua*` / `Accept-Language` / 头顺序 / `Sec-Fetch-*` | **本包**按 Identity 在每次请求时注入，且与 ① 自洽。 |
| **③ JS (SO)** | sdk.js 在沙箱里读到的 `navigator.*` / `screen` / `timezone` / `deviceMemory` | **本包**生成并经 `SentinelEnv` 交给 P4 的 v8go 沙箱。 |

**铁律（codex-auto 反复踩坑换来的教训）：**

> 三路必须自洽。UA 说 `Chrome/148`、`sec-ch-ua` 就不能说 `v=152`；`navigator.platform` 说 `Win32`，UA 就不能是 `Macintosh`；时区说 `America/New_York`，`Accept-Language` 就该是 `en-US`。任何两者对不上，CF/OpenAI 一眼假（OTP silent-drop / 403）。

---

## 2. 文件地图

| 文件 | 职责 |
|---|---|
| `presets.go` | httpcloak 预设池 + 每个预设对应的 HTTP/JS 层常量（UA 模板、sec-ch-ua、navigator 常量、硬件/屏幕候选池）。**传输层固定，②③ 常量与之一一对应。** |
| `identity.go` | `Identity` 结构（三路画像的单一事实来源）+ 装配逻辑（家族权重、UA 装配、语言联动、硬件抽取）+ 同族轮换。 |
| `languages.go` | 国家 → 语言池（对齐 Python `_COUNTRY_PROFILES.languages`），构建 `Accept-Language` 与 `navigator.languages`。 |
| `geoip.go` | GeoIP 解析器（git-lfs GeoLite2-City.mmdb），由出口 IP 解出 `CountryCode + IANA TimezoneID`。懒加载 + 热重载 + 线程安全。 |
| `session.go` | `Session` 封装 httpcloak：请求头注入（②）、socks5h 标准化、TLS 瞬断原 session 重试、cookie 访问。 |
| `factory.go` | `Bootstrap` 门面：把 `Identity + Session + GeoLocation` 绑成一体，供 `authflow` 直接消费；含 GeoIP 绑定与同族 TLS 轮换。 |
| `core_test.go` / `session_test.go` | 网络无关单测（三路一致性、时区语言联动、轮换、头注入、代理归一）。 |
| `smoke_live_test.go` | 联网冒烟（默认跳过，`CORE_LIVE=1` 启用）：tls.peet.ws 回显验证网线上的 TLS↔UA。 |

---

## 3. 快速上手（authflow 怎么用）

```go
import "gpt-go/internal/service/signup/core"

// ① 装配地基（每次注册开始时做一次）
boot, err := core.NewBootstrap(core.BootstrapOptions{
    Proxy:       "socks5://u:p@host:1080", // 自动标准化 socks5h
    CountryHint: lease.Country,            // 代理租约自带国家码（GeoIP 前的兜底）
    Vendor:      "iprocket",               // 代理厂商（作指纹 seed 盐）
    Timeout:     45 * time.Second,
})
if err != nil { /* 处理 */ }
defer boot.Session.Close()

// ② check_proxy / warmup 拿到出口 IP 后，绑定 GeoIP（时区/语言联动刷新）
boot.BindEgress(egressIP, nil)   // git-lfs GeoLite2 解出真实国家码 + IANA 时区

// ③ 用同一 Session 走协议（② 头注入由 Session 内部完成）
resp, err := boot.Session.GetNavigation(ctx, "https://chatgpt.com/")            // warmup 首个直达导航
resp, err = boot.Session.Get(ctx, csrfURL, "https://chatgpt.com/auth/login")    // XHR
resp, err = boot.Session.Post(ctx, url, referer, "application/json", body)      // POST

// ④ 到 sentinel 步骤，导出 ③ JS 层画像喂给 P4 沙箱（与 HTTP 层同源）
//    【红线】deviceID 必须来自 boot.DeviceID()（即 oai-did cookie），三处同源：
//    oai-did cookie == oai-device-id 请求头 == SentinelEnv.DeviceID，绝不各取各的。
deviceID := boot.DeviceID()
env := boot.Identity.SentinelEnv(deviceID, "authorize_continue")
// → 交给 P4 的 Solver.GetToken(ctx, env)

// ⑤ 重定向链上的导航用专用方法（去掉 sec-fetch-user，真浏览器自动跟随不发此头）
resp, err = boot.Session.GetNavigationFollowRedirect(ctx, nextURL, "cross-site")

// ⑥ TLS 瞬断且尚未种 cookie 时，同版本轮换（仅换 TLS 一致的同版本变体，不跨大版本）
ok, err := boot.RotateTLSIdentity(proxy, 45*time.Second, nil)
```

---

## 4. 设计决策（为什么这么做）

### 4.1 为什么用 httpcloak 而不是继续用 tls-client
- httpcloak v1.7.2 提供 **100 个预设**，全家族覆盖（Chrome 133~152、Firefox 133/148、Safari 17/18、iOS），且每个预设的 JA4/peetprint/Akamai 哈希与真机逐项核对（README 实测）。
- 独有 **0-RTT session 复用 / ECH / QUIC / TCP 指纹**，比 tls-client 更贴真机（bot score 实测 43→99）。
- 已实测在本项目可编译、可联网（见 `smoke_live_test.go`）。
- **注意**：httpcloak 要求 `go >= 1.26`，本项目用 `GOTOOLCHAIN=auto` 自动拉取工具链（`go.mod` 已升至 1.26）。

### 4.2 为什么时区用 GeoIP 而不是固定映射表
- Python 版用「国家码 → 固定时区表」近似（如 US 固定 4 个时区加权）。
- 本包升级为：**由出口 IP 直接查 GeoLite2 解出真实 IANA 时区**（`8.8.8.8 → America/Chicago`），更准、免维护映射表。
- 数据源是 git-lfs 的 `GeoLite2-City.mmdb`（~67MB），经符号链接接入 `data/geoip/`。
- **降级**：DB 缺失（git-lfs 未拉、仍是 ~133B 指针）时，`LookupGeo` 返回 nil，时区回退到国家主时区、语言回退到语言池，绝不崩。

### 4.3 为什么"一次注册一个 session、不共享"
- httpcloak.Session 非并发安全；且 codex-auto 的教训是「同号全程同 session 同出口」。
- 批量并发 = 多号各自持有独立 `Bootstrap`（独立 Identity + Session + 出口），互不共享。

### 4.4 为什么 TLS 瞬断「原 session 重试」而不是「重建」
- Python `_TlsRetrySession` 的核心经验：链路中后段 session 里已装 warmup 的 `oai-did` 和 csrf，
  一重建就全丢 → 409 invalid_state。所以 `session.go` 只对 TLS 握手错做**原 session 重试**（最多 2 次，退避 1.5s）。
- 只有**早期步骤**（尚未种 cookie）才允许 `RotateTLSIdentity` 轮换。

### 4.5 为什么轮换只允许「同版本变体」而不是「跨大版本」（2026-09 审计修正）
- httpcloak 的 TLS 指纹在**建会话时已定死**（① 不可变）。若轮换把 Identity 换成另一个 Chrome 大版本
  （如会话是 chrome-148 的 TLS，UA/sec-ch-ua 却换成 chrome-152），就成了「TLS 握手说 148、UA 说 152」的
  自相矛盾——这是 codex-auto 在 `auth_flow.py:1706` 警告的同一类坑，会被 CF 一抓一个准。
- 所以 `RotateSameFamily` 只挑**同主版本**的候选（如 `chrome-148` ↔ `chrome-148-windows`，共享同一 TLS 指纹）。
- **跨大版本不可轮换**：那种情况按 codex-auto 语义只能「换新 session 重试」（重试只换出口 IP，不换指纹）。

### 4.5 为什么预设池要"受控"而非全用 httpcloak 的 100 个
- 池内每个预设的 UA/sec-ch-ua/navigator 常量是**人工与真机核对过的**（尤其 Chrome 的 `notABrand` 串随版本变：136→`Not.A/Brand;v=99`、142→`Not/A)Brand;v=8`、143+/148→`Not A(Brand;v=24`）。
- `NewSession` 对未知 preset **构造期即报错**，杜绝 httpcloak 作者警示的「拼错一个字符换回旧指纹」事故。
- 扩池 = 在 `presets.go` 的 `presetPool` 加一条核对过的 `PresetSpec` 即可。

---

## 5. 与其他模块的接缝（并行开发边界）

| 模块 | 关系 | 接缝 |
|---|---|---|
| **authflow**（本仓库） | **消费方** | 只面向 `core.Bootstrap` / `core.Session` / `core.Identity` 编程；不再 import 旧的 `fingerprint` / `httpclient` 包。 |
| **P4 sentinel v8go**（另一条线） | **被喂数据** | 经 `Identity.SentinelEnv(deviceID, flow)` 拿到 ③ JS 层画像；P4 把它注入 v8go 沙箱。**本包不 import P4，P4 也不 import 本包内部**，只认 `SentinelEnv` 这个 DTO。 |
| **proxy 包** | **参照** | 本包的 GeoIP 逻辑与 `proxy/geoip.go` 同构但独立（proxy 那份未导出、服务于体检）。 |
| **旧 `signup/fingerprint` + `signup/httpclient`** | **被替代** | 本包是其 httpcloak + GeoIP 升级版；authflow 接入本包后，旧两包可退役（待 authflow 改造时一并清理）。 |

---

## 6. 测试

```bash
# 网络无关单测（默认全跑）
GOTOOLCHAIN=auto go test ./internal/service/signup/core/

# 联网冒烟（tls.peet.ws 回显指纹，验证网线上 TLS↔UA）
CORE_LIVE=1 GOTOOLCHAIN=auto go test -run TestLiveFingerprintSmoke -v ./internal/service/signup/core/
# 带代理验证：
CORE_LIVE=1 CORE_LIVE_PROXY="socks5://u:p@host:1080" GOTOOLCHAIN=auto go test -run TestLiveFingerprintSmoke -v ./internal/service/signup/core/
```

当前覆盖：三路一致性（200 种子）、时区/语言联动、GeoIP 真实解析、同族轮换、头注入形态、代理归一、构造期校验、SentinelEnv 同源。

---

## 7. 已知边界 / TODO

- [ ] **authflow 尚未接入本包**（仍引用旧 `fingerprint`+`httpclient` 骨架）。下一步把 `authflow.New()` 改为消费 `core.NewBootstrap`。
- [ ] `oai-device-id` 头、Datadog trace 头、humanize 请求节奏：属于 P5（行为细节对齐），**暂缓**，待主链路跑通后统一补。
- [ ] httpcloak 的 0-RTT session 持久化（`session.Save/Load`）尚未启用，可在 warmup 后接入以提升 bot score（属增强，非必须）。
- [ ] GeoIP 与 proxy 包的 geoip.go 存在逻辑重复，未来可抽取公共 `internal/geoip` 包统一（当前为低耦合各自独立，刻意为之）。

---

**给后续 AGENT 的话**：改本包前，先读懂 §1 的「三路自洽铁律」。任何让 UA、sec-ch-ua、navigator、时区、语言各自为政的改动，都是在给风控送特征。扩预设只动 `presets.go`；改装配只动 `identity.go`；改传输只动 `session.go`；接缝只动 `factory.go`。
