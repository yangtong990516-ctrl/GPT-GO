import * as React from "react"
import { useNavigate } from "react-router-dom"
import { toast } from "sonner"
import { Copy, CreditCard, Eye, Gift, HeartPulse, KeyRound, Link2, Loader2, MailQuestion, MoreHorizontal, Plus, RefreshCw, ShieldCheck, Trash2 } from "lucide-react"
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
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import {
  useAccounts,
  useBulkDeleteAccounts,
  useBulkEnsure2FA,
  useBulkHealAccounts,
  useCheck2FAAccounts,
  useCheckCombinedAccounts,
  useCreateAccount,
  useEnsure2FA,
  useHealAccount,
} from "@/lib/queries"
import type { AccountRecord } from "@/lib/types"
import { HttpError } from "@/lib/api"
import { campaignLabel, countryLabel, formatTime } from "@/lib/format"

// ── Cell renderers ────────────────────────────────────────────────────────────

function AccountTypeBadge({ type }: { type: string }) {
  return type === "plus" ? (
    <Badge className="border-amber-500/40 bg-amber-500/10 text-amber-600 dark:text-amber-400">
      Plus
    </Badge>
  ) : (
    <Badge variant="secondary">Free</Badge>
  )
}

function TotpCell({ record }: { record: AccountRecord }) {
  if (record.totpStatus === "missing") {
    return <Badge variant="destructive">缺失</Badge>
  }
  return record.totpSecretConfigured ? (
    <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
      已启用
    </Badge>
  ) : (
    <Badge variant="outline">未配置</Badge>
  )
}

function AliveBadge({ status }: { status: string | null }) {
  switch (status) {
    case "alive":
      return (
        <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
          存活
        </Badge>
      )
    case "dead":
      return <Badge variant="destructive">已失效</Badge>
    case "running":
      return <Badge variant="secondary">检测中</Badge>
    case "unknown":
      return <Badge variant="outline">异常</Badge>
    default:
      return <Badge variant="outline">未检测</Badge>
  }
}

function PromotionCell({ record }: { record: AccountRecord }) {
  if (record.planCheckStatus === "running") {
    return <Badge variant="secondary">查询中…</Badge>
  }
  if (record.planCheckStatus === "failed") {
    return <Badge variant="destructive">查询失败</Badge>
  }
  const campaigns = record.promotionCampaigns ?? []
  if (campaigns.length > 1) {
    return <Badge variant="secondary">可试用 ×{campaigns.length}</Badge>
  }
  if (campaigns.length === 1) {
    return <Badge variant="secondary">{campaignLabel(campaigns[0].id)}</Badge>
  }
  if (record.promotionEligible === true) {
    return (
      <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
        有优惠
      </Badge>
    )
  }
  if (record.promotionEligible === false) {
    return <Badge variant="outline">不可试用</Badge>
  }
  return <Badge variant="outline">未查询</Badge>
}

const PAYMENT_METHOD_LABELS: Record<string, string> = {
  card: "银行卡",
  paypal: "PayPal",
  momo: "MoMo",
  gcash: "GCash",
  gopay: "GoPay",
  grabpay: "GrabPay",
}

const PAYMENT_STATUS_LABELS: Record<string, string> = {
  available: "已返回",
  partial: "部分返回",
  not_returned: "未返回",
  already_paid: "已是付费账号",
  token_invalid: "Token 失效",
  risk_blocked: "风控拦截",
  rate_limited: "请求限流",
  running: "检测中",
}

