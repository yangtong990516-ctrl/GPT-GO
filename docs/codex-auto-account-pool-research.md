# codex-auto 账号管理调研报告（供 GPT-GO 账号池参考）

调研对象：`/Users/iceman/Documents/workspace/codex-auto-register-macos`（主）
与 `codex-auto-register-sanitized-final` 结构同源，本报告以 macos 版为准。

技术栈速览：前端 Vue3 + Element Plus（`app/src`），主后端是 **Python FastAPI**
（`app/backend/main.py`，4471 行，挂所有 `/api/*` 路由）；`backend-go/` 只有一个
极薄的 Go 服务（健康检查 + Mongo manager），**账号管理逻辑全部在 Python 后端**。
GPT-GO 是 Go+React，照搬时对应关系：FastAPI handler → Go handler，pydantic model → Go struct。

---

## 1. 账号模型字段（AccountRecord）

定义文件：
- 后端 pydantic：`app/backend/resource_models.py` → `class AccountRecord`（L63-140）
- 前端 TS 镜像：`app/src/types/index.ts` → `interface AccountRecord`（L2-98）

### 1.1 完整字段清单（按业务分组）

**身份与凭据（核心）**

| 字段 | 类型 | 含义 |
|---|---|---|
| `id` | string | Mongo `_id` |
| `email` | string | 邮箱（注册时 lowercase 归一化） |
| `chatgptPassword` | string | ChatGPT 登录密码（明文存储，前端默认遮罩） |
| `totpSecret` | string | TOTP 2FA 密钥（base32） |
| `totpStatus` | string? | `enabled`/`orphaned`（服务端启了但本地没密钥）/`dirty`（本地有密钥服务端没启）/`missing` |
| `totpSecretConfigured` | bool | 密钥是否已落库 |
| `passwordStatus` | string? | `missing` 等（前端列里用到，后端靠 `chatgptPassword` 是否为空推断） |
| `passwordVerified` | bool? | 密码是否已被一次成功登录验证过 |
| `emailAccessUrl` | string | 接码/邮箱访问 URL（用于收 OTP） |

**Token / 登录态**

| 字段 | 类型 | 含义 |
|---|---|---|
| `accessTokenConfigured` | bool | AT 是否已配置 |
| `accessTokenExpiresAt` | datetime? | AT 过期时间（前端据此判 valid/expired） |
| `accessTokenUpdatedAt` | datetime? | AT 最近刷新时间 |
| `refreshToken` | string? | RT，用于秒级刷新 AT（OAuth refresh_token 流程） |
| `accessTokenMissing` | bool | 账号已建但 AT 没拿到（待补 AT），区别于过期 |
| `atRefillStatus` | string? | 补 AT 结果：`failed` / `add_phone_required` |
| `atRefillError` / `atRefillErrorAt` | string?/any | 补 AT 失败详情 |

> 注意：实际 `accessToken` 和 `deviceId` 存在 Mongo 里但不外传给前端列表（只在
> claim_* 投影里取，见 `resource_service.py` L1159-1165）。

**订阅 / 优惠资格（plan check 产出）**

| 字段 | 类型 | 含义 |
|---|---|---|
| `accountType` | `plus`\|`free` | 账号类型（plan check 后会自动纠正） |
| `planCheckStatus` | `running`\|`success`\|`failed`? | 优惠查询进行中标记（防并发重复查） |
| `planCheckedAt` / `planCheckErrorCode` / `planCheckHttpStatus` | — | 查询时间/错误码/HTTP 状态 |
| `planAccountId` | string? | ChatGPT 侧 account_id |
| `subscriptionPlan` | string? | 如 `chatgptplusplan` |
| `hasActiveSubscription` | bool? | 是否有生效订阅 |
| `planExpiresAt` / `planRenewsAt` | datetime? | 订阅到期/续费时间 |
| `promotionEligible` | bool? | **优惠资格核心字段**：true=可试用 |
| `promotionCampaignId` | string? | 首个优惠 campaign id |
| `promotionCampaigns` | array | 全部优惠套餐 `[{plan,id,promotion_type,title}]`（排除 go 套餐） |

