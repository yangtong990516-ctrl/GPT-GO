// 事件日志 tab:事件表格 + 清空。
import { toast } from "sonner"
import { RefreshCw, Trash2 } from "lucide-react"
import { HttpError } from "@/lib/api"
import { useICloudClearEvents, useICloudEvents } from "@/lib/icloud-queries"
import { TableSkeleton } from "@/components/data-table"
import { formatRelative } from "@/lib/format"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import * as React from "react"
import { EmptyRow, LevelBadge } from "./shared"

export function EventsTab({ enabled }: { enabled: boolean }) {
  const events = useICloudEvents(enabled)
  const clearEvents = useICloudClearEvents()
  const [clearOpen, setClearOpen] = React.useState(false)
  const [levelFilter, setLevelFilter] = React.useState<string>("all")
  const allItems = events.data?.items ?? []
  const items = React.useMemo(
    () => (levelFilter === "all" ? allItems : allItems.filter((ev) => ev.level === levelFilter)),
    [allItems, levelFilter],
  )

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex items-start justify-between gap-3">
          <div>
            <CardTitle className="text-sm">事件日志({items.length})</CardTitle>
            <CardDescription>模块运行事件,每 5 秒自动刷新</CardDescription>
          </div>
          <div className="flex items-center gap-2">
            <Select value={levelFilter} onValueChange={setLevelFilter}>
              <SelectTrigger className="h-8 w-32">
                <SelectValue placeholder="全部级别" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部级别</SelectItem>
                <SelectItem value="info">信息</SelectItem>
                <SelectItem value="warning">警告</SelectItem>
                <SelectItem value="error">错误</SelectItem>
              </SelectContent>
            </Select>
            <Button variant="ghost" size="sm" onClick={() => events.refetch()}>
              <RefreshCw /> 刷新
            </Button>
            <Button variant="outline" size="sm" onClick={() => setClearOpen(true)} disabled={items.length === 0}>
              <Trash2 /> 清空日志
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="overflow-x-auto">
        {events.isPending && enabled ? (
          <TableSkeleton rows={8} cols={4} />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-20">级别</TableHead>
                <TableHead className="w-32">分类</TableHead>
                <TableHead>内容</TableHead>
                <TableHead className="w-28">时间</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.length === 0 && <EmptyRow cols={4} />}
              {items.map((ev) => (
                <TableRow key={ev.id}>
                  <TableCell>
                    <LevelBadge level={ev.level} />
                  </TableCell>
                  <TableCell className="text-xs">{ev.category || "-"}</TableCell>
                  <TableCell className="text-sm break-all">{ev.message}</TableCell>
                  <TableCell className="text-muted-foreground text-xs">
                    {formatRelative(ev.created_at)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>

      <AlertDialog open={clearOpen} onOpenChange={setClearOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认清空事件日志?</AlertDialogTitle>
            <AlertDialogDescription>全部事件记录将被删除,该操作不可撤销。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={clearEvents.isPending}
              onClick={() =>
                clearEvents.mutate(undefined, {
                  onSuccess: () => {
                    toast.success("日志已清空")
                    setClearOpen(false)
                  },
                  onError: (e) => toast.error(e instanceof HttpError ? e.message : "清空失败"),
                })
              }
            >
              {clearEvents.isPending ? "清空中…" : "确认清空"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  )
}
