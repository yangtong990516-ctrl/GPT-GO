// 控制台 tab:三张统计卡 + 左「运行记录」+ 右「核心服务」。
import * as React from "react"
import {
  AlertCircle,
  AtSign,
  CalendarClock,
  CheckCircle2,
  CircleDashed,
  Clock,
  Inbox,
  Loader2,
  Mail,
  PauseCircle,
  PlayCircle,
  ShieldCheck,
  Trash2,
  XCircle,
} from "lucide-react"
import { toast } from "sonner"
import {
  useICloudClearEvents,
  useICloudDashboard,
  useICloudEvents,
  useICloudTasks,
} from "@/lib/icloud-queries"
import type { ICloudEvent, ICloudTask } from "@/lib/icloud-api"
import { TableSkeleton } from "@/components/data-table"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { errMsg } from "./shared"

// ── 固定核心任务 ──────────────────────────────────────────────────────────────

const CORE_TASK_IDS = ["imap-watcher", "apple-keepalive", "scheduler", "public-api"] as const

// ── 任务状态文案与徽章 ─────────────────────────────────────────────────────────

const TASK_STATUS_LABELS: Record<string, string> = {
  completed: "已就绪",
  running: "运行中",
  starting: "启动中",
  creating: "创建中",
  waiting: "等待条件",
  failed: "运行异常",
  stopped: "已停止",
  idle: "未启动",
  planned: "待启用",
}

type BadgeVariant = "default" | "secondary" | "destructive" | "outline" | "ghost" | "link"

function taskStatusVariant(status: string): BadgeVariant {
  switch (status) {
    case "running":
      return "default"
    case "failed":
      return "destructive"
    case "completed":
      return "secondary"
    case "starting":
    case "creating":
    case "waiting":
      return "outline"
    default:
      return "secondary"
  }
}

function TaskStatusIcon({ status }: { status: string }) {
  const cls = "size-4 shrink-0"
  switch (status) {
    case "running":
      return <Loader2 className={`${cls} animate-spin text-emerald-500`} />
    case "completed":
      return <CheckCircle2 className={`${cls} text-emerald-500`} />
    case "failed":
      return <XCircle className={`${cls} text-destructive`} />
    case "starting":
    case "creating":
      return <PlayCircle className={`${cls} text-amber-500`} />
    case "waiting":
      return <Clock className={`${cls} text-amber-500`} />
    case "stopped":
      return <PauseCircle className={`${cls} text-muted-foreground`} />
    case "planned":
      return <CalendarClock className={`${cls} text-muted-foreground`} />
    case "idle":
    default:
      return <CircleDashed className={`${cls} text-muted-foreground`} />
  }
}

// ── 事件级别图标 ───────────────────────────────────────────────────────────────

function EventLevelIcon({ level }: { level: string }) {
  const cls = "size-4 shrink-0"
  if (level === "error") return <XCircle className={`${cls} text-destructive`} />
  if (level === "warning") return <AlertCircle className={`${cls} text-amber-500`} />
  return <CheckCircle2 className={`${cls} text-emerald-500`} />
}

// ── 时间格式化(MM-DD HH:mm)────────────────────────────────────────────────────

function pad2(n: number) {
  return String(n).padStart(2, "0")
}

function formatEventTime(input: string): string {
  const d = new Date(input)
  if (Number.isNaN(d.getTime())) return "-"
  return `${pad2(d.getMonth() + 1)}-${pad2(d.getDate())} ${pad2(d.getHours())}:${pad2(d.getMinutes())}`
}

// ── 倒计时文案 ────────────────────────────────────────────────────────────────

function countdownText(nextRunAt: string | undefined, now: number): string {
  if (!nextRunAt) return "正在安排"
  const diff = new Date(nextRunAt).getTime() - now
  if (Number.isNaN(diff)) return "正在安排"
  if (diff <= 0) return "即将执行"
  const totalSec = Math.floor(diff / 1000)
  const m = Math.floor(totalSec / 60)
  const s = totalSec % 60
  return `${m}分${pad2(s)}秒`
}

// ── 统计卡 ───────────────────────────────────────────────────────────────────

