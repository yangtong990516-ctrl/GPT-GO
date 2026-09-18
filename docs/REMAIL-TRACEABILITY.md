# Remail（ReMail 接码平台）接口迁移依据索引

> 本文件是 remail 模块每个接口的迁移依据，供下次程序/人直接定位源码位置并了解功能点。
> 位置格式：`文件:行号`。参考源为只读对照，Go 为迁移产物。

参考源根：`/Users/iceman/Documents/workspace/codex-auto-register-macos/app/backend`
Go 根：`/Users/iceman/Documents/workspace/GPT-GO`

---

## 一、接口 → 实现映射总表

| # | 接口 | 功能点 | Python 路由 | Go 路由 | Go 业务 | 参考业务 |
|---|------|--------|------------|---------|---------|---------|
| R1 | GET /api/remail/config | 读取 ReMail 配置（apiKey/projectId/emailSuffix/baseUrl/updatedAt） | main.py:2235 | apiserver/remail/remail.go:35 | service/remail/service.go:70 | remail_service.py:82 |
| R2 | PUT /api/remail/config | 保存配置（apiKey strip、suffix 去@、baseUrl 去尾斜杠） | main.py:2239 | apiserver/remail/remail.go:45 | service/remail/service.go:89 | remail_service.py:92 |
| R3 | POST /api/remail/probe | 连通性测试（GET /v1/open/apikey/profile，Bearer） | main.py:2243 | apiserver/remail/remail.go:77 | service/remail/service.go:121 | remail_service.py:432 |
| R4 | POST /api/remail/create-mailboxes | 批量下单并导入邮箱池（purchase 长效） | main.py:2247 | apiserver/remail/remail.go:96 | service/remail/service.go:341 | remail_service.py:354 |
| R5 | POST /api/remail/import-purchased-orders | 遍历已购订单导入长效邮箱（并行取 token） | main.py:2251 | apiserver/remail/remail.go:123 | service/remail/service.go:195 | remail_service.py:146 |
| R6 | GET /api/remail/wallet | 查询钱包余额（GET /v1/open/wallet） | main.py:2255 | apiserver/remail/remail.go:159 | service/remail/service.go:151 | remail_service.py:415 |

## 二、数据模型映射

| 模型 | Python 位置 | Go 位置 | 字段 |
|------|------------|---------|------|
| RemailConfig | resource_models.py:944 | model/remail.go:12 | apiKey, projectId(nullable), emailSuffix, baseUrl, updatedAt(nullable) |
| RemailConfigInput | resource_models.py:952 | model/remail.go:20 | apiKey(8-320), projectId(>=1), emailSuffix(1-128), baseUrl(https) |
| RemailMailboxCreate | resource_models.py:975 | model/remail.go:28 | count(1-1000), emailSuffix(<=128) |
| RemailImportRequest | resource_models.py:980 | model/remail.go:35 | limit(1-100), maxOrders(0-2000), productType(<=32), emailSuffix(<=128), onlyIcloud |
| RemailWallet | resource_models.py:992 | model/remail.go:43 | ok, message, consumerBalance, totalRecharged, historicalSpend |
| RemailProbeInput | resource_models.py:1000 | model/remail.go:51 | apiKey(<=320) |
| RemailProbeResult | resource_models.py:1004 | model/remail.go:57 | ok, message, reachable |
| RemailOrderResult | resource_models.py:1010 | model/remail.go:64 | orderNo, deliveryEmail, serviceToken, verificationCode, status |
| RemailMailboxRecord | resource_models.py:1018 | model/remail.go:73 | email, accessUrl, imported, duplicate, orderNo, error(nullable) |

## 三、辅助函数映射

| 函数 | Python 位置 | Go 位置 | 功能点 |
|------|------------|---------|--------|
| _auth_headers | remail_service.py:108 | service/remail/service.go:477 | Bearer 认证 header（空 key 抛 remail_no_api_key 400） |
| build_access_url | remail_service.py:137 | service/remail/service.go:111 | /v1/pickup?email=&token= |
| _get | remail_service.py:118 | (内联 defaultHTTPDo) | GET + auth + 非200抛 remail_http_error |
| place_order | remail_service.py:251 | service/remail/service.go:411(placeOrders) | 单次下单 |
| place_order_batch | remail_service.py:289 | service/remail/service.go:411(placeOrders) | 批量下单(2-100) |
| _generate_mailbox_names | (无，remail 用下单) | — | 不适用 |
| normalize_suffix | remail_service.py:966 | service/remail/service.go:491 | 去@前缀，取@后域名 |
| normalize_email | remail_service.py:48 | service/remail/service.go:501 | strip+lower |

## 四、常量映射

