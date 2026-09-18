// iCloud 邮箱管理模块的 API 类型与请求封装。
//
// 与主站 /api 其它模块不同,iCloud 模块(挂载于 /api/icloud)沿用原项目的
// 响应包壳:成功 {"success":true,"data":...},失败 {"success":false,"code","message"}。
// 因此这里单独封装一个 icloudFetch,不共用 lib/api 的 request。

import { HttpError } from "./api"

interface ICloudEnvelope<T> {
  success: boolean
  data?: T
  code?: string
  message?: string
  retryable?: boolean
}

async function icloudFetch<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const res = await fetch(`/api/icloud${path}`, {
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    ...options,
  })
  const text = await res.text()
  let body: ICloudEnvelope<T> | null = null
  if (text) {
    try {
      body = JSON.parse(text) as ICloudEnvelope<T>
    } catch {
      body = null
    }
  }
  if (!res.ok || !body?.success) {
    // 注意:后端「公共取码无码」等场景会返回 HTTP 200 但 success:false,
    // 必须同时检查包壳 success 字段,否则会把错误包壳误当 data。
    const code = body?.code || "internal_error"
    const message = body?.message || `请求失败(HTTP ${res.status})`
    throw new HttpError(res.status, code, message)
  }
  return body.data as T
}

function qs(params: Record<string, string | number | boolean | undefined>): string {
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") sp.set(k, String(v))
  }
  const s = sp.toString()
  return s ? `?${s}` : ""
}

export const icloudApi = {
  get: <T>(path: string, params?: Record<string, string | number | boolean | undefined>) =>
    icloudFetch<T>(path + (params ? qs(params) : "")),
  post: <T>(path: string, body?: unknown) =>
    icloudFetch<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) }),
  put: <T>(path: string, body: unknown) =>
    icloudFetch<T>(path, { method: "PUT", body: JSON.stringify(body) }),
  delete: <T>(path: string, params?: Record<string, string | number | boolean | undefined>) =>
    icloudFetch<T>(path + (params ? qs(params) : ""), { method: "DELETE" }),
}

// ── 类型定义(对齐后端 domain) ────────────────────────────────────────────────

export interface ICloudLoginStateSummary {
  kind: string
  saved: boolean
  last_checked_at?: string
  last_check_ok: boolean
  last_status_message?: string
  manage_expires_at?: string
  imap_email?: string
}

export interface ICloudAppleAccount {
  id: string
  label: string
  apple_id: string
  status: string
  icloud_status: string
  note: string
  created_at: string
  updated_at: string
  login_states?: ICloudLoginStateSummary[]
  imap_email?: string
  imap_app_password?: string
}

export interface ICloudMailbox {
  id: string
  account_id?: string
  label: string
  email: string
  forward_to_email?: string
  api_active: boolean
  icloud_active: boolean
  receive_count: number
  status: string
  note: string
  active_lease_id?: string
  last_sync_at?: string
  last_code_at?: string
  created_at: string
  updated_at: string
}

export interface ICloudMailboxPage {
  items: ICloudMailbox[]
  page: number
  page_size: number
  total: number
  total_pages: number
}

export interface ICloudMessage {
  id: string
  mailbox_id: string
  source?: string
  subject: string
  from: string
  body: string
  html_body?: string
  content_type?: string
  received_at: string
  created_at: string
}

export interface ICloudCodeResult {
  email: string
  code: string
  subject: string
  from: string
  received_at: string
  message_id: string
}

export interface ICloudDashboard {
  apple_account_count: number
  active_account_count: number
  mailbox_count: number
  available_count: number
  message_count: number
  events: ICloudEvent[]
}

export interface ICloudEvent {
  id: string
  level: string
  category: string
  message: string
  created_at: string
}

export interface ICloudTask {
  id: string
  name: string
  description: string
  status: string
  progress: number
  module: string
  next_run_at?: string
  scheduled_interval_seconds?: number
  jitter_percent?: number
}

