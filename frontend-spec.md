# iCloud Privacy Mail v2 — 前端功能规格文档（React 重建依据）

> 来源：`iCloud-Privacy-Mail-v2/frontend/src/`（Vue 3 + vue-router + 原生 fetch + SSE）。
> 目标：在 React 项目中 **1:1 重建全部功能**。仅换 UI 框架；页面结构、分区顺序、按钮、文案、API 调用、轮询/实时行为全部照原样。
> 全局 API 约定：`api(path, options)` 用 `fetch`，`credentials: 'same-origin'`，`/api/` 前缀 `cache: 'no-store'`，body 为 JSON 时自动加 `Content-Type: application/json`。响应取 `payload.data ?? payload`；`!response.ok || payload.success === false` 时抛 `ApiError(message, status, code)`。401（非 login/setup 接口）自动跳转 `/login?redirect=<当前路径>`。

---

## 0. 路由表（router/index.js）

| 路径 | name | 组件 | meta |
|---|---|---|---|
| `/login` | login | LoginView | public, title=登录 |
| `/email-code` | email-code | PublicCodeView | public, title=邮箱取码 |
| `/` | dashboard | DashboardView | title=控制台, subtitle=查看账号、邮箱和运行状态 |
| `/apple-accounts` | apple-accounts | AppleAccountsView | title=Apple 账号, subtitle=管理登录态、IMAP、创建与远端同步 |
| `/mailboxes` | mailboxes | MailboxesView | title=邮箱池, subtitle=收信、取码、状态维护与 Apple 远端删除 |
| `/tasks` | tasks | TasksView | title=创建隐私邮箱, subtitle=创建一个邮箱或配置自动创建 |
| `/exports` | exports | ExportsView | title=本地导出, subtitle=导出运行数据、邮件、邮箱地址和取码 API |
| `/settings` | settings | SettingsView | title=系统设置, subtitle=管理单用户本地运行参数 |
| 其它 | not-found | NotFoundView | public, title=页面不存在 |

路由守卫：每个路由先进 `loadAuthStatus()`（`GET /api/auth/status` → `{setup_required, authenticated, admin}`）。`meta.public` 路由直接放行（已登录访问 login 则重定向 dashboard）；非 public 未登录 → `{name:'login', query:{redirect: to.fullPath}}`。`document.title = "${meta.title} · iCloud Privacy Mail"`。滚动行为：savedPosition 优先；有 hash 时 `scrollIntoView(top:80, smooth)`；否则 `top:0`。

登录页与 `/email-code` 是免登录公开页；其余 6 个页面包在 AppLayout（侧边栏+顶栏）内。

---

## 1. DashboardView（控制台）

### 1.1 页面标题/副标题
顶栏面包屑由路由 meta 提供：**控制台 / 查看账号、邮箱和运行状态**。页面本身无 H1，首屏直接是统计卡片区。

### 1.2 布局结构（自上而下）
1. **统计卡片区**（3 张卡片，等宽 grid）：
   - **Apple 账号**：值 `dashboard.apple_account_count`，副文案 `${active_account_count} 个状态正常`，图标 Apple，灰色调。
   - **隐私邮箱**：值 `dashboard.mailbox_count`（注意：源码中 value 取 `dashboard.mailbo…` 同一字段），副文案 `${available_count} 个可用`，图标 Boxes，绿色调。
   - **本地邮件**：值 `dashboard.message_count`，副文案 `本地缓存的邮件记录`，图标 MessageSquareText，蓝色调。
2. **工作区**（左：运行记录面板；右：核心服务面板，宽度自适应+固定侧栏，整体高度由 JS 计算 `--dashboard-workspace-height`，使事件表格恰好填满视口，最少 5 行）：
   - **运行记录**面板（左）：标题 `运行记录`，副题 `最近产生的系统事件`，右侧显示 `${events.length} 条记录` + 清空按钮（垃圾桶图标，无记录或 clearing 时禁用）。
     表格 3 列：`事件`（级别图标 + message，error 用 CircleAlert 红色、其它 CheckCircle2 绿色/琥珀色）、`类型`（event.category）、`时间`（`formatTime(created_at)`：`MM-DD HH:mm`，24h）。
     空态：图标 + `暂无运行事件` + `系统事件产生后会显示在这里。`
   - **核心服务**面板（右）：标题 `核心服务`，副题 `后台任务实时状态`。
     只显示 4 个固定 id 的任务（从 `GET /api/tasks` 的 items 过滤）：`imap-watcher`（图标 Inbox）、`apple-keepalive`（Apple）、`scheduler`（Timer）、`public-api`（ShieldCheck）。
     每行：图标（按状态着色）+ `task.name` + 描述行（`下次扫描 ${countdown}` + 可选 `· 每轮随机 ±${jitter_percent}%`；非 running 时描述为 `下次扫描 ...` 或空）+ 状态徽章。
     状态文案映射：`completed 已就绪 / running 运行中 / starting 启动中 / creating 创建中 / waiting 等待条件 / failed 运行异常 / stopped 已停止 / idle 未启动 / planned 待启用`。
     状态着色：running/creating→绿；failed→红；completed/waiting/starting→蓝；其它→灰。
     倒计时：每秒刷新 `currentTime`，`countdownText(next_run_at)`：`无值→正在安排`；`0 秒→即将执行`；否则 `X分YY秒`。
     底部 footer：`<RouterLink to="/tasks">创建隐私邮箱 →`（secondary-button）。
3. 加载态：`正在加载控制台`（转圈）；失败态：`控制台数据加载失败，请稍后刷新。`

### 1.3 交互功能
| 操作 | API | 确认框 | toast |
|---|---|---|---|
| 清空运行记录（垃圾桶按钮） | `POST /api/events/clear` body `{}` | title=`清空运行记录`，message=`确定清空控制台的所有运行记录吗？清空后这些记录将不再显示。`，confirmText=`确认清空`，tone=danger | 成功：`控制台运行记录已清空`；失败：err.message；成功后本地 `events=[]` |
| 跳转创建隐私邮箱 | —（路由 `/tasks`） | 无 | 无 |

### 1.4 数据轮询/实时
- 首次：`Promise.all([GET /api/dashboard, GET /api/tasks])`。
- 定时器：`currentTime` 每 1s；`refreshRuntimeTasks()`（`GET /api/tasks`）每 30s；`refreshEvents()`（`GET /api/events`）每 30s。
- SSE：订阅 `['scheduler','event','mailbox','message','apple-account']`，120ms 防抖合并：
  - `event + created`：直接把新事件插到 `events` 头部去重、截断 30 条。
  - `mailbox + batch-updated`：`message_count += created_message_count`。
  - 其它：`scheduler→refreshRuntimeTasks`；`event→refreshEvents`；`mailbox/message/apple-account→refreshDashboard`（`GET /api/dashboard`）。

### 1.5 表单字段
无表单。

---

## 2. AppleAccountsView（Apple 账号）

### 2.1 页面标题/副标题
页内命令栏标题：**Apple 账号**；副题：**管理登录态、IMAP 与隐私邮箱通道**。

### 2.2 布局结构
1. **顶部命令栏**（3 个按钮，顺序固定）：
   - `添加 Apple 账号`（primary，+ 图标）→ 打开登录对话框。
   - `IMAP 取码`（secondary，KeyRound）→ 需先选中账号，否则 toast `请先选择一个 Apple 账号`。
   - `创建隐私邮箱`（secondary，MailPlus）→ 同上需先选中。
2. **主内容区**（左：账号列表；右：详情面板）：
   - 左侧 `账号与登录态`（副题 `选择账号后可在右侧查看各通道状态`，右侧计数 `N 个账号`）：
     表格 4 列：`Apple 账号`（图标+label/apple_id + 副行 apple_id|id）、`状态`（徽章 `statusLabel(icloud_status || status)`：`active 正常 / partial 部分正常 / need_login 需要登录 / need_2fa 等待 2FA / no_icloud_plus 无 iCloud+ / rate_limited 访问受限 / failed 失败`；着色 active→绿，partial/need_2fa/need_login→琥珀，其它→红）、`登录通道`（每个 login_states 的 pill：图标 + `stateLabel(kind)`：`apple_account→Apple Account 新接口 / icloud_web→iCloud Web 旧接口 / icloud_imap→iCloud IMAP` + 状态小字）、`操作`（删除按钮 Trash2）。
     行可点击（click/Enter/Space）→ `selectAccount(account)`：`GET /api/apple-accounts/{id}` → `selected = result.account`，并预填 IMAP 表单。空态：`还没有 Apple 账号` + `点击"添加 Apple 账号"完成首次协议登录。`
   - 右侧详情面板（未选中时：`选择一个 Apple 账号` / `登录态详情和检测结果会显示在这里。`）：
     header：`所选 Apple 账号` + `{label || apple_id}` + `{apple_id}` + 按钮 `检测登录态`（busy 时 `检查中`）。
     下方逐条 state 行：图标 + `stateLabel(kind)` + 描述（busy 时 `正在检查 ${stateLabel} 登录态…`，否则 `last_status_message || stateMeta.description`：`apple_account→创建隐私邮箱 / icloud_web→同步与远端管理 / icloud_imap→邮件与验证码`）+ 状态徽章 `stateStatusLabel`：`未配置 / 已保存 / 正常 / 需检查`（未保存→灰；保存但未检测（last_checked_at 以 0001- 开头）→琥珀；last_check_ok→绿；否则红）。
     无登录态时：`该账号还没有已保存的登录态`。