// PaymentCell 渲染「支付渠道」列：有渠道显示标签（0 元渠道高亮），无渠道回退状态文本。
function PaymentCell({ record }: { record: AccountRecord }) {
  if (record.paymentStatus === "running") {
    return <Badge variant="secondary">检测中…</Badge>
  }
  const methods = record.paymentMethods ?? []
  const zero = new Set(record.paymentZeroMethods ?? [])
  if (methods.length > 0) {
    return (
      <div className="flex flex-wrap gap-1">
        {methods.slice(0, 3).map((m) => (
          <Badge
            key={m}
            variant={zero.has(m) ? "default" : "outline"}
            className="text-xs"
          >
            {PAYMENT_METHOD_LABELS[m] ?? m}
            {zero.has(m) ? "·0元" : ""}
          </Badge>
        ))}
        {methods.length > 3 && (
          <Badge variant="outline" className="text-xs">
            +{methods.length - 3}
          </Badge>
        )}
      </div>
    )
  }
  if (record.paymentStatus) {
    const label = PAYMENT_STATUS_LABELS[record.paymentStatus] ?? record.paymentStatus
    const bad = record.paymentStatus === "token_invalid" || record.paymentStatus === "risk_blocked"
    return <Badge variant={bad ? "destructive" : "outline"}>{label}</Badge>
  }
  return <Badge variant="outline">未检测</Badge>
}

function AtStatusCell({ record }: { record: AccountRecord }) {
  if (record.atRefillStatus === "failed") {
    return <Badge variant="destructive">补AT失败</Badge>
  }
  if (record.atRefillStatus === "add_phone_required") {
    return <Badge variant="secondary">需绑手机</Badge>
  }
  if (record.accessTokenMissing) {
    return <Badge variant="secondary">待补 AT</Badge>
  }
  if (record.accessTokenConfigured) {
    return (
      <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
        已配置
      </Badge>
    )
  }
  return <Badge variant="outline">未配置</Badge>
}

// ── Create dialog ─────────────────────────────────────────────────────────────

function CreateAccountDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const create = useCreateAccount()
  const [form, setForm] = React.useState({
    email: "",
    chatgptPassword: "",
    totpSecret: "",
    emailAccessUrl: "",
    accountType: "free" as "plus" | "free",
    registrationCountry: "",
  })

  const submit = () => {
    create.mutate(
      {
        email: form.email.trim(),
        chatgptPassword: form.chatgptPassword || "",
        totpSecret: form.totpSecret.trim() || "",
        emailAccessUrl: form.emailAccessUrl.trim() || "",
        accountType: form.accountType,
        registrationCountry: form.registrationCountry
          ? form.registrationCountry.trim().toUpperCase()
          : null,
      },
      {
        onSuccess: (rec) => {
          toast.success(`账号 ${rec.email} 已创建`)
          onOpenChange(false)
          setForm({
            email: "",
            chatgptPassword: "",
            totpSecret: "",
            emailAccessUrl: "",
            accountType: "free",
            registrationCountry: "",
          })
        },
        onError: (e) => {
          if (e instanceof HttpError && e.status === 409) {
            toast.error(`创建失败：邮箱 ${form.email.trim()} 已在账号池中（重复录入）`)
          } else {
            toast.error(e instanceof HttpError ? e.message : "创建失败")
          }
        },
      },
    )
  }

  const valid = form.email.includes("@")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>录入账号</DialogTitle>
          <DialogDescription>
            手工录入一个 ChatGPT 账号。仅邮箱必填；密码 / 2FA / 取件 URL 可留空后补。
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="grid gap-2">
            <Label htmlFor="acc-email">邮箱</Label>
            <Input
              id="acc-email"
              placeholder="user@example.com"
              value={form.email}
              onChange={(e) => setForm({ ...form, email: e.target.value })}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="acc-pass">ChatGPT 密码（可空）</Label>
            <Input
              id="acc-pass"
              type="password"
              value={form.chatgptPassword}
              onChange={(e) => setForm({ ...form, chatgptPassword: e.target.value })}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="acc-totp">TOTP 密钥（可空）</Label>
            <Input
              id="acc-totp"
              placeholder="JBSWY3DPEHPK3PXP"
              className="font-mono"
              value={form.totpSecret}
              onChange={(e) => setForm({ ...form, totpSecret: e.target.value })}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="acc-url">邮箱取件 URL（可空）</Label>
            <Input
              id="acc-url"
              placeholder="https://mail.example.com/..."
              value={form.emailAccessUrl}
              onChange={(e) => setForm({ ...form, emailAccessUrl: e.target.value })}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="grid gap-2">
              <Label>账号类型</Label>
              <Select
                value={form.accountType}
                onValueChange={(v) => setForm({ ...form, accountType: v as "plus" | "free" })}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="free">Free</SelectItem>
                  <SelectItem value="plus">Plus</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="acc-country">注册国家（2 位码，可空）</Label>
              <Input
                id="acc-country"
                placeholder="US"
                maxLength={2}
                className="font-mono uppercase"
                value={form.registrationCountry}
                onChange={(e) => setForm({ ...form, registrationCountry: e.target.value })}
              />
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button onClick={submit} disabled={!valid || create.isPending}>
            {create.isPending ? "创建中…" : "创建"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ── Detail dialog ─────────────────────────────────────────────────────────────

function DetailRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[110px_1fr] gap-2 py-1 text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span className="min-w-0 break-all">{value}</span>
    </div>
  )
}

