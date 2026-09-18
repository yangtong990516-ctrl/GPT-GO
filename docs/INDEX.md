# GPT-GO 功能索引（INDEX）

> 本文件是 GPT-GO 项目全部已迁移功能的统一索引，供下次程序/人快速定位模块、接口、实现位置与依据文档。
> 位置格式：`文件:行号`。各模块的详细依据见对应的 TRACEABILITY 文件。

---

## 一、模块总览

| 模块 | API 分组前缀 | 状态 | 依据文档 | 核心实现 |
|------|-------------|------|---------|---------|
| health（健康检查） | /api/health | DONE | 见 CONTRACT §7 | apiserver/server.go |
| accounts（账号池·基本信息） | /api/accounts | DONE(第1步) | ACCOUNTS-TRACEABILITY.md | service/account/service.go |
| emails（邮箱池） | /api/emails | DONE | EMAILS-TRACEABILITY.md | service/email/service.go |
| mailcode（自建 Mailcow 取件） | /api/mailcode | DONE | MAILCODE-TRACEABILITY.md | service/mailcode/service.go |
| remail（ReMail 接码平台） | /api/remail | DONE | REMAIL-TRACEABILITY.md | service/remail/service.go |
| proxies（代理池） | /api/proxies | 待迁移 | — | — |
| cloudmail（域名邮箱） | /api/cloudmail | 待迁移 | — | — |
| pipeline | /api/pipeline | 待迁移 | — | — |
| runs | /api/runs | 待迁移 | — | — |

## 二、文档索引（docs 目录）

| 文档 | 用途 |
|------|------|
| CONTRACT.md | 权威契约（路径/废弃接口/仅迁在用/YAML/包结构/API分组/工具类） |
| GOAL.md | 迁移目标、范围、验收条件 |
| TASKS.md | 任务清单与状态 |
| DECISIONS.md | 技术决策与差异记录 |
| STATE.md | 交接状态 |
| EMAILS-FACTS.md | 邮箱池事实罗列 |
| EMAILS-TRACEABILITY.md | 邮箱池接口依据索引 |
| MAILCODE-TRACEABILITY.md | mailcode 接口依据索引 |
| REMAIL-TRACEABILITY.md | remail 接口依据索引 |
| ACCOUNTS-TRACEABILITY.md | 账号池基本信息依据索引 |
| INDEX.md | 本文件（全局功能索引） |

## 三、代码分层定位

| 层 | 目录 | 说明 |
|----|------|------|
| 入口 | cmd/server/ | main（-config 指定 YAML） |
| API 层 | internal/apiserver/<域>/ | 每个业务域独立 package，一个 group 前缀 |
| 业务层 | internal/service/<域>/ | 1:1 复刻 Python 业务逻辑 |
| 存储层 | internal/store/ | 接口 + mock（Mongo 后续 docker 替换） |
| 模型层 | internal/model/ | JSON 契约（camelCase） |
| 配置层 | internal/config/ | YAML 加载 |
| 工具类 | internal/util/ | HTTP/JSON/错误/env 统一调用 |

## 四、已迁移接口清单（含行号）

### health
- GET /api/health → apiserver/server.go:59（health 方法）

### accounts（账号池·基本信息，3 接口，详见 ACCOUNTS-TRACEABILITY.md）
- GET /api/accounts → apiserver/accounts/accounts.go:33
- POST /api/accounts → apiserver/accounts/accounts.go:59
- POST /api/accounts/bulk-delete → apiserver/accounts/accounts.go:100

### emails（邮箱池，7 接口，详见 EMAILS-TRACEABILITY.md）
- GET /api/emails → apiserver/emails/emails.go:59
- POST /api/emails/import → apiserver/emails/emails.go:95
- POST /api/emails/sync-mailcom-aliases → apiserver/emails/emails.go:110
- POST /api/emails/bulk-delete → apiserver/emails/emails.go:125
- POST /api/emails/reset-failed → apiserver/emails/emails.go:140
- POST /api/emails/{email_id}/status → apiserver/emails/emails.go:159
- POST /api/emails/export → apiserver/emails/emails.go:179

### mailcode（自建 Mailcow，4 接口，详见 MAILCODE-TRACEABILITY.md）
- GET /api/mailcode/config → apiserver/mailcode/mailcode.go:33
- PUT /api/mailcode/config → apiserver/mailcode/mailcode.go:43
- POST /api/mailcode/probe → apiserver/mailcode/mailcode.go:71
- POST /api/mailcode/create-mailboxes → apiserver/mailcode/mailcode.go:90

### remail（ReMail 接码平台，6 接口，详见 REMAIL-TRACEABILITY.md）
- GET /api/remail/config → apiserver/remail/remail.go:35
- PUT /api/remail/config → apiserver/remail/remail.go:45
- POST /api/remail/probe → apiserver/remail/remail.go:77
- POST /api/remail/create-mailboxes → apiserver/remail/remail.go:96
- POST /api/remail/import-purchased-orders → apiserver/remail/remail.go:123
- GET /api/remail/wallet → apiserver/remail/remail.go:159

## 五、验证方式

- 单元测试：go test ./...
- 运行时对照：启动本地服务 + curl 对照线上（只读）响应。
- 1:1 校验点：JSON 字段名/顺序/null 语义/状态码/错误码与 Python 一致。

## 六、环境信息（只读探查记录）

- 线上服务器：114.134.186.92（root SSH，只读，勿改）
- 线上部署：mailcow(docker) + mailcode-api(docker:8800) + Python 后端(127.0.0.1:8000) + mongod(127.0.0.1:27017)
- mailcode 线上配置：baseUrl=https://mail.liwei-inc.com, domain=liwei-inc.com
- remail 线上配置：baseUrl=https://remail.aishop6.com, projectId=2, emailSuffix=icloud.com