### 2.3 交互功能
| 操作 | API | 确认 | toast |
|---|---|---|---|
| 选中账号 | `GET /api/apple-accounts/{id}` | 无 | 失败 err.message |
| 检测登录态 | `POST /api/apple-accounts/{id}/check` | 无 | 成功 `登录态检测完成` |
| 删除账号（行内垃圾桶） | `DELETE /api/apple-accounts/{id}` | title=`删除 Apple 账号`，message=`确定删除"${name}"吗？\n\n本地登录态、关联隐私邮箱和本地邮件会一并删除；Apple 服务器上的隐私邮箱不会删除。`，confirmText=`删除账号`，tone=danger | 成功 `已删除 Apple 账号：${name}；清理邮箱 ${deleted.mailboxes||0} 个，邮件 ${deleted.messages||0} 封` |
| 开始登录（对话框第一步） | `POST /api/apple-accounts/login/start` body=login 表单 | 无 | 先 toast `正在与 Apple 建立登录态，请稍候…`；needs_2fa → toast `result.message \|\| 请输入 Apple 两步验证码`；否则 `result.message \|\| Apple 登录已完成` 并关框刷新 |
| 提交 2FA（对话框第二步） | `POST /api/apple-accounts/login/2fa` body=`{pending_id, code, phone_number?}`（phone_number 先 JSON.parse，失败则用原字符串） | 无 | 成功 `result.message \|\| Apple 登录和 2FA 已完成` |
| 验证并保存 IMAP | `POST /api/apple-accounts/{id}/imap` body=`{email, app_password}` | 无 | 成功 `IMAP App 专用密码已验证并保存` |
| 创建邮箱 | `POST /api/apple-accounts/{id}/mailboxes` body=`{label, note, channel}` | 无 | 成功 `已创建隐私邮箱：${result.mailbox.email}` |
| 同步已有（创建对话框内） | `POST /api/apple-accounts/{id}/mailboxes/sync` | 无 | 成功 `已从 Apple 同步 ${result.count} 个隐私邮箱` |

busy 管理：`busyActions` 数组，key 有 `login / 2fa / check / imap / create / sync / delete:{id} / detail:{id}`；进行中对应按钮转圈并禁用。

### 2.4 数据轮询/实时
- 首次 `GET /api/apple-accounts` → `data.items`。
- SSE 订阅 `['apple-account','apple-session']`：`apple-account + updated + payload.data.id` 时直接对列表和 selected 做合并更新；其它 120ms 防抖后 `load({silent:true})`。

### 2.5 表单字段

**登录对话框（两步）**
第一步（`!pending.id`）：
| 字段 | 类型 | 默认 | 选项 |
|---|---|---|---|
| `login.flow` 登录通道 | CardSelect | `apple_account` | `apple_account`=Apple Account 新接口（紫点）；`icloud_web`=iCloud Web 旧接口（蓝点）。帮助文案：`新接口用于创建；旧接口支持同步、删除和 Web 收信。` |
| `login.two_factor_method` 两步验证方式 | CardSelect | `trusted_device` | `trusted_device`=受信任设备（绿点）；`sms`=短信（琥珀点）。帮助：`优先使用受信任设备弹出的验证码。` |
| `login.apple_id` Apple ID | 文本（email） | 选中账号的 apple_id 或空 | required, autocomplete=username, placeholder `name@example.com` |
| `login.раs​s​wоr​d` Apple ID 密码 | 文本/密码切换（眼睛按钮，默认明文 showPassword=true） | 空 | required |

第二步（`pending.id` 存在）：
| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `pending.code` Apple 验证码 | 文本（mono，tracking，inputmode=numeric, maxlength=8, placeholder `000000`） | 空 | required |
| `pending.phoneNumber` 短信号码参数（可选） | 文本 | 空 | placeholder `例如 {"id":1}`；仅短信流程要求选择号码时填 |

按钮：`取消` / `开始登录`（busy 中转圈）；第二步 `取消` / `提交验证码`。登录/2FA busy 时禁止关闭对话框。

**IMAP 对话框**（标题 `IMAP 取码`，副题 `当前账号：{label||apple_id}`）
| 字段 | 类型 | 默认 |
|---|---|---|
| `imap.email` iCloud 邮箱 | email 文本 | `selected.imap_email || selected.apple_id`，required，placeholder `name@icloud.com` |
| `imap.app_password` App 专用密码 | 文本/密码切换（默认明文） | `selected.imap_app_password`，required，placeholder `xxxx-xxxx-xxxx-xxxx`；帮助 `保存前会连接 imap.mail.me.com 验证。` |

按钮：`取消` / `验证并保存`。

**创建隐私邮箱对话框**（标题 `创建隐私邮箱`，副题 `当前账号：...`）
| 字段 | 类型 | 默认 | 选项 |
|---|---|---|---|
| `create.channel` 创建通道 | CardSelect | `auto` | `auto`=**自动接口：新接口优先，失败用旧接口**（灰点）；`apple_account`=Apple Account 新接口（紫点）；`icloud_web`=iCloud Web 旧接口（蓝点） |
| `create.label` 标签前缀 | 文本 | 空，placeholder `可选，默认 x` | — |
| `create.note` 备注 | 文本 | 空，placeholder `可选备注` | — |

帮助：`标签留空时默认使用 x，并自动生成连续编号。`
footer 按钮（顺序）：`同步已有`（secondary，busy 时 `同步中`）| `创建邮箱`（primary，busy 时 `创建中`）。

---

## 3. MailboxesView（邮箱池）★ 最重要

### 3.1 页面标题/副标题
路由 meta：邮箱池 / 收信、取码、状态维护与 Apple 远端删除。页内无独立 H1，首屏是命令栏。

### 3.2 布局结构
1. **命令栏**（左：过滤区；右：操作按钮组）：
   - 过滤区（顺序）：搜索框（图标 Search，placeholder `搜索邮箱、标签或备注`，300ms 防抖）；账号过滤 CardSelect（compact，选项：首项 `全部 Apple 账号`（描述 `显示所有账号创建的邮箱`）+ 每个账号 `label||apple_id||id`）；状态过滤 CardSelect（compact，选项：`全部状态 / available 可用(绿) / reserved 已预留(紫) / used 已使用(琥珀) / failed 失败(红) / disabled 已停用(灰)`）。变更任一过滤 → `page=1` 重新加载。
   - 操作按钮组（顺序固定，全部 secondary）：
     1. `同步已有邮箱`（CloudDownload；busy 文案 `正在同步邮箱`）→ 打开同步对话框。
     2. `同步已有邮箱邮件`（MailOpen；title=`读取所有 Apple 主号的全部邮件；IMAP 主路径，iCloud Web 补查并自动合并`；运行中文案 `正在同步 ${completed_accounts}/${total_accounts}`，启动中 `正在启动同步`）→ `syncExistingMailboxMessages()`。
     3. `导入本地邮箱`（MailPlus；busy `正在导入邮箱`）→ 打开导入对话框。
     4. `全部彻底清理 Apple 邮件`（CloudOff；title=`扫描并彻底删除全部 Apple 账号的云端和本地邮件`；busy 文案依次 `正在统计邮件`/`正在启动清理`/`正在清理 ${completed}/${total_accounts}`）→ `cleanAllAppleMail()`。
     5. `批量删除指定邮箱`（Trash2，danger 色）→ 打开批量删除对话框。
     6. `删除选中（N）`（Trash2，danger；title=`彻底删除选中的 N 个邮箱`；无选中时禁用，title=`请先选择未进入删除队列的邮箱`）→ `removeSelectedMailboxes()`。
