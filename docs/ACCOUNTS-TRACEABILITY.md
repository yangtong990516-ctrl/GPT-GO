# 账号池（Accounts）基本信息迁移依据索引

> 本文件是账号池「基本信息」第一步的迁移依据，供下次程序/人直接定位源码位置并了解字段含义与展示信息。
> 位置格式：`文件:行号`。参考源为只读对照，Go 为迁移产物。

参考源根：`/Users/iceman/Documents/workspace/codex-auto-register-macos/app/backend`
Go 根：`/Users/iceman/Documents/workspace/GPT-GO`

---

## 一、接口 → 实现映射总表

| # | 接口 | 功能点 | Python 路由 | Go 路由 | Go 业务 | 参考业务 |
|---|------|--------|------------|---------|---------|---------|
| A1 | GET /api/accounts | 分页列表（q/country/alive/promotion 筛选，createdAt 降序） | main.py:1226 | apiserver/accounts/accounts.go:33 | service/account/service.go:49 | resource_service.py:476 |
| A2 | POST /api/accounts | 创建账号（201，email 去重 409） | main.py:1245 | apiserver/accounts/accounts.go:59 | service/account/service.go:76 | resource_service.py:619 |
| A3 | POST /api/accounts/bulk-delete | 批量删除（返回 deleted 计数） | main.py:1253 | apiserver/accounts/accounts.go:100 | service/account/service.go:111 | resource_service.py:661 |
| A4 | GET /api/stats/overview | 统计总览（accounts 块） | main.py:2485 | apiserver/accounts/accounts.go:Stats | service/account/service.go:Stats | resource_service.py:2353 |

## 二、字段映射（37 个返回字段，含注释 + 展示信息 + 展示/动作归属）

> 每个字段注释标注归属：【展示】服务于页面表格/卡片渲染；【动作】服务于操作按钮触发；
> 【动作结果】动作完成后写回的结果展示；【不展示】仅存储/导出，不直接渲染。

