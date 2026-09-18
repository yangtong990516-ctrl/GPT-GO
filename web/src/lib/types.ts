// ── Shared ────────────────────────────────────────────────────────────────────

export interface Page<T> {
  items: T[]
  total: number
  page: number
  pageSize: number
}

export interface ApiError {
  code: string
  message: string
}

export interface HealthResponse {
  status: "ok" | "degraded"
  mode: string
  mongodb: {
    status: string
    database: string
    error: string | null
    nextRetrySeconds: number | null
  }
}

// ── Stats ─────────────────────────────────────────────────────────────────────

export interface OverviewStats {
  accounts: AccountStats
  emails: EmailStats
  proxies: ProxyStats
}

export interface AccountStats {
  total: number
  today: number
  totpComplete: number
  plus: { total: number; bound: number; unbound: number }
  free: { total: number; eligible: number; ineligible: number }
}

export interface EmailStats {
  available: number
  reserved: number
  failed: number
  quarantined: number
  aliases: number
  mailcode: number
  remail: number
}

export interface ProxyStats {
  total: number
  enabled: number
  available: number
  used: number
  quarantined: number
}

// ── Accounts ──────────────────────────────────────────────────────────────────

export interface PromotionCampaign {
  plan: string
  id: string
  promotion_type: string
  title: string
}

export interface TrialCountryResult {
  eligible: boolean | null
  state: string
  signal: string
  paymentMethods: string[]
  error: string
}

export interface AccountRecord {
  id: string
  email: string
  chatgptPassword: string
  totpSecret: string
  totpStatus: string | null
  totpSecretConfigured: boolean
  emailAccessUrl: string
  createdAt: string
  accountType: "plus" | "free"
  phoneBound: boolean | null
  accessTokenConfigured: boolean
  accessTokenExpiresAt: string | null
  accessTokenUpdatedAt: string | null
  refreshToken: string
  accessTokenMissing: boolean
  atRefillStatus: string | null
  atRefillError: string | null
  atRefillErrorAt: string | null
  planCheckStatus: string | null
  planCheckedAt: string | null
  planCheckErrorCode: string | null
  subscriptionPlan: string | null
  planExpiresAt: string | null
  promotionCampaigns: PromotionCampaign[] | null
  trialScanCheckedAt: string | null
  trialScanCountries: Record<string, TrialCountryResult> | null
  trialScanError: string | null
  registrationCountry: string | null
  registrationIp: string | null
  registrationIsp: string | null
  aliveStatus: "running" | "alive" | "dead" | "unknown" | null
  aliveCheckedAt: string | null
  aliveErrorCode: string | null
  aliveHttpStatus: number | null
  rebindStatus: string | null
  remark: string | null
  promotionEligible: boolean | null
  // ── 支付类型检测写回字段 ──
  paymentStatus: string | null
  paymentMethods: string[]
  paymentZeroMethods: string[]
  paymentCheckedAt: string | null
  paymentRoutes: Record<string, PaymentRouteResult> | null
}

// ── 支付类型检测 ──────────────────────────────────────────────────────────────

export interface PaymentRouteResult {
  country: string
  currency: string
  locale: string
  status: string
  state: string
  methods: string[]
  methodsInferred?: string[]
  amountDue: number | null
  zeroStatus: "zero_confirmed" | "not_zero" | "zero_unknown" | ""
  exitIp: string
  exit: string
  exitPurity?: string
  sessionType?: string
  processorEntity?: string
  httpStatus: number
  error?: string
  checkedAt: string
}

export interface PaymentBatchStatus {
  batchId: string
  total: number
  done: number
  skipped: number
  zeroCount: number
  running: boolean
  canceled: boolean
  accounts: Record<string, string>
  startedAt: string
  finishedAt?: string
}

export interface PaymentRoutesConfig {
  routesText: string
  routeCount: number
  enabledRoutes: number
  parseError: string
}

export interface AccountCreateInput {
  email: string
  chatgptPassword: string
  totpSecret: string
  emailAccessUrl: string
  accountType?: "plus" | "free"
  phoneBound?: boolean | null
  promotionEligible?: boolean | null
  sourceEmailId?: string | null
  registrationCountry?: string | null
}

export interface DeleteResult {
  deleted: number
}

// ── Emails ────────────────────────────────────────────────────────────────────

export type EmailStatus = "available" | "reserved" | "failed" | "quarantined"
export type EmailSourceType =
  | "manual"
  | "mailcom_alias"
  | "mailcode"
  | "remail"

export interface EmailRecord {
  id: string
  email: string
  accessUrl: string
  importedAt: string
  sourceType: EmailSourceType
  parentEmail: string | null
  status: EmailStatus
  statusReason: string | null
  statusUpdatedAt: string | null
}