| 常量 | Python 位置 | Go 位置 | 值 |
|------|------------|---------|-----|
| CONFIG_KEY | remail_service.py:40 | service/remail/service.go:16 | remail_config |
| API_BASE | remail_service.py:41 | service/remail/service.go:17 | https://remail.aishop6.com |

## 五、存储映射

| 接口 | Go 位置 | 对应 Python | 功能点 |
|------|---------|------------|--------|
| RemailStore.Load | store/remail.go:17 | _load_document find_one | 读 remail_settings |
| RemailStore.Save | store/remail.go:19 | save_config replace_one(upsert) | 存 remail_settings |
| MockRemailStore | store/remail.go:28 | (内存 mock) | 内存实现 |

邮箱 upsert 复用：remail 通过 `email.Service.UpsertMailbox`（source_type="remail"、mailbox_kind="url"）插入邮箱池，不重复实现 upsert。

## 六、测试映射

| 测试 | Go 位置 | 覆盖 |
|------|---------|------|
| TestGetConfigDefaults | service/remail/service_test.go | R1 默认值 |
| TestSaveConfigNormalizes | service/remail/service_test.go | R2 去斜杠+去@前缀+strip |
| TestSaveConfigSuffixFromEmail | service/remail/service_test.go | R2 user@icloud.com→icloud.com |
| TestBuildAccessURL | service/remail/service_test.go | accessUrl email+token 顺序 |
| TestProbeOK/Non200/Unreachable | service/remail/service_test.go | R3 三种结果 |
| TestGetBalance | service/remail/service_test.go | R6 wallet 解析 |
| TestImportPurchasedOrders | service/remail/service_test.go | R5 订单导入 |
| TestImportFiltersInvalid | service/remail/service_test.go | R5 过滤(status/type/suffix) |
| TestCreateMailboxes | service/remail/service_test.go | R4 批量下单 |
| TestCreateMailboxesNoProject | service/remail/service_test.go | R4 projectId 缺失 |
| TestRemail* | apiserver/remail_test.go | 6 路由 + 校验 |

## 七、线上事实（只读探查 114.134.186.92，勿改动）

- remail_settings（Mongo，只读）：
  - `{_id:"remail_config", apiKey:"rk-498f8007-1d81-49e2-afbf-a3bfc832b922", projectId:2, emailSuffix:"icloud.com", baseUrl:"https://remail.aishop6.com", updatedAt:2026-09-11T06:20:43.519Z}`
- 线上后端 GET /api/remail/config → `{"apiKey":"rk-498f8007-1d81-49e2-afbf-a3bfc832b922","projectId":2,"emailSuffix":"icloud.com","baseUrl":"https://remail.aishop6.com","updatedAt":"2026-09-11T06:20:43.519000Z"}`
- 线上后端 GET /api/remail/wallet → `{"ok":true,"message":"","consumerBalance":"15.00","totalRecharged":"150000.00","historicalSpend":"150330.00"}`（余额实时变化）
- 线上后端 POST /api/remail/probe → `{"ok":true,"message":"ReMail API Key 有效，连接正常","reachable":true}`
- 上游 remail API：`GET /v1/open/apikey/profile` 返回 `{"apiKey":{"id":968,"balance":"15.00",...}}`
- 上游 import 真实订单：`cookie.aglow.1f@icloud.com`、`sorcery.poplars5l@icloud.com`（OR01A08F...），accessUrl 指向 `/v1/pickup?email=...&token=...`

## 八、1:1 关键行为（已复刻，勿改回）

- probe 只看 HTTP 状态码==200（不解析 body）；非 200 时 reachable=true、ok=false；连接异常 reachable=false。
- `_auth_headers`：apiKey 为空时抛 `remail_no_api_key`（400）"ReMail API Key 未配置"。import/wallet/下单都会触发。
- `build_access_url`：`/v1/pickup?email=<urlencode>&token=<token>`，key 顺序 email 在前、token 在后（@→%40）。
- `import_purchased_orders` 过滤：status ∈ {refunded,failed,closed} 跳过；productType 过滤；emailSuffix 过滤；maxOrders 截断；最多扫 100 页。
- `create_mailboxes`：count 1-1000，每片最多 100（批量接口要求 2-100），剩 1 个走单次下单；`remail_no_project`/`remail_no_suffix` 错误被捕获进 record（不向上抛）。
- `place_order_batch`：quantity 2-100；`insufficient_balance`/`insufficient_inventory` 抛 402 `remail_balance_low`。
- `get_balance`：任何异常返回 `ok=false, message=str(exc)`（不抛错）。
- `RemailMailboxRecord` 有 `orderNo` 字段（mailcode 没有）；error 默认 null。
