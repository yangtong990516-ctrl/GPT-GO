// runs —— 注册运行大盘页。
//
// 用户心理设计：
//   顶部：触发注册卡片（数量/国家/邮箱来源，实时显示将生效的并发与超时——A 方案回显），
//        一键发起，发起后自动跳到该批次日志；
//   运行中：大号进度卡片（实时进度条 + 成功/失败/取消 + 成功率 + 速率），一眼掌握进展，
//        点击卡片直达实时日志；
//   历史：紧凑列表（状态徽标 + 进度 + 时长），可回看日志。
import * as React from "react"
import { RunLogView } from "@/components/run-log-view"
import {
  Activity,
  CheckCircle2,
  CircleSlash,
  Globe,
  Loader2,
  Mail,
  Play,
  Rocket,
  ScrollText,
  Timer,
  TrendingUp,
  Users,
  XCircle,
} from "lucide-react"
import { PageHeader, StatCard } from "@/components/page-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  useCreateRun,
  useEmailSourceOptions,
  useProxyCountries,
  useProxyGroups,
  useRun,
  useExecutionSettings,
  useRuns,
} from "@/lib/queries"
import type { RunState, RunStatus } from "@/lib/types"
import { countryLabel, formatRelative } from "@/lib/format"

// 邮箱来源中文标签(对齐后端 EmailSourceType 全部 5 种)。
const SOURCE_LABELS: Record<string, string> = {
  manual: "手工导入",
  mailcode: "Mailcode",
  mailcom_alias: "Mailcom 别名",
  remail: "Remail",
}

function sourceLabel(sourceType: string): string {
  return SOURCE_LABELS[sourceType] ?? sourceType
}
import { toast } from "sonner"

// ── 状态元数据 ──

const STATUS_META: Record<
  RunStatus,
  { label: string; className: string; icon: React.ReactNode }
> = {
  running: {
    label: "运行中",
    className: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400 border-emerald-500/30",
    icon: <Loader2 className="size-3 animate-spin" />,
  },
  succeeded: {
    label: "已完成",
    className: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400 border-emerald-500/30",
    icon: <CheckCircle2 className="size-3" />,
  },
  partial_success: {
    label: "部分成功",
    className: "bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/30",
    icon: <TrendingUp className="size-3" />,
  },
  failed: {
    label: "失败",
    className: "bg-red-500/15 text-red-600 dark:text-red-400 border-red-500/30",
    icon: <XCircle className="size-3" />,
  },
  cancelled: {
    label: "已取消",
    className: "bg-muted text-muted-foreground border-border",
    icon: <CircleSlash className="size-3" />,
  },
}

function StatusBadge({ status }: { status: RunStatus }) {
  const meta = STATUS_META[status] ?? STATUS_META.running
  return (
    <Badge variant="outline" className={`gap-1 ${meta.className}`}>
      {meta.icon} {meta.label}
    </Badge>
  )
}

// ── 触发注册表单 ──

