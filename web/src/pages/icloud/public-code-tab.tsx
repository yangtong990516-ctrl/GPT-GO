// 公共取码 tab:免登录公开能力的控制台内嵌视图。
// 接口均为公共接口(挂载于 /api/icloud/v1/public-code*),返回信封 {success,data}。
// icloudApi 已自动拼 /api/icloud 前缀,故这里路径写 "/v1/public-code/..."。
import * as React from "react"
import {
  CircleAlert,
  Clipboard,
  Cloud,
  KeyRound,
  LoaderCircle,
  Mail,
  MailSearch,
  RefreshCw,
} from "lucide-react"
import { toast } from "sonner"
import { icloudApi } from "@/lib/icloud-api"
import { HttpError } from "@/lib/api"
import { formatTime } from "@/lib/format"
import { buildEmailHTMLDocument, errMsg } from "./shared"
import { Badge } from "@/components/ui/badge"
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
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

// ── 类型定义(对齐公共接口返回) ────────────────────────────────────────────────

interface PublicCodeStatus {
  enabled: boolean
}

interface PublicCodeMessageItem {
  id: string
  subject?: string
  from?: string
  type?: string
  source?: string
  content_type?: string
  has_html?: boolean
  received_at?: string
  created_at?: string
}

interface PublicCodeMessageList {
  items: PublicCodeMessageItem[]
  total: number
  last_sync_at?: string
  sync_error?: string
}

interface PublicCodeMessageDetail {
  id: string
  subject?: string
  from?: string
  to?: string
  body?: string
  html?: string
  html_body?: string
  received_at?: string
  created_at?: string
}

interface PublicCodeResult {
  code?: string
  email?: string
  subject?: string
  from?: string
  received_at?: string
  message_id?: string
}

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

// ── 辅助 ──────────────────────────────────────────────────────────────────────

function looksLikeHTML(value?: string): boolean {
  return /<(?:!doctype|html|head|body|style|table|div|p|a|img|span)\b/i.test(String(value ?? ""))
}

/** 类型徽章文案:优先 type,其次 source / content_type 推断。 */
function messageTypeLabel(m: PublicCodeMessageItem): string {
  const raw = String(m.type ?? m.source ?? "").trim()
  if (raw) return raw
  const ct = String(m.content_type ?? "").toLowerCase()
  if (m.has_html || ct.includes("text/html")) return "HTML"
  if (ct.includes("text/plain")) return "文本"
  return "邮件"
}

function messageTime(m: PublicCodeMessageItem): string {
  return m.received_at || m.created_at || ""
}

async function copyText(value: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(value)
    return true
  } catch {
    return false
  }
}

// ── 组件 ──────────────────────────────────────────────────────────────────────

