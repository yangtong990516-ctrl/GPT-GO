import * as React from "react"
import { toast } from "sonner"
import { Globe, RefreshCw, Save, ShieldCheck } from "lucide-react"
import { PageHeader } from "@/components/page-header"
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
import { Switch } from "@/components/ui/switch"
import {
  useExecutionSettings,
  useRunSentinelCheck,
  useSaveExecutionSettings,
  useSaveSentinelConfig,
  useSentinelConfig,
  useSentinelVersion,
} from "@/lib/queries"
import { HttpError } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { ProxiesSection } from "@/pages/proxies"

// ── Execution settings card ───────────────────────────────────────────────────

function ExecutionSettingsCard() {
  const settings = useExecutionSettings()
  const save = useSaveExecutionSettings()
  const [form, setForm] = React.useState({
    requireRegistrationPassword: false,
    enableRegistrationTotp: true,
    requireTrialOnCheck: false,
    autoMultiCountryProbe: false,
    registrationMode: "protocol",
    proxyRetryCount: 1,
    proxyCheckConcurrency: 16,
    maxRegistrationsPerExitIp: 0,
    concurrency: 2,
    taskTimeoutSeconds: 0,
  })

  React.useEffect(() => {
    if (settings.data) {
      const d = settings.data
      setForm({
        requireRegistrationPassword: d.requireRegistrationPassword,
        enableRegistrationTotp: d.enableRegistrationTotp,
        requireTrialOnCheck: d.requireTrialOnCheck,
        autoMultiCountryProbe: d.autoMultiCountryProbe,
        registrationMode: d.registrationMode,
        proxyRetryCount: d.proxyRetryCount,
        proxyCheckConcurrency: d.proxyCheckConcurrency,
        maxRegistrationsPerExitIp: d.maxRegistrationsPerExitIp,
        concurrency: d.concurrency,
        taskTimeoutSeconds: d.taskTimeoutSeconds,
      })
    }
  }, [settings.data])

  if (settings.isPending) return <Skeleton className="h-96 w-full" />

  const submit = () => {
    save.mutate(form, {
      onSuccess: () => toast.success("执行参数已保存"),
      onError: (e) => toast.error(e instanceof HttpError ? e.message : "保存失败"),
    })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">注册执行参数</CardTitle>
        <CardDescription>
          控制注册流水线的并发、重试与安全策略
          {settings.data?.updatedAt && `（更新于 ${formatRelative(settings.data.updatedAt)}）`}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <div>
              <Label>注册必须设置密码</Label>
              <p className="text-muted-foreground text-xs">关闭时允许无密码注册流程</p>
            </div>
            <Switch
              checked={form.requireRegistrationPassword}
              onCheckedChange={(v) => setForm({ ...form, requireRegistrationPassword: v })}
            />
          </div>
          <Separator />
          <div className="flex items-center justify-between">
            <div>
              <Label>注册时启用 TOTP 2FA</Label>
              <p className="text-muted-foreground text-xs">为每个新账号自动绑定 2FA</p>
            </div>
            <Switch
              checked={form.enableRegistrationTotp}
              onCheckedChange={(v) => setForm({ ...form, enableRegistrationTotp: v })}
            />
          </div>
          <Separator />
          <div className="flex items-center justify-between">
            <div>
              <Label>检测时必须含试用资格</Label>
              <p className="text-muted-foreground text-xs">无试用资格的账号视为检测失败</p>
            </div>
            <Switch
              checked={form.requireTrialOnCheck}
              onCheckedChange={(v) => setForm({ ...form, requireTrialOnCheck: v })}
            />
          </div>
          <Separator />
          <div className="flex items-center justify-between">
            <div>
              <Label>自动多国探测</Label>
              <p className="text-muted-foreground text-xs">自动执行多国试用资格扫描</p>
            </div>
            <Switch
              checked={form.autoMultiCountryProbe}
              onCheckedChange={(v) => setForm({ ...form, autoMultiCountryProbe: v })}
            />
          </div>
        </div>

        <Separator />

        <div className="grid grid-cols-2 gap-4 md:grid-cols-3">
          <div className="grid gap-2">
            <Label htmlFor="st-retry">代理重试次数（0-5）</Label>
            <Input
              id="st-retry"
              type="number"
              min={0}
              max={5}
              value={form.proxyRetryCount}
              onChange={(e) => setForm({ ...form, proxyRetryCount: Number(e.target.value) })}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="st-pcc">代理检测并发（1-32）</Label>
            <Input
              id="st-pcc"
              type="number"
              min={1}
              max={32}
              value={form.proxyCheckConcurrency}
              onChange={(e) =>
                setForm({ ...form, proxyCheckConcurrency: Number(e.target.value) })
              }
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="st-maxip">单出口 IP 上限（0-32）</Label>
            <Input
              id="st-maxip"
              type="number"
              min={0}
              max={32}
              value={form.maxRegistrationsPerExitIp}
              onChange={(e) =>
                setForm({ ...form, maxRegistrationsPerExitIp: Number(e.target.value) })
              }
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="st-conc">注册并发（1-12）</Label>
            <Input
              id="st-conc"
              type="number"
              min={1}
              max={12}
              value={form.concurrency}
              onChange={(e) => setForm({ ...form, concurrency: Number(e.target.value) })}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="st-timeout">任务超时（秒，0=不限）</Label>
            <Input
              id="st-timeout"
              type="number"
              min={0}
              value={form.taskTimeoutSeconds}
              onChange={(e) =>
                setForm({ ...form, taskTimeoutSeconds: Number(e.target.value) })
              }
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="st-mode">注册模式</Label>
            <Input id="st-mode" value={form.registrationMode} disabled />
          </div>
        </div>
      </CardContent>
      <CardFooter>
        <Button size="sm" onClick={submit} disabled={save.isPending}>
          <Save /> {save.isPending ? "保存中…" : "保存执行参数"}
        </Button>
      </CardFooter>
    </Card>
  )
}

