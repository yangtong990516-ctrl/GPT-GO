import * as React from "react"
import { useSearchParams } from "react-router-dom"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  CircleStop,
  CreditCard,
  Loader2,
  Play,
  Plus,
  ShieldCheck,
  X,
} from "lucide-react"
import { PageHeader } from "@/components/page-header"
import { HttpError } from "@/lib/api"
import { formatTime } from "@/lib/format"
import {
  useAccounts,
  useCancelPaymentCheck,
  usePaymentBatchItems,
  usePaymentBatchStatus,
  usePaymentProxies,
  useRunPaymentCheck,
  type PaymentBatchItem,
  type PaymentProxyItem,
  type PaymentRouteSpec,
} from "@/lib/queries"
import type { AccountRecord } from "@/lib/types"
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"

// ── 展示辅助 ──────────────────────────────────────────────────────────────────

const METHOD_LABELS: Record<string, string> = {
  card: "银行卡",
  paypal: "PayPal",
  momo: "MoMo",
  gcash: "GCash",
  gopay: "GoPay",
  grabpay: "GrabPay",
  link: "Link",
  upi: "UPI",
  pix: "PIX",
  ideal: "iDEAL",
  kakao_pay: "Kakao Pay",
}

const STATUS_LABELS: Record<string, string> = {
  available: "已返回",
  partial: "部分返回",
  not_returned: "未返回",
  already_paid: "已是付费账号",
  token_invalid: "Token 失效",
  risk_blocked: "风控拦截",
  rate_limited: "请求限流",
  checkout_rejected: "建单被拒",
  checkout_failed: "建单失败",
  proxy_unavailable: "代理不可用",
  proxy_country_mismatch: "国家不符",
  disabled: "无有效线路",
  canceled: "已取消",
  running: "检测中",
}

const COUNTRY_PRESETS: Record<string, { currency: string; locale: string }> = {
  VN: { currency: "VND", locale: "vi-VN" },
  PH: { currency: "PHP", locale: "en-PH" },
  ID: { currency: "IDR", locale: "id-ID" },
  TH: { currency: "THB", locale: "th-TH" },
  MY: { currency: "MYR", locale: "ms-MY" },
  IN: { currency: "INR", locale: "en-IN" },
  US: { currency: "USD", locale: "en-US" },
  DE: { currency: "EUR", locale: "de-DE" },
  BR: { currency: "BRL", locale: "pt-BR" },
  JP: { currency: "JPY", locale: "ja-JP" },
}

function methodLabel(m: string) {
  return METHOD_LABELS[m] ?? m
}

// checkoutTypeLabel 把 sessionType 前缀映射为可读的 Checkout 类型。
function checkoutTypeLabel(sessionType?: string): string {
  switch (sessionType) {
    case "oaics_":
      return "Custom Checkout"
    case "cs_live_":
      return "Stripe 正式"
    case "cs_test_":
      return "Stripe 测试"
    default:
      return sessionType ? sessionType.replace(/_$/, "") : "—"
  }
}

// entityLabel 把处理实体代码映射为可读名。
function entityLabel(entity?: string): string {
  switch (entity) {
    case "openai_llc":
      return "OpenAI LLC(美国)"
    case "openai_ie":
      return "OpenAI IE(爱尔兰)"
    case "stripe":
      return "Stripe"
    default:
      return entity || "—"
  }
}

// emailFromJWT 解析 access token(JWT)payload 里的 email 字段。
// ChatGPT AT 是标准 JWT(header.payload.signature),payload 含 https://api.openai.com/auth 等
// claim,邮箱常在 email / https://api.openai.com/profile.email。解析失败返回空串。
function emailFromJWT(token: string): string {
  try {
    const parts = token.trim().split(".")
    if (parts.length < 2) return ""
    const b64 = parts[1].replace(/-/g, "+").replace(/_/g, "/")
    const json = JSON.parse(decodeURIComponent(escape(atob(b64))))
    if (typeof json.email === "string" && json.email.includes("@")) return json.email
    // 兼容 namespaced claim
    for (const k of Object.keys(json)) {
      const v = json[k]
      if (typeof v === "object" && v && typeof v.email === "string" && v.email.includes("@")) {
        return v.email
      }
    }
    return ""
  } catch {
    return ""
  }
}

function formatAmount(minor: number | null, currency: string) {
  if (minor === null) return "金额未知"
  if (minor === 0) return "0 元"
  return `${(minor / 100).toFixed(2)} ${currency}`
}