function AccountDetailDialog({
  record,
  open,
  onOpenChange,
}: {
  record: AccountRecord | null
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  if (!record) return null
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="font-mono text-base">{record.email}</DialogTitle>
          <DialogDescription>账号详情（创建于 {formatTime(record.createdAt)}）</DialogDescription>
        </DialogHeader>
        <div className="divide-y">
          <div>
            <DetailRow label="账号 ID" value={<span className="font-mono text-xs">{record.id}</span>} />
            <DetailRow label="账号类型" value={<AccountTypeBadge type={record.accountType} />} />
            <DetailRow label="绑定手机" value={record.phoneBound === null ? "未知" : record.phoneBound ? "已绑定" : "未绑定"} />
            <DetailRow
              label="TOTP 密钥"
              value={
                record.totpSecret ? (
                  <CopyText text={record.totpSecret} className="font-mono text-xs">
                    {record.totpSecret}
                  </CopyText>
                ) : (
                  "-"
                )
              }
            />
            <DetailRow
              label="取件 URL"
              value={
                <a
                  href={record.emailAccessUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="text-primary break-all hover:underline"
                >
                  {record.emailAccessUrl}
                </a>
              }
            />
          </div>
          <div>
            <DetailRow
              label="注册国家 / IP"
              value={`${countryLabel(record.registrationCountry)} / ${record.registrationIp ?? "-"}`}
            />
            <DetailRow label="存活状态" value={<AliveBadge status={record.aliveStatus} />} />
            <DetailRow label="检测时间" value={formatTime(record.aliveCheckedAt)} />
            <DetailRow
              label="检测结果"
              value={`${record.aliveErrorCode ?? "-"}${record.aliveHttpStatus ? `（HTTP ${record.aliveHttpStatus}）` : ""}`}
            />
            <DetailRow label="换绑状态" value={record.rebindStatus === "success" ? "已换绑" : "未换绑"} />
          </div>
          <div>
            <DetailRow label="优惠资格" value={<PromotionCell record={record} />} />
            <DetailRow label="订阅套餐" value={record.subscriptionPlan ?? "-"} />
            <DetailRow label="套餐过期" value={formatTime(record.planExpiresAt)} />
            <DetailRow label="资格查询时间" value={formatTime(record.planCheckedAt)} />
            {record.planCheckErrorCode && (
              <DetailRow label="查询错误码" value={<span className="text-destructive">{record.planCheckErrorCode}</span>} />
            )}
          </div>
          <div>
            <DetailRow label="试用扫描时间" value={formatTime(record.trialScanCheckedAt)} />
            {record.trialScanError ? (
              <DetailRow label="扫描错误" value={<span className="text-destructive">{record.trialScanError}</span>} />
            ) : record.trialScanCountries && Object.keys(record.trialScanCountries).length > 0 ? (
              <div className="py-2">
                <p className="text-muted-foreground mb-2 text-sm">试用扫描结果</p>
                <div className="flex flex-wrap gap-1.5">
                  {Object.entries(record.trialScanCountries).map(([cc, r]) => (
                    <Badge
                      key={cc}
                      variant={r.error ? "destructive" : r.eligible ? "secondary" : "outline"}
                      title={r.error || r.state}
                    >
                      {cc}: {r.error ? "失败" : r.eligible === true ? "有" : r.eligible === false ? "无" : "未知"}
                    </Badge>
                  ))}
                </div>
              </div>
            ) : (
              <DetailRow label="试用扫描" value="未扫描" />
            )}
          </div>
          {record.remark && (
            <div>
              <DetailRow label="备注" value={record.remark} />
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

// ── Main page ─────────────────────────────────────────────────────────────────

export default function AccountsPage() {
  const navigate = useNavigate()
  const [page, setPage] = React.useState(1)
  const [pageSize, setPageSize] = React.useState(20)
  const [q, setQ] = React.useState("")
  const [promotion, setPromotion] = React.useState("all")
  const [country, setCountry] = React.useState("all")
  const [alive, setAlive] = React.useState("all")
  const [paymentFilter, setPaymentFilter] = React.useState("all")
  const [selected, setSelected] = React.useState<Set<string>>(new Set())
  const [createOpen, setCreateOpen] = React.useState(false)
  const [deleteOpen, setDeleteOpen] = React.useState(false)
  const [detail, setDetail] = React.useState<AccountRecord | null>(null)

  const list = useAccounts({
    page,
    pageSize,
    q: q || undefined,
    promotion: promotion === "all" ? undefined : promotion,
    country: country === "all" ? undefined : country,
    alive: alive === "all" ? undefined : alive,
    zero_payment: paymentFilter === "all" ? undefined : paymentFilter,
  })
  const bulkDelete = useBulkDeleteAccounts()
  const checkCombined = useCheckCombinedAccounts()
  const check2FA = useCheck2FAAccounts()
  const bulkEnsure2FA = useBulkEnsure2FA()
  const ensure2FA = useEnsure2FA()
  const bulkHeal = useBulkHealAccounts()
  const healAccount = useHealAccount()

  const items = list.data?.items ?? []
  const allChecked = items.length > 0 && items.every((a) => selected.has(a.id))

  const toggleAll = () => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (allChecked) items.forEach((a) => next.delete(a.id))
      else items.forEach((a) => next.add(a.id))
      return next
    })
  }

  const toggleOne = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const doDelete = () => {
    bulkDelete.mutate(Array.from(selected), {
      onSuccess: (r) => {
        toast.success(`已删除 ${r.deleted} 个账号`)
        setSelected(new Set())
        setDeleteOpen(false)
      },
      onError: (e) => toast.error(e instanceof HttpError ? e.message : "删除失败"),
    })
  }

  // 批量「验活」:调 check-combined,只关注存活/失效计数(toast 侧重 alive/dead)。
  const runCheckAlive = () => {
    const ids = Array.from(selected)
    checkCombined.mutate(
      { ids },
      {
        onSuccess: (r) =>
          toast.success(
            `验活完成:存活 ${r.alive}｜失效 ${r.dead}｜失败 ${r.failed}｜跳过 ${r.skipped}`,
            {
              description:
                r.failed > 0 ? "失败项多为无 AccessToken 或代理不可用,可在详情查看错误码"
                  : `共请求 ${r.requested} 个账号`,
            },
          ),
        onError: (e) =>
          toast.error(e instanceof HttpError ? e.message : "批量验活失败"),
      },
    )
  }

  // 批量「查优惠资格」:同样调 check-combined(物理同一次 plan.Check),toast 侧重 plan/资格。
  const runCheckPromotion = () => {
    const ids = Array.from(selected)
    checkCombined.mutate(
      { ids },
      {
        onSuccess: (r) => {
          const planOk = r.items.filter((i) => i.planStatus === "success").length
          toast.success(
            `查优惠完成:套餐/资格已刷新 ${planOk}｜失败 ${r.failed}｜跳过 ${r.skipped}`,
            {
              description:
                r.failed > 0 ? "失败项多为无 AccessToken 或代理不可用,可在详情查看错误码"
                  : `共请求 ${r.requested} 个账号;有资格/无资格已写入账号池`,
            },
          )
        },
        onError: (e) =>
          toast.error(e instanceof HttpError ? e.message : "批量查优惠失败"),
      },
    )
  }

  // 批量「查询 2FA 状态」:用已存 accessToken 查 /me(无需重认证),回写 totpStatus。
  const runCheck2FA = () => {
    const ids = Array.from(selected)
    check2FA.mutate(
      { ids },
      {
        onSuccess: (r) =>
          toast.success(
            `查 2FA 完成:成功 ${r.succeeded}｜失败 ${r.failed}｜跳过 ${r.skipped}`,
            { description: `共请求 ${r.requested} 个账号` },
          ),
        onError: (e) =>
          toast.error(e instanceof HttpError ? e.message : "批量查 2FA 失败"),
      },
    )
  }

  // 批量「补 2FA(开通 TOTP)」:对选中账号执行协议登录+enroll+activate。
  // 仅对有密码账号生效;无密码/已绑定 2FA 的账号会被跳过(skipped)。
  const runBulkEnsure2FA = () => {
    const ids = Array.from(selected)
    bulkEnsure2FA.mutate(
      { ids },
      {
        onSuccess: (r) => {
          toast.success(
            `补 2FA 完成:成功 ${r.succeeded}｜失败 ${r.failed}｜跳过 ${r.skipped}`,
            {
              description:
                r.failed > 0
                  ? "失败项多为密码错误/代理不可用/已启用 2FA 冲突,可展开单号查看错误"
                  : `共请求 ${r.requested} 个账号`,
            },
          )
        },
        onError: (e) =>
          toast.error(e instanceof HttpError ? e.message : "批量补 2FA 失败"),
      },
    )
  }

  // 单账号补 2FA(操作菜单触发)。
  const runEnsure2FAOne = (id: string) => {
    ensure2FA.mutate(
      { id },
      {
        onSuccess: (r) => {
          if (r.status === "success") {
            toast.success("2FA 绑定成功", { description: "TOTP 密钥已落库,可导出用 Authenticator 登录" })
          } else if (r.status === "skipped") {
            toast.info("已跳过", { description: r.error || "该账号无需补 2FA" })
          } else {
            toast.error("补 2FA 失败", { description: r.error || "未知错误" })
          }
        },
        onError: (e) =>
          toast.error(e instanceof HttpError ? e.message : "补 2FA 失败"),
      },
    )
  }

  // 批量「续 AT(token-heal)」:用落库 session cookie 轻量续期,无需密码/OTP。
  // 无 session token 的老账号会被跳过(skipped=no_session_token)。
  const runBulkHeal = () => {
    const ids = Array.from(selected)
    bulkHeal.mutate(
      { ids },
      {
        onSuccess: (r) => {
          toast.success(
            `续 AT 完成:成功 ${r.succeeded}｜失败 ${r.failed}｜跳过 ${r.skipped}`,
            {
              description:
                r.failed > 0
                  ? "失败项多为 session cookie 已过期(约 3 个月),需走完整登录重建;跳过项为无 session token 的老账号"
                  : `共请求 ${r.requested} 个账号`,
            },
          )
        },
        onError: (e) =>
          toast.error(e instanceof HttpError ? e.message : "批量续 AT 失败"),
      },
    )
  }

  // 单账号续 AT(操作菜单触发)。
  const runHealOne = (id: string) => {
    healAccount.mutate(
      { id },
      {
        onSuccess: (r) => {
          if (r.status === "success") {
            toast.success("AT 已续期", { description: "用 session cookie 换到新 access_token" })
          } else if (r.status === "skipped") {
            toast.info("已跳过", { description: r.error || "该账号无 session token" })
          } else {
            toast.error("续 AT 失败", { description: r.error || "未知错误" })
          }
        },
        onError: (e) =>
          toast.error(e instanceof HttpError ? e.message : "续 AT 失败"),
      },
    )
  }

  return (
    <div>
      <PageHeader
        title="账号池"
        description="ChatGPT 账号的录入、筛选、验活与生命周期管理"
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={() => navigate("/payment-check?preset=eligible_untested")}
            >
              <CreditCard /> 检测有资格未测支付的免费号
            </Button>
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <Plus /> 录入账号
            </Button>
          </>
        }
      />

      {/* 批量操作工具栏(常驻):核心批量功能前置可见,勾选后激活。 */}
      <div className="mb-3 flex flex-wrap items-center gap-2 rounded-xl border bg-muted/40 px-3 py-2.5">
        <span className="text-sm text-muted-foreground">
          {selected.size > 0 ? (
            <>已选 <span className="font-semibold text-foreground">{selected.size}</span> 个账号</>
          ) : (
            "勾选账号后可批量操作"
          )}
        </span>
        <div className="mx-1 h-5 w-px bg-border" />
        <Button
          size="sm"
          variant="outline"
          disabled={selected.size === 0 || checkCombined.isPending}
          onClick={runCheckAlive}
        >
          {checkCombined.isPending ? <Loader2 className="size-4 animate-spin" /> : <HeartPulse />}
          批量验活
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={selected.size === 0 || checkCombined.isPending}
          onClick={runCheckPromotion}
        >
          {checkCombined.isPending ? <Loader2 className="size-4 animate-spin" /> : <Gift />}
          批量查优惠资格
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={selected.size === 0 || bulkEnsure2FA.isPending}
          onClick={runBulkEnsure2FA}
        >
          {bulkEnsure2FA.isPending ? <Loader2 className="size-4 animate-spin" /> : <ShieldCheck />}
          批量补 2FA + 密码
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={selected.size === 0 || bulkHeal.isPending}
          onClick={runBulkHeal}
        >
          {bulkHeal.isPending ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw />}
          批量续 AT
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="sm" variant="outline" disabled={selected.size === 0}>
              <MoreHorizontal /> 更多操作
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuItem disabled={check2FA.isPending} onClick={runCheck2FA}>
              <KeyRound /> 批量查 2FA 状态
            </DropdownMenuItem>
            <DropdownMenuItem
              onClick={() => navigate(`/payment-check?ids=${Array.from(selected).join(",")}`)}
            >
              <CreditCard /> 批量支付检测
            </DropdownMenuItem>
            <DropdownMenuItem
              onClick={() => navigate(`/rebind?ids=${Array.from(selected).join(",")}`)}
            >
              <MailQuestion /> 批量邮箱换绑
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              className="text-destructive focus:text-destructive"
              onClick={() => setDeleteOpen(true)}
            >
              <Trash2 /> 删除已选（{selected.size}）
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {/* Filters */}
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <SearchInput
          value={q}
          onChange={(v) => {
            setQ(v)
            setPage(1)
          }}
          placeholder="搜索邮箱 / 备注…"
          className="w-64"
        />
        <Select
          value={promotion}
          onValueChange={(v) => {
            setPromotion(v)
            setPage(1)
          }}
        >
          <SelectTrigger className="w-40">
            <SelectValue placeholder="优惠资格" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部资格</SelectItem>
            <SelectItem value="untried_plus">免费试用一个月</SelectItem>
            <SelectItem value="ineligible">不可试用</SelectItem>
            <SelectItem value="unchecked">未查询</SelectItem>
          </SelectContent>
        </Select>
        <Select
          value={alive}
          onValueChange={(v) => {
            setAlive(v)
            setPage(1)
          }}
        >
          <SelectTrigger className="w-36">
            <SelectValue placeholder="存活状态" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部状态</SelectItem>
            <SelectItem value="alive">存活</SelectItem>
            <SelectItem value="dead">已失效</SelectItem>
            <SelectItem value="unknown">检测异常</SelectItem>
            <SelectItem value="running">检测中</SelectItem>
            <SelectItem value="unchecked">未检测</SelectItem>
          </SelectContent>
        </Select>
        <Input
          value={country === "all" ? "" : country}
          onChange={(e) => {
            const v = e.target.value.trim().toUpperCase()
            setCountry(v || "all")
            setPage(1)
          }}
          placeholder="国家码"
          className="w-24 font-mono uppercase"
          maxLength={2}
        />
        <Select
          value={paymentFilter}
          onValueChange={(v) => {
            setPaymentFilter(v)
            setPage(1)
          }}
        >
          <SelectTrigger className="w-44">
            <SelectValue placeholder="0 元支付渠道" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部渠道</SelectItem>
            <SelectItem value="momo">0 元可走 MoMo</SelectItem>
            <SelectItem value="gcash">0 元可走 GCash</SelectItem>
            <SelectItem value="card">0 元可走银行卡</SelectItem>
            <SelectItem value="paypal">0 元可走 PayPal</SelectItem>
          </SelectContent>
        </Select>
        <Button variant="ghost" size="sm" onClick={() => list.refetch()}>
          <RefreshCw /> 刷新
        </Button>
      </div>

      {/* Table */}
      <div className="bg-card rounded-xl border">
        {list.isPending ? (
          <TableSkeleton rows={10} cols={8} />
        ) : items.length === 0 ? (
          <EmptyState
            title="暂无账号"
            description="点击右上角「录入账号」添加第一个 ChatGPT 账号。"
            action={
              <Button size="sm" onClick={() => setCreateOpen(true)}>
                <Plus /> 录入账号
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
                <TableHead>类型</TableHead>
                <TableHead>2FA</TableHead>
                <TableHead>AT 状态</TableHead>
                <TableHead>优惠资格</TableHead>
                <TableHead>支付渠道</TableHead>
                <TableHead>国家</TableHead>
                <TableHead>验活</TableHead>
                <TableHead>创建时间</TableHead>
                <TableHead className="w-10" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((acc) => (
                <TableRow
                  key={acc.id}
                  className="cursor-pointer"
                  onClick={() => setDetail(acc)}
                >
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <Checkbox
                      checked={selected.has(acc.id)}
                      onCheckedChange={() => toggleOne(acc.id)}
                    />
                  </TableCell>
                  <TableCell className="max-w-[220px] truncate font-mono text-xs">
                    <CopyText text={acc.email}>{acc.email}</CopyText>
                  </TableCell>
                  <TableCell>
                    <AccountTypeBadge type={acc.accountType} />
                  </TableCell>
                  <TableCell>
                    <TotpCell record={acc} />
                  </TableCell>
                  <TableCell>
                    <AtStatusCell record={acc} />
                  </TableCell>
                  <TableCell>
                    <PromotionCell record={acc} />
                  </TableCell>
                  <TableCell>
                    <PaymentCell record={acc} />
                  </TableCell>
                  <TableCell className="text-xs">
                    {countryLabel(acc.registrationCountry)}
                  </TableCell>
                  <TableCell>
                    <AliveBadge status={acc.aliveStatus} />
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">
                    {formatTime(acc.createdAt)}
                  </TableCell>
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <div className="flex items-center gap-1">
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <Button
                            variant="outline"
                            size="icon-sm"
                            onClick={() => setDetail(acc)}
                          >
                            <Eye />
                          </Button>
                        </TooltipTrigger>
                        <TooltipContent>查看详情</TooltipContent>
                      </Tooltip>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon-sm">
                            <MoreHorizontal />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem
                            disabled={ensure2FA.isPending}
                            onClick={() => runEnsure2FAOne(acc.id)}
                          >
                            <ShieldCheck /> 补 2FA
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            disabled={healAccount.isPending}
                            onClick={() => runHealOne(acc.id)}
                          >
                            <RefreshCw /> 续期 AT
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => navigator.clipboard.writeText(acc.totpSecret || "")}
                          >
                            <KeyRound /> 复制 2FA 密钥
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => navigator.clipboard.writeText(acc.emailAccessUrl)}
                          >
                            <Link2 /> 复制取件 URL
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => navigator.clipboard.writeText(acc.email)}
                          >
                            <Copy /> 复制邮箱
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            variant="destructive"
                            onClick={() => {
                              setSelected(new Set([acc.id]))
                              setDeleteOpen(true)
                            }}
                          >
                            <Trash2 /> 删除
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
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

      <CreateAccountDialog open={createOpen} onOpenChange={setCreateOpen} />
      <AccountDetailDialog record={detail} open={!!detail} onOpenChange={(o) => !o && setDetail(null)} />

      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认删除 {selected.size} 个账号？</AlertDialogTitle>
            <AlertDialogDescription>
              该操作不可撤销，账号记录将从池中永久移除。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={doDelete}
              disabled={bulkDelete.isPending}
              variant="destructive"
            >
              {bulkDelete.isPending ? "删除中…" : "确认删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
