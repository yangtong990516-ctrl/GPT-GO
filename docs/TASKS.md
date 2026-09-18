# TASKS - GPT-GO 迁移任务清单

> 每个任务可独立验证，同一时间只推进一个 IN_PROGRESS。
> 任务由用户决定先后。废弃接口不收录。任务编号稳定。

## 阶段 0：骨架与基础设施

- T-001 工程骨架 + YAML 配置 + 工具类 + /api/health —— DONE
  - 参考: __main__.py, main.py:455-461, mongo_manager.py, resource_models.py:745-755
  - Go: cmd/, internal/{config,util,model,apiserver,store/mongo}
  - 验收: 构建/测试通过；/api/health 与 Python 契约一致
- T-002 MongoDB 完整连接管理（MongoManager.start/stop/probe/monitor） —— TODO
  - 参考: mongo_manager.py
  - Go: internal/store/mongo/
  - 依赖: T-001
  - 验收: online/reconnecting/offline + 重连回退(2,5,10,30) + ping 探测一致
- T-003 错误类型 + HTTP 错误映射中间件 —— TODO
  - 参考: errors.py, main.py:413-453
  - Go: internal/util/errors.go + apiserver 中间件
  - 验收: Mongo->503 / duplicate->409 / notfound->404 一致
- T-004 本地 JSON 存储 settings —— TODO
  - 参考: settings_store.py
  - Go: internal/store/settings/
  - 验收: 读写/schema/备份(.bak)/临时(.tmp) 一致
- T-005 本地 JSON 存储 run-log —— TODO
  - 参考: run_log_store.py
  - Go: internal/store/runlog/
  - 验收: JSONL 追加/清理/脱敏一致

## 阶段 1：资源 CRUD

- T-101 资源模型 + Mongo store（accounts/emails/proxies） —— TODO
  - 参考: resource_models.py, resource_service.py, probe_store.py
  - Go: internal/model/, internal/store/resource/
  - 依赖: T-002
- T-102 accounts API（18 路由） —— TODO
  - 参考: main.py:1226-*
  - Go: internal/service/account/ + apiserver
- **T-102a accounts 基本信息（3 路由：list/create/bulk-delete，24 字段） —— DONE**
  - 参考: main.py:1226-1256, resource_service.py:476-661, resource_models.py:63-162
  - Go: internal/apiserver/accounts/, internal/service/account/, internal/store/account.go, internal/model/account.go
  - 依赖: 无（mock store；emailAccessUrl 复用 email.DirectMailboxAccessURL）
  - 验收: 3 API 行为 1:1，含 promotion=untried_plus 筛选，见 ACCOUNTS-TRACEABILITY.md
- **T-103 emails API（邮箱池，7 路由） —— DONE**
  - 参考: main.py:2145-2215, resource_service.py email 方法
  - Go: internal/apiserver/emails/, internal/service/email/, internal/store/{store,mock_email}.go, internal/model/email.go
  - 依赖: 无（用 mock store 推进，Mongo 后续 docker 搭建）
  - 验收: 7 API 行为 1:1 复刻，见 EMAILS-FACTS.md
- **T-104 proxies API（14 路由） —— DONE（订阅导入/iprocket 已按决策删除，纯净度探测引擎已实现）**
  - 参考: main.py:2297-2483, resource_service.py(proxy 方法), probe_store.py(ProxyLease/租约), proxy_subscription_service.py(_probe_proxy 探测), geoip_lookup.py(GeoLite2)
  - Go: internal/apiserver/proxy/, internal/service/proxy/, internal/store/proxy.go, internal/model/proxy.go
  - 依赖: geoip2-golang（MaxMind mmdb 读取）；GeoLite2-City.mmdb（67MB，git-lfs，软链接 data/geoip/）
  - 验收: 14 API 行为 1:1（camelCase + 厂商协议纠正 9595→socks5h + 国家推断 + 租约/独占策略 + 纯净度探测引擎 cdn-cgi/trace sliver 分级），用 ipipbright + iprocket 两组真实代理验证导入解析，见 PROXY-TRACEABILITY.md
  - 已删除: 订阅导入（easy-proxies/resin，依赖本机管理器）、IPRocket config/generate（用户手动 import 代理，绕过生成）
  - 待办: 真实 Mongo（T-002）
- **T-105 mailcode API（自建 Mailcow，4 路由） —— DONE**
  - 参考: main.py:2218-2232, mailcode_service.py, resource_models.py:833-886
  - Go: internal/apiserver/mailcode/, internal/service/mailcode/, internal/store/mailcode.go, internal/model/mailcode.go
  - 依赖: 无（mock store；邮箱 upsert 复用 email.Service.UpsertMailbox）
  - 验收: 4 API 行为 1:1 复刻，线上只读对照，见 MAILCODE-TRACEABILITY.md
