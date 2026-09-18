import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "./api"
import type {
  AccountCreateInput,
  AccountRecord,
  DeleteResult,
  ExecutionSettings,
  HealthResponse,
  ImportResult,
  MailboxRecord,
  MailcodeConfig,
  MailcodeProbeResult,
  OverviewStats,
  Page,
  PaymentBatchStatus,
  PaymentRouteResult,
  PaymentRoutesConfig,
  ProxyCountrySummary,
  ProxyGroupSummary,
  ProxyGroupUpdateResult,
  ProxyRecord,
  ProxyTestResult,
  RemailConfig,
  RemailMailboxRecord,
  RemailProbeResult,
  RemailWallet,
  RestoreUsedResult,
  SentinelConfig,
  SentinelVersionInfo,
  TextExport,
} from "./types"

// ── Health / Stats ────────────────────────────────────────────────────────────

export function useHealth() {
  return useQuery({
    queryKey: ["health"],
    queryFn: () => api.get<HealthResponse>("/api/health"),
    refetchInterval: 30_000,
    retry: false,
  })
}

export function useOverviewStats() {
  return useQuery({
    queryKey: ["stats", "overview"],
    queryFn: () => api.get<OverviewStats>("/api/stats/overview"),
  })
}

// ── Accounts ──────────────────────────────────────────────────────────────────

export interface AccountListParams {
  page: number
  pageSize: number
  q?: string
  promotion?: string
  country?: string
  alive?: string
  payment?: string
  zero_payment?: string
  payment_status?: string
}

export function useAccounts(params: AccountListParams) {
  return useQuery({
    queryKey: ["accounts", params],
    queryFn: () =>
      api.get<Page<AccountRecord>>("/api/accounts", {
        page: params.page,
        pageSize: params.pageSize,
        q: params.q,
        promotion: params.promotion,
        country: params.country,
        alive: params.alive,
        payment: params.payment,
        zero_payment: params.zero_payment,
        payment_status: params.payment_status,
      }),
    placeholderData: (prev) => prev,
  })
}

