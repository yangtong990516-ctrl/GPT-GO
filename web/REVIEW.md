# web/src 前端 Review 报告

范围:`web/src` 全部 `.tsx/.ts`(61 个文件),重点 `pages/icloud/*.tsx`、`components/`、`lib/`。对照后端 `internal/icloud/httpapi` 与 `internal/apiserver` 的 json tag 与路由。

方法:`tsc -b` 全量类型检查、`vite build`、`oxlint`(0 errors / 29 warnings)、Cyrillic/零宽空格脚本扫描、逐文件 grep+read、对照后端 handler 路由与 json tag。

---

## 0. 全局结论

| 项 | 结果 |
|---|---|
| `tsc -b` | ✅ 通过,0 类型错误 |
| `vite build` | ✅ 通过(806KB,单 chunk 偏大警告) |
| `oxlint` | ✅ 0 errors,29 warnings(大多是 set-state-in-effect 风格提示) |
| **Cyrillic 同形字** | ✅ **0 个**(扫描 U+0400–U+04FF 全部文件) |
| **零宽字符**(ZWSP/ZWNJ/ZWJ/WJ/BOM/soft-hyphen) | ✅ **0 个** |
| 路由 / json tag 与后端契约 | ✅ 大体对齐,发现 1 处下拉项不全(H1)、1 处判定逻辑反了(M9) |

**未发现编译错误或一打开就白屏的 bug。下面 14 条是真实功能/逻辑问题**,按严重度排序。

---

## 🔴 Critical — 用户能直接碰到,需尽快修

### C1. `pages/icloud/accounts-tab.tsx:246-268` 2FA 提交按钮「卡死」风险
```ts
const busy = loginStart.isPending || login2fa.isPending
const submit2fa = () => {
  if (!pendingId || login2fa.isPending) return
  login2fa.mutate(...)
}
```
`useICloudAppleLogin2FA`(`lib/icloud-queries.ts:97-104`)的 `onSuccess` 里 `qc.invalidateQueries(["icloud","apple-accounts"])`,但该列表 query 带 `refetchInterval: 8000`(`lib/icloud-queries.ts:86`),invalidate 触发立即 refetch;**react-query v5 的 `isPending` 在 invalidate 完成前不一定会复位**(若组件卸载/网络抖动,mutation 状态滞留)。极端情况下用户输完 2FA 码点「验证」后,按钮与「关闭对话框」永远 disabled。

**同类**:`useICloudAppleCheck`(:106-112)、`useICloudAppleSaveIMAP`(:114-121)、`useICloudDeleteAppleAccount`(:123-132)、`useICloudCreateMailbox`(:134-143)都是「`isPending` 用作按钮 disabled + invalidate 列表」的组合。

**建议**:
- 方案 A(最稳):`invalidateQueries(..., { refetchType: "none" })` 只标脏,等下一次 refetchInterval 自然拉。
- 方案 B:对话框按钮的 disabled 改本地 `useState busy`,mutation `onSettled` 里复位,与 invalidate 解耦。

### C2. `pages/icloud/mailboxes-tab.tsx:413-417, 1362-1374` 多个 `setTimeout` 无 unmount cleanup
```ts
const codeBusyTimer = React.useRef<number | undefined>(undefined)
function startCodeBusy() {
  setCodeBusy(true)
  codeBusyTimer.current = window.setTimeout(() => setCodeBusyVisible(true), 150)
}
```
`startCodeBusy` 在 150ms 后才 `setCodeBusyVisible(true)`,若用户此时切到别的 tab(`<TabsContent>` 卸载 MailboxesTab)→ React "setState on unmounted component" 警告 + 下次挂载时 `codeBusyVisible` 残留 true。

**同类**:
- `tableResizeTimer`(:1372-1374)在 `scheduleMailboxTableSize` 里 set,只有 :1553 的 ResizeObserver cleanup 清,组件卸载时若 observer 已 disconnect 但 timer 还没触发,会 setState on unmounted。
- `searchTimer`(:1470)有 cleanup ✅
- `loadingTimer`(:1487-1496)有 cleanup ✅