**Checkout 类型 / 支付方式探测**

| 字段 | 含义 |
|---|---|
| `checkoutType` / `checkoutTypeDetail` | `oaics`/`cs` 及细分（`stripe_cs_live` 等） |
| `checkoutTypeCheckStatus` / `checkoutTypeCheckedAt` / `checkoutTypeErrorCode` / `checkoutTypeHttpStatus` | 同上 running 标记组 |
| `payMethodCheckStatus` / `payMethodCheckedAt` / `payMethodCountry` / `payMethodCurrency` / `payMethodList[]` / `payMethodHasMomo` / `payMethodHttpStatus` / `payMethodErrorCode` | 支付方式探测结果组 |

**多国试用扫描（trial scan）**

| 字段 | 含义 |
|---|---|
| `trialScanCheckedAt` / `trialScanError` | 扫描时间/错误 |
| `trialScanCountries` | `Record<CC, {eligible, state, signal, paymentMethods[], error}>` 逐国结果 |

**注册来源 / 验活**

| 字段 | 含义 |
|---|---|
| `registrationCountry` | 注册国家（两位码，筛选维度） |
| `registrationIp` / `registrationIsp` | 注册出口 IP / ISP |
| `registrationProxyId` | **注册时绑定的代理 id**（验活/查优惠时优先复用） |
| `aliveStatus` | `running`\|`alive`\|`dead`\|`unknown`?（验活状态） |
| `aliveCheckedAt` / `aliveErrorCode` / `aliveHttpStatus` | 验活时间/错误码/HTTP 状态 |

**全代理优惠扫描 / OAICS 扫描（重型后台任务）**

| 字段 | 含义 |
|---|---|
| `globalPromotionStatus` / `globalPromotionEligible` / `globalPromotionCheckedAt` / `globalPromotionProxyCount` / `globalPromotionCountries[]` / `globalPromotionResults[]` / `globalPromotionMessage` | 用池内全部代理逐国扫优惠资格 |
| `oaicsScanStatus` / `oaicsScanCheckedAt` / `oaicsScanTotal` / `oaicsScanSuccess` / `oaicsScanDone` / `oaicsScanCountryStats[]` / `oaicsScanResults[]` / `oaicsScanMessage` | OAICS 全代理检测进度与结果 |

**换绑（rebind）与备注**

| 字段 | 含义 |
|---|---|
| `rebindStatus` | `in_progress`/`success`/`email_changed_token_pending`/`failed` |
| `rebindProxy` / `rebindProxyCountry` / `rebindMailboxId` / `rebindTargetEmail` | 换绑用代理与目标邮箱 |
| `previousEmail` / `reboundEmail` | 换绑前后邮箱 |
| `remark` | string?（≤2000 字）人工备注 |

**其它**

| 字段 | 含义 |
|---|---|
| `createdAt` | 注册时间（前端据此判“新号”：<24h 打 new 标） |
| `phoneBound` | bool? 是否绑过手机 |

> “最近登录”字段没有独立存储；近似物是 `accessTokenUpdatedAt`（最后一次刷新/登录拿 AT 时间）。

### 1.2 GPT-GO 照搬建议（最小可用集）

```
email, password, totpSecret, totpStatus, emailAccessUrl,
accessToken(私有), accessTokenConfigured, accessTokenExpiresAt, refreshToken,
accountType, promotionEligible, promotionCampaigns[], planCheckStatus, planCheckedAt,
aliveStatus, aliveCheckedAt, aliveErrorCode,
registrationCountry, registrationIp, registrationProxyId,
remark, createdAt
```
每个“异步检查类”字段都遵循同一三件套模式：`<x>Status(running/success/failed)` +
`<x>CheckedAt` + `<x>ErrorCode` (+`<x>HttpStatus`) —— 这套模式值得整套照搬。

---

## 2. 账号「详细信息」面板

codex-auto **没有独立的账号详情页/抽屉**，所有信息都在表格行 + 悬浮 tooltip/popover
里展示（`app/src/views/AccountsView.vue`）。单账号可见信息的完整清单（即可作为
GPT-GO 详情面板的字段分组）：

