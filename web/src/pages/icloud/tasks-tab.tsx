// TasksView(创建任务/调度器) — 1:1 重建自 iCloud-Privacy-Mail-v2
import * as React from "react"
import { toast } from "sonner"
import {
  CircleAlert,
  Copy,
  Play,
  Settings,
  Square,
  Trash2,
} from "lucide-react"
import type { ICloudAppleAccount, ICloudCreateSettings, ICloudSchedulerEvent } from "@/lib/icloud-api"
import {
  useICloudAppleAccounts,
  useICloudCreateMailbox,
  useICloudCreateSettings,
  useICloudSaveCreateSettings,
  useICloudSchedulerClearLogs,
  useICloudSchedulerStart,
  useICloudSchedulerStatus,
  useICloudSchedulerStop,
} from "@/lib/icloud-queries"
import { TableSkeleton } from "@/components/data-table"
import { formatTime, formatTimeShort } from "@/lib/format"
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
import { Input } from "@/components/ui/input"
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
import { errMsg } from "./shared"

// ── 工具函数 ──────────────────────────────────────────────────────────────────

function numOrUndef(v: string): number | undefined {
  const n = Number(v)
  return v.trim() === "" || Number.isNaN(n) ? undefined : n
}

function formatSeconds(seconds: number): string {
  if (seconds >= 3600) {
    const h = Math.floor(seconds / 3600)
    const m = Math.floor((seconds % 3600) / 60)
    return m > 0 ? `${h} 小时 ${m} 分钟` : `${h} 小时`
  }
  if (seconds >= 60) {
    const m = Math.floor(seconds / 60)
    const s = seconds % 60
    return s > 0 ? `${m} 分钟 ${s} 秒` : `${m} 分钟`
  }
  return `${seconds} 秒`
}

// ── 调度日志事件徽章 ─────────────────────────────────────────────────────────

const EVENT_TYPE_LABELS: Record<string, string> = {
  started: "启动",
  stopped: "停止",
  round_started: "新一轮",
  created: "已创建",
  failed: "失败",
  waiting: "等待",
  resumed: "恢复",
}

function EventTypeBadge({ type }: { type: string }) {
  const label = EVENT_TYPE_LABELS[type] ?? type
  const variant =
    type === "failed"
      ? "destructive"
      : type === "created"
        ? "default"
        : "secondary"
  return (
    <Badge variant={variant} className="text-xs">
      {type === "failed" && <CircleAlert className="mr-1 size-3" />}
      {label}
    </Badge>
  )
}

// ── 设置对话框 ───────────────────────────────────────────────────────────────

