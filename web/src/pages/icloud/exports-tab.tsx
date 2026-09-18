// 本地导出 tab:运行数据/运行数据与邮件/邮箱地址/取码 API 四类导出 + 导出环境面板。
import * as React from "react"
import { toast } from "sonner"
import { Database, Download, FileJson, FileText, Globe2, KeyRound, Mail, ShieldAlert } from "lucide-react"
import { useICloudSettings } from "@/lib/icloud-queries"
import { HttpError } from "@/lib/api"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { errMsg } from "./shared"

// ── 下载辅助 ───────────────────────────────────────────────────────────────────

function dateStamp(): string {
  const d = new Date()
  const pad = (x: number) => String(x).padStart(2, "0")
  return `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}`
}

/** 前端生成并触发文件下载。 */
function downloadFile(filename: string, content: string) {
  const blob = new Blob([content])
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

// ── 导出请求 ───────────────────────────────────────────────────────────────────

/**
 * 导出接口返回的是原始文本/JSON 文件流(带 Content-Disposition),不是
 * {success,data} 信封;因此这里统一走原生 fetch 拿 response.text()。
 */
async function fetchExport(path: string): Promise<string> {
  const res = await fetch(`/api/icloud${path}`, { credentials: "same-origin" })
  const text = await res.text()
  if (!res.ok) {
    let message = `导出失败(HTTP ${res.status})`
    try {
      const body = JSON.parse(text) as { code?: string; message?: string }
      if (body?.message) message = body.message
    } catch {
      // 保留默认错误信息
    }
    throw new HttpError(res.status, "export_failed", message)
  }
  return text
}

// ── 导出卡片定义 ───────────────────────────────────────────────────────────────

interface ExportCardDef {
  key: string
  title: string
  description: string
  format: "JSON" | "TXT"
  path: string
  filename: (stamp: string) => string
  icon: React.ComponentType<{ className?: string }>
  /** 从导出内容估算条数,用于成功 toast;返回 null 表示不显示条数。 */
  countOf: (content: string) => number | null
}

const EXPORT_CARDS: ExportCardDef[] = [
  {
    key: "runtime",
    title: "运行数据",
    description: "导出账号、邮箱、登录态和系统设置,不包含本地邮件正文",
    format: "JSON",
    path: "/runtime/export",
    filename: (s) => `gpt-go-icloud-runtime-${s}.json`,
    icon: Database,
    countOf: (content) => {
      try {
        const data = JSON.parse(content) as { mailboxes?: unknown[]; apple_accounts?: unknown[] }
        return (data.mailboxes?.length ?? 0) + (data.apple_accounts?.length ?? 0)
      } catch {
        return null
      }
    },
  },
  {
    key: "runtime-messages",
    title: "运行数据与邮件",
    description: "在完整运行数据中加入所有已同步的本地邮件内容",
    format: "JSON",
    path: "/runtime/export?include_messages=1",
    filename: (s) => `gpt-go-icloud-runtime-messages-${s}.json`,
    icon: FileJson,
    countOf: (content) => {
      try {
        const data = JSON.parse(content) as { message_count?: number; messages?: unknown[] }
        return data.message_count ?? data.messages?.length ?? null
      } catch {
        return null
      }
    },
  },
  {
    key: "mailbox-emails",
    title: "邮箱地址",
    description: "只导出邮箱池中的隐私邮箱地址,便于导入其他本地工具",
    format: "TXT",
    path: "/runtime/export-mailbox-emails?format=txt",
    filename: (s) => `gpt-go-icloud-mailbox-emails-${s}.txt`,
    icon: Mail,
    countOf: (content) => content.split("\n").map((l) => l.trim()).filter(Boolean).length,
  },
  {
    key: "mailbox-apis",
    title: "取码 API",
    description: "导出每个邮箱的地址与独立取码 API 链接",
    format: "TXT",
    path: "/runtime/export-mailbox-apis?format=txt",
    filename: (s) => `gpt-go-icloud-mailbox-apis-${s}.txt`,
    icon: FileText,
    countOf: (content) => content.split("\n").map((l) => l.trim()).filter(Boolean).length,
  },
]

// ── 通用展示单元 ───────────────────────────────────────────────────────────────

function EnvCell({
  icon: Icon,
  label,
  value,
  ok,
}: {
  icon: React.ComponentType<{ className?: string }>
  label: string
  value: string
  ok?: boolean
}) {
  return (
    <div className="rounded-md border px-3 py-2">
      <p className="text-muted-foreground flex items-center gap-1.5 text-xs">
        <Icon className="size-3.5" /> {label}
      </p>
      <p
        className={`mt-0.5 truncate font-mono text-sm ${ok === false ? "text-amber-600 dark:text-amber-400" : ""}`}
        title={value}
      >
        {value}
      </p>
    </div>
  )
}

// ── 主组件 ────────────────────────────────────────────────────────────────────

export function ExportsTab({ enabled }: { enabled: boolean }) {
  const settingsQuery = useICloudSettings(enabled)
  const [exportingKey, setExportingKey] = React.useState<string | null>(null)

  const runtime = (settingsQuery.data?.runtime ?? {}) as Record<string, unknown>

  // SQLite 数据库路径:优先取 runtime.database_status.path
  const dbStatus = runtime.database_status as { path?: string } | undefined
  const dbPath =
    typeof dbStatus?.path === "string" && dbStatus.path.trim() !== ""
      ? dbStatus.path
      : "使用当前运行数据库"

  // 公共基础地址
  const publicBaseUrl =
    typeof runtime.public_base_url === "string" && runtime.public_base_url.trim() !== ""
      ? runtime.public_base_url
      : "按当前访问地址生成"

  // 全局 API keys 状态(含来源)
  const apiConfigured = runtime.api_configured === true
  const apiKeySource = typeof runtime.api_key_source === "string" ? runtime.api_key_source : ""
  const apiKeySourceLabel =
    apiKeySource === "system_settings" ? "系统设置" : apiKeySource === "config" ? "config.json" : ""
  const apiKeyLabel = apiConfigured
    ? apiKeySourceLabel
      ? `已配置(${apiKeySourceLabel})`
      : "已配置"
    : "尚未设置"

  const doExport = async (def: ExportCardDef) => {
    if (exportingKey) return
    setExportingKey(def.key)
    try {
      const content = await fetchExport(def.path)
      const stamp = dateStamp()
      downloadFile(def.filename(stamp), content)
      const n = def.countOf(content)
      toast.success(n !== null && n > 0 ? `已导出 ${n} 条` : "已导出")
    } catch (e) {
      toast.error(errMsg(e, "导出失败"))
    } finally {
      setExportingKey(null)
    }
  }

  return (
    <div className="space-y-4">
      {/* 顶部标题栏 */}
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-medium">本地导出</h2>
          <p className="text-muted-foreground text-xs">
            下载运行数据、邮件、邮箱地址或取码 API
          </p>
        </div>
        <Badge
          variant="outline"
          className="border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-400"
        >
          <ShieldAlert className="size-3.5" /> 包含敏感本地数据
        </Badge>
      </div>

      {/* 4 个导出卡片 */}
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        {EXPORT_CARDS.map((def) => {
          const Icon = def.icon
          const exporting = exportingKey === def.key
          return (
            <Card key={def.key}>
              <CardHeader className="pb-3">
                <div className="flex items-start justify-between gap-2">
                  <CardTitle className="flex items-center gap-2 text-sm">
                    <Icon className="size-4" /> {def.title}
                  </CardTitle>
                  <Badge variant="secondary" className="font-mono text-xs">
                    {def.format}
                  </Badge>
                </div>
                <CardDescription>{def.description}</CardDescription>
              </CardHeader>
              <CardContent className="space-y-3">
                <p className="text-muted-foreground truncate font-mono text-xs" title={`GET /api/icloud${def.path}`}>
                  GET /api/icloud{def.path}
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={exportingKey !== null}
                  onClick={() => void doExport(def)}
                >
                  <Download className={exporting ? "animate-bounce" : undefined} />
                  {exporting ? "导出中" : "导出"}
                </Button>
              </CardContent>
            </Card>
          )
        })}
      </div>

      {/* 导出环境面板 */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">导出环境</CardTitle>
          <CardDescription>当前服务生成文件时使用的运行信息</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="grid grid-cols-1 gap-2 md:grid-cols-3">
            <EnvCell icon={Database} label="SQLite 数据库" value={dbPath} ok />
            <EnvCell icon={Globe2} label="公共基础地址" value={publicBaseUrl} ok />
            <EnvCell icon={KeyRound} label="全局 API keys" value={apiKeyLabel} ok={apiConfigured} />
          </div>
          <p className="text-muted-foreground text-xs leading-relaxed">
            运行数据导出包含 Apple 登录态、Cookie 和 App 专用密码等内容,请将下载文件保存在可信位置。
          </p>
        </CardContent>
      </Card>
    </div>
  )
}