export function PublicCodeTab({ enabled }: { enabled: boolean }) {
  // 服务状态徽章
  const [serviceEnabled, setServiceEnabled] = React.useState<boolean | null>(null)

  // 输入
  const [email, setEmail] = React.useState("")
  const [inputError, setInputError] = React.useState<string | null>(null)

  // 邮件列表
  const [messages, setMessages] = React.useState<PublicCodeMessageItem[]>([])
  const [total, setTotal] = React.useState(0)
  const [lastSyncAt, setLastSyncAt] = React.useState<string | undefined>(undefined)
  const [syncError, setSyncError] = React.useState<string | null>(null)
  const [listLoading, setListLoading] = React.useState(false)

  // 取码弹窗
  const [codeDialogOpen, setCodeDialogOpen] = React.useState(false)
  const [codePending, setCodePending] = React.useState(false)
  const [codeResult, setCodeResult] = React.useState<PublicCodeResult | null>(null)
  const [codeError, setCodeError] = React.useState<string | null>(null)

  // 完整邮件弹窗
  const [detailDialogOpen, setDetailDialogOpen] = React.useState(false)
  const [detailLoading, setDetailLoading] = React.useState(false)
  const [detail, setDetail] = React.useState<PublicCodeMessageDetail | null>(null)
  const [detailError, setDetailError] = React.useState<string | null>(null)

  // 服务状态:进入页面查询一次
  React.useEffect(() => {
    if (!enabled) return
    let cancelled = false
    icloudApi
      .get<PublicCodeStatus>("/v1/public-code/status")
      .then((r) => {
        if (!cancelled) setServiceEnabled(!!r?.enabled)
      })
      .catch(() => {
        if (!cancelled) setServiceEnabled(false)
      })
    return () => {
      cancelled = true
    }
  }, [enabled])

  // 邮箱校验;通过则返回 trim 后的邮箱,否则置错并返回 null
  const validateEmail = (): string | null => {
    const value = email.trim()
    if (!value) {
      setInputError("请输入隐私邮箱地址")
      return null
    }
    if (!EMAIL_RE.test(value)) {
      setInputError("邮箱格式不正确")
      return null
    }
    setInputError(null)
    return value
  }

  // 拉取邮件列表(sync=1 触发同步)
  const fetchMessages = async () => {
    const value = validateEmail()
    if (!value) return
    setListLoading(true)
    setSyncError(null)
    try {
      const r = await icloudApi.get<PublicCodeMessageList>(
        `/v1/public-code/messages?email=${encodeURIComponent(value)}&sync=1&limit=50`,
      )
      setMessages(r?.items ?? [])
      setTotal(r?.total ?? r?.items?.length ?? 0)
      setLastSyncAt(r?.last_sync_at || undefined)
      setSyncError(r?.sync_error || null)
    } catch (e) {
      setSyncError(errMsg(e, "邮件同步失败"))
    } finally {
      setListLoading(false)
    }
  }

  // 获取验证码(长轮询等待最新邮件)
  const fetchCode = async () => {
    const value = validateEmail()
    if (!value) return
    setCodeDialogOpen(true)
    setCodePending(true)
    setCodeResult(null)
    setCodeError(null)
    try {
      const r = await icloudApi.get<PublicCodeResult>(
        `/v1/public-code?email=${encodeURIComponent(value)}&wait_ms=15000`,
      )
      setCodeResult(r)
    } catch (e) {
      setCodeError(e instanceof HttpError ? e.message : errMsg(e, "取码失败"))
    } finally {
      setCodePending(false)
    }
  }

  const copyCode = async () => {
    const code = String(codeResult?.code ?? "").trim()
    if (!code) return
    const ok = await copyText(code)
    if (ok) toast.success("验证码已复制")
    else toast.error("复制失败,请手动复制")
  }

  // 打开完整邮件弹窗
  const openMessageDetail = async (id: string) => {
    const value = validateEmail()
    if (!value) return
    setDetailDialogOpen(true)
    setDetailLoading(true)
    setDetail(null)
    setDetailError(null)
    try {
      const r = await icloudApi.get<PublicCodeMessageDetail>(
        `/v1/public-code/messages/${encodeURIComponent(id)}?email=${encodeURIComponent(value)}`,
      )
      setDetail(r)
    } catch (e) {
      setDetailError(e instanceof HttpError ? e.message : errMsg(e, "邮件加载失败"))
    } finally {
      setDetailLoading(false)
    }
  }

  const detailHTML = React.useMemo(() => {
    const html = String(detail?.html ?? detail?.html_body ?? "").trim()
    const raw = html || (looksLikeHTML(detail?.body) ? String(detail?.body ?? "") : "")
    // 与 mailboxes-tab 一致:渲染前做安全清洗(移除危险节点/on* 属性/javascript: URL、注入 CSP)。
    return raw ? buildEmailHTMLDocument(raw) : ""
  }, [detail])

  const detailTime = detail?.received_at || detail?.created_at || ""

  return (
    <div className="space-y-4">
      {/* ── 页头 ── */}
      <div className="flex flex-wrap items-center gap-3">
        <span className="bg-primary/10 text-primary flex size-10 items-center justify-center rounded-xl">
          <Cloud className="size-5" />
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="text-lg font-semibold">公共取码</h2>
          <p className="text-muted-foreground text-sm">
            免登录公开能力:输入隐私邮箱即可查询验证码与邮件
          </p>
        </div>
        {serviceEnabled === null ? (
          <Badge variant="secondary" className="text-xs">
            <LoaderCircle className="size-3 animate-spin" /> 检测中…
          </Badge>
        ) : serviceEnabled ? (
          <Badge variant="default" className="text-xs">
            服务已启用
          </Badge>
        ) : (
          <Badge variant="destructive" className="text-xs">
            服务未启用
          </Badge>
        )}
      </div>

      {/* ── 输入卡 ── */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">隐私邮箱地址</CardTitle>
          <CardDescription>输入需要查询的隐私邮箱,可同步邮件或直接获取验证码</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="grid gap-2">
            <Label htmlFor="public-code-email">隐私邮箱地址</Label>
            <Input
              id="public-code-email"
              type="email"
              value={email}
              onChange={(e) => {
                setEmail(e.target.value)
                if (inputError) setInputError(null)
              }}
              placeholder="name@icloud.com"
              className="font-mono"
              aria-invalid={!!inputError}
            />
            {inputError && <p className="text-destructive text-xs">{inputError}</p>}
          </div>
          <div className="flex flex-wrap gap-2">
            <Button onClick={fetchMessages} disabled={listLoading}>
              {listLoading ? (
                <LoaderCircle className="animate-spin" />
              ) : (
                <MailSearch />
              )}
              {listLoading ? "同步中…" : "同步邮件"}
            </Button>
            <Button variant="outline" onClick={fetchCode} disabled={codePending}>
              {codePending ? <LoaderCircle className="animate-spin" /> : <KeyRound />}
              获取验证码
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* ── 邮件列表卡 ── */}
      <Card>
        <CardHeader className="pb-3">
          <div className="flex flex-wrap items-center gap-2">
            <CardTitle className="text-sm">
              邮件列表 {messages.length}
              {total > messages.length ? `/${total}` : ""}
            </CardTitle>
            {lastSyncAt && (
              <span className="text-muted-foreground text-xs">
                同步于 {formatTime(lastSyncAt)}
              </span>
            )}
            <Button
              variant="ghost"
              size="icon-sm"
              className="ml-auto"
              onClick={fetchMessages}
              disabled={listLoading}
              title="刷新邮件列表"
            >
              <RefreshCw className={listLoading ? "animate-spin" : ""} />
            </Button>
          </div>
          {syncError && (
            <p className="text-amber-600 dark:text-amber-400 flex items-center gap-1 text-xs">
              <CircleAlert className="size-3.5" /> {syncError}
            </p>
          )}
        </CardHeader>
        <CardContent>
          {messages.length === 0 ? (
            <p className="text-muted-foreground py-10 text-center text-sm">
              {listLoading ? "正在同步邮件…" : "暂无邮件,输入邮箱后点击「同步邮件」"}
            </p>
          ) : (
            <ul className="divide-y">
              {messages.map((m) => (
                <li key={m.id}>
                  <button
                    type="button"
                    className="hover:bg-muted/50 flex w-full items-center gap-3 rounded-md px-2 py-2.5 text-left transition-colors"
                    onClick={() => openMessageDetail(m.id)}
                  >
                    <Mail className="text-muted-foreground size-4 shrink-0" />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium">
                        {m.subject || "无主题"}
                      </p>
                      <p className="text-muted-foreground truncate text-xs">
                        {m.from || "未知发件人"}
                      </p>
                    </div>
                    <Badge variant="secondary" className="shrink-0 text-xs">
                      {messageTypeLabel(m)}
                    </Badge>
                    <time className="text-muted-foreground w-32 shrink-0 text-right text-xs">
                      {formatTime(messageTime(m))}
                    </time>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      {/* ── footer 文案 ── */}
      <p className="text-muted-foreground text-center text-xs">
        只会查询当前输入邮箱的验证码与邮件
      </p>

      {/* ── 取码弹窗 ── */}
      <Dialog open={codeDialogOpen} onOpenChange={setCodeDialogOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>获取验证码</DialogTitle>
            <DialogDescription className="break-all">{email.trim() || "-"}</DialogDescription>
          </DialogHeader>
          {codePending ? (
            <div className="flex min-h-44 flex-col items-center justify-center gap-3 text-center">
              <LoaderCircle className="text-primary size-8 animate-spin" />
              <p className="text-muted-foreground text-sm">
                正在同步并检查最新邮件,请稍候…
              </p>
            </div>
          ) : codeResult?.code ? (
            <div className="space-y-4">
              <div className="flex flex-col items-center gap-2">
                <Badge
                  variant="default"
                  className="h-auto px-4 py-2 font-mono text-3xl font-semibold tracking-widest"
                >
                  {codeResult.code}
                </Badge>
                {codeResult.subject && (
                  <p
                    className="text-muted-foreground max-w-full truncate text-xs"
                    title={codeResult.subject}
                  >
                    {codeResult.subject}
                  </p>
                )}
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
                  {codeError || "请稍后重新取码"}
                </span>
              </div>
              <Button variant="outline" onClick={() => setCodeDialogOpen(false)}>
                关闭
              </Button>
            </div>
          )}
        </DialogContent>
      </Dialog>

      {/* ── 完整邮件弹窗 ── */}
      <Dialog open={detailDialogOpen} onOpenChange={setDetailDialogOpen}>
        <DialogContent className="flex max-h-[calc(100vh-2.5rem)] flex-col overflow-hidden sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle className="leading-6 break-words">
              {detail?.subject || "无主题"}
            </DialogTitle>
            <DialogDescription className="break-all">
              {detail?.from || "未知发件人"}
            </DialogDescription>
          </DialogHeader>
          <div className="text-muted-foreground flex items-center justify-between gap-3 border-b pb-2 text-[11px]">
            <span className="truncate">收件邮箱:{detail?.to || email.trim() || "-"}</span>
            <time className="shrink-0">{formatTime(detailTime)}</time>
          </div>
          <div className="bg-muted/30 relative min-h-64 flex-1 overflow-hidden rounded-md">
            {detailLoading ? (
              <div className="text-muted-foreground absolute inset-0 flex items-center justify-center gap-2 text-xs">
                <LoaderCircle className="size-5 animate-spin" />
                <span>正在加载完整邮件</span>
              </div>
            ) : detailError ? (
              <div className="text-destructive flex h-full min-h-64 flex-col items-center justify-center gap-2 text-sm">
                <CircleAlert className="size-6" />
                <p>{detailError}</p>
              </div>
            ) : detailHTML ? (
              <iframe
                className="h-[60vh] w-full border-0 bg-white"
                srcDoc={detailHTML}
                sandbox="allow-popups allow-popups-to-escape-sandbox"
                title="HTML 邮件正文"
              />
            ) : (
              <pre className="h-[60vh] overflow-y-auto p-4 text-sm whitespace-pre-wrap">
                {detail?.body || "这封邮件没有正文内容。"}
              </pre>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