**表格列（L1132-1245）**
1. 选择框
2. **邮箱**（mono 字体，tooltip 全量）
3. **账号类型**：Plus / Free 胶囊
4. **国家**：`registrationCountry` 转中文名
5. **注册IP**：`registrationIp`
6. **优惠资格**：`promotionLabel` — 查询中/查询失败/具体 campaign 名（如“免费试用 1 个月”）/可试用 ×N/不可试用/未查询；tooltip 列全部 campaigns + `planCheckedAt` + 错误码（L208-222）
7. **试用扫描**：`trialScanLabel` — “有试用 N 国/无试用/未知 N 国”；点击 popover 出**逐国卡片墙**（国家码、中文名、有/无试用、支付方式 chips）（L1162-1190）
8. **验活**：`aliveLabel` — 检测中/正常/已失效/检测异常/未检测；tooltip 含 `aliveCheckedAt`+`aliveHttpStatus`+`aliveErrorCode`
9. **AT状态**：已配置/已过期/需绑手机/补AT失败/缺失/未配置；tooltip 显示 `accessTokenExpiresAt` 与 `atRefillError`
10. **2FA**：已启用/缺失/未启用（`totpSecret` + `totpStatus` 推断）
11. **换绑**：未换绑/换绑中/已换绑/邮箱已换·AT待更新/换绑失败
12. **备注**：两行截断展示
13. **操作列**：提取AT / 补AT / 备注 / 接码（打开 emailAccessUrl）/ 更多下拉（刷新AT、检测2FA状态、验证2FA、补密码+2FA、验活、查优惠、送去待换绑、导出）

**行内单账号操作下拉（L1230-1241）** — 相当于详情操作组：
`refresh-at`（RT 换新，需有 refreshToken）、`check-2fa`（查 mfa_flag_enabled）、
`verify-2fa`（本地密钥生成验证码验证）、`ensure-2fa`（补密码+2FA）、
`check-alive`、`check-promotion`、`enqueue-rebind`、`export`

**页面顶部统计卡（StatCard）**：账号总数 / 今日新增 / TOTP 完整数 / 可导出数
（`OverviewStats.accounts`：`total, today, totpComplete, plus{total,bound,unbound}, free{total,eligible,ineligible}`）

---

## 3. 批量操作功能（前端按钮 → API → 展示）

前端批量按钮区：`AccountsView.vue` L1104-1120（`handleBatchAction` L699-741 分发）。
数据源封装：`app/src/services/dataGateway.ts`。状态在 `app/src/stores/app.ts`。

### 3.1 三个点名功能

#### (a) 批量验活 — “验活”
- 按钮：L1106 `验活`；单行下拉也有 `check-alive`
- 前端：`checkAlive(ids)` L397-413；**单次上限 100**；可选代理下拉 `promotionProxyId`
- API：`POST /api/accounts/check-alive`
- 请求：`{ ids: string[1..100], proxyId?: string }`
- 响应 `AccountAliveCheckResult`：
  `{ requested, alive, dead, failed, skipped, items: [{id, status: alive|dead|failed|skipped, errorCode}] }`
- 进度/结果展示：**同步等待**（按钮 loading），完成后 `ElMessage` 汇总
  “验活完成：正常 X，失效 Y，异常 Z，跳过 W”；列内 `aliveStatus` 胶囊实时反映；
  运行中状态由后端先写 `aliveStatus=running`（乐观锁 claim），前端刷新即见“检测中”。

#### (b) 批量查询优惠资格 — “查询优惠资格”
- 按钮：L1105 `查询优惠资格`
- 前端：`checkPromotions(ids)` L415-435；单次上限 100
- API：`POST /api/accounts/check-promotion`
- 请求：`{ ids, proxyId? }`
- 响应 `AccountPlanCheckResult`：
  `{ requested, succeeded, failed, skipped, items: [{id, status: success|failed|skipped, errorCode, promotionEligible, promotionCampaigns[]}] }`
