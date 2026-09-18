import * as React from "react"
import { useSearchParams } from "react-router-dom"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  CheckCircle2,
  CircleStop,
  Link2,
  MailQuestion,
  Play,
  RefreshCw,
  ScrollText,
  XCircle,
} from "lucide-react"
import { PageHeader } from "@/components/page-header"
import { HttpError } from "@/lib/api"
import {
  useAccounts,
  useCancelRebind,
  useEmails,
  useProxyCountries,
  useRebindItems,
  useRebindPools,
  useRebindStatus,
  useRunRebind,
} from "@/lib/queries"
import type { AccountRecord } from "@/lib/types"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"

// ── 类型 ──

interface LogEntry {
  timestamp: string
  level: string
  event: string
  message: string
  email?: string
}

// 换绑配对：账号 → 邮箱。
interface Pair {
  accountId: string
  emailId: string
}

// ── 子组件：账号选择列表 ──

function AccountPicker({
  accounts,
  pairs,
  emails,
  selectedAccount,
  onToggle,
  onSelect,
}: {
  accounts: AccountRecord[]
  pairs: Map<string, Pair> // accountId → Pair
  emails: { id: string; email: string }[]
  selectedAccount: string | null
  onToggle: (accountId: string) => void
  onSelect: (accountId: string | null) => void
}) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm">
          <MailQuestion className="size-4" /> 换绑账号（{pairs.size} 个已选）
        </CardTitle>
        <CardDescription>
          勾选加入批次；点击行选中账号（高亮）→ 再到右列点邮箱完成配对
        </CardDescription>
      </CardHeader>
      <CardContent className="max-h-96 overflow-y-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-10" />
              <TableHead>账号邮箱（换绑前）</TableHead>
              <TableHead className="w-20">状态</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {accounts.map((a) => (
              <TableRow
                key={a.id}
                className={`cursor-pointer transition-colors ${
                  selectedAccount === a.id ? "bg-primary/5 ring-1 ring-primary/20" : "hover:bg-muted/50"
                }`}
                onClick={() => onSelect(a.id)}
              >
                <TableCell onClick={(e) => e.stopPropagation()}>
                  <Checkbox
                    checked={pairs.has(a.id)}
                    onCheckedChange={() => onToggle(a.id)}
                  />
                </TableCell>
                <TableCell className="font-mono text-xs">
                  <div>{a.email}</div>
                  {pairs.has(a.id) && (
                    <div className="text-emerald-600 dark:text-emerald-400 text-xs mt-0.5">
                      → {emails.find((e) => e.id === pairs.get(a.id)?.emailId)?.email ?? "已配对"}
                    </div>
                  )}
                  {!pairs.has(a.id) && (
                    <div className="text-muted-foreground text-xs mt-0.5">待配对邮箱</div>
                  )}
                  {a.rebindStatus === "success" && (
                    <Badge variant="secondary" className="ml-0 mt-1 text-xs">已换绑</Badge>
                  )}
                </TableCell>
                <TableCell>
                  <Badge variant="outline" className="text-xs">
                    {a.accountType ?? "free"}
                  </Badge>
                </TableCell>
              </TableRow>
            ))}
            {accounts.length === 0 && (
              <TableRow>
                <TableCell colSpan={3} className="text-muted-foreground py-6 text-center text-sm">
                  没有可换绑的账号（需要 AccessToken）
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}

// ── 子组件：邮箱选择列表 ──

function EmailPicker({
  emails,
  pairs,
  selectedAccount,
  onAssign,
}: {
  emails: { id: string; email: string; status: string; sourceType?: string }[]
  pairs: Map<string, Pair>
  selectedAccount: string | null // 当前在左列选中的账号（点邮箱分配给它）
  onAssign: (accountId: string, emailId: string) => void
}) {
  const usedEmailIds = new Set(Array.from(pairs.values()).map((p) => p.emailId))
  const availableEmails = emails.filter((e) => e.status === "available")
  const noSelection = selectedAccount == null

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm">
          <Link2 className="size-4" /> 目标邮箱（{availableEmails.length} 个可用）
          {noSelection && (
            <Badge variant="outline" className="text-xs text-amber-600">
              先点左侧账号
            </Badge>
          )}
        </CardTitle>
        <CardDescription>
          {noSelection
            ? "先在左侧点击选中一个账号，再回到这里点目标邮箱完成配对"
            : "点击目标邮箱分配给左侧选中的账号（绿色 = 已配对，不可重复选）"}
        </CardDescription>
      </CardHeader>
      <CardContent className="max-h-96 overflow-y-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>新邮箱（换绑后）</TableHead>
              <TableHead className="w-20">状态</TableHead>
              <TableHead className="w-32">分配给</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {availableEmails.map((e) => {
              const assignedTo = Array.from(pairs.entries()).find(
                ([, p]) => p.emailId === e.id,
              )?.[0]
              const isUsed = usedEmailIds.has(e.id)
              return (
                <TableRow
                  key={e.id}
                  className={`transition-colors ${
                    isUsed || noSelection
                      ? "opacity-50"
                      : "cursor-pointer hover:bg-muted/50 hover:ring-1 hover:ring-primary/30"
                  }`}
                  onClick={() => {
                    if (!isUsed && selectedAccount) {
                      onAssign(selectedAccount, e.id)
                    }
                  }}
                >
                  <TableCell className="font-mono text-xs">{e.email}</TableCell>
                  <TableCell>
                    <Badge variant={isUsed ? "secondary" : "outline"} className="text-xs">
                      {isUsed ? "已分配" : "可用"}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    {isUsed ? (
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span className="cursor-help text-xs text-emerald-600 dark:text-emerald-400">
                            {assignedTo?.slice(0, 8)}…
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>
                          <p>分配给账号 {assignedTo}</p>
                        </TooltipContent>
                      </Tooltip>
                    ) : (
                      <span className="text-muted-foreground text-xs">—</span>
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
            {availableEmails.length === 0 && (
              <TableRow>
                <TableCell colSpan={3} className="text-muted-foreground py-6 text-center text-sm">
                  邮箱池无可用邮箱（去邮箱池导入）
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}

// ── 子组件：配对预览 + 启动 ──

function PairPreview({
  pairs,
  accounts,
  emails,
  onRemove,
}: {
  pairs: Map<string, Pair>
  accounts: AccountRecord[]
  emails: { id: string; email: string }[]
  onRemove: (accountId: string) => void
}) {
  const accountById = new Map(accounts.map((a) => [a.id, a]))
  const emailById = new Map(emails.map((e) => [e.id, e]))

  if (pairs.size === 0) {
    return (
      <Card>
        <CardContent className="text-muted-foreground py-8 text-center text-sm">
          先在左侧勾选账号、在右侧点选邮箱，配对关系会显示在这里
        </CardContent>
      </Card>
    )
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm">配对关系（{pairs.size} 对）</CardTitle>
        <CardDescription>
          换绑前邮箱 → 换绑后邮箱；点 × 移除配对
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className="space-y-2">
          {Array.from(pairs.entries()).map(([accountId, pair]) => {
            const account = accountById.get(accountId)
            const email = emailById.get(pair.emailId)
            return (
              <div
                key={accountId}
                className="flex items-center justify-between rounded-md border px-3 py-2 text-sm"
              >
                <div className="flex items-center gap-2 font-mono text-xs">
                  <span className="text-muted-foreground">
                    {account?.email ?? accountId}
                  </span>
                  <span className="text-primary">→</span>
                  <span className="text-emerald-600 dark:text-emerald-400">
                    {email?.email ?? pair.emailId}
                  </span>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-6 w-6 p-0"
                  onClick={() => onRemove(accountId)}
                >
                  ×
                </Button>
              </div>
            )
          })}
        </div>
      </CardContent>
    </Card>
  )
}

// ── 子组件：实时日志面板 ──

function LogPanel({ batchId, running }: { batchId: string | null; running: boolean }) {
  const [logs, setLogs] = React.useState<LogEntry[]>([])
  const logRef = React.useRef<HTMLDivElement>(null)

  // SSE 订阅批次日志流。
  React.useEffect(() => {
    if (!batchId) return
    const es = new EventSource(`/api/run-logs/runs/${batchId}/stream`)
    es.onmessage = (ev) => {
      try {
        const entry = JSON.parse(ev.data) as LogEntry
        setLogs((prev) => [...prev.slice(-199), entry])
      } catch {
        // 非 JSON 消息（如心跳）忽略
      }
    }
    es.onerror = () => es.close()
    return () => es.close()
  }, [batchId])

  // 自动滚动到底部。
  React.useEffect(() => {
    if (logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight
    }
  }, [logs])

  if (!batchId) {
    return (
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="flex items-center gap-2 text-sm">
            <ScrollText className="size-4" /> 运行日志
          </CardTitle>
        </CardHeader>
        <CardContent className="text-muted-foreground py-8 text-center text-sm">
          启动批次后这里会实时显示换绑每一步的日志
        </CardContent>
      </Card>
    )
  }

  const levelColor = (level: string) => {
    switch (level) {
      case "error":
        return "text-red-500"
      case "warn":
        return "text-amber-500"
      case "info":
        return "text-emerald-500"
      default:
        return "text-muted-foreground"
    }
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm">
          <ScrollText className="size-4" /> 运行日志
          {running && <Badge variant="secondary" className="text-xs">实时</Badge>}
        </CardTitle>
        <CardDescription>
          批次 {batchId.slice(0, 16)}… 的每一步操作实时推送
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div
          ref={logRef}
          className="max-h-64 space-y-1 overflow-y-auto rounded-md bg-muted/30 p-3 font-mono text-xs"
        >
          {logs.length === 0 ? (
            <div className="text-muted-foreground py-4 text-center">
              等待日志…
            </div>
          ) : (
            logs.map((log, i) => (
              <div key={i} className="flex gap-2">
                <span className="text-muted-foreground shrink-0">
                  {new Date(log.timestamp).toLocaleTimeString()}
                </span>
                <span className={`shrink-0 ${levelColor(log.level)}`}>
                  [{log.level.toUpperCase()}]
                </span>
                <span className="break-all">{log.message}</span>
              </div>
            ))
          )}
        </div>
      </CardContent>
    </Card>
  )
}

// ── 主页面 ──

export default function RebindPage() {
  const [params] = useSearchParams()
  const qc = useQueryClient()
  const presetIds = React.useMemo(
    () => (params.get("ids") ?? "").split(",").filter(Boolean),
    [params],
  )

  // 账号池数据（只显示有 AT 的）。
  const accountsQuery = useAccounts({ page: 1, pageSize: 100 })
  const allAccounts: AccountRecord[] = React.useMemo(
    () => (accountsQuery.data?.items ?? []).filter((a) => !a.accessTokenMissing),
    [accountsQuery.data],
  )

  // 邮箱池数据（available 的）。
  const emailsQuery = useEmails({ page: 1, pageSize: 100, status: "available" })
  const availableEmails = React.useMemo(
    () => emailsQuery.data?.items ?? [],
    [emailsQuery.data],
  )

  // 配对关系：accountId → Pair。
  const [pairs, setPairs] = React.useState<Map<string, Pair>>(new Map())
  // 当前选中的账号（点右列邮箱分配给它）。
  const [selectedAccount, setSelectedAccount] = React.useState<string | null>(null)
  // 换绑设置。
  const [country, setCountry] = React.useState("")
  const [concurrency, setConcurrency] = React.useState(2)
  // 资源池（可用邮箱/代理数量）。
  const pools = useRebindPools(country || undefined)
  // 代理池国家列表(动态:国家 + 代理数),替换原写死的 越南/菲律宾/美国。
  const proxyCountries = useProxyCountries()

  // 预设账号自动配对（从账号池跳转过来时）。
  React.useEffect(() => {
    if (presetIds.length > 0 && availableEmails.length > 0) {
      setPairs((prev) => {
        const next = new Map(prev)
        let emailIdx = 0
        const usedEmailIds = new Set(Array.from(prev.values()).map((p) => p.emailId))
        for (const accountId of presetIds) {
          if (!next.has(accountId) && emailIdx < availableEmails.length) {
            // 跳过已被占用的邮箱。
            while (emailIdx < availableEmails.length && usedEmailIds.has(availableEmails[emailIdx].id)) {
              emailIdx++
            }
            if (emailIdx < availableEmails.length) {
              next.set(accountId, { accountId, emailId: availableEmails[emailIdx].id })
              usedEmailIds.add(availableEmails[emailIdx].id)
              emailIdx++
            }
          }
        }
        return next
      })
    }
  }, [presetIds, availableEmails])

  // 切换账号勾选（只标记加入批次，配对交给点击邮箱完成）。
  const toggleAccount = (accountId: string) => {
    setPairs((prev) => {
      const next = new Map(prev)
      if (next.has(accountId)) {
        next.delete(accountId)
      }
      // 勾选后不自动配对，等用户点击邮箱完成配对。
      return next
    })
    // 勾选后自动选中该账号（方便直接点邮箱配对）。
    setSelectedAccount((prev) => (prev === accountId ? null : accountId))
  }

  // 手动分配邮箱（点右列邮箱行触发）。
  const assignEmail = (accountId: string, emailId: string) => {
    setPairs((prev) => {
      const next = new Map(prev)
      // 先移除该邮箱的旧配对。
      for (const [aid, p] of next.entries()) {
        if (p.emailId === emailId) next.delete(aid)
      }
      next.set(accountId, { accountId, emailId })
      return next
    })
    setSelectedAccount(null) // 分配后清除选中态
  }

  // 移除配对。
  const removePair = (accountId: string) => {
    setPairs((prev) => {
      const next = new Map(prev)
      next.delete(accountId)
      return next
    })
  }

  // 批次状态轮询。
  const [polling, setPolling] = React.useState(false)
  const [currentBatchId, setCurrentBatchId] = React.useState<string | null>(null)
  const batch = useRebindStatus(polling)
  const batchRunning = batch.data && "running" in batch.data ? batch.data.running : false
  const items = useRebindItems(polling || (batch.data != null && "done" in batch.data && batch.data.done > 0))
  React.useEffect(() => {
    setPolling(batchRunning)
    if (!batchRunning && batch.data && "done" in batch.data && batch.data.done > 0) {
      qc.invalidateQueries({ queryKey: ["accounts"] })
      qc.invalidateQueries({ queryKey: ["emails"] })
    }
  }, [batchRunning, batch.data, qc])

  const runRebind = useRunRebind()
  const cancelRebind = useCancelRebind()

  const doRun = () => {
    if (pairs.size === 0) {
      toast.error("请先勾选账号并配对邮箱")
      return
    }
    // 配对关系转成后端格式：accountId → emailId。
    const pairsPayload: Record<string, string> = {}
    for (const [accountId, pair] of pairs.entries()) {
      pairsPayload[accountId] = pair.emailId
    }
    runRebind.mutate(
      {
        ids: Array.from(pairs.keys()),
        country: country || undefined,
        pairs: pairsPayload,
      },
      {
        onSuccess: (r) => {
          toast.success(`换绑批次已启动：${r.total} 个账号`)
          setCurrentBatchId(r.batchId)
          setPolling(true)
        },
        onError: (e) => toast.error(e instanceof HttpError ? e.message : "启动失败"),
      },
    )
  }

  return (
    <div>
      <PageHeader
        title="邮箱换绑"
        description="勾选账号 + 点选目标邮箱 → 配对换绑；实时日志看每一步操作"
        actions={
          <>
            {batchRunning ? (
              <Button variant="destructive" size="sm" onClick={() => cancelRebind.mutate()}>
                <CircleStop /> 停止批次
              </Button>
            ) : (
              <Button size="sm" onClick={doRun} disabled={runRebind.isPending || pairs.size === 0}>
                <Play /> 开始换绑（{pairs.size}）
              </Button>
            )}
          </>
        }
      />

      {/* 换绑设置（代理国家 + 并发上限） */}
      <Card className="mb-4">
        <CardContent className="flex flex-wrap items-center gap-x-6 gap-y-3 py-3">
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">目标国家：</span>
            <select
              className="rounded-md border bg-background px-2 py-1 text-sm"
              value={country}
              onChange={(e) => setCountry(e.target.value)}
            >
              <option value="">任意</option>
              {(proxyCountries.data ?? []).map((c) => (
                <option key={c.country} value={c.country}>
                  {c.country}（{c.enabled} 条代理）
                </option>
              ))}
            </select>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">并发上限：</span>
            <select
              className="rounded-md border bg-background px-2 py-1 text-sm"
              value={concurrency}
              onChange={(e) => setConcurrency(Number(e.target.value))}
            >
              <option value={1}>1（串行）</option>
              <option value={2}>2（默认）</option>
              <option value={3}>3</option>
              <option value={5}>5</option>
            </select>
            <span className="text-xs text-muted-foreground">
              实际并发 = min(并发上限, 可用代理数)
            </span>
          </div>
          <div className="text-xs text-muted-foreground">
            可用邮箱 {availableEmails.length} 个 · 可用代理 {pools.data?.eligibleProxies ?? "—"} 个
          </div>
        </CardContent>
      </Card>

      {/* 批次进度条 */}
      {batch.data && "total" in batch.data && (
        <Card className="mb-4">
          <CardContent className="flex flex-wrap items-center gap-x-6 gap-y-2 py-3 text-sm">
            <span>
              进度：<strong>{batch.data.done}/{batch.data.total}</strong>
            </span>
            <span className="flex items-center gap-1">
              <CheckCircle2 className="size-4 text-emerald-500" />
              成功：<strong>{batch.data.success}</strong>
            </span>
            <span className="flex items-center gap-1">
              <XCircle className="size-4 text-red-500" />
              失败：<strong>{batch.data.failed}</strong>
            </span>
            <span className={batchRunning ? "text-primary" : "text-muted-foreground"}>
              {batchRunning ? "运行中…" : batch.data.canceled ? "已取消" : "已完成"}
            </span>
            <Button variant="ghost" size="sm" onClick={() => batch.refetch()}>
              <RefreshCw /> 刷新
            </Button>
          </CardContent>
        </Card>
      )}

      {/* 双列表配对 + 日志面板 */}
      <div className="grid gap-4 lg:grid-cols-3">
        <AccountPicker
          accounts={allAccounts}
          pairs={pairs}
          emails={availableEmails}
          selectedAccount={selectedAccount}
          onToggle={toggleAccount}
          onSelect={setSelectedAccount}
        />
        <EmailPicker
          emails={availableEmails}
          pairs={pairs}
          selectedAccount={selectedAccount}
          onAssign={assignEmail}
        />
        <div className="space-y-4">
          <PairPreview
            pairs={pairs}
            accounts={allAccounts}
            emails={availableEmails}
            onRemove={removePair}
          />
          <LogPanel batchId={currentBatchId} running={batchRunning} />
        </div>
      </div>

      {/* 结果矩阵（换绑完成后显示） */}
      {items.data && items.data.items.length > 0 && (
        <Card className="mt-4">
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">换绑结果（{items.data.items.length} 个）</CardTitle>
          </CardHeader>
          <CardContent className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>换绑前邮箱</TableHead>
                  <TableHead>换绑后邮箱</TableHead>
                  <TableHead className="w-20">状态</TableHead>
                  <TableHead>错误</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.data.items.map((it) => (
                  <TableRow key={it.id}>
                    <TableCell className="font-mono text-xs">{it.oldEmail}</TableCell>
                    <TableCell className="font-mono text-xs text-emerald-600 dark:text-emerald-400">
                      {it.newEmail}
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={it.status === "success" ? "default" : "destructive"}
                        className="text-xs"
                      >
                        {it.status}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-xs text-red-500">
                      {it.error ?? "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}
    </div>
  )
}