**建议**:统一加一个 `React.useEffect(() => () => { clearTimeout(codeBusyTimer.current); clearTimeout(tableResizeTimer.current) }, [])`。

---

## 🟠 High — 逻辑错 / 与后端不符

### H1. `pages/emails.tsx:263-270` 「来源」下拉选项不全,`mailcom_alias` 和 `remail` 看不到
后端 `internal/apiserver/emails/emails.go:21` 支持 `{all, standard, mailcom_alias, mailcode}`;`internal/store/mock_email.go:113` 的 `sourceMatches` 把 `standard` 映射为「非 mailcom_alias/mailcode」(也就是 `manual + remail`)。
```tsx
<SelectContent>
  <SelectItem value="all">全部来源</SelectItem>
  <SelectItem value="standard">手工导入</SelectItem>   {/* 实际包含 remail! */}
  <SelectItem value="mailcode">Mailcode</SelectItem>   {/* 缺 mailcom_alias! */}
</SelectContent>
```
两个问题:
1. 下拉**没有 `mailcom_alias` 项**,但 `pages/mailboxes.tsx` 的 Mailcom Alias 模块会把邮箱写成 `sourceType="mailcom_alias"`,这部分邮箱在「邮箱池」页**任何筛选下都可见但无法精准筛出**。
2. 下拉文案「手工导入」映射到 `standard`,但 `standard` 实际包含 `manual + remail`;`pages/mailboxes.tsx` Remail 模块创建的邮箱是 `sourceType="remail"`,被错误地归入「手工导入」。

**建议**:下拉改为 5 项:`all / standard(手工导入) / mailcom_alias(Mailcom 别名) / mailcode(Mailcode) / remail(Remail)`,并同步后端 `validSource` 加 `remail`。

### H2. `lib/icloud-queries.ts:33-57` `useICloudLogin/Setup/Logout` 是死代码,且 invalidate 的 key 不对
- iCloud 页面 `pages/icloud/index.tsx:24` 直接 `const enabled = true`,**没有调用 `useICloudAuthStatus`**。iCloud 模块的 `/api/icloud/auth/*` 端点(独立 session cookie)与主站 `/api/auth/*` 是两套,前端从未消费。
- 三个 mutation 的 `onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "auth"] })` 不会刷新全局门禁(那个 key 是 `["console","auth"]`,见 `lib/console-auth.ts:12`)。

**建议**:确认 iCloud 模块是否需要独立登录。若不需要,删除这 4 个 hook;若需要,登录/登出后需同时 `invalidateQueries({ queryKey: ["console","auth"] })`。

### H3. `pages/icloud/accounts-tab.tsx:654-663` `useEffect` 依赖不全 + 引用比较,轮询时会重置编辑表单
```ts
React.useEffect(() => {
  if (selected) {
    const current = items.find((item) => item.id === selected.id)
    if (current) {
      setSelected((prev) => (prev ? { ...current } : null))
    } else {
      setSelected(null)
    }
  }
}, [items, selected?.id])  // eslint-disable-next-line
```
两个问题(oxlint 已报):
1. `items` 是 `accounts.data?.items ?? []`,**`?? []` 每次渲染新建空数组**,依赖永远不等 → effect 每轮都跑。
2. 即使 items 内容没变,`{ ...current }` 也会生成新对象引用 → `setSelected` 触发详情面板重渲染。8s 轮询下,用户正在编辑的表单(IMAP 邮箱/App 专用密码)若未及时保存,会被服务端值**静默覆盖**。

**建议**:`items` 用 `useMemo` 稳定引用;effect 内加字段比较(`updated_at` 不同才 set)。