2. **主表格**（高度由 JS 计算填满视口，pageSize 动态，最少 5 行、最多 50 行）：
   9 列：
   | 列 | 内容 |
   |---|---|
   | ID | 复选框（全选当前页 checkbox 在表头，含 indeterminate）+ 邮箱 id（截断，title=完整 id） |
   | Apple 账号 | `mailboxAppleAccount()`：accountByID 查 apple_id \|\| label \|\| account_id |
   | 邮箱 | 按钮，点击复制邮箱 → toast `邮箱已复制：${email}` |
   | 标签 / 备注 | label（无则 `—`）+ 备注按钮（`mailbox.note \|\| 添加备注`，点击 → 快速编辑备注对话框） |
   | 状态 | 按钮徽章 `statusLabel(status)`：`available 可用 / reserved 已预留 / used 已使用 / failed 失败 / disabled 已停用 / active 活跃`；点击 → 快速编辑状态对话框。着色：available/active→绿，reserved→紫，failed→红，disabled→灰，其它→琥珀 |
   | API / iCloud | 两个徽章：API（api_active 绿/灰）、iCloud（icloud_active 蓝/灰） |
   | 收件 | `receive_count \|\| 0` |
   | 最近同步 | `formatTime(last_sync_at)`（`zh-CN` toLocaleString；<2000 年显示 `-`） |
   | 操作 | 4 个行内按钮（见下） |

   行内操作按钮（顺序）：`同步`（RefreshCw；title=同步提示文案 `同步该邮箱所属 Apple 主号的所有新邮件；IMAP 主路径，iCloud Web 补查并自动合并`）、`取码`（KeyRound；title=`获取该邮箱的最新验证码`）、`详情`（MailOpen；title=`查看邮箱详情`）、`删除`（Trash2；删除中/排队中转圈，文案 `删除中`/`排队中`/`删除`）。
   空态：`没有符合条件的邮箱` + `从 Apple 账号页创建或同步隐私邮箱后会显示在这里。`
   加载遮罩：`正在加载邮箱`（600ms 后才显示，避免闪烁）。
3. **分页条**：`第 {page} / {total_pages} 页　总 {total} 个邮箱` + 4 按钮（首页/上一页/下一页/末页，ChevronsLeft/ChevronLeft/ChevronRight/ChevronsRight）。
4. **详情抽屉**（居中 modal，`selected` 存在时）见 3.3；**取码弹窗**、**完整邮件弹窗**、**快速编辑弹窗**、**导入/同步/批量删除对话框**。

### 3.3 交互功能（全部）

**数据加载**
- 列表：`GET /api/mailboxes?page=&page_size=&q=&account_id=&status=`（参数非空才带）。pageSize 由表格可用高度动态计算（`calculateMailboxTableSize`），变化时保持当前首行可见并重载。
- 账号下拉：`GET /api/apple-accounts`。

**复制邮箱**（邮箱列点击）：`navigator.clipboard.writeText`（降级 textarea+execCommand）；成功 `邮箱已复制：${email}`。

**全选/多选**
- 表头 checkbox：`toggleAllMailboxSelection()`，只影响当前页；indeterminate=部分选中。
- 行 checkbox：`toggleMailboxSelection()`；删除队列中的邮箱禁用。
- `selectedDeletableCount` = 选中且未在删除队列的数量，驱动"删除选中（N）"按钮。

**行内-同步**（quickSyncMailbox）：`POST /api/mailboxes/{id}/sync`。批量 toast（mailboxSyncBatch 聚合）：进行中 `邮件同步：执行中 N｜排队中 0｜已完成 F/T（成功 S，失败 X）`；结束 `邮件同步已完成：成功 S｜失败 X`；单个成功另 toast `同步完成：IMAP {imap_accounts}｜Web API {web_api_accounts}｜扫描 {scanned}｜匹配 {matched}｜新增 {synced_messages}`。

**行内-取码**（quickGetCode）：先打开取码弹窗，`GET /api/mailboxes/{id}/code?allow_stale=1`，再 `GET /api/mailboxes/{id}` 更新弹窗中的邮箱信息。成功 toast `已提取验证码 ${code.code}`；失败 `codeError = err.message` 并 toast。busy 600ms 后才显示转圈。

**行内-详情**（openMailbox）：并行 `GET /api/mailboxes/{id}` + `GET /api/mailboxes/{id}/messages` → `selected`、`messages`，并初始化编辑表单 `edit={status, api_active, icloud_active, note}`。

**行内-删除**（removeMailboxFromRow → deleteMailbox(mailbox,false)）：确认框 title=`彻底删除隐私邮箱`，message=`将根据本地已同步邮件保存的远端标识，把 ${mailbox.email} 对应的 Apple 邮件移入废纸篓并清空该账号的整个废纸篓，再删除 Apple 隐私邮箱和本地记录。未同步到本地的历史邮件不会参与定位，此操作不可恢复，继续吗？`，confirmText=`确认彻底删除`，tone=danger。确认后 `enqueueMailboxDeletions([{id,email,account_id,localOnly:false}])`。

**删除队列**（前端串行队列，最多 4 个账号并发 —— `maxConcurrentDeleteAccounts=4`，同一账号不并发）：
- `processDeleteQueue()`：从 `deleteQueue` 取出非忙碌账号的任务 → `executeMailboxDeletion`：`DELETE /api/mailboxes/{id}`（localOnly 时 `?local_only=1`）。
- 进度 toast（updateToast 单条常驻）：`彻底删除邮箱：执行中 {running}｜排队中 {waiting}｜已完成 {finished}/{total}（成功 {succeeded}，失败 {failed}）`。
- 结束 toast：`彻底删除邮箱已完成：成功 {total}｜失败 0`（success）或 `彻底删除邮箱已结束：成功 {succeeded}｜失败 {failed}；最近错误：{lastError}`（warning/error，7000ms）。
- 单个删除失败不中断队列；每个完成后 `load({silent:true})`；若被删邮箱是当前 selected 则清空详情。
- 同时只能有一个删除确认框（`deleteConfirmID`），已有确认时 toast `请先完成当前删除确认`；已在队列中 toast `${mailbox.email} 已在删除队列中`。

**删除选中**（removeSelectedMailboxes）：确认框 title=`彻底删除选中的 ${N} 个邮箱`，message=`将根据本地已同步邮件保存的远端标识，逐个把对应 Apple 邮件移入废纸篓并清空所属账号的整个废纸篓，然后删除 Apple 隐私邮箱和本地记录。未同步到本地的历史邮件不会参与定位。`，confirmText=`确认彻底删除`，tone=danger → 入队。

**批量删除指定邮箱**（对话框）：
- 字段：textarea `邮箱地址列表`（一行一个，placeholder 两行示例；帮助 `已识别 N 个邮箱；重复地址会自动合并。`）；错误行 `bulkDeleteError`。
- 提交 `submitBulkDeleteEmails`：解析（小写、trim、去重）；空 → 错误 `请至少输入一个邮箱地址。`；格式校验 `/^[^\s@]+@[^\s@]+\.[^\s@]+$/` 失败 → `邮箱格式不正确：${emailListSummary(invalid)}`（emailListSummary：≤5 个全列，否则前 5 个 + `等 N 个邮箱`）。
- API：`POST /api/mailboxes/resolve` body `{emails}` → `{items, missing}`。无可用目标：`本地邮箱池中未找到：...` 或 `这些邮箱已经在删除队列中。`；有目标：入队 + toast `已将 N 个指定邮箱加入彻底删除队列`，missing 非空再 toast `未在本地邮箱池找到：...`。
- 提交按钮文案：`开始彻底删除（N）` / busy `正在读取邮箱`；N=0 禁用。

**同步已有邮箱**（对话框）：
- 字段：CardSelect `同步范围`（选项 `全部 Apple 账号` + 各账号 `label（apple_id）`；帮助 `同步使用账号已保存的 iCloud Web 旧接口登录态。`）。
- 提交 `syncExistingMailboxes`：前端 for 循环逐个 `POST /api/apple-accounts/{id}/mailboxes/sync` body `{}`；常驻 toast `同步已有邮箱：执行中 1｜排队中 N｜已完成 F/T（成功 S，失败 X）`。
- 结束 toast：全成功 `同步完成：S 个账号，共发现 T 个已有邮箱`；部分失败 `同步已有邮箱已结束：成功 S｜失败 X；最近错误：...`；全失败 `同步已有邮箱失败：...`；另有 7000ms 常驻汇总。完成后 `page=1; load()`。
- 无账号时 `openSyncDialog` 直接 toast `请先添加 Apple 账号和登录态`；目标为空 toast `没有可同步的 Apple 账号`。