function DashboardStatCard({
  label,
  value,
  hint,
  icon,
  iconClass,
}: {
  label: string
  value: React.ReactNode
  hint?: string
  icon: React.ReactNode
  iconClass: string
}) {
  return (
    <div className="bg-card rounded-xl border p-4">
      <div className="flex items-center justify-between">
        <p className="text-muted-foreground text-sm">{label}</p>
        <span className={`flex size-8 items-center justify-center rounded-lg ${iconClass}`}>
          {icon}
        </span>
      </div>
      <p className="mt-1 text-2xl font-semibold tabular-nums">{value}</p>
      {hint && <p className="text-muted-foreground mt-1 text-xs">{hint}</p>}
    </div>
  )
}

// ── 错误态 ───────────────────────────────────────────────────────────────────

function ErrorBox({ message }: { message: string }) {
  return (
    <div className="flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-6 text-sm text-destructive">
      <AlertCircle className="size-4 shrink-0" />
      {message}
    </div>
  )
}

// ── 主组件 ───────────────────────────────────────────────────────────────────

export function DashboardTab({
  enabled,
  onGoTasks,
}: {
  enabled: boolean
  onGoTasks?: () => void
}) {
  const dashboard = useICloudDashboard(enabled)
  const tasks = useICloudTasks(enabled)
  const events = useICloudEvents(enabled)
  const clearEvents = useICloudClearEvents()
  const [clearOpen, setClearOpen] = React.useState(false)

  // 每秒刷新当前时间,驱动核心服务的倒计时文案。
  const [currentTime, setCurrentTime] = React.useState(() => Date.now())
  React.useEffect(() => {
    const t = setInterval(() => setCurrentTime(Date.now()), 1000)
    return () => clearInterval(t)
  }, [])

  const d = dashboard.data
  const eventItems: ICloudEvent[] = events.data?.items ?? []

  // 固定 4 个核心服务,保持顺序;接口缺失时以占位行显示「未启动」。
  const allTasks = tasks.data?.items ?? []
  const coreTasks: (ICloudTask | undefined)[] = CORE_TASK_IDS.map(
    (id) => allTasks.find((t) => t.id === id || t.module === id || t.name === id),
  )

  return (
    <div className="space-y-4">
      {/* 统计卡片 */}
      {dashboard.isPending && enabled ? (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <div key={i} className="bg-card rounded-xl border p-4">
              <div className="flex items-center justify-between">
                <Skeleton className="h-4 w-20" />
                <Skeleton className="size-8 rounded-lg" />
              </div>
              <Skeleton className="mt-2 h-8 w-16" />
              <Skeleton className="mt-2 h-3 w-28" />
            </div>
          ))}
        </div>
      ) : dashboard.isError ? (
        <ErrorBox message={errMsg(dashboard.error, "控制台统计加载失败")} />
      ) : (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
          <DashboardStatCard
            label="Apple 账号"
            value={d?.apple_account_count ?? "—"}
            hint={`${d?.active_account_count ?? 0} 个状态正常`}
            icon={<ShieldCheck className="size-4" />}
            iconClass="bg-blue-500/10 text-blue-500"
          />
          <DashboardStatCard
            label="隐私邮箱"
            value={d?.mailbox_count ?? "—"}
            hint={`${d?.available_count ?? 0} 个可用`}
            icon={<AtSign className="size-4" />}
            iconClass="bg-emerald-500/10 text-emerald-500"
          />
          <DashboardStatCard
            label="本地邮件"
            value={d?.message_count ?? "—"}
            hint="本地缓存的邮件记录"
            icon={<Mail className="size-4" />}
            iconClass="bg-violet-500/10 text-violet-500"
          />
        </div>
      )}

      {/* 左右两栏:运行记录 + 核心服务 */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {/* 运行记录 */}
        <Card>
          <CardHeader className="pb-3">
            <div className="flex items-start justify-between gap-3">
              <div>
                <CardTitle className="text-sm">运行记录</CardTitle>
                <CardDescription>最近产生的系统事件</CardDescription>
              </div>
              <div className="flex items-center gap-2">
                <Badge variant="secondary" className="text-xs">
                  {eventItems.length} 条记录
                </Badge>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setClearOpen(true)}
                  disabled={eventItems.length === 0}
                >
                  <Trash2 /> 清空
                </Button>
              </div>
            </div>
          </CardHeader>
          <CardContent className="overflow-x-auto">
            {events.isPending && enabled ? (
              <TableSkeleton rows={5} cols={3} />
            ) : events.isError ? (
              <ErrorBox message={errMsg(events.error, "运行记录加载失败")} />
            ) : eventItems.length === 0 ? (
              <div className="flex flex-col items-center justify-center gap-1 py-12 text-center">
                <Inbox className="text-muted-foreground/50 size-8" />
                <p className="text-sm font-medium">暂无运行事件</p>
                <p className="text-muted-foreground text-xs">系统事件产生后会显示在这里</p>
              </div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>内容</TableHead>
                    <TableHead className="w-28">类型</TableHead>
                    <TableHead className="w-28">时间</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {eventItems.map((ev) => (
                    <TableRow key={ev.id}>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <EventLevelIcon level={ev.level} />
                          <span className="min-w-0 truncate text-sm" title={ev.message}>
                            {ev.message}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell className="text-muted-foreground text-xs">
                        {ev.category || "-"}
                      </TableCell>
                      <TableCell className="text-muted-foreground text-xs whitespace-nowrap tabular-nums">
                        {formatEventTime(ev.created_at)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>

        {/* 核心服务 */}
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">核心服务</CardTitle>
            <CardDescription>后台任务实时状态</CardDescription>
          </CardHeader>
          <CardContent>
            {tasks.isPending && enabled ? (
              <div className="space-y-3">
                {Array.from({ length: 4 }).map((_, i) => (
                  <div key={i} className="flex items-center gap-3">
                    <Skeleton className="size-4 rounded-full" />
                    <div className="min-w-0 flex-1 space-y-1.5">
                      <Skeleton className="h-4 w-28" />
                      <Skeleton className="h-3 w-44" />
                    </div>
                    <Skeleton className="h-5 w-14 rounded-full" />
                  </div>
                ))}
              </div>
            ) : tasks.isError ? (
              <ErrorBox message={errMsg(tasks.error, "核心服务状态加载失败")} />
            ) : (
              <div className="divide-y">
                {coreTasks.map((task, i) => {
                  const status = task?.status ?? "idle"
                  const running = status === "running"
                  return (
                    <div key={CORE_TASK_IDS[i]} className="flex items-center gap-3 py-3 first:pt-0 last:pb-0">
                      <TaskStatusIcon status={status} />
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-sm font-medium">
                          {task?.name ?? CORE_TASK_IDS[i]}
                        </p>
                        <p className="text-muted-foreground truncate text-xs tabular-nums">
                          {running
                            ? `下次扫描 ${countdownText(task?.next_run_at, currentTime)}` +
                              (task?.jitter_percent != null && task.jitter_percent > 0
                                ? ` · 每轮随机 ±${task.jitter_percent}%`
                                : "")
                            : `下次扫描 ${countdownText(task?.next_run_at, currentTime)}`}
                        </p>
                      </div>
                      <Badge variant={taskStatusVariant(status)} className="text-xs">
                        {TASK_STATUS_LABELS[status] ?? status}
                      </Badge>
                    </div>
                  )
                })}
              </div>
            )}
          </CardContent>
          {onGoTasks && (
            <div className="border-t px-6 py-3">
              <Button variant="link" size="sm" className="px-0" onClick={onGoTasks}>
                创建隐私邮箱 →
              </Button>
            </div>
          )}
        </Card>
      </div>

      {/* 清空运行记录确认 */}
      <AlertDialog open={clearOpen} onOpenChange={setClearOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>清空运行记录</AlertDialogTitle>
            <AlertDialogDescription>
              确定清空控制台的所有运行记录吗?清空后这些记录将不再显示。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={clearEvents.isPending}
              onClick={() =>
                clearEvents.mutate(undefined, {
                  onSuccess: () => {
                    toast.success("控制台运行记录已清空")
                    setClearOpen(false)
                  },
                  onError: (e) => toast.error(errMsg(e, "清空失败")),
                })
              }
            >
              {clearEvents.isPending ? "清空中…" : "确认清空"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
