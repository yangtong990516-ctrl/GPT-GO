# 邮箱池接口迁移依据索引（TRACEABILITY）

> 本文件是**每个接口的迁移依据**，供下次程序/人直接定位源码位置并了解功能点。
> 格式说明：
> - 每条记录含：接口（方法+路径）、功能点、Python 参考位置（文件:行号）、Go 实现位置（文件:行号）、关联模型/存储。
> - 位置均用 `文件:起始行号` 表示，行号基于当前仓库快照，可 grep 直接定位。
> - 配套契约详情见 `EMAILS-FACTS.md`；技术决策见 `DECISIONS.md`。

参考源根目录：`/Users/iceman/Documents/workspace/codex-auto-register-macos/app/backend`
Go 根目录：`/Users/iceman/Documents/workspace/GPT-GO`

---

## 一、接口 → 实现 映射总表

| # | 接口 | 功能点 | Python 路由 | Go 路由 | Go 业务 | 参考业务 |
|---|------|--------|------------|---------|---------|---------|
| E1 | GET /api/emails | 分页列出邮箱（page/pageSize/q/source/status 筛选，importedAt 降序，排除已注册账号） | main.py:2145 | apiserver/emails/emails.go:59 | service/email/service.go:44 | resource_service.py:1506 |
| E2 | POST /api/emails/import | 批量导入邮箱（email----credential 格式，URL 或 mailcom 密码） | main.py:2158 | apiserver/emails/emails.go:95 | service/email/service.go:106 | resource_service.py:2934 |
| E3 | POST /api/emails/sync-mailcom-aliases | 从 MailCom Hub(127.0.0.1:3211) 同步别名邮箱 | main.py:2163 | apiserver/emails/emails.go:110 | service/email/service.go:168 | resource_service.py:2984 |
| E4 | POST /api/emails/bulk-delete | 按 ids 批量删除邮箱 | main.py:2192 | apiserver/emails/emails.go:125 | service/email/service.go:97 | resource_service.py:1675 |
| E5 | POST /api/emails/reset-failed | 失败邮箱重置为可用（deleted 字段承载 modified_count） | main.py:2197 | apiserver/emails/emails.go:140 | service/email/service.go:88 | resource_service.py:1583 |
| E6 | POST /api/emails/{email_id}/status | 修改单个邮箱状态 | main.py:2205 | apiserver/emails/emails.go:159 | service/email/service.go:78 | resource_service.py:1539 |
| E7 | POST /api/emails/export | 导出可用邮箱（email----accessUrl，文件名带时间戳） | main.py:2210 | apiserver/emails/emails.go:179 | service/email/service.go:218 | resource_service.py:3222 |

## 二、数据模型映射

| 模型 | Python 位置 | Go 位置 | 说明 |
|------|------------|---------|------|
| EmailStatus | resource_models.py:169 | model/email.go:7 | 枚举 available/reserved/failed/quarantined |
| EmailSourceType | resource_models.py:20 | model/email.go:26 | manual/mailcom_alias/cloudmail/mailcode/remail |
| EmailRecord | resource_models.py:176 | model/email.go:37 | 邮箱记录（11 字段） |
| EmailStatusUpdateInput | resource_models.py:190 | model/email.go:52 | 改状态入参 |
| EmailExportInput | resource_models.py:488 | model/email.go:57 | 导出入参 |
| RawImportInput | resource_models.py:228 | model/email.go:63 | 导入入参 rawText |
| BulkIdsInput | resource_models.py:365 | model/email.go:68 | 批量 ids |
| ResetFailedEmailsInput | resource_models.py:369 | model/email.go:73 | 重置入参 ids(可空) |
| ImportResult | resource_models.py:253 | model/email.go:78 | 导入结果 |
| DeleteResult | resource_models.py:478 | model/email.go:86 | 删除结果 deleted |
| TextExport | resource_models.py:493 | model/email.go:91 | 导出结果 |
| Page[T] | resource_models.py:221 | model/email.go:120 | 分页 |
| PageSize | resource_models.py:11 | model/email.go:101 | 10/20/50/100 |

## 三、辅助函数映射