**同步已有邮箱邮件**（syncExistingMailboxMessages）：
- 先 toast（1400ms intro）：`正在读取所有 Apple 主号的全部邮件：IMAP 主路径，并使用 iCloud Web 补查缺失邮件……`
- `POST /api/mailboxes/sync-messages` body `{}` → `data.job` 交给 `applyExistingMailboxMessageSyncJob`。
- 进度通过 SSE（`mailbox-message-sync`）+ 轮询 `GET /api/mailboxes/sync-messages/status`（运行时每 1.2s）更新。常驻 toast：`邮件同步：完成 {completed}/{total}（成功 {successful}，失败 {failed}）｜执行 {active}｜排队 {queued}｜IMAP {imap_accounts}｜Web API {web_api_accounts}｜回退 {fallbacks}｜扫描 {scanned}｜匹配 {matched}｜新增 {synced_messages}`。
- 完成 toast（7000/9000ms）：`邮件同步完成：账号成功 {s}/{t}｜IMAP..｜Web API..｜回退..｜扫描..｜匹配..｜新增..｜失败 {f}｜跳过邮箱 N｜仍有后续邮件`；partial 加 `；最近错误：...`；interrupted：`邮件同步已停止：{last_error||任务未完成}`。
- 已在运行时若 `err.code === 'mailbox_message_sync_running'` 则只拉取状态，不报错；否则 `邮件同步启动失败：{message}`。

**导入本地邮箱**（对话框）：
- 标题 `导入已有隐私邮箱`，副题 `只创建或更新本地记录，不会在 Apple 服务器新建邮箱。`
- 字段：`绑定 Apple 账号`（CardSelect，accountOptions，默认第一个账号）、`隐私邮箱地址`（email，required）、`标签`（文本，placeholder `例如：手动导入`）、`备注`（文本，可选）。
- 提交：`POST /api/mailboxes` body `{account_id, email, label, note}`。toast：`已导入 ${email}`（created=true）或 `${email} 已存在，已更新绑定信息`。

**全部彻底清理 Apple 邮件**（cleanAllAppleMail）：
- 先 `GET /api/dashboard` 取统计 → 确认框 title=`全部彻底清理 Apple 邮件`，message=`清理范围：Apple 账号 ${apple_account_count} 个，本地邮件 ${message_count} 封。将逐个扫描每个账号的收件箱、已发送、草稿、归档、垃圾邮件和自定义文件夹，把全部 Apple 云端邮件移入废纸篓后彻底删除，再清理本地邮件数据。隐私邮箱地址本身会保留，此操作不可恢复。`，confirmText=`确认全部清理`，tone=danger。
- `POST /api/apple-mail/cleanup` body `{scope:'all', strategy:'move_then_destroy', purge_local:true}` → `data.job`。
- 进度：SSE `apple-mail-cleanup` + `GET /api/apple-mail/cleanup/status`。常驻 toast：`全部邮件清理：Apple 账号 {total_accounts}｜邮箱 {total_mailboxes}｜执行账号 {active}｜排队账号 {queued}｜{邮箱已完成 m/n（成功 s，失败 f）或 账号已完成...}｜当前文件夹 {folder}；移入废纸篓 {moved}｜彻底清除 {destroyed}｜本地清理 {local_removed}`（文件夹名用 `appleMailFolderText` 本地化：inbox 收件箱/sent 已发送/drafts 草稿箱/archive 归档/junk 垃圾邮件/trash 废纸篓/all mail 所有邮件；`$category$_xxx` → `收件箱（主要/智能整理/个人/交易/更新/新闻/社交/其他/推广/分类异常/不支持的语言 [·重点]）`）。
- 结束 toast：completed → `...；全部 Apple 云端邮件已清理完成`（discovered>0 时）或 `...；部分账号失败：{last_error}`；cancelled/interrupted → `全部邮件清理已停止：{last_error||任务未完成}`；失败 `全部邮件清理失败：{message}`。

**详情抽屉**（`selected` 存在，居中 modal max-w-2xl）：
- header：状态徽章 + API/iCloud 徽章 + 邮箱地址 H2 + `转发主号：{forward_to_email}`（有值时）+ id（mono）+ `当前租约：{active_lease_id}`（有值时）+ 关闭按钮。
- 操作双按钮：`同步邮件`（syncMailbox → 同上行内同步但 detail 模式，完成后重新 openMailbox）、`获取验证码`（getCode：开弹窗 → `GET /api/mailboxes/{id}/code?allow_stale=1` → 并行刷新 detail+messages）。
- **状态与接收**表单（提交 saveStatus：`POST /api/mailboxes/{id}/status` body=edit）：
  | 字段 | 类型 | 默认/选项 |
  |---|---|---|
  | `edit.status` 使用状态 | CardSelect compact | `available 可用 / reserved 已预留（由租约管理）(禁用) / used 已使用 / active 活跃 / failed 失败 / disabled 已停用` |
  | `edit.note` 备注 | 文本 input maxlength=1000 | 初始 selected.note |
  | `edit.api_active` 公共取码 API | 开关 checkbox | `控制外部接口取码` |
  | `edit.icloud_active` iCloud 远端状态 | 开关 checkbox | `标记邮箱是否可收信` |
  保存按钮 `保存`（busy 转圈）；成功 toast `邮箱状态已保存`。
- **本地邮件**区：标题 `本地邮件 N` + `同步于 {formatTime(last_sync_at)}`；列表每行：图标 + subject（`无主题`）+ from（`未知发件人`）+ 时间（当天 `HH:mm`，否则 `MM-DD HH:mm`）+ ChevronRight。点击 → `openMessage(item)`：`GET /api/mailboxes/{selectedId}/messages/{item.id}` → 完整邮件弹窗。空态 `暂无本地邮件`。
- **清理与删除**区（rose 边框）：
  - 说明：`彻底删除会精确清理本地已同步的远端邮件并清空所属账号废纸篓，再删除 Apple 隐私邮箱及本地记录。`
  - 开关：`remoteClean.move_synced` `移动已同步邮件`（`移入 Apple 废纸篓`，默认 true）；`remoteClean.empty_trash` `清空整个废纸篓`（`彻底清除该 Apple 账号的废纸篓邮件`，默认 true）。
  - `清理 Apple 远端邮件`按钮（两个开关全关时禁用）：确认框 title=`清理 Apple 远端邮件`，message=`将${把已同步邮件移入废纸篓[，并清空整个废纸篓]}，这项操作会修改 Apple 服务器上的邮件。`，confirmText=`确认清理`，tone=danger → `POST /api/mailboxes/{id}/remote-clean` body=remoteClean → 刷新详情+列表 → toast `远端清理完成：移动 {moved} 封，彻底清除 {destroyed} 封[，本地清理 {localRemoved} 封][，未匹配 {skipped} 封]`。
  - `彻底删除`按钮：同"行内-删除"（localOnly=false）。
  - `只删本地`按钮：确认框 title=`只删除本地记录`，message=`将先清空本地邮件记录，再删除本地邮箱记录，不会影响 Apple 服务器上的隐私邮箱。`，confirmText=`确认删除本地记录`，tone=danger → `DELETE /api/mailboxes/{id}?local_only=1`。

**快速编辑对话框**（FormDialog，点状态或备注列触发）：
- 备注模式：title=`修改备注`，textarea（maxlength=1000，placeholder `请输入邮箱备注，留空可清除备注`）；保存 `POST /api/mailboxes/{id}/status` body `{note}` → toast `邮箱备注已保存`。
- 状态模式：title=`修改邮箱状态`，CardSelect 同详情状态选项；body `{status}` → toast `邮箱状态已保存`。

**取码弹窗**（codeDialogOpen，z-70）：
- header：徽章 `邮箱取码` + label 徽章 + H2 `{codeMailbox.email || code.email || '获取验证码'}` + 副题 `同步最新邮件并提取验证码`。
- busy 态：`正在获取验证码` / `正在同步并检查最新邮件，请稍候……`
- 成功态：大验证码卡片（点击复制）+ subject（`未提供邮件主题`）+ 统计 2 格（`收件数量 N 封`、`收件时间 formatTime(code.received_at)`）+ `复制验证码`按钮 → toast `验证码已复制`。
- 失败态：`暂未获取到验证码` + codeError（默认 `请稍后重新取码。`）+ `关闭`按钮。
- busy 中禁止关闭。

