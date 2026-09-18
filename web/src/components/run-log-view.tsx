// run-log-view —— 单 run 的实时日志终端视图。
//
// 用户心理设计：
//   - 终端式滚动（等宽字体 + 级别色），一眼扫到关键节点；
//   - 默认「跟随滚动」到底（看最新），用户上滑即自动暂停跟随（定格查看历史），
//     提供「回到底部」悬浮按钮一键复位；
//   - 级别过滤（info/success/warning/error）+ 关键词搜索（邮箱/事件/消息）；
//   - 连接状态实时指示（连接中/实时/已完成/错误重连），终态 run 显示完成徽标；
//   - 复制全部日志按钮（排障分享）。
import * as React from "react"
import {
  ArrowDown,
  CheckCircle2,
  Circle,
  Copy,
  Loader2,
  Pause,
  Play,
  Search,
  XCircle,
} from "lucide-react"
import type { RunLogEntry, RunLogLevel } from "@/lib/types"
import { formatTimeOfDay } from "@/lib/format"
import { useRunLogStream } from "@/lib/use-run-log-stream"
import { cn } from "@/lib/utils"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { toast } from "sonner"

const LEVEL_META: Record<
  RunLogLevel,
  { label: string; dot: string; text: string; badge: string }
> = {
  info: {
    label: "信息",
    dot: "bg-sky-500",
    text: "text-foreground",
    badge: "secondary",
  },
  success: {
    label: "成功",
    dot: "bg-emerald-500",
    text: "text-emerald-600 dark:text-emerald-400",
    badge: "secondary",
  },
  warning: {
    label: "警告",
    dot: "bg-amber-500",
    text: "text-amber-600 dark:text-amber-400",
    badge: "secondary",
  },
  error: {
    label: "错误",
    dot: "bg-red-500",
    text: "text-red-600 dark:text-red-400",
    badge: "destructive",
  },
}

const EVENT_LABELS: Record<string, string> = {
  run_created: "任务创建",
  run_completed: "任务完成",
  run_failed: "任务失败",
  run_cancelled: "任务取消",
  progress: "进度汇总",
  protocol_step: "步骤",
  proxy_acquired: "租到代理",
  proxy_acquire_failed: "租代理失败",
  email_reserved: "预留邮箱",
  email_reserve_failed: "预留邮箱失败",
  dial_succeeded: "拨号成功",
  dial_failed: "拨号失败",
  protocol_started: "开始注册",
  protocol_failed: "注册失败",
  account_persisted: "账号落库",
  persist_failed: "落库失败",
  account_succeeded: "注册成功",
  account_failed: "账号失败",
  account_cancelled: "账号取消",
  plancheck_alive: "套餐存活",
  plancheck_failed: "套餐失败",
  plancheck_dead: "套餐死号",
  plancheck_skipped: "套餐跳过",
}

function eventLabel(event: string): string {
  return EVENT_LABELS[event] ?? event
}

