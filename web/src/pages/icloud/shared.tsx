// iCloud 模块共享展示辅助。
import * as React from "react"
import { HttpError } from "@/lib/api"
import { Badge } from "@/components/ui/badge"

// ── 错误 toast ────────────────────────────────────────────────────────────────

export function errMsg(e: unknown, fallback: string): string {
  return e instanceof HttpError ? e.message : fallback
}

// ── Badge 着色 ────────────────────────────────────────────────────────────────

export function LevelBadge({ level }: { level: string }) {
  const variant =
    level === "error" ? "destructive" : level === "warning" ? "outline" : "secondary"
  const label =
    level === "error" ? "错误" : level === "warning" ? "警告" : "信息"
  return (
    <Badge variant={variant} className="text-xs">
      {label}
    </Badge>
  )
}

const MAILBOX_STATUS_LABELS: Record<string, string> = {
  available: "可用",
  reserved: "已占用",
  used: "已使用",
  failed: "失败",
  disabled: "已禁用",
}

export function MailboxStatusBadge({ status }: { status: string }) {
  const variant =
    status === "available"
      ? "default"
      : status === "failed"
        ? "destructive"
        : status === "disabled"
          ? "outline"
          : "secondary"
  return (
    <Badge variant={variant} className="text-xs">
      {MAILBOX_STATUS_LABELS[status] ?? status}
    </Badge>
  )
}

export function ICloudStatusBadge({ status }: { status: string }) {
  const ok = status === "active" || status === "ok" || status === "online"
  const bad = status === "error" || status === "failed" || status === "offline"
  return (
    <Badge variant={ok ? "default" : bad ? "destructive" : "secondary"} className="text-xs">
      {status || "-"}
    </Badge>
  )
}

/** 登录态小圆点(kind + 最近检测是否成功)。 */
export function LoginStateDot({ kind, ok, saved }: { kind: string; ok: boolean; saved: boolean }) {
  const color = !saved ? "bg-muted-foreground/30" : ok ? "bg-emerald-500" : "bg-amber-500"
  return (
    <span className="inline-flex items-center gap-1 text-xs" title={`${kind}: ${saved ? (ok ? "正常" : "异常") : "未保存"}`}>
      <span className={`inline-block size-2 rounded-full ${color}`} />
      <span className="text-muted-foreground">{kind}</span>
    </span>
  )
}

// ── 空态 ─────────────────────────────────────────────────────────────────────

export function EmptyRow({ cols }: { cols: number }) {
  return (
    <tr>
      <td colSpan={cols} className="text-muted-foreground py-10 text-center text-sm">
        暂无数据
      </td>
    </tr>
  )
}

/** 简单进度条(无 ui/progress 组件,用 div 实现)。 */
export function ProgressBar({ value }: { value: number }) {
  const pct = Math.max(0, Math.min(100, Math.round(value * (value <= 1 ? 100 : 1))))
  return (
    <div className="bg-muted h-2 w-28 overflow-hidden rounded-full">
      <div className="bg-primary h-full transition-all" style={{ width: `${pct}%` }} />
    </div>
  )
}

export type SetState<T> = React.Dispatch<React.SetStateAction<T>>

// ── 邮件 HTML 安全渲染(与原项目 buildEmailHTMLDocument 一致) ───────────────────

function shrinkEmailFontSizes(value: string): string {
  return String(value ?? "").replace(
    /(font-size\s*:\s*)(\d+(?:\.\d+)?)px/gi,
    (match, prefix: string, rawSize: string) => {
      const size = Number(rawSize)
      if (!Number.isFinite(size) || size < 12) return match
      const reduced = Math.max(11, Math.round(size * 0.82))
      return `${prefix}${reduced}px`
    },
  )
}

/** 与原 buildEmailHTMLDocument 一致:移除危险节点/on* 属性/javascript: URL,
 *  a 加 target=_blank rel=noopener,字体缩小,注入 CSP + viewport + 阅读样式。 */
export function buildEmailHTMLDocument(value: string): string {
  if (!value || typeof DOMParser === "undefined") return ""
  const doc = new DOMParser().parseFromString(value, "text/html")
  doc
    .querySelectorAll(
      'script, iframe, object, embed, form, input, button, textarea, select, base, meta[http-equiv="refresh"]',
    )
    .forEach((el) => el.remove())
  doc.querySelectorAll("*").forEach((el) => {
    for (const attr of Array.from(el.attributes)) {
      const name = attr.name.toLowerCase()
      const v = attr.value.trim().toLowerCase()
      if (
        name.startsWith("on") ||
        ((name === "href" || name === "src" || name === "action") && v.startsWith("javascript:"))
      ) {
        el.removeAttribute(attr.name)
      }
    }
  })
  doc.querySelectorAll("a[href]").forEach((link) => {
    link.setAttribute("target", "_blank")
    link.setAttribute("rel", "noopener noreferrer")
  })
  // 只调整用于展示的副本,保留数据库中的原始邮件 HTML。
  doc.querySelectorAll("style").forEach((el) => {
    el.textContent = shrinkEmailFontSizes(el.textContent ?? "")
  })
  doc.querySelectorAll("[style]").forEach((el) => {
    el.setAttribute("style", shrinkEmailFontSizes(el.getAttribute("style") ?? ""))
  })
  const policy = doc.createElement("meta")
  policy.setAttribute("http-equiv", "Content-Security-Policy")
  policy.setAttribute(
    "content",
    "default-src 'none'; img-src https: http: data:; style-src 'unsafe-inline' https: http:; font-src https: http: data:; media-src https: http: data:; script-src 'none'; object-src 'none'; frame-src 'none'; form-action 'none'",
  )
  const viewport = doc.createElement("meta")
  viewport.setAttribute("name", "viewport")
  viewport.setAttribute("content", "width=device-width, initial-scale=1")
  const readerStyle = doc.createElement("style")
  readerStyle.textContent =
    "html{color-scheme:light;background:#fff;scrollbar-width:thin;scrollbar-color:#cbd5e1 transparent}body{box-sizing:border-box;min-height:100%;margin:0;padding:24px;color:#172033;background:#fff;font-size:13px;overflow-wrap:anywhere}::-webkit-scrollbar{width:8px;height:8px;background:transparent}::-webkit-scrollbar-thumb{border:2px solid transparent;border-radius:9999px;background:#cbd5e1;background-clip:content-box}img{max-width:100%;height:auto}table{max-width:100%}pre{white-space:pre-wrap}"
  doc.head.append(policy, viewport, readerStyle)
  return `<!doctype html>\n${doc.documentElement.outerHTML}`
}