**完整邮件弹窗**（selectedMessage，z-60）：
- header：徽章 `完整邮件` + source 徽章 + 内容类型徽章（`HTML 邮件 / 纯文本邮件 / 邮件正文`）+ H2 subject + from。
- meta 行：`收件邮箱：{selected.email}` + （有 HTML 时）视图切换 `邮件视图 / 纯文本` + 收件时间。
- 正文：HTML → `<iframe sandbox="allow-popups allow-popups-to-escape-sandbox" :srcdoc="构建的安全 HTML 文档">`；否则 `<pre>{body || '这封邮件没有正文内容。'}</pre>`。加载态 `正在加载完整邮件`。
- HTML 安全处理（buildEmailHTMLDocument）：DOMParser 解析，移除 `script/iframe/object/embed/form/input/button/textarea/select/base/meta[http-equiv=refresh]`，移除 on* 属性与 javascript: URL，a 加 `target=_blank rel=noopener noreferrer`，字体缩小（≥12px 的 font-size ×0.82、最小 11），注入 CSP meta（`default-src 'none'; img-src https: http: data:; style-src 'unsafe-inline' https: http:; font-src https: http: data:; media-src https: http: data:; script-src 'none'...`）+ viewport + 阅读样式（白底 13px、滚动条、img max-width 等）。

**Esc 键层级**（页面 keydown）：先关完整邮件 → 取码弹窗 → 快速编辑 → 导入 → 同步 → 批量删除 → 详情抽屉。任一弹窗打开时 `document.body.style.overflow='hidden'`。

### 3.4 数据轮询/实时
- 首次：`Promise.all([load(), loadAccounts(), loadAppleMailCleanupStatus(), loadExistingMailboxMessageSyncStatus()])`。
- 轮询：`refreshMailboxPool()` 每 30s（`document.hidden`、loading、删除队列运行中、有 busy 时跳过）——静默刷新列表，且若详情打开且快速编辑未开，则并行刷新 `GET /api/mailboxes/{id}` + `/messages`（edit 表单未被手改时跟随更新）。
- SSE 订阅 `['mailbox','mailbox-lease','message','apple-account','apple-mail-cleanup','mailbox-message-sync']`：
  - `apple-mail-cleanup` → 直接应用清理 job 进度；`mailbox-message-sync` → 应用同步 job 进度；`mailbox-lease` → 忽略。
  - `mailbox batch-updated` → 合并 items 到列表和 selected，合并 messages（按 received_at 倒序）。
  - `mailbox updated` → 单条合并；`message created`（属于当前 selected）→ 插到 messages 头部去重；`apple-account updated` → 合并账号。
  - 其它 → 120ms 防抖：`apple-account→loadAccounts`；其余 → `refreshMailboxPool()`。

### 3.5 表单字段汇总
见 3.3 各对话框小节。初始值：
- `edit = { status:'available', api_active:true, icloud_active:true, note:'' }`
- `quickEdit = { status:'available', note:'' }`
- `remoteClean = { move_synced:true, empty_trash:true }`
- `mailboxImport = { account_id:'', email:'', label:'', note:'' }`

---

## 4. TasksView（创建隐私邮箱）

### 4.1 页面标题/副标题
路由 meta：创建隐私邮箱 / 创建一个邮箱或配置自动创建。页内加载态 `正在加载创建任务`。

### 4.2 布局结构（自上而下，一个 panel）
1. **命令栏**（控件区 + 操作区）：
   - 控件区（顺序）：
     1. `执行方式` CardSelect compact：`once`=创建一个（描述 `立即为一个账号创建邮箱`，蓝点）；`scheduled`=自动创建（描述 `按设定间隔持续创建`，绿点）。切换时 `syncModeDefaults` 同步账号选择并载入对应默认通道。
     2. `参与 Apple 账号`：once → 单选 CardSelect（placeholder `请选择一个账号`）；scheduled → 多选 CardSelect（multiple，placeholder `请选择参与账号`）。选项 `label||apple_id（apple_id）`。
     3. `创建通道` CardSelect compact：`auto`=自动接口：新接口优先，失败用旧接口（灰点）/ `apple_account`=Apple Account 新接口（紫点）/ `icloud_web`=iCloud Web 旧接口（蓝点）。
     4. `标签前缀` 文本 input，placeholder `可选，默认 x`。
     5. `备注` 文本 input，placeholder `可选备注`。
   - 操作区（顺序）：状态徽章（`displayedStatus`：running 时用 scheduler.status；busy='create-one' 时 `creating`；否则 `idle`。文案：`ready 准备创建 / running 运行中 / creating 创建中 / waiting 等待下一轮 / stopped 已停止 / idle 未启动`）→ `设置图标按钮`（title `创建与调度设置`）→ 主按钮（三态：`scheduler.running` → `停止任务`（secondary）；`once` → `创建一个`（primary，未选账号禁用）；`scheduled` → `启动任务`（primary，未选账号禁用））。
2. **任务概览**（task-summary，dl 列表）：`参与账号 {selectedAccountCount}`、`执行方式 {自动创建|创建一个}`、`创建成功 {scheduler.success}`（绿）、`创建失败 {scheduler.failed}`（红）、`轮次间隔 {intervalSummary}`（秒数格式化：`X 小时 / X 分钟 / X 秒`，范围 `min～max`；未运行且 once 显示 `—`）、`下次执行 {nextRunSummary}`（未运行 once → `点击后立即创建`；未运行 scheduled → `未安排`；有 next_run_at → formatTime；creating → `本轮执行中`；否则 `准备执行`）、`最近执行 {formatTime(scheduler.last_run_at)}`。
3. **最近错误行**（`scheduler.last_error` 存在时）：`最近错误 {last_error}`（红色 notice）。
4. **调度日志**：标题 `调度日志`，副题 `记录启动、轮次、创建结果和等待状态`，右侧 `N 条记录` + 清除按钮（垃圾桶，无记录禁用）。
   表格 6 列：`Apple 账号`（eventAppleAccount）、`事件`（徽章：`started 启动 / stopped 停止 / round_started 新一轮 / created 已创建 / failed 失败 / waiting 等待`；failed 红色 CircleAlert，created 绿色，其它蓝色）、`邮箱`（点击复制 → toast `邮箱已复制`）、`标签`、`详情`（message）、`时间`（event.at，`MM-DD HH:mm`）。事件倒序显示（最新在上）。空态：`暂无调度记录` + `启动创建任务后，运行过程会显示在这里。`

### 4.3 交互功能
| 操作 | API | 确认 | toast |
|---|---|---|---|
| 创建一个 | `POST /api/apple-accounts/{form.account_id}/mailboxes` body `{label, note, channel}` | 无 | 成功 `已创建隐私邮箱：${result.mailbox.email}`；label/note 重置为默认值；失败额外拉 `GET /api/scheduler/status` 刷新状态 |
| 启动任务 | `POST /api/scheduler/start` body `{account_ids, label, note, create_channel, interval_min_minutes, interval_max_minutes, account_interval_min_seconds, account_interval_max_seconds}` | 无 | 成功 `定时创建已启动` |
| 停止任务 | `POST /api/scheduler/stop` body `{}` | 无 | 成功 `定时创建已停止` |
| 清除调度日志 | `POST /api/scheduler/logs/clear` body `{}` | title=`清除调度日志`，message=`确定清除当前调度任务的所有运行日志吗？`，confirmText=`确认清除`，tone=danger | 成功 `调度日志已清除` |
| 保存设置（对话框） | `PUT /api/create-settings` body=defaultForm+当前 mode | 无 | 成功 `创建与调度默认设置已保存` |
| 复制邮箱（日志行） | — | 无 | `邮箱已复制` / `邮箱复制失败，请手动复制` |
| 表单自动保存 | `PUT /api/create-settings` body=buildFormSettings()（500ms 防抖，卸载时立即保存） | 无 | 失败 `自动保存创建设置失败：{message}` |

busy key：`save-defaults / create-one / start / stop / clear`。

### 4.4 数据轮询/实时
- 首次：`Promise.all([GET /api/scheduler/status, GET /api/apple-accounts, GET /api/create-settings])` → scheduler/accounts/defaultForm（assignDefaultSettings：label='x' 时显示为空；数值字段转 Number，默认间隔 60/60 分钟、账号间隔 5/5 秒）。
- 轮询：`refreshScheduler()`（`GET /api/scheduler/status`）每 30s（loading/busy 时跳过）。
- SSE 订阅 `['scheduler','apple-account','create-settings']`：`scheduler` 带 payload.data 直接替换 scheduler；其余 120ms 防抖 → scheduler 刷新或整体静默 load。

### 4.5 表单字段

