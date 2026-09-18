// 邮箱池 tab —— 1:1 重建 iCloud-Privacy-Mail-v2 MailboxesView(1574 行)全部功能:
// 命令栏 6 按钮(同步已有邮箱/同步已有邮箱邮件/导入本地邮箱/全部彻底清理 Apple 邮件/
// 批量删除指定邮箱/删除选中)、9 列表格 + 全选(indeterminate)+ 动态 pageSize、
// 行内 4 操作(同步/取码/详情/删除)、取码弹窗三态、详情抽屉(状态与接收表单/本地邮件/
// 单封邮件弹窗 sandbox iframe)、remote-clean 远程清理、前端并发彻底删除队列(按账号限流)、
// 批量解析入队、全部清理 + 批量同步进度轮询 toast、快速编辑、30s 轮询。
import * as React from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  Boxes,
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  Clipboard,
  CloudDownload,
  CloudOff,
  KeyRound,
  LoaderCircle,
  MailOpen,
  MailPlus,
  RefreshCw,
  Save,
  Search,
  ShieldX,
  Trash2,
  X,
} from "lucide-react"
import { HttpError } from "@/lib/api"
import { icloudApi } from "@/lib/icloud-api"
import type {
  ICloudAppleMailCleanupStatus,
  ICloudCodeResult,
  ICloudMailbox,
  ICloudMessage,
} from "@/lib/icloud-api"
import {
  useICloudAppleAccounts,
  useICloudAppleMailCleanupStatus,
  useICloudDashboard,
  useICloudImportMailbox,
  useICloudMailboxCode,
  useICloudMailboxMessage,
  useICloudMailboxMessages,
  useICloudMailboxes,
  useICloudResolveMailboxEmails,
  useICloudSyncAllMessagesStatus,
  useICloudSyncMailboxMessages,
  useICloudSyncMailboxes,
  useICloudUpdateMailboxStatus,
} from "@/lib/icloud-queries"
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
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
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
import { buildEmailHTMLDocument, errMsg } from "./shared"

// ── 常量与纯函数(对齐原视图) ──────────────────────────────────────────────────

const MAILBOX_MESSAGE_SYNC_HINT =
  "同步该邮箱所属 Apple 主号的所有新邮件;IMAP 主路径,iCloud Web 补查并自动合并"
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

const DELETE_STATUS_LABELS: Record<string, string> = {
  available: "可用",
  reserved: "已预留(由租约管理)",
  used: "已使用",
  active: "活跃",
  failed: "失败",
  disabled: "已停用",
}

function statusLabel(value: string): string {
  return (
    {
      available: "可用",
      reserved: "已预留",
      used: "已使用",
      failed: "失败",
      disabled: "已停用",
      active: "活跃",
    }[value] ?? value ?? "未知"
  )
}

function formatTime(value?: string): string {
  if (!value) return "-"
  const date = new Date(value)
  return Number.isNaN(date.getTime()) || date.getFullYear() < 2000 ? "-" : date.toLocaleString("zh-CN")
}

function formatMessageTime(value?: string): string {
  if (!value) return "-"
  const date = new Date(value)
  if (Number.isNaN(date.getTime()) || date.getFullYear() < 2000) return "-"
  const today = new Date()
  const sameDay =
    date.getFullYear() === today.getFullYear() &&
    date.getMonth() === today.getMonth() &&
    date.getDate() === today.getDate()
  return sameDay
    ? date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })
    : date.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })
}

async function copyText(value: string) {
  await navigator.clipboard.writeText(value)
}

function looksLikeHTML(value?: string): boolean {
  return /<(?:!doctype|html|head|body|style|table|div|p|a|img|span)\b/i.test(String(value ?? ""))
}

function messageContentTypeLabel(message: ICloudMessage | null): string {
  const contentType = String(message?.content_type ?? "").toLowerCase()
  if (
    String(message?.html_body ?? "").trim() ||
    contentType.includes("text/html") ||
    looksLikeHTML(message?.body)
  )
    return "HTML 邮件"
  if (contentType.includes("text/plain")) return "纯文本邮件"
  return "邮件正文"
}
function parseBulkDeleteEmails(value: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const line of String(value ?? "").split(/\r?\n/)) {
    const email = line.trim().toLowerCase()
    if (!email || seen.has(email)) continue
    seen.add(email)
    out.push(email)
  }
  return out
}

function emailListSummary(items: string[]): string {
  if (items.length <= 5) return items.join("、")
  return `${items.slice(0, 5).join("、")} 等 ${items.length} 个邮箱`
}

interface RemoteCleanupStats {
  moved_to_trash?: number
  destroyed?: number
  skipped?: number
  local_removed?: number
}

function cleanupText(cleanup?: RemoteCleanupStats): string {
  const moved = cleanup?.moved_to_trash ?? 0
  const destroyed = cleanup?.destroyed ?? 0
  const skipped = cleanup?.skipped ?? 0
  const localRemoved = cleanup?.local_removed ?? 0
  return `移动 ${moved} 封,彻底清除 ${destroyed} 封${localRemoved ? `,本地清理 ${localRemoved} 封` : ""}${skipped ? `,未匹配 ${skipped} 封` : ""}`
}

function appleMailFolderText(value?: string): string {
  const name = String(value ?? "").trim()
  if (!name) return ""
  const normalized = name.toLowerCase()
  const marker = "$category$_"
  const idx = normalized.indexOf(marker)
  if (idx >= 0) {
    let category = normalized.slice(idx + marker.length)
    const highlighted = category.endsWith("_hi")
    if (highlighted) category = category.slice(0, -3)
    let label =
      {
        primary: "主要",
        decluttered: "智能整理",
        personal: "个人",
        transactions: "交易",
        updates: "更新",
        news: "新闻",
        social: "社交",
        others: "其他",
        promotions: "推广",
        error: "分类异常",
        unsupportedlanguage: "不支持的语言",
      }[category] ?? "智能分类"
    if (highlighted) label += "·重点"
    return `收件箱(${label})`
  }
  return (
    {
      inbox: "收件箱",
      sent: "已发送",
      "sent mail": "已发送",
      "sent messages": "已发送",
      drafts: "草稿箱",
      archive: "归档",
      junk: "垃圾邮件",
      "junk mail": "垃圾邮件",
      "bulk mail": "垃圾邮件",
      spam: "垃圾邮件",
      trash: "废纸篓",
      deleted: "废纸篓",
      "deleted messages": "废纸篓",
      "all mail": "所有邮件",
    }[normalized] ?? name
  )
}

/** 后端 AppleMailCleanupJob 的宽松形态(比 icloud-api.ts 的 ICloudAppleMailCleanupStatus 更全)。 */
interface AppleMailCleanupJobLike extends ICloudAppleMailCleanupStatus {
  status?: string
  total_accounts?: number
  total_mailboxes?: number
  completed_mailboxes?: number
  successful_mailboxes?: number
  failed_mailboxes?: number
  queued?: number
  active?: number
  completed?: number
  success?: number
  current_folder?: string
  discovered?: number
  moved_to_trash?: number
}

function appleMailCleanupText(job: AppleMailCleanupJobLike | undefined): string {
  const total = Number(job?.total_accounts ?? 0)
  const totalMailboxes = Number(job?.total_mailboxes ?? 0)
  const active = Number(job?.active ?? 0)
  const queued = Number(job?.queued ?? 0)
  const completedAccounts = Number(job?.completed ?? 0)
  const successfulAccounts = Number(job?.success ?? 0)
  const failedAccounts = Number(job?.failed ?? 0)
  const completedMailboxes = Number(job?.completed_mailboxes ?? 0)
  const successfulMailboxes = Number(job?.successful_mailboxes ?? 0)
  const failedMailboxes = Number(job?.failed_mailboxes ?? 0)
  const folder = job?.current_folder ? `｜当前文件夹 ${appleMailFolderText(job.current_folder)}` : ""
  const progress = totalMailboxes
    ? `邮箱已完成 ${completedMailboxes}/${totalMailboxes}(成功 ${successfulMailboxes},失败 ${failedMailboxes})`
    : `账号已完成 ${completedAccounts}/${total}(成功 ${successfulAccounts},失败 ${failedAccounts})`
  const counts = `移入废纸篓 ${job?.moved_to_trash ?? 0}｜彻底删除 ${job?.destroyed ?? 0}｜本地清理 ${job?.local_removed ?? 0}`
  return `全部邮件清理:Apple 账号 ${total}｜邮箱 ${totalMailboxes}｜执行账号 ${active}｜排队账号 ${queued}｜${progress}${folder};${counts}`
}

interface MessageSyncJobLike {
  running: boolean
  status?: string
  total_accounts?: number
  total_mailboxes?: number
  queued?: number
  active?: number
  completed_accounts?: number
  successful_accounts?: number
  failed_accounts?: number
  skipped_mailboxes?: number
  has_more?: boolean
  imap_accounts?: number
  web_api_accounts?: number
  fallbacks?: number
  scanned?: number
  matched?: number
  synced_messages?: number
  last_error?: string
}

function mailboxMessageSyncJobText(job: MessageSyncJobLike | undefined): string {
  const total = Number(job?.total_accounts ?? 0)
  const completed = Number(job?.completed_accounts ?? 0)
  const successful = Number(job?.successful_accounts ?? 0)
  const failed = Number(job?.failed_accounts ?? 0)
  return `邮件同步:完成 ${completed}/${total}(成功 ${successful},失败 ${failed})｜执行 ${job?.active ?? 0}｜排队 ${job?.queued ?? 0}｜IMAP ${job?.imap_accounts ?? 0}｜Web API ${job?.web_api_accounts ?? 0}｜回退 ${job?.fallbacks ?? 0}｜扫描 ${job?.scanned ?? 0}｜匹配 ${job?.matched ?? 0}｜新增 ${job?.synced_messages ?? 0}`
}

function mailboxMessageSyncCompletedText(job: MessageSyncJobLike | undefined): string {
  const total = Number(job?.total_accounts ?? 0)
  const successful = Number(job?.successful_accounts ?? 0)
  const failed = Number(job?.failed_accounts ?? 0)
  const skipped = Number(job?.skipped_mailboxes ?? 0) ? `｜跳过邮箱 ${job?.skipped_mailboxes}` : ""
  const hasMore = job?.has_more ? "｜仍有后续邮件" : ""
  return `邮件同步完成:账号成功 ${successful}/${total}｜IMAP ${job?.imap_accounts ?? 0}｜Web API ${job?.web_api_accounts ?? 0}｜回退 ${job?.fallbacks ?? 0}｜扫描 ${job?.scanned ?? 0}｜匹配 ${job?.matched ?? 0}｜新增 ${job?.synced_messages ?? 0}｜失败 ${failed}${skipped}${hasMore}`
}

// ── 彻底删除队列类型(前端并发,按账号限流) ──────────────────────────────────────

interface DeleteTask {
  id: string
  email: string
  accountID: string
  localOnly: boolean
}

interface DeleteQueueState {
  queue: DeleteTask[]
  deleting: string[]
  confirmID: string
  succeeded: number
  failed: number
  lastError: string
}

const DELETE_QUEUE_IDLE: DeleteQueueState = {
  queue: [],
  deleting: [],
  confirmID: "",
  succeeded: 0,
  failed: 0,
  lastError: "",
}

// ── 子组件:API/iCloud 通道徽章 ────────────────────────────────────────────────

function ChannelBadges({ apiActive, icloudActive }: { apiActive: boolean; icloudActive: boolean }) {
  return (
    <div className="flex gap-1">
      <span
        className={`rounded px-1.5 py-0.5 text-[10px] font-semibold ${
          apiActive
            ? "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400"
            : "bg-muted text-muted-foreground"
        }`}
      >
        API
      </span>
      <span
        className={`rounded px-1.5 py-0.5 text-[10px] font-semibold ${
          icloudActive
            ? "bg-sky-500/15 text-sky-600 dark:text-sky-400"
            : "bg-muted text-muted-foreground"
        }`}
      >
        iCloud
      </span>
    </div>
  )
}

// ── 主组件 ────────────────────────────────────────────────────────────────────