| 函数 | Python 位置 | Go 位置 | 功能点 |
|------|------------|---------|--------|
| normalize_email | resource_service.py:148 | service/email/service.go:394 | 邮箱规范化（小写+去前缀标签） |
| valid_url | resource_service.py:177 | service/email/service.go:406 | 校验 http/https URL |
| email_source_filter | resource_service.py:185 | store/mock_email.go:121 | source 筛选（standard 排除 mailcom_alias/cloudmail/mailcode） |
| export_timestamp | resource_service.py:247 | service/email/service.go:432 | 导出文件名时间戳 %Y%m%d-%H%M%S |
| utc_now | resource_service.py:144 | (Go 用 time.Now().UTC()) | UTC 当前时间 |
| upsert_email | resource_service.py:1604 | service/email/service.go:246 | 按 emailNormalized upsert |
| _email_record | resource_service.py:2538 | service/email/service.go:292 | Mongo 文档 → EmailRecord |
| _exclude_registered_accounts | resource_service.py:2602 | store/mock_email.go(registeredNormalized) | 排除已注册账号 |
| direct_mailbox_access_url | mailbox_client.py:94 | service/email/service.go:348 | api798 访问链接重写 |
| _latest_cloudmail_token | resource_service.py:2590 | store/mock_email.go:180 | 取最新 cloudmail token |

## 四、常量映射

| 常量 | Python 位置 | Go 位置 | 值 |
|------|------------|---------|-----|
| EMAIL_PATTERN | resource_service.py:59 | service/email/service.go(变量 emailPattern) | ^[^@\s]+@[^@\s]+\.[^@\s]+$ |
| URL_IMPORT_EMAIL_PATTERN | resource_service.py:61 | service/email/service.go(urlImportEmailPattern) | ^[^@]+@[^@\s]+\.[^@\s]+$ |
| MAILCOM_WEBMAIL_URL | mailbox_client.py:38 | service/email/service.go(mailcomWebmailURL) | https://www.mail.com/int/ |
| API798_HOSTS | mailbox_client.py:30 | service/email/service.go(api798Hosts) | {api798.com, www.api798.com} |

## 五、存储接口映射

| 接口方法 | Go 位置 | 对应 Python 逻辑 | 功能点 |
|---------|---------|-----------------|--------|
| EmailStore.List | store/store.go | list_emails 的 Mongo find | 分页+筛选+排序 |
| EmailStore.SetStatus | store/store.go | set_email_status 的 find_one_and_update | 改状态+清 reservedBy |
| EmailStore.ResetFailed | store/store.go | reset_failed_emails 的 update_many | 重置失败 |
| EmailStore.Delete | store/store.go | delete_emails 的 delete_many | 删除 |
| EmailStore.Upsert | store/store.go | upsert_email 的 update_one(upsert) | 插入/去重 |
| EmailStore.ListForExport | store/store.go | emails_for_export | 导出列表 |
| EmailStore.RegisteredEmails | store/store.go | _exclude_registered_accounts 的 distinct | 已注册账号 |
| EmailStore.LatestCloudmailToken | store/store.go | _latest_cloudmail_token | cloudmail token |

mock 实现：`store/mock_email.go`（Mongo 后续 docker 搭建后新增真实现，实现同一 EmailStore 接口）。

## 六、测试映射（验证依据）

| 测试 | Go 位置 | 覆盖接口/功能 |
|------|---------|--------------|
| TestImport | service/email/service_test.go | E2 导入（URL+密码+重复+错误） |
| TestImportBOMAndLabels | service/email/service_test.go | E2 BOM+前缀标签剥离 |
| TestImportSkipsRegistered | service/email/service_test.go | E2 已注册账号跳过 |
| TestListFiltering | service/email/service_test.go | E1 筛选（status/source） |
| TestListOrdering | service/email/service_test.go | E1 importedAt 降序 |
| TestExport | service/email/service_test.go | E7 导出（仅 available） |
| TestSetStatusNotFound | service/email/service_test.go | E6 404 |
| TestSyncMailcomAliases | service/email/sync_test.go | E3 同步解析 |
| TestDirectMailboxAccessURL | service/email/sync_test.go | direct_mailbox_access_url 重写 |
| TestEmailsListEmpty | apiserver/emails_test.go | E1 空列表 |
| TestEmailsListInvalidSource | apiserver/emails_test.go | E1 source=remail 422 |
| TestEmailsImportAndList | apiserver/emails_test.go | E2+E1 往返 |
| TestEmailsBulkDelete | apiserver/emails_test.go | E4 删除 |
| TestEmailsResetFailed | apiserver/emails_test.go | E5 重置 |
| TestEmailsUpdateStatusNotFound | apiserver/emails_test.go | E6 404 |
| TestEmailsExportNoIDs | apiserver/emails_test.go | E7 缺 ids 422 |