- 展示：同步等待 + ElMessage 汇总；列内“优惠资格”胶囊显示具体 campaign 名称
  （`CAMPAIGN_LABELS` 映射 L170-180：plus-1-month-free→免费试用 1 个月 等）。

#### (c) 批量补 2FA — “批量补密码+2FA”
- 按钮：L1108 `批量补密码+2FA`
- 前端：`bulkEnsureAccount2fa()` L488-503
- API：`POST /api/accounts/bulk-ensure-2fa`
- 请求：`{ ids: string[1..10000] }`
- 响应：`{ requested, succeeded, failed, skipped, concurrency, results: [{id,status,errorCode?,passwordConfigured?,totpStatus?}] }`
- 展示：同步等待 + ElMessage（含并发数）；**另有实时日志浮窗**（L1272-1303）：
  右下角固定面板，每 3s 轮询 `GET /api/account-security/logs`（最近 300 条内存环形缓冲），
  条目含 `time/level(SUCCESS/ERROR/WARN/INFO)/message/accountId/step/source`，
  可最小化/清空（`POST /api/account-security/logs/clear`）。
- 单账号配套 API：
  - `POST /api/accounts/{id}/2fa-status` → `{mfaFlagEnabled, totpSecretConfigured, totpStatus, message}`（查服务端 mfa 状态并回写）
  - `POST /api/accounts/{id}/verify-2fa` → `{valid, code, secretConfigured, error}`（本地密钥生成 6 位验证码）
  - `POST /api/accounts/{id}/ensure-2fa` → 单账号补密码+2FA

### 3.2 其它批量操作（同一按钮区）

| 按钮 | API | 请求 | 响应 | 说明 |
|---|---|---|---|---|
| 批量补 AT（重新登录） | `POST /api/accounts/refill-at` | `{ids?}` 空=全部待补 | `{total,succeeded,failed,skipped}` | 走完整登录流程（邮箱 OTP），20-60s/个，有确认弹窗 |
| 提取 AT | `POST /api/accounts/export` | `{format:"access-tokens",scope:"selected",ids}` | `{content,filename,count,skippedMissingCount,skippedExpiredCount}` | 导出对话框 ExportDialog |
| 刷新 AT | `POST /api/accounts/refresh-at` | `{ids?, near_expiry_hours?}` | `{total,succeeded,failed,skipped,items[{id,email,status,errorCode,permanent}]}` | 用 refresh_token 秒级换新，无需 OTP |
| 刷新过期 AT | `POST /api/account-rebind/access-tokens/refresh-expired` | `{accountIds,proxyMode:"auto",emailSource:"standard"}` | `{started}` | 走换绑管线后台任务 |
| 送去待换绑 | `POST /api/account-rebind/tasks` | `{accountIds}` | `{items[]}` | 入换绑队列 |
| AT 分组复制 | （纯前端组件 AccessTokenGroupsDialog） | — | — | 按组复制 token |
| 一键复制 AT | `POST /api/accounts/export`（scope=all, format=access-tokens）→ 前端剪贴板截断 | 同导出 | 同导出 | `copyAtLimit`（localStorage `autoregister.copyAtLimit`，默认100） |
| 批量添加备注 | `POST /api/accounts/bulk-remark` | `{ids, remark}` | `{matched, remark}` | 空串清除；单个用 `PUT /api/accounts/{id}/remark` |
| 删除选中 | `POST /api/accounts/bulk-delete` | `{ids}` | `{deleted}` | 有确认弹窗 |
| 导出选中/全部 | `POST /api/accounts/export` | `{format: credential\|mail-links\|access-tokens, scope, ids}` | TextExport | 三种格式 |

另有两个**后台任务型**批量检查（立即返回、轮询进度）：
- `POST /api/accounts/check-global-promotion` `{ids}` → `{requested,queued,skipped}`（全代理逐国扫优惠，写 globalPromotion* 字段）
- `POST /api/accounts/check-oaics-all-proxies` `{ids}` → `{requested,started,skipped}`（全代理 OAICS 检测，前端 5s 轮询表格自动刷新 running 行，L971-975）
- `POST /api/accounts/check-checkout-type` / `POST /api/accounts/check-paymethod`（同 plan check 家族，参数多了 `country/retries/concurrency`）