- **T-106 remail API（ReMail 接码平台，6 路由） —— DONE**
  - 参考: main.py:2235-2256, remail_service.py, resource_models.py:944-1024
  - Go: internal/apiserver/remail/, internal/service/remail/, internal/store/remail.go, internal/model/remail.go
  - 依赖: 无（mock store；邮箱 upsert 复用 email.Service.UpsertMailbox）
  - 验收: 6 API 行为 1:1 复刻，线上只读对照，见 REMAIL-TRACEABILITY.md
- **T-107 settings 执行参数（2 路由） —— DONE**
  - 参考: main.py:1197-1224, settings_store.py
  - Go: internal/apiserver/settings/, internal/service/settings/, internal/store/settings.go, internal/model/settings.go
  - 依赖: 无（本地 JSON 文件，非 Mongo）
  - 验收: GET/PUT /api/settings/execution 行为 1:1（schema v1/v2 迁移 + 原子写 + 损坏检测）；修正 Python save 漏复制 requireTrialOnCheck/autoMultiCountryProbe 的 bug
- **T-108 stats 统计概览（1 路由） —— DONE**
  - 参考: main.py:2485-2488, resource_service.py:2353-2452
  - Go: internal/apiserver/stats/, internal/service/stats/, internal/model/account.go(复用 OverviewStats 等)
  - 依赖: account/email/proxy store 新增 Count 方法
  - 验收: GET /api/stats/overview 聚合 account/email/proxy 计数，email available 排除已注册账号（1:1）

## 阶段 2：流水线与任务

- T-201 流水线服务（AccountPipelineService） —— TODO
- T-202 pipeline API（26 路由）+ WS /ws/tasks —— TODO
- T-203 runs/tasks 管理 —— TODO

## 阶段 3：支付提取与通道（暂缓，需用户决策替代方案）

- T-301 支付提取服务 —— TODO
- T-302 OAI 支付提取子包 —— TODO
- T-303 PayPal 协议子包（curl_cffi 指纹替代需决策） —— TODO
- T-304 提炼模块（内嵌 JS/QuickJS 替代需决策） —— TODO
- T-305 protocol_signup 子包 —— TODO
- T-306 qt_sentinel / icloud_hme —— TODO

## 阶段 4：邮件/短信/代理/安全

- T-401 邮箱客户端 + cloudmail/mailcode/remail —— TODO
- T-402 短信服务（非废弃部分） —— TODO
- T-403 代理订阅 + 健康调度 —— TODO
- T-404 账号安全/重绑 —— TODO

## 阶段 5：其余辅助模块

- **T-501a sentinel CDK 检测（3 路由：config GET/PUT + version） —— DONE（网络探测留桩）**
  - 参考: main.py:473-529, sentinel_version_scheduler.py, sentinel_quickjs.py:48-50
  - Go: internal/apiserver/sentinel/, internal/service/sentinel/, internal/store/sentinel.go, internal/model/sentinel.go
  - 依赖: 无（mock store；代理池 ProxyProvider mock 返回空）
  - 验收: 3 API 行为 1:1（snake_case 字段 + config 热更新 + 调度循环），见 SENTINEL-TRACEABILITY.md
  - 待办: 真实网络探测（curl_cffi 指纹决策）+ 真实代理池（T-104）+ 真实 Mongo（T-002）
- T-501 sentinel 版本调度 + 探测存储 —— 部分（见 T-501a，探测存储已接）
- T-502 试用扫描 —— TODO
- T-503 支付探测/全局矩阵（非废弃部分） —— TODO
- T-504 零散模块（totp/geoip/chatgpt_plan 等） —— TODO

## 阶段 6：部署收尾

- T-601 部署/配置/切换说明 —— TODO
- T-602 全量兼容对照验证 —— TODO
- T-603 最终验收 —— TODO

## 验证证据

### T-001（DONE）
- go build/vet/test 全通过；curl /api/health 返回与 Python 契约一致 JSON。

### T-103 邮箱池（DONE）
- go build/vet/test 全通过。
- 单元测试（internal/service/email）: TestImport / TestImportBOMAndLabels /
  TestImportSkipsRegistered / TestListFiltering / TestListOrdering / TestExport /
  TestSetStatusNotFound / TestSyncMailcomAliases / TestDirectMailboxAccessURL。
- HTTP 层测试（internal/apiserver）: TestEmailsListEmpty / TestEmailsListInvalidSource /
  TestEmailsImportAndList / TestEmailsBulkDelete / TestEmailsResetFailed /
  TestEmailsUpdateStatusNotFound / TestEmailsExportNoIDs。
- 运行时 curl 验证 7 端点：
  - import -> {"total":2,"imported":2,...}
  - list available -> 分页 JSON，字段 camelCase、null 语义正确
  - source=remail -> 422 invalid_source（路由 pattern 不含 remail，1:1 复刻）
  - export all -> content/filename/count/format=null
  - update status 不存在 -> 404 resource_not_found "邮箱不存在"
  - sync-mailcom-aliases（hub 未起）-> 502 mailcom_hub_unavailable

