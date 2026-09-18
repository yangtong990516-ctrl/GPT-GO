// Apple 账号 tab:账号列表 + 添加(两步登录)/检测/IMAP/同步/创建邮箱/删除。
import * as React from "react"
import { toast } from "sonner"
import {
  Apple,
  CheckCircle2,
  CloudDownload,
  Eye,
  EyeOff,
  KeyRound,
  Loader2,
  Mail,
  MailPlus,
  Plus,
  RefreshCw,
  Server,
  ShieldCheck,
  Trash2,
} from "lucide-react"
import { HttpError } from "@/lib/api"
import type { ICloudAppleAccount, ICloudLoginStateSummary } from "@/lib/icloud-api"
import {
  useICloudAppleAccounts,
  useICloudAppleCheck,
  useICloudAppleLogin2FA,
  useICloudAppleLoginStart,
  useICloudAppleSaveIMAP,
  useICloudCreateMailbox,
  useICloudDeleteAppleAccount,
  useICloudSyncMailboxes,
} from "@/lib/icloud-queries"
import { TableSkeleton } from "@/components/data-table"
import { Button } from "@/components/ui/button"
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

// ── 标签映射 ────────────────────────────────────────────────────────────────────

function statusLabel(value: string): string {
  return (
    {
      active: "正常",
      partial: "部分正常",
      need_login: "需要登录",
      need_2fa: "等待 2FA",
      no_icloud_plus: "无 iCloud+",
      rate_limited: "访问受限",
      failed: "失败",
    } as Record<string, string>
  )[value] || value || "未知"
}

function accountStatusClass(account: ICloudAppleAccount): string {
  const status = account.icloud_status || account.status
  if (status === "active")
    return "bg-emerald-50 text-emerald-600 dark:bg-emerald-950/30 dark:text-emerald-300"
  if (status === "partial" || status === "need_2fa" || status === "need_login")
    return "bg-amber-50 text-amber-600 dark:bg-amber-950/30 dark:text-amber-300"
  return "bg-rose-50 text-rose-600 dark:bg-rose-950/30 dark:text-rose-300"
}

function stateLabel(kind: string): string {
  return (
    {
      apple_account: "Apple Account 新接口",
      icloud_web: "iCloud Web 旧接口",
      icloud_imap: "iCloud IMAP",
    } as Record<string, string>
  )[kind] || kind
}

interface StateMeta {
  description: string
  icon: React.ComponentType<{ className?: string }>
  tone: string
}

function stateMeta(kind: string): StateMeta {
  return (
    ({
      apple_account: {
        description: "创建隐私邮箱",
        icon: Apple,
        tone: "text-violet-600 bg-violet-50 dark:bg-violet-950/30 dark:text-violet-300",
      },
      icloud_web: {
        description: "同步与远端管理",
        icon: CloudDownload,
        tone: "text-sky-600 bg-sky-50 dark:bg-sky-950/30 dark:text-sky-300",
      },
      icloud_imap: {
        description: "邮件与验证码",
        icon: Mail,
        tone: "text-amber-600 bg-amber-50 dark:bg-amber-950/30 dark:text-amber-300",
      },
    } as Record<string, StateMeta>)[kind] || {
      description: "登录态",
      icon: Server,
      tone: "text-slate-500 bg-slate-100 dark:bg-slate-700 dark:text-slate-300",
    }
  )
}

function stateStatusLabel(state: ICloudLoginStateSummary): string {
  if (!state.saved) return "未配置"
  if (!state.last_checked_at || String(state.last_checked_at).startsWith("0001-"))
    return "已保存"
  return state.last_check_ok ? "正常" : "需检查"
}

function stateStatusClass(state: ICloudLoginStateSummary): string {
  if (!state.saved)
    return "bg-slate-100 text-slate-400 dark:bg-slate-700 dark:text-slate-400"
  if (!state.last_checked_at || String(state.last_checked_at).startsWith("0001-"))
    return "bg-amber-50 text-amber-600 dark:bg-amber-950/30 dark:text-amber-300"
  return state.last_check_ok
    ? "bg-emerald-50 text-emerald-600 dark:bg-emerald-950/30 dark:text-emerald-300"
    : "bg-rose-50 text-rose-600 dark:bg-rose-950/30 dark:text-rose-300"
}