export function useCreateAccount() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: AccountCreateInput) =>
      api.post<AccountRecord>("/api/accounts", input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

export function useBulkDeleteAccounts() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (ids: string[]) =>
      api.post<DeleteResult>("/api/accounts/bulk-delete", { ids }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

// ── 账号批量「验活 + 查询优惠资格」─────────────────────────────────────────────
// 后端 POST /api/accounts/check-combined:一次请求同时写 alive + plan 两组字段。
// 对齐 codex-auto 的 check-alive / check-promotion 批量语义。
export interface AccountCheckItem {
  id: string
  status: string // alive / dead / failed / skipped
  planStatus: string // success / failed / skipped
  errorCode?: string
  httpStatus?: number
}
export interface AccountCheckResult {
  requested: number
  alive: number
  dead: number
  failed: number
  skipped: number
  items: AccountCheckItem[]
}

export function useCheckCombinedAccounts() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { ids: string[]; proxyId?: string }) =>
      api.post<AccountCheckResult>("/api/accounts/check-combined", {
        ids: input.ids,
        proxyId: input.proxyId,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

// ── 账号批量「查询 2FA 状态」─────────────────────────────────────────────────
// 后端 POST /api/accounts/check-2fa:用已存 accessToken 查 /me,回写 totpStatus/mfaFlagEnabled。
// 对齐 codex check_2fa_status;无需重认证,只读查询。
export interface Check2FAItem {
  id: string
  status: string // success / failed / skipped
  totpStatus?: string
  mfaFlagEnabled?: boolean
  errorCode?: string
}
export interface Check2FAResult {
  requested: number
  succeeded: number
  failed: number
  skipped: number
  items: Check2FAItem[]
}

export function useCheck2FAAccounts() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { ids: string[]; proxyId?: string }) =>
      api.post<Check2FAResult>("/api/accounts/check-2fa", {
        ids: input.ids,
        proxyId: input.proxyId,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

// ── 账号「补 2FA(开通 TOTP)」────────────────────────────────────────────────
// 后端 POST /api/accounts/{id}/ensure-2fa(单号)与 /api/accounts/bulk-ensure-2fa(批量)。
// 对齐 codex ensure_totp 有密码分支:协议登录拿 recent_auth AT → enroll+activate → 落库。
export interface Ensure2FAItem {
  id: string
  status: string // success / failed / skipped
  error?: string
  totpSecretConfigured?: boolean
}
export interface Ensure2FAResult {
  requested: number
  succeeded: number
  failed: number
  skipped: number
  items: Ensure2FAItem[]
}

// 单账号补 2FA。
export function useEnsure2FA() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { id: string; proxyId?: string }) =>
      api.post<Ensure2FAItem>(`/api/accounts/${input.id}/ensure-2fa`, {
        proxyId: input.proxyId,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

// 批量补 2FA(对选中账号)。
export function useBulkEnsure2FA() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { ids: string[]; proxyId?: string }) =>
      api.post<Ensure2FAResult>("/api/accounts/bulk-ensure-2fa", {
        ids: input.ids,
        proxyId: input.proxyId,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

// ── Token Heal(session cookie 续 AT,docs/SESSION-TOKEN-HEAL.md)───────────────
// 后端 POST /api/accounts/{id}/heal(单号)与 /api/accounts/bulk-heal(批量):
// 用落库 session cookie 单次只读 GET /api/auth/session 换新 AT,无需密码/OTP/2FA。
export interface HealItem {
  id: string
  status: string // success / failed / skipped
  error?: string
}
export interface HealResult {
  requested: number
  succeeded: number
  failed: number
  skipped: number
  items: HealItem[]
}

// 单账号续 AT。
export function useHealAccount() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { id: string; proxyId?: string }) =>
      api.post<HealItem>(`/api/accounts/${input.id}/heal`, { proxyId: input.proxyId }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

// 批量续 AT(对选中账号)。
export function useBulkHealAccounts() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { ids: string[]; proxyId?: string }) =>
      api.post<HealResult>("/api/accounts/bulk-heal", { ids: input.ids, proxyId: input.proxyId }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

// ── Payment Check（支付类型检测）─────────────────────────────────────────────

export interface PaymentRouteSpec {
  country: string
  currency: string
  locale: string
  proxies: string[]
}

export interface PaymentRunInput {
  ids?: string[]
  tokens?: { accessToken: string; label?: string }[]
  routes?: PaymentRouteSpec[]
  routesText?: string
  writeBack?: boolean
}

export function usePaymentRoutesConfig() {
  return useQuery({
    queryKey: ["payment-check", "routes-config"],
    queryFn: () => api.get<PaymentRoutesConfig>("/api/payment-check/routes-config"),
  })
}

export function usePaymentBatchStatus(enabled: boolean) {
  return useQuery({
    queryKey: ["payment-check", "status"],
    queryFn: () => api.get<PaymentBatchStatus | { running: false }>("/api/payment-check/status"),
    refetchInterval: enabled ? 2000 : false,
  })
}

export function useRunPaymentCheck() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: PaymentRunInput) =>
      api.post<{ ok: boolean; batchId: string; total: number }>("/api/payment-check/run", {
        ids: input.ids,
        tokens: input.tokens,
        routes: input.routes,
        routesText: input.routesText,
        writeBack: input.writeBack ?? true,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["payment-check", "status"] })
    },
  })
}

export function useCancelPaymentCheck() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post<{ ok: boolean }>("/api/payment-check/cancel"),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["payment-check", "status"] })
    },
  })
}

export interface PaymentProxyItem {
  id: string
  host: string
  port: number
  username: string
  password: string
  scheme: string
  country: string
  group: string
  status: string
}

// 代理池可用代理列表（线路点选器用；只展示 enabled+available）。
export function usePaymentProxies(country?: string) {
  return useQuery({
    queryKey: ["payment-check", "proxies", country ?? "all"],
    queryFn: () =>
      api.get<{ items: PaymentProxyItem[]; total: number }>("/api/payment-check/proxies", {
        country,
      }),
  })
}

export interface PaymentBatchItem {
  id: string
  label: string
  status: string
  methods: string[]
  zeroMethods: string[]
  routes: Record<string, PaymentRouteResult>
}

// 批次完成账号的线路明细（粘贴 AT 模式的结果矩阵从这里拉）。
export function usePaymentBatchItems(enabled: boolean) {
  return useQuery({
    queryKey: ["payment-check", "items"],
    queryFn: () => api.get<{ items: PaymentBatchItem[] }>("/api/payment-check/items"),
    refetchInterval: enabled ? 2000 : false,
  })
}

// ── 换绑（rebind）──

export interface RebindBatchStatus {
  batchId: string
  total: number
  done: number
  success: number
  failed: number
  running: boolean
  canceled: boolean
  accounts: Record<string, string>
  startedAt: string
  finishedAt?: string
}