export interface ImportResult {
  total: number
  imported: number
  duplicateCount: number
  errorCount: number
}

export interface TextExport {
  content: string
  filename: string
  count: number
  format: string | null
  skippedMissingCount: number
  skippedExpiredCount: number
}

// ── Mailcode ──────────────────────────────────────────────────────────────────

export interface MailcodeConfig {
  baseUrl: string
  domain: string
  updatedAt: string | null
}

export interface MailcodeProbeResult {
  ok: boolean
  message: string
  reachable: boolean
}

export interface MailboxRecord {
  email: string
  accessUrl: string
  imported: boolean
  duplicate: boolean
  error: string | null
}

// ── Remail ────────────────────────────────────────────────────────────────────

export interface RemailConfig {
  apiKey: string
  projectId: number | null
  emailSuffix: string
  baseUrl: string
  updatedAt: string | null
}

export interface RemailProbeResult {
  ok: boolean
  message: string
  reachable: boolean
}

export interface RemailWallet {
  ok: boolean
  message: string
  // 后端可能返回数字或数字字符串(如 "45.00"),渲染侧用 toAmount 统一兜底。
  consumerBalance: number | string
  totalRecharged: number | string
  historicalSpend: number | string
}

export interface RemailMailboxRecord {
  email: string
  accessUrl: string
  imported: boolean
  duplicate: boolean
  orderNo: string
  error: string | null
}

// ── Proxies ───────────────────────────────────────────────────────────────────

export interface ProxyRecord {
  id: string
  host: string
  port: number
  username: string
  password: string
  enabled: boolean
  status: "available" | "unknown" | "used" | "quarantined"
  latencyMs: number | null
  lastCheckedAt: string | null
  country: string
  group: string
  scheme: string
}

export interface ProxyCountrySummary {
  country: string
  total: number
  enabled: number
}

export interface ProxyGroupSummary {
  country: string
  group: string
  total: number
  enabled: number
  available: number
  used: number
  quarantined: number
  schemes: string[]
}

export interface ProxyTestResult {
  tested: number
  available: number
  failed: number
  averageLatencyMs: number | null
  countries: Record<string, unknown>[]
}

export interface ProxyGroupUpdateResult {
  matched: number
  modified: number
}

export interface RestoreUsedResult {
  restored: number
}

// ── Sentinel ──────────────────────────────────────────────────────────────────

export interface SentinelConfig {
  enabled: boolean
  interval_hours: number
  proxy: string
  error?: string
}

export interface SentinelVersionInfo {
  version: string
  url: string | null
  etag: string | null
  last_modified: string | null
  content_length: number | null
  reachable: boolean
  configured_version: string
  is_expired: boolean | null
  last_checked_at: string | null
  proxy_used: boolean
  check_error: string | null
}

// ── Settings ──────────────────────────────────────────────────────────────────

export interface ExecutionSettings {
  schemaVersion: number
  enableRegistrationSecurity: boolean
  registrationMode: string
  proxyRetryCount: number
  proxyCheckConcurrency: number
  maxRegistrationsPerExitIp: number
  concurrency: number
  taskTimeoutSeconds: number
  updatedAt: string | null
}

// ── Run Logs(注册运行日志) ────────────────────────────────────────────────────

export type RunLogLevel = "info" | "success" | "warning" | "error"

export interface RunLogEntry {
  timestamp: string
  level: RunLogLevel
  event: string
  message: string
  email?: string
  sequence: number
  details?: Record<string, unknown>
}

export interface RunLogSummary {
  runId: string
  startedAt: string
  updatedAt: string
  entryCount: number
  lastEvent: string
  terminal: boolean
}

export interface RunLogFile extends RunLogSummary {
  entries: RunLogEntry[]
}

// ── Runs(注册运行批次进度) ────────────────────────────────────────────────────

export type RunStatus =
  | "running"
  | "succeeded"
  | "partial_success"
  | "failed"
  | "cancelled"

export interface RunState {
  runId: string
  kind: string
  status: RunStatus
  requested: number
  pending: number
  processed: number
  succeeded: number
  failed: number
  cancelled: number
  successRate: number
  registrationCountry?: string
  registrationProxyGroup?: string
  emailSource?: string
  workerCount: number
  startedAt: string
  updatedAt: string
  finishedAt?: string
  cancelRequested: boolean
}

export interface CreateRunInput {
  count: number
  country?: string
  group?: string
  emailSource?: string
  runId?: string
  stopOnError?: boolean
  otpTimeout?: number
  leaseSeconds?: number
}

export interface CreateRunResponse {
  ok: number
  failed: number
  cancelled: number
  total: number
  runId: string
  errors?: string[]
  applied: {
    concurrency: number
    maxRegistrationsPerExitIp: number
    taskTimeoutSeconds: number
  }
}