function SettingsDialog({
  open,
  onOpenChange,
  defaultForm,
  setDefaultForm,
  accountItems,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  defaultForm: ICloudCreateSettings
  setDefaultForm: React.Dispatch<React.SetStateAction<ICloudCreateSettings>>
  accountItems: ICloudAppleAccount[]
}) {
  const updateField = <K extends keyof ICloudCreateSettings>(
    key: K,
    value: ICloudCreateSettings[K],
  ) => {
    setDefaultForm((prev) => ({ ...prev, [key]: value }))
  }

  const toggleAccountId = (id: string) => {
    setDefaultForm((prev) => {
      const current = prev.account_ids ?? []
      const next = current.includes(id)
        ? current.filter((x) => x !== id)
        : [...current, id]
      return { ...prev, account_ids: next }
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>创建与调度设置</DialogTitle>
          <DialogDescription>默认创建参数和调度间隔，保存后自动应用</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          {/* 默认标签前缀 */}
          <div className="grid gap-2">
            <Label>默认标签前缀</Label>
            <Input
              value={defaultForm.label ?? ""}
              onChange={(e) => updateField("label", e.target.value)}
              placeholder="留空默认使用 x"
            />
            <p className="text-muted-foreground text-xs">
              留空默认使用 x，并从现有最大编号继续创建。
            </p>
          </div>

          {/* 默认备注 */}
          <div className="grid gap-2">
            <Label>默认备注</Label>
            <Input
              value={defaultForm.note ?? ""}
              onChange={(e) => updateField("note", e.target.value)}
              placeholder="可选备注"
            />
          </div>

          {/* 创建一个通道 */}
          <div className="grid gap-2">
            <Label>创建一个通道</Label>
            <Select
              value={defaultForm.create_channel ?? "auto"}
              onValueChange={(v) => updateField("create_channel", v)}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="auto">自动接口：新接口优先，失败用旧接口</SelectItem>
                <SelectItem value="apple_account">Apple Account 新接口</SelectItem>
                <SelectItem value="icloud_web">iCloud Web 旧接口</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {/* 自动创建通道 */}
          <div className="grid gap-2">
            <Label>自动创建通道</Label>
            <Select
              value={defaultForm.scheduler_create_channel ?? "auto"}
              onValueChange={(v) => updateField("scheduler_create_channel", v)}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="auto">自动接口：新接口优先，失败用旧接口</SelectItem>
                <SelectItem value="apple_account">Apple Account 新接口</SelectItem>
                <SelectItem value="icloud_web">iCloud Web 旧接口</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {/* 下一轮间隔 */}
          <div className="grid gap-2">
            <Label>下一轮间隔（分钟）</Label>
            <div className="flex items-center gap-2">
              <Input
                inputMode="numeric"
                placeholder="最小"
                value={defaultForm.scheduler_interval_min_minutes ?? ""}
                onChange={(e) =>
                  updateField("scheduler_interval_min_minutes", numOrUndef(e.target.value) ?? 60)
                }
              />
              <span className="text-muted-foreground text-sm">到</span>
              <Input
                inputMode="numeric"
                placeholder="最大"
                value={defaultForm.scheduler_interval_max_minutes ?? ""}
                onChange={(e) =>
                  updateField("scheduler_interval_max_minutes", numOrUndef(e.target.value) ?? 60)
                }
              />
            </div>
          </div>

          {/* 账号间隔 */}
          <div className="grid gap-2">
            <Label>账号间隔（秒）</Label>
            <div className="flex items-center gap-2">
              <Input
                inputMode="numeric"
                placeholder="最小"
                value={defaultForm.scheduler_account_interval_min_seconds ?? ""}
                onChange={(e) =>
                  updateField("scheduler_account_interval_min_seconds", numOrUndef(e.target.value) ?? 5)
                }
              />
              <span className="text-muted-foreground text-sm">到</span>
              <Input
                inputMode="numeric"
                placeholder="最大"
                value={defaultForm.scheduler_account_interval_max_seconds ?? ""}
                onChange={(e) =>
                  updateField("scheduler_account_interval_max_seconds", numOrUndef(e.target.value) ?? 5)
                }
              />
            </div>
          </div>

          {/* 默认参与账号 */}
          <div className="grid gap-2">
            <Label>默认参与账号</Label>
            <div className="max-h-32 space-y-1 overflow-y-auto rounded-md border p-2">
              {accountItems.length === 0 && (
                <p className="text-muted-foreground py-3 text-center text-xs">暂无 Apple 账号</p>
              )}
              {accountItems.map((a) => (
                <label key={a.id} className="flex cursor-pointer items-center gap-2 text-xs">
                  <Checkbox
                    checked={(defaultForm.account_ids ?? []).includes(a.id)}
                    onCheckedChange={() => toggleAccountId(a.id)}
                  />
                  <span className="truncate font-mono">{a.label || a.apple_id}</span>
                  <span className="text-muted-foreground truncate">({a.apple_id})</span>
                </label>
              ))}
            </div>
            <p className="text-muted-foreground text-xs">
              可以保存多个默认账号；进入页面时先选择第一个，也可以在自动创建中继续多选。
            </p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            关闭
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ── 主组件 ────────────────────────────────────────────────────────────────────

export function TasksTab({ enabled }: { enabled: boolean }) {
  // ── 数据查询 ──
  const statusQuery = useICloudSchedulerStatus(enabled)
  const accountsQuery = useICloudAppleAccounts(enabled)
  const createSettingsQuery = useICloudCreateSettings(enabled)

  // ── 变更 ──
  const schedulerStart = useICloudSchedulerStart()
  const schedulerStop = useICloudSchedulerStop()
  const schedulerClearLogs = useICloudSchedulerClearLogs()
  const createMailbox = useICloudCreateMailbox()
  const saveCreateSettings = useICloudSaveCreateSettings()

  // ── 表单状态 ──
  const [mode, setMode] = React.useState<"once" | "scheduled">("once")
  const [selectedAccountId, setSelectedAccountId] = React.useState("") // once 单选
  const [selectedAccountIds, setSelectedAccountIds] = React.useState<string[]>([]) // scheduled 多选
  const [label, setLabel] = React.useState("")
  const [note, setNote] = React.useState("")
  const [channel, setChannel] = React.useState("auto")
  const [intervalMin, setIntervalMin] = React.useState("")
  const [intervalMax, setIntervalMax] = React.useState("")
  const [acctIntervalMin, setAcctIntervalMin] = React.useState("")
  const [acctIntervalMax, setAcctIntervalMax] = React.useState("")

  // ── 对话框状态 ──
  const [settingsOpen, setSettingsOpen] = React.useState(false)
  const [clearLogsOpen, setClearLogsOpen] = React.useState(false)

  // ── 设置表单 ──
  const [defaultForm, setDefaultForm] = React.useState<ICloudCreateSettings>({})

  // ── 从 create-settings 加载默认值 ──
  const settingsLoadedRef = React.useRef(false)
  React.useEffect(() => {
    if (createSettingsQuery.data?.settings && !settingsLoadedRef.current) {
      const s = createSettingsQuery.data.settings
      settingsLoadedRef.current = true
      setDefaultForm(s)
      // 加载默认值到主表单
      setMode((s.mode as "once" | "scheduled") || "once")
      if (s.label === "x") {
        setLabel("")
      } else {
        setLabel(s.label ?? "")
      }
      setNote(s.note ?? "")
      setChannel(s.create_channel ?? "auto")
      // 默认账号
      const ids = s.account_ids ?? []
      if (ids.length > 0) {
        setSelectedAccountId(ids[0])
        setSelectedAccountIds(ids)
      }
      // 间隔
      setIntervalMin(String(s.scheduler_interval_min_minutes ?? 60))
      setIntervalMax(String(s.scheduler_interval_max_minutes ?? 60))
      setAcctIntervalMin(String(s.scheduler_account_interval_min_seconds ?? 5))
      setAcctIntervalMax(String(s.scheduler_account_interval_max_seconds ?? 5))
    }
  }, [createSettingsQuery.data])

  // ── 500ms 防抖自动保存 ──
  const autoSaveTimerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null)
  const defaultFormRef = React.useRef(defaultForm)
  defaultFormRef.current = defaultForm

  const triggerAutoSave = React.useCallback(() => {
    if (autoSaveTimerRef.current) clearTimeout(autoSaveTimerRef.current)
    autoSaveTimerRef.current = setTimeout(() => {
      const form = defaultFormRef.current
      saveCreateSettings.mutate(form, {
        onError: (e) => toast.error(errMsg(e, "自动保存创建设置失败")),
      })
    }, 500)
  }, [saveCreateSettings])

  // 表单变更时自动保存
  React.useEffect(() => {
    if (settingsLoadedRef.current) {
      triggerAutoSave()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [defaultForm])

  // 卸载时立即保存
  React.useEffect(() => {
    return () => {
      if (autoSaveTimerRef.current) {
        clearTimeout(autoSaveTimerRef.current)
      }
    }
  }, [])

  // ── 派生数据 ──
  const sched = statusQuery.data?.scheduler
  const running = sched?.running ?? false
  const accountItems = accountsQuery.data?.items ?? []
  const events: ICloudSchedulerEvent[] = React.useMemo(() => {
    const evts = sched?.events ?? []
    return [...evts].reverse() // 最新在上
  }, [sched?.events])

  // 状态徽章
  const busyKey = createMailbox.isPending
    ? "create-one"
    : schedulerStart.isPending
      ? "start"
      : schedulerStop.isPending
        ? "stop"
        : schedulerClearLogs.isPending
          ? "clear"
          : null

  const displayedStatus = running
    ? (sched?.status ?? "running")
    : busyKey === "create-one"
      ? "creating"
      : "idle"

  const statusLabels: Record<string, string> = {
    ready: "准备创建",
    running: "运行中",
    creating: "创建中",
    waiting: "等待下一轮",
    stopped: "已停止",
    idle: "未启动",
  }

  // 已选账号数量
  const selectedAccountCount =
    mode === "once" ? (selectedAccountId ? 1 : 0) : selectedAccountIds.length

  // 轮次间隔摘要
  const intervalSummary = React.useMemo(() => {
    if (!running && mode === "once") return "—"
    const minSec = sched?.interval_min_seconds
    const maxSec = sched?.interval_max_seconds
    if (minSec && maxSec && minSec !== maxSec) {
      return `${formatSeconds(minSec)} ~ ${formatSeconds(maxSec)}`
    }
    if (minSec) return formatSeconds(minSec)
    // fallback: 从分钟字段
    const minMin = sched?.current_interval_seconds
    if (minMin) return formatSeconds(minMin)
    return "—"
  }, [running, mode, sched])

  // 下次执行摘要
  const nextRunSummary = React.useMemo(() => {
    if (!running && mode === "once") return "点击后立即创建"
    if (!running) return "未安排"
    if (sched?.next_run_at) return formatTime(sched.next_run_at)
    if (sched?.status === "creating") return "本轮执行中"
    return "准备执行"
  }, [running, mode, sched])

  // ── 事件操作 ──

  // 创建一个(once)
  const doCreateOne = () => {
    if (!selectedAccountId) return
    createMailbox.mutate(
      {
        id: selectedAccountId,
        label: label.trim(),
        note: note.trim(),
        channel,
      },
      {
        onSuccess: (r) => {
          toast.success(`已创建隐私邮箱：${r.mailbox.email}`)
          // 重置 label/note 为默认值
          const s = createSettingsQuery.data?.settings
          setLabel(s?.label === "x" ? "" : (s?.label ?? ""))
          setNote(s?.note ?? "")
        },
        onError: (e) => {
          toast.error(errMsg(e, "创建失败"))
          statusQuery.refetch()
        },
      },
    )
  }

  // 启动任务(scheduled)
  const doStart = () => {
    const cfg: Record<string, unknown> = {
      account_ids: selectedAccountIds,
      label: label.trim(),
      note: note.trim(),
      create_channel: channel,
    }
    const imin = numOrUndef(intervalMin)
    const imax = numOrUndef(intervalMax)
    const amin = numOrUndef(acctIntervalMin)
    const amax = numOrUndef(acctIntervalMax)
    if (imin !== undefined) cfg.interval_min_minutes = imin
    if (imax !== undefined) cfg.interval_max_minutes = imax
    if (amin !== undefined) cfg.account_interval_min_seconds = amin
    if (amax !== undefined) cfg.account_interval_max_seconds = amax

    schedulerStart.mutate(cfg, {
      onSuccess: () => toast.success("定时创建已启动"),
      onError: (e) => toast.error(errMsg(e, "启动失败")),
    })
  }

  // 停止任务
  const doStop = () => {
    schedulerStop.mutate(undefined, {
      onSuccess: () => toast.success("定时创建已停止"),
      onError: (e) => toast.error(errMsg(e, "停止失败")),
    })
  }

  // 清除日志
  const doClearLogs = () => {
    schedulerClearLogs.mutate(undefined, {
      onSuccess: () => {
        toast.success("调度日志已清除")
        setClearLogsOpen(false)
      },
      onError: (e) => toast.error(errMsg(e, "清除失败")),
    })
  }

  // 复制邮箱
  const copyEmail = (email: string) => {
    navigator.clipboard.writeText(email).then(
      () => toast.success("邮箱已复制"),
      () => toast.error("邮箱复制失败，请手动复制"),
    )
  }

  // 查找账号名称
  const accountName = (id: string | undefined) => {
    if (!id) return "-"
    const a = accountItems.find((x) => x.id === id)
    return a ? (a.label || a.apple_id) : id
  }

  // 模式切换时同步默认值
  const handleModeChange = (newMode: "once" | "scheduled") => {
    setMode(newMode)
    // 同步默认通道
    const s = createSettingsQuery.data?.settings
    if (s) {
      if (newMode === "once") {
        setChannel(s.create_channel ?? "auto")
        // 恢复单选
        if (selectedAccountIds.length > 0 && !selectedAccountId) {
          setSelectedAccountId(selectedAccountIds[0])
        }
      } else {
        setChannel(s.scheduler_create_channel ?? "auto")
        // 恢复多选
        if (selectedAccountId && selectedAccountIds.length === 0) {
          setSelectedAccountIds([selectedAccountId])
        }
      }
    }
  }

  // ── 主按钮三态 ──
  const renderMainButton = () => {
    if (running) {
      return (
        <Button
          variant="secondary"
          size="sm"
          disabled={schedulerStop.isPending}
          onClick={doStop}
        >
          <Square /> {schedulerStop.isPending ? "停止中…" : "停止任务"}
        </Button>
      )
    }
    if (mode === "once") {
      return (
        <Button
          size="sm"
          disabled={!selectedAccountId || createMailbox.isPending}
          onClick={doCreateOne}
        >
          <Play /> {createMailbox.isPending ? "创建中…" : "创建一个"}
        </Button>
      )
    }
    return (
      <Button
        size="sm"
        disabled={selectedAccountIds.length === 0 || schedulerStart.isPending}
        onClick={doStart}
      >
        <Play /> {schedulerStart.isPending ? "启动中…" : "启动任务"}
      </Button>
    )
  }

  // ── 渲染 ──
  return (
    <div className="space-y-4">
      {/* ── 命令栏 ── */}
      <Card>
        <CardHeader className="pb-3">
          <div className="flex items-start justify-between gap-3">
            <div>
              <CardTitle className="text-sm">创建隐私邮箱</CardTitle>
              <CardDescription>创建一个邮箱或配置自动创建</CardDescription>
            </div>
            <div className="flex items-center gap-2">
              <Badge
                variant={running ? "default" : "secondary"}
                className="text-xs"
              >
                {statusLabels[displayedStatus] ?? displayedStatus}
              </Badge>
              <Button
                variant="ghost"
                size="sm"
                title="创建与调度设置"
                onClick={() => setSettingsOpen(true)}
              >
                <Settings />
              </Button>
              {renderMainButton()}
            </div>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          {/* 执行方式 */}
          <div className="grid gap-2">
            <Label>执行方式</Label>
            <div className="grid grid-cols-2 gap-2">
              <button
                type="button"
                className={`flex items-center gap-2 rounded-md border p-2.5 text-left text-xs transition-colors ${
                  mode === "once"
                    ? "border-primary bg-primary/5"
                    : "hover:bg-muted/50"
                }`}
                onClick={() => handleModeChange("once")}
              >
                <span className="inline-block size-2 rounded-full bg-blue-500" />
                <div>
                  <div className="font-medium">创建一个</div>
                  <div className="text-muted-foreground">立即为一个账号创建邮箱</div>
                </div>
              </button>
              <button
                type="button"
                className={`flex items-center gap-2 rounded-md border p-2.5 text-left text-xs transition-colors ${
                  mode === "scheduled"
                    ? "border-primary bg-primary/5"
                    : "hover:bg-muted/50"
                }`}
                onClick={() => handleModeChange("scheduled")}
              >
                <span className="inline-block size-2 rounded-full bg-emerald-500" />
                <div>
                  <div className="font-medium">自动创建</div>
                  <div className="text-muted-foreground">按设定间隔持续创建</div>
                </div>
              </button>
            </div>
          </div>

          {/* 参与 Apple 账号 */}
          <div className="grid gap-2">
            <Label>参与 Apple 账号</Label>
            {mode === "once" ? (
              <Select value={selectedAccountId} onValueChange={setSelectedAccountId}>
                <SelectTrigger>
                  <SelectValue placeholder="请选择一个账号" />
                </SelectTrigger>
                <SelectContent>
                  {accountItems.map((a) => (
                    <SelectItem key={a.id} value={a.id}>
                      {a.label || a.apple_id}（{a.apple_id}）
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <div className="max-h-32 space-y-1 overflow-y-auto rounded-md border p-2">
                {accountItems.length === 0 && (
                  <p className="text-muted-foreground py-3 text-center text-xs">
                    暂无 Apple 账号
                  </p>
                )}
                {accountItems.map((a) => (
                  <label
                    key={a.id}
                    className="flex cursor-pointer items-center gap-2 text-xs"
                  >
                    <Checkbox
                      checked={selectedAccountIds.includes(a.id)}
                      onCheckedChange={(v) =>
                        setSelectedAccountIds((prev) =>
                          v ? [...prev, a.id] : prev.filter((x) => x !== a.id),
                        )
                      }
                    />
                    <span className="truncate font-mono">{a.label || a.apple_id}</span>
                    <span className="text-muted-foreground truncate">({a.apple_id})</span>
                  </label>
                ))}
              </div>
            )}
          </div>

          {/* 创建通道 + 标签 + 备注 */}
          <div className="grid grid-cols-3 gap-3">
            <div className="grid gap-2">
              <Label>创建通道</Label>
              <Select value={channel} onValueChange={setChannel}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="auto">自动接口：新接口优先，失败用旧接口</SelectItem>
                  <SelectItem value="apple_account">Apple Account 新接口</SelectItem>
                  <SelectItem value="icloud_web">iCloud Web 旧接口</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-2">
              <Label>标签前缀</Label>
              <Input
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                placeholder="可选，默认 x"
              />
            </div>
            <div className="grid gap-2">
              <Label>备注</Label>
              <Input
                value={note}
                onChange={(e) => setNote(e.target.value)}
                placeholder="可选备注"
              />
            </div>
          </div>

          {/* 调度参数(仅 scheduled 模式显示) */}
          {mode === "scheduled" && (
            <div className="grid grid-cols-2 gap-3">
              <div className="grid gap-2">
                <Label>批次间隔下限（分钟）</Label>
                <Input
                  inputMode="numeric"
                  value={intervalMin}
                  onChange={(e) => setIntervalMin(e.target.value)}
                  placeholder="60"
                />
              </div>
              <div className="grid gap-2">
                <Label>批次间隔上限（分钟）</Label>
                <Input
                  inputMode="numeric"
                  value={intervalMax}
                  onChange={(e) => setIntervalMax(e.target.value)}
                  placeholder="60"
                />
              </div>
              <div className="grid gap-2">
                <Label>单号间隔下限（秒）</Label>
                <Input
                  inputMode="numeric"
                  value={acctIntervalMin}
                  onChange={(e) => setAcctIntervalMin(e.target.value)}
                  placeholder="5"
                />
              </div>
              <div className="grid gap-2">
                <Label>单号间隔上限（秒）</Label>
                <Input
                  inputMode="numeric"
                  value={acctIntervalMax}
                  onChange={(e) => setAcctIntervalMax(e.target.value)}
                  placeholder="5"
                />
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      {/* ── 任务概览 ── */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">任务概览</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm sm:grid-cols-4 lg:grid-cols-7">
            <div>
              <dt className="text-muted-foreground text-xs">参与账号</dt>
              <dd className="tabular-nums font-medium">{selectedAccountCount}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground text-xs">执行方式</dt>
              <dd className="font-medium">{mode === "scheduled" ? "自动创建" : "创建一个"}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground text-xs">创建成功</dt>
              <dd className="tabular-nums font-medium text-emerald-600">{sched?.success ?? 0}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground text-xs">创建失败</dt>
              <dd className="tabular-nums font-medium text-destructive">{sched?.failed ?? 0}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground text-xs">轮次间隔</dt>
              <dd className="text-xs">{intervalSummary}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground text-xs">下次执行</dt>
              <dd className="text-xs">{nextRunSummary}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground text-xs">最近执行</dt>
              <dd className="text-xs">{sched?.last_run_at ? formatTime(sched.last_run_at) : "-"}</dd>
            </div>
          </dl>
          {/* 最近错误 */}
          {sched?.last_error && (
            <div className="mt-3 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-destructive">
              最近错误 {sched.last_error}
            </div>
          )}
        </CardContent>
      </Card>

      {/* ── 调度日志 ── */}
      <Card>
        <CardHeader className="pb-3">
          <div className="flex items-start justify-between gap-3">
            <div>
              <CardTitle className="text-sm">调度日志</CardTitle>
              <CardDescription>记录启动、轮次、创建结果和等待状态</CardDescription>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-muted-foreground text-xs">{events.length} 条记录</span>
              <Button
                variant="ghost"
                size="sm"
                disabled={events.length === 0}
                onClick={() => setClearLogsOpen(true)}
              >
                <Trash2 />
              </Button>
            </div>
          </div>
        </CardHeader>
        <CardContent className="overflow-x-auto">
          {statusQuery.isPending && enabled ? (
            <TableSkeleton rows={5} cols={6} />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Apple 账号</TableHead>
                  <TableHead>事件</TableHead>
                  <TableHead>邮箱</TableHead>
                  <TableHead>标签</TableHead>
                  <TableHead>详情</TableHead>
                  <TableHead>时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {events.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={6} className="text-muted-foreground py-10 text-center text-sm">
                      <p>暂无调度记录</p>
                      <p className="mt-1 text-xs">启动创建任务后，运行过程会显示在这里。</p>
                    </TableCell>
                  </TableRow>
                )}
                {events.map((evt) => (
                  <TableRow key={evt.id}>
                    <TableCell className="text-xs">{accountName(evt.account_id)}</TableCell>
                    <TableCell>
                      <EventTypeBadge type={evt.type} />
                    </TableCell>
                    <TableCell>
                      {evt.email ? (
                        <button
                          type="button"
                          className="flex cursor-pointer items-center gap-1 font-mono text-xs hover:underline"
                          onClick={() => copyEmail(evt.email!)}
                        >
                          {evt.email}
                          <Copy className="size-3 opacity-50" />
                        </button>
                      ) : (
                        <span className="text-muted-foreground text-xs">-</span>
                      )}
                    </TableCell>
                    <TableCell className="text-xs">{evt.label || "-"}</TableCell>
                    <TableCell className="max-w-[200px] truncate text-xs" title={evt.message}>
                      {evt.message}
                      {evt.error && (
                        <span className="text-destructive ml-1">{evt.error}</span>
                      )}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs">
                      {formatTimeShort(evt.at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {/* ── 设置对话框 ── */}
      <SettingsDialog
        open={settingsOpen}
        onOpenChange={setSettingsOpen}
        defaultForm={defaultForm}
        setDefaultForm={setDefaultForm}
        accountItems={accountItems}
      />

      {/* ── 清除日志确认 ── */}
      <AlertDialog open={clearLogsOpen} onOpenChange={setClearLogsOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>清除调度日志</AlertDialogTitle>
            <AlertDialogDescription>
              确定清除当前调度任务的所有运行日志吗？
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={schedulerClearLogs.isPending}
              onClick={doClearLogs}
            >
              {schedulerClearLogs.isPending ? "清除中…" : "确认清除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
