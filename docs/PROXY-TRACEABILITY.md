# Proxy（代理池）接口迁移依据索引

> 本文件是 proxy 模块每个「接口→实现」的迁移依据，供下次程序/人定位。
> 参考源根：`/Users/iceman/Documents/workspace/codex-auto-register-macos/app/backend`
> Go 根：`/Users/iceman/Documents/workspace/GPT-GO`

---

## 一、接口 → 实现映射总表（14 路由）

> 原 Python 有 17 路由，其中订阅导入（import-subscription）与 IPRocket（config/generate）
> 两个入口已按项目决策删除（见 §七「删除决策」），保留 14 个核心路由。

| # | 接口 | 功能点 | Python 路由 | Go 路由 | Go 业务 | 参考业务 |
|---|------|--------|------------|---------|---------|---------|
| P1 | GET /api/proxies | 列表（q 搜索 host/username、country 筛选、分页） | main.py:2297 | apiserver/proxy/proxy.go:57 | service/proxy/crud.go:15 | resource_service.py:1897 |
| P2 | POST /api/proxies/import | 导入解析（URL/host:port:user:pass） | main.py:2328 | apiserver/proxy/proxy.go:67 | service/proxy/import.go:16 | resource_service.py:3037 |
| P3 | GET /api/proxies/countries | 国家汇总 | main.py:2405 | apiserver/proxy/proxy.go:113 | service/proxy/crud.go:66 | resource_service.py:2209 |
| P4 | POST /api/proxies/test | 连通性+纯净度探测 | main.py:2410 | apiserver/proxy/proxy.go:123 | service/proxy/probe.go:151 | proxy_subscription_service.py:349 |
| P5 | GET /api/proxies/groups | 分组汇总 | main.py:2423 | apiserver/proxy/proxy.go:135 | service/proxy/crud.go:71 | resource_service.py:2229 |
| P6 | PATCH /api/proxies/groups | 分组改（country/group/enabled） | main.py:2428 | apiserver/proxy/proxy.go:145 | service/proxy/crud.go:76 | resource_service.py:2284 |
| P7 | DELETE /api/proxies/groups | 分组删 | main.py:2439 | apiserver/proxy/proxy.go:155 | service/proxy/crud.go:86 | resource_service.py:2304 |
| P8 | POST /api/proxies/bulk-delete | 批量删 | main.py:2447 | apiserver/proxy/proxy.go:165 | service/proxy/crud.go:51 | resource_service.py:2345 |
| P9 | DELETE /api/proxies | 清空 | main.py:2452 | apiserver/proxy/proxy.go:175 | service/proxy/crud.go:56 | resource_service.py:2349 |
| P10 | PATCH /api/proxies/{id} | 单条改 | main.py:2457 | apiserver/proxy/proxy.go:189 | service/proxy/crud.go:27 | resource_service.py:2127 |
| P11 | POST /api/proxies/{id}/status | 单条改状态 | main.py:2467 | apiserver/proxy/proxy.go:204 | service/proxy/crud.go:35 | resource_service.py:2153 |
| P12 | POST /api/proxies/restore-used | 恢复 used → available | main.py:2474 | apiserver/proxy/proxy.go:185 | service/proxy/crud.go:96 | resource_service.py:2177 |
| P13 | DELETE /api/proxies/{id} | 单条删 | main.py:2480 | apiserver/proxy/proxy.go:216 | service/proxy/crud.go:46 | resource_service.py:2341 |

## 二、数据模型映射

| 模型 | Python 位置 | Go 位置 | 字段 |
|------|------------|---------|------|
| ProxyRecord | resource_models.py:203 | model/proxy.go:ProxyRecord | id,host,port,username,password,enabled,status,latencyMs,lastCheckedAt,country,group,scheme |
| ProxyLease | probe_store.py:54 | model/proxy.go:ProxyLease | id,host,port,username,password,country,group,scheme,timezoneId,timezoneOffsetSec |
| ProxyImportInput | resource_models.py:232 | model/proxy.go:ProxyImportInput | rawText,country,group |
| ProxyUpdate | resource_models.py:502 | model/proxy.go:ProxyUpdate | enabled,country,group |
| ProxyStatusUpdateInput | resource_models.py:194 | model/proxy.go:ProxyStatusUpdateInput | status |
| RestoreUsedProxiesInput | resource_models.py:198 | model/proxy.go:RestoreUsedProxiesInput | country,group |
| ProxyCountrySummary | resource_models.py:534 | model/proxy.go:ProxyCountrySummary | country,total,enabled |
| ProxyGroupSummary | resource_models.py:540 | model/proxy.go:ProxyGroupSummary | country,group,total,enabled,available,used,quarantined,schemes |
| ProxyGroupUpdate | resource_models.py:570 | model/proxy.go:ProxyGroupUpdate | country,group,newCountry,newGroup,enabled |
| ProxyTestInput/Result | resource_models.py:551/562 | model/proxy.go:ProxyTestInput/Result | country,group,timeoutSeconds / tested,available,failed,averageLatencyMs,countries |