export interface RebindItem {
  id: string
  oldEmail: string
  newEmail: string
  status: string
  error?: string
  proxy?: string
  startedAt: string
  completedAt: string
}

export interface RebindPools {
  availableEmails: number
  eligibleProxies: number
}

// 换绑批次状态轮询（运行中 2s）。
export function useRebindStatus(enabled: boolean) {
  return useQuery({
    queryKey: ["rebind", "status"],
    queryFn: () => api.get<RebindBatchStatus | { running: false }>("/api/rebind/status"),
    refetchInterval: enabled ? 2000 : false,
  })
}

// 换绑完成账号明细（结果矩阵用）。
export function useRebindItems(enabled: boolean) {
  return useQuery({
    queryKey: ["rebind", "items"],
    queryFn: () => api.get<{ items: RebindItem[] }>("/api/rebind/items"),
    refetchInterval: enabled ? 2000 : false,
  })
}

// 换绑资源池（可用邮箱/代理数量）。
export function useRebindPools(country?: string) {
  return useQuery({
    queryKey: ["rebind", "pools", country ?? "all"],
    queryFn: () => api.get<RebindPools>("/api/rebind/pools", { country }),
  })
}

export function useRunRebind() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { ids: string[]; country?: string; group?: string; pairs?: Record<string, string> }) =>
      api.post<{ ok: boolean; batchId: string; total: number }>("/api/rebind/run", input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["rebind"] })
    },
  })
}

export function useCancelRebind() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post<{ ok: boolean }>("/api/rebind/cancel"),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["rebind"] })
    },
  })
}

export function usePaymentAccountRoutes(accountId: string | null) {
  return useQuery({
    queryKey: ["payment-check", "account-routes", accountId],
    queryFn: () =>
      api.get<{ routes: Record<string, PaymentRouteResult> }>(
        `/api/payment-check/accounts/${encodeURIComponent(accountId!)}/routes`,
      ),
    enabled: !!accountId,
  })
}

// ── Emails ────────────────────────────────────────────────────────────────────

export interface EmailListParams {
  page: number
  pageSize: number
  q?: string
  source?: string
  status?: string
}

export function useEmails(params: EmailListParams) {
  return useQuery({
    queryKey: ["emails", params],
    queryFn: () =>
      api.get<Page<import("./types").EmailRecord>>("/api/emails", {
        page: params.page,
        pageSize: params.pageSize,
        q: params.q,
        source: params.source || "all",
        status: params.status || "available",
      }),
    placeholderData: (prev) => prev,
  })
}

// useEmailSourceOptions 拉取可用邮箱,按来源聚合出「注册来源下拉」选项。
// 直接从邮箱池实际数据取(池里有什么来源就显示什么),而非写死 mailcode。
export function useEmailSourceOptions() {
  return useQuery({
    queryKey: ["emails", "sourceOptions"],
    queryFn: async () => {
      // 拉全部可用邮箱做来源聚合。后端 pageSize 上限 100,故分页循环拉取直到取完。
      const counts = new Map<string, number>()
      const pageSize = 100
      let page = 1
      // 安全上限:最多 200 页(2 万邮箱),防异常死循环。
      for (let guard = 0; guard < 200; guard++) {
        const resp = await api.get<Page<import("./types").EmailRecord>>("/api/emails", {
          page,
          pageSize,
          status: "available",
          source: "all",
        })
        const items = resp.items ?? []
        for (const e of items) {
          const st = e.sourceType || "manual"
          counts.set(st, (counts.get(st) ?? 0) + 1)
        }
        // 取完判定:本页不满 pageSize 说明已是最后一页。
        if (items.length < pageSize) break
        page++
      }
      return Array.from(counts.entries())
        .map(([sourceType, count]) => ({ sourceType, count }))
        .sort((a, b) => b.count - a.count) // 可用数多的排前
    },
    refetchInterval: 5000, // 邮箱池随注册消耗,周期刷新可用数
  })
}