function proxyURL(p: PaymentProxyItem): string {
  const scheme = p.scheme || "socks5"
  const auth = p.username ? `${p.username}:${p.password}@` : ""
  return `${scheme}://${auth}${p.host}:${p.port}`
}

// 线路单元格：金额+渠道 / 出口IP+纯度 / 状态。

// ── 线路点选器（djblook 式：点国家 → 点代理，免手写）─────────────────────────

interface RouteDraft {
  country: string
  currency: string
  locale: string
  proxies: string[] // 代理 URL 列表（从代理池点选）
}

function RoutePicker({
  routes,
  onChange,
}: {
  routes: RouteDraft[]
  onChange: (routes: RouteDraft[]) => void
}) {
  const [country, setCountry] = React.useState("VN")
  const proxiesQuery = usePaymentProxies()
  const allProxies = (proxiesQuery.data?.items ?? []).filter(
    (p) => p.status === "available",
  )
  const countriesInPool = React.useMemo(() => {
    const set = new Set(allProxies.map((p) => p.country).filter(Boolean))
    return Array.from(set).sort()
  }, [allProxies])
  const candidateProxies = allProxies.filter((p) => p.country === country)
  const preset = COUNTRY_PRESETS[country] ?? { currency: "USD", locale: "en-US" }
  const [picked, setPicked] = React.useState<Set<string>>(new Set())

  const addRoute = () => {
    if (picked.size === 0) {
      toast.error("请先点选至少一个代理")
      return
    }
    // 按 host:port 去重（同一代理不同认证串视为同一个），保留先到的 URL。
    const keyOf = (u: string) => {
      const m = u.match(/@([^@]+)$/)
      return m ? m[1] : u.replace(/^[a-z]+:\/\//, "")
    }
    const urls = candidateProxies.filter((p) => picked.has(p.id)).map(proxyURL)
    const idx = routes.findIndex((r) => r.country === country)
    const next = [...routes]
    if (idx >= 0) {
      const seen = new Set(next[idx].proxies.map(keyOf))
      const merged = [...next[idx].proxies]
      for (const u of urls) {
        if (!seen.has(keyOf(u))) {
          seen.add(keyOf(u))
          merged.push(u)
        }
      }
      next[idx] = { ...next[idx], proxies: merged }
    } else {
      const seen = new Set<string>()
      const uniq: string[] = []
      for (const u of urls) {
        if (!seen.has(keyOf(u))) {
          seen.add(keyOf(u))
          uniq.push(u)
        }
      }
      next.push({ country, currency: preset.currency, locale: preset.locale, proxies: uniq })
    }
    onChange(next)
    setPicked(new Set())
  }

  return (
    <div className="space-y-3">
      {/* 已选线路 */}
      {routes.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {routes.map((r) => (
            <Badge key={r.country} variant="secondary" className="gap-1.5 py-1">
              {r.country} · {r.currency} · {r.proxies.length} 个代理
              <button
                type="button"
                className="hover:text-foreground text-muted-foreground"
                onClick={() => onChange(routes.filter((x) => x.country !== r.country))}
              >
                <X className="size-3" />
              </button>
            </Badge>
          ))}
        </div>
      )}
      {/* 国家选择 */}
      <div className="flex items-end gap-2">
        <div className="grid gap-1.5">
          <span className="text-muted-foreground text-xs">国家/地区</span>
          <Select value={country} onValueChange={setCountry}>
            <SelectTrigger className="w-44">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {countriesInPool.length > 0 && (
                <>
                  {countriesInPool.map((c) => (
                    <SelectItem key={c} value={c}>
                      {c}（池中 {allProxies.filter((p) => p.country === c).length} 个）
                    </SelectItem>
                  ))}
                </>
              )}
              {Object.keys(COUNTRY_PRESETS)
                .filter((c) => !countriesInPool.includes(c))
                .map((c) => (
                  <SelectItem key={c} value={c}>
                    {c}（池中 0 个）
                  </SelectItem>
                ))}
            </SelectContent>
          </Select>
        </div>
        <div className="text-muted-foreground pb-2 text-xs">
          币种 {preset.currency} · 语言 {preset.locale}
        </div>
      </div>
      {/* 代理点选 */}
      <div className="rounded-md border">
        <div className="text-muted-foreground flex items-center justify-between border-b px-3 py-2 text-xs">
          <span>点选 {country} 线路代理（{candidateProxies.length} 个可用）</span>
          <Button
            variant="ghost"
            size="sm"
            className="h-6 text-xs"
            onClick={() =>
              setPicked(
                picked.size === candidateProxies.length
                  ? new Set()
                  : new Set(candidateProxies.map((p) => p.id)),
              )
            }
          >
            {picked.size === candidateProxies.length ? "全不选" : "全选"}
          </Button>
        </div>
        <div className="max-h-36 space-y-1 overflow-y-auto p-2">
          {candidateProxies.length === 0 && (
            <p className="text-muted-foreground py-3 text-center text-xs">
              该国家在代理池中没有可用代理——先到「邮箱池/代理池」导入
            </p>
          )}
          {candidateProxies.map((p) => (
            <label key={p.id} className="flex cursor-pointer items-center gap-2 text-xs">
              <Checkbox
                checked={picked.has(p.id)}
                onCheckedChange={(v) =>
                  setPicked((prev) => {
                    const next = new Set(prev)
                    if (v) next.add(p.id)
                    else next.delete(p.id)
                    return next
                  })
                }
              />
              <span className="font-mono">
                {p.host}:{p.port}
              </span>
              <span className="text-muted-foreground">{p.scheme}</span>
            </label>
          ))}
        </div>
        <div className="border-t p-2">
          <Button size="sm" variant="outline" className="w-full" onClick={addRoute}>
            <Plus /> 添加 {country} 线路（{picked.size} 个代理）
          </Button>
        </div>
      </div>
    </div>
  )
}

