# 邮箱池（Emails）迁移事实罗列

> 本文件**只罗列事实**，逐条来自参考实现源码，作为邮箱池迁移的 1:1 契约依据。
> 事实来源（只读对照）：
> - `/Users/iceman/Documents/workspace/codex-auto-register-macos/app/backend/main.py`（路由层）
> - `.../resource_models.py`（模型/枚举）
> - `.../resource_service.py`（store 方法 + service 方法 + 辅助函数）
> - `.../mailbox_client.py`（`direct_mailbox_access_url`、`MAILCOM_WEBMAIL_URL`）
> - `.../app/src/services/dataGateway.ts` + `.../app/src/types/index.ts`（前端契约）

---

## 1. 邮箱池 API 清单（7 个路由）

| # | 方法 | 路径 | 请求 | 响应 | 前端调用点 |
|---|------|------|------|------|-----------|
| E1 | GET | `/api/emails` | query: `page`(≥1,默认1) `pageSize`(10/20/50/100,默认10) `q`(≤320) `source`(all/standard/mailcom_alias/cloudmail/mailcode,默认all) `status`(all/available/reserved/failed/quarantined,默认available) | `Page[EmailRecord]` | `dataGateway.listEmails` |
| E2 | POST | `/api/emails/import` | `{"rawText": str}` | `ImportResult` | `dataGateway.importEmails` |
| E3 | POST | `/api/emails/sync-mailcom-aliases` | 无 body | `ImportResult` | `dataGateway.syncMailcomAliases` |
| E4 | POST | `/api/emails/bulk-delete` | `{"ids": [str]}`(1..10000) | `DeleteResult` | `dataGateway.deleteEmails` |
| E5 | POST | `/api/emails/reset-failed` | `{"ids": [str]|null}`(可选,≤10000) | `DeleteResult` | `dataGateway.resetFailedEmails` |
| E6 | POST | `/api/emails/{email_id}/status` | `{"status": EmailStatus}` | `EmailRecord` | `dataGateway.updateEmailStatus` |
| E7 | POST | `/api/emails/export` | `{"scope": "single"/"selected"/"all", "ids": [str]}` | `TextExport` | `dataGateway.exportEmails` |

> 注：所有 email 路由先执行 `mongo.require_online()`（Mongo 不可用 → 抛 `MongoUnavailableError` → 503）。

---

## 2. 数据模型（字段与类型，camelCase）

### 2.1 `EmailStatus`（枚举）
`available` | `reserved` | `failed` | `quarantined`

### 2.2 `EmailSourceType`（枚举）
`manual` | `mailcom_alias` | `cloudmail` | `mailcode` | `remail`

### 2.3 `EmailRecord`
| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | str | Mongo `_id` 转字符串 |
| `email` | str | 原始邮箱地址 |
| `accessUrl` | str | 经 `direct_mailbox_access_url` 处理后的访问链接 |
| `importedAt` | datetime | ISO 时间 |
| `sourceType` | EmailSourceType | 默认 `"manual"` |
| `parentEmail` | str\|null | 别名父邮箱 |
| `cloudmailRole` | "mother"\|"child"\|null | |
| `cloudmailTokenExpired` | bool | 默认 false |
| `status` | EmailStatus | 默认 available |
| `statusReason` | str\|null | |
| `statusUpdatedAt` | datetime\|null | |

### 2.4 其它模型
- `ImportResult`: `{total:int, imported:int, duplicateCount:int, errorCount:int}`
- `DeleteResult`: `{deleted:int}`
- `TextExport`: `{content:str, filename:str, count:int, format:str|null, skippedMissingCount:int, skippedExpiredCount:int}`（邮箱导出时 format=None）
- `Page[T]`: `{items:[T], total:int, page:int, pageSize:int}`

---

## 3. 行为逻辑（事实）

### 3.1 列表 `list_emails`（E1）
1. 构造 Mongo 查询：
   - `status != "all"` → `{status: <status>}`
   - 叠加 `email_source_filter(source)`（见 3.7）
   - `q` 非空 → `{emailNormalized: {$regex: re.escape(q.lower())}}`
   - 叠加 `_exclude_registered_accounts()`（见 3.6）