export function useImportEmails() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (rawText: string) =>
      api.post<ImportResult>("/api/emails/import", { rawText }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["emails"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

export function useSyncMailcomAliases() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post<ImportResult>("/api/emails/sync-mailcom-aliases"),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["emails"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

export function useBulkDeleteEmails() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (ids: string[]) =>
      api.post<DeleteResult>("/api/emails/bulk-delete", { ids }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["emails"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

export function useResetFailedEmails() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (ids: string[] | null) =>
      api.post<{ reset: number }>("/api/emails/reset-failed", ids ? { ids } : {}),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["emails"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

export function useUpdateEmailStatus() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, status }: { id: string; status: string }) =>
      api.post(`/api/emails/${encodeURIComponent(id)}/status`, { status }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["emails"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

export function useExportEmails() {
  return useMutation({
    mutationFn: (input: { scope: string; ids?: string[] }) =>
      api.post<TextExport>("/api/emails/export", input),
  })
}

// ── Mailcode ──────────────────────────────────────────────────────────────────

export function useMailcodeConfig() {
  return useQuery({
    queryKey: ["mailcode", "config"],
    queryFn: () => api.get<MailcodeConfig>("/api/mailcode/config"),
  })
}

export function useSaveMailcodeConfig() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { baseUrl: string; domain: string }) =>
      api.put<MailcodeConfig>("/api/mailcode/config", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["mailcode"] }),
  })
}

export function useProbeMailcode() {
  return useMutation({
    mutationFn: (baseUrl: string) =>
      api.post<MailcodeProbeResult>("/api/mailcode/probe", { baseUrl }),
  })
}

export function useCreateMailcodeMailboxes() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: {
      emails: string[]
      count: number
      prefix: string
      domain: string
    }) => api.post<MailboxRecord[]>("/api/mailcode/create-mailboxes", input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["emails"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

// ── Remail ────────────────────────────────────────────────────────────────────

export function useRemailConfig() {
  return useQuery({
    queryKey: ["remail", "config"],
    queryFn: () => api.get<RemailConfig>("/api/remail/config"),
  })
}

export function useSaveRemailConfig() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: {
      apiKey: string
      projectId: number
      emailSuffix: string
      baseUrl: string
    }) => api.put<RemailConfig>("/api/remail/config", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["remail"] }),
  })
}

export function useProbeRemail() {
  return useMutation({
    mutationFn: (apiKey: string) =>
      api.post<RemailProbeResult>("/api/remail/probe", { apiKey }),
  })
}

export function useRemailWallet(enabled = true) {
  return useQuery({
    queryKey: ["remail", "wallet"],
    queryFn: () => api.get<RemailWallet>("/api/remail/wallet"),
    enabled,
    retry: false,
  })
}

export function useCreateRemailMailboxes() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { count: number; emailSuffix?: string }) =>
      api.post<RemailMailboxRecord[]>("/api/remail/create-mailboxes", input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["emails"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
      qc.invalidateQueries({ queryKey: ["remail", "wallet"] })
    },
  })
}