| # | 字段 | Go 类型 | 归属 | 注释（含义） | 前端展示列 / 标签 |
|---|------|---------|------|-------------|------------------|
| 1 | id | string | 不展示 | 账号唯一 ID（Mongo _id） | 无独立列（勾选/操作引用） |
| 2 | email | string | 展示 | 账号登录邮箱 | 邮箱列（等宽字体） |
| 3 | chatgptPassword | string | 不展示 | ChatGPT 登录密码 | 不展示（存储/导出） |
| 4 | totpSecret | string | 展示 | TOTP 2FA 密钥 | 2FA 列：有值→"已启用" |
| 5 | totpStatus | *string | 展示 | TOTP 检测状态 | 2FA 列：=="missing"→"缺失" |
| 6 | totpSecretConfigured | bool | 展示 | 是否已配置 TOTP（=bool(totpSecret)，推导） | 2FA 列 |
| 7 | emailAccessUrl | string | 动作 | 关联邮箱取件 URL（api798 重写） | 「接码」按钮依赖；不展示 |
| 8 | createdAt | time.Time | 不展示 | 创建时间 UTC | 不展示（排序/存储） |
| 9 | accountType | plus/free | 展示 | 账号类型 | 账号类型列：plus→"Plus"(黄)，free→"Free"(灰) |
| 10 | phoneBound | *bool | 展示 | 是否已绑定手机 | 间接（plus 统计 bound/unbound） |
| 11 | accessTokenConfigured | bool | 展示 | AT 是否已配置（默认 false） | AT状态列核心判断 |
| 12 | accessTokenExpiresAt | *time | 展示 | AT 过期时间 | AT状态列 tooltip："过期时间：xxx" |
| 13 | accessTokenUpdatedAt | *time | 动作结果 | AT 更新时间 | 刷新/补 AT 后写回 |
| 14 | refreshToken | *string | 动作 | 刷新令牌 | 「刷新 AT」按钮依赖；不展示 |
| 15 | accessTokenMissing | bool | 展示 | 已建但 AT 未拿到（待补） | AT状态列："缺失"标签 |
| 16 | atRefillStatus | *string | 展示 | 补 AT 状态 | AT状态列：failed→"补AT失败"，add_phone_required→"需绑手机" |
| 17 | atRefillError | *string | 展示 | 补 AT 错误 | AT状态列 tooltip："补AT失败: xxx" |
| 18 | atRefillErrorAt | *time | 动作结果 | 补 AT 错误时间 | 补 AT 失败时写回 |
| 19 | planCheckStatus | *string | 展示 | 套餐检查状态 | 优惠资格列：running→"查询中" |
| 20 | planCheckedAt | *time | 展示 | 套餐检查时间 | tooltip："查询时间：xxx" |
| 21 | planCheckErrorCode | *string | 展示 | 套餐检查错误码 | tooltip："错误码：xxx" |
| 22 | subscriptionPlan | *string | 展示 | 订阅套餐名 | 优惠资格列（campaign） |
| 23 | planExpiresAt | *time | 展示 | 套餐过期时间 | tooltip |
| 24 | promotionCampaigns | []PromotionCampaign | 展示 | 促销活动列表 | 优惠资格列：1个→标签，多个→"可试用×N" |
| 25 | trialScanCheckedAt | *time | 动作结果 | 试用扫描时间 | 「试用扫描」动作后写回 |
| 26 | trialScanCountries | map[string]TrialCountryResult | 动作结果 | 试用扫描各国家结果 | 试用扫描列（空→"未扫描"） |
| 27 | trialScanError | *string | 动作结果 | 试用扫描错误 | 试用扫描列：有值→"扫描失败" |
| 28 | registrationCountry | *string | 展示 | 注册国家 2 位码 | 国家列：countryLabel 中文名，空→"-" |
| 29 | registrationIp | *string | 展示 | 注册 IP | 注册IP列（等宽），空→"-" |
| 30 | registrationIsp | *string | 不展示 | 注册 ISP 运营商 | 不展示 |
| 31 | aliveStatus | *string | 展示 | 存活状态 | 验活列：alive→"正常"等 |
| 32 | aliveCheckedAt | *time | 展示 | 存活检查时间 | 验活列 tooltip："检测时间：xxx" |
| 33 | aliveErrorCode | *string | 展示 | 存活检查错误码 | 验活列 tooltip："结果：xxx" |
| 34 | aliveHttpStatus | *int | 展示 | 存活检查 HTTP 状态码 | 验活列 tooltip："HTTP：xxx" |
| 35 | rebindStatus | *string | 展示 | 换绑状态（唯一展示字段：是否换绑过） | 换绑列：success→"已换绑"，null→"未换绑" |
| 36 | remark | *string | 展示 | 备注 | 备注列：有值→显示，空→"—" |
| 37 | promotionEligible | *bool | 展示 | 优惠资格 | 优惠资格列：true/false/null |

## 三、促销活动标签枚举（CAMPAIGN_LABELS）

> 位于 model/account.go:50 `CampaignLabel`，镜像前端 AccountsView.vue:170-180。

| 活动 ID | 中文标签 |
|---------|---------|
| plus-1-month-free | 免费试用 1 个月 |
| plus-2-months-free | 免费试用 2 个月 |
| plus-3-months-free | 免费试用 3 个月 |
| plus-1-month-50-pct-off | 首月 5 折 |
| plus-2-months-50-pct-off | 前 2 个月 5 折 |
| plus-3-months-50-pct-off | 前 3 个月 5 折 |
| plus-1-month-25-pct-off | 首月立减 25% |
| plus-6-months-50-pct-off | 前 6 个月 5 折 |
| plus-1-month-75-pct-off | 首月立减 75% |

promotionCampaigns 每个元素结构：`{plan, id, promotion_type, title}`（chatgpt_plan.py:171-176）。

## 四、promotion 筛选枚举（ResourceQuery.promotion）

| 值 | 中文标签 | 过滤逻辑（resource_service.py:504-514） |
|----|---------|----------------------------------------|
| untried_plus | 未试用 Plus（免费试用一个月） | accountType=free 且 promotionEligible=true |
| ineligible | 不可试用 | promotionEligible=false |
| unchecked | 未查询 | promotionEligible 为空（null/不存在） |

## 五、枚举类型映射

| 枚举 | Python 位置 | Go 位置 |
|------|------------|---------|
| AccountType = plus/free | resource_models.py:12 | model/account.go:8 |
| PlanCheckStatus = running/success/failed | resource_models.py:14 | model/account.go:16 |
| AliveStatus = running/alive/dead/unknown | (内联 Literal) | model/account.go:25 |
| PromotionFilter | (前端 ResourceQuery) | model/account.go:36 |