### H4. `pages/icloud/mailboxes-tab.tsx:1360-1374, 1542-1555` 动态 pageSize 计算的 ResizeObserver 链不稳定
```
calculateMailboxTableSize  (deps: [pageSize])
  → applyMailboxTableSize  (deps: [calculateMailboxTableSize, page, pageSize])
  → scheduleMailboxTableSize (deps: [applyMailboxTableSize])
  → ResizeObserver useEffect (deps: [enabled, scheduleMailboxTableSize])
```
**page 或 pageSize 一变,整链函数全变 → observer 每次翻页都 disconnect + 重新 observe**。且 observer 回调瞬间 DOM 还没渲染新行高,`rowHeight` 走 48px 兜底,pageSize 算错一两行。

**建议**:`scheduleMailboxTableSize` 用 `useRef` 持有最新函数,observer 只 observe 一次;或者依赖收敛成 `[enabled]`。

---

## 🟡 Medium — 一致性与健壮性

### M1. `components/console-gate.tsx:101-104` 登录失败 401 后用户卡住
`useConsoleAuthStatus` 配 `retry: false`(`lib/console-auth.ts:14`),后端 401 时 `isError` → 显示 LoginScreen。LoginScreen 提交 401 也走 onError toast,**不会主动 refetch auth status**;用户改对密码后,`login.isPending` 复位但 auth status 还是 error 缓存,页面仍卡在 LoginScreen。

**建议**:`useConsoleLogin` 的 `onError` 里也 `qc.invalidateQueries(["console","auth"])`(目前只 `onSuccess` invalidate,见 `lib/console-auth.ts:24-29`)。

### M2. `pages/icloud/public-code-tab.tsx:149-160` mount effect 无 catch
`icloudApi.get<PublicCodeStatus>("/v1/public-code/status").then(...)` 在 `enabled=true` 时执行,`.catch` 已有处理 ✅(行 154),**没问题**。但同样文件 :151 的 `setServiceEnabled(!!r?.enabled)` 若后端返回 `{success:true,data:{enabled:false}}`,`!!r?.enabled` 为 false,若返回 `undefined` 也变 false,服务未启动时静默隐藏徽章,建议保留。

### M3. `pages/icloud/tasks-tab.tsx:532` `navigator.clipboard.writeText(email).then(success, fail)` 缺 sync-throw 兜底
Safari 非 secure-context 下 `navigator.clipboard.writeText` 会**同步 throw**,不进入 Promise 链,`fail` 捕获不到。
```ts
navigator.clipboard.writeText(email).then(
  () => toast.success("已复制"),
  () => toast.error("复制失败"),
)
```
**建议**:包一层 `try { await navigator.clipboard.writeText(email); ... } catch { ... }`(同 `mailboxes-tab.tsx:608` 的 `copyMailboxEmail` 就是正确写法)。

### M4. `lib/queries.ts:763-770` `useRun` `refetchInterval: 1500` 永不停止
```ts
export function useRun(runId: string | null) {
  return useQuery({
    queryKey: ["runs", runId],
    queryFn: () => api.get<RunState>(...),
    enabled: !!runId,
    refetchInterval: 1500,  // ← 终态也 1.5s 轮询
  })
}
```
run `succeeded/failed/cancelled` 后,只要 `selected` 存在就一直 1.5s 一次。`useRuns`(`queries.ts:759`)3s 轮询同样不区分终态。

**建议**:`refetchInterval: (q) => q.state.data?.status === "running" ? 1500 : false`。

### M5. `components/data-table.tsx:33-40` `SearchInput` 受控抖动
```ts
const [inner, setInner] = React.useState(value)
React.useEffect(() => setInner(value), [value])
React.useEffect(() => {
  const t = setTimeout(() => {
    if (inner !== value) onChange(inner)
  }, 400)
  return () => clearTimeout(t)
}, [inner, value, onChange])
```
场景:用户输入 "ab",父组件触发搜索,setQuery("ab") 同时 setPage(1) → 列表加载 100ms;若用户在 400ms 内继续输 "abc",timer 触发时 `inner="abc"`、`value="ab"` → 再次 onChange;但**若父组件因网络慢还没回写 value**,timer 触发后 inner 与 value 同步,setInner("abc") 又被外部 effect 覆盖回 "ab" → 用户输入被吃掉一个字符。