2. `total = count_documents(query)`
3. 查询按 `importedAt` **降序**，`skip((page-1)*pageSize)`，`limit(pageSize)`
4. 每条经 `_email_record()`（见 3.5）转 `EmailRecord`
5. 返回 `Page`（含 `total/page/pageSize/items`）

### 3.2 导入 `import_emails`（E2）
逐行处理 `rawText`（先 `lstrip("\ufeff")`）：
- 空行跳过；每行 `total += 1`
- 按 `----`（第一个分隔符）拆 `email` / `credential`
- 格式非法（email 不匹配 `EMAIL_PATTERN` 或 `URL_IMPORT_EMAIL_PATTERN`，或 credential 既非 URL 也非 mailcom 密码）→ `errorCount += 1`
- 邮箱（`normalize_email` 后）在本批已见 → `duplicateCount += 1`
- credential 是 URL → `upsert_email(key, url)`；否则 → `upsert_email(key, MAILCOM_WEBMAIL_URL, mailbox_kind="mailcom_imap", mailbox_password=credential)`
- `upsert_email` 返回 `inserted`：true→`imported += 1`；false→`duplicateCount += 1`

### 3.3 同步 mailcom 别名 `sync_mailcom_aliases`（E3）
1. `httpx.AsyncClient(trust_env=False, timeout=10)` GET `http://127.0.0.1:3211/api/export/registration-items`
2. 失败（HTTPError/ValueError）→ 502 `{"code":"mailcom_hub_unavailable","message":"MailCom Hub 暂时不可用，请先启动本机邮箱管理器"}`
3. `payload.items` 非 list → 502 `{"code":"mailcom_hub_invalid_response","message":"MailCom Hub 返回格式无效"}`
4. 逐 item：`isAlias is not True` 跳过；校验 `email`、`accountEmail`（父邮箱）均匹配 `EMAIL_PATTERN`，`accessUrl` 必须是 `http://127.0.0.1:3211/api/mail/latest`（host 127.0.0.1/localhost、port 3211、path `/api/mail/latest`）
5. 生成 `expected_url = "http://127.0.0.1:3211/api/mail/latest?" + urlencode({"email": email})`
6. `upsert_email(key, expected_url, source_type="mailcom_alias", parent_email=accountEmail)`
7. 统计同 3.2（total/imported/duplicateCount/errorCount）

### 3.4 删除 / 重置 / 改状态
- `bulk-delete`（E4）：`delete_many({_id: {$in: ids}})` → `{deleted: deleted_count}`
- `reset-failed`（E5）：`update_many({status:"failed"}[,_id:{$in:ids}], {$set:{status:"available"}, $unset:{reservedBy,reservedAt,statusReason}})` → `{deleted: modified_count}`（注意：字段名是 `deleted` 但值是 modified_count）
- `{email_id}/status`（E6）：`find_one_and_update({_id}, {$set:{status,statusReason,statusUpdatedAt,errorCode}, $unset:{reservedBy,reservedAt}}, AFTER)`；找不到 → `ResourceNotFoundError("邮箱不存在")` → 404

### 3.5 `_email_record`（文档 → EmailRecord）
- `id` = `str(_id)`
- `accessUrl` = `direct_mailbox_access_url(accessUrl, email)`
- `sourceType`：cloudmail/mailcom_alias/mailcode/remail 按原值，否则 "manual"
- `parentEmail`：`str(...)` or None
- `cloudmailRole`：仅在 mother/child 时保留，否则 None
- `cloudmailTokenExpired`：sourceType==cloudmail 且 latest token 存在时，比较 accessUrl 内嵌 token 与最新 token
- `status`：合法枚举值则保留，否则 available
- `statusReason`/`statusUpdatedAt`：原样（可 None）

### 3.6 `_exclude_registered_accounts`
取 `accounts.distinct("emailNormalized")`（仅 string），非空则查询追加 `{$nin: normalized}`（用 `$and` 包裹）。

### 3.7 `email_source_filter(source)`
- mailcom_alias → `{sourceType:"mailcom_alias"}`
- cloudmail → `{sourceType:"cloudmail"}`
- mailcode → `{sourceType:"mailcode"}`
- remail → `{sourceType:"remail"}`
- standard → `{$or:[{sourceType:{$exists:false}},{sourceType:{$nin:["mailcom_alias","cloudmail","mailcode"]}}]}`
- 其它(all) → `{}`（不过滤）