export function RunLogView({ runId }: { runId: string }) {
  const [paused, setPaused] = React.useState(false)
  const { entries, status, liveCount } = useRunLogStream(runId, paused)
  const [levelFilter, setLevelFilter] = React.useState<RunLogLevel | "all">("all")
  const [query, setQuery] = React.useState("")
  const [follow, setFollow] = React.useState(true)
  const scrollRef = React.useRef<HTMLDivElement>(null)

  // 过滤后的日志。
  const filtered = React.useMemo(() => {
    const q = query.trim().toLowerCase()
    return entries.filter((e) => {
      if (levelFilter !== "all" && e.level !== levelFilter) return false
      if (!q) return true
      return (
        e.message.toLowerCase().includes(q) ||
        e.event.toLowerCase().includes(q) ||
        (e.email ?? "").toLowerCase().includes(q) ||
        eventLabel(e.event).includes(q)
      )
    })
  }, [entries, levelFilter, query])

  // 跟随滚动：新日志到达且 follow=true 时滚到底。
  React.useEffect(() => {
    if (!follow) return
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [filtered.length, follow])

  // 用户手动滚动：离开底部即停跟随，回到底部恢复。
  const onScroll = () => {
    const el = scrollRef.current
    if (!el) return
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    setFollow(nearBottom)
  }

  const copyAll = async () => {
    const text = filtered
      .map((e) => `[${formatTimeOfDay(e.timestamp)}] [${e.level.toUpperCase()}] [${eventLabel(e.event)}]${e.email ? ` <${e.email}>` : ""} ${e.message}`)
      .join("\n")
    try {
      await navigator.clipboard.writeText(text)
      toast.success(`已复制 ${filtered.length} 条日志`)
    } catch {
      toast.error("复制失败")
    }
  }

  const statusBadge = {
    idle: <Badge variant="outline">未连接</Badge>,
    connecting: (
      <Badge variant="secondary" className="gap-1">
        <Loader2 className="size-3 animate-spin" /> 连接中
      </Badge>
    ),
    open: (
      <Badge variant="secondary" className="gap-1">
        <span className="size-1.5 animate-pulse rounded-full bg-emerald-500" /> 实时
      </Badge>
    ),
    done: (
      <Badge variant="secondary" className="gap-1 text-emerald-600 dark:text-emerald-400">
        <CheckCircle2 className="size-3" /> 已完成
      </Badge>
    ),
    error: (
      <Badge variant="destructive" className="gap-1">
        <XCircle className="size-3" /> 重连中
      </Badge>
    ),
  }[status]

  return (
    <div className="flex h-full min-h-0 flex-col gap-3">
      {/* 工具栏 */}
      <div className="flex flex-wrap items-center gap-2">
        {statusBadge}
        <div className="relative min-w-48 flex-1">
          <Search className="text-muted-foreground absolute left-2.5 top-1/2 size-4 -translate-y-1/2" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索邮箱 / 事件 / 消息…"
            className="pl-8"
          />
        </div>
        <div className="flex items-center gap-1">
          {(["all", "info", "success", "warning", "error"] as const).map((lv) => (
            <Button
              key={lv}
              size="sm"
              variant={levelFilter === lv ? "default" : "outline"}
              onClick={() => setLevelFilter(lv)}
            >
              {lv === "all" ? "全部" : LEVEL_META[lv].label}
            </Button>
          ))}
        </div>
        <Button
          size="sm"
          variant="outline"
          onClick={() => setPaused((p) => !p)}
          title={paused ? "恢复实时推送" : "暂停实时推送(定格查看)"}
        >
          {paused ? <Play className="size-4" /> : <Pause className="size-4" />}
          {paused ? "恢复" : "暂停"}
        </Button>
        <Button size="sm" variant="outline" onClick={copyAll} title="复制当前过滤结果">
          <Copy className="size-4" /> 复制
        </Button>
      </div>

      {/* 日志终端 */}
      <div className="relative min-h-0 flex-1">
        <div
          ref={scrollRef}
          onScroll={onScroll}
          className="bg-card h-full overflow-y-auto rounded-xl border p-3 font-mono text-[13px] leading-relaxed"
        >
          {filtered.length === 0 ? (
            <div className="text-muted-foreground flex h-full items-center justify-center text-sm">
              {entries.length === 0 ? "等待日志推送…" : "无匹配日志"}
            </div>
          ) : (
            filtered.map((e) => (
              <LogLine key={e.sequence} entry={e} />
            ))
          )}
        </div>
        {/* 回到底部悬浮按钮 */}
        {!follow && (
          <Button
            size="sm"
            className="absolute bottom-4 right-4 shadow-lg"
            onClick={() => {
              setFollow(true)
              const el = scrollRef.current
              if (el) el.scrollTop = el.scrollHeight
            }}
          >
            <ArrowDown className="size-4" /> 回到底部
          </Button>
        )}
      </div>

      {/* 底部状态行 */}
      <div className="text-muted-foreground flex items-center justify-between text-xs">
        <span>
          共 {entries.length} 条{levelFilter !== "all" || query ? ` · 过滤后 ${filtered.length} 条` : ""}
          {paused && " · 已暂停实时推送"}
        </span>
        <span>实时新增 {liveCount} 条</span>
      </div>
    </div>
  )
}

function LogLine({ entry: e }: { entry: RunLogEntry }) {
  const meta = LEVEL_META[e.level] ?? LEVEL_META.info
  const isProgress = e.event === "progress"
  return (
    <div
      className={cn(
        "hover:bg-muted/60 flex items-start gap-2 rounded px-1 py-0.5",
        // 进度汇总:主题色底纹高亮,一眼区分于普通步骤日志(聚合而来的心跳)。
        isProgress && "bg-primary/5 border-primary/20 my-0.5 border-l-2 py-1",
      )}
    >
      <span className="text-muted-foreground shrink-0 tabular-nums">
        {formatTimeOfDay(e.timestamp)}
      </span>
      <span className="mt-1.5 shrink-0">
        <Circle className={cn("size-2 fill-current", meta.dot.replace("bg-", "text-"))} />
      </span>
      <Badge
        variant={isProgress ? "default" : "outline"}
        className="shrink-0 font-normal"
      >
        {eventLabel(e.event)}
      </Badge>
      {e.email && (
        <span className="text-primary shrink-0">{e.email}</span>
      )}
      <span className={cn("min-w-0 flex-1 break-words", isProgress ? "text-primary font-medium" : meta.text)}>
        {e.message}
      </span>
    </div>
  )
}
