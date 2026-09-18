// iCloud 邮箱管理模块的 react-query hooks。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { icloudApi } from "./icloud-api"
import type {
  ICloudAppleAccount,
  ICloudAppleMailCleanupStatus,
  ICloudAuthStatus,
  ICloudCodeResult,
  ICloudCreateSettings,
  ICloudDashboard,
  ICloudDatabaseStatus,
  ICloudMailbox,
  ICloudMailboxPage,
  ICloudMessage,
  ICloudMessageSyncJob,
  ICloudResolvedMailbox,
  ICloudSchedulerState,
  ICloudSettings,
  ICloudTask,
  ICloudUpdateStatus,
} from "./icloud-api"

// ── 鉴权 ─────────────────────────────────────────────────────────────────────

export function useICloudAuthStatus() {
  return useQuery({
    queryKey: ["icloud", "auth", "status"],
    queryFn: () => icloudApi.get<ICloudAuthStatus>("/auth/status"),
    retry: false,
  })
}

export function useICloudLogin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { username: string; password: string }) =>
      icloudApi.post<{ admin: unknown }>("/auth/login", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "auth"] }),
  })
}

export function useICloudSetup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { username: string; password: string }) =>
      icloudApi.post<{ admin: unknown }>("/auth/setup", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "auth"] }),
  })
}

export function useICloudLogout() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => icloudApi.post<unknown>("/auth/logout"),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "auth"] }),
  })
}

// ── 控制台 ───────────────────────────────────────────────────────────────────

export function useICloudDashboard(enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "dashboard"],
    queryFn: () => icloudApi.get<ICloudDashboard>("/dashboard"),
    enabled,
    refetchInterval: 5000,
  })
}

export function useICloudTasks(enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "tasks"],
    queryFn: () => icloudApi.get<{ items: ICloudTask[]; scheduler: ICloudSchedulerState }>("/tasks"),
    enabled,
    refetchInterval: 5000,
  })
}

// ── Apple 账号 ───────────────────────────────────────────────────────────────

export function useICloudAppleAccounts(enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "apple-accounts"],
    queryFn: () => icloudApi.get<{ items: ICloudAppleAccount[]; module_ready: boolean }>("/apple-accounts"),
    enabled,
    refetchInterval: 8000,
  })
}

export function useICloudAppleLoginStart() {
  return useMutation({
    mutationFn: (input: { flow: string; apple_id: string; password: string; two_factor_method: string }) =>
      icloudApi.post<Record<string, unknown>>("/apple-accounts/login/start", input),
  })
}

export function useICloudAppleLogin2FA() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { pending_id: string; code: string; phone_number?: unknown }) =>
      icloudApi.post<Record<string, unknown>>("/apple-accounts/login/2fa", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "apple-accounts"] }),
  })
}

export function useICloudAppleCheck() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => icloudApi.post<{ account: ICloudAppleAccount }>(`/apple-accounts/${encodeURIComponent(id)}/check`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "apple-accounts"] }),
  })
}

export function useICloudAppleSaveIMAP() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...input }: { id: string; email: string; app_password: string }) =>
      icloudApi.post<{ account: ICloudAppleAccount }>(`/apple-accounts/${encodeURIComponent(id)}/imap`, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "apple-accounts"] }),
  })
}