### 3.8 `upsert_email`（内部，导入/同步共用）
- `normalize_email(email)` 已在 accounts 中注册 → return False（不插入）
- 构造 document（`_id=uuid4()`, `email`, `emailNormalized`, `accessUrl`, `importedAt=utc_now()`, `status="available"`, `mailboxKind`, `sourceType`, `randomScore=random 63bit`）
- mailcom_alias 有 parent → 设 `parentEmail`；cloudmail 有 mother/child → 设 `cloudmailRole`；mailcom_imap 有密码 → 设 `mailboxPassword`
- `update_one({emailNormalized}, {$setOnInsert:...}[, $set:可变字段], upsert=True)`；mailcom_alias/cloudmail 的可变字段用 `$set` 更新
- 返回 `upserted_id is not None`

### 3.9 导出 `export_emails`（E7）
- `scope != "all"` 且 `ids` 空 → 422 `"选中导出必须提供邮箱 ID"`
- `ids = None if scope=="all" else incoming.ids`
- `emails_for_export(ids)`：仅 `status=="available"` + 排除已注册账号 + 按 importedAt 降序
- `content = "\n".join(f"{email}----{accessUrl}")`
- `filename = f"emails-{count}-mail-links-{export_timestamp()}.txt"`（`export_timestamp`=`%Y%m%d-%H%M%S`）
- 返回 `{content, filename, count, format:null}`

---

## 4. 关键常量 / 辅助

| 项 | 值 / 逻辑 |
|----|-----------|
| `EMAIL_PATTERN` | `^[^@\s]+@[^@\s]+\.[^@\s]+$` |
| `URL_IMPORT_EMAIL_PATTERN` | `^[^@]+@[^@\s]+\.[^@\s]+$` |
| `MAILCOM_WEBMAIL_URL` | `https://www.mail.com/int/` |
| `normalize_email` | `strip().lower()`，剥前缀 `邮箱：/邮箱:/email：/email:` |
| `valid_url` | scheme in {http,https} 且 netloc 非空 |
| `utc_now` | `datetime.now(timezone.utc)` |
| `direct_mailbox_access_url` | 仅当 host 在 `API798_HOSTS`（=`{api798.com, www.api798.com}`）、path 为 `/get_code` 或 `/latest` 时重写 auth_code（见 `mailbox_client.py`） |

---

## 5. 事实确认状态

- 以上 7 个 API 路径、方法、请求/响应字段、枚举、行为逻辑，均逐条取自源码，无推断。
- 前端实际调用邮箱池 API 的页面：`app/src/views/EmailsView.vue`（经 `stores/app.ts` / `dataGateway.ts`）。
- 前端测试 `dataGateway.spec.ts` 确认 `sync-mailcom-aliases` 为 `POST` 无 body，返回 `ImportResult`。

### 关键差异记录（1:1 复刻，不夹带修复）

| 差异点 | 事实 | 迁移策略 |
|--------|------|----------|
| `source` 查询参数合法值 | 路由 pattern = `^(all|standard|mailcom_alias|cloudmail|mailcode)$`，**不含 `remail`**；但 `EmailSourceType` 枚举、`email_source_filter`、前端 `EmailSource` 类型都含 `remail` | Go 路由校验**严格复刻** pattern（不含 remail）；`email_source_filter` 仍实现 remail 分支（供内部调用，与 Python 一致） |
| 前端筛选下拉 | EmailsView 仅提供 `standard`/`mailcom_alias`/`mailcode`/`all` 四个选项（无 cloudmail/remail 下拉项，类型却含） | 后端按事实实现，前端差异不影响后端契约 |
| E5 reset-failed 响应字段 | 返回 `DeleteResult`（字段名 `deleted`），但值 = `modified_count` | 复刻：字段 `deleted` 承载 modified_count |
| 西里尔混淆标识符 | `resource_models.py:208` `раs​s​wоr​d`、`resource_service.py:2946` `сrеdеn​t​iаl` | Go 用正常 ASCII 命名，对外 JSON 字段名按实际（email 池无此字段泄漏） |
