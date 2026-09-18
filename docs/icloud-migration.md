# iCloud-Privacy-Mail-v2 → GPT-GO 迁移文档

> 迁移自 `/Users/iceman/Documents/workspace/iCloud-Privacy-Mail-v2`(Go + Vue3 + SQLite)。
> 目标:把源项目**全部功能**迁入 GPT-GO(Go + React/shadcn + MongoDB),路由独立分组 `/api/icloud`,统一 MongoDB 存储;前端用当前项目风格重设计,布局对齐原项目。

## 一、交付总览

| 维度 | 结果 |
|---|---|
| 后端 Go 包 | `internal/icloud/{domain,config,protocol,auth,serverchan,updatecheck,buildinfo,apple,mailbox,scheduler,mailwatcher,store,httpapi}` + `internal/icloud/bootstrap.go` |
| 后端路由 | `/api/icloud` 分组,**69 条与原项目 1:1 完全一致**(公开 4 + 后台 49 + 公共 v1 16),逐条 diff 零遗漏 |
| 存储 | 全部迁移到 MongoDB(`autoregister` 库),12 个集合替代原 12 张 SQLite 表 |
| 前端 | 侧边栏「iCloud 邮箱」导航,**8 个功能 Tab**(控制台 / Apple 账号 / 邮箱池 / 创建隐私邮箱 / 本地导出 / 系统设置 / 取码工具 / 事件日志),布局对齐原项目,UI 用 GPT-GO shadcn 风格 |
| 前端功能 | 对照规格 `frontend-spec.md` 逐点 review:批量删除、全部彻底删除 Apple 邮件、远程清理、批量解析、批量同步邮件、创建隐私邮箱渠道回退、任务概览、调度器、Server酱、数据库维护、导出(4 项)、公共取码、单封邮件详情**全部完整迁移** |
| 渠道回退 | 创建隐私邮箱 / 创建任务 / 调度设置三处 `channel=auto` 标注「自动接口:新接口优先,失败用旧接口」;后端 `mailbox/service.go` 完整迁移 apple_account 失败回退 icloud_web、IMAP 失败回退 Web API 的逻辑(FallbackUsed / Fallbacks) |
| 登录 | 全局控制台一道密码 **admin / 1024**,cookie `gptgo_session` 全站贯通,iCloud 不再单独要密码 |
| 验证 | `go build/vet/test ./...` 全绿、`web npm run build` 通过、运行时 12 后台接口 + 公共 v1 API + SPA 路由全部 200 / 鉴权正确 |

## 一·五、全量迁移复审(2026-09-18)

针对「功能未全量接入」的反馈做了完整复审与补齐:

1. **后端**:逐条 diff 原项目 69 条路由,GPT-GO 69 条完全一致,零遗漏;channel 回退、彻底删除、远程清理、批量解析/同步等逻辑在 service 层逐行迁移。
2. **前端**:以 `frontend-spec.md`(原 Vue 全部视图功能规格,约 590 行)为蓝本,React 重建 8 个视图。复审发现的 4 项实质遗漏已补齐:
   - events-tab:补**级别筛选**(全部 / 信息 / 警告 / 错误下拉)。
   - settings-tab:补**邮件后台监听状态行**(显示 IMAP x/y｜Web z｜同步 n)。
   - settings-tab:补 **Server酱 SendKey 已配置值脱敏回显**(`server_chan_send_key_masked`)。
   - public-code-tab:补**邮件 HTML 安全清洗**(复用 shared 的 `buildEmailHTMLDocument`,与 mailboxes-tab 一致)。
3. **公开 v1 API**(`/api/icloud/v1/*`):16 条全部保留且功能完整,**用 API key 鉴权(`?key=` / `X-арi_keys` / `Authorization: Bearer`),不走全局控制台密码**——这是保护外部程序能调用,绝非删除。
4. 复审确认:除已补项外核心功能**无遗漏**;轻微偏差(SSE 用 react-query 轮询替代、个别文案微调)不影响功能完整性。

