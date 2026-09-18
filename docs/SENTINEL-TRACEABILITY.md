# Sentinel（CDK SDK）版本检测接口迁移依据索引

> 本文件是 sentinel 模块每个「接口→实现」的迁移依据，供下次程序/人定位。
> 参考源根：`/Users/iceman/Documents/workspace/codex-auto-register-macos/app/backend`
> Go 根：`/Users/iceman/Documents/workspace/GPT-GO`

---

## 一、接口 → 实现映射总表

| # | 接口 | 功能点 | Python 路由 | Go 路由 | Go 业务 | 参考业务 |
|---|------|--------|------------|---------|---------|---------|
| S1 | GET /api/sentinel/config | 读取检测配置（enabled/interval_hours/proxy），异常回退默认+error | main.py:473 | apiserver/sentinel/sentinel.go:46 | service/sentinel/service.go:67 | sentinel_version_scheduler.py:81 |
| S2 | PUT /api/sentinel/config | 保存配置（enabled bool、interval 1-720、proxy strip），热重启调度 | main.py:481 | apiserver/sentinel/sentinel.go:61 | service/sentinel/service.go:80 | sentinel_version_scheduler.py:100 |
| S3 | GET /api/sentinel/version | SDK 版本 + 最近检测结果投影 | main.py:489 | apiserver/sentinel/sentinel.go:81 | service/sentinel/service.go:108 | sentinel_version_scheduler.py:286 |

## 二、数据模型映射

| 模型 | Python 位置 | Go 位置 | 字段 |
|------|------------|---------|------|
| 配置(dict) | sentinel_version_scheduler.py:32 DEFAULT_CONFIG | model/sentinel.go:33 | enabled, interval_hours, proxy |
| 配置输入(dict) | main.py:482 payload | model/sentinel.go:48 | enabled?, interval_hours?, proxy? |
| 检测结果(dict) | sentinel_version_scheduler.py:178 _check_once | model/sentinel.go:56 | configured_version, is_expired, reachable, proxy_used, checked_at, error, version, url, status_code, etag, last_modified, content_length, content_type, newest_available, newest_version, discovery_method, newest_downloaded, discovery_error, newest_download_error |
| version 响应(dict) | main.py:496 base | model/sentinel.go:84 | version, url, etag, last_modified, content_length, reachable, configured_version, is_expired, last_checked_at, proxy_used, check_error |

## 三、常量映射

| 常量 | Python 位置 | Go 位置 | 值 |
|------|------------|---------|-----|
| SENTINEL_VERSION | sentinel_quickjs.py:48 | model/sentinel.go:13 | 20260810913b |
| SENTINEL_SDK_URL | sentinel_quickjs.py:49 | model/sentinel.go:20 | https://sentinel.openai.com/sentinel/{ver}/sdk.js |
| SENTINEL_REQ_URL | sentinel_quickjs.py:50 | model/sentinel.go:25 | https://sentinel.openai.com/backend-api/sentinel/req |
| frame.html URL | sentinel_quickjs.py:592 | model/sentinel.go:30 | /backend-api/sentinel/frame.html |

## 四、存储映射

| 接口 | Go 位置 | 对应 Python | 功能点 |
|------|---------|------------|--------|
| SentinelStore.LoadConfig | store/sentinel.go:29 | get_config find_one("main") | 读 sentinel_settings |
| SentinelStore.SaveConfig | store/sentinel.go:33 | save_config update_one(upsert) | 存 sentinel_settings |
| SentinelStore.AppendCheck | store/sentinel.go:36 | _persist insert + skip(50) 删旧 | 存检测结果，保留 50 条 |
| SentinelStore.LatestCheck | store/sentinel.go:41 | latest_result find_one(sort) | 读最近一次检测 |
| MockSentinelStore | store/sentinel.go:46 | (内存 mock) | 内存实现（mutex 保护） |

## 五、调度与检测映射

| 功能 | Python 位置 | Go 位置 | 说明 |
|------|------------|---------|------|
| start/stop | sentinel_version_scheduler.py:132/139 | service/sentinel/service.go:210/229 | asyncio.Task ↔ goroutine+context |
| run_once 锁 | sentinel_version_scheduler.py:169 | service/sentinel/service.go:113 | asyncio.Lock.locked ↔ runMu.TryLock |
| _resolve_proxy | sentinel_version_scheduler.py:151 | service/sentinel/service.go:193 | 配置 proxy → 代理池[0] → 直连 |
| _run 循环 | sentinel_version_scheduler.py:295 | service/sentinel/service.go:265 | 立即一次 + 每 interval 循环 |
| check_sentinel_sdk_status | sentinel_quickjs.py:454 | service/sentinel/check.go:71 | **空桩（TODO 反爬决策）** |
| fetch_latest_sentinel_version | sentinel_quickjs.py:570 | service/sentinel/check.go:91 | **空桩（TODO 反爬决策）** |
| proxy_url | plan_check_service.py:70 | service/sentinel/check.go:46 | scheme://[user:pass@]host:port |

## 六、测试映射

| 测试 | Go 位置 | 覆盖 |
|------|---------|------|
| TestGetConfigDefault | service/sentinel/service_test.go | S1 默认值 |
| TestSaveConfig / ClampLow | service/sentinel/service_test.go | S2 合并+钳制 |
| TestRunOncePersists | service/sentinel/service_test.go | run_once 落库 |
| TestStoreTrimKeepsLatest | service/sentinel/service_test.go | 保留 50 条 |
| TestStartStopLifecycle | service/sentinel/service_test.go | 调度启停 |
| TestSentinelGetConfigDefault | apiserver/sentinel_test.go | S1 HTTP |
| TestSentinelSaveConfig | apiserver/sentinel_test.go | S2 HTTP |
| TestSentinelVersionDefault | apiserver/sentinel_test.go | S3 HTTP |

## 七、本次未实现（明确边界，TODO 标记）

1. **真实网络探测**：`checkSDKStatus` / `fetchLatestVersion` 为空桩，返回 `reachable:false` + TODO 错误文案。依赖 curl_cffi 浏览器指纹（impersonate chrome）反 Cloudflare 403 的替代方案决策（见 DECISIONS.md）。
2. **真实代理池**：`ProxyProvider` 接口已定义，`mockProxyProvider` 返回空；等 T-104 proxies 迁移后接 `all_eligible_proxy_candidates`。
3. **真实 Mongo**：`MockSentinelStore`，等 T-002 Docker Mongo。

## 八、1:1 关键行为（已复刻，勿改回）

- 字段命名：sentinel 模块全程 **snake_case**（`interval_hours`/`configured_version`/`last_modified`/`proxy_used` 等），因为 Python 源码直接透传 Mongo 文档 + 内部 dict，未经 Pydantic camelCase 转换。与其他模块的 camelCase 不同，**这是客观契约，勿改**。
- 配置默认值硬编码 `{enabled:true, interval_hours:24, proxy:""}`（项目决策：不引入 env/YAML，运行时开关由 PUT config 存库热更新）。
- `interval_hours` 钳制 `[1, 720]`；`proxy` 用 `str(...).strip()`。
- config GET 异常时返回默认值 + `error` 字段（HTTP 200，非 500）；PUT 异常才 500。
- version GET：`last_checked_at` 由 `checked_at` ISO 序列化；无检测记录时 `url/etag/is_expired/last_checked_at` 为 null、`reachable/proxy_used` 为 false。
- `_resolve_proxy` 优先级：配置固定 proxy → 代理池第一个候选 → 空（直连）。
- `run_once` 锁忙时返回 skipped（不等待）；检测结果落库后 trim 到最近 50 条。