// ── Sentinel card ─────────────────────────────────────────────────────────────

function SentinelCard() {
  const config = useSentinelConfig()
  const version = useSentinelVersion()
  const save = useSaveSentinelConfig()
  const runCheck = useRunSentinelCheck()
  const [enabled, setEnabled] = React.useState(true)
  const [intervalHours, setIntervalHours] = React.useState(24)
  const [proxy, setProxy] = React.useState("")

  React.useEffect(() => {
    if (config.data) {
      setEnabled(config.data.enabled)
      setIntervalHours(config.data.interval_hours)
      setProxy(config.data.proxy || "")
    }
  }, [config.data])

  const v = version.data

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <ShieldCheck className="size-4" /> Sentinel 版本监控
        </CardTitle>
        <CardDescription>定时检测 CDK SDK 上游版本是否过期</CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        {config.isPending ? (
          <Skeleton className="h-24 w-full" />
        ) : (
          <>
            <div className="flex items-center justify-between">
              <div>
                <Label>启用自动检查</Label>
                <p className="text-muted-foreground text-xs">保存后立即生效，无需重启</p>
              </div>
              <Switch checked={enabled} onCheckedChange={setEnabled} />
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div className="grid gap-2">
                <Label htmlFor="sn-interval">检查间隔（小时）</Label>
                <Input
                  id="sn-interval"
                  type="number"
                  min={1}
                  value={intervalHours}
                  onChange={(e) => setIntervalHours(Number(e.target.value))}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="sn-proxy">固定代理（可空）</Label>
                <Input
                  id="sn-proxy"
                  placeholder="socks5://127.0.0.1:7890"
                  className="font-mono text-xs"
                  value={proxy}
                  onChange={(e) => setProxy(e.target.value)}
                />
              </div>
            </div>
          </>
        )}

        <Separator />

        <div className="space-y-2 text-sm">
          <div className="flex items-center justify-between">
            <span className="text-muted-foreground">内置版本</span>
            <Badge variant="secondary" className="font-mono">
              {v?.configured_version ?? "…"}
            </Badge>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-muted-foreground">上游最新版本</span>
            <span className="font-mono text-xs">{v?.version || "未检测"}</span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-muted-foreground">CDN 可达性</span>
            {v?.reachable ? (
              <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
                可达
              </Badge>
            ) : (
              <Badge variant="destructive">不可达</Badge>
            )}
          </div>
          <div className="flex items-center justify-between">
            <span className="text-muted-foreground">版本状态</span>
            {v?.is_expired === true ? (
              <Badge variant="destructive">已过期</Badge>
            ) : v?.is_expired === false ? (
              <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
                最新
              </Badge>
            ) : (
              <Badge variant="outline">未知</Badge>
            )}
          </div>
          <div className="flex items-center justify-between">
            <span className="text-muted-foreground">最近检查</span>
            <span className="text-xs">{formatRelative(v?.last_checked_at)}</span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-muted-foreground">ETag / 大小</span>
            <span className="font-mono text-xs">
              {v?.etag ?? "-"} / {v?.content_length != null ? `${v.content_length} B` : "-"}
            </span>
          </div>
          {v?.check_error && (
            <p className="text-destructive text-xs">检查错误：{v.check_error}</p>
          )}
        </div>
      </CardContent>
      <CardFooter className="gap-2">
        <Button
          size="sm"
          disabled={save.isPending}
          onClick={() =>
            save.mutate(
              { enabled, interval_hours: intervalHours, proxy },
              {
                onSuccess: () => toast.success("Sentinel 配置已保存并生效"),
                onError: (e) => toast.error(e instanceof HttpError ? e.message : "保存失败"),
              },
            )
          }
        >
          <Save /> {save.isPending ? "保存中…" : "保存配置"}
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={runCheck.isPending}
          onClick={() =>
            runCheck.mutate(undefined, {
              onSuccess: (d) =>
                d.is_expired || d.check_error
                  ? toast.warning("检测完成:版本可能已过期", {
                      description: d.check_error ?? `HTTP 探测返回异常`,
                    })
                  : toast.success("检测完成:版本有效", {
                      description: `走代理 ${d.proxy_used ? "是" : "否"} · 可达 ${d.reachable ? "是" : "否"}`,
                    }),
              onError: (e) =>
                toast.error("检测失败", {
                  description: e instanceof Error ? e.message : "请稍后重试",
                }),
            })
          }
        >
          <RefreshCw className={runCheck.isPending ? "animate-spin" : ""} />
          {runCheck.isPending ? "探测中…" : "立即检测版本"}
        </Button>
      </CardFooter>
    </Card>
  )
}

export default function SettingsPage() {
  return (
    <div className="space-y-8">
      <PageHeader title="系统设置" description="执行参数、代理池与 Sentinel 版本监控配置" />

      <div className="grid gap-4 xl:grid-cols-2">
        <ExecutionSettingsCard />
        <SentinelCard />
      </div>

      <Separator />

      <section>
        <h2 className="mb-1 flex items-center gap-2 text-base font-semibold">
          <Globe className="size-4" /> 代理池
        </h2>
        <p className="text-muted-foreground mb-4 text-sm">
          出口代理的导入、分组、连通性测试与租约管理
        </p>
        <ProxiesSection />
      </section>
    </div>
  )
}