## 二、后端架构与包职责

```
internal/icloud/
├── bootstrap.go     模块装配:Open(连 Mongo+seed admin+组 Server)/Register(挂 /api/icloud)/StartBackground/Close
├── domain/          领域模型(原样迁移 model.go,312 行)
├── config/          业务配置(删除 sqlite 的 host/port/data_path/backup_dir,保留协议/后台/公共 API 字段)
├── protocol/        Apple/iCloud 协议层(零第三方依赖):SRP 登录、iCloud Web 客户端、IMAP 客户端、OTP 提取
├── store/           【重写】MongoDB 数据访问层(60+ 方法)
├── auth/            cookie-session 鉴权(PBKDF2-SHA256 120000 迭代)
├── apple/           Apple 账号登录/2FA/登录态检测/IMAP 凭据/保活
├── mailbox/         隐私邮箱创建/导入/同步/取码/清理/正文补全(含 channel 回退)
├── scheduler/       定时批量创建隐私邮箱
├── mailwatcher/     邮件后台监听(IMAP IDLE + Web 轮询)
├── serverchan/      Server 酱推送
├── updatecheck/     版本更新检查(依赖 buildinfo + embed announcements.json)
├── buildinfo/       版本信息
└── httpapi/         【重写】gin HTTP 层(server/public/lease/realtime/serverchan/database/update)
```

迁移顺序(依赖自底向上):config → domain → store → protocol → auth/serverchan/updatecheck → apple → mailbox → scheduler → mailwatcher → httpapi → bootstrap。

## 三、SQLite → MongoDB 关键决策

1. **集合映射**:12 表 → 12 集合(admins/web_sessions/apple_accounts/mailboxes/mailbox_leases/messages/events/settings/create_settings/icloud_sessions/change_log/counters)+ maintenance_runs。
2. **文档结构**:每集合 `{_id: <实体ID>, ...实体字段}`,字段名 snake_case;读写用「json 中间表示」(`docToJSON`/`jsonToDoc`)保证 json tag 语义一致。
3. **ID 生成**:原 `metadata.next_id` 计数器 → `counters` 集合 `FindOneAndUpdate($inc)`,格式 `prefix_000001`。
4. **敏感字段加密**:复刻 AES-GCM secretCodec(`enc:v1:` 前缀);密钥从 `<db>.key` 文件改为存 `metadata.secret_key`(base64)。
5. **事务**:放弃多文档事务(单机 Mongo 无副本集不支持),改为单文档原子 + 应用层幂等(读旧值 bytes.Equal 无变化不产 Change)。租约 claim 的 available 占位用 `FindOneAndUpdate({status:available, active_lease_id:""})` 单文档原子占位。
6. **change_log / SSE**:change_log 集合 + `counters.change_seq` 提供递增序号;`/api/icloud/realtime` 用 change 事件 + `Last-Event-ID` 续传 + 15s 心跳。
7. **数据库维护**:IntegrityCheck→ping ok;Checkpoint/Vacuum→no-op;Backup→每集合导出解密明文 JSON(0600 权限);Prune(过期邮件清理)保留;备份目录 `data/icloud-backups`,保留 3 份。

## 四、登录设计(全局控制台密码,admin/1024)

**最终方案(v2,2026-09-18):整个 GPT-GO 控制台共用一道登录门禁,而非 iCloud 单独一套。**