// ── 主页面 ────────────────────────────────────────────────────────────────────

export default function PaymentCheckPage() {
  const [params] = useSearchParams()
  const qc = useQueryClient()
  const presetIds = React.useMemo(
    () => (params.get("ids") ?? "").split(",").filter(Boolean),
    [params],
  )
  const preset = params.get("preset") ?? ""
  const [tab, setTab] = React.useState(presetIds.length > 0 || preset ? "pool" : "paste")

  // ── 账号池模式 ──
  const eligibleQuery = useAccounts({
    page: 1,
    pageSize: 500,
    promotion: preset === "eligible_untested" ? "untried_plus" : undefined,
    payment_status: preset === "eligible_untested" ? "unchecked" : undefined,
  })
  const allAccountsQuery = useAccounts({ page: 1, pageSize: 500 })
  const poolAccounts: AccountRecord[] = React.useMemo(() => {
    if (preset === "eligible_untested") return eligibleQuery.data?.items ?? []
    return allAccountsQuery.data?.items ?? []
  }, [preset, eligibleQuery.data, allAccountsQuery.data])

  const [poolSelected, setPoolSelected] = React.useState<Set<string>>(new Set(presetIds))
  React.useEffect(() => {
    if (presetIds.length > 0) setPoolSelected(new Set(presetIds))
  }, [presetIds])

  // ── 粘贴 AT 模式 ──
  const [rawTokens, setRawTokens] = React.useState("")
  const parsedTokens = React.useMemo(() => {
    return rawTokens
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean)
      .map((line) => {
        // 支持 "email----token" / "email,token" / 纯 token 三种粘贴格式。
        const sep = line.includes("----") ? "----" : line.includes(",") ? "," : null
        if (sep) {
          const [label, token] = line.split(sep, 2)
          const tok = (token ?? "").trim()
          // label 缺失时尝试从 JWT 解出邮箱(用户只粘 "----token" 的边角情况)。
          return { accessToken: tok, label: (label ?? "").trim() || emailFromJWT(tok) }
        }
        // 纯 token:用 JWT 解出邮箱做展示标签。
        return { accessToken: line, label: emailFromJWT(line) }
      })
      .filter((t) => t.accessToken.length > 0)
  }, [rawTokens])

  // ── 线路点选 ──
  const [routes, setRoutes] = React.useState<RouteDraft[]>([])

  // ── 批次状态 ──
  // polling 全程开启:运行中每 2s 拉状态,完成后也保持(便于看历史批次),由后端 running 控制真实请求频率。
  const [polling, setPolling] = React.useState(false)
  const batch = usePaymentBatchStatus(polling)
  const batchRunning = batch.data && "running" in batch.data ? batch.data.running : false
  // items(完成明细)只要有批次就拉取,运行中也会逐步累积完成项 → 完成一行立即显示一行。
  const hasBatch = batch.data != null && "total" in batch.data
  const batchItems = usePaymentBatchItems(hasBatch)
  React.useEffect(() => {
    setPolling(batchRunning)
    if (!batchRunning && batch.data && "done" in batch.data && batch.data.done > 0) {
      qc.invalidateQueries({ queryKey: ["accounts"] })
    }
  }, [batchRunning, batch.data, qc])

  // 完成明细按 id 索引(pool 模式 id=账号 id,paste 模式 id=AT id),供结果表按行匹配。
  const itemById = React.useMemo(() => {
    const m = new Map<string, PaymentBatchItem>()
    for (const it of batchItems.data?.items ?? []) m.set(it.id, it)
    return m
  }, [batchItems.data])

  // pool 模式账号 id→email(结果表 label 回退用,AT 未返回明细前也能显示邮箱)。
  const poolEmailById = React.useMemo(() => {
    const m = new Map<string, string>()
    for (const a of poolAccounts) m.set(a.id, a.email)
    return m
  }, [poolAccounts])

  const runCheck = useRunPaymentCheck()
  const cancelCheck = useCancelPaymentCheck()

  const effectiveIds = poolSelected
  const canRun =
    routes.length > 0 &&
    (tab === "pool" ? effectiveIds.size > 0 : parsedTokens.length > 0)

  const doRun = () => {
    if (routes.length === 0) {
      toast.error("请先添加至少一条检测线路")
      return
    }
    const routeSpecs: PaymentRouteSpec[] = routes.map((r) => ({
      country: r.country,
      currency: r.currency,
      locale: r.locale,
      proxies: r.proxies,
    }))
    if (tab === "paste") {
      if (parsedTokens.length === 0) {
        toast.error("请粘贴至少一条 Access Token")
        return
      }
      runCheck.mutate(
        { tokens: parsedTokens, routes: routeSpecs },
        {
          onSuccess: (r) => {
            toast.success(`批次已启动：${r.total} 条 AT`)
            setPolling(true)
          },
          onError: (e) => toast.error(e instanceof HttpError ? e.message : "启动失败"),
        },
      )
      return
    }
    if (effectiveIds.size === 0) {
      toast.error("请先在账号池勾选账号")
      return
    }
    runCheck.mutate(
      { ids: Array.from(effectiveIds), routes: routeSpecs, writeBack: true },
      {
        onSuccess: (r) => {
          toast.success(`批次已启动：${r.total} 个账号`)
          setPolling(true)
        },
        onError: (e) => toast.error(e instanceof HttpError ? e.message : "启动失败"),
      },
    )
  }

  const routeColumns = routes.map((r) => r.country)
  const accountState = (id: string): string => {
    if (batch.data && "accounts" in batch.data && batch.data.accounts) {
      return batch.data.accounts[id] ?? "pending"
    }
    return "pending"
  }
  const stateBadge = (state: string) => (
    <Badge
      variant={state === "done" ? "default" : state === "failed" ? "destructive" : "secondary"}
      className="text-xs"
    >
      {state === "done" ? "完成" : state === "failed" ? "失败" : state === "running" ? "检测中" : state === "skipped" ? "跳过" : "等待"}
    </Badge>
  )

  return (
    <div>
      <PageHeader
        title="支付类型检测"
        description="按区域线路创建优惠 Checkout（只建单不付款），读取 0 元试用金额与可用支付渠道，并记录检测出口 IP"
        actions={
          <>
            {batchRunning ? (
              <Button variant="destructive" size="sm" onClick={() => cancelCheck.mutate()}>
                <CircleStop /> 停止批次
              </Button>
            ) : (
              <Button size="sm" onClick={doRun} disabled={runCheck.isPending || !canRun}>
                <Play /> 开始检测（{tab === "pool" ? effectiveIds.size : parsedTokens.length}）
              </Button>
            )}
          </>
        }
      />

      <div className="grid gap-4 lg:grid-cols-[minmax(0,860px)]">
        {/* 左列:提交区(账号/粘贴 + 线路) */}
        <div className="space-y-4">
      <Tabs value={tab} onValueChange={setTab} className="">
        <TabsList>
          <TabsTrigger value="pool">账号池选择</TabsTrigger>
          <TabsTrigger value="paste">粘贴 Access Token</TabsTrigger>
        </TabsList>

        {/* ── Tab 1：账号池模式 ── */}
        <TabsContent value="pool" className="mt-3">
          <Card>
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">从账号池勾选（{effectiveIds.size} 个）</CardTitle>
              <CardDescription>
                {preset === "eligible_untested"
                  ? "当前筛选：有优惠资格但未测支付渠道的免费号"
                  : "勾选要检测的账号；检测结果写回账号池"}
              </CardDescription>
            </CardHeader>
            <CardContent>
              <div className="max-h-48 space-y-1 overflow-y-auto rounded-md border p-2">
                {poolAccounts.map((a) => (
                  <label key={a.id} className="flex cursor-pointer items-center gap-2 text-sm">
                    <Checkbox
                      checked={effectiveIds.has(a.id)}
                      onCheckedChange={(v) =>
                        setPoolSelected((prev) => {
                          const next = new Set(prev)
                          if (v) next.add(a.id)
                          else next.delete(a.id)
                          return next
                        })
                      }
                    />
                    <span className="truncate font-mono text-xs">{a.email}</span>
                    {a.paymentMethods.length > 0 && (
                      <span className="text-muted-foreground text-xs">
                        （已测：{a.paymentMethods.join("/")}）
                      </span>
                    )}
                  </label>
                ))}
                {poolAccounts.length === 0 && (
                  <p className="text-muted-foreground py-4 text-center text-xs">
                    账号池为空——先到「账号池」录入，或切到「粘贴 Access Token」
                  </p>
                )}
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        {/* ── Tab 2：粘贴 AT 模式 ── */}
        <TabsContent value="paste" className="mt-3">
          <Card>
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">粘贴 Access Token（{parsedTokens.length} 条）</CardTitle>
              <CardDescription>
                一行一条；支持「邮箱----token」「邮箱,token」或纯 token；仅本次检测使用，不写入账号池
              </CardDescription>
            </CardHeader>
            <CardContent>
              <Textarea
                value={rawTokens}
                onChange={(e) => setRawTokens(e.target.value)}
                rows={7}
                className="max-h-44 resize-none overflow-y-auto font-mono text-xs"
                placeholder={"user@example.com----eyJhbGciOi...\nuser2@example.com,eyJhbGciOi...\neyJhbGciOi...（纯 token）"}
              />
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      {/* 线路点选器（两模式共用） */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">检测线路（点选国家 + 点选代理）</CardTitle>
          <CardDescription>
            每条线路 = 一个国家 + 该国代理池；同国家多次添加会合并代理
          </CardDescription>
        </CardHeader>
        <CardContent>
          <RoutePicker routes={routes} onChange={setRoutes} />
        </CardContent>
      </Card>
        </div>

        {/* 右列:检测结果(常驻,并发实时) */}
      </div>
        <div className="min-w-0">
      <Card>
        <CardHeader className="pb-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <CardTitle className="text-sm">检测结果</CardTitle>
              <CardDescription>
                {!hasBatch
                  ? "配置好上方账号与线路后,点「开始检测」,这里会实时显示每个账号的结果"
                  : batchRunning
                    ? "并发检测中,完成一行填一行…"
                    : "本批次检测完成"}
              </CardDescription>
            </div>
            {hasBatch && batch.data && "total" in batch.data && (
              <div className="flex flex-wrap items-center gap-x-5 gap-y-1 text-sm">
                <span className="flex items-center gap-1.5">
                  {batchRunning && <Loader2 className="size-4 animate-spin text-primary" />}
                  进度 <strong>{batch.data.done}/{batch.data.total}</strong>
                </span>
                <span>跳过 <strong>{batch.data.skipped}</strong></span>
                <span className="flex items-center gap-1">
                  <ShieldCheck className="size-4 text-emerald-500" />
                  0 元有渠道 <strong className="text-emerald-600">{batch.data.zeroCount}</strong>
                </span>
                <span className={batchRunning ? "font-medium text-primary" : "text-muted-foreground"}>
                  {batchRunning ? "运行中" : batch.data.canceled ? "已取消" : "已完成"}
                </span>
              </div>
            )}
          </div>
          {/* 进度条(仅有批次时) */}
          {hasBatch && batch.data && "total" in batch.data && (
            <div className="bg-muted mt-2 h-1.5 w-full overflow-hidden rounded-full">
              <div
                className="bg-primary h-full rounded-full transition-all duration-500"
                style={{ width: `${batch.data.total > 0 ? (batch.data.done / batch.data.total) * 100 : 0}%` }}
              />
            </div>
          )}
        </CardHeader>
        <CardContent>
          {!hasBatch || !(batch.data && "accounts" in batch.data) ? (
            <div className="text-muted-foreground flex flex-col items-center justify-center gap-2 py-12 text-sm">
              <CreditCard className="size-8 opacity-40" />
              <p>暂无检测任务</p>
              <p className="text-xs">检测结果会显示在这里:账号 / 状态 / 各线路金额与支付渠道 / 出口 IP</p>
            </div>
          ) : (
            <Table className="w-full table-auto">
              <TableHeader>
                <TableRow>
                  <TableHead className="min-w-52">账号</TableHead>
                  <TableHead className="w-24">状态</TableHead>
                  <TableHead className="w-16">线路</TableHead>
                  <TableHead className="w-32">Checkout 类型</TableHead>
                  <TableHead className="w-28">出口地区</TableHead>
                  <TableHead>支付方式</TableHead>
                  <TableHead className="w-28">金额</TableHead>
                  <TableHead className="w-36">处理实体</TableHead>
                  <TableHead className="w-40">检查时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {Object.keys(batch.data.accounts ?? {}).flatMap((id) => {
                  const state = accountState(id)
                  const item = itemById.get(id)
                  const label = item?.label || poolEmailById.get(id) || id
                  const running = state !== "done" && state !== "failed" && state !== "skipped"
                  // 该账号要跑的线路:优先已返回的 routes 键,否则用用户点选的 routeColumns。
                  const itemRouteKeys = Object.keys(item?.routes ?? {})
                  const cols = itemRouteKeys.length > 0 ? itemRouteKeys : routeColumns
                  if (running) {
                    // 运行中:一行占位(跨列显示检测中/排队中)。
                    return [
                      <TableRow key={id}>
                        <TableCell className="font-mono text-xs">{label}</TableCell>
                        <TableCell>{stateBadge(state)}</TableCell>
                        <TableCell colSpan={7}>
                          <span className="text-muted-foreground flex items-center gap-1.5 text-xs">
                            {state === "running" ? (
                              <><Loader2 className="size-3.5 animate-spin text-primary" /> 检测中</>
                            ) : (
                              "排队中"
                            )}
                          </span>
                        </TableCell>
                      </TableRow>,
                    ]
                  }
                  // 已完成:每条线路一行。
                  return cols.map((c) => {
                    const r = item?.routes?.[c]
                    const methods = [...(r?.methods ?? []), ...(r?.methodsInferred ?? [])]
                    return (
                      <TableRow key={`${id}-${c}`}>
                        <TableCell className="font-mono text-xs">{label}</TableCell>
                        <TableCell>{stateBadge(state)}</TableCell>
                        <TableCell>
                          <Badge variant="outline" className="text-xs">{c}</Badge>
                        </TableCell>
                        <TableCell className="text-xs">{checkoutTypeLabel(r?.sessionType)}</TableCell>
                        <TableCell className="font-mono text-xs">
                          {r?.exit || r?.exitIp ? (
                            <span className="flex items-center gap-1">
                              {r?.exit || r?.exitIp}
                              {r?.exitPurity && (
                                <Badge
                                  variant={r.exitPurity === "clean" ? "secondary" : "destructive"}
                                  className="text-[10px]"
                                >
                                  {r.exitPurity === "clean" ? "干净" : "机房"}
                                </Badge>
                              )}
                            </span>
                          ) : "—"}
                        </TableCell>
                        <TableCell>
                          {methods.length > 0 ? (
                            <div className="flex flex-wrap gap-1">
                              {methods.map((m) => (
                                <Badge key={m} variant="outline" className="text-xs">
                                  {methodLabel(m)}
                                  {(r?.methodsInferred ?? []).includes(m) ? "(推断)" : ""}
                                </Badge>
                              ))}
                            </div>
                          ) : (
                            <span className="text-muted-foreground text-xs">
                              {r ? (STATUS_LABELS[r.status] ?? r.status) : "—"}
                            </span>
                          )}
                        </TableCell>
                        <TableCell className="text-xs">
                          {r ? (
                            <Badge variant={r.zeroStatus === "zero_confirmed" ? "default" : "secondary"} className="text-xs">
                              {formatAmount(r.amountDue, r.currency)}
                            </Badge>
                          ) : "—"}
                        </TableCell>
                        <TableCell className="text-xs">{entityLabel(r?.processorEntity)}</TableCell>
                        <TableCell className="text-muted-foreground text-xs">
                          {r?.checkedAt ? formatTime(r.checkedAt) : "—"}
                        </TableCell>
                      </TableRow>
                    )
                  })
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
        </div>
    </div>
  )
}