---

## 4. 后端实现（文件 + 核心逻辑）

所有路由挂在 `app/backend/main.py`（FastAPI）。GPT-GO 改写时按下面对应。

### 4.1 批量验活
- 路由：`main.py` L1931-1942 `check_accounts_alive` → `app.state.alive_check_service.check_accounts(ids, proxy_id)`
- 服务：`app/backend/account_alive_service.py`（166 行全量已读）
- 核心逻辑：
  1. **并发**：`Semaphore(min(max_concurrency=3, 可用代理数))`；指定 proxyId 时退化为 1
  2. **乐观锁 claim**：`resource_service.claim_account_alive_check`（L1170）用
     `find_one_and_update` 把 `aliveStatus` 置 `running`（5 分钟卡死自愈），
     同时投影取 `accessToken/registrationCountry/registrationProxyId/deviceId`；
     无 AT 或已在跑 → `skipped(account_missing_token_or_busy)`
  3. **判定**：`chatgpt_plan.check_account_plan_curl`（curl_cffi 伪装 chrome）打
     `GET https://chatgpt.com/backend-api/accounts/check/v4-2023-04-27?timezone_offset_min=..`，
     带 `Authorization: Bearer <AT>`、`oai-device-id`、`oai-language`（按出口国家）
     - 401 → `access_token_unauthorized`；JWT 本地判过期 → `access_token_expired`；
       两者属 `DEAD_ACCOUNT_ERRORS` → **dead**
     - 2xx → **alive**，顺手记录代理延迟
     - 代理/网络错误（`plan_request_failed` 等 retryable）→ **换代理重试**（默认 3 次，
       `AUTOREGISTER_ALIVE_PROXY_ATTEMPTS`），坏代理 `record_proxy_test(available=False)`
       累计失败次数自动隔离
     - 最终失败 → **unknown**
  4. **回写**：`store_account_alive_result/failure` → `aliveStatus/aliveCheckedAt/aliveErrorCode/aliveHttpStatus`
  5. **代理选择**：优先 `proxyId` 参数 → 账号 `registrationProxyId` → 按注册国家随机

### 4.2 批量查优惠资格
- 路由：`main.py` L1275-1286 → `plan_check_service.check_accounts`
- 服务：`app/backend/plan_check_service.py` L186-260 + `app/backend/chatgpt_plan.py`
- 核心逻辑：
  1. claim/并发/换代理重试模式与验活完全相同（`claim_account_plan_check` 要求 `accessTokenConfigured=true`）
  2. 同一个 `check_account_plan_curl` 请求，区别在**结果解析** `parse_accounts_check`：
     - 从 JWT payload 取 `chatgpt_account_id` / `chatgpt_plan_type`
     - 响应 `accounts[accountId|default]` → `account.plan_type` + `entitlement`（`subscription_plan/has_active_subscription/expires_at/renews_at`）+ `eligible_promo_campaigns`
     - **排除 go 套餐**（key=="go" 或 id 前缀 "go-" 或 metadata.plan_name=="chatgptgoplan"）
     - `promotionEligible = is_free && 有非 go campaign`
     - entitlement 是付费状态的最终事实源（JWT claim 可能滞后：`has_active_subscription && subscription 含 plus` → plan_type 修正为 plus）
  3. 回写 `store_account_plan_result`（L1330）：plan 字段全家 + `accountType` 纠正；
     **失败也落 `promotionEligible=False`**（归入“不可试用”而非“未查询”）；
     `access_token_expired/unauthorized` 时顺手把 `accessTokenConfigured=False`
  4. 副作用：有资格且开关开 → 自动 `queue_payment_probe`（fire-and-forget 支付探测入队）

