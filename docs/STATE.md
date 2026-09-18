# STATE - 交接状态（精简）

## 当前阶段与任务

- 阶段 1：资源 CRUD（+ 阶段 5 sentinel 检测先行接入）
- 已完成：T-103 邮箱池、T-105 mailcode、T-106 remail、T-102a 账号池基本信息、T-501a sentinel CDK 检测、T-104 proxies 代理池、T-107 settings 执行参数、T-108 stats 统计概览
- 下一任务：protocol_signup 纯注册（用户已指定"先设置 protocol_signup"，pipeline 全剔除）。当前为「骨架已搭好」阶段，10 步 auth_flow + sentinel sdk.js 求解为下一步核心。

## 已完成

- 固定路径项目：/Users/iceman/Documents/workspace/GPT-GO（module gpt-go）。
- 权威契约 docs/CONTRACT.md（路径/废弃接口/仅迁在用/YAML/包结构/API 分组/工具类）。
- 迁移文档 docs/{GOAL,TASKS,DECISIONS,STATE,INDEX}.md + docs/{EMAILS,MAILCODE,REMAIL,ACCOUNTS,SENTINEL,PROXY}-TRACEABILITY.md + docs/EMAILS-FACTS.md。
- T-001 工程骨架 + /api/health —— DONE。
- T-103 邮箱池 7 个 API —— DONE（mock store）。
- 前端 web/ —— DONE（React + Vite + TS + Tailwind v4 + shadcn/ui 主 + appica-ui 辅，深色紫色主题，覆盖 9 个业务域）。
- T-105 mailcode 4 个 API —— DONE（mock store，复用邮箱池 upsert）。
- T-106 remail 6 个 API —— DONE（mock store，复用邮箱池 upsert）。
- T-102a 账号池基本信息 3 API —— DONE（mock store，24 字段 + promotion 筛选）。
- T-501a sentinel CDK 检测 3 API —— DONE（mock store，调度循环完整，网络探测留桩）。
- T-104 proxies 代理池 14 API —— DONE（mock store，租约/候选/独占策略 + 纯净度探测引擎已实现；订阅导入/iprocket 已删除）。
- T-107 settings 执行参数 2 API —— DONE（本地 JSON 文件，非 Mongo；修正 Python save 漏字段 bug）。
- T-108 stats 统计概览 1 API —— DONE（聚合 account/email/proxy 计数，email available 排除已注册）。

## 代理池交付明细

- 模型 internal/model/proxy.go: ProxyRecord/Lease/Import/Test/Country/Group/Update/Status + 纯函数（协议/国家/组/厂商推断 + proxy_url）。
- 存储 internal/store/proxy.go: ProxyStore 接口 + MockProxyStore（CRUD + 租约 + 候选 + 探测落库，mutex 保护）。
- 业务 internal/service/proxy/: crud.go/import.go/lease.go/probe.go/geoip.go/service.go（按子域分组）；探测引擎 cdnProber 已实现（cdn-cgi/trace sliver 纯净度分级 + GeoLite2 国家/时区）。
- API internal/apiserver/proxy/proxy.go: 14 路由（/api/proxies）。
- sentinel 已接入真实代理池：proxyProviderAdapter 桥接 proxysvc → sentinel 的 all_eligible_proxy_candidates；resolveProxy 复用 model.ProxyURL。
- 关键决策：camelCase（与其他资源一致）；厂商协议纠正（IPRocket 9595→socks5h）；国家推断正则；租约独占策略；纯净度探测引擎复用（删了订阅/iprocket 入口，探测能力保留给 /api/proxies/test）。
- 已删除：订阅导入（easy-proxies/resin）、IPRocket config/generate（用户手动 import 代理）。
- GeoLite2：github.com/oschwald/geoip2-golang；GeoLite2-City.mmdb（67MB git-lfs，已 git lfs pull + 软链接 data/geoip/）。

## settings / stats 交付明细

- 模型 internal/model/settings.go: ExecutionSettings/Input + 默认值 + 边界校验（对齐 pydantic Field ge/le）。
- 存储 internal/store/settings.go: SettingsStore（本地 JSON 文件，原子写 tmp+fsync+backup+replace，schema v1/v2 迁移，损坏检测，mutex）。
- 业务 internal/service/settings/ + internal/service/stats/: settings Get/Update；stats Overview（聚合 account/email/proxy 计数）。
- API internal/apiserver/settings/ + internal/apiserver/stats/: GET/PUT /api/settings/execution + GET /api/stats/overview。
- store 扩展：EmailStore/ProxyStore 新增 Count(pred) 方法（AccountStore 已有）。
- 关键决策：settings 存本地 JSON（非 Mongo，对齐 settings_store.py）；修正 Python save 漏复制 requireTrialOnCheck/autoMultiCountryProbe 的 bug；stats 复用 model/account.go 已有的 OverviewStats 等类型（删重复定义）。