// ── busy key 管理 ─────────────────────────────────────────────────────────────

type BusyKey =
  | "login"
  | "2fa"
  | "check"
  | "imap"
  | "create"
  | "sync"
  | `delete:${string}`
  | `detail:${string}`

function useBusyActions() {
  const [busy, setBusy] = React.useState<BusyKey[]>([])
  const isBusy = React.useCallback((key: BusyKey) => busy.includes(key), [busy])
  const startBusy = React.useCallback(
    (key: BusyKey) => {
      if (busy.includes(key)) return false
      setBusy((prev) => [...prev, key])
      return true
    },
    [busy],
  )
  const finishBusy = React.useCallback((key: BusyKey) => {
    setBusy((prev) => prev.filter((k) => k !== key))
  }, [])
  return { isBusy, startBusy, finishBusy }
}

// ── 添加 Apple 账号(两步:登录 → 2FA) ─────────────────────────────────────────

function AddAccountDialog({
  open,
  onOpenChange,
  selectedAccount,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  selectedAccount: ICloudAppleAccount | null
}) {
  const loginStart = useICloudAppleLoginStart()
  const login2fa = useICloudAppleLogin2FA()
  const [flow, setFlow] = React.useState("apple_account")
  const [appleId, setAppleId] = React.useState("")
  const [password, setPassword] = React.useState("")
  const [tfMethod, setTfMethod] = React.useState("trusted_device")
  const [pendingId, setPendingId] = React.useState<string | null>(null)
  const [code, setCode] = React.useState("")
  const [phoneNumber, setPhoneNumber] = React.useState("")
  const [showPassword, setShowPassword] = React.useState(true)

  React.useEffect(() => {
    if (open) {
      setFlow("apple_account")
      setAppleId(selectedAccount?.apple_id || "")
      setPassword("")
      setTfMethod("trusted_device")
      setPendingId(null)
      setCode("")
      setPhoneNumber("")
      setShowPassword(true)
    }
  }, [open, selectedAccount])

  const submitStart = () => {
    if (loginStart.isPending) return
    toast("正在与 Apple 建立登录态，请稍候…")
    loginStart.mutate(
      { flow, apple_id: appleId.trim(), password, two_factor_method: tfMethod },
      {
        onSuccess: (r) => {
          if (r.needs_2fa) {
            setPendingId(String(r.pending_id ?? ""))
            toast(String(r.message || "请输入 Apple 两步验证码"))
          } else {
            toast.success(String(r.message || "Apple 登录已完成"))
            onOpenChange(false)
          }
        },
        onError: (e) => toast.error(errMsg(e, "登录失败")),
      },
    )
  }

  const submit2fa = () => {
    if (!pendingId || login2fa.isPending) return
    let phone: unknown = undefined
    if (phoneNumber.trim()) {
      try {
        phone = JSON.parse(phoneNumber)
      } catch {
        phone = phoneNumber.trim()
      }
    }
    login2fa.mutate(
      { pending_id: pendingId, code: code.trim(), phone_number: phone },
      {
        onSuccess: (r) => {
          toast.success(String(r?.message || "Apple 登录和 2FA 已完成"))
          onOpenChange(false)
        },
        onError: (e) => toast.error(errMsg(e, "验证失败")),
      },
    )
  }

  const busy = loginStart.isPending || login2fa.isPending

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Apple className="size-5" />
            登录 Apple 账号
          </DialogTitle>
          <DialogDescription>
            登录凭据仅用于本次 Apple 协议请求；保存的是本地登录态。
          </DialogDescription>
        </DialogHeader>
        {!pendingId ? (
          <div className="grid gap-4">
            <div className="grid gap-2">
              <Label>登录通道</Label>
              <Select value={flow} onValueChange={setFlow}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="apple_account">Apple Account 新接口</SelectItem>
                  <SelectItem value="icloud_web">iCloud Web 旧接口</SelectItem>
                </SelectContent>
              </Select>
              <p className="text-muted-foreground text-xs">
                新接口用于创建；旧接口支持同步、删除和 Web 收信。
              </p>
            </div>
            <div className="grid gap-2">
              <Label>两步验证方式</Label>
              <Select value={tfMethod} onValueChange={setTfMethod}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="trusted_device">受信任设备</SelectItem>
                  <SelectItem value="sms">短信</SelectItem>
                </SelectContent>
              </Select>
              <p className="text-muted-foreground text-xs">
                优先使用受信任设备弹出的验证码。
              </p>
            </div>
            <div className="grid gap-2">
              <Label>Apple ID</Label>
              <div className="relative">
                <Mail className="text-muted-foreground absolute left-2.5 top-1/2 size-4 -translate-y-1/2" />
                <Input
                  value={appleId}
                  onChange={(e) => setAppleId(e.target.value)}
                  placeholder="name@example.com"
                  autoComplete="username"
                  className="pl-8"
                />
              </div>
            </div>
            <div className="grid gap-2">
              <Label>Apple ID 密码</Label>
              <div className="relative">
                <KeyRound className="text-muted-foreground absolute left-2.5 top-1/2 size-4 -translate-y-1/2" />
                <Input
                  type={showPassword ? "text" : "password"}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="输入 Apple ID 密码"
                  autoComplete="current-password"
                  className="pl-8 pr-9"
                />
                <button
                  type="button"
                  className="text-muted-foreground hover:text-foreground absolute right-2 top-1/2 -translate-y-1/2"
                  title={showPassword ? "隐藏密码" : "显示密码"}
                  onClick={() => setShowPassword(!showPassword)}
                >
                  {showPassword ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                </button>
              </div>
            </div>
          </div>
        ) : (
          <div className="grid gap-4">
            <div className="grid gap-2">
              <Label>Apple 验证码</Label>
              <Input
                value={code}
                onChange={(e) => setCode(e.target.value)}
                placeholder="000000"
                className="font-mono tracking-[0.3em]"
                inputMode="numeric"
                maxLength={8}
              />
              <p className="text-muted-foreground text-xs">
                输入受信任设备或短信收到的验证码。
              </p>
            </div>
            <div className="grid gap-2">
              <Label>短信号码参数（可选）</Label>
              <Input
                value={phoneNumber}
                onChange={(e) => setPhoneNumber(e.target.value)}
                placeholder='例如 {"id":1}'
              />
              <p className="text-muted-foreground text-xs">
                只有短信流程要求选择号码时才填写。
              </p>
            </div>
          </div>
        )}
        <DialogFooter className="gap-2">
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            取消
          </Button>
          {!pendingId ? (
            <Button
              onClick={submitStart}
              disabled={!appleId.trim() || !password || loginStart.isPending}
            >
              {loginStart.isPending ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <ShieldCheck className="size-4" />
              )}
              开始登录
            </Button>
          ) : (
            <Button onClick={submit2fa} disabled={!code.trim() || login2fa.isPending}>
              {login2fa.isPending ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <CheckCircle2 className="size-4" />
              )}
              提交验证码
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ── IMAP 取码对话框 ───────────────────────────────────────────────────────────

function ImapDialog({
  account,
  open,
  onOpenChange,
}: {
  account: ICloudAppleAccount | null
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const saveImap = useICloudAppleSaveIMAP()
  const [email, setEmail] = React.useState("")
  const [appPassword, setAppPassword] = React.useState("")
  const [showIMAPPassword, setShowIMAPPassword] = React.useState(true)

  React.useEffect(() => {
    if (open && account) {
      setEmail(account.imap_email || account.apple_id || "")
      setAppPassword(account.imap_app_password || "")
      setShowIMAPPassword(true)
    }
  }, [open, account])

  if (!account) return null

  const busy = saveImap.isPending

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <KeyRound className="size-5" />
            IMAP 取码
          </DialogTitle>
          <DialogDescription>
            当前账号：{account.label || account.apple_id}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <div className="grid gap-2">
            <Label>iCloud 邮箱</Label>
            <Input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="name@icloud.com"
            />
          </div>
          <div className="grid gap-2">
            <Label>App 专用密码</Label>
            <div className="relative">
              <KeyRound className="text-muted-foreground absolute left-2.5 top-1/2 size-4 -translate-y-1/2" />
              <Input
                type={showIMAPPassword ? "text" : "password"}
                value={appPassword}
                onChange={(e) => setAppPassword(e.target.value)}
                placeholder="xxxx-xxxx-xxxx-xxxx"
                className="pl-8 pr-9"
              />
              <button
                type="button"
                className="text-muted-foreground hover:text-foreground absolute right-2 top-1/2 -translate-y-1/2"
                title={showIMAPPassword ? "隐藏 App 专用密码" : "显示 App 专用密码"}
                onClick={() => setShowIMAPPassword(!showIMAPPassword)}
              >
                {showIMAPPassword ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
              </button>
            </div>
            <p className="text-muted-foreground text-xs">
              保存前会连接 imap.mail.me.com 验证。
            </p>
          </div>
        </div>
        <DialogFooter className="gap-2">
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button
            disabled={!email.trim() || !appPassword.trim() || busy}
            onClick={() =>
              saveImap.mutate(
                { id: account.id, email: email.trim(), app_password: appPassword.trim() },
                {
                  onSuccess: () => {
                    toast.success("IMAP App 专用密码已验证并保存")
                    onOpenChange(false)
                  },
                  onError: (e) => toast.error(errMsg(e, "保存失败")),
                },
              )
            }
          >
            {busy ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <ShieldCheck className="size-4" />
            )}
            验证并保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ── 创建隐私邮箱对话框 ─────────────────────────────────────────────────────────

function CreateMailboxDialog({
  account,
  open,
  onOpenChange,
  onSync,
  syncBusy,
}: {
  account: ICloudAppleAccount | null
  open: boolean
  onOpenChange: (o: boolean) => void
  onSync: () => void
  syncBusy: boolean
}) {
  const createMailbox = useICloudCreateMailbox()
  const [label, setLabel] = React.useState("")
  const [note, setNote] = React.useState("")
  const [channel, setChannel] = React.useState("auto")

  React.useEffect(() => {
    if (open) {
      setLabel("")
      setNote("")
      setChannel("auto")
    }
  }, [open])

  if (!account) return null

  const busy = createMailbox.isPending

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && !syncBusy && onOpenChange(o)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <MailPlus className="size-5" />
            创建隐私邮箱
          </DialogTitle>
          <DialogDescription>
            当前账号：{account.label || account.apple_id}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
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
          <div className="grid grid-cols-2 gap-3">
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
          <p className="text-muted-foreground text-xs">
            标签留空时默认使用 x，并自动生成连续编号。
          </p>
        </div>
        <DialogFooter className="gap-2">
          <Button variant="outline" disabled={syncBusy} onClick={onSync}>
            {syncBusy ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <CloudDownload className="size-4" />
            )}
            {syncBusy ? "同步中" : "同步已有"}
          </Button>
          <Button
            disabled={busy}
            onClick={() =>
              createMailbox.mutate(
                { id: account.id, label: label.trim(), note: note.trim(), channel },
                {
                  onSuccess: (r) => {
                    toast.success(`已创建隐私邮箱：${r.mailbox.email}`)
                    onOpenChange(false)
                  },
                  onError: (e) => toast.error(errMsg(e, "创建失败")),
                },
              )
            }
          >
            {busy ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <Plus className="size-4" />
            )}
            {busy ? "创建中" : "创建邮箱"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ── 主 tab ────────────────────────────────────────────────────────────────────

export function AccountsTab({ enabled }: { enabled: boolean }) {
  const accounts = useICloudAppleAccounts(enabled)
  const check = useICloudAppleCheck()
  const syncMailboxes = useICloudSyncMailboxes()
  const deleteAccount = useICloudDeleteAppleAccount()

  const [selected, setSelected] = React.useState<ICloudAppleAccount | null>(null)
  const [addOpen, setAddOpen] = React.useState(false)
  const [imapOpen, setImapOpen] = React.useState(false)
  const [createOpen, setCreateOpen] = React.useState(false)
  const [deleteTarget, setDeleteTarget] = React.useState<ICloudAppleAccount | null>(null)

  const { isBusy, startBusy, finishBusy } = useBusyActions()

  const items = accounts.data?.items ?? []

  // 同步 selected 与最新 items
  React.useEffect(() => {
    if (selected) {
      const current = items.find((item) => item.id === selected.id)
      if (current) {
        setSelected((prev) => (prev ? { ...current } : null))
      } else {
        setSelected(null)
      }
    }
  }, [items, selected?.id])

  const selectAccount = (account: ICloudAppleAccount) => {
    const key: BusyKey = `detail:${account.id}`
    if (!startBusy(key)) return
    // 模拟 GET /apple-accounts/{id} 获取详情
    // 实际数据已在列表中，直接选中并预填 IMAP
    setSelected({
      ...account,
      imap_email: account.imap_email || account.apple_id || "",
      imap_app_password: account.imap_app_password || "",
    })
    finishBusy(key)
  }

  const openLoginDialog = () => {
    setImapOpen(false)
    setCreateOpen(false)
    setAddOpen(true)
  }

  const openIMAPDialog = () => {
    if (!selected) {
      toast.error("请先选择一个 Apple 账号")
      return
    }
    setAddOpen(false)
    setCreateOpen(false)
    setImapOpen(true)
  }

  const openCreateDialog = () => {
    if (!selected) {
      toast.error("请先选择一个 Apple 账号")
      return
    }
    setAddOpen(false)
    setImapOpen(false)
    setCreateOpen(true)
  }

  const handleCheck = () => {
    if (!selected || check.isPending) return
    check.mutate(selected.id, {
      onSuccess: (r) => {
        setSelected(r.account)
        toast.success("登录态检测完成")
      },
      onError: (e) => toast.error(errMsg(e, "检测失败")),
    })
  }

  const handleSync = () => {
    if (!selected || syncMailboxes.isPending) return
    syncMailboxes.mutate(selected.id, {
      onSuccess: (r) => {
        setCreateOpen(false)
        toast.success(`已从 Apple 同步 ${r.count} 个隐私邮箱`)
      },
      onError: (e) => toast.error(errMsg(e, "同步失败")),
    })
  }

  const handleDelete = (account: ICloudAppleAccount) => {
    setDeleteTarget(account)
  }

  const confirmDelete = () => {
    if (!deleteTarget || deleteAccount.isPending) return
    const key: BusyKey = `delete:${deleteTarget.id}`
    startBusy(key)
    deleteAccount.mutate(deleteTarget.id, {
      onSuccess: (data) => {
        const name = deleteTarget.label || deleteTarget.apple_id || deleteTarget.id
        if (selected?.id === deleteTarget.id) setSelected(null)
        const deleted = (data as Record<string, unknown>)?.deleted as Record<string, unknown> | undefined
        toast.success(
          `已删除 Apple 账号：${name}；清理邮箱 ${deleted?.mailboxes || 0} 个，邮件 ${deleted?.messages || 0} 封`,
        )
        setDeleteTarget(null)
        finishBusy(key)
      },
      onError: (e) => {
        toast.error(e instanceof HttpError ? e.message : "删除失败")
        finishBusy(key)
      },
    })
  }

  return (
    <div className="space-y-4">
      {/* 顶部命令栏 */}
      <Card>
        <CardHeader className="pb-3">
          <div className="flex items-start justify-between gap-3">
            <div>
              <CardTitle className="flex items-center gap-2 text-base">
                <Apple className="size-4" />
                Apple 账号
              </CardTitle>
              <CardDescription>管理登录态、IMAP 与隐私邮箱通道</CardDescription>
            </div>
            <div className="flex gap-2">
              <Button size="sm" onClick={openLoginDialog}>
                <Plus /> 添加 Apple 账号
              </Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={!selected}
                title={selected ? `为 ${selected.apple_id} 配置 IMAP` : "请先选择一个 Apple 账号"}
                onClick={openIMAPDialog}
              >
                <KeyRound /> IMAP 取码
              </Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={!selected}
                title={selected ? `使用 ${selected.apple_id} 创建隐私邮箱` : "请先选择一个 Apple 账号"}
                onClick={openCreateDialog}
              >
                <MailPlus /> 创建隐私邮箱
              </Button>
            </div>
          </div>
        </CardHeader>
      </Card>

      {/* 左右分栏 */}
      {accounts.isPending && enabled ? (
        <Card>
          <CardContent className="p-4">
            <TableSkeleton rows={5} cols={4} />
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-4 lg:grid-cols-[1fr_320px]">
          {/* 左侧账号列表 */}
          <Card>
            <CardHeader className="pb-3">
              <div className="flex items-center justify-between">
                <div>
                  <CardTitle className="text-sm">账号与登录态</CardTitle>
                  <CardDescription>选择账号后可在右侧查看各通道状态</CardDescription>
                </div>
                <span className="text-muted-foreground text-sm">{items.length} 个账号</span>
              </div>
            </CardHeader>
            <CardContent className="overflow-x-auto">
              {items.length === 0 ? (
                <div className="flex flex-col items-center justify-center gap-2 py-10 text-center">
                  <Server className="text-muted-foreground size-5" />
                  <strong className="text-sm">还没有 Apple 账号</strong>
                  <p className="text-muted-foreground text-xs">
                    点击"添加 Apple 账号"完成首次协议登录。
                  </p>
                </div>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Apple 账号</TableHead>
                      <TableHead>状态</TableHead>
                      <TableHead>登录通道</TableHead>
                      <TableHead className="w-16 text-right">操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {items.map((a) => (
                      <TableRow
                        key={a.id}
                        className={`cursor-pointer ${selected?.id === a.id ? "bg-muted/50" : ""}`}
                        tabIndex={0}
                        aria-label={`查看 ${a.label || a.apple_id || "Apple 账号"} 的登录态详情`}
                        onClick={() => selectAccount(a)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter" || e.key === " ") {
                            e.preventDefault()
                            selectAccount(a)
                          }
                        }}
                      >
                        <TableCell>
                          <div className="flex items-center gap-2">
                            <span className="flex size-7 items-center justify-center rounded-md bg-muted">
                              {isBusy(`detail:${a.id}`) ? (
                                <Loader2 className="size-3.5 animate-spin" />
                              ) : (
                                <Apple className="size-3.5" />
                              )}
                            </span>
                            <div>
                              <div className="font-medium text-sm">
                                {a.label || a.apple_id || "Apple 账号"}
                              </div>
                              <div className="text-muted-foreground text-xs">{a.apple_id}</div>
                            </div>
                          </div>
                        </TableCell>
                        <TableCell>
                          <span
                            className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${accountStatusClass(a)}`}
                          >
                            {statusLabel(a.icloud_status || a.status)}
                          </span>
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-wrap gap-1">
                            {(a.login_states ?? []).length === 0 && (
                              <span className="text-muted-foreground text-xs">暂无登录态</span>
                            )}
                            {(a.login_states ?? []).map((s) => (
                              <span
                                key={s.kind}
                                className="inline-flex items-center gap-1 rounded-full border px-1.5 py-0.5 text-xs"
                              >
                                {(() => {
                                  const MetaIcon = stateMeta(s.kind).icon
                                  return <MetaIcon className="size-3" />
                                })()}
                                <span>{stateLabel(s.kind)}</span>
                                <em
                                  className={`not-italic rounded-full px-1.5 py-0.5 text-[10px] font-medium ${stateStatusClass(s)}`}
                                >
                                  {stateStatusLabel(s)}
                                </em>
                              </span>
                            ))}
                          </div>
                        </TableCell>
                        <TableCell className="text-right">
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            title={`删除 ${a.label || a.apple_id || "Apple 账号"}`}
                            aria-label={`删除 ${a.label || a.apple_id || "Apple 账号"}`}
                            disabled={isBusy(`delete:${a.id}`)}
                            onClick={(e) => {
                              e.stopPropagation()
                              handleDelete(a)
                            }}
                          >
                            {isBusy(`delete:${a.id}`) ? (
                              <Loader2 className="size-3 animate-spin" />
                            ) : (
                              <Trash2 className="size-3" />
                            )}
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>

          {/* 右侧详情面板 */}
          <Card>
            <CardContent className="p-4">
              {!selected ? (
                <div className="flex flex-col items-center justify-center gap-2 py-16 text-center">
                  <Apple className="text-muted-foreground size-5" />
                  <strong className="text-sm">选择一个 Apple 账号</strong>
                  <p className="text-muted-foreground text-xs">
                    登录态详情和检测结果会显示在这里。
                  </p>
                </div>
              ) : (
                <div className="space-y-4">
                  {/* header */}
                  <div className="flex items-start justify-between gap-2">
                    <div>
                      <span className="text-muted-foreground text-xs">所选 Apple 账号</span>
                      <h3 className="font-medium text-sm">
                        {selected.label || selected.apple_id}
                      </h3>
                      <p className="text-muted-foreground text-xs">{selected.apple_id}</p>
                    </div>
                    <Button
                      variant="secondary"
                      size="sm"
                      disabled={check.isPending}
                      onClick={handleCheck}
                    >
                      {check.isPending ? (
                        <Loader2 className="size-3 animate-spin" />
                      ) : (
                        <RefreshCw className="size-3" />
                      )}
                      {check.isPending ? "检查中" : "检测登录态"}
                    </Button>
                  </div>

                  {/* 登录态列表 */}
                  <div className="space-y-2">
                    {(selected.login_states ?? []).map((s) => (
                      <div
                        key={s.kind}
                        className="flex items-center gap-3 rounded-lg border p-3"
                      >
                        <span
                          className={`flex size-8 items-center justify-center rounded-md ${stateMeta(s.kind).tone}`}
                        >
                          {(() => {
                            const MetaIcon = stateMeta(s.kind).icon
                            return <MetaIcon className="size-4" />
                          })()}
                        </span>
                        <div className="min-w-0 flex-1">
                          <div className="font-medium text-sm">{stateLabel(s.kind)}</div>
                          <div className="text-muted-foreground truncate text-xs">
                            {check.isPending
                              ? `正在检查 ${stateLabel(s.kind)} 登录态…`
                              : s.last_status_message || stateMeta(s.kind).description}
                          </div>
                        </div>
                        {check.isPending ? (
                          <span className="text-muted-foreground inline-flex items-center gap-1 text-xs">
                            <Loader2 className="size-2.5 animate-spin" />
                            检查中
                          </span>
                        ) : (
                          <span
                            className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${stateStatusClass(s)}`}
                          >
                            {stateStatusLabel(s)}
                          </span>
                        )}
                      </div>
                    ))}
                    {(selected.login_states ?? []).length === 0 && (
                      <div className="text-muted-foreground py-6 text-center text-xs">
                        该账号还没有已保存的登录态
                      </div>
                    )}
                  </div>
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      )}

      {/* 对话框 */}
      <AddAccountDialog open={addOpen} onOpenChange={setAddOpen} selectedAccount={selected} />
      <ImapDialog account={selected} open={imapOpen} onOpenChange={setImapOpen} />
      <CreateMailboxDialog
        account={selected}
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSync={handleSync}
        syncBusy={syncMailboxes.isPending}
      />

      {/* 删除确认 */}
      <AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除 Apple 账号</AlertDialogTitle>
            <AlertDialogDescription>
              确定删除"{deleteTarget?.label || deleteTarget?.apple_id || deleteTarget?.id}"吗？
              <br />
              <br />
              本地登录态、关联隐私邮箱和本地邮件会一并删除；Apple 服务器上的隐私邮箱不会删除。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={deleteAccount.isPending}
              onClick={confirmDelete}
            >
              {deleteAccount.isPending ? (
                <Loader2 className="size-4 animate-spin" />
              ) : null}
              删除账号
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
