# 坏代理自动切换（Cloudflare sliver 预检）机制研究

> 来源：codex-auto-register-macos 逆向（`app/backend/protocol_signup/service.py`、`app/backend/main.py`）
> 目的：在真正发起注册/登录/补2FA 前，先用一次廉价请求探出代理出口 IP 的 Cloudflare 风控分，脏的提前换掉，避免拿毒代理白跑完整协议链路。

---

## 1. 核心思想

代理池里的住宅/机房代理质量参差。同一个出口 IP，Cloudflare 对它的"信任度"不同：
- 干净 IP → 协议链路顺畅
- 脏 IP（被 CF 打过标）→ warmup 拿不到 `oai-did`、authorize 409 invalid_state、弹 CF challenge、注册/登录直接被拦

**与其让脏代理在完整协议链路里失败（一次注册要跑 warmup→csrf→signin→authorize→sentinel→OTP→callback 十几步，浪费几十秒 + 一个邮箱），不如花 ~1 秒先探一下，脏的当场换掉。**

---

## 2. sliver 是什么 —— Cloudflare 给的"脏度分"

ChatGPT 全站在 Cloudflare 后面。CF 暴露一个调试端点：

```
GET https://chatgpt.com/cdn-cgi/trace
```

任何请求经过它，都返回一段 `key=value` 纯文本，关键两行：

```
sliver=none          ← 出口 IP 的风控等级
ip=203.0.113.7       ← 出口 IP 本身
```

| sliver 值 | 含义 | 处置 |
|---|---|---|
| `none` | 干净，CF 未打标 | ✅ 拿去用 |
| `xxx-tierN`（如 `8000100-tier1`） | 脏，CF 标了风控等级 | ❌ 换掉 |
| 空 / 请求异常 | 代理连不通 / trace 失败 | ❌ 换掉（判不干净） |

**一次 GET 同时取回 sliver + 出口 IP 两个值**，成本极低（不走完整协议，只一次 trace）。

### codex 探测实现（service.py:34-62）

```python
async def _probe_trace(proxy_url, timeout=8.0):
    async with httpx.AsyncClient(proxy=proxy_url, timeout=..., trust_env=False,
                                 follow_redirects=True) as client:
        resp = await client.get("https://chatgpt.com/cdn-cgi/trace")
        sliver = re.search(r"(?m)^sliver=([^\s]+)\s*$", resp.text)
        ip     = re.search(r"(?m)^ip=([^\s]+)\s*$", resp.text)
        return (sliver.group(1).strip() if sliver else "",
                ip.group(1).strip() if ip else "")
    # 任何异常 → return "", ""

async def _probe_sliver(proxy_url, timeout=8.0):
    sliver, _ = await _probe_trace(proxy_url, timeout)
    return sliver
```

要点：
- `trust_env=False` —— 不读系统代理环境变量，强制走指定代理（否则探测的是本机出口，误判）。
- 超时收紧（connect ≤ 8s）—— 连不通的代理快速判负。
- **任何异常都返回空串**，调用方把空串当"不干净"处理（宁可错杀，不放行可疑代理）。

---

## 3. 预检 + 换代理循环

### 3.1 补2FA / 换绑这类「无状态单次操作」（main.py:2071，最多试 6 个）

```
for attempt in range(6):
    lease = acquire_proxy(owner=f"ensure-2fa:{account}:{attempt}", lease_seconds=240)
    proxy_url = build_url(lease)
    clean, sliver = _rebind_proxy_sliver_clean(proxy_url)
    if not clean:
        log(WARN, f"第{attempt+1}个代理不干净 sliver={sliver}，换干净 IP")
        continue                       # 换下一个
    try:
        result = ensure_totp(account, proxy_url)   # 干净代理跑业务
        return success(result)
    except AccountSecurityError as e:
        if "timeout" in e.message:                 # 超时类才换代理重试
            continue
        return failed(e)                           # 业务失败(密码错等)不换代理
    finally:
        consume_proxy(lease.id)                    # 用完即隔离(见 §4)
return failed("no_clean_proxy", "无可用干净代理")
```

判定函数（main.py:2746）：

```python
async def _rebind_proxy_sliver_clean(proxy_url):
    sliver = await _probe_sliver(proxy_url, timeout=8.0)
    if not sliver:                 # 探测失败 = 连不上 → 不干净
        return (False, "")
    return (sliver.lower() == "none", sliver)   # 只有 none 才算干净
```

