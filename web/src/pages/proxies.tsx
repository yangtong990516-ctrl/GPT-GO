import * as React from "react"
import { toast } from "sonner"
import {
  Eraser,
  MoreHorizontal,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Trash2,
} from "lucide-react"
import { StatCard } from "@/components/page-header"
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
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
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
  useBulkDeleteProxies,
  useClearProxies,
  useDeleteProxy,
  useDeleteProxyGroup,
  useImportProxies,
  useProxies,
  useProxyCountries,
  useProxyGroups,
  useRestoreUsedProxies,
  useTestProxies,
  useUpdateProxy,
  useUpdateProxyGroup,
  useUpdateProxyStatus,
} from "@/lib/queries"
import type { ProxyGroupSummary, ProxyRecord } from "@/lib/types"
import { HttpError } from "@/lib/api"
import { countryLabel, formatRelative } from "@/lib/format"

function ProxyStatusBadge({ status }: { status: ProxyRecord["status"] }) {
  switch (status) {
    case "available":
      return (
        <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
          可用
        </Badge>
      )
    case "used":
      return <Badge variant="secondary">已占用</Badge>
    case "quarantined":
      return <Badge variant="destructive">隔离</Badge>
    default:
      return <Badge variant="outline">未知</Badge>
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
  const [country, setCountry] = React.useState("")
  const [group, setGroup] = React.useState("")
  const importProxies = useImportProxies()

  const submit = () => {
    importProxies.mutate(
      {
        rawText: raw,
        country: country ? country.toUpperCase() : undefined,
        group: group || undefined,
      },
      {
        onSuccess: (r) => {
          toast.success(
            `导入完成：共 ${r.total} 行，新增 ${r.imported}，重复 ${r.duplicateCount}，失败 ${r.errorCount}`,
          )
          setRaw("")
          onOpenChange(false)
        },
        onError: (e) => toast.error(e instanceof HttpError ? e.message : "导入失败"),
      },
    )
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>导入代理</DialogTitle>
          <DialogDescription>
            每行一条，支持 <code>scheme://user:pass@host:port</code> 或{" "}
            <code>host:port:user:pass</code> 格式。
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <div className="grid gap-2">
            <Label htmlFor="px-raw">原始文本</Label>
            <Textarea
              id="px-raw"
              rows={10}
              className="font-mono text-xs"
              placeholder={"socks5://user:pass@1.2.3.4:1080\nhttp://5.6.7.8:8080"}
              value={raw}
              onChange={(e) => setRaw(e.target.value)}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="grid gap-2">
              <Label htmlFor="px-country">国家码（可空，自动推断）</Label>
              <Input
                id="px-country"
                placeholder="US"
                maxLength={2}
                className="font-mono uppercase"
                value={country}
                onChange={(e) => setCountry(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="px-group">分组（可空，默认组）</Label>
              <Input
                id="px-group"
                placeholder="默认组"
                value={group}
                onChange={(e) => setGroup(e.target.value)}
              />
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button onClick={submit} disabled={!raw.trim() || importProxies.isPending}>
            {importProxies.isPending ? "导入中…" : "开始导入"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function GroupEditDialog({
  group,
  open,
  onOpenChange,
}: {
  group: ProxyGroupSummary | null
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const update = useUpdateProxyGroup()
  const [newGroup, setNewGroup] = React.useState("")
  const [newCountry, setNewCountry] = React.useState("")

  React.useEffect(() => {
    if (group) {
      setNewGroup(group.group)
      setNewCountry(group.country)
    }
  }, [group])

  if (!group) return null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑分组</DialogTitle>
          <DialogDescription>
            {countryLabel(group.country)} / {group.group}（{group.total} 个代理）
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <div className="grid gap-2">
            <Label>新国家码</Label>
            <Input
              value={newCountry}
              maxLength={2}
              className="font-mono uppercase"
              onChange={(e) => setNewCountry(e.target.value.toUpperCase())}
            />
          </div>
          <div className="grid gap-2">
            <Label>新分组名</Label>
            <Input value={newGroup} onChange={(e) => setNewGroup(e.target.value)} />
          </div>
        </div>
        <DialogFooter className="gap-2">
          <Button
            variant="outline"
            onClick={() =>
              update.mutate(
                { country: group.country, group: group.group, enabled: false },
                {
                  onSuccess: (r) => {
                    toast.success(`已禁用 ${r.modified} 个代理`)
                    onOpenChange(false)
                  },
                  onError: (e) => toast.error(e instanceof HttpError ? e.message : "操作失败"),
                },
              )
            }
          >
            整组禁用
          </Button>
          <Button
            variant="outline"
            onClick={() =>
              update.mutate(
                { country: group.country, group: group.group, enabled: true },
                {
                  onSuccess: (r) => {
                    toast.success(`已启用 ${r.modified} 个代理`)
                    onOpenChange(false)
                  },
                  onError: (e) => toast.error(e instanceof HttpError ? e.message : "操作失败"),
                },
              )
            }
          >
            整组启用
          </Button>
          <Button
            disabled={update.isPending}
            onClick={() =>
              update.mutate(
                {
                  country: group.country,
                  group: group.group,
                  newCountry: newCountry || undefined,
                  newGroup: newGroup || undefined,
                },
                {
                  onSuccess: (r) => {
                    toast.success(`已更新 ${r.modified} 个代理`)
                    onOpenChange(false)
                  },
                  onError: (e) => toast.error(e instanceof HttpError ? e.message : "保存失败"),
                },
              )
            }
          >
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function ProxiesSection() {
  const [page, setPage] = React.useState(1)
  const [pageSize, setPageSize] = React.useState(20)
  const [q, setQ] = React.useState("")
  const [country, setCountry] = React.useState("all")
  const [selected, setSelected] = React.useState<Set<string>>(new Set())
  const [importOpen, setImportOpen] = React.useState(false)
  const [deleteOpen, setDeleteOpen] = React.useState(false)
  const [clearOpen, setClearOpen] = React.useState(false)
  const [editGroup, setEditGroup] = React.useState<ProxyGroupSummary | null>(null)
  const [deleteGroupTarget, setDeleteGroupTarget] = React.useState<ProxyGroupSummary | null>(null)

  const list = useProxies({
    page,
    pageSize,
    q: q || undefined,
    country: country === "all" ? undefined : country,
  })
  const countries = useProxyCountries()
  const groups = useProxyGroups()
  const bulkDelete = useBulkDeleteProxies()
  const clearAll = useClearProxies()
  const restoreUsed = useRestoreUsedProxies()
  const testProxies = useTestProxies()
  const updateProxy = useUpdateProxy()
  const updateStatus = useUpdateProxyStatus()
  const deleteProxy = useDeleteProxy()
  const deleteGroup = useDeleteProxyGroup()

  const items = list.data?.items ?? []
  const allChecked = items.length > 0 && items.every((p) => selected.has(p.id))

  const toggleAll = () => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (allChecked) items.forEach((p) => next.delete(p.id))
      else items.forEach((p) => next.add(p.id))
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

  const doTest = (targetCountry?: string, targetGroup?: string) => {
    testProxies.mutate(
      { country: targetCountry, group: targetGroup },
      {
        onSuccess: (r) =>
          toast.success(
            `测试完成：共 ${r.tested} 个，可用 ${r.available}，失败 ${r.failed}${
              r.averageLatencyMs != null ? `，平均延迟 ${r.averageLatencyMs}ms` : ""
            }`,
          ),
        onError: (e) => toast.error(e instanceof HttpError ? e.message : "测试失败"),
      },
    )
  }

  const groupList = groups.data ?? []
  const summaryTotal = groupList.reduce((a, g) => a + g.total, 0)
  const summaryAvail = groupList.reduce((a, g) => a + g.available, 0)
  const summaryUsed = groupList.reduce((a, g) => a + g.used, 0)
  const summaryQuar = groupList.reduce((a, g) => a + g.quarantined, 0)

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-2">
        {selected.size > 0 && (
          <Button variant="destructive" size="sm" onClick={() => setDeleteOpen(true)}>
            <Trash2 /> 删除已选（{selected.size}）
          </Button>
        )}
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            restoreUsed.mutate(
              {},
              {
                onSuccess: (r) => toast.success(`已恢复 ${r.restored} 个占用代理`),
                onError: (e) => toast.error(e instanceof HttpError ? e.message : "恢复失败"),
              },
            )
          }
          disabled={restoreUsed.isPending}
        >
          <RotateCcw /> 恢复已占用
        </Button>
        <Button variant="outline" size="sm" onClick={() => doTest()} disabled={testProxies.isPending}>
          <Play /> {testProxies.isPending ? "测试中…" : "测试全部"}
        </Button>
        <Button variant="outline" size="sm" onClick={() => setClearOpen(true)}>
          <Eraser /> 清空
        </Button>
        <Button size="sm" onClick={() => setImportOpen(true)}>
          <Plus /> 导入代理
        </Button>
      </div>

      <div className="mb-4 grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard label="代理总数" value={summaryTotal} />
        <StatCard label="可用" value={summaryAvail} />
        <StatCard label="已占用" value={summaryUsed} />
        <StatCard label="隔离" value={summaryQuar} />
      </div>

      <Tabs defaultValue="groups">
        <TabsList className="mb-4">
          <TabsTrigger value="list">代理列表</TabsTrigger>
          <TabsTrigger value="groups">分组管理</TabsTrigger>
        </TabsList>

        <TabsContent value="list">
          <div className="mb-4 flex flex-wrap items-center gap-2">
            <SearchInput
              value={q}
              onChange={(v) => {
                setQ(v)
                setPage(1)
              }}
              placeholder="搜索 host / 用户名…"
              className="w-64"
            />
            <Select
              value={country}
              onValueChange={(v) => {
                setCountry(v)
                setPage(1)
              }}
            >
              <SelectTrigger className="w-44">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部国家</SelectItem>
                {(countries.data ?? []).map((c) => (
                  <SelectItem key={c.country} value={c.country}>
                    {countryLabel(c.country)}（{c.total}）
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button variant="ghost" size="sm" onClick={() => list.refetch()}>
              <RefreshCw /> 刷新
            </Button>
          </div>

          <div className="bg-card rounded-xl border">
            {list.isPending ? (
              <TableSkeleton rows={10} cols={7} />
            ) : items.length === 0 ? (
              <EmptyState
                title="暂无代理"
                description="导入一批代理以开始使用。"
                action={
                  <Button size="sm" onClick={() => setImportOpen(true)}>
                    <Plus /> 导入代理
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
                    <TableHead>代理</TableHead>
                    <TableHead>协议</TableHead>
                    <TableHead>国家/分组</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>延迟</TableHead>
                    <TableHead>最近测试</TableHead>
                    <TableHead>启用</TableHead>
                    <TableHead className="w-10" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((p) => (
                    <TableRow key={p.id}>
                      <TableCell>
                        <Checkbox checked={selected.has(p.id)} onCheckedChange={() => toggleOne(p.id)} />
                      </TableCell>
                      <TableCell className="max-w-[280px] truncate font-mono text-xs">
                        <CopyText text={`${p.scheme}://${p.host}:${p.port}`}>
                          {p.host}:{p.port}
                        </CopyText>
                      </TableCell>
                      <TableCell>
                        <Badge variant="outline" className="font-mono">
                          {p.scheme}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-xs">
                        <div>{countryLabel(p.country)}</div>
                        <div className="text-muted-foreground">{p.group}</div>
                      </TableCell>
                      <TableCell>
                        <ProxyStatusBadge status={p.status} />
                      </TableCell>
                      <TableCell className="text-xs tabular-nums">
                        {p.latencyMs != null ? `${p.latencyMs}ms` : "-"}
                      </TableCell>
                      <TableCell className="text-muted-foreground text-xs">
                        {formatRelative(p.lastCheckedAt)}
                      </TableCell>
                      <TableCell>
                        <Switch
                          checked={p.enabled}
                          onCheckedChange={(v) =>
                            updateProxy.mutate(
                              { id: p.id, enabled: v },
                              {
                                onError: (e) =>
                                  toast.error(e instanceof HttpError ? e.message : "更新失败"),
                              },
                            )
                          }
                        />
                      </TableCell>
                      <TableCell>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                              <Button variant="ghost" size="icon-sm">
                                <MoreHorizontal />
                              </Button>
                            </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem
                              onClick={() =>
                                navigator.clipboard.writeText(
                                  `${p.scheme}://${p.username ? `${p.username}:${p.password}@` : ""}${p.host}:${p.port}`,
                                )
                              }
                            >
                              复制代理 URL
                            </DropdownMenuItem>
                            <DropdownMenuSeparator />
                            {p.status !== "available" && (
                              <DropdownMenuItem
                                onClick={() =>
                                  updateStatus.mutate(
                                    { id: p.id, status: "available" },
                                    {
                                      onSuccess: () => toast.success("已标记可用"),
                                      onError: (e) =>
                                        toast.error(e instanceof HttpError ? e.message : "操作失败"),
                                    },
                                  )
                                }
                              >
                                标记为可用
                              </DropdownMenuItem>
                            )}
                            {p.status !== "quarantined" && (
                              <DropdownMenuItem
                                onClick={() =>
                                  updateStatus.mutate(
                                    { id: p.id, status: "quarantined" },
                                    {
                                      onSuccess: () => toast.success("已移入隔离"),
                                      onError: (e) =>
                                        toast.error(e instanceof HttpError ? e.message : "操作失败"),
                                    },
                                  )
                                }
                              >
                                移入隔离区
                              </DropdownMenuItem>
                            )}
                            <DropdownMenuSeparator />
                            <DropdownMenuItem
                              variant="destructive"
                              onClick={() =>
                                deleteProxy.mutate(p.id, {
                                  onSuccess: () => toast.success("已删除"),
                                  onError: (e) =>
                                    toast.error(e instanceof HttpError ? e.message : "删除失败"),
                                })
                              }
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
        </TabsContent>

        <TabsContent value="groups">
          <div className="bg-card rounded-xl border">
            {groups.isPending ? (
              <TableSkeleton rows={6} cols={6} />
            ) : groupList.length === 0 ? (
              <EmptyState title="暂无分组" description="导入代理后将按 国家/分组 自动聚合。" />
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>国家</TableHead>
                    <TableHead>分组</TableHead>
                    <TableHead>总数</TableHead>
                    <TableHead>可用/占用/隔离</TableHead>
                    <TableHead>协议</TableHead>
                    <TableHead className="w-10" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {groupList.map((g) => (
                    <TableRow key={`${g.country}/${g.group}`}>
                      <TableCell className="text-xs">{countryLabel(g.country)}</TableCell>
                      <TableCell className="text-sm">{g.group}</TableCell>
                      <TableCell className="tabular-nums">
                        {g.total}
                        <span className="text-muted-foreground text-xs">（启用 {g.enabled}）</span>
                      </TableCell>
                      <TableCell className="text-xs tabular-nums">
                        <span className="text-emerald-600 dark:text-emerald-400">{g.available}</span>
                        {" / "}
                        <span>{g.used}</span>
                        {" / "}
                        <span className="text-destructive">{g.quarantined}</span>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap gap-1">
                          {g.schemes.map((s) => (
                            <Badge key={s} variant="outline" className="font-mono">
                              {s}
                            </Badge>
                          ))}
                        </div>
                      </TableCell>
                      <TableCell>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                              <Button variant="ghost" size="icon-sm">
                                <MoreHorizontal />
                              </Button>
                            </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem onClick={() => setEditGroup(g)}>
                              编辑分组
                            </DropdownMenuItem>
                            <DropdownMenuItem onClick={() => doTest(g.country, g.group)}>
                              测试本组
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              onClick={() =>
                                restoreUsed.mutate(
                                  { country: g.country, group: g.group },
                                  {
                                    onSuccess: (r) => toast.success(`已恢复 ${r.restored} 个`),
                                    onError: (e) =>
                                      toast.error(e instanceof HttpError ? e.message : "恢复失败"),
                                  },
                                )
                              }
                            >
                              恢复本组占用
                            </DropdownMenuItem>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem
                              variant="destructive"
                              onClick={() => setDeleteGroupTarget(g)}
                            >
                              删除分组
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
        </TabsContent>
      </Tabs>

      <ImportDialog open={importOpen} onOpenChange={setImportOpen} />
      <GroupEditDialog group={editGroup} open={!!editGroup} onOpenChange={(o) => !o && setEditGroup(null)} />

      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认删除 {selected.size} 个代理？</AlertDialogTitle>
            <AlertDialogDescription>该操作不可撤销。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                bulkDelete.mutate(Array.from(selected), {
                  onSuccess: (r) => {
                    toast.success(`已删除 ${r.deleted} 个代理`)
                    setSelected(new Set())
                    setDeleteOpen(false)
                  },
                  onError: (e) => toast.error(e instanceof HttpError ? e.message : "删除失败"),
                })
              }
              disabled={bulkDelete.isPending}
              variant="destructive"
            >
              {bulkDelete.isPending ? "删除中…" : "确认删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={clearOpen} onOpenChange={setClearOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认清空全部代理？</AlertDialogTitle>
            <AlertDialogDescription>
              代理池中的所有记录将被删除，该操作不可撤销。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                clearAll.mutate(undefined, {
                  onSuccess: (r) => {
                    toast.success(`已清空 ${r.deleted} 个代理`)
                    setClearOpen(false)
                  },
                  onError: (e) => toast.error(e instanceof HttpError ? e.message : "清空失败"),
                })
              }
              disabled={clearAll.isPending}
              variant="destructive"
            >
              {clearAll.isPending ? "清空中…" : "确认清空"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={!!deleteGroupTarget}
        onOpenChange={(o) => !o && setDeleteGroupTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              删除分组 {deleteGroupTarget?.country}/{deleteGroupTarget?.group}？
            </AlertDialogTitle>
            <AlertDialogDescription>
              组内 {deleteGroupTarget?.total ?? 0} 个代理将全部删除。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (!deleteGroupTarget) return
                deleteGroup.mutate(
                  { country: deleteGroupTarget.country, group: deleteGroupTarget.group },
                  {
                    onSuccess: () => {
                      toast.success("分组已删除")
                      setDeleteGroupTarget(null)
                    },
                    onError: (e) =>
                      toast.error(e instanceof HttpError ? e.message : "删除失败"),
                  },
                )
              }}
              disabled={deleteGroup.isPending}
              variant="destructive"
            >
              {deleteGroup.isPending ? "删除中…" : "确认删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