export function MailboxesTab({ enabled }: { enabled: boolean }) {
  const qc = useQueryClient()

  // ── 列表查询状态 ──
  const [page, setPage] = React.useState(1)
  const [pageSize, setPageSize] = React.useState(7)
  const [query, setQuery] = React.useState("")
  const [accountID, setAccountID] = React.useState("")
  const [statusFilter, setStatusFilter] = React.useState("")

  const accountsQ = useICloudAppleAccounts(enabled)
  const mailboxesQ = useICloudMailboxes(
    {
      page,
      pageSize,
      q: query.trim() || undefined,
      account_id: accountID || undefined,
      status: statusFilter || undefined,
    },
    enabled,
  )
  const result = mailboxesQ.data
  const items = React.useMemo(() => result?.items ?? [], [result])
  const accounts = React.useMemo(() => accountsQ.data?.items ?? [], [accountsQ.data])
  const accountByID = React.useMemo(() => new Map(accounts.map((a) => [a.id, a])), [accounts])

  // ── 详情抽屉/弹窗状态 ──
  const [selected, setSelected] = React.useState<ICloudMailbox | null>(null)
  const [selectedMessageID, setSelectedMessageID] = React.useState<string | null>(null)
  const [messageViewMode, setMessageViewMode] = React.useState<"html" | "text">("text")
  const [edit, setEdit] = React.useState({ status: "available", api_active: true, icloud_active: true, note: "" })
  const editOriginal = React.useRef(edit)

  const [codeOpen, setCodeOpen] = React.useState(false)
  const [codeMailbox, setCodeMailbox] = React.useState<ICloudMailbox | null>(null)
  const [codeResult, setCodeResult] = React.useState<ICloudCodeResult | null>(null)
  const [codeError, setCodeError] = React.useState("")
  const [codeBusy, setCodeBusy] = React.useState(false)
  const [codeBusyVisible, setCodeBusyVisible] = React.useState(false)
  const codeBusyTimer = React.useRef<number | undefined>(undefined)

  const [quickEditOpen, setQuickEditOpen] = React.useState(false)
  const [quickEditField, setQuickEditField] = React.useState<"note" | "status">("note")
  const [quickEditMailbox, setQuickEditMailbox] = React.useState<ICloudMailbox | null>(null)
  const [quickEdit, setQuickEdit] = React.useState({ status: "available", note: "" })

  const [showImport, setShowImport] = React.useState(false)
  const [mailboxImport, setMailboxImport] = React.useState({ account_id: "", email: "", label: "", note: "" })

  const [showSync, setShowSync] = React.useState(false)
  const [syncAccountID, setSyncAccountID] = React.useState("")

  const [showBulkDelete, setShowBulkDelete] = React.useState(false)
  const [bulkDeleteEmails, setBulkDeleteEmails] = React.useState("")
  const [bulkDeleteError, setBulkDeleteError] = React.useState("")

  const [selectedMailboxes, setSelectedMailboxes] = React.useState<{ id: string; email: string }[]>([])
  const [rowBusy, setRowBusy] = React.useState<Record<string, string>>({})
  const [busyActions, setBusyActions] = React.useState<string[]>([])

  // 远程清理开关(详情抽屉内)
  const [remoteClean, setRemoteClean] = React.useState({ move_synced: true, empty_trash: true })

  // 确认对话框(alert-dialog)状态;onClose 在取消/确认后统一清理 busy 等附带状态
  const [confirmState, setConfirmState] = React.useState<null | {
    title: string
    message: string
    confirmText: string
    action: () => void
    onClose?: () => void
  }>(null)

  // ── 彻底删除队列 ──
  const [dq, setDq] = React.useState<DeleteQueueState>(DELETE_QUEUE_IDLE)
  const dqRef = React.useRef(dq)
  dqRef.current = dq
  const deletingIDsRef = React.useRef<Set<string>>(new Set())
  const deleteNoticeID = React.useRef<string | number | undefined>(undefined)
  const deleteToastVisible = React.useRef(false)

  // ── 批量同步/全部清理进度 ──
  const cleanupStatusQ = useICloudAppleMailCleanupStatus(enabled)
  const cleanupJob = cleanupStatusQ.data?.job as AppleMailCleanupJobLike | undefined
  const cleanupRunning = Boolean(cleanupJob?.running)
  const cleanupRef = React.useRef(cleanupJob)
  cleanupRef.current = cleanupJob
  const cleanAllNoticeID = React.useRef<string | number | undefined>(undefined)
  const cleanupWasRunning = React.useRef(false)

  const syncAllStatusQ = useICloudSyncAllMessagesStatus(enabled)
  const syncAllJob = syncAllStatusQ.data?.job as MessageSyncJobLike | undefined
  const syncAllRunning = Boolean(syncAllJob?.running)
  const syncAllRef = React.useRef(syncAllJob)
  syncAllRef.current = syncAllJob
  const syncAllNoticeID = React.useRef<string | number | undefined>(undefined)
  const syncAllWasRunning = React.useRef(false)
  const syncAllIntroVisible = React.useRef(false)
  const syncAllIntroTimer = React.useRef<number | undefined>(undefined)

  // ── 行内同步批量 toast 聚合(mailboxSyncBatch) ──
  const syncBatchRef = React.useRef<{
    total: number
    running: number
    success: number
    failed: number
    noticeID: string | number | undefined
  } | null>(null)

  // ── 动态 pageSize ──
  const commandBarRef = React.useRef<HTMLDivElement | null>(null)
  const tableViewportRef = React.useRef<HTMLDivElement | null>(null)
  const paginationRef = React.useRef<HTMLDivElement | null>(null)
  const tableResizeTimer = React.useRef<number | undefined>(undefined)

  // ── 加载遮罩(600ms 后才显示) ──
  const [loadingVisible, setLoadingVisible] = React.useState(false)
  const loadingTimer = React.useRef<number | undefined>(undefined)

  // ── mutations ──
  const importMailboxMut = useICloudImportMailbox()
  const updateStatusMut = useICloudUpdateMailboxStatus()
  const syncMailboxMessagesMut = useICloudSyncMailboxMessages()
  const syncMailboxesMut = useICloudSyncMailboxes()
  const resolveEmailsMut = useICloudResolveMailboxEmails()
  const mailboxCodeMut = useICloudMailboxCode()
  const dashboardQ = useICloudDashboard(enabled)

  const messagesQ = useICloudMailboxMessages(selected?.id ?? null, enabled && !!selected)
  const messages = React.useMemo(() => messagesQ.data?.items ?? [], [messagesQ.data])
  const messageDetailQ = useICloudMailboxMessage(selected?.id ?? null, selectedMessageID, enabled && !!selectedMessageID)
  const selectedMessage = messageDetailQ.data?.message ?? null

  // ── busy 管理 ──
  const isBusy = React.useCallback((action: string) => busyActions.includes(action), [busyActions])
  const startBusy = React.useCallback((action: string): boolean => {
    let added = false
    setBusyActions((prev) => {
      if (prev.includes(action)) return prev
      added = true
      return [...prev, action]
    })
    return added
  }, [])
  const finishBusy = React.useCallback((action: string) => {
    setBusyActions((prev) => prev.filter((a) => a !== action))
  }, [])

  const rowBusyAction = React.useCallback((id: string) => rowBusy[id] ?? "", [rowBusy])
  const startRowBusy = React.useCallback((id: string, action: string): boolean => {
    let added = false
    setRowBusy((prev) => {
      if (prev[id]) return prev
      added = true
      return { ...prev, [id]: action }
    })
    return added
  }, [])
  const finishRowBusy = React.useCallback((id: string) => {
    setRowBusy((prev) => {
      if (!prev[id]) return prev
      const next = { ...prev }
      delete next[id]
      return next
    })
  }, [])

  const refreshList = React.useCallback(() => {
    void qc.invalidateQueries({ queryKey: ["icloud", "mailboxes"] })
  }, [qc])

  // ── 取码 busy(600ms 后才显示转圈) ──
  const startCodeBusy = React.useCallback(() => {
    setCodeBusy(true)
    setCodeBusyVisible(false)
    window.clearTimeout(codeBusyTimer.current)
    codeBusyTimer.current = window.setTimeout(() => setCodeBusyVisible(true), 600)
  }, [])
  const finishCodeBusy = React.useCallback(() => {
    window.clearTimeout(codeBusyTimer.current)
    setCodeBusy(false)
    setCodeBusyVisible(false)
  }, [])

  // ── 删除队列派生 ──
  const isMailboxDeleteQueued = React.useCallback((id: string) => dq.queue.some((t) => t.id === id), [dq.queue])
  const isMailboxDeleting = React.useCallback((id: string) => dq.deleting.includes(id), [dq.deleting])
  const isMailboxDeleteBusy = React.useCallback(
    (id: string) => dq.confirmID === id || isMailboxDeleting(id) || isMailboxDeleteQueued(id),
    [dq.confirmID, isMailboxDeleting, isMailboxDeleteQueued],
  )

  // ── 选择 ──
  const selectedMailboxIDs = React.useMemo(() => selectedMailboxes.map((m) => m.id), [selectedMailboxes])
  const selectedDeletableCount = React.useMemo(
    () => selectedMailboxes.filter((m) => !isMailboxDeleteBusy(m.id)).length,
    [selectedMailboxes, isMailboxDeleteBusy],
  )
  const selectedPageCount = React.useMemo(
    () => items.filter((m) => selectedMailboxIDs.includes(m.id)).length,
    [items, selectedMailboxIDs],
  )
  const allPageMailboxesSelected = items.length > 0 && selectedPageCount === items.length
  const somePageMailboxesSelected = selectedPageCount > 0 && !allPageMailboxesSelected

  function unselectMailbox(id: string) {
    setSelectedMailboxes((prev) => prev.filter((m) => m.id !== id))
  }

  function toggleMailboxSelection(mailbox: ICloudMailbox) {
    if (isMailboxDeleteBusy(mailbox.id)) return
    setSelectedMailboxes((prev) =>
      prev.some((m) => m.id === mailbox.id)
        ? prev.filter((m) => m.id !== mailbox.id)
        : [...prev, { id: mailbox.id, email: mailbox.email }],
    )
  }

  function toggleAllMailboxSelection() {
    if (!items.length) return
    const pageIDs = new Set(items.map((m) => m.id))
    if (allPageMailboxesSelected) {
      setSelectedMailboxes((prev) => prev.filter((m) => !pageIDs.has(m.id)))
      return
    }
    setSelectedMailboxes((prev) => {
      const have = new Set(prev.map((m) => m.id))
      return [...prev, ...items.filter((m) => !have.has(m.id)).map((m) => ({ id: m.id, email: m.email }))]
    })
  }

  async function copyMailboxEmail(mailbox: ICloudMailbox) {
    try {
      await copyText(mailbox.email)
      toast.success(`邮箱已复制:${mailbox.email}`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "复制邮箱失败,请重试")
    }
  }

  function mailboxAppleAccount(mailbox?: ICloudMailbox | null): string {
    const account = mailbox?.account_id ? accountByID.get(mailbox.account_id) : undefined
    return account?.apple_id || account?.label || mailbox?.account_id || "—"
  }

  // ── 彻底删除队列(前端并发,同一账号同时只删一个,最多 4 账号) ──
  const MAX_CONCURRENT_DELETE_ACCOUNTS = 4
  const activeDeleteAccountsRef = React.useRef<Set<string>>(new Set())

  const updateDeleteProgress = React.useCallback((state: DeleteQueueState) => {
    const finished = state.succeeded + state.failed
    const running = state.deleting.length
    const waiting = state.queue.length
    const total = finished + running + waiting
    if (!total) return
    deleteNoticeID.current = toast(
      `彻底删除邮箱:执行中 ${running}｜排队中 ${waiting}｜已完成 ${finished}/${total}(成功 ${state.succeeded},失败 ${state.failed})`,
      { id: deleteNoticeID.current, duration: Infinity },
    )
    deleteToastVisible.current = true
  }, [])

  const executeMailboxDeletion = React.useCallback(
    async (task: DeleteTask, accountKey: string) => {
      try {
        // useICloudDeleteMailboxOne({id, localOnly}) 语义:DELETE /mailboxes/:id(?local_only=1)
        await icloudApi.delete(`/mailboxes/${encodeURIComponent(task.id)}`, { local_only: task.localOnly })
        setDq((prev) => {
          const next = { ...prev, succeeded: prev.succeeded + 1 }
          dqRef.current = next
          return next
        })
        unselectMailbox(task.id)
        setSelected((prev) => (prev?.id === task.id ? null : prev))
        setCodeMailbox((prev) => (prev?.id === task.id ? null : prev))
      } catch (err) {
        const msg = errMsg(err, "删除失败")
        setDq((prev) => {
          const next = { ...prev, failed: prev.failed + 1, lastError: `${task.email}:${msg}` }
          dqRef.current = next
          return next
        })
      } finally {
        deletingIDsRef.current.delete(task.id)
        activeDeleteAccountsRef.current.delete(accountKey)
        setDq((prev) => {
          const next = { ...prev, deleting: prev.deleting.filter((x) => x !== task.id) }
          dqRef.current = next
          return next
        })
        refreshList()
        // 递归驱动队列
        window.setTimeout(() => pumpDeleteQueue(), 0)
      }
    },
    // eslint-disable-next-line react-hook/exhaustive-deps
    [refreshList],
  )

  const pumpDeleteQueue = React.useCallback(() => {
    const snapshot = dqRef.current
    const queue = [...snapshot.queue]
    const deleting = [...snapshot.deleting]
    let changed = false
    while (activeDeleteAccountsRef.current.size < MAX_CONCURRENT_DELETE_ACCOUNTS) {
      const idx = queue.findIndex((t) => !activeDeleteAccountsRef.current.has(t.accountID || "unknown-account"))
      if (idx < 0) break
      const [task] = queue.splice(idx, 1)
      const key = task.accountID || "unknown-account"
      activeDeleteAccountsRef.current.add(key)
      deletingIDsRef.current.add(task.id)
      deleting.push(task.id)
      changed = true
      void executeMailboxDeletion(task, key)
    }
    if (changed) {
      const next: DeleteQueueState = { ...dqRef.current, queue, deleting }
      dqRef.current = next
      setDq(next)
      updateDeleteProgress(next)
      return
    }
    // 无新任务可调度:若队列与执行都为空 → 汇总收尾
    if (!queue.length && activeDeleteAccountsRef.current.size === 0 && deletingIDsRef.current.size === 0) {
      const { succeeded, failed, lastError } = dqRef.current
      if (succeeded + failed > 0) {
        const total = succeeded + failed
        const type = failed ? (succeeded ? "warning" : "error") : "success"
        const text = failed
          ? `彻底删除邮箱已结束:成功 ${succeeded}｜失败 ${failed}${lastError ? `;最近错误:${lastError}` : ""}`
          : `彻底删除邮箱已完成:成功 ${total}｜失败 0`
        toast[type](text, { id: deleteNoticeID.current, duration: 7000 })
      } else if (deleteToastVisible.current) {
        toast.dismiss(deleteNoticeID.current)
      }
      deleteToastVisible.current = false
      deleteNoticeID.current = undefined
      const idle: DeleteQueueState = { ...DELETE_QUEUE_IDLE, confirmID: dqRef.current.confirmID }
      dqRef.current = idle
      setDq(idle)
    }
  }, [executeMailboxDeletion, updateDeleteProgress])

  const enqueueMailboxDeletions = React.useCallback(
    (mailboxes: { id: string; email: string; account_id?: string; accountID?: string; localOnly?: boolean }[]) => {
      const targets = mailboxes.filter(
        (m) => m?.id && !deletingIDsRef.current.has(m.id) && !dqRef.current.queue.some((t) => t.id === m.id),
      )
      if (!targets.length) return
      const fresh = dqRef.current.queue.length === 0 && dqRef.current.deleting.length === 0
      const next: DeleteQueueState = {
        ...dqRef.current,
        succeeded: fresh ? 0 : dqRef.current.succeeded,
        failed: fresh ? 0 : dqRef.current.failed,
        lastError: fresh ? "" : dqRef.current.lastError,
        queue: [
          ...dqRef.current.queue,
          ...targets.map((m) => ({
            id: m.id,
            email: m.email,
            accountID: m.account_id || m.accountID || "",
            localOnly: Boolean(m.localOnly),
          })),
        ],
      }
      dqRef.current = next
      setDq(next)
      updateDeleteProgress(next)
      window.setTimeout(() => pumpDeleteQueue(), 0)
    },
    [pumpDeleteQueue, updateDeleteProgress],
  )

  // ── 行内/详情删除 ──
  function deleteMailbox(mailbox: ICloudMailbox | null, localOnly: boolean) {
    if (!mailbox) return
    if (dqRef.current.confirmID) {
      toast.error("请先完成当前删除确认")
      return
    }
    if (isMailboxDeleteBusy(mailbox.id)) {
      toast.error(`${mailbox.email} 已在删除队列中`)
      return
    }
    setDq((prev) => {
      const next = { ...prev, confirmID: mailbox.id }
      dqRef.current = next
      return next
    })
    setConfirmState({
      title: localOnly ? "只删除本地记录" : "彻底删除隐私邮箱",
      message: localOnly
        ? `将先清空本地邮件记录,再删除本地邮箱记录;Apple 服务器上的隐私邮箱会保留。继续吗?`
        : `将根据本地已同步邮件保存的远端标识,把 ${mailbox.email} 对应的 Apple 邮件移入废纸篓并清空该账号的整个废纸篓,再删除 Apple 隐私邮箱和本地记录。未同步到本地的历史邮件不会参与定位,此操作不可恢复,继续吗?`,
      confirmText: localOnly ? "确认删除本地记录" : "确认彻底删除",
      action: () => {
        enqueueMailboxDeletions([
          { id: mailbox.id, email: mailbox.email, account_id: mailbox.account_id, localOnly },
        ])
      },
    })
  }

  // ── 删除选中 ──
  function removeSelectedMailboxes() {
    const targets = selectedMailboxes.filter((m) => !isMailboxDeleteBusy(m.id))
    if (!targets.length) return
    if (dqRef.current.confirmID) {
      toast.error("请先完成当前删除确认")
      return
    }
    setDq((prev) => {
      const next = { ...prev, confirmID: "selected" }
      dqRef.current = next
      return next
    })
    setConfirmState({
      title: `彻底删除选中的 ${targets.length} 个邮箱`,
      message:
        "将根据本地已同步邮件保存的远端标识,逐个把对应 Apple 邮件移入废纸篓并清空所属账号的整个废纸篓,然后删除 Apple 隐私邮箱和本地记录。未同步到本地的历史邮件不会参与定位。",
      confirmText: "确认彻底删除",
      action: () => enqueueMailboxDeletions(targets),
    })
  }

  function closeConfirm(confirmed: boolean) {
    const pending = confirmState
    setConfirmState(null)
    setDq((prev) => {
      if (!prev.confirmID) return prev
      const next = { ...prev, confirmID: "" }
      dqRef.current = next
      return next
    })
    pending?.onClose?.()
    if (confirmed && pending) pending.action()
  }

  // ── 行内同步(mailboxSyncBatch 聚合进度 toast) ──
  function beginMailboxSync() {
    if (!syncBatchRef.current)
      syncBatchRef.current = { total: 0, running: 0, success: 0, failed: 0, noticeID: undefined }
    const batch = syncBatchRef.current
    batch.total += 1
    batch.running += 1
    batch.noticeID = toast(
      `邮件同步:执行中 ${batch.running}｜排队中 0｜已完成 ${batch.success + batch.failed}/${batch.total}(成功 ${batch.success},失败 ${batch.failed})`,
      { id: batch.noticeID, duration: Infinity },
    )
    return batch
  }

  function finishMailboxSync(batch: NonNullable<typeof syncBatchRef.current>, successful: boolean) {
    if (syncBatchRef.current !== batch) return
    batch.running = Math.max(0, batch.running - 1)
    if (successful) batch.success += 1
    else batch.failed += 1
    if (batch.running > 0) {
      batch.noticeID = toast(
        `邮件同步:执行中 ${batch.running}｜排队中 0｜已完成 ${batch.success + batch.failed}/${batch.total}(成功 ${batch.success},失败 ${batch.failed})`,
        { id: batch.noticeID, duration: Infinity },
      )
      return
    }
    const type = batch.failed ? (batch.success ? "warning" : "error") : "success"
    toast[type](`邮件同步已完成:成功 ${batch.success}｜失败 ${batch.failed}`, {
      id: batch.noticeID,
      duration: 7000,
    })
    syncBatchRef.current = null
  }

  async function runMailboxMessageSync(mailbox: ICloudMailbox, opts: { detail?: boolean } = {}) {
    if (!mailbox) return
    const started = opts.detail ? startBusy("sync") : startRowBusy(mailbox.id, "sync")
    if (!started) return
    const batch = beginMailboxSync()
    let successful = false
    try {
      const data = await syncMailboxMessagesMut.mutateAsync(mailbox.id)
      successful = true
      toast.success(`同步完成:${mailboxMessageSyncSummary(data)}`)
      if (opts.detail) {
        await openMailbox(mailbox)
      }
      refreshList()
    } catch (err) {
      toast.error(errMsg(err, "同步失败"))
    } finally {
      finishMailboxSync(batch, successful)
      if (opts.detail) finishBusy("sync")
      else finishRowBusy(mailbox.id)
    }
  }

  function mailboxMessageSyncSummary(data: unknown): string {
    const d = (data ?? {}) as Record<string, number | undefined>
    return `IMAP ${d.imap_accounts ?? 0}｜Web API ${d.web_api_accounts ?? 0}｜扫描 ${d.scanned ?? 0}｜匹配 ${d.matched ?? 0}｜新增 ${d.synced_messages ?? d.synced ?? 0}`
  }

  // ── 取码 ──
  function openCodeDialog(mailbox: ICloudMailbox) {
    setCodeResult(null)
    setCodeMailbox({ ...mailbox })
    setCodeError("")
    setCodeOpen(true)
  }

  function closeCodeDialog() {
    if (codeBusy) return
    setCodeOpen(false)
    setCodeResult(null)
    setCodeMailbox(null)
    setCodeError("")
  }

  async function quickGetCode(mailbox: ICloudMailbox) {
    if (rowBusyAction(mailbox.id)) return
    if (!startRowBusy(mailbox.id, "code")) return
    openCodeDialog(mailbox)
    startCodeBusy()
    try {
      // GET /mailboxes/:id/code?allow_stale=1(后台带缓存)
      const result = await mailboxCodeMut.mutateAsync({ id: mailbox.id, allow_stale: true })
      setCodeResult(result)
      const detail = await icloudApi.get<{ mailbox: ICloudMailbox }>(
        `/mailboxes/${encodeURIComponent(mailbox.id)}`,
      )
      setCodeMailbox(detail.mailbox)
      refreshList()
      toast.success(`已提取验证码 ${result.code}`)
    } catch (err) {
      const message = errMsg(err, "取码失败")
      setCodeError(message)
      toast.error(message)
    } finally {
      finishCodeBusy()
      finishRowBusy(mailbox.id)
    }
  }

  async function getCodeFromDetail() {
    if (!selected) return
    const mailbox = selected
    openCodeDialog(mailbox)
    startCodeBusy()
    try {
      const result = await mailboxCodeMut.mutateAsync({ id: mailbox.id, allow_stale: true })
      setCodeResult(result)
      toast.success(`已提取验证码 ${result.code}`)
      const [detail, messageData] = await Promise.all([
        icloudApi.get<{ mailbox: ICloudMailbox }>(`/mailboxes/${encodeURIComponent(mailbox.id)}`),
        icloudApi.get<{ items: ICloudMessage[] }>(`/mailboxes/${encodeURIComponent(mailbox.id)}/messages`),
      ])
      setSelected(detail.mailbox)
      setCodeMailbox(detail.mailbox)
      qc.setQueryData(["icloud", "mailboxes", mailbox.id, "messages"], messageData)
    } catch (err) {
      const message = errMsg(err, "取码失败")
      setCodeError(message)
      toast.error(message)
    } finally {
      finishCodeBusy()
    }
  }

  async function copyCode() {
    if (!codeResult?.code) return
    try {
      await copyText(codeResult.code)
      toast.success("验证码已复制")
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "复制验证码失败,请重试")
    }
  }

  // ── 详情抽屉 ──
  async function openMailbox(mailbox: ICloudMailbox) {
    setCodeResult(null)
    setCodeError("")
    setCodeOpen(false)
    setSelectedMessageID(null)
    try {
      const [detail, messageData] = await Promise.all([
        icloudApi.get<{ mailbox: ICloudMailbox }>(`/mailboxes/${encodeURIComponent(mailbox.id)}`),
        icloudApi.get<{ items: ICloudMessage[] }>(`/mailboxes/${encodeURIComponent(mailbox.id)}/messages`),
      ])
      setSelected(detail.mailbox)
      qc.setQueryData(["icloud", "mailboxes", mailbox.id, "messages"], messageData)
      const form = {
        status: detail.mailbox.status,
        api_active: detail.mailbox.api_active,
        icloud_active: detail.mailbox.icloud_active,
        note: detail.mailbox.note || "",
      }
      setEdit(form)
      editOriginal.current = form
    } catch (err) {
      toast.error(errMsg(err, "加载邮箱详情失败"))
    }
  }

  async function openMailboxFromRow(mailbox: ICloudMailbox) {
    if (rowBusyAction(mailbox.id)) return
    if (!startRowBusy(mailbox.id, "detail")) return
    try {
      await openMailbox(mailbox)
    } finally {
      finishRowBusy(mailbox.id)
    }
  }

  async function saveStatus() {
    if (!selected) return
    if (!startBusy("save")) return
    try {
      await updateStatusMut.mutateAsync({ id: selected.id, ...edit })
      setSelected((prev) => (prev ? { ...prev, ...edit } : prev))
      const original = { ...edit }
      editOriginal.current = original
      toast.success("邮箱状态已保存")
      refreshList()
    } catch (err) {
      toast.error(errMsg(err, "保存失败"))
    } finally {
      finishBusy("save")
    }
  }

  function openMessage(item: ICloudMessage) {
    if (!item || !selected?.id) return
    setSelectedMessageID(item.id)
    setMessageViewMode(messageHasHTMLHint(item) ? "html" : "text")
  }

  function closeSelectedMessage() {
    setSelectedMessageID(null)
    setMessageViewMode("text")
  }

  function messageHasHTMLHint(message: ICloudMessage | null): boolean {
    return Boolean(
      String(message?.html_body ?? "").trim() ||
        (String(message?.content_type ?? "").toLowerCase().includes("text/html") && looksLikeHTML(message?.body)),
    )
  }

  const selectedMessageHasHTML = messageHasHTMLHint(selectedMessage)
  const selectedMessageHTML = React.useMemo(() => {
    if (!selectedMessage) return ""
    return String(
      selectedMessage.html_body || (looksLikeHTML(selectedMessage.body) ? selectedMessage.body : ""),
    ).trim()
  }, [selectedMessage])
  const selectedMessageHTMLDocument = React.useMemo(
    () => buildEmailHTMLDocument(selectedMessageHTML),
    [selectedMessageHTML],
  )
  const showSelectedMessageHTML = selectedMessageHasHTML && messageViewMode !== "text"

  // ── 快速编辑(备注/状态) ──
  function openQuickEdit(mailbox: ICloudMailbox, field: "note" | "status") {
    setQuickEditMailbox(mailbox)
    setQuickEditField(field)
    setQuickEdit({ status: mailbox.status || "available", note: mailbox.note || "" })
    setQuickEditOpen(true)
  }

  function closeQuickEdit() {
    if (isBusy("quick-edit")) return
    setQuickEditOpen(false)
    setQuickEditMailbox(null)
  }

  async function saveQuickEdit() {
    if (!quickEditMailbox) return
    const payload =
      quickEditField === "note" ? { note: quickEdit.note } : { status: quickEdit.status }
    if (!startBusy("quick-edit")) return
    try {
      await updateStatusMut.mutateAsync({ id: quickEditMailbox.id, ...payload })
      toast.success(quickEditField === "note" ? "邮箱备注已保存" : "邮箱状态已保存")
      setQuickEditOpen(false)
      setQuickEditMailbox(null)
      refreshList()
    } catch (err) {
      toast.error(errMsg(err, "保存失败"))
    } finally {
      finishBusy("quick-edit")
    }
  }

  // ── 远程清理 ──
  function cleanRemote() {
    if (!selected || (!remoteClean.move_synced && !remoteClean.empty_trash)) return
    const mailbox = selected
    const actions = [
      remoteClean.move_synced ? "把已同步邮件移入废纸篓" : "",
      remoteClean.empty_trash ? "清空该账号废纸篓" : "",
    ]
      .filter(Boolean)
      .join(",并")
    setConfirmState({
      title: "清理 Apple 远端邮件",
      message: `将${actions},这项操作会修改 Apple 服务器上的邮件。`,
      confirmText: "确认清理",
      action: async () => {
        if (!startBusy("clean")) return
        try {
          const data = await icloudApi.post<{ cleanup?: RemoteCleanupStats }>(
            `/mailboxes/${encodeURIComponent(mailbox.id)}/remote-clean`,
            remoteClean,
          )
          await openMailbox(mailbox)
          refreshList()
          toast.success(`远端清理完成:${cleanupText(data?.cleanup)}`)
        } catch (err) {
          toast.error(errMsg(err, "远端清理失败"))
        } finally {
          finishBusy("clean")
        }
      },
    })
  }

  // ── 全部彻底清理 Apple 邮件 ──
  function cleanAllAppleMail() {
    if (isBusy("clean-summary") || isBusy("clean-start") || cleanupRunning) return
    startBusy("clean-summary")
    const dashboard = dashboardQ.data
    setConfirmState({
      title: "全部彻底清理 Apple 邮件",
      message: `清理范围:Apple 账号 ${dashboard?.apple_account_count ?? 0} 个,本地邮件 ${dashboard?.message_count ?? 0} 封。将逐个扫描每个账号的收件箱、已发送、草稿、归档、垃圾邮件和自定义文件夹,把全部 Apple 云端邮件移入废纸篓后彻底删除,再清理本地邮件数据。隐私邮箱地址本身会保留,此操作不可恢复。`,
      confirmText: "确认全部清理",
      action: async () => {
        startBusy("clean-start")
        try {
          const data = await icloudApi.post<{ job?: AppleMailCleanupJobLike }>("/apple-mail/cleanup", {
            account_ids: [],
            scope: "all",
            strategy: "move_then_destroy",
            purge_local: true,
          })
          if (data?.job) {
            qc.setQueryData(["icloud", "apple-mail-cleanup", "status"], { job: data.job })
          }
          void cleanupStatusQ.refetch()
        } catch (err) {
          const message = errMsg(err, "清理失败")
          if (cleanAllNoticeID.current !== undefined)
            toast.error(`全部邮件清理失败:${message}`, { id: cleanAllNoticeID.current, duration: 7000 })
          else toast.error(message)
        } finally {
          finishBusy("clean-start")
        }
      },
      onClose: () => finishBusy("clean-summary"),
    })
  }

  // ── 同步已有邮箱(对话框) ──
  function openSyncDialog() {
    if (!accounts.length) {
      toast.error("请先添加 Apple 账号和登录态")
      return
    }
    setSyncAccountID("")
    setShowImport(false)
    setShowBulkDelete(false)
    setShowSync(true)
  }

  async function syncExistingMailboxes() {
    if (isBusy("sync-existing")) return
    const targets = syncAccountID ? accounts.filter((a) => a.id === syncAccountID) : accounts
    if (!targets.length) {
      toast.error("没有可同步的 Apple 账号")
      return
    }
    startBusy("sync-existing")
    const failures: string[] = []
    let total = 0
    let succeeded = 0
    let noticeID: string | number | undefined
    try {
      for (const [index, account] of targets.entries()) {
        const finished = succeeded + failures.length
        noticeID = toast(
          `同步已有邮箱:执行中 1｜排队中 ${Math.max(0, targets.length - index - 1)}｜已完成 ${finished}/${targets.length}(成功 ${succeeded},失败 ${failures.length})`,
          { id: noticeID, duration: Infinity },
        )
        try {
          const data = await syncMailboxesMut.mutateAsync(account.id)
          total += data.count || data.items?.length || 0
          succeeded += 1
        } catch (err) {
          failures.push(`${account.label || account.apple_id || account.id}:${errMsg(err, "同步失败")}`)
        }
      }
      setShowSync(false)
      setPage(1)
      refreshList()
      const noticeType = failures.length ? (succeeded ? "warning" : "error") : "success"
      const text = failures.length
        ? `同步已有邮箱已结束:成功 ${succeeded}｜失败 ${failures.length};最近错误:${failures[failures.length - 1]}`
        : `同步已有邮箱已完成:成功 ${succeeded}｜失败 0`
      toast[noticeType](text, { id: noticeID, duration: 7000 })
      if (failures.length && succeeded) {
        toast.error(`同步完成 ${succeeded} 个账号,共发现 ${total} 个已有邮箱;${failures.join(";")}`)
      } else if (failures.length) {
        toast.error(`同步已有邮箱失败:${failures.join(";")}`)
      } else {
        toast.success(`同步完成:${succeeded} 个账号,共发现 ${total} 个已有邮箱`)
      }
    } catch (err) {
      toast.error(`同步已有邮箱失败:${errMsg(err, "同步失败")}`, { id: noticeID, duration: 7000 })
    } finally {
      finishBusy("sync-existing")
    }
  }

  // ── 同步已有邮箱邮件(触发后端任务,进度走轮询) ──
  function showSyncAllIntro() {
    window.clearTimeout(syncAllIntroTimer.current)
    syncAllIntroVisible.current = true
    syncAllNoticeID.current = toast("正在读取所有 Apple 主号的全部邮件:IMAP 主路径,并使用 iCloud Web 补查缺失邮件……", {
      id: syncAllNoticeID.current,
      duration: 1400,
    })
    syncAllIntroTimer.current = window.setTimeout(() => {
      syncAllIntroVisible.current = false
      applySyncAllJob(syncAllRef.current)
    }, 1450)
  }

  async function syncExistingMailboxMessages() {
    if (isBusy("sync-existing-messages") || isBusy("sync-existing") || syncAllRunning) return
    if (!startBusy("sync-existing-messages")) return
    showSyncAllIntro()
    try {
      const data = await icloudApi.post<{ job?: MessageSyncJobLike }>("/mailboxes/sync-messages", {})
      if (data?.job) {
        qc.setQueryData(["icloud", "mailboxes", "sync-messages", "status"], { job: data.job })
      }
      void syncAllStatusQ.refetch()
    } catch (err) {
      syncAllIntroVisible.current = false
      window.clearTimeout(syncAllIntroTimer.current)
      if (err instanceof HttpError && err.code === "mailbox_message_sync_running") {
        // 已有任务在跑:只拉取进度
        void syncAllStatusQ.refetch()
      } else {
        toast.error(`邮件同步启动失败:${errMsg(err, "启动失败")}`, {
          id: syncAllNoticeID.current,
          duration: 7000,
        })
      }
    } finally {
      finishBusy("sync-existing-messages")
    }
  }

  // ── 批量删除指定邮箱 ──
  const bulkDeleteEmailCount = React.useMemo(() => parseBulkDeleteEmails(bulkDeleteEmails).length, [bulkDeleteEmails])

  function openBulkDeleteDialog() {
    setShowImport(false)
    setShowSync(false)
    setBulkDeleteError("")
    setShowBulkDelete(true)
  }

  function closeBulkDeleteDialog() {
    if (isBusy("bulk-delete-resolve")) return
    setShowBulkDelete(false)
    setBulkDeleteError("")
  }

  async function submitBulkDeleteEmails() {
    const emails = parseBulkDeleteEmails(bulkDeleteEmails)
    if (!emails.length) {
      setBulkDeleteError("请至少输入一个邮箱地址。")
      return
    }
    const invalid = emails.filter((e) => !EMAIL_RE.test(e))
    if (invalid.length) {
      setBulkDeleteError(`邮箱格式不正确:${emailListSummary(invalid)}`)
      return
    }
    if (!startBusy("bulk-delete-resolve")) return
    setBulkDeleteError("")
    try {
      const data = await resolveEmailsMut.mutateAsync(emails)
      const resolved = data.items ?? []
      const targets = resolved.filter((m) => !isMailboxDeleteBusy(m.id))
      const missing = data.missing ?? []
      if (!targets.length) {
        if (missing.length) setBulkDeleteError(`本地邮箱池中未找到:${emailListSummary(missing)}`)
        else setBulkDeleteError("这些邮箱已经在删除队列中。")
        return
      }
      enqueueMailboxDeletions(targets.map((m) => ({ id: m.id, email: m.email, account_id: m.account_id })))
      setShowBulkDelete(false)
      setBulkDeleteEmails("")
      toast.success(`已将 ${targets.length} 个指定邮箱加入彻底删除队列`)
      if (missing.length) toast.error(`未在本地邮箱池找到:${emailListSummary(missing)}`)
    } catch (err) {
      setBulkDeleteError(errMsg(err, "解析邮箱失败"))
    } finally {
      finishBusy("bulk-delete-resolve")
    }
  }

  // ── 导入本地邮箱 ──
  function openImportDialog() {
    setShowSync(false)
    setShowBulkDelete(false)
    setMailboxImport((prev) => ({
      ...prev,
      account_id: prev.account_id || accounts[0]?.id || "",
      email: "",
      label: "",
      note: "",
    }))
    setShowImport(true)
  }

  async function importMailbox() {
    if (!mailboxImport.account_id || !mailboxImport.email.trim()) return
    if (!startBusy("import")) return
    try {
      const data = await importMailboxMut.mutateAsync({
        account_id: mailboxImport.account_id,
        email: mailboxImport.email.trim(),
        label: mailboxImport.label.trim(),
        note: mailboxImport.note.trim(),
      })
      toast.success(
        data.created ? `已导入 ${data.mailbox.email}` : `${data.mailbox.email} 已存在,已更新绑定信息`,
      )
      setMailboxImport({ account_id: mailboxImport.account_id, email: "", label: "", note: "" })
      setShowImport(false)
      refreshList()
    } catch (err) {
      toast.error(errMsg(err, "导入失败"))
    } finally {
      finishBusy("import")
    }
  }

  // ── 分页 ──
  function goToPage(target: number) {
    if (mailboxesQ.isFetching && !mailboxesQ.isPlaceholderData) return
    const totalPages = result?.total_pages ?? 1
    const next = Math.max(1, Math.min(totalPages, target))
    if (next === page) return
    setPage(next)
  }

  // ── 动态 pageSize(calculateMailboxTableSize) ──
  const calculateMailboxTableSize = React.useCallback((): number => {
    const element = tableViewportRef.current
    if (!element) return pageSize
    const scrollContainer = element.closest(".page-scroll") as HTMLElement | null
    const scrollTop = scrollContainer?.scrollTop ?? 0
    const viewportTop = element.getBoundingClientRect().top + scrollTop
    const scrollBottom = scrollContainer?.getBoundingClientRect().bottom || window.innerHeight
    const scrollStyle = scrollContainer ? window.getComputedStyle(scrollContainer) : null
    const bottomPadding = Number.parseFloat(scrollStyle?.paddingBottom || "0") || 0
    const footerHeight = paginationRef.current?.getBoundingClientRect().height || 53
    const headerHeight = element.querySelector("thead")?.getBoundingClientRect().height || 36
    const dataRow = element.querySelector("tbody tr:not([data-empty-row])")
    const rowHeight = dataRow?.getBoundingClientRect().height || 48
    const tableWidth = element.querySelector("table")?.scrollWidth || 0
    const hasHorizontalScrollbar = tableWidth > element.clientWidth + 1
    const reservedHeight = (hasHorizontalScrollbar ? 8 : 0) + 1
    const minimumRows = window.matchMedia("(max-width: 620px)").matches ? 3 : 5
    const availableHeight = scrollBottom - viewportTop - footerHeight - bottomPadding - 2
    const visibleRows = Math.max(
      minimumRows,
      Math.min(50, Math.floor((availableHeight - headerHeight - reservedHeight) / rowHeight)),
    )
    return visibleRows
  }, [pageSize])

  const applyMailboxTableSize = React.useCallback(() => {
    const nextPageSize = calculateMailboxTableSize()
    if (nextPageSize === pageSize) return
    // 保持首个可见项:计算原 pageSize 下的第一条索引并换算到新页
    const firstVisibleIndex = (page - 1) * pageSize
    setPageSize(nextPageSize)
    setPage(Math.floor(firstVisibleIndex / nextPageSize) + 1)
  }, [calculateMailboxTableSize, page, pageSize])

  const scheduleMailboxTableSize = React.useCallback(() => {
    window.clearTimeout(tableResizeTimer.current)
    tableResizeTimer.current = window.setTimeout(applyMailboxTableSize, 120)
  }, [applyMailboxTableSize])

  // ── 全部清理进度 toast(轮询驱动) ──
  const applyCleanupJob = React.useCallback(
    (job: AppleMailCleanupJobLike | undefined, showCompleted = true) => {
      if (!job || typeof job !== "object") return
      if (job.running) {
        cleanAllNoticeID.current = toast(appleMailCleanupText(job), {
          id: cleanAllNoticeID.current,
          duration: Infinity,
        })
        cleanupWasRunning.current = true
        return
      }
      if (!showCompleted && !cleanupWasRunning.current) return
      if (job.status === "completed") {
        const completedText =
          Number(job.discovered ?? 0) > 0 ? "全部 Apple 云端邮件已清理完成" : "没有可清理的 Apple 云端邮件"
        toast.success(`${appleMailCleanupText(job)};${completedText}`, {
          id: cleanAllNoticeID.current,
          duration: 7000,
        })
      } else if (job.status === "partial") {
        toast.warning(`${appleMailCleanupText(job)};部分账号失败:${job.last_error || "请查看失败账号"}`, {
          id: cleanAllNoticeID.current,
          duration: 9000,
        })
      } else if (job.status === "cancelled" || job.status === "interrupted") {
        toast.warning(`全部邮件清理已停止:${job.last_error || "任务未完成"}`, {
          id: cleanAllNoticeID.current,
          duration: 7000,
        })
      }
      cleanAllNoticeID.current = undefined
      if (cleanupWasRunning.current) refreshList()
      cleanupWasRunning.current = false
    },
    [refreshList],
  )

  React.useEffect(() => {
    applyCleanupJob(cleanupJob, false)
  }, [cleanupJob, applyCleanupJob])

  // ── 批量邮件同步进度 toast(轮询驱动) ──
  const applySyncAllJob = React.useCallback(
    (job: MessageSyncJobLike | undefined, showCompleted = true) => {
      if (!job || typeof job !== "object") return
      if (syncAllIntroVisible.current) return
      if (job.running) {
        syncAllNoticeID.current = toast(mailboxMessageSyncJobText(job), {
          id: syncAllNoticeID.current,
          duration: Infinity,
        })
        syncAllWasRunning.current = true
        return
      }
      if (!showCompleted && !syncAllWasRunning.current) return
      if (job.status === "completed") {
        const text = Number(job.total_mailboxes ?? 0)
          ? mailboxMessageSyncCompletedText(job)
          : "没有可同步邮件的已有邮箱"
        const type = Number(job.total_mailboxes ?? 0) && !job.has_more ? "success" : "warning"
        toast[type](text, { id: syncAllNoticeID.current, duration: 7000 })
      } else if (job.status === "partial") {
        const recentError = job.last_error ? `;最近错误:${job.last_error}` : ""
        const type = job.successful_accounts ? "warning" : "error"
        toast[type](`${mailboxMessageSyncCompletedText(job)}${recentError}`, {
          id: syncAllNoticeID.current,
          duration: 9000,
        })
      } else if (job.status === "interrupted") {
        toast.warning(`邮件同步已停止:${job.last_error || "任务未完成"}`, {
          id: syncAllNoticeID.current,
          duration: 7000,
        })
      }
      syncAllNoticeID.current = undefined
      if (syncAllWasRunning.current) refreshList()
      syncAllWasRunning.current = false
    },
    [refreshList],
  )

  React.useEffect(() => {
    applySyncAllJob(syncAllJob, false)
  }, [syncAllJob, applySyncAllJob])

  // ── 搜索防抖(300ms)与筛选变更重置页码 ──
  const searchTimer = React.useRef<number | undefined>(undefined)
  const [queryInput, setQueryInput] = React.useState("")
  React.useEffect(() => {
    window.clearTimeout(searchTimer.current)
    searchTimer.current = window.setTimeout(() => {
      setQuery(queryInput)
      setPage(1)
    }, 300)
    return () => window.clearTimeout(searchTimer.current)
  }, [queryInput])

  function hasActiveFilters() {
    return Boolean(queryInput || query || accountID || statusFilter)
  }

  function clearFilters() {
    setQueryInput("")
    setQuery("")
    setAccountID("")
    setStatusFilter("")
    setPage(1)
  }

  // ── 加载遮罩:600ms 后才显示,避免闪烁 ──
  React.useEffect(() => {
    if (mailboxesQ.isPending && enabled) {
      window.clearTimeout(loadingTimer.current)
      loadingTimer.current = window.setTimeout(() => setLoadingVisible(true), 600)
      return () => window.clearTimeout(loadingTimer.current)
    }
    window.clearTimeout(loadingTimer.current)
    setLoadingVisible(false)
    return undefined
  }, [mailboxesQ.isPending, enabled])

  // 首次加载完成后计算一次表格尺寸
  React.useEffect(() => {
    if (!mailboxesQ.isPending) scheduleMailboxTableSize()
  }, [mailboxesQ.isPending, scheduleMailboxTableSize])

  // ── 30s 轮询(refreshMailboxPool):静默刷新列表 + 详情(表单未被手改时跟随) ──
  React.useEffect(() => {
    if (!enabled) return
    const timer = window.setInterval(() => {
      if (document.hidden || busyActions.length || Object.keys(rowBusy).length) return
      if (dqRef.current.queue.length || dqRef.current.deleting.length) return
      refreshList()
      const selectedID = selected?.id
      if (!selectedID || quickEditOpen) return
      void (async () => {
        try {
          const [detail, messageData] = await Promise.all([
            icloudApi.get<{ mailbox: ICloudMailbox }>(`/mailboxes/${encodeURIComponent(selectedID)}`),
            icloudApi.get<{ items: ICloudMessage[] }>(`/mailboxes/${encodeURIComponent(selectedID)}/messages`),
          ])
          setSelected((prev) => {
            if (!prev || prev.id !== selectedID) return prev
            // 用户未手改的字段跟随服务器
            setEdit((form) => {
              const next = { ...form }
              if (form.status === prev.status) next.status = detail.mailbox.status
              if (form.api_active === prev.api_active) next.api_active = detail.mailbox.api_active
              if (form.icloud_active === prev.icloud_active) next.icloud_active = detail.mailbox.icloud_active
              if (form.note === (prev.note || "")) next.note = detail.mailbox.note || ""
              return next
            })
            return detail.mailbox
          })
          qc.setQueryData(["icloud", "mailboxes", selectedID, "messages"], messageData)
        } catch {
          return
        }
      })()
    }, 30000)
    return () => window.clearInterval(timer)
    // eslint-disable-next-line react-hook/exhaustive-deps
  }, [enabled, selected?.id, quickEditOpen, busyActions.length, rowBusy])

  // ── 布局观察:命令栏/分页/滚动容器尺寸变化时重算 pageSize ──
  React.useEffect(() => {
    if (!enabled) return
    const observer = new ResizeObserver(scheduleMailboxTableSize)
    if (commandBarRef.current) observer.observe(commandBarRef.current)
    if (paginationRef.current) observer.observe(paginationRef.current)
    const scrollContainer = tableViewportRef.current?.closest(".page-scroll")
    if (scrollContainer) observer.observe(scrollContainer)
    window.addEventListener("resize", scheduleMailboxTableSize)
    return () => {
      observer.disconnect()
      window.removeEventListener("resize", scheduleMailboxTableSize)
      window.clearTimeout(tableResizeTimer.current)
    }
  }, [enabled, scheduleMailboxTableSize])

  // ── 任一弹窗打开时锁定 body 滚动 ──
  const anyOverlayOpen = Boolean(
    selected || codeOpen || quickEditOpen || showImport || showSync || showBulkDelete || confirmState,
  )
  React.useEffect(() => {
    document.body.style.overflow = anyOverlayOpen ? "hidden" : ""
    return () => {
      document.body.style.overflow = ""
    }
  }, [anyOverlayOpen])

  // ── Esc 键层级:完整邮件 → 取码 → 快速编辑 → 导入 → 同步 → 批量删除 → 详情抽屉 ──
  React.useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return
      if (selectedMessageID) closeSelectedMessage()
      else if (codeOpen) closeCodeDialog()
      else if (quickEditOpen) closeQuickEdit()
      else if (showImport) setShowImport(false)
      else if (showSync) setShowSync(false)
      else if (showBulkDelete) closeBulkDeleteDialog()
      else if (selected) setSelected(null)
    }
    document.addEventListener("keydown", handler)
    return () => document.removeEventListener("keydown", handler)
    // eslint-disable-next-line react-hook/exhaustive-deps
  }, [selectedMessageID, codeOpen, quickEditOpen, showImport, showSync, showBulkDelete, selected, codeBusy])

  // ── 渲染 ──
  const syncingExisting = isBusy("sync-existing")
  const syncingExistingMessages = isBusy("sync-existing-messages")
  const cleaningAll = isBusy("clean-summary") || isBusy("clean-start")

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <CardTitle className="text-sm">邮箱池</CardTitle>
            <CardDescription>收信、取码、状态维护与 Apple 远端删除</CardDescription>
          </div>
        </div>

        {/* 命令栏:过滤区 + 6 按钮操作区 */}
        <div ref={commandBarRef} className="flex flex-wrap items-center justify-between gap-2 pt-2">
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative">
              <Search className="text-muted-foreground absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" />
              <Input
                value={queryInput}
                onChange={(e) => setQueryInput(e.target.value)}
                placeholder="搜索邮箱、标签或备注"
                aria-label="搜索邮箱、标签或备注"
                className="h-8 w-56 pl-8 text-xs"
              />
            </div>
            <Select
              value={accountID || "all"}
              onValueChange={(v) => {
                setAccountID(v === "all" ? "" : v)
                setPage(1)
              }}
            >
              <SelectTrigger className="h-8 w-44 text-xs" size="sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部 Apple 账号</SelectItem>
                {accounts.map((a) => (
                  <SelectItem key={a.id} value={a.id}>
                    {a.label || a.apple_id || a.id}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={statusFilter || "all"}
              onValueChange={(v) => {
                setStatusFilter(v === "all" ? "" : v)
                setPage(1)
              }}
            >
              <SelectTrigger className="h-8 w-32 text-xs" size="sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部状态</SelectItem>
                <SelectItem value="available">可用</SelectItem>
                <SelectItem value="reserved">已预留</SelectItem>
                <SelectItem value="used">已使用</SelectItem>
                <SelectItem value="failed">失败</SelectItem>
                <SelectItem value="disabled">已停用</SelectItem>
              </SelectContent>
            </Select>
            {hasActiveFilters() && (
              <Button variant="ghost" size="sm" className="h-8 text-xs" onClick={clearFilters}>
                <X /> 清空筛选
              </Button>
            )}
          </div>

          <div className="flex flex-wrap items-center gap-1.5">
            <Button
              variant="secondary"
              size="sm"
              className="h-8 text-xs"
              disabled={syncingExisting || syncingExistingMessages || syncAllRunning}
              onClick={openSyncDialog}
            >
              {syncingExisting ? <LoaderCircle className="animate-spin" /> : <CloudDownload />}
              {syncingExisting ? "正在同步邮箱" : "同步已有邮箱"}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              className="h-8 text-xs"
              disabled={syncingExistingMessages || syncingExisting || syncAllRunning}
              title="读取所有 Apple 主号的全部邮件;IMAP 主路径,iCloud Web 补查并自动合并"
              onClick={syncExistingMailboxMessages}
            >
              {syncingExistingMessages || syncAllRunning ? (
                <LoaderCircle className="animate-spin" />
              ) : (
                <MailOpen />
              )}
              {syncAllRunning
                ? `正在同步 ${syncAllJob?.completed_accounts ?? 0}/${syncAllJob?.total_accounts ?? 0}`
                : syncingExistingMessages
                  ? "正在启动同步"
                  : "同步已有邮箱邮件"}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              className="h-8 text-xs"
              disabled={isBusy("import")}
              onClick={openImportDialog}
            >
              {isBusy("import") ? <LoaderCircle className="animate-spin" /> : <MailPlus />}
              {isBusy("import") ? "正在导入邮箱" : "导入本地邮箱"}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              className="h-8 text-xs"
              disabled={cleaningAll || cleanupRunning}
              title="扫描并彻底删除全部 Apple 账号的云端和本地邮件"
              onClick={cleanAllAppleMail}
            >
              {cleaningAll || cleanupRunning ? <LoaderCircle className="animate-spin" /> : <CloudOff />}
              {isBusy("clean-summary")
                ? "正在统计邮件"
                : isBusy("clean-start")
                  ? "正在启动清理"
                  : cleanupRunning
                    ? `正在清理 ${cleanupJob?.completed ?? 0}/${cleanupJob?.total_accounts ?? 0}`
                    : "全部彻底清理 Apple 邮件"}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              className="text-destructive h-8 text-xs"
              disabled={isBusy("bulk-delete-resolve")}
              title="按邮箱地址批量彻底删除 Apple 云端和本地邮箱"
              onClick={openBulkDeleteDialog}
            >
              <Trash2 /> 批量删除指定邮箱
            </Button>
            <Button
              variant="secondary"
              size="sm"
              className="text-destructive h-8 text-xs"
              disabled={!selectedDeletableCount || dq.confirmID === "selected"}
              title={
                selectedDeletableCount
                  ? `彻底删除选中的 ${selectedDeletableCount} 个邮箱`
                  : "请先选择未进入删除队列的邮箱"
              }
              onClick={removeSelectedMailboxes}
            >
              {dq.confirmID === "selected" ? <LoaderCircle className="animate-spin" /> : <Trash2 />}
              删除选中{selectedDeletableCount ? `(${selectedDeletableCount})` : ""}
            </Button>
          </div>
        </div>
      </CardHeader>

      <CardContent className="space-y-3">
        {/* 加载遮罩(600ms 后才显示) */}
        {loadingVisible && (
          <div className="text-muted-foreground flex items-center justify-center gap-2 py-2 text-xs">
            <LoaderCircle className="size-4 animate-spin" /> 正在加载邮箱
          </div>
        )}

        <div ref={tableViewportRef} className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">
                  <Checkbox
                    checked={allPageMailboxesSelected ? true : somePageMailboxesSelected ? "indeterminate" : false}
                    disabled={!items.length}
                    aria-label="全选当前页邮箱"
                    onCheckedChange={toggleAllMailboxSelection}
                  />
                </TableHead>
                <TableHead>Apple 账号</TableHead>
                <TableHead>邮箱</TableHead>
                <TableHead>标签 / 备注</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>API / iCloud</TableHead>
                <TableHead>收件</TableHead>
                <TableHead>最近同步</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {!items.length && !mailboxesQ.isPending && (
                <TableRow data-empty-row>
                  <TableCell colSpan={9} className="py-10 text-center">
                    <div className="flex flex-col items-center gap-1.5">
                      <Boxes className="text-muted-foreground size-5" />
                      <strong className="text-sm">没有符合条件的邮箱</strong>
                      <small className="text-muted-foreground text-xs">
                        从 Apple 账号页创建或同步隐私邮箱后会显示在这里。
                      </small>
                    </div>
                  </TableCell>
                </TableRow>
              )}
              {items.map((mailbox) => {
                const busy = rowBusyAction(mailbox.id)
                const deleteBusy = isMailboxDeleteBusy(mailbox.id)
                const deleting = isMailboxDeleting(mailbox.id)
                const queued = isMailboxDeleteQueued(mailbox.id)
                return (
                  <TableRow
                    key={mailbox.id}
                    data-state={selectedMailboxIDs.includes(mailbox.id) ? "selected" : undefined}
                  >
                    <TableCell>
                      <Checkbox
                        checked={selectedMailboxIDs.includes(mailbox.id)}
                        disabled={deleteBusy}
                        aria-label={`选择邮箱 ${mailbox.email}`}
                        onCheckedChange={() => toggleMailboxSelection(mailbox)}
                      />
                    </TableCell>
                    <TableCell className="max-w-36">
                      <span className="block truncate text-xs" title={mailboxAppleAccount(mailbox)}>
                        {mailboxAppleAccount(mailbox)}
                      </span>
                    </TableCell>
                    <TableCell className="max-w-52">
                      <button
                        type="button"
                        className="block w-full truncate text-left font-mono text-xs hover:underline"
                        title={`点击复制邮箱:${mailbox.email}`}
                        onClick={() => copyMailboxEmail(mailbox)}
                      >
                        {mailbox.email}
                      </button>
                    </TableCell>
                    <TableCell className="max-w-40">
                      <span className="block truncate text-xs" title={mailbox.label || "—"}>
                        {mailbox.label || "—"}
                      </span>
                      <button
                        type="button"
                        className="text-muted-foreground block max-w-full truncate text-left text-[11px] hover:underline"
                        title={mailbox.note ? `点击修改备注:${mailbox.note}` : "点击添加备注"}
                        onClick={() => openQuickEdit(mailbox, "note")}
                      >
                        {mailbox.note || "添加备注"}
                      </button>
                    </TableCell>
                    <TableCell>
                      <button
                        type="button"
                        title="点击修改邮箱状态"
                        onClick={() => openQuickEdit(mailbox, "status")}
                      >
                        <Badge
                          variant={
                            mailbox.status === "available" || mailbox.status === "active"
                              ? "default"
                              : mailbox.status === "failed"
                                ? "destructive"
                                : mailbox.status === "disabled"
                                  ? "outline"
                                  : "secondary"
                          }
                          className="text-xs"
                        >
                          {statusLabel(mailbox.status)}
                        </Badge>
                      </button>
                    </TableCell>
                    <TableCell>
                      <ChannelBadges apiActive={mailbox.api_active} icloudActive={mailbox.icloud_active} />
                    </TableCell>
                    <TableCell className="text-xs tabular-nums">{mailbox.receive_count || 0}</TableCell>
                    <TableCell className="text-muted-foreground text-xs whitespace-nowrap">
                      {formatTime(mailbox.last_sync_at)}
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="xs"
                          disabled={Boolean(busy) || deleteBusy}
                          title={MAILBOX_MESSAGE_SYNC_HINT}
                          onClick={() => runMailboxMessageSync(mailbox)}
                        >
                          {busy === "sync" ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}
                          同步
                        </Button>
                        <Button
                          variant="ghost"
                          size="xs"
                          disabled={Boolean(busy) || deleteBusy}
                          title="获取该邮箱的最新验证码"
                          onClick={() => quickGetCode(mailbox)}
                        >
                          {busy === "code" ? <LoaderCircle className="animate-spin" /> : <KeyRound />}
                          取码
                        </Button>
                        <Button
                          variant="ghost"
                          size="xs"
                          disabled={Boolean(busy) || deleteBusy}
                          title="查看邮箱详情"
                          onClick={() => openMailboxFromRow(mailbox)}
                        >
                          {busy === "detail" ? <LoaderCircle className="animate-spin" /> : <MailOpen />}
                          详情
                        </Button>
                        <Button
                          variant="ghost"
                          size="xs"
                          className="text-destructive"
                          disabled={Boolean(busy) || deleteBusy}
                          title={
                            deleting
                              ? "正在清理已同步邮件并删除隐私邮箱"
                              : queued
                                ? "已加入彻底删除队列"
                                : "清理已同步的远端邮件后彻底删除隐私邮箱"
                          }
                          onClick={() => deleteMailbox(mailbox, false)}
                        >
                          {deleting || queued ? <LoaderCircle className="animate-spin" /> : <Trash2 />}
                          {deleting ? "删除中" : queued ? "排队中" : "删除"}
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>

        {/* 分页条 */}
        <div ref={paginationRef} className="flex flex-wrap items-center justify-between gap-2">
          <span className="text-muted-foreground text-xs">
            第 {result?.page ?? page} / {result?.total_pages ?? 1} 页 总 {result?.total ?? 0} 个邮箱
          </span>
          <div className="flex items-center gap-1">
            <Button
              variant="outline"
              size="icon-sm"
              disabled={mailboxesQ.isPending || page <= 1}
              title="跳转到首页"
              aria-label="跳转到首页"
              onClick={() => goToPage(1)}
            >
              <ChevronsLeft />
            </Button>
            <Button
              variant="outline"
              size="icon-sm"
              disabled={mailboxesQ.isPending || page <= 1}
              title="上一页"
              aria-label="上一页"
              onClick={() => goToPage(page - 1)}
            >
              <ChevronLeft />
            </Button>
            <Button
              variant="outline"
              size="icon-sm"
              disabled={mailboxesQ.isPending || page >= (result?.total_pages ?? 1)}
              title="下一页"
              aria-label="下一页"
              onClick={() => goToPage(page + 1)}
            >
              <ChevronRight />
            </Button>
            <Button
              variant="outline"
              size="icon-sm"
              disabled={mailboxesQ.isPending || page >= (result?.total_pages ?? 1)}
              title="跳转到末页"
              aria-label="跳转到末页"
              onClick={() => goToPage(result?.total_pages ?? 1)}
            >
              <ChevronsRight />
            </Button>
          </div>
        </div>
      </CardContent>

      {/* ── 导入已有隐私邮箱对话框 ── */}
      <Dialog open={showImport} onOpenChange={(o) => !o && setShowImport(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <MailPlus className="size-4" /> 导入已有隐私邮箱
            </DialogTitle>
            <DialogDescription>只创建或更新本地记录,不会在 Apple 服务器新建邮箱。</DialogDescription>
          </DialogHeader>
          <div className="grid gap-4">
            <div className="grid gap-2">
              <Label>绑定 Apple 账号</Label>
              <Select
                value={mailboxImport.account_id}
                onValueChange={(v) => setMailboxImport((p) => ({ ...p, account_id: v }))}
              >
                <SelectTrigger>
                  <SelectValue placeholder="请选择 Apple 账号" />
                </SelectTrigger>
                <SelectContent>
                  {accounts.map((a) => (
                    <SelectItem key={a.id} value={a.id}>
                      {a.label || a.apple_id || a.id}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-2">
              <Label>隐私邮箱地址</Label>
              <Input
                type="email"
                value={mailboxImport.email}
                onChange={(e) => setMailboxImport((p) => ({ ...p, email: e.target.value }))}
                placeholder="example@icloud.com"
                className="font-mono"
              />
            </div>
            <div className="grid gap-2">
              <Label>标签</Label>
              <Input
                value={mailboxImport.label}
                onChange={(e) => setMailboxImport((p) => ({ ...p, label: e.target.value }))}
                placeholder="例如:手动导入"
              />
            </div>
            <div className="grid gap-2">
              <Label>备注</Label>
              <Input
                value={mailboxImport.note}
                onChange={(e) => setMailboxImport((p) => ({ ...p, note: e.target.value }))}
                placeholder="可选"
              />
            </div>
          </div>
          <DialogFooter className="gap-2">
            <Button variant="outline" disabled={isBusy("import")} onClick={() => setShowImport(false)}>
              取消
            </Button>
            <Button
              disabled={isBusy("import") || !mailboxImport.account_id || !mailboxImport.email.trim()}
              onClick={importMailbox}
            >
              {isBusy("import") ? <LoaderCircle className="animate-spin" /> : <MailPlus />}
              保存本地邮箱
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ── 同步已有邮箱对话框 ── */}
      <Dialog open={showSync} onOpenChange={(o) => !o && setShowSync(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <CloudDownload className="size-4" /> 同步已有邮箱
            </DialogTitle>
            <DialogDescription>从所选 Apple 账号读取已有隐私邮箱,并更新到本地邮箱池。</DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label>同步范围</Label>
            <Select value={syncAccountID || "all"} onValueChange={(v) => setSyncAccountID(v === "all" ? "" : v)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部 Apple 账号</SelectItem>
                {accounts.map((a) => (
                  <SelectItem key={a.id} value={a.id}>
                    {`${a.label || a.apple_id || a.id}(${a.apple_id})`}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-muted-foreground text-xs">同步使用账号已保存的 iCloud Web 旧接口登录态。</p>
          </div>
          <DialogFooter className="gap-2">
            <Button variant="outline" disabled={syncingExisting} onClick={() => setShowSync(false)}>
              取消
            </Button>
            <Button disabled={syncingExisting} onClick={syncExistingMailboxes}>
              {syncingExisting ? <LoaderCircle className="animate-spin" /> : <CloudDownload />}
              {syncingExisting ? "同步中" : "开始同步"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ── 批量删除指定邮箱对话框 ── */}
      <Dialog open={showBulkDelete} onOpenChange={(o) => !o && closeBulkDeleteDialog()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Trash2 className="size-4" /> 批量删除指定邮箱
            </DialogTitle>
            <DialogDescription>
              一行输入一个邮箱。提交后会根据本地已同步邮件的远端标识逐个清理 Apple 邮件、清空所属账号的整个废纸篓,再删除
              Apple 隐私邮箱、本地邮件和邮箱记录。
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label>邮箱地址列表</Label>
            <Textarea
              value={bulkDeleteEmails}
              onChange={(e) => setBulkDeleteEmails(e.target.value)}
              placeholder={"example1@icloud.com\nexample2@icloud.com"}
              spellCheck={false}
              className="max-h-64 resize-none overflow-y-auto font-mono text-xs"
            />
            <p className="text-muted-foreground text-xs">已识别 {bulkDeleteEmailCount} 个邮箱;重复地址会自动合并。</p>
            {bulkDeleteError && (
              <p role="alert" className="text-destructive text-xs">
                {bulkDeleteError}
              </p>
            )}
          </div>
          <DialogFooter className="gap-2">
            <Button variant="outline" disabled={isBusy("bulk-delete-resolve")} onClick={closeBulkDeleteDialog}>
              取消
            </Button>
            <Button
              variant="destructive"
              disabled={isBusy("bulk-delete-resolve") || !bulkDeleteEmailCount}
              onClick={submitBulkDeleteEmails}
            >
              {isBusy("bulk-delete-resolve") ? <LoaderCircle className="animate-spin" /> : <Trash2 />}
              {isBusy("bulk-delete-resolve") ? "正在读取邮箱" : `开始彻底删除(${bulkDeleteEmailCount})`}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ── 快速编辑对话框(备注/状态) ── */}
      <Dialog open={quickEditOpen} onOpenChange={(o) => !o && closeQuickEdit()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{quickEditField === "note" ? "修改备注" : "修改邮箱状态"}</DialogTitle>
            <DialogDescription className="font-mono text-xs">{quickEditMailbox?.email || ""}</DialogDescription>
          </DialogHeader>
          {quickEditField === "note" ? (
            <div className="grid gap-2">
              <Label>备注</Label>
              <Textarea
                value={quickEdit.note}
                onChange={(e) => setQuickEdit((p) => ({ ...p, note: e.target.value }))}
                maxLength={1000}
                placeholder="请输入邮箱备注,留空可清除备注"
                className="max-h-48 resize-none overflow-y-auto"
              />
            </div>
          ) : (
            <div className="grid gap-2">
              <Label>邮箱状态</Label>
              <Select
                value={quickEdit.status}
                onValueChange={(v) => setQuickEdit((p) => ({ ...p, status: v }))}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="available">可用</SelectItem>
                  <SelectItem value="reserved" disabled>
                    已预留(由租约管理)
                  </SelectItem>
                  <SelectItem value="used">已使用</SelectItem>
                  <SelectItem value="active">活跃</SelectItem>
                  <SelectItem value="failed">失败</SelectItem>
                  <SelectItem value="disabled">已停用</SelectItem>
                </SelectContent>
              </Select>
            </div>
          )}
          <DialogFooter className="gap-2">
            <Button variant="outline" disabled={isBusy("quick-edit")} onClick={closeQuickEdit}>
              取消
            </Button>
            <Button disabled={isBusy("quick-edit")} onClick={saveQuickEdit}>
              {isBusy("quick-edit") && <LoaderCircle className="animate-spin" />}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ── 详情抽屉(居中 modal) ── */}
      <Dialog open={!!selected} onOpenChange={(o) => !o && setSelected(null)}>
        <DialogContent className="max-h-[calc(100vh-2.5rem)] overflow-y-auto sm:max-w-2xl">
          {selected && (
            <>
              <DialogHeader>
                <div className="flex flex-wrap items-center gap-1.5">
                  <Badge
                    variant={
                      selected.status === "available" || selected.status === "active"
                        ? "default"
                        : selected.status === "failed"
                          ? "destructive"
                          : selected.status === "disabled"
                            ? "outline"
                            : "secondary"
                    }
                    className="text-xs"
                  >
                    {statusLabel(selected.status)}
                  </Badge>
                  <ChannelBadges apiActive={selected.api_active} icloudActive={selected.icloud_active} />
                </div>
                <DialogTitle className="truncate font-mono text-base">{selected.email}</DialogTitle>
                <DialogDescription asChild>
                  <div className="space-y-0.5 text-left">
                    {selected.forward_to_email && (
                      <p className="truncate text-xs text-sky-500">转发主号:{selected.forward_to_email}</p>
                    )}
                    <p className="truncate font-mono text-[10px]">{selected.id}</p>
                    {selected.active_lease_id && (
                      <p className="truncate font-mono text-[10px] text-violet-500">
                        当前租约:{selected.active_lease_id}
                      </p>
                    )}
                  </div>
                </DialogDescription>
              </DialogHeader>

              <div className="space-y-3">
                {/* 同步/取码 */}
                <div className="grid grid-cols-2 gap-2">
                  <Button
                    variant="default"
                    size="sm"
                    disabled={isBusy("sync") || isMailboxDeleteBusy(selected.id)}
                    title={MAILBOX_MESSAGE_SYNC_HINT}
                    onClick={() => runMailboxMessageSync(selected, { detail: true })}
                  >
                    <RefreshCw className={isBusy("sync") ? "animate-spin" : undefined} /> 同步邮件
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={codeBusy || isMailboxDeleteBusy(selected.id)}
                    onClick={getCodeFromDetail}
                  >
                    {codeBusy && codeBusyVisible ? <LoaderCircle className="animate-spin" /> : <KeyRound />}
                    获取验证码
                  </Button>
                </div>

                {/* 状态与接收表单 */}
                <form
                  className="space-y-3 rounded-lg border p-3"
                  onSubmit={(e) => {
                    e.preventDefault()
                    void saveStatus()
                  }}
                >
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <h3 className="text-xs font-semibold">状态与接收</h3>
                    <div className="ml-auto flex items-center gap-2">
                      <div className="flex items-center gap-1.5">
                        <span className="text-muted-foreground text-[10px] font-semibold whitespace-nowrap">
                          使用状态
                        </span>
                        <Select
                          value={edit.status}
                          onValueChange={(v) => setEdit((p) => ({ ...p, status: v }))}
                        >
                          <SelectTrigger className="h-7 w-36 text-xs" size="sm">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {Object.entries(DELETE_STATUS_LABELS).map(([value, label]) => (
                              <SelectItem key={value} value={value} disabled={value === "reserved"}>
                                {label}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </div>
                      <Button
                        type="submit"
                        variant="secondary"
                        size="sm"
                        className="h-7 text-xs"
                        disabled={isBusy("save") || isMailboxDeleteBusy(selected.id)}
                      >
                        {isBusy("save") ? <LoaderCircle className="animate-spin" /> : <Save />}
                        保存
                      </Button>
                    </div>
                  </div>
                  <div className="grid gap-1.5">
                    <Label className="text-[11px]">备注</Label>
                    <Input
                      value={edit.note}
                      onChange={(e) => setEdit((p) => ({ ...p, note: e.target.value }))}
                      maxLength={1000}
                      placeholder="可选备注"
                    />
                  </div>
                  <div className="grid gap-2 sm:grid-cols-2">
                    <label className="flex items-center justify-between gap-3 rounded-md border p-2.5">
                      <span>
                        <strong className="block text-xs">公共取码 API</strong>
                        <small className="text-muted-foreground text-[10px]">控制外部接口取码</small>
                      </span>
                      <Switch
                        checked={edit.api_active}
                        onCheckedChange={(v) => setEdit((p) => ({ ...p, api_active: !!v }))}
                      />
                    </label>
                    <label className="flex items-center justify-between gap-3 rounded-md border p-2.5">
                      <span>
                        <strong className="block text-xs">iCloud 远端状态</strong>
                        <small className="text-muted-foreground text-[10px]">标记邮箱是否可收信</small>
                      </span>
                      <Switch
                        checked={edit.icloud_active}
                        onCheckedChange={(v) => setEdit((p) => ({ ...p, icloud_active: !!v }))}
                      />
                    </label>
                  </div>
                </form>

                {/* 本地邮件 */}
                <section className="space-y-2.5 rounded-lg border p-3">
                  <div className="flex items-start justify-between gap-3">
                    <h3 className="text-xs font-semibold">
                      本地邮件 <span className="text-muted-foreground ml-1">{messages.length}</span>
                    </h3>
                    <span className="text-muted-foreground text-[10px] whitespace-nowrap">
                      同步于 {formatTime(selected.last_sync_at)}
                    </span>
                  </div>
                  {!messages.length ? (
                    <div className="text-muted-foreground flex items-center justify-center py-6 text-xs">
                      暂无本地邮件
                    </div>
                  ) : (
                    <div className="max-h-64 space-y-1 overflow-y-auto">
                      {messages.map((item) => (
                        <button
                          key={item.id}
                          type="button"
                          className="hover:bg-muted/60 flex w-full items-center gap-2.5 rounded-md border px-2.5 py-2 text-left transition-colors"
                          title={`查看完整邮件:${item.subject || "无主题"}`}
                          onClick={() => openMessage(item)}
                        >
                          <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-sky-500/15 text-sky-600 dark:text-sky-400">
                            <MailOpen className="size-4" />
                          </span>
                          <span className="min-w-0 flex-1">
                            <strong className="block truncate text-xs">{item.subject || "无主题"}</strong>
                            <small className="text-muted-foreground mt-0.5 block truncate text-[10px]">
                              {item.from || "未知发件人"}
                            </small>
                          </span>
                          <span className="flex shrink-0 items-center gap-1.5">
                            <time className="text-muted-foreground text-[10px]">
                              {formatMessageTime(item.received_at)}
                            </time>
                            <ChevronRight className="text-muted-foreground size-3.5" />
                          </span>
                        </button>
                      ))}
                    </div>
                  )}
                </section>

                {/* 清理与删除 */}
                <section className="border-destructive/40 space-y-2.5 rounded-lg border p-3">
                  <div>
                    <h3 className="text-xs font-semibold">清理与删除</h3>
                    <p className="text-muted-foreground mt-0.5 text-[10px] leading-4">
                      彻底删除会精确清理本地已同步的远端邮件并清空所属账号废纸篓,再删除 Apple 隐私邮箱及本地记录。
                    </p>
                  </div>
                  <div className="grid gap-2 sm:grid-cols-2">
                    <label className="flex items-center justify-between gap-3 rounded-md border p-2.5">
                      <span>
                        <strong className="block text-xs">移动已同步邮件</strong>
                        <small className="text-muted-foreground text-[10px]">移入 Apple 废纸篓</small>
                      </span>
                      <Switch
                        checked={remoteClean.move_synced}
                        onCheckedChange={(v) => setRemoteClean((p) => ({ ...p, move_synced: !!v }))}
                      />
                    </label>
                    <label className="flex items-center justify-between gap-3 rounded-md border p-2.5">
                      <span>
                        <strong className="block text-xs">清空整个废纸篓</strong>
                        <small className="text-muted-foreground text-[10px]">
                          彻底清除该 Apple 账号的废纸篓邮件
                        </small>
                      </span>
                      <Switch
                        checked={remoteClean.empty_trash}
                        onCheckedChange={(v) => setRemoteClean((p) => ({ ...p, empty_trash: !!v }))}
                      />
                    </label>
                  </div>
                  <div className="grid gap-2 sm:grid-cols-2">
                    <Button
                      variant="secondary"
                      size="sm"
                      className="sm:col-span-2"
                      disabled={
                        isBusy("clean") ||
                        isMailboxDeleteBusy(selected.id) ||
                        (!remoteClean.move_synced && !remoteClean.empty_trash)
                      }
                      onClick={cleanRemote}
                    >
                      {isBusy("clean") ? <LoaderCircle className="animate-spin" /> : <CloudOff />}
                      清理 Apple 远端邮件
                    </Button>
                    <Button
                      variant="destructive"
                      size="sm"
                      disabled={isMailboxDeleteBusy(selected.id)}
                      onClick={() => deleteMailbox(selected, false)}
                    >
                      {isMailboxDeleteBusy(selected.id) ? (
                        <LoaderCircle className="animate-spin" />
                      ) : (
                        <Trash2 />
                      )}
                      {isMailboxDeleting(selected.id)
                        ? "删除中"
                        : isMailboxDeleteQueued(selected.id)
                          ? "排队中"
                          : "彻底删除"}
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={isMailboxDeleteBusy(selected.id)}
                      onClick={() => deleteMailbox(selected, true)}
                    >
                      {isMailboxDeleteBusy(selected.id) ? (
                        <LoaderCircle className="animate-spin" />
                      ) : (
                        <ShieldX />
                      )}
                      {isMailboxDeleting(selected.id)
                        ? "删除中"
                        : isMailboxDeleteQueued(selected.id)
                          ? "排队中"
                          : "只删本地"}
                    </Button>
                  </div>
                </section>
              </div>
            </>
          )}
        </DialogContent>
      </Dialog>

      {/* ── 取码弹窗(三态:取到/暂无/失败) ── */}
      <Dialog open={codeOpen} onOpenChange={(o) => !o && closeCodeDialog()}>
        <DialogContent>
          <DialogHeader>
            <div className="flex items-center gap-1.5">
              <Badge variant="default" className="text-xs">
                邮箱取码
              </Badge>
              {codeMailbox?.label && (
                <Badge variant="secondary" className="max-w-40 truncate text-xs">
                  {codeMailbox.label}
                </Badge>
              )}
            </div>
            <DialogTitle className="truncate font-mono text-base">
              {codeMailbox?.email || codeResult?.email || "获取验证码"}
            </DialogTitle>
            <DialogDescription>同步最新邮件并提取验证码</DialogDescription>
          </DialogHeader>

          {codeBusy ? (
            <div className="flex min-h-44 flex-col items-center justify-center gap-3 text-center">
              <span className="flex size-12 items-center justify-center rounded-2xl bg-emerald-500/10 text-emerald-600 dark:text-emerald-400">
                {codeBusyVisible ? (
                  <LoaderCircle className="size-6 animate-spin" />
                ) : (
                  <KeyRound className="size-6" />
                )}
              </span>
              <div>
                <strong className="block text-sm">正在获取验证码</strong>
                <span className="text-muted-foreground mt-1 block text-xs">
                  正在同步并检查最新邮件,请稍候……
                </span>
              </div>
            </div>
          ) : codeResult ? (
            <div className="space-y-4">
              <div className="rounded-2xl border border-emerald-500/30 bg-emerald-500/5 p-4 text-center">
                <div className="text-[11px] font-semibold text-emerald-600 dark:text-emerald-400">
                  最新验证码
                </div>
                <button
                  type="button"
                  className="mt-1 font-mono text-3xl font-bold tracking-widest text-emerald-600 hover:underline dark:text-emerald-300"
                  title="点击复制验证码"
                  aria-label={`复制验证码 ${codeResult.code}`}
                  onClick={copyCode}
                >
                  {codeResult.code}
                </button>
                <div
                  className="mt-2 truncate text-xs text-emerald-700/70 dark:text-emerald-300/70"
                  title={codeResult.subject}
                >
                  {codeResult.subject || "未提供邮件主题"}
                </div>
              </div>
              <div className="grid grid-cols-2 gap-2 text-xs">
                <div className="bg-muted/50 rounded-md px-3 py-2.5">
                  <span className="text-muted-foreground block text-[10px]">收件数量</span>
                  <strong className="mt-0.5 block">{codeMailbox?.receive_count || 0} 封</strong>
                </div>
                <div className="bg-muted/50 rounded-md px-3 py-2.5">
                  <span className="text-muted-foreground block text-[10px]">收件时间</span>
                  <strong className="mt-0.5 block truncate" title={formatTime(codeResult.received_at)}>
                    {formatTime(codeResult.received_at)}
                  </strong>
                </div>
              </div>
              <Button className="w-full" onClick={copyCode}>
                <Clipboard /> 复制验证码
              </Button>
            </div>
          ) : (
            <div className="flex min-h-44 flex-col items-center justify-center gap-3 text-center">
              <span className="bg-destructive/10 text-destructive flex size-12 items-center justify-center rounded-2xl">
                <KeyRound className="size-6" />
              </span>
              <div>
                <strong className="block text-sm">暂未获取到验证码</strong>
                <span className="text-muted-foreground mt-1 block max-w-sm text-xs leading-5">
                  {codeError || "请稍后重新取码。"}
                </span>
              </div>
              <Button variant="outline" onClick={closeCodeDialog}>
                关闭
              </Button>
            </div>
          )}
        </DialogContent>
      </Dialog>

      {/* ── 完整邮件弹窗 ── */}
      <Dialog open={!!selectedMessageID} onOpenChange={(o) => !o && closeSelectedMessage()}>
        <DialogContent className="flex max-h-[calc(100vh-2.5rem)] flex-col overflow-hidden sm:max-w-3xl">
          <DialogHeader>
            <div className="flex flex-wrap items-center gap-1.5">
              <Badge variant="default" className="text-xs">
                完整邮件
              </Badge>
              {selectedMessage?.source && (
                <Badge variant="secondary" className="text-xs">
                  {selectedMessage.source}
                </Badge>
              )}
              <Badge variant="outline" className="text-xs">
                {messageContentTypeLabel(selectedMessage)}
              </Badge>
            </div>
            <DialogTitle className="leading-6 break-words">
              {selectedMessage?.subject || "无主题"}
            </DialogTitle>
            <DialogDescription className="break-all">
              {selectedMessage?.from || "未知发件人"}
            </DialogDescription>
          </DialogHeader>
          <div className="text-muted-foreground flex items-center justify-between gap-3 border-b pb-2 text-[11px]">
            <span className="truncate">收件邮箱:{selected?.email}</span>
            <div className="flex shrink-0 items-center gap-3">
              {selectedMessageHasHTML && (
                <div className="flex overflow-hidden rounded-md border text-[10px]">
                  <button
                    type="button"
                    className={`px-2 py-1 ${messageViewMode !== "text" ? "bg-primary text-primary-foreground" : ""}`}
                    onClick={() => setMessageViewMode("html")}
                  >
                    邮件视图
                  </button>
                  <button
                    type="button"
                    className={`px-2 py-1 ${messageViewMode === "text" ? "bg-primary text-primary-foreground" : ""}`}
                    onClick={() => setMessageViewMode("text")}
                  >
                    纯文本
                  </button>
                </div>
              )}
              <time>{formatTime(selectedMessage?.received_at)}</time>
            </div>
          </div>
          <div className="bg-muted/30 relative min-h-64 flex-1 overflow-hidden rounded-md">
            {messageDetailQ.isFetching && (
              <div className="text-muted-foreground absolute inset-0 z-10 flex items-center justify-center gap-2 text-xs">
                <LoaderCircle className="size-5 animate-spin" />
                <span>正在加载完整邮件</span>
              </div>
            )}
            {showSelectedMessageHTML ? (
              <iframe
                className="h-[60vh] w-full border-0 bg-white"
                srcDoc={selectedMessageHTMLDocument}
                sandbox="allow-popups allow-popups-to-escape-sandbox"
                title="HTML 邮件正文"
              />
            ) : (
              <pre className="h-[60vh] overflow-y-auto p-4 text-sm whitespace-pre-wrap">
                {selectedMessage?.body || "这封邮件没有正文内容。"}
              </pre>
            )}
          </div>
        </DialogContent>
      </Dialog>

      {/* ── 确认对话框(alert-dialog,danger) ── */}
      <AlertDialog open={!!confirmState} onOpenChange={(o) => !o && closeConfirm(false)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirmState?.title}</AlertDialogTitle>
            <AlertDialogDescription>{confirmState?.message}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => closeConfirm(false)}>取消</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => closeConfirm(true)}>
              {confirmState?.confirmText}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  )
}