### T-105 mailcode（DONE）
- go build/vet/test 全通过。
- 单元测试（internal/service/mailcode）: TestGetConfigDefaults /
  TestSaveConfigNormalizes / TestSaveConfigDomainFromEmail / TestBuildAccessURL /
  TestProbeOK / TestProbeNon200 / TestProbeUnreachable /
  TestCreateMailboxesByCount / TestCreateMailboxesByEmails / TestCreateMailboxesEmpty /
  TestGenerateMailboxNamesFormat。
- HTTP 层测试（internal/apiserver）: TestMailcodeGetConfigDefault /
  TestMailcodeSaveConfig / TestMailcodeSaveConfigInvalidBaseURL / TestMailcodeProbeRoute /
  TestMailcodeCreateMailboxesArray / TestMailcodeCreateMailboxesInvalidCount。
- 运行时 curl 验证 4 端点：
  - GET config 默认 -> {"baseUrl":"https://mail.example.com","domain":"example.com","updatedAt":null}
  - PUT config -> baseUrl 去尾斜杠、domain 去@前缀
  - probe(baseUrl=https://mail.liwei-inc.com) -> {"ok":true,"message":"mailcode-api 连接正常","reachable":true}（与线上后端逐字一致）
  - create-mailboxes(count=2) -> 生成 test-XXXX-XXXXXX@liwei-inc.com + accessUrl（%40 编码）
- 线上只读对照（114.134.186.92）：mailcode_settings 配置 baseUrl=https://mail.liwei-inc.com，
  domain=liwei-inc.com；probe 响应逐字段一致。

### T-106 remail（DONE）
- go build/vet/test 全通过。
- 单元测试（internal/service/remail）: TestGetConfigDefaults / TestSaveConfigNormalizes /
  TestSaveConfigSuffixFromEmail / TestBuildAccessURL / TestProbeOK / TestProbeNon200 /
  TestProbeUnreachable / TestGetBalance / TestImportPurchasedOrders /
  TestImportFiltersInvalid / TestCreateMailboxes / TestCreateMailboxesNoProject。
- HTTP 层测试（internal/apiserver）: TestRemailGetConfigDefault / TestRemailSaveConfig /
  TestRemailSaveConfigInvalid / TestRemailProbeRoute / TestRemailWalletRoute /
  TestRemailCreateMailboxesInvalidCount / TestRemailImportInvalidLimit / TestRemailImportNoAPIKey。
- 运行时 curl 验证 6 端点：
  - GET config 默认 -> {"apiKey":"","projectId":null,"emailSuffix":"","baseUrl":"https://remail.aishop6.com","updatedAt":null}
  - PUT config -> apiKey 保存、emailSuffix 去@前缀（@icloud.com -> icloud.com）
  - probe(真实 apiKey) -> {"ok":true,"message":"ReMail API Key 有效，连接正常","reachable":true}（与线上后端逐字一致）
  - wallet(真实查询) -> {"ok":true,"message":"","consumerBalance":"25.00","totalRecharged":"150000.00","historicalSpend":"150330.00"}
  - import-purchased-orders(真实拉取 maxOrders=2) -> 2 个 icloud 订单（cookie.aglow.1f@icloud.com、sorcery.poplars5l@icloud.com），accessUrl 正确
  - 错误路径: import 无 apiKey -> 400 remail_no_api_key；count=0/1001 -> 422；limit=0 -> 422
- 线上只读对照（114.134.186.92）：remail_settings 配置 apiKey=rk-498f8007-...、projectId=2、
  emailSuffix=icloud.com；probe/wallet/import 响应逐字段一致。

### T-102a 账号池基本信息（DONE）
- go build/vet/test 全通过。
- 单元测试（internal/service/account）: TestListPromotionUntriedPlus /
  TestListPromotionIneligible / TestListPromotionUnchecked / TestListAliveFilter /
  TestAccountRecordMapping / TestAccountRecordTotpNotConfigured / TestCreateDuplicate / TestDelete。
- HTTP 层测试（internal/apiserver）: TestAccountsListEmpty / TestAccountsCreateAndList /
  TestAccountsPromotionFilter / TestAccountsBulkDelete / TestAccountsCreateDuplicate /
  TestAccountsCreateInvalid。
- 运行时 curl 验证 3 端点：
  - POST /api/accounts -> 201，返回 24 字段，email lowercased，totpSecretConfigured=true（推导）
  - GET /api/accounts?promotion=untried_plus -> 只返回 free+eligible=true（免费试用一个月）
  - country=US 筛选 -> 小写 us 归一化为 US；bulk-delete -> {"deleted":1}
- 24 字段 + 9 个促销活动标签枚举（免费试用1个月/首月5折等）已定义并注释。