## 三、常量与枚举映射

| 常量 | Python 位置 | Go 位置 | 值 |
|------|------------|---------|-----|
| ProxyStatus | resource_models.py:16 | model/proxy.go | available/unknown/used/quarantined |
| ProxyScheme | resource_models.py:17 | model/proxy.go | http/https/socks5/socks5h |
| DEFAULT_PROXY_GROUP | probe_store.py:25 | model/proxy.go | 默认组 |
| LOCAL_PROXY_GROUP | probe_store.py:26 | model/proxy.go | __local_127_0_0_1_7890__ |
| SUPPORTED_PROXY_SCHEMES | resource_service.py:66 | model/proxy.go | http/https/socks5/socks5h |

## 四、纯函数映射（协议/国家/组/厂商）

| 函数 | Python 位置 | Go 位置 | 功能点 |
|------|------------|---------|--------|
| normalize_proxy_scheme | resource_service.py:69 | model.NormalizeProxyScheme | socks→socks5h |
| normalize_country_code | resource_service.py:251 | model.NormalizeCountryCode | 两位大写否则 ZZ |
| infer_proxy_country | resource_service.py:256 | model.InferProxyCountry | 正则提取 -res-VN/_area-VN |
| normalize_proxy_group | resource_service.py:158 | model.NormalizeProxyGroup | 折叠空白截 64 |
| infer_proxy_vendor | resource_service.py:80 | model.InferProxyVendor | kookeey/cliproxy/iprocket |
| infer_proxy_scheme_by_vendor | resource_service.py:106 | model.InferProxySchemeByVendor | 9595→socks5h |
| proxy_url | plan_check_service.py:70 | model.ProxyURL | scheme://[user:pass@]host:port |
| PROXY_COUNTRY_PATTERN | resource_service.py:62 | model.proxyCountryPattern | 正则 |

## 五、存储映射

| 接口 | Go 位置 | 对应 Python | 功能点 |
|------|---------|------------|--------|
| ProxyStore.List | store/proxy.go | list_proxies | 分页+筛选 |
| ProxyStore.Upsert | store/proxy.go | upsert_proxy | 身份去重 upsert |
| ProxyStore.Update/SetStatus | store/proxy.go | update_proxy/set_proxy_status | 单条改 |
| ProxyStore.DeleteOne/Many/Clear | store/proxy.go | delete_proxy(s)/clear_proxies | 删 |
| ProxyStore.CountrySummaries/GroupSummaries | store/proxy.go | proxy_country/group_summaries | 汇总 |
| ProxyStore.UpdateGroup/DeleteGroup | store/proxy.go | update/delete_proxy_group | 分组 |
| ProxyStore.RestoreUsed | store/proxy.go | restore_used_proxies | used→available |
| ProxyStore.AllEligibleProxyCandidates | store/proxy.go | all_eligible_proxy_candidates | 候选 |
| ProxyStore.Acquire/Release/Consume/Heartbeat | store/proxy.go | acquire/release/consume/heartbeat_proxy | 租约 |
| ProxyStore.RecordProxyTest | store/proxy.go | record_proxy_test | 探测落库 |

## 六、探测引擎映射（纯净度检测，已实现）

| 功能 | Python 位置 | Go 位置 | 说明 |
|------|------------|---------|------|
| cdn-cgi/trace 探测 | proxy_subscription_service.py:453 | service/proxy/probe.go:Probe | GET chatgpt.com/cdn-cgi/trace 拿 ip/loc/sliver |
| 纯净度分级 | proxy_subscription_service.py:486 | service/proxy/probe.go:gradePurity | none/tier1→clean；datacenter/cloud/hosting/vpn/proxy/tier2-5/tunnel→dirty |
| GeoLite2 查询 | geoip_lookup.py:237 | service/proxy/geoip.go:lookupGeoIP | 国家+IANA时区，懒加载+热更新+缺失降级 |
| 时区偏移 | proxy_subscription_service.py:520 | service/proxy/probe.go:tzOffset | ZoneInfo → time.LoadLocation |