### 4.3 批量补 2FA（补密码+TOTP）
- 路由：`main.py` L2111-2136 `bulk_ensure_2fa`；单账号执行单元 `_ensure_one_account` L2064-2109
- 服务：`app/backend/account_security_service.py`（`ensure_totp` L292-428、`_enroll_totp` L152-217、`check_2fa_status` L220-275、`verify_totp` L278-290）
- 核心逻辑：
  1. **并发**：`asyncio.gather` + `Semaphore(settings.concurrency)`（全局设置里的注册并发数）
  2. **每账号独立干净代理**：最多试 6 个代理，先 `_rebind_proxy_sliver_clean` 预检
     （sliver 指纹检测出口 IP 是否脏），脏代理直接换下一个
  3. **ensure_totp 两路径**：
     - 已有密码：`run_protocol_login`（协议化登录，curl_cffi，不走浏览器）→
       拿到满足 recent_auth 的 AT → `_enroll_totp`：
       `POST /backend-api/accounts/mfa/enroll {factor_type:"totp"}` 拿 `secret+session_id`
       → 本地 `generate_totp(secret)` 生成验证码 →
       `POST /backend-api/accounts/mfa/user/activate_enrollment {code, factor_type, session_id}`
     - 无密码（passwordless）：`backfill_password_and_totp`：signin 带
       `post_login_add_password` → 邮箱 OTP 重新认证（MailBridgeProvider 走
       `emailAccessUrl` 收码）→ `POST /api/accounts/password/add` → enroll+activate TOTP
  4. **落库顺序很关键**：密码先落库（即使绑 2FA 失败也要落，否则下次重试撞“已有密码”），
     异常时还有兜底把 `flow.result.password` 落库；TOTP 缺失判失败（`backfill_empty`）；
     成功 `store_account_totp(id, secret, accessToken, expires=now+30d, now)`
  5. **操作日志**：`_append_security_log` 写内存环形缓冲（≤2000 条，保留最近 1500），
     另挂 `SecurityLogHandler` 桥接 `auth_flow` 的 logger.info；前端 3s 轮询。
     步骤标签：`ensure_2fa.start/success/failed/dirty_proxy`、`bulk_ensure_2fa.start/done`
- refresh_token 刷新 AT：`refresh_access_token_with_rt`（L64-141）
  `POST https://auth.openai.com/oauth/token {client_id:"app_EMoamEEZ73f0CkXaXp7hrann", grant_type:"refresh_token", refresh_token}`，
  `invalid_grant/expired/reused/invalidated` → permanent（只能重新登录）

### 4.4 关键存储模式（resource_service.py）
- **claim-then-run 乐观锁**：每种检查一个 `claim_account_<x>_check`，
  `find_one_and_update({status != running 或 startedAt 超过 5min}, {$set: status=running})`，
  原子防并发重复执行 + 卡死自愈。**这套模式强烈建议照搬**。
- 状态三件套回写：`<x>Status / <x>CheckedAt / <x>ErrorCode / <x>HttpStatus`。

---

## 5. 代理配置

### 5.1 代理模型与池
- 模型：`ProxyRecord`（resource_models.py L203-215）：`host/port/username/password/scheme(http|https|socks5|socks5h)/country(2位码)/group/enabled/status(available|unknown|used|quarantined)/latencyMs/lastCheckedAt`
- 导入：
  - 粘贴导入 `POST /api/proxies/import {rawText, country?, group?}`（`host:port:user:pass` 或 URL，前端 parsers.ts 解析去重）
  - 订阅导入 `POST /api/proxies/import-subscription`（easy-proxies / resin 两家：subscriptionUrl+managerUrl+token，自动拉节点→生成代理→测速→入库）
  - IPRocket 按国生成：`GET/PUT /api/proxies/iprocket/config` + `POST /api/proxies/iprocket/generate {countries[], linesPerCountry}`（住宅代理，sessionMode lsid/sid/none 粘性会话，生成 `-res-{CC}` 线路，产出 `usableProxiesByCountry` 供逐国扫描用）
- 测速：`POST /api/proxies/test {country?, group?, timeoutSeconds:6}` → `{tested,available,failed,averageLatencyMs,countries[]}`

