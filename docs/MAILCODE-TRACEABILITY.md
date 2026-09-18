# Mailcode（自建 Mailcow + mail.example.com）接口迁移依据索引

> 本文件是 mailcode 模块每个接口的迁移依据，供下次程序/人直接定位源码位置并了解功能点。
> 位置格式：`文件:行号`。参考源为只读对照，Go 为迁移产物。

参考源根：`/Users/iceman/Documents/workspace/codex-auto-register-macos/app/backend`
Go 根：`/Users/iceman/Documents/workspace/GPT-GO`

---

## 一、接口 → 实现映射总表

| # | 接口 | 功能点 | Python 路由 | Go 路由 | Go 业务 | 参考业务 |
|---|------|--------|------------|---------|---------|---------|
| M1 | GET /api/mailcode/config | 读取自建 Mailcow 取件配置（baseUrl/domain/updatedAt） | main.py:2218 | apiserver/mailcode/mailcode.go:33 | service/mailcode/service.go:68 | mailcode_service.py:82 |
| M2 | PUT /api/mailcode/config | 保存配置（baseUrl 去尾斜杠，domain 规范化去@前缀） | main.py:2222 | apiserver/mailcode/mailcode.go:43 | service/mailcode/service.go:89 | mailcode_service.py:90 |
| M3 | POST /api/mailcode/probe | 无副作用连通性测试（GET /health） | main.py:2226 | apiserver/mailcode/mailcode.go:71 | service/mailcode/service.go:115 | mailcode_service.py:112 |
| M4 | POST /api/mailcode/create-mailboxes | 批量生成/导入邮箱到邮箱池（accessUrl 指向 mailcode-api） | main.py:2230 | apiserver/mailcode/mailcode.go:90 | service/mailcode/service.go:150 | mailcode_service.py:151 |

## 二、数据模型映射