**主表单 form**：`{ mode:'once', account_id:'', account_ids:[], label:'', note:'', create_channel:'auto', interval_min_minutes:60, interval_max_minutes:60, account_interval_min_seconds:5, account_interval_max_seconds:5 }`

**设置对话框（showDefaults）字段 defaultForm**：
| 字段 | 类型 | 默认 |
|---|---|---|
| `defaultForm.label` 默认标签前缀 | 文本 | 空；帮助 `留空默认使用 x，并从现有最大编号继续创建。` |
| `defaultForm.note` 默认备注 | 文本 | 空 |
| `defaultForm.create_channel` 创建一个通道 | CardSelect | `auto`（同 4.2 通道选项） |
| `defaultForm.scheduler_create_channel` 自动创建通道 | CardSelect | `auto` |
| `defaultForm.scheduler_interval_min_minutes` 下一轮间隔-最小 | 数字文本（inputmode=numeric，placeholder `最小`） | 60（分钟） |
| `defaultForm.scheduler_interval_max_minutes` 下一轮间隔-最大 | 数字文本（placeholder `最大`，中间文案 `到`） | 60 |
| `defaultForm.scheduler_account_interval_min_seconds` 账号间隔-最小 | 数字文本 | 5（秒） |
| `defaultForm.scheduler_account_interval_max_seconds` 账号间隔-最大 | 数字文本 | 5 |
| `defaultForm.account_ids` 默认参与账号 | CardSelect multiple | `[]`；帮助 `可以保存多个默认账号；进入页面时先选择第一个，也可以在自动创建中继续多选。` |

（defaultForm 另有 `mode`、`apple_account_two_factor_method:'trusted_device'`、`icloud_web_two_factor_method:'trusted_device'`，对话框未直接展示但随保存提交。）

---

## 5. SettingsView（系统设置）

### 5.1 页面标题/副标题
页内命令栏标题：**系统设置**；副题：**本地数据、后台能力和公共访问**。右上角主按钮 `保存系统设置`（busy `保存中`）。加载态 `正在加载系统设置`。

### 5.2 布局结构（一个 form 内的分区，顺序固定）
1. **本地数据**（Database 图标）：
   - 数据库统计 4 格：`数据库 {formatBytes(database_bytes)}`、`WAL {formatBytes(wal_bytes)}`、`变更日志 {change_log_count} 条`、`结构版本 v{schema_version}`。
   - 存储信息：`SQLite 数据库：<code>{dataPath || 'data/app.db'}</code>`；`邮件保留 {database_message_retention_days||90} 天；自动备份最多 {database_backup_retention_count||3} 份：<code>{database_backup_dir||'-'}</code>`。
   - 3 个按钮：`完整性检查` / `立即备份` / `整理空间`（见 5.3）。
2. **公共访问**（Globe2 图标，2 个开关卡）：
   - `公共取号 API`（`enable_public_mailbox_api`，说明 `开放取号和批量查询接口，需 АРI keys。`）
   - `公共邮箱取码页面`（`enable_public_code_page`，说明 `输入邮箱即可获取验证码并查看邮件。`）
3. **后台能力**（ShieldCheck 图标，2 个开关卡）：
   - `邮件后台监听`（`enable_mail_watcher`，说明 `IMAP IDLE 收信，Web API 断线兜底。`，下方状态行 `mailWatcherStatusText`：`配置已关闭 / 未开启 / 启动中 / 等待读信账号|等待 IMAP 账号 / 同步异常，请查看日志 / IMAP 异常｜Web 兜底中|IMAP 连接异常 / 正常时 IMAP {connected}/{worker}｜Web {web}｜同步 {synced}`；`runtime.mail_watcher_available` 为 false 时开关禁用）
   - `Apple 登录态保活`（`enable_apple_keep_alive`，说明 `基础 {apple_keep_alive_ms/60000||3} 分钟；每 30 秒扫描并在每轮重新随机 ±{jitter_percent??15}%`；`apple_keep_alive_available` false 时禁用）
4. **iCloud Web API**（Cloud 图标，4 个开关卡）：
   - `Web API 取码与邮件刷新`（`enable_web_code_sync`，说明 `后台与公共取码、公共页面邮件刷新时使用 Web API 补查；默认关闭。`）
   - `Web API 手动邮件同步`（`enable_web_manual_mail_sync`，默认 true，说明 `用于表格、详情和全部已有邮箱的邮件补查与回退。`）
   - `Web API 后台邮件监听`（`enable_web_background_mail`，默认 true，说明 `用于首次扫描、IMAP IDLE 补查及低频轮询。`）
   - `Web API 远端邮件操作`（`enable_web_remote_mail_cleanup`，默认 true，说明 `允许移动邮件、清空废纸篓、云端清理及彻底删除邮箱。`）
5. **Server 酱消息推送**（卡片）：
   - 标题 `Server 酱消息推送`，副题 `通过 sct.ftqq.com 把关键运行事件推送到默认微信消息通道。`
   - `SendKey` 输入（mono，maxlength=180，placeholder `输入 SCT 开头的 SendKey`；帮助 `SendKey 默认显示，并使用本地数据库加密保存；推送使用 Server 酱网站配置的默认微信消息通道。`）
   - 3 个开关：`后台登录通知`（`notify_admin_login`，说明 `管理员成功登录后推送账号、时间、访问地址和浏览器信息。`）；`账号与登录态掉线通知`（`notify_account_login_state_offline`，说明 `Apple Account、iCloud Web 或 IMAP 由正常转为异常时推送，标题会显示具体 Apple 账号；发送额度由填写的 SendKey 套餐决定，本地不限制条数。`）；`隐藏调用 IP`（`server_chan_hide_ip`，默认 true，说明 `向 Server 酱提交 noip=1，消息中不显示本服务的外网调用 IP。`）
   - footer：状态点（`推送凭据已就绪` / `等待配置 SendKey`）+ 链接 `Server 酱控制台`（https://sct.ftqq.com/）+ 按钮 `发送测试`（busy `提交中`，未就绪禁用）。
6. **公共取号 АРI keys**（卡片）：
   - 标题 `公共取号 АРI keys`，说明 `外部调用取号、批量查询接口时使用；来源：{系统设置|config.json|尚未设置}。`；状态徽章 `已配置`/`待设置`。
   - key 输入（mono，密码/明文眼睛切换，placeholder：`config_api_key_configured` 时 `留空继续使用 config.json 中的 арi_keys`，否则 `输入或点击右侧按钮生成`）+ `生成新 Key` 按钮（前端生成 `ipm_` + 24 字节 base64url；toast `公共 АРI keys 已生成，请保存系统设置`）。
   - 端点提示：`POST /api/v1/mailboxes/claim`、`/email-code 打开页面`（新窗口）。
   - 注：`生成或修改后点击"保存系统设置"立即生效；公共邮箱取码页面不使用这个 Key。`
7. **版本与更新**（独立 panel，id=`version-updates`，路由 hash `#version-updates` 时滚动到此）：
   - 标题 `版本与更新`，副题 `根据仓库公告配置检查版本，无需 API Token；当前只提供查看。`
   - 按钮：`打开仓库`（有 repository_url 时，新窗口）+ `检查更新`（busy `正在检查`；`enabled===false` 时禁用文案 `检查更新已关闭`）。
   - 4 格信息：`当前版本 {current.version||'2.1.2'}`、`构建提交 {commit 前 12 位，unknown→'未写入'}`、`运行平台 {os / arch}`、`检查时间 {formatDate(checked_at)}`。
   - 状态区：error → 红卡 `检查更新失败 {error}`；有 latest → `发现新的项目内容`（琥珀，含 name/notes + `重新下载源码`链接）或 `当前已经是最新版本`（绿卡）；否则提示 `配置文件已关闭更新检查。` 或 `点击"检查更新"读取仓库公告配置。`

### 5.3 交互功能
| 操作 | API | toast |
|---|---|---|
| 保存系统设置（整表单提交） | `PUT /api/settings` body=form | 成功 `系统设置已保存` |
| 完整性检查 | `POST /api/database/check` body `{}` | `数据库完整性检查：${data.result}` |
| 立即备份 | `POST /api/database/backup` body `{}` | `数据库备份已创建：${data.path}` |
| 整理空间 | `POST /api/database/optimize` body `{}` | `数据库空间整理完成` |
| 数据库操作后 | `GET /api/database/status` → 更新 database_status | — |
| 发送 Server 酱测试 | `POST /api/server-chan/test` body `{send_key, hide_ip}` | `data.message \|\| 测试推送已加入 Server 酱队列` |
| 检查更新 | `GET /api/update/status?force=1`（经 useUpdates.loadUpdates(true)） | error→`status.error`；update_available→`发现新的项目版本或源码提交`；否则 `检查完成，当前已经是最新版本` |
| 生成新 Key | —（前端 crypto） | `公共 АРI keys 已生成，请保存系统设置` |