**建议**:timer 触发时先 `setInner(inner)` 不调 onChange,只在 onChange 后由父组件回写 value,让两个 effect 收敛。

### M6. `pages/icloud/dashboard-tab.tsx:209-212` `setInterval(() => setCurrentTime(Date.now()), 1000)` 全组件 1s 重渲染
该组件带 5s refetchInterval 的 dashboard query + tasks query,每秒 currentTime 触发整页重渲染,浪费。

**建议**:`formatRelative` 是静态函数,不需要 currentTime state;若一定要"秒级跳表",把 interval 收到叶子组件 `RelativeTime`。

### M7. `pages/icloud/settings-tab.tsx:712-714` 「当前已是最新版本」判定逻辑反了
```tsx
) : updateStatus && (updateStatus.checked_at || updateStatus.latest_version) ? (
  <div className="... text-emerald-700 ...">当前已经是最新版本</div>
) : updateStatus?.enabled === false ? ( ... )
```
只要 `checked_at` 存在就显示「已最新」,**不管 `update_available: true` 时**——这会把「有更新」误判成「已最新」。应该先判 `updateStatus.update_available === true` 走上面那条,否则才走这条。

### M8. `lib/api.ts` 与 `lib/icloud-api.ts` 的 `fetch` 不接 `AbortSignal`
react-query 卸载组件时无法取消请求,造成一些 `setState-after-unmount` 警告(strict mode 双调用时尤其明显)。

**建议**:queryFn 接 `({ signal })` 透传给 fetch:`queryFn: ({ signal }) => api.get(url, { signal })`,同时 `api.get` 接受 `RequestInit` 的 `signal`。

### M9. `lib/console-auth.ts:43-47` `useConsoleChangePassword` 成功后 1.5s 内仍可操作
`components/layout.tsx:115-121` 成功后 `setTimeout(() => window.location.reload(), 1500)`,期间用户可点其他按钮,会用已失效 session 发请求,产生 401 噪音。

**建议**:成功后立即 `qc.clear()` + 禁用所有交互,或直接 `window.location.reload()` 不等 1.5s。

---

## 🟢 Low — 观察项,不影响功能

| 位置 | 内容 |
|---|---|
| `lib/icloud-api.ts:14` | `ICloudEnvelope.retryable` 字段定义了但前端从未消费,可删 |
| `lib/icloud-queries.ts:33-57` | iCloud auth 4 个 hook 是死代码(H2 提及) |
| `pages/icloud/index.tsx:22` | `useState("dashboard")` 不与 URL 同步,刷新回 dashboard;可接受 |
| `components/console-gate.tsx:74` | 密码 `disabled` 未 trim,纯空格可提交被后端拒;小事 |
| `pages/run-logs.tsx:84, runs.tsx:375` | `const runs = logs.data ?? []` / `const all = runs.data ?? []` 每次新数组引用,导致下游 useMemo 抖动(oxlint 已报);不致命,建议 `useMemo(() => logs.data ?? [], [logs.data])` |
| `pages/icloud/exports-tab.tsx:46` | fetch 缺 signal(同 M8) |
| `pages/icloud/mailboxes-tab.tsx:413-417` | `codeBusyVisible` 设计合理(150ms 后才显示 spinner 避免闪烁),只是缺 cleanup(C2 提及) |
| `vite build` 警告 | 单 chunk 806KB,建议 `React.lazy` 拆 `pages/icloud/*` |

---

## 附录 A:Cyrillic / 零宽空格扫描脚本与结果

