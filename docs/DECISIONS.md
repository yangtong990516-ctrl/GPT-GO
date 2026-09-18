# DECISIONS — 技术决策与差异记录

> 权威契约见 `CONTRACT.md`。本文件记录技术选择、兼容差异、疑似缺陷与待确认问题。

## 已确定技术选择

| 编号 | 决策 | 原因 | 状态 |
|------|------|------|------|
| D1 | 项目路径 `/Users/iceman/Documents/workspace/GPT-GO`，module `gpt-go` | 用户明确固定，不可漂移 | 已定 |
| D2 | 框架 `gin` v1.10.0 | 最接近 FastAPI；v1.12 需 Go≥1.25，本机 1.24.13 故锁 v1.10.0 | 已定 |
| D3 | 配置格式 YAML（`config/config.yaml`） | 用户明确要求 | 已定 |
| D4 | YAML 库 `gopkg.in/yaml.v3` | 成熟稳定 | 已定 |
| D5 | 工具类目录 `internal/util/` | 用户明确：通用能力统一进工具类 | 已定 |
| D6 | HTTP 层包名 `apiserver` | 避免与 `net/http` 冲突 | 已定 |
| D7 | 分层：`cmd/` + `internal/{apiserver,service,store,model,config,util}` | 用户明确：每模块独立 package | 已定 |
| D8 | 邮箱池先用 mock store，Mongo 后续 docker 搭建再换真实现 | 用户明确：先 mock 推进 | 已定 |
| D9 | 存储用接口（store.EmailStore），mock 与未来 Mongo 实现同接口 | 替换 store 不影响 service/apiserver 层 | 已定 |

## 邮箱池 1:1 关键差异（已复刻，勿改回）

- source 查询参数合法值不含 remail（路由 pattern = all|standard|mailcom_alias|cloudmail|mailcode），但 email_source_filter 仍实现 remail 分支供内部调用。
- direct_mailbox_access_url 的 urlencode：key 顺序 email 在前、auth_code 在后；@ 编码为 %40（对齐 urllib.parse.urlencode 默认 quote_plus）。Go 用 queryEscape + 手工拼串（不用 url.Values.Encode，后者会按字母排序 key）。

## 兼容差异（跨语言需处理）

| 差异 | Python | Go 策略 |
|------|--------|---------|
| 空值 vs 零值 | `None`→`null` | 指针 + `omitempty` 精确区分 |
| 时间时区 | Mongo `tz_aware=True` | 统一 UTC `time.Time`（util.NowUTC） |
| JSON 字段名 | Pydantic camelCase | struct tag 显式声明 |
| 严格解析 | `extra="forbid"` | `util.Unmarshal` 用 `DisallowUnknownFields` |
| 异常→HTTP | exception_handler | `util.HTTPError` + 中间件 |
| 并发取消 | asyncio Task | goroutine + context |
| settings save 漏字段（疑似 bug） | `settings_store.py:197-211` 的 `save()` 未复制 `requireTrialOnCheck` / `autoMultiCountryProbe`，导致这两个字段永远保存为默认 false | Go `store/settings.go:Save()` 正确持久化全部字段（记录为差异，属修正而非 1:1 复刻） |

## 疑似缺陷（不夹带修复）

- 参考源 `main.py:233` 混入西里尔字符的标识符 `раs​s​wоr​d`、`:379` `u​nаu​t​hоr​i​zеd`。Go 侧用正常 ASCII 命名；对外 JSON 字段名按实际核对。
- 参考源 `main.py:175-176` 重复 import `MailcodeService`。Go 无此问题。

## 待确认问题（需用户答复）

- Q1. `curl_cffi` 的 TLS 指纹模拟在 Go 的替代方案（`tls-client` / `utls`？）——影响 T-303 等支付通道。
- Q2. `playwright` 浏览器自动化在 Go 的替代（`playwright-go` / `chromedp`？）。
- Q3. 内嵌 JS/QuickJS token 生成（`protocol_signup/_runtime`、`extract_modules/*/sentinel_engine`）在 Go 的替代（`goja`？）。
- Q4. 上述高风险项是否延后、由用户逐个决定（符合「每个迁移任务由用户决定」）。

## 依赖用途事实（用户询问「为谁服务」，已核实源码）

### playwright（浏览器自动化）为谁服务
- `extract_modules/blik/browser_nav.py` —— BLIK 真实浏览器导航
- `extract_modules/gcash_chain/payment_monitor.py` —— GCash 无头页监控
- `extract_modules/gopay/429/bypass_429.py` —— GoPay 429 绕过（Chromium）
- `extract_modules/momo/momo_channel_probe.py` / `momo_probe.py` —— MoMo 通道探测
- `paypal_agreement_protocol/paypal/manual_browser.py` —— PayPal 协议人工浏览器控制
- `paypal_agreement_protocol/tools/discover_country_fields.py` —— PayPal 国家字段探测工具
- `protocol_signup/capture.py` —— 注册抓包（引用 playwright 相关）
- 结论：**服务于「支付通道探测/提取」与「注册抓包」**，非邮箱池/账号池/代理池等基础 CRUD。

### 内嵌 JS/QuickJS（token 生成）为谁服务
- `extract_modules/blik/sentinel_engine/*` —— BLIK 的 Sentinel token（AI Platform）
- `extract_modules/gcash_chain/*` —— GCash 的 Sentinel
- `extract_modules/gopay/plus_gopay_links/gopay.py` —— GoPay
- `extract_modules/momo/*` —— MoMo（human_fingerprint/sentinel_real）
- `protocol_signup/_runtime/{sentinel,sentinel_quickjs}.py` —— 协议注册的 Sentinel token（跑真实 `sdk.js`）
- `qt_sentinel/checkout.py` —— GCash 资质检查的 Sentinel
- `main.py` / `resource_service.py` / `sentinel_version_scheduler.py` —— 引用/调度
- 结论：**服务于「支付通道的 Sentinel token 生成（反爬/风控对抗）」**，非基础资源 CRUD。

> 以上两类均属于「支付通道/注册对抗」范畴，**与邮箱池迁移无关**；邮箱池迁移不涉及 playwright 与内嵌 JS。