saving key：`system / server-chan-test / database-check / database-backup / database-optimize`。

### 5.4 数据轮询/实时
- 首次：`Promise.allSettled([load()（GET /api/settings）, loadUpdates()（GET /api/update/status）])`。
- 轮询：`refreshRuntime()`（`GET /api/settings`，只取 runtime）每 30s。
- SSE 订阅 `['settings','mailwatcher','apple-session','apple-account','mailbox']`：`mailwatcher` 带 payload.data 直接更新 `runtime.mail_watcher_status`；`settings` → 静默 load；其它 → refreshRuntime（120ms 防抖）。

### 5.5 表单字段（form 全部）
```
enable_mail_watcher: false        邮件后台监听（开关）
enable_apple_keep_alive: false    Apple 登录态保活（开关）
enable_public_mailbox_api: false  公共取号 API（开关）
enable_public_code_page: false    公共邮箱取码页面（开关）
enable_web_code_sync: false       Web API 取码与邮件刷新（开关）
enable_web_manual_mail_sync: true Web API 手动邮件同步（开关）
enable_web_background_mail: true  Web API 后台邮件监听（开关）
enable_web_remote_mail_cleanup: true Web API 远端邮件操作（开关）
public_api_key: ''                公共取号 АРI keys（文本/密码切换）
apple_account_module_ready: true  （随设置返回，无独立控件）
server_chan_send_key: ''          Server 酱 SendKey（文本，maxlength 180）
server_chan_hide_ip: true         隐藏调用 IP（开关）
notify_admin_login: false         后台登录通知（开关）
notify_account_login_state_offline: false 账号与登录态掉线通知（开关）
```

---

## 6. ExportsView（本地导出）

### 6.1 页面标题/副标题
页内命令栏标题：**本地导出**；副题：**下载运行数据、邮件、邮箱地址或取码 API**。右侧警示徽章：`包含敏感本地数据`（ShieldAlert）。

### 6.2 布局结构
1. **导出项 grid**（4 个 `<a download>` 卡片，顺序固定）：
   | 标题 | 描述 | href | 格式 |
   |---|---|---|---|
   | 运行数据 | 导出账号、邮箱、登录态和系统设置，不包含本地邮件正文。 | `/api/runtime/export` | JSON |
   | 运行数据与邮件 | 在完整运行数据中加入所有已同步的本地邮件内容。 | `/api/runtime/export?include_messages=1` | JSON |
   | 邮箱地址 | 只导出邮箱池中的隐私邮箱地址，方便导入其他本地工具。 | `/api/runtime/export-mailbox-emails?format=txt` | TXT |
   | 取码 API | 导出每个邮箱的地址与独立取码 API 链接。 | `/api/runtime/export-mailbox-apis?format=txt` | TXT |
   每卡：图标 + 标题/格式徽章 + 描述 + `下载`。
2. **导出环境**面板：标题 `导出环境`，副题 `当前服务生成文件时使用的运行信息`。3 格：`SQLite 数据库 {dataPath||'使用当前运行数据库'}`、`公共基础地址 {runtime.public_base_url||'按当前访问地址生成'}`、`全局 АРI keys {已配置（来源）|尚未设置}`。footer：`运行数据导出包含 Apple 登录态、Cookie 和 App 专用密码等内容，请将下载文件保存在可信位置。`

### 6.3 交互功能
仅 4 个下载链接（浏览器直接下载，无 JS 调用）。无确认框、无 toast（加载失败时 toast err.message）。

### 6.4 数据轮询/实时
- 首次 `GET /api/settings`（取 runtime/settings/data_path）。
- SSE 订阅 `'settings'` → 120ms 防抖重新 load。

### 6.5 表单字段
无。

---

## 7. PublicCodeView（公共取码页 /email-code，免登录）

### 7.1 页面标题/副标题
页头：Cloud 图标 + `获取验证码与邮件` + 副题 `公共邮箱取码与收件`。右侧：服务状态徽章（`服务已开启`/`服务未开启`，statusLoading 时转圈）+ 主题切换按钮（Sun/Moon）。**整页无侧边栏、无登录要求**。

### 7.2 布局结构（单列 max-w-5xl，带渐变光斑背景）
1. **邮箱输入卡片**（form）：`隐私邮箱地址` 输入（email 类型，placeholder `name@icloud.com`，服务未开启时禁用）+ 2 按钮：`获取邮件`（secondary，busy `正在同步`）、`获取验证码`（primary，submit）。
2. **邮件列表卡片**：header（图标 + `邮件列表 N[/total]` + 当前邮箱/占位 `获取邮件后会显示在这里` + `同步于 {formatTime(lastSyncAt)}` + 刷新按钮）；
   错误条（mailError，琥珀色）；加载态 `正在同步最新邮件`；列表行（图标 + subject + from + 内容类型徽章 + 时间 + ChevronRight，hover/focus 时预取详情）；空态：`等待获取邮件`/`当前邮箱暂无本地邮件` + 提示文案。
3. footer：`只会查询当前输入邮箱的验证码与邮件`。
4. **取码弹窗**（z-70）：与 Mailboxes 取码弹窗同结构——header（`邮箱取码`徽章 + 邮箱 + `同步最新邮件并提取验证码`）、busy 态 `正在获取验证码`、成功态（大验证码 + subject + `复制验证码`按钮，复制后 toast `验证码已复制`（2500ms）并显示勾 1.8s）、失败态（`暂未获取到验证码` + codeError + `关闭`）。
5. **完整邮件弹窗**（z-80）：同 Mailboxes 完整邮件弹窗（徽章 `完整邮件`+source+类型、subject、from、`收件邮箱：{loadedEmail}`、视图切换、iframe/pre 正文），另有 `selectedMessageError` 错误展示。

### 7.3 交互功能
| 操作 | API | 说明 |
|---|---|---|
| 页面初始化 | `GET /api/v1/public-code/status` → `{enabled}` | 失败时 enabled=false 且 mailError=err.message |
| 获取验证码（submit） | `GET /api/v1/public-code?email={encodeURIComponent(email)}&wait_ms=15000` | 邮箱需通过 `/^[^\s@]+@[^\s@]+\.[^\s@]+$/` 校验；500ms 后才显示转圈 |
| 获取邮件 | `GET /api/v1/public-code/messages?email={email}&sync=1&limit=50` | 350ms 后显示转圈；返回 `{items,total,last_sync_at,sync_error}`；成功后 0ms 延迟预取前 6 条详情 |
| 单条消息详情 | `GET /api/v1/public-code/messages/{encodeURIComponent(id)}?email={email}` | 带内存缓存（detail+in-flight request 两级，key=`${email}:${id}`） |
| 复制验证码 | clipboard | toast `验证码已复制` / `复制验证码失败，请重试` |
| 主题切换 | — | localStorage `ipm_v2_theme`，初始读存储否则 prefers-color-scheme |

URL query `?email=xxx` 预填邮箱。Esc：先关邮件详情，再关取码弹窗。

### 7.4 数据轮询/实时
无轮询、无 SSE。所有数据由用户操作触发。

### 7.5 表单字段
| 字段 | 类型 | 默认 |
|---|---|---|
| `email` 隐私邮箱地址 | email 文本 | URL query `email` 或空，required，小写化比较 |

### 7.6 HTML 邮件安全渲染
与 Mailboxes 相同的 buildEmailHTMLDocument，但额外：移除 `link[rel=stylesheet/preload/preconnect/dns-prefetch]`、style 中移除 `@font-face`、移除 1×1/2×2 追踪像素与 `/wf/open|pixel|track/` 图片、img 加 `loading=lazy decoding=async referrerpolicy=no-referrer`、CSS 禁用动画过渡、CSP 更严格（`img-src https: http: data:; style-src 'unsafe-inline'; font-src data:; media-src 'none'`）。

---

## 8. LoginView（登录页，免登录）