## protocol_signup 骨架（纯注册，pipeline 已剔除）

- 包结构 internal/service/signup/（对齐 Python protocol_signup/）：
  - config.go 最小 Config（仅 proxy）
  - errors.go 错误目录（14 码，对齐 error_catalog.py）+ RegistrationError
  - types.go AuthResult / RegistrationResult / BatchResult（对齐 Python 返回 dict）
  - ports.go ProxyStore / ResourceStore / MailProvider / SMSController 接口
  - service.go run_protocol_registration 编排（5 步：租代理→预留邮箱→AuthFlow→落库→清理）
  - batch.go run_protocol_batch 批量入口（并发 + 取消 + 进度回调）
  - httpclient/ tls-client 封装（替代 curl_cffi，socks5h 标准化 + TLS 瞬断重试）
  - fingerprint/ 设备指纹生成（chrome/safari/firefox 三家族，国家联动 TODO）
  - sentinel/ goja 跑 sdk.js 解 PoW（替代 Node/QuickJS）
  - authflow/ 10 步注册状态机（结构 + 方法签名，实现 TODO）
  - bridge/ 邮箱池/代理池桥接适配
- 关键依赖已引入 go.mod：
  - github.com/bogdanfinn/tls-client v1.16.0（TLS 指纹，替代 curl_cffi）
  - github.com/dop251/goja v0.0.0-20260311135729-065cd970411c（JS 引擎，替代 Node；选 go 1.20 兼容版本，因项目/机器是 go 1.24.13，goja 最新版需 go 1.25）
- 关键结论：sdk.js 依赖的 Web API 很有限（crypto.getRandomValues / btoa / atob / TextEncoder / performance），无需 WebCrypto.subtle，goja 注入这几个基础 API 即可。

## 未完成 / 阻塞

- Mongo 真实连接未接（T-002），全部模块用 mock store。
- 支付通道类（playwright/内嵌 JS）暂缓。
- 账号池剩余字段/接口（AT/2FA/refill/export/备注等）后续步骤。
- cloudmail（域名邮箱）尚未迁移。
- sentinel 真实网络探测未接（依赖 curl_cffi 指纹决策）。
- protocol_signup 核心实现未接：authflow 10 步、sentinel sdk.js 三阶段求解（requirements/challenge/solve）、fingerprint 完整版本列表+国家 profile。

## 整体 review 修复（契约 §5/§6.5 合规）

- P0 补 `util.Logger()` 统一日志器（zap，config.yaml log.level/json 驱动），main.go 改用它。
- P1 收敛 JSON：新增 `util.UnmarshalLenient`（宽松，用于第三方响应），remail/emails/settings 改用工具类；修复 remail 5 处吞错（改为显式 Warn 日志）。
- P2 收敛 env：geoip/settings 的 os.Getenv 改为 util.EnvOrDefault。
- P3 错误判断：settings 直接类型断言改为 errors.As。
- 差异记录：settings save 漏字段 bug 已补进 DECISIONS.md。
- 保留合理例外：json.MarshalIndent（settings 写文件需 indent=2）、probe 的 http.Client（util.HTTPClient 缺代理注入能力）。

## 最近验证

- go build / go vet / go test / go test -race 全通过（100+ 测试全绿）。
- 用 ipipbright（http，VN）+ iprocket（socks5h 9595，VN）两组真实代理验证导入解析、去重、候选查询、租约周期。
- 纯净度探测引擎：gradePurity 分级 + cdn-cgi/trace 字段解析单测通过；GeoLite2 实查 8.8.8.8 → US/America/Chicago（mmdb 已拉取并软链接）。
- 运行时 curl 验证 remail 6 端点（此前），响应与线上后端逐字一致。

## 下一步具体操作

- 等用户指定下一个迁移任务（cloudmail / 账号池剩余字段 / Mongo T-002 / curl_cffi 接入）。

## 必须阅读的代码位置

- docs/INDEX.md（全局功能索引）
- docs/CONTRACT.md（权威契约，含 API 分组 + 文档索引）
- docs/PROXY-TRACEABILITY.md（代理池依据 + 删除决策）
- internal/service/proxy/import.go（导入解析核心）
- internal/service/proxy/lease.go（租约）
- internal/service/proxy/probe.go（纯净度探测引擎）
- internal/service/proxy/geoip.go（GeoLite2 懒加载/热更新）
- internal/apiserver/proxy/proxy.go（14 路由）
- 参考源: codex-auto-register-macos/app/backend/{main.py:2297-2483, resource_service.py, probe_store.py, proxy_subscription_service.py, geoip_lookup.py}

## Git

- GPT-GO 尚未 git init（新项目）。
