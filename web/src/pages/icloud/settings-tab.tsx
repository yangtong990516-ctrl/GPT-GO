// 系统设置 tab:本地数据/公共访问/后台能力/iCloud Web API/Server酱/公共 API keys/版本与更新。
import * as React from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  Database,
  Eye,
  EyeOff,
  ExternalLink,
  Globe2,
  HardDriveDownload,
  KeyRound,
  RefreshCw,
  Save,
  Send,
  ShieldCheck,
  Wrench,
} from "lucide-react"
import { icloudApi } from "@/lib/icloud-api"
import type { ICloudSettings } from "@/lib/icloud-api"
import {
  useICloudDatabaseAction,
  useICloudDatabaseStatus,
  useICloudSaveSettings,
  useICloudServerChanTest,
  useICloudSettings,
  useICloudUpdateStatus,
} from "@/lib/icloud-queries"
import { TableSkeleton } from "@/components/data-table"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"
import { Switch } from "@/components/ui/switch"
import { errMsg } from "./shared"

// ── 默认值 ───────────────────────────────────────────────────────────────────

const DEFAULT_FORM: ICloudSettings = {
  enable_mail_watcher: false,
  enable_apple_keep_alive: false,
  enable_public_mailbox_api: false,
  enable_public_code_page: false,
  enable_web_code_sync: false,
  enable_web_manual_mail_sync: true,
  enable_web_background_mail: true,
  enable_web_remote_mail_cleanup: true,
  public_api_key: "",
  apple_account_module_ready: true,
  server_chan_send_key: "",
  server_chan_hide_ip: true,
  notify_admin_login: false,
  notify_account_login_state_offline: false,
}

// ── 格式化辅助 ────────────────────────────────────────────────────────────────

// 后端 /update/status 的真实返回结构(见 updatecheck.Status,字段比
// icloud-api.ts 里的 ICloudUpdateStatus 声明更全)。
interface UpdateStatusInfo {
  enabled?: boolean
  repository_url?: string
  current?: { version?: string; commit?: string; os?: string; arch?: string }
  latest?: { name?: string; notes?: string; url?: string; version?: string }
  update_available?: boolean
  has_update?: boolean
  release_url?: string
  release_notes?: string
  latest_version?: string
  checked_at?: string
  error?: string
}

function formatBytes(n?: number): string {
  if (n === undefined || n === null || Number.isNaN(n)) return "-"
  if (n < 1024) return `${n} B`
  const units = ["KB", "MB", "GB", "TB"]
  let v = n
  let i = -1
  do {
    v = v / 1024
    i++
  } while (v >= 1024 && i < units.length - 1)
  return `${v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2)} ${units[i]}`
}

function formatDate(s?: string): string {
  if (!s) return "-"
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const pad = (x: number) => String(x).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function shortCommit(c?: string): string {
  if (!c || c === "unknown") return "未写入"
  return c.slice(0, 12)
}

/** 生成 ipm_ + 24 字节 base64url 的公共 API keys。 */
function generateApiKey(): string {
  const bytes = new Uint8Array(24)
  crypto.getRandomValues(bytes)
  let bin = ""
  for (const b of bytes) bin += String.fromCharCode(b)
  const b64 = btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
  return `ipm_${b64}`
}

// ── 通用行组件 ────────────────────────────────────────────────────────────────

function SwitchRow({
  label,
  hint,
  checked,
  disabled,
  onChange,
}: {
  label: string
  hint?: string
  checked: boolean
  disabled?: boolean
  onChange: (v: boolean) => void
}) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-md border px-3 py-2">
      <div className="min-w-0">
        <p className="text-sm">{label}</p>
        {hint && <p className="text-muted-foreground text-xs leading-relaxed">{hint}</p>}
      </div>
      <Switch checked={checked} disabled={disabled} onCheckedChange={onChange} />
    </div>
  )
}