function CreateRunCard({ onCreated }: { onCreated?: (runId: string) => void }) {
  const settings = useExecutionSettings()
  const createRun = useCreateRun()
  const sourceOptions = useEmailSourceOptions() // 邮箱池实际可用来源(动态)
  const proxyGroups = useProxyGroups() // 代理池分组(含 available 可用数)
  const proxyCountries = useProxyCountries() // 代理池已有国家(目标国家从这里选,注册要靠对应国代理)
  const [count, setCount] = React.useState("10")
  const [country, setCountry] = React.useState("")
  // 邮箱来源默认「自动」= 让后端按邮箱池可用数最多的来源取(或用户显式指定)。
  const [emailSource, setEmailSource] = React.useState<string>("auto")

  const st = settings.data
  const countNum = Math.max(1, parseInt(count, 10) || 1)
  const options = sourceOptions.data ?? []
  // 解析最终生效来源:auto → 可用数最多的来源;否则用户选定值。
  const resolvedSource = emailSource === "auto" ? options[0]?.sourceType : emailSource
  const totalAvailable = options.reduce((n, o) => n + o.count, 0)
  // 代理池可用总数(注册每个账号要独占一个出口代理)。
  const availableProxies = (proxyGroups.data ?? []).reduce((n, g) => n + (g.available ?? 0), 0)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      const resp = await createRun.mutateAsync({
        count: countNum,
        country: country.trim() || undefined,
        emailSource: resolvedSource || undefined,
      })
      toast.success(`已发起批量注册：目标 ${resp.total} 个`, {
        description: `批次 ${resp.runId} · 并发 ${resp.applied.concurrency}`,
      })
      // 通知父组件选中该批次(右侧日志立即联动,不跳页)。
      onCreated?.(resp.runId)
    } catch (err) {
      toast.error("发起失败", { description: err instanceof Error ? err.message : "未知错误" })
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <Rocket className="size-4" /> 发起批量注册
        </CardTitle>
        <CardDescription>
          并发 / 超时 / 出口IP上限实时读取配置栏（A 方案），改动即时生效
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-3">
            <div className="space-y-1.5">
              <Label htmlFor="count">注册数量</Label>
              <Input
                id="count"
                type="number"
                min={1}
                value={count}
                onChange={(e) => setCount(e.target.value)}
                required
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="country">目标国家</Label>
              <Select value={country === "" ? "__auto__" : country} onValueChange={(v) => setCountry(v === "__auto__" ? "" : v)}>
                <SelectTrigger id="country">
                  <SelectValue placeholder="自动（按代理实测）" />
                </SelectTrigger>
                <SelectContent>
                  {/* 自动:不指定国家,按代理出口实测归属(默认,推荐)。 */}
                  <SelectItem value="__auto__">自动（按代理实测）</SelectItem>
                  {(proxyCountries.data ?? []).map((c) => (
                    <SelectItem key={c.country} value={c.country}>
                      {countryLabel(c.country)}（代理 {c.enabled}/{c.total}）
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {/* 代理池无国家时的提示 */}
              {(proxyCountries.data ?? []).length === 0 && (
                <p className="text-muted-foreground text-xs">
                  代理池暂无代理——先到「邮箱池/代理池」导入,或留空按代理实测
                </p>
              )}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="emailSource">邮箱来源</Label>
              <Select value={emailSource} onValueChange={setEmailSource}>
                <SelectTrigger id="emailSource">
                  <SelectValue placeholder="选择邮箱来源" />
                </SelectTrigger>
                <SelectContent>
                  {/* 自动:按邮箱池可用数最多的来源取(推荐,直接从池里挑) */}
                  <SelectItem value="auto">
                    自动（可用 {totalAvailable} 个）
                  </SelectItem>
                  {options.map((o) => (
                    <SelectItem key={o.sourceType} value={o.sourceType}>
                      {sourceLabel(o.sourceType)}（可用 {o.count} 个）
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {/* 当前生效来源提示 */}
              <p className="text-muted-foreground text-xs">
                {resolvedSource
                  ? `将从「${sourceLabel(resolvedSource)}」取邮箱`
                  : "邮箱池暂无可用邮箱，请先导入"}
              </p>
            </div>
          </div>

          {/* 将生效的执行参数（A 方案回显） */}
          <div className="bg-muted/50 flex flex-wrap items-center gap-x-6 gap-y-1 rounded-lg px-3 py-2 text-xs">
            <span className="text-muted-foreground">本次将生效：</span>
            <span className="flex items-center gap-1">
              <Users className="size-3" /> 并发{" "}
              <b className="tabular-nums">{st?.concurrency ?? "…"}</b>
            </span>
            <span className="flex items-center gap-1">
              <Timer className="size-3" /> 单号超时{" "}
              <b className="tabular-nums">{st?.taskTimeoutSeconds ?? "…"}s</b>
            </span>
            <span className="flex items-center gap-1">
              <Globe className="size-3" /> 同出口IP上限{" "}
              <b className="tabular-nums">{st?.maxRegistrationsPerExitIp ?? "…"}</b>
            </span>
          </div>

          {/* 前置条件检查:邮箱 + 代理,缺什么一眼看到去哪配 */}
          <div className="space-y-1.5 rounded-lg border px-3 py-2 text-xs">
            <div className="flex items-center gap-2">
              <Mail className="size-3.5 shrink-0" />
              {totalAvailable > 0 ? (
                <span>
                  邮箱池可用 <b className="text-emerald-600 tabular-nums">{totalAvailable}</b> 个
                </span>
              ) : (
                <span className="text-amber-600">
                  邮箱池暂无可用邮箱 — 先到「邮箱池 / 邮箱开通」导入
                </span>
              )}
            </div>
            <div className="flex items-center gap-2">
              <Globe className="size-3.5 shrink-0" />
              {availableProxies > 0 ? (
                <span>
                  代理池可用 <b className="text-emerald-600 tabular-nums">{availableProxies}</b> 个
                  {availableProxies < countNum && (
                    <span className="text-amber-600">
                      （少于目标 {countNum} 个,部分账号可能等代理）
                    </span>
                  )}
                </span>
              ) : (
                <span className="text-amber-600">
                  代理池暂无可用代理 — 先到「系统设置 → 代理池」导入(注册每个账号要独占一个出口代理)
                </span>
              )}
            </div>
          </div>

          <Button type="submit" disabled={createRun.isPending} className="w-full sm:w-auto">
            {createRun.isPending ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <Play className="size-4" />
            )}
            发起注册（{countNum} 个）
          </Button>
        </form>
      </CardContent>
    </Card>
  )
}

// ── 进度条 ──

function ProgressBar({ run }: { run: RunState }) {
  const pct = run.requested > 0 ? Math.round((run.processed / run.requested) * 100) : 0
  const successPct = run.requested > 0 ? (run.succeeded / run.requested) * 100 : 0
  const failedPct = run.requested > 0 ? (run.failed / run.requested) * 100 : 0
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between text-sm">
        <span className="text-muted-foreground">
          {run.processed} / {run.requested}
        </span>
        <span className="font-medium tabular-nums">{pct}%</span>
      </div>
      <div className="bg-muted h-2.5 overflow-hidden rounded-full">
        <div className="flex h-full">
          <div className="bg-emerald-500 transition-all" style={{ width: `${successPct}%` }} />
          <div className="bg-red-500 transition-all" style={{ width: `${failedPct}%` }} />
        </div>
      </div>
      <div className="text-muted-foreground flex items-center gap-4 text-xs">
        <span className="flex items-center gap-1">
          <span className="size-2 rounded-full bg-emerald-500" /> 成功 {run.succeeded}
        </span>
        <span className="flex items-center gap-1">
          <span className="size-2 rounded-full bg-red-500" /> 失败 {run.failed}
        </span>
        {run.cancelled > 0 && (
          <span className="flex items-center gap-1">
            <span className="size-2 rounded-full bg-amber-500" /> 取消 {run.cancelled}
          </span>
        )}
        <span className="ml-auto">成功率 {Math.round(run.successRate * 100)}%</span>
      </div>
    </div>
  )
}

// ── 运行中批次卡片 ──

function RunningCard({
  run,
  selected,
  onSelect,
}: {
  run: RunState
  selected: boolean
  onSelect: () => void
}) {
  return (
    <button onClick={onSelect} className="w-full text-left">
      <Card
        className={`transition-colors ${
          selected ? "border-primary bg-primary/5" : "hover:border-primary/50"
        }`}
      >
        <CardHeader className="pb-3">
          <div className="flex items-center justify-between gap-2">
            <CardTitle className="flex items-center gap-2 font-mono text-base">
              <Activity className="size-4 text-emerald-500" />
              {run.runId}
            </CardTitle>
            <StatusBadge status={run.status} />
          </div>
          <CardDescription className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
            <span>开始于 {formatRelative(run.startedAt)}</span>
            {run.registrationCountry && <span>· {countryLabel(run.registrationCountry)}</span>}
            {run.emailSource && <span>· {run.emailSource}</span>}
            <span>· 并发 {run.workerCount}</span>
          </CardDescription>
        </CardHeader>
        <CardContent>
          <ProgressBar run={run} />
        </CardContent>
      </Card>
    </button>
  )
}

// ── 历史批次行 ──

function HistoryRow({
  run,
  selected,
  onSelect,
}: {
  run: RunState
  selected: boolean
  onSelect: () => void
}) {
  return (
    <button
      onClick={onSelect}
      className={`flex w-full items-center gap-3 rounded-lg border p-3 text-left transition-colors ${
        selected ? "border-primary bg-primary/5" : "hover:bg-muted/60"
      }`}
    >
      <StatusBadge status={run.status} />
      <span className="min-w-0 flex-1 truncate font-mono text-sm">{run.runId}</span>
      <span className="text-muted-foreground hidden text-xs sm:block">
        {run.succeeded}/{run.requested} 成功
      </span>
      <span className="text-muted-foreground shrink-0 text-xs">
        {formatRelative(run.startedAt)}
      </span>
    </button>
  )
}

// ── 选中批次的实时进度条(日志面板顶部,随注册推进实时刷新) ──

function SelectedRunProgress({ runId }: { runId: string }) {
  const runQuery = useRun(runId)
  const run = runQuery.data
  if (!run) {
    return <Skeleton className="h-20 w-full rounded-xl" />
  }
  return (
    <Card>
      <CardContent className="pt-4">
        <div className="mb-2 flex items-center justify-between gap-2">
          <StatusBadge status={run.status} />
          <span className="text-muted-foreground text-xs">
            并发 {run.workerCount}
            {run.registrationCountry && ` · ${countryLabel(run.registrationCountry)}`}
          </span>
        </div>
        <ProgressBar run={run} />
      </CardContent>
    </Card>
  )
}

// ── 主页面 ──

export default function RunsPage() {
  const runs = useRuns()
  const all = runs.data ?? []
  const running = all.filter((r) => r.status === "running")
  const history = all.filter((r) => r.status !== "running")

  // 选中批次(右侧日志联动):默认选运行中第一个,否则最新批次。
  const [selectedRunId, setSelectedRunId] = React.useState<string | null>(null)
  const effectiveSelected = React.useMemo(() => {
    if (selectedRunId && all.some((r) => r.runId === selectedRunId)) return selectedRunId
    return running[0]?.runId ?? all[0]?.runId ?? null
  }, [selectedRunId, all, running])

  // 发起成功后自动选中该批次(右侧日志立即滚动)。
  const onCreated = React.useCallback((runId: string) => setSelectedRunId(runId), [])

  // 汇总统计（全部批次）。
  const totalSucceeded = all.reduce((n, r) => n + r.succeeded, 0)
  const totalFailed = all.reduce((n, r) => n + r.failed, 0)
  const totalRequested = all.reduce((n, r) => n + r.requested, 0)

  return (
    <div className="space-y-6">
      <PageHeader
        title="注册运行"
        description="发起批量注册、实时跟踪进度、同屏查看执行日志"
      />

      <CreateRunCard onCreated={onCreated} />

      {/* 汇总统计 */}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard label="运行中批次" value={running.length} icon={<Activity className="size-4 text-emerald-500" />} />
        <StatCard label="累计注册成功" value={totalSucceeded} icon={<CheckCircle2 className="size-4 text-emerald-500" />} />
        <StatCard label="累计失败" value={totalFailed} icon={<XCircle className="size-4 text-red-500" />} />
        <StatCard
          label="累计目标"
          value={totalRequested}
          hint={totalRequested > 0 ? `总成功率 ${Math.round((totalSucceeded / totalRequested) * 100)}%` : undefined}
          icon={<Mail className="size-4 text-sky-500" />}
        />
      </div>

      {/* 主从分屏:左批次管理,右实时日志(选中即看,不跳页) */}
      <div className="grid gap-4 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
        {/* 左:批次列表 */}
        <div className="min-w-0 space-y-4">
          {running.length > 0 && (
            <section>
              <h2 className="mb-3 flex items-center gap-2 text-sm font-medium">
                <Loader2 className="size-4 animate-spin" /> 运行中（{running.length}）
              </h2>
              <div className="space-y-3">
                {running.map((r) => (
                  <RunningCard
                    key={r.runId}
                    run={r}
                    selected={r.runId === effectiveSelected}
                    onSelect={() => setSelectedRunId(r.runId)}
                  />
                ))}
              </div>
            </section>
          )}

          <section>
            <h2 className="mb-3 text-sm font-medium">历史批次（{history.length}）</h2>
            {runs.isPending ? (
              <div className="space-y-2">
                {Array.from({ length: 4 }).map((_, i) => (
                  <Skeleton key={i} className="h-14 w-full rounded-lg" />
                ))}
              </div>
            ) : history.length === 0 ? (
              <div className="text-muted-foreground flex flex-col items-center gap-2 rounded-xl border border-dashed py-12 text-sm">
                <Activity className="size-8 opacity-40" />
                <p>暂无历史批次</p>
                <p className="text-xs">发起一次批量注册后，进度会实时显示在这里</p>
              </div>
            ) : (
              <div className="space-y-2">
                {history.map((r) => (
                  <HistoryRow
                    key={r.runId}
                    run={r}
                    selected={r.runId === effectiveSelected}
                    onSelect={() => setSelectedRunId(r.runId)}
                  />
                ))}
              </div>
            )}
          </section>
        </div>

        {/* 右:实时日志终端(sticky 跟随滚动,选中批次即切换) */}
        <div className="min-w-0">
          <div className="lg:sticky lg:top-4 flex h-[560px] flex-col lg:h-[calc(100svh-16rem)]">
            <h2 className="mb-3 flex items-center gap-2 text-sm font-medium">
              <ScrollText className="size-4" /> 实时日志
              {effectiveSelected && (
                <span className="text-muted-foreground truncate font-mono text-xs font-normal">
                  {effectiveSelected}
                </span>
              )}
            </h2>
            <div className="flex min-h-0 flex-1 flex-col gap-3">
              {effectiveSelected ? (
                <>
                  {/* 选中批次的实时进度(成功/失败/成功率,随注册推进) */}
                  <SelectedRunProgress runId={effectiveSelected} />
                  <div className="min-h-0 flex-1">
                    <RunLogView key={effectiveSelected} runId={effectiveSelected} />
                  </div>
                </>
              ) : (
                <div className="text-muted-foreground flex h-full items-center justify-center rounded-xl border border-dashed text-sm">
                  选择一个批次查看实时日志
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