export interface ICloudSchedulerEvent {
  id: number
  at: string
  type: string
  message: string
  account_id?: string
  mailbox_id?: string
  email?: string
  label?: string
  error?: string
}

export interface ICloudSchedulerState {
  running: boolean
  status: string
  account_ids: string[]
  label: string
  note: string
  create_channel: string
  current_interval_seconds?: number
  interval_min_seconds?: number
  interval_max_seconds?: number
  account_interval_min_seconds?: number
  account_interval_max_seconds?: number
  batch_index?: number
  success?: number
  failed?: number
  started_at?: string
  last_run_at?: string
  next_run_at?: string
  stopped_at?: string
  last_error?: string
  events?: ICloudSchedulerEvent[]
}

export interface ICloudSettings {
  enable_mail_watcher: boolean
  enable_apple_keep_alive: boolean
  enable_public_mailbox_api: boolean
  enable_public_code_page: boolean
  enable_web_code_sync: boolean
  enable_web_manual_mail_sync: boolean
  enable_web_background_mail: boolean
  enable_web_remote_mail_cleanup: boolean
  public_api_key?: string
  apple_account_module_ready: boolean
  server_chan_send_key?: string
  server_chan_hide_ip: boolean
  notify_admin_login: boolean
  notify_account_login_state_offline: boolean
}

export interface ICloudCreateSettings {
  mode?: string
  label?: string
  note?: string
  account_ids?: string[]
  create_channel?: string
  scheduler_create_channel?: string
  apple_account_two_factor_method?: string
  icloud_web_two_factor_method?: string
  scheduler_interval_min_minutes?: number
  scheduler_interval_max_minutes?: number
  scheduler_account_interval_min_seconds?: number
  scheduler_account_interval_max_seconds?: number
}

export interface ICloudAuthStatus {
  setup_required: boolean
  authenticated: boolean
  admin?: { id: string; username: string; created_at: string; last_login_at: string }
}

// ── 批量/彻底删除/远程清理/解析/同步/导出/更新(补齐迁移缺失的功能) ─────────────

// 批量删除/彻底删除邮箱的任务进度。
export interface ICloudDeleteJob {
  running: number
  waiting: number
  finished: number
  total: number
  succeeded: number
  failed: number
  last_error?: string
  done: boolean
}

// 彻底删除全部 Apple 账号云端+本地邮件的任务状态。
export interface ICloudAppleMailCleanupStatus {
  running: boolean
  destroyed?: number
  local_removed?: number
  failed?: number
  last_error?: string
  started_at?: string
  finished_at?: string
}

// 批量解析(resolve)命中的邮箱。
export interface ICloudResolvedMailbox {
  id: string
  email: string
  account_id: string
}

// 批量同步邮件(sync-messages)的进度。
export interface ICloudMessageSyncJob {
  running: boolean
  processed?: number
  total?: number
  created?: number
  updated?: number
  fallbacks?: number
  failed?: number
  last_error?: string
  started_at?: string
  finished_at?: string
}

// 版本更新检查状态(对齐后端 updatecheck.Status)。
export interface ICloudUpdateStatus {
  enabled: boolean
  repository?: string
  repository_url?: string
  current?: { version?: string; commit?: string; built_at?: string; os?: string; arch?: string }
  latest?: { version?: string; name?: string; notes?: string; published_at?: string; url?: string; source?: string }
  update_available: boolean
  checked_at?: string
  error?: string
  announcements?: ICloudAnnouncement[]
}

export interface ICloudAnnouncement {
  id: string
  title: string
  content: string
  level?: string
  published_at?: string
}

// 数据库运行状态。
export interface ICloudDatabaseStatus {
  database: {
    path: string
    schema_version: number
    journal_mode: string
    database_bytes: number
    wal_bytes: number
    change_log_count: number
    latest_sequence: number
    encrypted_fields: boolean
  }
  backup_dir: string
  backup_retention_count: number
  message_retention_days: number
  last_maintenance?: Record<string, unknown>
}