function StatCell({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="rounded-md border px-3 py-2">
      <p className="text-muted-foreground text-xs">{label}</p>
      <p className="mt-0.5 truncate font-mono text-sm" title={typeof value === "string" ? value : undefined}>
        {value}
      </p>
    </div>
  )
}

// 邮件后台监听的实时运行状态行(对齐原项目 mailWatcherStatusText)。
function MailWatcherStatusLine({ runtime }: { runtime: Record<string, unknown> }) {
  const ws = (runtime.mail_watcher_status ?? {}) as Record<string, unknown>
  const available = runtime.mail_watcher_available !== false
  if (!available) {
    return <p className="text-muted-foreground px-1 text-xs">配置已关闭</p>
  }
  const enabled = ws.enabled === true
  if (!enabled) {
    return <p className="text-muted-foreground px-1 text-xs">未开启</p>
  }
  const running = ws.running === true
  const lastError = typeof ws.last_error === "string" && ws.last_error ? ws.last_error : ""
  if (!running) {
    return (
      <p className="px-1 text-xs text-amber-600">
        {lastError ? `同步异常,请查看日志:${lastError}` : "启动中…"}
      </p>
    )
  }
  const imapConnected = Number(ws.connected_worker_count ?? 0)
  const imapTotal = Number(ws.worker_count ?? ws.imap_group_count ?? 0)
  const webGroups = Number(ws.web_polling_group_count ?? 0)
  const synced = Number(ws.synced_messages ?? 0)
  return (
    <p className="px-1 text-xs text-emerald-600">
      IMAP {imapConnected}/{imapTotal} ｜ Web {webGroups} ｜ 同步 {synced}
      {lastError ? ` ｜ 最近错误:${lastError}` : ""}
    </p>
  )
}

// ── 主组件 ───────────────────────────────────────────────────────────────────