## 六、辅助函数映射

| 函数 | Python 位置 | Go 位置 | 功能点 |
|------|------------|---------|--------|
| _account_record | resource_service.py:2455 | service/account/service.go:121 | Mongo 文档 → AccountRecord（24 字段） |
| normalize_country_code | resource_service.py:251 | service/account/service.go:40 | 2 位大写国家码，否则 ZZ |
| normalize_email | resource_service.py:148 | service/account/service.go:161 | strip+lower |
| direct_mailbox_access_url | mailbox_client.py:94 | email service.DirectMailboxAccessURL | api798 邮箱 URL 重写 |

## 七、存储映射

| 接口 | Go 位置 | 对应 Python | 功能点 |
|------|---------|------------|--------|
| AccountStore.List | store/account.go:83 | list_accounts find | 分页+筛选+createdAt 降序 |
| AccountStore.Create | store/account.go:162 | insert_one | 插入（emailNormalized 去重） |
| AccountStore.Delete | store/account.go:175 | delete_many | 批量删除 |
| AccountStore.Count | store/account.go:193 | count_documents | 统计计数（stats 用） |
| matchAccount | store/account.go:100 | mongo_query 构建 | q/country/promotion/alive 筛选 |
| MockAccountStore | store/account.go:72 | (内存 mock) | 内存实现 |

## 八、统计接口（/api/stats/overview）

> 本轮仅实现 accounts 块；emails/proxies 块返回零值（对应模块统计未接）。

| 统计项 | 计算逻辑（resource_service.py:2353-2434） |
|--------|------------------------------------------|
| accounts.total | count_documents({}) |
| accounts.today | createdAt >= 今日 UTC 零点 |
| accounts.totpComplete | totpSecret 非空（$nin ["", None]） |
| accounts.plus.total | accountType=="plus" |
| accounts.plus.bound | accountType=="plus" 且 phoneBound==true |
| accounts.plus.unbound | plus.total - plus.bound |
| accounts.free.total | accountType=="free" |
| accounts.free.eligible | accountType=="free" 且 promotionEligible==true |
| accounts.free.ineligible | free.total - free.eligible |

## 九、测试映射

| 测试 | Go 位置 | 覆盖 |
|------|---------|------|
| TestListPromotionUntriedPlus/Ineligible/Unchecked | service/account/service_test.go | promotion 筛选三值 |
| TestListAliveFilter | service/account/service_test.go | alive 筛选 |
| TestAccountRecordMapping | service/account/service_test.go | 字段映射 + CampaignLabel |
| TestAccountRecordTotpNotConfigured | service/account/service_test.go | totpSecretConfigured 推导 |
| TestCreateDuplicate | service/account/service_test.go | 409 去重 |
| TestDelete | service/account/service_test.go | 批量删除 |
| TestStats | service/account/service_test.go | 统计计算（total/today/totp/plus/free） |
| TestAccounts* | apiserver/accounts_test.go | 3 路由 + 校验 + stats/overview |

## 十、1:1 关键行为（已复刻，勿改回）

- `totpSecretConfigured` = bool(totpSecret)，推导字段非独立存储。
- `accessTokenConfigured` = bool(document.get("accessTokenConfigured", False))，默认 false。
- `emailAccessUrl` 输出经 `direct_mailbox_access_url`（api798 重写）。
- `promotionCampaigns` 空值 → `[]`（非 null）；`trialScanCountries` 空值 → `{}`（非 null）；`promotionEligible` 可空。
- `normalize_country_code("")` 在 list 里**不触发过滤**（只有非空才归一化）；无效国家码 → "ZZ"。
- `untried_plus` = accountType=free 且 promotionEligible=true（免费试用一个月）。
- 创建时 email strip+lower；重复 email → 409 "账号已存在"。
- 创建时 totpSecret 空串 → 422（min_length=1，与 Python 一致）。
- `rebindStatus` 是换绑列唯一展示字段（是否换绑过）；其余换绑动作字段（rebindProxy/rebindError 等）未接入。
- 本步骤接入 37 字段（24 基础 + 10 展示/动作结果 + phoneBound/accessTokenConfigured/accessTokenUpdatedAt 等）+ 4 接口（含 stats）。
- 换绑动作字段、其余动作接口（access-tokens/2fa/refill-at/export/备注编辑等）后续步骤。