### 5.2 注册时用代理吗？
是。**注册独占代理策略**（probe_store.py L552-575 注释）：
- 每个注册 worker `acquire_proxy` 原子租一个代理（`leaseOwner/leaseUntil` 排他租约，
  同并发数内按 `activeLeaseCount` 升序 + `randomScore` 随机选，避免顺序被风控识别）
- 用完 `consume_proxy` 把代理标 **used 永久出池**（一代理一账号），可前端“恢复可用”放回
- `maxRegistrationsPerExitIp` 设置限制每出口 IP 注册数
- 注册时把 `registrationProxyId/registrationCountry/registrationIp/registrationIsp` 落到账号上，
  后续验活/查优惠**默认复用注册时代理**（`effective_proxy_id = proxyId || registrationProxyId`）

### 5.3 每账号绑定代理？
- 注册期：`registrationProxyId`（永久绑定记录，但代理本体可能已 used）
- 换绑期：`rebindProxy/rebindProxyCountry`
- 运行期操作（验活/查优惠/补2FA）：不绑死，走 acquire 租约；前端 AccountsView 顶部有
  **“默认注册代理（可选指定）”下拉**（L1026-1039，列 enabled 代理
  `country · host:port · group`），选中后所有批量检查带 `proxyId` 强制执行

### 5.4 前端如何引导配代理
- **SettingsView.vue**（代理管理页签）：导入粘贴框、订阅导入表单（provider 选择/
  subscriptionUrl/managerUrl/adminToken/proxyToken）、IPRocket 配置表单
  （gateway/port/scheme/sessionMode/sessionTtl/linesPerCountry）、按国家/分组表格、
  测速按钮、启用开关（`PATCH /api/proxies/{id}`）、恢复可用、执行设置表单
  （`proxyRetryCount`、`proxyCheckConcurrency`、`maxRegistrationsPerExitIp`、`concurrency`）
- **LaunchView.vue**（注册发起页）：注册时选 `country` + `proxyGroup`（多选，
  来自 `store.proxyGroups` 聚合），提交 `POST /api/runs {kind, count, country[], group[], emailSource}`
- **AccountsView.vue**：批量操作前的代理下拉（可选覆盖）
- 空代理池时的错误引导：如试用扫描直接 503 “没有可用代理，请补充代理池或粘贴代理后重试”

---

## 6. 给 GPT-GO 的搬运清单（优先级排序）

1. **账号 struct 扩展**：补 `totpSecret/totpStatus`、`refreshToken`、`accessTokenExpiresAt`、
   `promotionEligible/promotionCampaigns[]`、`aliveStatus/aliveCheckedAt/aliveErrorCode`、
   `registrationCountry/registrationIp/registrationProxyId`、`remark`、以及每个异步检查的
   `xxxStatus(running)/xxxCheckedAt/xxxErrorCode` 三件套。
2. **三个批量 API**（Go handler）：
   - `POST /api/accounts/check-alive` → 带 AT 打 accounts/check 端点，401/expired=dead
   - `POST /api/accounts/check-promotion` → 同端点解析 entitlement + eligible_promo_campaigns
   - `POST /api/accounts/bulk-ensure-2fa` → enroll→本地生成 code→activate；先落密码再落 secret
3. **claim-then-run 乐观锁** + 并发 Semaphore + 换代理重试（最多 3-5 次，坏代理计数隔离）。
4. **批量结果统一结构**：`{requested, succeeded/alive, failed/dead, skipped, items:[{id,status,errorCode}]}`，
   前端 ElMessage/Notification 汇总 + 行内状态胶囊。
5. **操作日志环形缓冲 + 前端轮询浮窗**（补 2FA 这类长任务体验关键）。
6. **代理池**：ProxyRecord + acquire/consume 独占租约 + `registrationProxyId` 落账号 +
   批量操作可选 proxyId 覆盖。
7. **筛选维度**：`q`(邮箱) / `promotion`(untried_plus|ineligible|unchecked) /
   `alive`(alive|dead|unknown|unchecked) / `country` / `rebind` —— 直接照 ResourceQuery。