| 模型 | Python 位置 | Go 位置 | 字段 |
|------|------------|---------|------|
| MailcodeConfig | resource_models.py:833 | model/mailcode.go:12 | baseUrl, domain, updatedAt(nullable) |
| MailcodeConfigInput | resource_models.py:841 | model/mailcode.go:18 | baseUrl(8-320, https://), domain(<=128) |
| MailcodeMailboxCreate | resource_models.py:863 | model/mailcode.go:24 | emails(<=200), count(0-200), prefix(<=64), domain(<=128) |
| MailcodeProbeInput | resource_models.py:871 | model/mailcode.go:31 | baseUrl(<=320) |
| MailcodeProbeResult | resource_models.py:875 | model/mailcode.go:37 | ok, message, reachable |
| MailcodeMailboxRecord | resource_models.py:881 | model/mailcode.go:44 | email, accessUrl, imported, duplicate, error(nullable) |

## 三、辅助函数映射

| 函数 | Python 位置 | Go 位置 | 功能点 |
|------|------------|---------|--------|
| normalize_email | mailcode_service.py:50 | service/mailcode/service.go:250 | strip+lower |
| build_access_url | mailcode_service.py:106 | service/mailcode/service.go:105 | base+/api/mail?email=<urlencode> |
| _generate_mailbox_names | mailcode_service.py:134 | service/mailcode/service.go:213 | {prefix}-{8小写}-{6hex}@domain |
| normalizeDomain | mailcode_service.py:90(内联) | service/mailcode/service.go:241 | 去@前缀，取@后真实域名 |
| utc_now | mailcode_service.py:46 | (Go time.Now().UTC()) | UTC 时间 |

## 四、常量映射

| 常量 | Python 位置 | Go 位置 | 值 |
|------|------------|---------|-----|
| CONFIG_KEY | mailcode_service.py:40 | service/mailcode/service.go:20 | mailcode_config |
| DEFAULT_BASE_URL | mailcode_service.py:42 | service/mailcode/service.go:21 | https://mail.example.com |
| DEFAULT_DOMAIN | mailcode_service.py:43 | service/mailcode/service.go:22 | example.com |

## 五、存储映射

| 接口 | Go 位置 | 对应 Python | 功能点 |
|------|---------|------------|--------|
| MailcodeStore.Load | store/mailcode.go:18 | _load_document 的 find_one | 读 mailcode_settings |
| MailcodeStore.Save | store/mailcode.go:20 | save_config 的 replace_one(upsert) | 存 mailcode_settings |
| MockMailcodeStore | store/mailcode.go:29 | (内存 mock) | 内存实现 |

邮箱 upsert 复用：mailcode 通过 `email.Service.UpsertMailbox`（service/email/service.go）插入邮箱池，`source_type="mailcode"`、`mailbox_kind="url"`，不重复实现 upsert 逻辑（符合 CONTRACT 第 5 条「不重复代码」）。

## 六、测试映射

| 测试 | Go 位置 | 覆盖 |
|------|---------|------|
| TestGetConfigDefaults | service/mailcode/service_test.go | M1 默认值 |
| TestSaveConfigNormalizes | service/mailcode/service_test.go | M2 去斜杠+去@前缀 |
| TestSaveConfigDomainFromEmail | service/mailcode/service_test.go | M2 admin@example.com→example.com |
| TestBuildAccessURL | service/mailcode/service_test.go | accessUrl urlencode |
| TestProbeOK / TestProbeNon200 / TestProbeUnreachable | service/mailcode/service_test.go | M3 三种结果 |
| TestCreateMailboxesByCount / ByEmails / Empty | service/mailcode/service_test.go | M4 生成/导入/422 |
| TestGenerateMailboxNamesFormat | service/mailcode/service_test.go | 名称格式 |
| TestMailcode* | apiserver/mailcode_test.go | 4 路由 + 校验 |

## 七、线上事实（只读探查 114.134.186.92，勿改动）

- 线上部署：mailcow(docker) + mailcode-api(docker, 端口 8800) + Python 后端(裸进程 127.0.0.1:8000) + mongod(裸进程 127.0.0.1:27017)。
- mailcode-api 环境变量：IMAP_HOST=dovecot, IMAP_PORT=993, IMAP_USER=catchall@liwei-inc.com, CODE_LEN=6, SCAN_MINUTES=10。
- mailcode-api 只读探测：
  - GET /health → `{"status":"ok"}`（HTTP 200）
  - GET /api/mail?email=probe-nonexist@example.com → `{"code":null,"error":"no_recent_mail","success":false}`
- MongoDB mailcode_settings（只读）：`{_id:"mailcode_config", baseUrl:"https://mail.liwei-inc.com", domain:"liwei-inc.com", updatedAt:2026-09-07T16:45:14.636Z}`
- 线上后端 GET /api/mailcode/config → `{"baseUrl":"https://mail.liwei-inc.com","domain":"liwei-inc.com","updatedAt":"2026-09-07T16:45:14.636000Z"}`
- 线上后端 POST /api/mailcode/probe(baseUrl=https://mail.liwei-inc.com) → `{"ok":true,"message":"mailcode-api 连接正常","reachable":true}`

## 八、1:1 关键行为（已复刻，勿改回）

- probe 只看 HTTP 状态码==200 判定 ok（不解析 body 的 status 字段）；HTTP 有响应但非 200 时 reachable=true、ok=false；连接异常时 reachable=false、ok=false。
- create_mailboxes 的 domain 规范化：`.strip().lower().lstrip("@")`，若含 @ 取最后 @ 后真实域名（兼容配置误存邮箱地址）。
- _generate_mailbox_names 名称格式：`{prefix}-{8位小写字母}-{6位hex}@domain`（secrets.choice + secrets.token_hex(3)）；无 prefix 时省略前缀段。
- create_mailboxes 每个邮箱 upsert 前 sleep 0.2s（interval）。
- 空 emails 且 count=0 时抛 mailcode_no_emails 422。
