// run-logs —— 注册运行日志页。
//
// 布局（用户心理：先看有哪些批次 → 选一个看详情）：
//   左侧：批次列表（状态点 + runId + 相对时间 + 条数），可搜索，点选切换；
//   右侧：选中批次的实时日志终端（RunLogView），SSE 流式滚动。
//   默认选中最新批次；运行中的批次置顶且有脉冲指示。
import * as React from "react"
import { useParams, useSearchParams } from "react-router-dom"
import { FileText, Radio, Search } from "lucide-react"
import { PageHeader } from "@/components/page-header"
import { RunLogView } from "@/components/run-log-view"
import { Badge } from "@/components/ui/badge"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Skeleton } from "@/components/ui/skeleton"
import { useRunLogs } from "@/lib/queries"
import type { RunLogSummary } from "@/lib/types"
import { formatRelative } from "@/lib/format"
import { cn } from "@/lib/utils"

function StatusDot({ summary }: { summary: RunLogSummary }) {
  if (!summary.terminal) {
    return (
      <span className="relative flex size-2">
        <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />
        <span className="relative inline-flex size-2 rounded-full bg-emerald-500" />
      </span>
    )
  }
  const failed = summary.lastEvent === "run_failed"
  const cancelled = summary.lastEvent === "run_cancelled"
  return (
    <span
      className={cn(
        "size-2 rounded-full",
        failed ? "bg-red-500" : cancelled ? "bg-amber-500" : "bg-emerald-500",
      )}
    />
  )
}

function RunListItem({
  summary,
  active,
  onClick,
}: {
  summary: RunLogSummary
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "w-full rounded-lg border p-3 text-left transition-colors",
        active ? "bg-primary/10 border-primary/40" : "hover:bg-muted/60",
      )}
    >
      <div className="flex items-center gap-2">
        <StatusDot summary={summary} />
        <span className="min-w-0 flex-1 truncate font-mono text-sm font-medium">
          {summary.runId}
        </span>
        {!summary.terminal && (
          <Badge variant="secondary" className="shrink-0 gap-1 text-emerald-600 dark:text-emerald-400">
            <Radio className="size-3" /> 运行中
          </Badge>
        )}
      </div>
      <div className="text-muted-foreground mt-1.5 flex items-center justify-between text-xs">
        <span>{formatRelative(summary.startedAt)}</span>
        <span className="tabular-nums">{summary.entryCount} 条</span>
      </div>
    </button>
  )
}

export default function RunLogsPage() {
  const logs = useRunLogs()
  const [searchParams, setSearchParams] = useSearchParams()
  const { runId: pathRunId } = useParams<{ runId: string }>()
  const [query, setQuery] = React.useState("")

  const runs = logs.data ?? []
  // 选中来源：URL path 段优先，其次 ?run= 查询参数（支持从 runs 页跳转 /logs/:runId）。
  const selected = pathRunId ?? searchParams.get("run") ?? null

  // 默认选中最新批次（首项，列表已按新→旧排序）。
  const effectiveSelected = React.useMemo(() => {
    if (selected && runs.some((r) => r.runId === selected)) return selected
    return runs[0]?.runId ?? null
  }, [selected, runs])

  const filtered = React.useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return runs
    return runs.filter((r) => r.runId.toLowerCase().includes(q))
  }, [runs, query])

  return (
    <div className="flex h-[calc(100svh-7rem)] flex-col">
      <PageHeader
        title="运行日志"
        description="批量注册的实时执行日志（SSE 流式推送）"
      />

      <div className="grid min-h-0 flex-1 gap-4 lg:grid-cols-[300px_1fr]">
        {/* 批次列表 */}
        <div className="flex min-h-0 flex-col gap-2">
          <div className="relative">
            <Search className="text-muted-foreground absolute left-2.5 top-1/2 size-4 -translate-y-1/2" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="搜索批次 ID…"
              className="pl-8"
            />
          </div>
          <ScrollArea className="min-h-0 flex-1">
            <div className="space-y-2 pr-1">
              {logs.isPending ? (
                Array.from({ length: 4 }).map((_, i) => (
                  <Skeleton key={i} className="h-16 w-full rounded-lg" />
                ))
              ) : filtered.length === 0 ? (
                <div className="text-muted-foreground flex flex-col items-center gap-2 py-12 text-sm">
                  <FileText className="size-8 opacity-40" />
                  <p>{runs.length === 0 ? "暂无运行日志" : "无匹配批次"}</p>
                  <p className="text-xs">发起一次批量注册后，这里会实时显示执行日志</p>
                </div>
              ) : (
                filtered.map((r) => (
                  <RunListItem
                    key={r.runId}
                    summary={r}
                    active={r.runId === effectiveSelected}
                    onClick={() => setSearchParams({ run: r.runId })}
                  />
                ))
              )}
            </div>
          </ScrollArea>
        </div>

        {/* 日志终端 */}
        <div className="min-h-0">
          {effectiveSelected ? (
            <RunLogView key={effectiveSelected} runId={effectiveSelected} />
          ) : (
            <div className="text-muted-foreground flex h-full items-center justify-center rounded-xl border border-dashed text-sm">
              选择一个批次查看实时日志
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