export function SettingsTab({ enabled }: { enabled: boolean }) {
  const qc = useQueryClient()
  const settingsQuery = useICloudSettings(enabled)
  const databaseStatusQuery = useICloudDatabaseStatus(enabled)
  const updateStatusQuery = useICloudUpdateStatus(enabled)
  const saveSettings = useICloudSaveSettings()
  const serverChanTest = useICloudServerChanTest()
  const databaseAction = useICloudDatabaseAction()

  const [form, setForm] = React.useState<ICloudSettings>(DEFAULT_FORM)
  const [apiKeyInput, setApiKeyInput] = React.useState("")
  const [showApiKey, setShowApiKey] = React.useState(false)
  const [sendKeyInput, setSendKeyInput] = React.useState("")
  const [checkingUpdate, setCheckingUpdate] = React.useState(false)
  const [forcedUpdate, setForcedUpdate] = React.useState<UpdateStatusInfo | null>(null)

  React.useEffect(() => {
    if (settingsQuery.data) {
      setForm({ ...DEFAULT_FORM, ...settingsQuery.data.settings })
      setApiKeyInput("")
      setSendKeyInput("")
    }
  }, [settingsQuery.data])

  const set = <K extends keyof ICloudSettings>(key: K, v: ICloudSettings[K]) =>
    setForm((f) => ({ ...f, [key]: v }))

  const runtime = (settingsQuery.data?.runtime ?? {}) as Record<string, unknown>
  const dbStatus = databaseStatusQuery.data
  const db = dbStatus?.database

  const apiKeySource = typeof runtime.api_key_source === "string" ? runtime.api_key_source : ""
  const configApiKeyConfigured = runtime.config_api_key_configured === true
  const apiKeyConfigured = runtime.api_configured === true || !!form.public_api_key || configApiKeyConfigured
  const apiKeySourceLabel =
    apiKeySource === "system_settings"
      ? "系统设置"
      : apiKeySource === "config"
        ? "config.json"
        : "尚未设置"

  const serverChanConfigured =
    runtime.server_chan_configured === true || !!form.server_chan_send_key || !!sendKeyInput.trim()

  const updateStatus: UpdateStatusInfo | undefined = forcedUpdate ?? updateStatusQuery.data

  const doSave = () => {
    const payload: Partial<ICloudSettings> & { clear_server_chan_send_key?: boolean } = { ...form }
    if (apiKeyInput.trim()) payload.public_api_key = apiKeyInput.trim()
    else delete payload.public_api_key
    if (sendKeyInput.trim()) payload.server_chan_send_key = sendKeyInput.trim()
    else delete payload.server_chan_send_key
    saveSettings.mutate(payload, {
      onSuccess: () => toast.success("系统设置已保存"),
      onError: (e) => toast.error(errMsg(e, "保存失败")),
    })
  }

  const doDatabaseAction = (action: "check" | "backup" | "optimize") => {
    databaseAction.mutate(action, {
      onSuccess: (data) => {
        if (action === "check") {
          toast.success(`数据库完整性检查:${String(data?.result ?? "-")}`)
        } else if (action === "backup") {
          toast.success(`数据库备份已创建:${String(data?.path ?? "-")}`)
        } else {
          toast.success("数据库空间整理完成")
        }
        databaseStatusQuery.refetch()
      },
      onError: (e) => toast.error(errMsg(e, "操作失败")),
    })
  }

  const doServerChanTest = () => {
    serverChanTest.mutate(
      { send_key: sendKeyInput.trim() || undefined, hide_ip: form.server_chan_hide_ip },
      {
        onSuccess: (data) =>
          toast.success(data?.message || "测试推送已加入 Server 酱队列"),
        onError: (e) => toast.error(errMsg(e, "发送失败")),
      },
    )
  }

  const doCheckUpdate = async () => {
    setCheckingUpdate(true)
    try {
      const status = await icloudApi.get<UpdateStatusInfo>("/update/status", { force: 1 })
      setForcedUpdate(status)
      qc.setQueryData(["icloud", "update", "status"], status)
      if (status.error) {
        toast.error(status.error)
      } else if (status.update_available || status.has_update) {
        toast.success("发现新的项目版本或源码提交")
      } else {
        toast.success("检查完成,当前已经是最新版本")
      }
    } catch (e) {
      toast.error(errMsg(e, "检查更新失败"))
    } finally {
      setCheckingUpdate(false)
    }
  }

  const doGenerateApiKey = () => {
    const key = generateApiKey()
    setApiKeyInput(key)
    setShowApiKey(true)
    toast.success("公共 API keys 已生成,请保存系统设置")
  }

  const keepAliveMinutes = Math.round(
    (typeof runtime.apple_keep_alive_ms === "number" ? runtime.apple_keep_alive_ms : 0) / 60000,
  ) || 3
  const keepAliveJitter =
    typeof runtime.apple_keep_alive_jitter_percent === "number"
      ? runtime.apple_keep_alive_jitter_percent
      : 15

  if (settingsQuery.isPending && enabled) {
    return (
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">正在加载系统设置</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <TableSkeleton rows={10} cols={2} />
        </CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      {/* 顶部命令栏 */}
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-medium">系统设置</h2>
          <p className="text-muted-foreground text-xs">本地数据、后台能力和公共访问</p>
        </div>
        <Button onClick={doSave} disabled={saveSettings.isPending}>
          <Save /> {saveSettings.isPending ? "保存中" : "保存系统设置"}
        </Button>
      </div>

      {/* 1. 本地数据 */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-sm">
            <Database className="size-4" /> 本地数据
          </CardTitle>
          <CardDescription>SQLite 数据库占用与维护操作</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
            <StatCell label="数据库" value={formatBytes(db?.database_bytes)} />
            <StatCell label="WAL" value={formatBytes(db?.wal_bytes)} />
            <StatCell
              label="变更日志"
              value={db ? `${db.change_log_count} 条` : "-"}
            />
            <StatCell
              label="结构版本"
              value={db ? `v${db.schema_version}` : "-"}
            />
          </div>
          <p className="text-muted-foreground text-xs leading-relaxed">
            SQLite 数据库:
            <code className="bg-muted mx-1 rounded px-1 py-0.5 font-mono">
              {db?.path || "data/app.db"}
            </code>
            ;邮件保留 {dbStatus?.message_retention_days ?? 90} 天;自动备份最多{" "}
            {dbStatus?.backup_retention_count ?? 3} 份:
            <code className="bg-muted mx-1 rounded px-1 py-0.5 font-mono break-all">
              {dbStatus?.backup_dir || "-"}
            </code>
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={databaseAction.isPending}
              onClick={() => doDatabaseAction("check")}
            >
              <ShieldCheck /> 完整性检查
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={databaseAction.isPending}
              onClick={() => doDatabaseAction("backup")}
            >
              <HardDriveDownload /> 立即备份
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={databaseAction.isPending}
              onClick={() => doDatabaseAction("optimize")}
            >
              <Wrench /> 整理空间
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* 2. 公共访问 */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-sm">
            <Globe2 className="size-4" /> 公共访问
          </CardTitle>
          <CardDescription>对外开放取号与网页取码能力</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2">
          <SwitchRow
            label="公共取号 API"
            hint="开放取号和批量查询接口,需 API keys"
            checked={form.enable_public_mailbox_api}
            onChange={(v) => set("enable_public_mailbox_api", v)}
          />
          <SwitchRow
            label="公共邮箱取码页面"
            hint="输入邮箱即可获取验证码并查看邮件"
            checked={form.enable_public_code_page}
            onChange={(v) => set("enable_public_code_page", v)}
          />
        </CardContent>
      </Card>

      {/* 3. 后台能力 */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-sm">
            <ShieldCheck className="size-4" /> 后台能力
          </CardTitle>
          <CardDescription>后台监听与登录态保活</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2">
          <SwitchRow
            label="邮件后台监听"
            hint="IMAP IDLE 收信,Web API 断线兜底"
            checked={form.enable_mail_watcher}
            disabled={runtime.mail_watcher_available === false}
            onChange={(v) => set("enable_mail_watcher", v)}
          />
          <MailWatcherStatusLine runtime={runtime} />
          <SwitchRow
            label="Apple 登录态保活"
            hint={`基础 ${keepAliveMinutes} 分钟;每 30 秒扫描并在每轮重新随机 ±${keepAliveJitter}%`}
            checked={form.enable_apple_keep_alive}
            disabled={runtime.apple_keep_alive_available === false}
            onChange={(v) => set("enable_apple_keep_alive", v)}
          />
        </CardContent>
      </Card>

      {/* 4. iCloud Web API */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-sm">
            <RefreshCw className="size-4" /> iCloud Web API
          </CardTitle>
          <CardDescription>Web API 通道的取码、同步与远端操作开关</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2">
          <SwitchRow
            label="Web API 取码与邮件刷新"
            hint="后台与公共取码、公共页面邮件刷新时使用 Web API 补查;默认关闭"
            checked={form.enable_web_code_sync}
            onChange={(v) => set("enable_web_code_sync", v)}
          />
          <SwitchRow
            label="Web API 手动邮件同步"
            hint="用于表格、详情和全部已有邮箱的邮件补查与回退"
            checked={form.enable_web_manual_mail_sync}
            onChange={(v) => set("enable_web_manual_mail_sync", v)}
          />
          <SwitchRow
            label="Web API 后台邮件监听"
            hint="用于首次扫描、IMAP IDLE 补查及低频轮询"
            checked={form.enable_web_background_mail}
            onChange={(v) => set("enable_web_background_mail", v)}
          />
          <SwitchRow
            label="Web API 远端邮件操作"
            hint="允许移动邮件、清空废纸篓、云端清理及彻底删除邮箱"
            checked={form.enable_web_remote_mail_cleanup}
            onChange={(v) => set("enable_web_remote_mail_cleanup", v)}
          />
        </CardContent>
      </Card>

      {/* 5. Server 酱消息推送 */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-sm">
            <Send className="size-4" /> Server 酱消息推送
          </CardTitle>
          <CardDescription>
            通过 sct.ftqq.com 把关键运行事件推送到默认微信消息通道
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="grid gap-2">
            <Label>SendKey</Label>
            <Input
              value={sendKeyInput}
              onChange={(e) => setSendKeyInput(e.target.value)}
              placeholder={
                serverChanConfigured
                  ? "已配置(输入以更换)"
                  : "输入 SCT 开头的 SendKey"
              }
              className="font-mono"
              maxLength={180}
              autoComplete="off"
            />
            {typeof runtime.server_chan_send_key_masked === "string" &&
              runtime.server_chan_send_key_masked !== "" && (
                <p className="text-xs">
                  当前已配置:
                  <span className="text-muted-foreground font-mono">
                    {runtime.server_chan_send_key_masked}
                  </span>
                </p>
              )}
            <p className="text-muted-foreground text-xs">
              SendKey 默认显示,并使用本地数据库加密保存;推送使用 Server 酱网站配置的默认微信消息通道。
            </p>
          </div>
          <SwitchRow
            label="后台登录通知"
            hint="管理员成功登录后推送账号、时间、访问地址和浏览器信息"
            checked={form.notify_admin_login}
            onChange={(v) => set("notify_admin_login", v)}
          />
          <SwitchRow
            label="账号与登录态掉线通知"
            hint="Apple Account、iCloud Web 或 IMAP 由正常转为异常时推送,标题会显示具体 Apple 账号;发送额度由填写的 SendKey 套餐决定,本地不限制条数"
            checked={form.notify_account_login_state_offline}
            onChange={(v) => set("notify_account_login_state_offline", v)}
          />
          <SwitchRow
            label="隐藏调用 IP"
            hint="向 Server 酱提交 noip=1,消息中不显示本服务的外网调用 IP"
            checked={form.server_chan_hide_ip}
            onChange={(v) => set("server_chan_hide_ip", v)}
          />
          <Separator />
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-2 text-xs">
              <span
                className={`inline-block size-2 rounded-full ${
                  serverChanConfigured ? "bg-emerald-500" : "bg-amber-500"
                }`}
              />
              <span className="text-muted-foreground">
                {serverChanConfigured ? "推送凭据已就绪" : "等待配置 SendKey"}
              </span>
              <a
                href="https://sct.ftqq.com/"
                target="_blank"
                rel="noopener noreferrer"
                className="text-primary inline-flex items-center gap-0.5 hover:underline"
              >
                Server 酱控制台 <ExternalLink className="size-3" />
              </a>
            </div>
            <Button
              variant="outline"
              size="sm"
              disabled={serverChanTest.isPending || !serverChanConfigured}
              onClick={doServerChanTest}
            >
              <Send /> {serverChanTest.isPending ? "提交中" : "发送测试"}
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* 6. 公共取号 API keys */}
      <Card>
        <CardHeader className="pb-3">
          <div className="flex items-center justify-between gap-2">
            <CardTitle className="flex items-center gap-2 text-sm">
              <KeyRound className="size-4" /> 公共取号 API keys
            </CardTitle>
            <Badge variant={apiKeyConfigured ? "default" : "secondary"} className="text-xs">
              {apiKeyConfigured ? "已配置" : "待设置"}
            </Badge>
          </div>
          <CardDescription>
            外部调用取号、批量查询接口时使用;来源:{apiKeySourceLabel}。
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex gap-2">
            <div className="relative flex-1">
              <Input
                type={showApiKey ? "text" : "password"}
                value={apiKeyInput}
                onChange={(e) => setApiKeyInput(e.target.value)}
                placeholder={
                  configApiKeyConfigured
                    ? "留空继续使用 config.json 中的 api_keys"
                    : form.public_api_key
                      ? "已配置(输入以更换)"
                      : "输入或点击右侧按钮生成"
                }
                className="pr-9 font-mono"
                autoComplete="off"
              />
              <button
                type="button"
                className="text-muted-foreground hover:text-foreground absolute top-1/2 right-2 -translate-y-1/2"
                onClick={() => setShowApiKey((v) => !v)}
                aria-label={showApiKey ? "隐藏" : "显示"}
              >
                {showApiKey ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
              </button>
            </div>
            <Button variant="outline" size="sm" className="shrink-0 self-start" onClick={doGenerateApiKey}>
              生成新 Key
            </Button>
          </div>
          <div className="text-muted-foreground space-y-1 text-xs">
            <p>
              端点:
              <code className="bg-muted mx-1 rounded px-1 py-0.5 font-mono">
                POST /api/icloud/v1/mailboxes/claim
              </code>
              ;
              <a
                href="/email-code"
                target="_blank"
                rel="noopener noreferrer"
                className="text-primary mx-1 hover:underline"
              >
                /email-code 打开页面
              </a>
            </p>
            <p>
              注:生成或修改后点击「保存系统设置」立即生效;公共邮箱取码页面不使用这个 Key。
            </p>
          </div>
        </CardContent>
      </Card>

      {/* 7. 版本与更新 */}
      <Card id="version-updates">
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-sm">
            <RefreshCw className="size-4" /> 版本与更新
          </CardTitle>
          <CardDescription>
            根据仓库公告配置检查版本,无需 API Token;当前只提供查看。
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap gap-2">
            {updateStatus?.repository_url && (
              <Button variant="outline" size="sm" asChild>
                <a href={updateStatus.repository_url} target="_blank" rel="noopener noreferrer">
                  <ExternalLink /> 打开仓库
                </a>
              </Button>
            )}
            <Button
              variant="outline"
              size="sm"
              disabled={checkingUpdate || updateStatus?.enabled === false}
              onClick={doCheckUpdate}
            >
              <RefreshCw className={checkingUpdate ? "animate-spin" : undefined} />
              {checkingUpdate
                ? "正在检查"
                : updateStatus?.enabled === false
                  ? "检查更新已关闭"
                  : "检查更新"}
            </Button>
          </div>
          <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
            <StatCell
              label="当前版本"
              value={updateStatus?.current?.version || "2.1.2"}
            />
            <StatCell label="构建提交" value={shortCommit(updateStatus?.current?.commit)} />
            <StatCell
              label="运行平台"
              value={
                updateStatus?.current
                  ? `${updateStatus.current.os} / ${updateStatus.current.arch}`
                  : "-"
              }
            />
            <StatCell label="检查时间" value={formatDate(updateStatus?.checked_at)} />
          </div>
          {updateStatus?.error ? (
            <div className="rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs text-red-600 dark:text-red-400">
              检查更新失败 {updateStatus.error}
            </div>
          ) : updateStatus?.update_available || updateStatus?.has_update ? (
            <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-400">
              <p className="font-medium">
                发现新的项目内容
                {updateStatus.latest?.name ? `:${updateStatus.latest.name}` : ""}
              </p>
              {updateStatus.latest?.notes && (
                <p className="mt-1 whitespace-pre-wrap">{updateStatus.latest.notes}</p>
              )}
              {(updateStatus.latest?.url || updateStatus.release_url || updateStatus.repository_url) && (
                <a
                  href={updateStatus.latest?.url || updateStatus.release_url || updateStatus.repository_url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="mt-1 inline-flex items-center gap-0.5 underline"
                >
                  重新下载源码 <ExternalLink className="size-3" />
                </a>
              )}
            </div>
          ) : updateStatus && (updateStatus.checked_at || updateStatus.latest_version) ? (
            <div className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-700 dark:text-emerald-400">
              当前已经是最新版本
            </div>
          ) : updateStatus?.enabled === false ? (
            <p className="text-muted-foreground text-xs">配置文件已关闭更新检查。</p>
          ) : (
            <p className="text-muted-foreground text-xs">
              点击「检查更新」读取仓库公告配置。
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