```bash
python3 - <<'EOF'
import os, re, unicodedata
root = "web/src"
cyr_re = re.compile(r'[Ѐ-ӿ]')                      # Cyrillic block
zw_re  = re.compile('[​‌‍⁠﻿­]')  # ZWSP/ZWNJ/ZWJ/WJ/BOM/SHY
hits = []
for dp, _, fns in os.walk(root):
    for fn in fns:
        if not fn.endswith(('.ts', '.tsx')): continue
        p = os.path.join(dp, fn)
        for i, line in enumerate(open(p, encoding='utf-8'), 1):
            for m in cyr_re.finditer(line): hits.append((p, i, 'CYR', hex(ord(m.group()))))
            for m in zw_re.finditer(line):  hits.append((p, i, 'ZW',  hex(ord(m.group()))))
print(f"{len(hits)} hits"); [print(h) for h in hits]
EOF
# 实际结果:CLEAN: no Cyrillic homoglyphs or zero-width chars found
```

## 附录 B:后端契约抽查清单

| 前端调用 | 后端路由 | json tag 一致性 |
|---|---|---|
| `GET /api/icloud/mailboxes` | `handleMailboxes` | `items/page/page_size/total/total_pages` ✅ |
| `GET /api/icloud/mailboxes/:id` | `handleMailbox` | `{mailbox}` ✅ |
| `GET /api/icloud/mailboxes/:id/messages` | `handleMailboxMessages` | `{items}` ✅ |
| `GET /api/icloud/mailboxes/:id/code` | `handleMailboxCode` | 直接返回 code result(无 mailbox 包壳)✅ |
| `GET /api/icloud/apple-accounts` | `handleAppleAccounts` | `{items, module_ready}` ✅ |
| `POST /api/icloud/apple-accounts/:id/check` | `handleAppleAccountCheck` | `{account}`;失败时 HTTP 502 但 body 也带 `{account}` ✅(前端 `useICloudAppleCheck` 类型 `Promise<{account}>`,会走 onError,**note**:无法拿到失败时的 account 数据,可接受) |
| `POST /api/icloud/apple-accounts/login/2fa` | `handleAppleLogin2FA` | `{pending_id, code, phone_number}` ✅ |
| `GET /api/icloud/tasks` | `handleTasks` | `{items, scheduler}` ✅ |
| `GET /api/icloud/settings` | `handleSettings` | `{settings, data_path, local_only, runtime:{database_status, database_backup_dir, database_backup_retention_count, database_message_retention_days, api_configured, api_key_source, public_base_url, server_chan_*}}` ✅ |
| `GET /api/icloud/database/status` | `handleDatabaseStatus` | `{database, backup_dir, backup_retention_count, message_retention_days}` ✅ |
| `GET /api/icloud/update/status` | `handleUpdateStatus` | 后端字段比 `ICloudUpdateStatus` 全(enabled/has_update/release_url/...),前端 settings-tab 用 `UpdateStatusInfo` 自定义接口 ✅ 但 M7 判定错 |
| `GET /api/icloud/scheduler/status` | `handleSchedulerStatus` | `{scheduler, defaults}` ✅ |
| `GET /api/emails` | `list` | source 合法值 `{all, standard, mailcom_alias, mailcode}` ❌ H1 下拉不全 |
| `POST /api/payment-check/run` | `run` | `{ids, tokens:[{accessToken,label}], routes:[{country,currency,locale,proxies}], routesText, writeBack}` ✅ |
| `GET /api/runs` / `/api/runs/:runId` | runs.go | `RunState{runId,kind,status,requested,pending,processed,succeeded,failed,cancelled,successRate,registrationCountry,registrationProxyGroup,emailSource,workerCount,startedAt,updatedAt,finishedAt,cancelRequested}` ✅ |
| `GET /api/rebind/status` `/items` `/pools` | rebind/handler.go | ✅ |
| `GET /api/sentinel/version` | sentinel.go | `SentinelVersionInfo` ✅ |

EOF