- **单一全局密码**:管理员账号 `admin`,默认密码 `1024`(可在「系统设置 → 控制台密码」修改)。密码 PBKDF2-SHA256(120000 迭代)哈希存 Mongo `admins` 集合,会话存 `web_sessions` 集合——均复用 icloud 模块的 `auth.Service` 托管。
- **一次登录全站贯通**:登录成功发全局 cookie `gptgo_session`(HttpOnly + SameSite=Strict,7 天)。主站所有业务 API(`/api/accounts`、`/api/emails`、`/api/runs` 等)与 iCloud 后台 API(`/api/icloud/*`)共用此 cookie。
- **后端**:
  - 全局登录路由 `/api/auth/{status,login,logout,password}`(`internal/apiserver/auth/auth.go`)。
  - gin 中间件 `consoleauth.Guard` 保护所有 `/api/*`,排除 `/api/auth`(登录)、`/api/health`(健康)、`/api/icloud`(iCloud 自带 protected 读同一 cookie;其 `/v1` 公开 API 用 API key 鉴权)。
  - **只拦 `/api/` 前缀**,前端静态页面(HTML/JS)放行,由 React 的 `ConsoleGate` 在未登录时渲染登录页(否则登录页本身进不去)。
  - 开关:`GPT_GO_AUTH_DISABLED=1` 可关闭门禁(测试用;apiserver 测试经 `main_test.go` 自动设置)。
- **iCloud 模块去掉了独立登录页/密码**:其 `protected` 中间件改读全局 cookie `gptgo_session`(原 `ipm_v2_session`),登录后直接用功能页,8 个 Tab 不再有登录卡片。
- **前端**:`components/console-gate.tsx` 顶层门禁(未登录整屏登录页);`pages/settings.tsx` 新增「控制台密码」卡片(改密码,改后所有会话失效重新登录);侧边栏加退出登录按钮。
- **iCloud 公开取码 API**(`/api/icloud/v1/mailboxes/:email/code` 等 v1 接口)**不走控制台密码**,用 API key(`?key=` / `X-арi_keys` / `Authorization: Bearer`,支持全局 key 或单邮箱 api_token)鉴权——供脚本/外部系统免登录调用。
- seed:首次启动若无管理员自动写 admin/1024(绕过 auth.Setup 的 ≥8 位强度校验,Login 只走 verifyPassword)。

## 五、前端设计

- 侧边栏新增「iCloud 邮箱」(Cloud 图标),路由 `/icloud`。
- 页面 `pages/icloud/`:`index.tsx`(PageHeader + 8 Tabs) + 8 个 Tab 组件 + `shared.tsx`。
- Tab:控制台 / Apple 账号 / 邮箱池 / 创建隐私邮箱 / 本地导出 / 系统设置 / 取码工具 / 事件日志,全部用 shadcn(Tabs/Card/Table/Dialog/AlertDialog/Switch/Select/Checkbox/Badge),布局分区对齐原项目。
- API 层:`lib/icloud-api.ts`(**独立封装** `{success,data}` 包壳请求,不与主站 `{detail:{code,message}}` 混用) + `lib/icloud-queries.ts`(react-query hооk,带 `enabled` 参数)。
- 共享:`shared.tsx` 提供 errMsg、状态徽章、ProgressBar、EmptyRow、`buildEmailHTMLDocument`(邮件 HTML 安全渲染,mailboxes 与 public-code 两 tab 共用)。

## 六、吸取的经验教训(重要)

1. **sync.RWMutex 不可重入 → 自死锁**【本次最关键 bug】
   子代理实现的 `commitChanges` 在持有 `s.mu.Lock()` 的写方法内被调用,却又执行 `s.mu.RLock()` 读 changeLogLimit,导致读写锁自死锁——登录/初始化永久卡死,但 `Admin()`(纯 RLock)正常。
   - 修复:持锁上下文直接读字段,删除重入 RLock。约定:helper 不加锁,由调用方统一持锁。

2. **mongo-driver v1.17 options API**:用函数式 `options.FindOne().SetSort(...)` / `options.Find().SetSort(...)`,不是旧的 options.FindOneOptionsBuilder 类型。

3. **大包迁移用「整文件复制 + 批量 sed 改 import」**远优于逐行手抄,配合子代理并行(protocol/service/httpapi/store 四条线),最后用 `go build ./...` 做统一契约校验。

