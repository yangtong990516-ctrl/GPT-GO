import * as React from "react"
import { toast } from "sonner"
import { CheckCircle2, MessagesSquare, PlugZap, Server, Wallet, XCircle } from "lucide-react"
import { PageHeader } from "@/components/page-header"
import { EmptyState } from "@/components/data-table"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  useCreateMailcodeMailboxes,
  useCreateRemailMailboxes,
  useImportRemailOrders,
  useMailcodeConfig,
  useProbeMailcode,
  useProbeRemail,
  useRemailConfig,
  useRemailWallet,
  useSaveMailcodeConfig,
  useSaveRemailConfig,
} from "@/lib/queries"
import { HttpError } from "@/lib/api"
import { formatTime } from "@/lib/format"

function ProbeBadge({ result }: { result: { ok: boolean; message: string; reachable: boolean } | undefined }) {
  if (!result) return null
  return result.ok ? (
    <Badge variant="secondary" className="gap-1 text-emerald-600 dark:text-emerald-400">
      <CheckCircle2 className="size-3" /> {result.message || "连接正常"}
    </Badge>
  ) : (
    <Badge variant="destructive" className="gap-1">
      <XCircle className="size-3" /> {result.message || "连接失败"}
    </Badge>
  )
}

function MailboxResultTable({
  records,
}: {
  records: { email: string; imported: boolean; duplicate: boolean; error: string | null; orderNo?: string }[]
}) {
  if (records.length === 0) return null
  return (
    <div className="mt-4 rounded-lg border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>邮箱</TableHead>
            {"orderNo" in (records[0] ?? {}) && <TableHead>订单号</TableHead>}
            <TableHead>结果</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {records.map((r) => (
            <TableRow key={r.email + (r.orderNo ?? "")}>
              <TableCell className="font-mono text-xs">{r.email}</TableCell>
              {"orderNo" in (records[0] ?? {}) && (
                <TableCell className="font-mono text-xs">{(r as { orderNo?: string }).orderNo || "-"}</TableCell>
              )}
              <TableCell>
                {r.error ? (
                  <Badge variant="destructive">{r.error}</Badge>
                ) : r.imported ? (
                  <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
                    已导入邮箱池
                  </Badge>
                ) : r.duplicate ? (
                  <Badge variant="outline">重复跳过</Badge>
                ) : (
                  <Badge variant="secondary">已创建</Badge>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

// ── Mailcode tab ──────────────────────────────────────────────────────────────

function MailcodeSection() {
  const config = useMailcodeConfig()
  const saveConfig = useSaveMailcodeConfig()
  const probe = useProbeMailcode()
  const create = useCreateMailcodeMailboxes()

  const [baseUrl, setBaseUrl] = React.useState("")
  const [domain, setDomain] = React.useState("")
  const [count, setCount] = React.useState(5)
  const [prefix, setPrefix] = React.useState("")

  React.useEffect(() => {
    if (config.data) {
      setBaseUrl(config.data.baseUrl || "")
      setDomain(config.data.domain || "")
    }
  }, [config.data])

  if (config.isPending) return <Skeleton className="h-64 w-full" />

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">服务配置</CardTitle>
          <CardDescription>
            自建 Mailcow + mailcode API 服务地址与默认域名
            {config.data?.updatedAt && `（更新于 ${formatTime(config.data.updatedAt)}）`}
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4">
          <div className="grid gap-2">
            <Label htmlFor="mc-url">Base URL（https://）</Label>
            <Input
              id="mc-url"
              placeholder="https://mail.example.com"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="mc-domain">邮箱域名</Label>
            <Input
              id="mc-domain"
              placeholder="example.com"
              value={domain}
              onChange={(e) => setDomain(e.target.value)}
            />
          </div>
          <div className="flex items-center gap-2">
            <ProbeBadge result={probe.data} />
          </div>
        </CardContent>
        <CardFooter className="gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={!baseUrl || probe.isPending}
            onClick={() =>
              probe.mutate(baseUrl, {
                onError: (e) => toast.error(e instanceof HttpError ? e.message : "探测失败"),
              })
            }
          >
            <PlugZap /> {probe.isPending ? "探测中…" : "测试连接"}
          </Button>
          <Button
            size="sm"
            disabled={!baseUrl.startsWith("https://") || saveConfig.isPending}
            onClick={() =>
              saveConfig.mutate(
                { baseUrl, domain },
                {
                  onSuccess: () => toast.success("Mailcode 配置已保存"),
                  onError: (e) => toast.error(e instanceof HttpError ? e.message : "保存失败"),
                },
              )
            }
          >
            {saveConfig.isPending ? "保存中…" : "保存配置"}
          </Button>
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">批量开通邮箱</CardTitle>
          <CardDescription>按数量自动生成邮箱（0 = 不生成）</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4">
          <div className="grid grid-cols-2 gap-3">
            <div className="grid gap-2">
              <Label htmlFor="mc-count">数量（0-200）</Label>
              <Input
                id="mc-count"
                type="number"
                min={0}
                max={200}
                value={count}
                onChange={(e) => setCount(Number(e.target.value))}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="mc-prefix">前缀（可空）</Label>
              <Input
                id="mc-prefix"
                placeholder="reg"
                maxLength={64}
                value={prefix}
                onChange={(e) => setPrefix(e.target.value)}
              />
            </div>
          </div>
        </CardContent>
        <CardFooter>
          <Button
            size="sm"
            disabled={count < 1 || count > 200 || create.isPending}
            onClick={() =>
              create.mutate(
                { emails: [], count, prefix, domain: domain || "" },
                {
                  onSuccess: (records) => {
                    const ok = records.filter((r) => r.imported).length
                    toast.success(`开通完成：${ok} 个已导入邮箱池`)
                  },
                  onError: (e) => toast.error(e instanceof HttpError ? e.message : "开通失败"),
                },
              )
            }
          >
            {create.isPending ? "开通中…" : `开通 ${count} 个邮箱`}
          </Button>
        </CardFooter>
        {create.data && (
          <CardContent>
            <MailboxResultTable records={create.data} />
          </CardContent>
        )}
      </Card>
    </div>
  )
}

// ── Remail tab ────────────────────────────────────────────────────────────────

function RemailSection() {
  const config = useRemailConfig()
  const saveConfig = useSaveRemailConfig()
  const probe = useProbeRemail()
  const wallet = useRemailWallet(true)
  const create = useCreateRemailMailboxes()
  const importOrders = useImportRemailOrders()

  const [apiKey, setApiKey] = React.useState("")
  const [projectId, setProjectId] = React.useState(2)
  const [emailSuffix, setEmailSuffix] = React.useState("icloud.com")
  const [remailBaseUrl, setRemailBaseUrl] = React.useState("")
  const [count, setCount] = React.useState(5)
  const [importLimit, setImportLimit] = React.useState(20)
  const [productType, setProductType] = React.useState("icloud")

  React.useEffect(() => {
    if (config.data) {
      setApiKey(config.data.apiKey || "")
      setProjectId(config.data.projectId ?? 2)
      setEmailSuffix(config.data.emailSuffix || "icloud.com")
      setRemailBaseUrl(config.data.baseUrl || "")
    }
  }, [config.data])

  if (config.isPending) return <Skeleton className="h-64 w-full" />

  return (
    <div className="space-y-4">
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">平台配置</CardTitle>
            <CardDescription>
              ReMail 接码平台 API Key 与项目
              {config.data?.updatedAt && `（更新于 ${formatTime(config.data.updatedAt)}）`}
            </CardDescription>
          </CardHeader>
          <CardContent className="grid gap-4">
            <div className="grid gap-2">
              <Label htmlFor="rm-key">API Key</Label>
              <Input
                id="rm-key"
                type="password"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="grid gap-2">
                <Label htmlFor="rm-pid">项目 ID</Label>
                <Input
                  id="rm-pid"
                  type="number"
                  min={1}
                  value={projectId}
                  onChange={(e) => setProjectId(Number(e.target.value))}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="rm-suffix">邮箱后缀</Label>
                <Input
                  id="rm-suffix"
                  value={emailSuffix}
                  onChange={(e) => setEmailSuffix(e.target.value)}
                />
              </div>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="rm-url">Base URL（https://）</Label>
              <Input
                id="rm-url"
                value={remailBaseUrl}
                onChange={(e) => setRemailBaseUrl(e.target.value)}
              />
            </div>
            <div className="flex items-center gap-2">
              <ProbeBadge result={probe.data} />
            </div>
          </CardContent>
          <CardFooter className="gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={!apiKey || probe.isPending}
              onClick={() =>
                probe.mutate(apiKey, {
                  onError: (e) => toast.error(e instanceof HttpError ? e.message : "探测失败"),
                })
              }
            >
              <PlugZap /> {probe.isPending ? "探测中…" : "测试连接"}
            </Button>
            <Button
              size="sm"
              disabled={
                apiKey.length < 8 || !remailBaseUrl.startsWith("https://") || saveConfig.isPending
              }
              onClick={() =>
                saveConfig.mutate(
                  { apiKey, projectId, emailSuffix, baseUrl: remailBaseUrl },
                  {
                    onSuccess: () => toast.success("Remail 配置已保存"),
                    onError: (e) => toast.error(e instanceof HttpError ? e.message : "保存失败"),
                  },
                )
              }
            >
              {saveConfig.isPending ? "保存中…" : "保存配置"}
            </Button>
          </CardFooter>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <Wallet className="size-4" /> 钱包余额
            </CardTitle>
            <CardDescription>ReMail 账户资金概况</CardDescription>
          </CardHeader>
          <CardContent>
            {wallet.isPending ? (
              <Skeleton className="h-20 w-full" />
            ) : wallet.isError || !wallet.data?.ok ? (
              <EmptyState
                title="无法获取钱包信息"
                description={wallet.data?.message || "请先保存有效的 API Key 后重试。"}
              />
            ) : (
              <div className="grid grid-cols-3 gap-3">
                <div>
                  <p className="text-muted-foreground text-xs">可用余额</p>
                  <p className="text-xl font-semibold tabular-nums">
                    ¥ {wallet.data.consumerBalance.toFixed(2)}
                  </p>
                </div>
                <div>
                  <p className="text-muted-foreground text-xs">累计充值</p>
                  <p className="text-xl font-semibold tabular-nums">
                    ¥ {wallet.data.totalRecharged.toFixed(2)}
                  </p>
                </div>
                <div>
                  <p className="text-muted-foreground text-xs">历史消费</p>
                  <p className="text-xl font-semibold tabular-nums">
                    ¥ {wallet.data.historicalSpend.toFixed(2)}
                  </p>
                </div>
              </div>
            )}
          </CardContent>
          <CardFooter>
            <Button variant="ghost" size="sm" onClick={() => wallet.refetch()}>
              刷新余额
            </Button>
          </CardFooter>
        </Card>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">购买新邮箱</CardTitle>
            <CardDescription>调用 Remail 下单购买（1-1000 个）</CardDescription>
          </CardHeader>
          <CardContent className="grid gap-2">
            <Label htmlFor="rm-count">数量</Label>
            <Input
              id="rm-count"
              type="number"
              min={1}
              max={1000}
              value={count}
              onChange={(e) => setCount(Number(e.target.value))}
            />
          </CardContent>
          <CardFooter>
            <Button
              size="sm"
              disabled={count < 1 || count > 1000 || create.isPending}
              onClick={() =>
                create.mutate(
                  { count },
                  {
                    onSuccess: (records) => {
                      const ok = records.filter((r) => r.imported).length
                      toast.success(`购买完成：${ok} 个已导入邮箱池`)
                    },
                    onError: (e) => toast.error(e instanceof HttpError ? e.message : "购买失败"),
                  },
                )
              }
            >
              {create.isPending ? "下单中…" : `购买 ${count} 个`}
            </Button>
          </CardFooter>
          {create.data && (
            <CardContent>
              <MailboxResultTable records={create.data} />
            </CardContent>
          )}
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">导入已购订单</CardTitle>
            <CardDescription>把历史订单中的邮箱导入邮箱池</CardDescription>
          </CardHeader>
          <CardContent className="grid grid-cols-2 gap-3">
            <div className="grid gap-2">
              <Label htmlFor="rm-limit">每页数量（1-100）</Label>
              <Input
                id="rm-limit"
                type="number"
                min={1}
                max={100}
                value={importLimit}
                onChange={(e) => setImportLimit(Number(e.target.value))}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="rm-ptype">产品类型</Label>
              <Input
                id="rm-ptype"
                placeholder="icloud / microsoft / gmail"
                value={productType}
                onChange={(e) => setProductType(e.target.value)}
              />
            </div>
          </CardContent>
          <CardFooter>
            <Button
              size="sm"
              variant="outline"
              disabled={importOrders.isPending}
              onClick={() =>
                importOrders.mutate(
                  {
                    limit: importLimit,
                    maxOrders: 0,
                    productType,
                    emailSuffix: "",
                    onlyIcloud: productType === "icloud",
                  },
                  {
                    onSuccess: (records) => {
                      const ok = records.filter((r) => r.imported).length
                      toast.success(`导入完成：${ok} 个已入池`)
                    },
                    onError: (e) => toast.error(e instanceof HttpError ? e.message : "导入失败"),
                  },
                )
              }
            >
              {importOrders.isPending ? "导入中…" : "导入已购订单"}
            </Button>
          </CardFooter>
          {importOrders.data && (
            <CardContent>
              <MailboxResultTable records={importOrders.data} />
            </CardContent>
          )}
        </Card>
      </div>
    </div>
  )
}

export default function MailboxesPage() {
  return (
    <div className="space-y-8">
      <PageHeader
        title="邮箱开通"
        description="通过自建 Mailcow（Mailcode）或 ReMail 接码平台批量开通注册邮箱"
      />

      <section>
        <h2 className="mb-1 flex items-center gap-2 text-base font-semibold">
          <Server className="size-4" /> Mailcode 自建邮局
        </h2>
        <p className="text-muted-foreground mb-4 text-sm">
          对接自建 Mailcow 邮局，按数量自动生成邮箱并写入邮箱池
        </p>
        <MailcodeSection />
      </section>

      <Separator />

      <section>
        <h2 className="mb-1 flex items-center gap-2 text-base font-semibold">
          <MessagesSquare className="size-4" /> Remail 接码平台
        </h2>
        <p className="text-muted-foreground mb-4 text-sm">
          对接第三方接码平台，购买新邮箱或导入历史订单
        </p>
        <RemailSection />
      </section>
    </div>
  )
}