### 3.2 注册主链路（service.py:260，更克制：网络类失败才换，且只重试 1 次）

注册中途换 IP 有状态成本（cookie / oai-did / csrf 全在一个 session 里），所以注册不像补2FA 那样放开换 6 次，而是：

```
for attempt in range(2):           # 最多 2 次
    # 先租一个 sliver 干净的代理(内层小循环,见下)
    result = flow.run_protocol_*(...)
    on Exception as exc:
        code = classify_failure(exc)
        if attempt == 0 and code in {"warmup_failed", "network"}:
            # 首轮且是"出口 IP 被 CF 拦 / TLS 瞬断" → 剔除坏代理换一次
            release_proxy(lease.id); continue
        # 非首轮 or 非网络类 → 真失败,抛出(先释放代理防泄漏)
        raise
```

内层「租干净代理」小循环（service.py:263-282）：最多试 `pool_size+2` 个，逐个 `_probe_sliver`，脏的 `release` 后 `continue`，直到租到干净或池空：

```python
for _ in range(max(1, pool_size) + 2):
    lease = acquire_proxy(run_id, excluded_ids=excluded_ids, ...)
    if lease is None: break
    excluded_ids.add(lease.id)               # 关键:防重租
    sliver = await _probe_sliver(proxy_url_from(lease))
    if sliver and sliver != "none":
        release_proxy(lease.id); lease=None; continue   # 脏 → 换
    break                                    # 干净 → 用
if lease is None: raise NoEligibleProxyError()
```

---

## 4. 三个关键设计细节

### 4.1 excluded_ids 防重租
换代理时把已试过的坏代理 id 加进 `excluded_ids` 集合，下次 `acquire_proxy` 带上它，**保证不会反复租到同一个脏 IP**（否则死循环）。租约系统按 id 排除。

### 4.2 释放分两种语义（consume vs release/return）
| 操作 | 语义 | 何时用 |
|---|---|---|
| `consume_proxy` | **隔离**，标记该代理坏的，不再放回池 | 网络失败 / sliver 脏 / 补2FA 用完 |
| `release_proxy` / `return_proxy` | **放回池**，还能被别人用 | 业务失败（非代理问题）/ 主动换干净 |

补2FA 统一 `consume`（宁可错杀：一个代理只要让它失败过就拉黑，避免下个账号又踩）。注册里网络类用 consume、其他失败用 return。

### 4.3 只对「网络/超时/脏」换代理，业务失败不换
- **换**：`warmup_failed`（没种到 oai-did）、`network`（TLS 瞬断/连不上）、`sliver 脏`、业务里的 `timeout`
- **不换**：密码错误、账号不存在、`recent_auth_required`、OTP 错——这些是**账号/业务问题，不是代理问题**，换代理无济于事还浪费配额。

判据是错误分类（`_classify_failure`），不是所有异常都无脑换。

---

## 5. 落到 GPT-GO 的映射

GPT-GO 已有全部零件：

| codex 概念 | GPT-GO 对应 |
|---|---|
| `_probe_sliver(proxy_url)` | 新增：用代理发 `GET https://chatgpt.com/cdn-cgi/trace` 解析 sliver。可走 `core.NewSession`（同协议指纹）或独立 http.Client（挂 proxy dialer） |
| `acquire_proxy(excluded_ids)` | `proxysvc.AcquireProxy(ctx, owner, excluded map[string]bool, ...)` |
| `consume_proxy` / `release_proxy` | `proxysvc` 的 consume/release（需确认方法名，补2FA 目前用 ReleaseProxy） |
| 换代理循环 | 包在 `accountsecurity.EnsureOne`（补2FA）和 `signup` 注册触发的代理选取处 |
| 出口 IP | `Bootstrap.Geo`（注册拨号阶段已测 exit IP，可顺带校验一致性） |

**改动集中在服务层的代理选取处，不动协议链路。** 补2FA（无状态）适合放开重试 N 次；注册（有状态）保持克制、网络类失败才换 1 次。

---

## 6. 一句话总结

> **sliver 预检 = 拿代理去打一次 CF 的 `/cdn-cgi/trace`，看 CF 给这个出口 IP 打没打风控标；打了标（非 none）或连不上就当场换掉，只让干净代理进入真正的协议链路；配合 excluded_ids 防重租、consume 隔离坏代理、只对网络/脏类失败换代理这三条规则，把"代理有毒"这一类失败在入口处消灭。**