export function useImportRemailOrders() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: {
      limit: number
      maxOrders: number
      productType: string
      emailSuffix: string
      onlyIcloud: boolean
    }) => api.post<RemailMailboxRecord[]>("/api/remail/import-purchased-orders", input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["emails"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

// ── Proxies ───────────────────────────────────────────────────────────────────

export interface ProxyListParams {
  page: number
  pageSize: number
  q?: string
  country?: string
}

export function useProxies(params: ProxyListParams) {
  return useQuery({
    queryKey: ["proxies", params],
    queryFn: () =>
      api.get<Page<ProxyRecord>>("/api/proxies", {
        page: params.page,
        pageSize: params.pageSize,
        q: params.q,
        country: params.country,
      }),
    placeholderData: (prev) => prev,
  })
}

export function useProxyCountries() {
  return useQuery({
    queryKey: ["proxies", "countries"],
    queryFn: () => api.get<ProxyCountrySummary[]>("/api/proxies/countries"),
  })
}

export function useProxyGroups() {
  return useQuery({
    queryKey: ["proxies", "groups"],
    queryFn: () => api.get<ProxyGroupSummary[]>("/api/proxies/groups"),
  })
}

export function useImportProxies() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { rawText: string; country?: string; group?: string }) =>
      api.post<ImportResult>("/api/proxies/import", input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxies"] })
      qc.invalidateQueries({ queryKey: ["payment-check"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

export function useTestProxies() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { country?: string; group?: string; timeoutSeconds?: number }) =>
      api.post<ProxyTestResult>("/api/proxies/test", input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxies"] })
      qc.invalidateQueries({ queryKey: ["payment-check"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

export function useUpdateProxyGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: {
      country: string
      group: string
      newCountry?: string
      newGroup?: string
      enabled?: boolean
    }) => api.patch<ProxyGroupUpdateResult>("/api/proxies/groups", input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxies"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

export function useDeleteProxyGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { country: string; group: string }) =>
      api.delete<DeleteResult>("/api/proxies/groups", {
        country: input.country,
        group: input.group,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxies"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

export function useBulkDeleteProxies() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (ids: string[]) =>
      api.post<DeleteResult>("/api/proxies/bulk-delete", { ids }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxies"] })
      qc.invalidateQueries({ queryKey: ["payment-check"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

export function useClearProxies() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.delete<DeleteResult>("/api/proxies"),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxies"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

export function useRestoreUsedProxies() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { country?: string; group?: string }) =>
      api.post<RestoreUsedResult>("/api/proxies/restore-used", input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxies"] })
      qc.invalidateQueries({ queryKey: ["payment-check"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

export function useUpdateProxy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      id,
      ...input
    }: { id: string; enabled?: boolean; country?: string; group?: string }) =>
      api.patch<ProxyRecord>(`/api/proxies/${encodeURIComponent(id)}`, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxies"] })
      qc.invalidateQueries({ queryKey: ["payment-check"] })
    },
  })
}

export function useUpdateProxyStatus() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, status }: { id: string; status: string }) =>
      api.post(`/api/proxies/${encodeURIComponent(id)}/status`, { status }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxies"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
      qc.invalidateQueries({ queryKey: ["payment-check"] })
    },
  })
}

export function useDeleteProxy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      api.delete<DeleteResult>(`/api/proxies/${encodeURIComponent(id)}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxies"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
    },
  })
}

// ── Sentinel ──────────────────────────────────────────────────────────────────

export function useSentinelConfig() {
  return useQuery({
    queryKey: ["sentinel", "config"],
    queryFn: () => api.get<SentinelConfig>("/api/sentinel/config"),
  })
}

export function useSaveSentinelConfig() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: {
      enabled?: boolean
      interval_hours?: number
      proxy?: string
    }) => api.put<SentinelConfig>("/api/sentinel/config", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["sentinel"] }),
  })
}

export function useSentinelVersion() {
  return useQuery({
    queryKey: ["sentinel", "version"],
    queryFn: () => api.get<SentinelVersionInfo>("/api/sentinel/version"),
  })
}

// 手动触发一次真实版本探测(走代理,落库),区别于 useSentinelVersion(只读缓存)。
export function useRunSentinelCheck() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post<SentinelVersionInfo>("/api/sentinel/check", {}),
    onSuccess: (data) => {
      // 用真实探测结果直接更新版本缓存,UI 立刻反映。
      qc.setQueryData(["sentinel", "version"], data)
    },
  })
}

// ── Settings ──────────────────────────────────────────────────────────────────

export function useExecutionSettings() {
  return useQuery({
    queryKey: ["settings", "execution"],
    queryFn: () => api.get<ExecutionSettings>("/api/settings/execution"),
  })
}

export function useSaveExecutionSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: Omit<ExecutionSettings, "schemaVersion" | "updatedAt">) =>
      api.put<ExecutionSettings>("/api/settings/execution", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["settings"] }),
  })
}

// ── Run Logs(注册运行日志) ────────────────────────────────────────────────────

export function useRunLogs() {
  return useQuery({
    queryKey: ["runlogs", "runs"],
    queryFn: () => api.get<import("./types").RunLogSummary[]>("/api/run-logs/runs"),
    refetchInterval: 5000, // 摘要列表轻量轮询(发现新 run / 终态变化)
  })
}

// ── Runs(注册运行批次) ────────────────────────────────────────────────────────

export function useRuns() {
  return useQuery({
    queryKey: ["runs"],
    // 后端返回 {runs: [...]}(对象包裹),解包取数组。
    queryFn: async () =>
      (await api.get<{ runs: import("./types").RunState[] }>("/api/runs")).runs ?? [],
    refetchInterval: 3000, // 批次进度实时刷新
  })
}

export function useRun(runId: string | null) {
  return useQuery({
    queryKey: ["runs", runId],
    queryFn: () => api.get<import("./types").RunState>(`/api/runs/${encodeURIComponent(runId!)}`),
    enabled: !!runId,
    refetchInterval: 1500, // 选中批次进度实时刷新(日志面板顶部)
  })
}

export function useCreateRun() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: import("./types").CreateRunInput) =>
      api.post<import("./types").CreateRunResponse>("/api/runs", input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["runs"] })
      qc.invalidateQueries({ queryKey: ["runlogs"] })
      qc.invalidateQueries({ queryKey: ["stats"] })
      qc.invalidateQueries({ queryKey: ["accounts"] })
    },
  })
}

