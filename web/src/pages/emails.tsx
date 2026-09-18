import * as React from "react"
import { toast } from "sonner"
import {
  Copy,
  Download,
  MailPlus,
  MoreHorizontal,
  RefreshCw,
  RotateCcw,
  Trash2,
} from "lucide-react"
import { PageHeader } from "@/components/page-header"
import {
  CopyText,
  EmptyState,
  Pagination,
  SearchInput,
  TableSkeleton,
} from "@/components/data-table"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
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
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Label } from "@/components/ui/label"
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
import { Textarea } from "@/components/ui/textarea"
import {
  useBulkDeleteEmails,
  useEmails,
  useExportEmails,
  useImportEmails,
  useResetFailedEmails,
  useUpdateEmailStatus,
} from "@/lib/queries"
import type { EmailRecord, EmailStatus } from "@/lib/types"
import { copyText, downloadText, HttpError } from "@/lib/api"
import { formatTime } from "@/lib/format"

const SOURCE_LABELS: Record<string, string> = {
  manual: "手工导入",
  mailcode: "Mailcode",
  remail: "Remail",
}

function StatusBadge({ status }: { status: EmailStatus }) {
  switch (status) {
    case "available":
      return (
        <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
          可用
        </Badge>
      )
    case "reserved":
      return <Badge variant="secondary">已保留</Badge>
    case "failed":
      return <Badge variant="destructive">失败</Badge>
    case "quarantined":
      return <Badge variant="outline">隔离</Badge>
  }
}

function ImportDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const [raw, setRaw] = React.useState("")
  const importEmails = useImportEmails()

  const submit = () => {
    importEmails.mutate(raw, {
      onSuccess: (r) => {
        toast.success(
          `导入完成：共 ${r.total} 行，新增 ${r.imported}，重复 ${r.duplicateCount}，失败 ${r.errorCount}`,
        )
        setRaw("")
        onOpenChange(false)
      },
      onError: (e) => toast.error(e instanceof HttpError ? e.message : "导入失败"),
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>导入邮箱</DialogTitle>
          <DialogDescription>
            粘贴原始文本，每行一条。支持 <code>email----密码</code>、
            <code>email|url</code> 等常见分隔格式，系统自动去重。
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2">
          <Label htmlFor="raw">原始文本</Label>
          <Textarea
            id="raw"
            rows={12}
            className="font-mono text-xs"
            placeholder={"alice@example.com----pass\nbob@example.com|https://mail.example.com/bob"}
            value={raw}
            onChange={(e) => setRaw(e.target.value)}
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button onClick={submit} disabled={!raw.trim() || importEmails.isPending}>
            {importEmails.isPending ? "导入中…" : "开始导入"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export default function EmailsPage() {
  const [page, setPage] = React.useState(1)
  const [pageSize, setPageSize] = React.useState(20)
  const [q, setQ] = React.useState("")
  const [source, setSource] = React.useState("all")
  const [status, setStatus] = React.useState("available")
  const [selected, setSelected] = React.useState<Set<string>>(new Set())
  const [importOpen, setImportOpen] = React.useState(false)
  const [deleteOpen, setDeleteOpen] = React.useState(false)
  const [resetOpen, setResetOpen] = React.useState(false)
  const [exportOpen, setExportOpen] = React.useState(false)

  const list = useEmails({ page, pageSize, q: q || undefined, source, status })
  const bulkDelete = useBulkDeleteEmails()
  const resetFailed = useResetFailedEmails()
  const updateStatus = useUpdateEmailStatus()
  const exportEmails = useExportEmails()

  const items = list.data?.items ?? []
  const allChecked = items.length > 0 && items.every((e) => selected.has(e.id))

  const toggleAll = () => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (allChecked) items.forEach((e) => next.delete(e.id))
      else items.forEach((e) => next.add(e.id))
      return next
    })
  }
  const toggleOne = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  // 单行导出仍直接下载(保持原有行为)。
  const doExportSingle = (single: EmailRecord) => {
    exportEmails.mutate(
      { scope: "single", ids: [single.id] },
      {
        onSuccess: (r) => {
          downloadText(r.filename, r.content)
          toast.success(`已导出 ${r.count} 个邮箱`)
        },
        onError: (e) => toast.error(e instanceof HttpError ? e.message : "导出失败"),
      },
    )
  }

  // 导出已选:弹窗内「复制」或「下载」,统一先取导出数据再执行对应动作。
  const doExportSelected = (mode: "copy" | "download") => {
    exportEmails.mutate(
      { scope: "selected", ids: Array.from(selected) },
      {
        onSuccess: async (r) => {
          if (mode === "copy") {
            const ok = await copyText(r.content)
            if (ok) {
              toast.success(`已复制 ${r.count} 个邮箱`)
            } else {
              toast.error("复制失败,请检查浏览器剪贴板权限")
            }
          } else {
            downloadText(r.filename, r.content)
            toast.success(`已导出 ${r.count} 个邮箱`)
          }
          setExportOpen(false)
        },
        onError: (e) => toast.error(e instanceof HttpError ? e.message : "导出失败"),
      },
    )
  }

  const doDelete = () => {
    bulkDelete.mutate(Array.from(selected), {
      onSuccess: (r) => {
        toast.success(`已删除 ${r.deleted} 个邮箱`)
        setSelected(new Set())
        setDeleteOpen(false)
      },
      onError: (e) => toast.error(e instanceof HttpError ? e.message : "删除失败"),
    })
  }

  const doResetFailed = () => {
    const ids = selected.size > 0 ? Array.from(selected) : null
    resetFailed.mutate(ids, {
      onSuccess: () => {
        toast.success("失败邮箱已重置为可用")
        setSelected(new Set())
        setResetOpen(false)
      },
      onError: (e) => toast.error(e instanceof HttpError ? e.message : "重置失败"),
    })
  }

  return (
    <div>
      <PageHeader
        title="邮箱池"
        description="注册用邮箱资源：导入、同步别名、状态维护与导出"
        actions={
          <>
            {selected.size > 0 && (
              <>
                <Button variant="outline" size="sm" onClick={() => setExportOpen(true)}>
                  <Download /> 导出已选（{selected.size}）
                </Button>
                <Button variant="destructive" size="sm" onClick={() => setDeleteOpen(true)}>
                  <Trash2 /> 删除已选
                </Button>
              </>
            )}
            <Button
              variant="outline"
              size="sm"
              onClick={() => setResetOpen(true)}
            >
              <RotateCcw /> 重置失败
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                exportEmails.mutate(
                  { scope: "all" },
                  {
                    onSuccess: (r) => {
                      downloadText(r.filename, r.content)
                      toast.success(`已导出 ${r.count} 个邮箱`)
                    },
                    onError: (e) => toast.error(e instanceof HttpError ? e.message : "导出失败"),
                  },
                )
              }
            >
              <Download /> 导出全部
            </Button>
            <Button size="sm" onClick={() => setImportOpen(true)}>
              <MailPlus /> 导入邮箱
            </Button>

          </>
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <SearchInput
          value={q}
          onChange={(v) => {
            setQ(v)
            setPage(1)
          }}
          placeholder="搜索邮箱地址…"
          className="w-64"
        />
        <Select
          value={source}
          onValueChange={(v) => {
            setSource(v)
            setPage(1)
          }}
        >
          <SelectTrigger className="w-40">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部来源</SelectItem>
            <SelectItem value="standard">手工导入</SelectItem>
            <SelectItem value="mailcode">Mailcode</SelectItem>
            <SelectItem value="remail">Remail</SelectItem>
          </SelectContent>
        </Select>
        <Select
          value={status}
          onValueChange={(v) => {
            setStatus(v)
            setPage(1)
          }}
        >
          <SelectTrigger className="w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部状态</SelectItem>
            <SelectItem value="available">可用</SelectItem>
            <SelectItem value="reserved">已保留</SelectItem>
            <SelectItem value="failed">失败</SelectItem>
            <SelectItem value="quarantined">隔离</SelectItem>
          </SelectContent>
        </Select>
        <Button variant="ghost" size="sm" onClick={() => list.refetch()}>
          <RefreshCw /> 刷新
        </Button>
      </div>

      <div className="bg-card rounded-xl border">
        {list.isPending ? (
          <TableSkeleton rows={10} cols={6} />
        ) : items.length === 0 ? (
          <EmptyState
            title="当前筛选下没有邮箱"
            description="尝试切换状态/来源筛选，或导入新的邮箱。"
            action={
              <Button size="sm" onClick={() => setImportOpen(true)}>
                <MailPlus /> 导入邮箱
              </Button>
            }
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">
                  <Checkbox checked={allChecked} onCheckedChange={toggleAll} />
                </TableHead>
                <TableHead>邮箱</TableHead>
                <TableHead>来源</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>父邮箱</TableHead>
                <TableHead>导入时间</TableHead>
                <TableHead className="w-10" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((em) => (
                <TableRow key={em.id}>
                  <TableCell>
                    <Checkbox
                      checked={selected.has(em.id)}
                      onCheckedChange={() => toggleOne(em.id)}
                    />
                  </TableCell>
                  <TableCell className="max-w-[260px] truncate font-mono text-xs">
                    <CopyText text={em.email}>{em.email}</CopyText>
                  </TableCell>
                  <TableCell className="text-xs">
                    {SOURCE_LABELS[em.sourceType] ?? em.sourceType}
                    
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={em.status} />
                    {em.statusReason && (
                      <p className="text-muted-foreground mt-0.5 max-w-[180px] truncate text-xs" title={em.statusReason}>
                        {em.statusReason}
                      </p>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground max-w-[180px] truncate text-xs">
                    {em.parentEmail ?? "-"}
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">
                    {formatTime(em.importedAt)}
                  </TableCell>
                  <TableCell>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon-sm">
                          <MoreHorizontal />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => doExportSingle(em)}>
                          导出此邮箱
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          onClick={() =>
                            window.open(em.accessUrl, "_blank", "noopener,noreferrer")
                          }
                        >
                          打开取件页
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                        {em.status !== "available" && (
                          <DropdownMenuItem
                            onClick={() =>
                              updateStatus.mutate(
                                { id: em.id, status: "available" },
                                {
                                  onSuccess: () => toast.success("已标记为可用"),
                                  onError: (e) =>
                                    toast.error(e instanceof HttpError ? e.message : "操作失败"),
                                },
                              )
                            }
                          >
                            标记为可用
                          </DropdownMenuItem>
                        )}
                        {em.status !== "failed" && (
                          <DropdownMenuItem
                            onClick={() =>
                              updateStatus.mutate(
                                { id: em.id, status: "failed" },
                                {
                                  onSuccess: () => toast.success("已标记为失败"),
                                  onError: (e) =>
                                    toast.error(e instanceof HttpError ? e.message : "操作失败"),
                                },
                              )
                            }
                          >
                            标记为失败
                          </DropdownMenuItem>
                        )}
                        <DropdownMenuItem
                          onClick={() =>
                            updateStatus.mutate(
                              { id: em.id, status: "quarantined" },
                              {
                                onSuccess: () => toast.success("已移入隔离区"),
                                onError: (e) =>
                                  toast.error(e instanceof HttpError ? e.message : "操作失败"),
                              },
                            )
                          }
                        >
                          移入隔离区
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem
                          variant="destructive"
                          onClick={() => {
                            setSelected(new Set([em.id]))
                            setDeleteOpen(true)
                          }}
                        >
                          删除
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>

      <div className="mt-4">
        <Pagination
          page={page}
          pageSize={pageSize}
          total={list.data?.total ?? 0}
          onPageChange={setPage}
          onPageSizeChange={(s) => {
            setPageSize(s)
            setPage(1)
          }}
        />
      </div>

      <ImportDialog open={importOpen} onOpenChange={setImportOpen} />

      <Dialog open={exportOpen} onOpenChange={setExportOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>导出已选（{selected.size}）</DialogTitle>
            <DialogDescription>
              选择导出方式:复制到剪贴板,或下载为文件。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => doExportSelected("copy")}
              disabled={exportEmails.isPending}
            >
              <Copy /> {exportEmails.isPending ? "处理中…" : "复制"}
            </Button>
            <Button
              onClick={() => doExportSelected("download")}
              disabled={exportEmails.isPending}
            >
              <Download /> {exportEmails.isPending ? "处理中…" : "下载"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认删除 {selected.size} 个邮箱？</AlertDialogTitle>
            <AlertDialogDescription>该操作不可撤销。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={doDelete} disabled={bulkDelete.isPending} variant="destructive">
              {bulkDelete.isPending ? "删除中…" : "确认删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={resetOpen} onOpenChange={setResetOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {selected.size > 0 ? `重置已选 ${selected.size} 个失败邮箱？` : "重置全部失败邮箱？"}
            </AlertDialogTitle>
            <AlertDialogDescription>
              失败状态的邮箱将被重置为「可用」，可再次参与注册任务。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={doResetFailed} disabled={resetFailed.isPending}>
              {resetFailed.isPending ? "重置中…" : "确认重置"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