export function useICloudDeleteAppleAccount() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => icloudApi.delete<unknown>(`/apple-accounts/${encodeURIComponent(id)}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["icloud", "apple-accounts"] })
      qc.invalidateQueries({ queryKey: ["icloud", "dashboard"] })
    },
  })
}

export function useICloudCreateMailbox() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...input }: { id: string; label: string; note: string; channel: string }) =>
      icloudApi.post<{ mailbox: ICloudMailbox }>(`/apple-accounts/${encodeURIComponent(id)}/mailboxes`, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["icloud", "mailboxes"] })
      qc.invalidateQueries({ queryKey: ["icloud", "dashboard"] })
    },
  })
}

export function useICloudSyncMailboxes() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      icloudApi.post<{ items: ICloudMailbox[]; count: number }>(`/apple-accounts/${encodeURIComponent(id)}/mailboxes/sync`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "mailboxes"] }),
  })
}

// ── 邮箱池 ───────────────────────────────────────────────────────────────────

export interface ICloudMailboxParams {
  page: number
  pageSize: number
  q?: string
  status?: string
  account_id?: string
}

export function useICloudMailboxes(params: ICloudMailboxParams, enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "mailboxes", params],
    queryFn: () =>
      icloudApi.get<ICloudMailboxPage>("/mailboxes", {
        page: params.page,
        page_size: params.pageSize,
        q: params.q,
        status: params.status,
        account_id: params.account_id,
      }),
    enabled,
    placeholderData: (prev) => prev,
  })
}

export function useICloudImportMailbox() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { account_id: string; email: string; label: string; note: string }) =>
      icloudApi.post<{ mailbox: ICloudMailbox; created: boolean }>("/mailboxes", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "mailboxes"] }),
  })
}

export function useICloudUpdateMailboxStatus() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...input }: { id: string; api_active?: boolean; icloud_active?: boolean; status?: string; note?: string }) =>
      icloudApi.post<{ mailbox: ICloudMailbox }>(`/mailboxes/${encodeURIComponent(id)}/status`, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "mailboxes"] }),
  })
}

export function useICloudDeleteMailbox() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, localOnly }: { id: string; localOnly: boolean }) =>
      icloudApi.delete<unknown>(`/mailboxes/${encodeURIComponent(id)}`, { local_only: localOnly }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["icloud", "mailboxes"] })
      qc.invalidateQueries({ queryKey: ["icloud", "dashboard"] })
    },
  })
}

export function useICloudSyncMailboxMessages() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => icloudApi.post<Record<string, unknown>>(`/mailboxes/${encodeURIComponent(id)}/sync`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "mailboxes"] }),
  })
}

export function useICloudMailboxMessages(id: string | null, enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "mailboxes", id, "messages"],
    queryFn: () => icloudApi.get<{ items: ICloudMessage[] }>(`/mailboxes/${encodeURIComponent(id!)}/messages`),
    enabled: enabled && !!id,
  })
}

export function useICloudMailboxCode() {
  return useMutation({
    mutationFn: ({ id, ...params }: { id: string; after?: string; keyword?: string; allow_stale?: boolean }) =>
      icloudApi.get<ICloudCodeResult>(`/mailboxes/${encodeURIComponent(id)}/code`, params),
  })
}

// ── 调度器(定时创建) ──────────────────────────────────────────────────────────

export function useICloudSchedulerStatus(enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "scheduler"],
    queryFn: () => icloudApi.get<{ scheduler: ICloudSchedulerState; defaults: Record<string, unknown> }>("/scheduler/status"),
    enabled,
    refetchInterval: 4000,
  })
}

export function useICloudSchedulerStart() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (cfg: Record<string, unknown>) => icloudApi.post<{ scheduler: ICloudSchedulerState }>("/scheduler/start", cfg),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "scheduler"] }),
  })
}

export function useICloudSchedulerStop() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => icloudApi.post<{ scheduler: ICloudSchedulerState }>("/scheduler/stop"),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "scheduler"] }),
  })
}

// ── 事件 ─────────────────────────────────────────────────────────────────────

export function useICloudEvents(enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "events"],
    queryFn: () => icloudApi.get<{ items: import("./icloud-api").ICloudEvent[] }>("/events"),
    enabled,
    refetchInterval: 5000,
  })
}

export function useICloudClearEvents() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => icloudApi.post<unknown>("/events/clear"),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "events"] }),
  })
}

// ── 设置 ─────────────────────────────────────────────────────────────────────

export function useICloudSettings(enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "settings"],
    queryFn: () => icloudApi.get<{ settings: ICloudSettings; runtime: Record<string, unknown> }>("/settings"),
    enabled,
  })
}

export function useICloudSaveSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (settings: Partial<ICloudSettings> & { clear_server_chan_send_key?: boolean }) =>
      icloudApi.put<{ settings: ICloudSettings }>("/settings", settings),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "settings"] }),
  })
}

export function useICloudCreateSettings(enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "create-settings"],
    queryFn: () => icloudApi.get<{ settings: ICloudCreateSettings }>("/create-settings"),
    enabled,
  })
}

export function useICloudSaveCreateSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (settings: ICloudCreateSettings) => icloudApi.put<{ settings: ICloudCreateSettings }>("/create-settings", settings),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "create-settings"] }),
  })
}

export function useICloudServerChanTest() {
  return useMutation({
    mutationFn: (input: { send_key?: string; hide_ip?: boolean }) =>
      icloudApi.post<{ message: string; push_id?: string }>("/server-chan/test", input),
  })
}

// ── 以下补齐原项目迁移缺失的功能(批量/彻底删除/远程清理/解析/同步/导出/更新) ──

// 单个邮箱详情
export function useICloudMailbox(id: string | null, enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "mailboxes", id],
    queryFn: () => icloudApi.get<{ mailbox: ICloudMailbox }>(`/mailboxes/${encodeURIComponent(id!)}`),
    enabled: enabled && !!id,
  })
}

// 单封邮件详情
export function useICloudMailboxMessage(mailboxId: string | null, messageId: string | null, enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "mailboxes", mailboxId, "messages", messageId],
    queryFn: () =>
      icloudApi.get<{ message: ICloudMessage }>(
        `/mailboxes/${encodeURIComponent(mailboxId!)}/messages/${encodeURIComponent(messageId!)}`,
      ),
    enabled: enabled && !!mailboxId && !!messageId,
  })
}

// 删除单个邮箱。localOnly=true 只删本地记录;false(彻底)先清云端邮件再删。
// 批量/彻底删除队列在视图层用此 hook 并发执行并展示进度(对齐原项目删除队列)。
export function useICloudDeleteMailboxOne() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, localOnly }: { id: string; localOnly: boolean }) =>
      icloudApi.delete<unknown>(`/mailboxes/${encodeURIComponent(id)}`, { local_only: localOnly }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["icloud", "mailboxes"] })
      qc.invalidateQueries({ queryKey: ["icloud", "dashboard"] })
    },
  })
}

// 解析邮箱地址列表 → 命中的邮箱 + 未找到的邮箱(用于批量彻底删除前解析)。
// 后端返回 items:[{id,email,account_id}], missing:[email,...]。
export function useICloudResolveMailboxEmails() {
  return useMutation({
    mutationFn: (emails: string[]) =>
      icloudApi.post<{ items: ICloudResolvedMailbox[]; missing: string[] }>("/mailboxes/resolve", { emails }),
  })
}

// 远程清理:单个邮箱
export function useICloudRemoteCleanMailbox() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => icloudApi.post<Record<string, unknown>>(`/mailboxes/${encodeURIComponent(id)}/remote-clean`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "mailboxes"] }),
  })
}

// 远程清理:批量(全部或按账号)
export function useICloudRemoteCleanMailboxes() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { mailbox_ids?: string[]; account_id?: string }) =>
      icloudApi.post<Record<string, unknown>>("/mailboxes/remote-clean", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "mailboxes"] }),
  })
}

// 批量同步邮件(触发后台任务)
export function useICloudSyncAllMessages() {
  return useMutation({
    mutationFn: (input?: { account_id?: string; full_scan?: boolean }) =>
      icloudApi.post<Record<string, unknown>>("/mailboxes/sync-messages", input ?? {}),
  })
}

// 批量同步邮件进度
export function useICloudSyncAllMessagesStatus(enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "mailboxes", "sync-messages", "status"],
    queryFn: () => icloudApi.get<{ job: ICloudMessageSyncJob }>("/mailboxes/sync-messages/status"),
    enabled,
    refetchInterval: (q) => ((q.state.data as { job?: ICloudMessageSyncJob } | undefined)?.job?.running ? 2000 : 8000),
  })
}

// 彻底删除全部 Apple 账号云端+本地邮件
export function useICloudAppleMailCleanupStart() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => icloudApi.post<Record<string, unknown>>("/apple-mail/cleanup"),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["icloud", "apple-mail-cleanup"] })
      qc.invalidateQueries({ queryKey: ["icloud", "mailboxes"] })
    },
  })
}

export function useICloudAppleMailCleanupStatus(enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "apple-mail-cleanup", "status"],
    queryFn: () => icloudApi.get<{ job: ICloudAppleMailCleanupStatus }>("/apple-mail/cleanup/status"),
    enabled,
    refetchInterval: (q) => ((q.state.data as { job?: ICloudAppleMailCleanupStatus } | undefined)?.job?.running ? 2000 : 8000),
  })
}

export function useICloudAppleMailCleanupCancel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => icloudApi.post<unknown>("/apple-mail/cleanup/cancel"),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "apple-mail-cleanup"] }),
  })
}

// 清空调度器日志
export function useICloudSchedulerClearLogs() {
  return useMutation({
    mutationFn: () => icloudApi.post<unknown>("/scheduler/logs/clear"),
  })
}

// 数据库状态 + 维护
export function useICloudDatabaseStatus(enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "database", "status"],
    queryFn: () => icloudApi.get<ICloudDatabaseStatus>("/database/status"),
    enabled,
  })
}

export function useICloudDatabaseAction() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (action: "backup" | "check" | "optimize") =>
      icloudApi.post<Record<string, unknown>>(`/database/${action}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["icloud", "database"] }),
  })
}

// 版本更新检查
export function useICloudUpdateStatus(enabled: boolean) {
  return useQuery({
    queryKey: ["icloud", "update", "status"],
    queryFn: () => icloudApi.get<ICloudUpdateStatus>("/update/status"),
    enabled,
  })
}

// 导出(返回文本内容,前端触发下载)
export function useICloudExportRuntime() {
  return useMutation({
    mutationFn: () => icloudApi.get<Record<string, unknown>>("/runtime/export"),
  })
}

export function useICloudExportMailboxAPIs() {
  return useMutation({
    mutationFn: () => icloudApi.get<{ items: { email: string; api_url: string }[] }>("/runtime/export-mailbox-apis"),
  })
}

export function useICloudExportMailboxEmails() {
  return useMutation({
    mutationFn: () => icloudApi.get<{ items: string[] } | { emails: string[] }>("/runtime/export-mailbox-emails"),
  })
}