**探测引擎核心（纯净度检测）原理**：通过代理访问 Cloudflare 公开的 `cdn-cgi/trace`，拿到 `sliver` 风控分级字段——Cloudflare 已免费算好"这个 IP 是住宅还是数据中心"，后端只需解析字符串判断干净/脏，无需自建 ASN 库。`/api/proxies/test` 会遍历可用代理逐个探测，脏代理自动累积 `consecutiveFailures` 并最终隔离。

## 七、删除决策（本次裁剪）

| 删除项 | Python 路由 | 原因 |
|--------|------------|------|
| 订阅导入（easy-proxies/resin） | POST /api/proxies/import-subscription | 依赖本机部署的第三方管理器（easy-proxies/resin），本地未使用机场订阅场景 |
| IPRocket 配置 | GET/PUT /api/proxies/iprocket/config | IPRocket 账号配置（generate 的前置），随 generate 一并删除 |
| IPRocket 生成 | POST /api/proxies/iprocket/generate | 自动生成 -res-{CC} 住宅代理；用户已通过手动 import（host:port:user:pass）管理 IPRocket 代理，绕过生成 |

> 注意：`InferProxyVendor` / `InferProxySchemeByVendor`（含 9595→socks5h 厂商协议纠正）**保留**，
> 因为手动粘贴 iprocket 代理仍需按厂商+端口纠正协议，否则 http 连不上 socks5 端口。

## 八、GeoLite2 依赖说明

- Go 库：`github.com/oschwald/geoip2-golang`（MaxMind mmdb 读取）。
- 数据库文件：`GeoLite2-City.mmdb`（67MB，Git LFS 托管）。
- 拉取：`git lfs install && git lfs pull`（已在参考源项目执行）。
- GPT-GO 侧：软链接 `data/geoip/GeoLite2-City.mmdb` → 参考源真实文件。
- 运行时路径解析：`AUTOREGISTER_GEOIP_DB` 环境变量 → 从 cwd 向上搜索 `data/geoip/` → `/data/geoip/`。
- 缺失降级：文件不存在时 GeoIP 返回 nil，探测国家降级到 cdn-cgi/trace 的 `loc` 字段（与 Python 一致）。

## 九、1:1 关键行为（已复刻，勿改回）

- 字段命名 camelCase（与其他资源模块一致）。
- `host:port:user:pass`（无 `://` 无 `@`）默认 http；`user:pass@host:port` 默认 socks5。
- IPRocket 9595/59999/619999 端口厂商纠正为 socks5h；Kookeey 一律 http；Cliproxy 一律 socks5h。
- 国家推断正则命中 username 或 host，否则 ZZ（列表时再回退推断）。
- `socks5/socks` 一律规范化 socks5h（远程 DNS）。
- 导入身份去重键 = `(host.lower(), port, username, password, scheme)`。
- `proxy_url` 凭证用 `quote(..., safe='')` 全量百分号编码（空格→%20）。
- 独占代理策略：acquire 只选 available；consume 标记 used；restore-used 手动放回。
- 租约排他：leaseOwner 空或 leaseUntil 过期才可选；排序 activeLeaseCount asc → randomScore desc → _id asc。
- 探测失败累积 consecutiveFailures，≥3 才隔离 quarantined；单次超时不误杀。

## 十、测试映射

| 测试 | Go 位置 | 覆盖 |
|------|---------|------|
| TestImportProxiesIpipbright | service/proxy/import_test.go | P2 http+country VN |
| TestImportProxiesIprocket | service/proxy/import_test.go | P2 socks5h 9595 |
| TestImportDuplicate | service/proxy/import_test.go | P2 去重 |
| TestImportMixedAndCandidates | service/proxy/import_test.go | 候选查询 |
| TestProxyURL | service/proxy/import_test.go | proxy_url 格式 |
| TestAcquireLease | service/proxy/import_test.go | 租约周期 |
| TestGradePurity | service/proxy/probe_test.go | 纯净度分级 |
| TestParseTrace | service/proxy/probe_test.go | trace 字段解析 |
| TestProxyImportAndList | apiserver/proxy_test.go | P1+P2 |
| TestProxyCountriesAndGroups | apiserver/proxy_test.go | P3+P5 |
| TestProxyDelete | apiserver/proxy_test.go | P13 |
| TestProxyTestStub | apiserver/proxy_test.go | P4 空池 |