4. **响应包壳风格差异要显式保留**:源项目 `{success,data}` 与 GPT-GO `{detail:{code,message}}` 不同,刻意让 icloud 模块保持原风格并在前端独立封装,避免「为了统一而改坏契约」。

5. **前端迁移要用「规格文档驱动」**:先把原 Vue 全部视图功能提取成 `frontend-spec.md`(权威蓝本),再据此重建,最后独立 review 对照规格逐点核对——避免凭记忆漏功能(本次批量删除/彻底删除/远程清理等就是靠规格+review 补齐的)。

## 七、迁移后 review(第二、三轮)发现与修复

| # | 严重度 | 位置 | 问题 | 修复 |
|---|--------|------|------|------|
| C1 | Critical | store/mailbox_lease.go | ClaimMailboxLease 原子占位后,若后续任一失败,`__claiming__` marker 残留 → 邮箱被永久跳过 | 加 releaseMarker 补偿回写,4 个失败分支统一回滚 |
| P1 | Critical | httpapi/public.go | mailboxAPIURL 生成 `/api/v1/...`,实际路由是 `/api/icloud/v1/...` → 公共取码 URL 全部 404 | 改为 `/api/icloud/v1/mailboxes/.../code` |
| P2 | 中 | httpapi/server.go | 原 ServeHTTP 的 4 个安全响应头未迁移 | 加 secureHeaders 中间件 |
| P3 | 中 | 404 兜底 | 未知 /api/icloud/* 落到主 engine NoRoute,包壳风格不一致 | 新增 httpapi.HandleNotFound |
| M1 | Medium | store/store.go | upsertEntity 幂等比对因键序不同恒 false → 每次都写库+发 SSE | 新增 jsonBytesEqual(map 往返归一化键序) |
| M3 | Medium | store/store.go | 消息三路查重缺 DB 唯一性兜底 | ensureIndexes 补 idx_messages_remote / idx_messages_canonical 两个 partial unique 索引 |
| L1 | Low | store/store.go | SetChangeLogLimit 下限语义写错 | 改回 defaultChangeLogLimit(5000) |
| 代码质量 | — | bootstrap.go | seed 哈希重复实现 | auth 包导出 HashPassword,bootstrap 改调用 |

**前端 review(第二轮)**:补 4 项实质遗漏(events 级别筛选、settings 邮件监听状态行、SendKey 脱敏回显、public-code HTML 清洗),见「一·五」。

**review 结论验证**:补 `store/integration_test.go`(5 个用例,真实 Mongo),全部通过;`go build/vet/test` 与 `web npm run build` 全绿。

## 八、遗留与后续

- `GPT_GO_ICLOUD_DISABLE_BG=1` 可禁用 icloud 后台协程(调试用,默认启用)。
- **H1(已知设计折中)**:多文档写失去原 SQLite 单事务原子性(单机 Mongo 无副本集),已用「单文档原子 + 幂等 + C1 补偿回滚」缓解;若部署 replica set 可用 session.WithTransaction 彻底对齐。
- **M4(已知限制)**:写方法持 s.mu.Lock 跨多次 mongo 往返,mongo 抖动时全局读写阻塞;可后续把热路径超时调小或重构两段式。
- store 折中:lower() 匹配改集合扫描+内存比较(数据量小可接受);BackupDatabase 导出明文(已用 0600 权限);时间字符串比较依赖 RFC3339 字典序(写入统一本地时区)。
- 集成测试:`store/integration_test.go` 5 个用例(需本地 Mongo,不可达自动 Skip);后续可扩 httptest + 测试容器覆盖 httpapi 层。
- updatecheck 的 `UpdateRepository` 仍指向原仓库 `xiuxiu56/iCloud-Privacy-Mail-v2`,如需自有发布流请改 `internal/icloud/config/config.go`。