- 双栏卡片：左侧品牌区（lg 显示， emerald 渐变，`iCloud Privacy Mail 隐私邮箱控制台` + 标语 + 3 特性格 `Go 接口/Vue 3/本地运行`）；右侧表单。
- 标题：`setupRequired` 时 `首次运行 / 设置本地管理员 / 这是第一次启动，请创建唯一的本地管理员。`，否则 `安全登录 / 欢迎回来 / 输入本地管理员账号后进入控制台。`
- 字段：`账号`（默认 `admin`，autocomplete=username）、`密码`（placeholder `至少 8 位`，眼睛切换）、setupRequired 时加 `确认密码`（不一致 toast `两次输入的密码不一致`）。
- 提交：setupRequired → `POST /api/auth/setup`，否则 `POST /api/auth/login`，body `{username, password}`；成功 → `router.replace(route.query.redirect || '/')`；按钮文案 `创建管理员并进入` / `登录控制台`（busy `正在处理...`）。
- footer：`登录状态仅保存在安全 Cookie 中；公网部署时再启用 HTTPS 配置。`

---

## 9. 全局功能清单

### 9.1 侧边栏导航（Sidebar.vue）
- 品牌：Cloud 图标 + `Privacy Mail` + `本地控制台`，点击回 `/`。
- 分组标题 `工作区`，导航项（顺序，RouterLink + 图标 + ChevronRight）：
  1. `控制台` → dashboard（LayoutDashboard）
  2. `Apple 账号` → apple-accounts（Apple）
  3. `邮箱池` → mailboxes（Boxes）
  4. `创建隐私邮箱` → tasks（MailPlus）
  5. `本地导出` → exports（Download）
  6. `系统设置` → settings（Settings）
- footer：`本地服务正常`（绿点）+ 版本链接 `版本 v{currentVersion||'2.1.2'}[· {commit 前 7 位}]`（点击 → `{name:'settings', hash:'#version-updates'}`，title=`当前版本 vX，提交 xxx`）+ 主题切换。
- 移动端：抽屉式，backdrop 点击关闭；Esc 关闭。

### 9.2 顶栏（AppLayout.vue）
- 左：移动端菜单按钮 + 面包屑 `控制台 / {route.meta.title}` + 副标题 `{route.meta.subtitle}`。
- 右（顺序）：实时状态徽章（`实时已连接/实时连接中/实时重连中/实时已断开`，realtime-{status} class）、`本地模式`徽章（Activity 图标）、主题切换、公告中心、管理员菜单（头像 + username + ChevronDown → 下拉：`{username} 单用户本地控制台` + `退出登录`按钮）。
- 主题：localStorage `ipm_v2_theme`，`document.documentElement.classList.toggle('dark')`，初始读存储否则系统偏好。

### 9.3 登录/登出（useAuth.js）
- `GET /api/auth/status` → `{setup_required, authenticated, admin}`（authState 缓存，force 刷新）。
- 登录：`POST /api/auth/login`；首次：`POST /api/auth/setup`；body `{username, password}`。
- 登出：`POST /api/auth/logout` body `{}` → 清空状态、断开 SSE、`router.replace({name:'login'})`。

### 9.4 SSE realtime（useRealtime.js）
- 连接：`new EventSource('/api/realtime')`，监听 `change` 事件（JSON）。`realtimeState = { status: 'closed|connecting|connected|reconnecting', lastSequence }`；按 `sequence`（或 lastEventId）去重。AppLayout onMounted 连接，onBeforeUnmount 断开，登出时断开。
- 订阅：`subscribeRealtime(resources, callback)`（resources 可为字符串/数组/null；null=全部）返回退订函数。各页面订阅的资源见各视图 1.4/2.4/3.4/4.4/5.4/6.4 节。已知 resource 值：`scheduler, event, mailbox, mailbox-lease, message, apple-account, apple-session, apple-mail-cleanup, mailbox-message-sync, settings, mailwatcher, create-settings`。常见 operation：`created, updated, batch-updated`；payload 结构：`{operation?, data?, items?, messages?, created_message_count?}`。

### 9.5 公告中心（AnnouncementCenter.vue + useUpdates.js）
- 顶栏铃铛按钮：未读时 BellRing + 红点角标（`N` 或 `9+`），否则 Bell。点击展开下拉（点击外部/Esc 关闭）。
- 数据：`GET /api/update/status`（`loadUpdates()`，600ms 后才显示 checking 态）→ `status.announcements`（数组 `{id,type,title,summary,content,published_at,url}`）；force 时 `?force=1`。
- 类型映射：`update 版本更新（绿，PackageOpen）/ system 系统公告（紫，Sparkles）/ 其它 项目公告（蓝，Megaphone）`。
- 已读状态：localStorage `ipm_v2_read_announcements`（最多保留 200 条 id）。`全部已读`按钮；点击单条 → 标记已读并打开详情弹窗（标题、类型、时间、content/summary 全文、有 url 时 `查看相关页面`外链按钮）。
- 空态：`暂无公告` + `版本更新和项目消息会显示在这里。`

### 9.6 更新检查（useUpdates.js）
- `updateState = { loaded, loading, status, error }`；`currentVersion`（默认 `2.1.2`）、`currentCommit`（默认 `unknown`）供侧边栏/设置页使用。
- status 结构：`{ current:{version,commit,os,arch}, latest:{name,notes,url}, update_available, enabled, repository_url, checked_at, error, announcements[] }`。
- 仅 Settings 页和公告中心消费；Sidebar 显示版本。

### 9.7 通用组件（React 需重建）
- **CardSelect**：自定义下拉（单选/多选 multiple、compact、disabled、option 支持 `{value,label,description,dot,disabled}`；多选时选中多项显示 `已选择 N 项`；菜单 max-height 按前 5 项计算；Esc/外点关闭；选项右侧 Check）。
- **ConfirmDialog / useConfirm**：`confirm({title,message,confirmText='确定',cancelText='取消',tone='primary|danger'})` → Promise<boolean>；danger 红色确认按钮，Esc 取消，打开时聚焦确认按钮；同一时间只允许一个（新 confirm 会把旧的 resolve(false)）。
- **FormDialog**：`{open,title,description,busy,submitText='保存'}` + slot 表单 + `取消/保存`（busy 显示 `保存中`），外点关闭（busy 时禁止）。
- **ToastStack / useToast**：`success/error/info/warning(text, duration=5000)`、`push`、`update(id,text,type,duration)`（id 为 null 时新建，用于常驻进度条）、`dismiss(id)`。最多同时 3 条（超出移除最老的非 persistent）；`duration<=0` 为常驻。
- **ThemeToggle**：太阳/月亮切换按钮。

### 9.8 全局 API 端点索引（前端用到的全部）
```
POST /api/auth/setup | /api/auth/login | /api/auth/logout
GET  /api/auth/status
GET  /api/dashboard
GET  /api/tasks
GET  /api/events            POST /api/events/clear
GET  /api/apple-accounts    GET /api/apple-accounts/{id}    DELETE /api/apple-accounts/{id}
POST /api/apple-accounts/login/start    POST /api/apple-accounts/login/2fa
POST /api/apple-accounts/{id}/check     POST /api/apple-accounts/{id}/imap
POST /api/apple-accounts/{id}/mailboxes POST /api/apple-accounts/{id}/mailboxes/sync
GET  /api/mailboxes (page,page_size,q,account_id,status)    POST /api/mailboxes
POST /api/mailboxes/resolve
GET  /api/mailboxes/{id}    DELETE /api/mailboxes/{id}[?local_only=1]
GET  /api/mailboxes/{id}/messages    GET /api/mailboxes/{id}/messages/{messageID}
POST /api/mailboxes/{id}/status      POST /api/mailboxes/{id}/sync
GET  /api/mailboxes/{id}/code?allow_stale=1
POST /api/mailboxes/{id}/remote-clean
POST /api/mailboxes/sync-messages    GET /api/mailboxes/sync-messages/status
POST /api/apple-mail/cleanup         GET /api/apple-mail/cleanup/status
GET  /api/scheduler/status  POST /api/scheduler/start  POST /api/scheduler/stop  POST /api/scheduler/logs/clear
GET  /api/create-settings   PUT  /api/create-settings
GET  /api/settings          PUT  /api/settings
POST /api/server-chan/test
GET  /api/database/status   POST /api/database/check|backup|optimize
GET  /api/update/status[?force=1]
GET  /api/runtime/export[?include_messages=1]
GET  /api/runtime/export-mailbox-emails?format=txt
GET  /api/runtime/export-mailbox-apis?format=txt
GET  /api/v1/public-code/status
GET  /api/v1/public-code?email=&wait_ms=15000
GET  /api/v1/public-code/messages?email=&sync=1&limit=50
GET  /api/v1/public-code/messages/{id}?email=
POST /api/v1/mailboxes/claim   （仅在设置页文案中出现，前端不直接调用）
GET  /api/realtime  （SSE）
```
