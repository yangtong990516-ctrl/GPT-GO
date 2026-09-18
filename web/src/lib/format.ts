// Shared display/formatting helpers.

const COUNTRY_NAMES: Record<string, string> = {
  US: "美国",
  GB: "英国",
  JP: "日本",
  SG: "新加坡",
  HK: "中国香港",
  TW: "中国台湾",
  DE: "德国",
  FR: "法国",
  NL: "荷兰",
  CA: "加拿大",
  AU: "澳大利亚",
  KR: "韩国",
  IN: "印度",
  BR: "巴西",
  VN: "越南",
  TH: "泰国",
  ID: "印度尼西亚",
  MY: "马来西亚",
  PH: "菲律宾",
  RU: "俄罗斯",
  TR: "土耳其",
  MX: "墨西哥",
  ES: "西班牙",
  IT: "意大利",
  SE: "瑞典",
  CH: "瑞士",
  ZZ: "未识别",
}

export function countryLabel(code: string | null | undefined): string {
  if (!code) return "-"
  const up = code.toUpperCase()
  const name = COUNTRY_NAMES[up]
  return name ? `${name} (${up})` : up
}

// 全项目统一北京时间(UTC+8):不依赖浏览器本地时区。
// 后端时间戳均为 UTC ISO 串,这里统一 +8h 换算,保证任何环境看到一致的时间。
const BEIJING_OFFSET_MS = 8 * 60 * 60 * 1000

/** toBeijing 把任意时间转为北京时间 Date(用于读取其 Y/M/D/h/m/s 字段)。 */
function toBeijing(d: Date): Date {
  // d.getTime() 是绝对毫秒;直接 +8h 后用 getUTC* 读即为北京时间字段。
  return new Date(d.getTime() + BEIJING_OFFSET_MS)
}

export function formatTime(iso: string | null | undefined): string {
  if (!iso) return "-"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const b = toBeijing(d)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${b.getUTCFullYear()}-${pad(b.getUTCMonth() + 1)}-${pad(b.getUTCDate())} ${pad(b.getUTCHours())}:${pad(b.getUTCMinutes())}:${pad(b.getUTCSeconds())}`
}

/** formatTimeOfDay 北京时间 时分秒(日志行用)。 */
export function formatTimeOfDay(iso: string | null | undefined): string {
  if (!iso) return "--:--:--"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return "--:--:--"
  const b = toBeijing(d)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${pad(b.getUTCHours())}:${pad(b.getUTCMinutes())}:${pad(b.getUTCSeconds())}`
}

/** formatTimeShort 北京时间 MM-DD HH:mm(调度日志用)。 */
export function formatTimeShort(iso: string | null | undefined): string {
  if (!iso) return "-"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const b = toBeijing(d)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${pad(b.getUTCMonth() + 1)}-${pad(b.getUTCDate())} ${pad(b.getUTCHours())}:${pad(b.getUTCMinutes())}`
}

export function formatRelative(iso: string | null | undefined): string {
  if (!iso) return "-"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const diff = Date.now() - d.getTime()
  const abs = Math.abs(diff)
  const min = Math.floor(abs / 60000)
  const hour = Math.floor(min / 60)
  const day = Math.floor(hour / 24)
  if (min < 1) return "刚刚"
  if (min < 60) return `${min} 分钟前`
  if (hour < 24) return `${hour} 小时前`
  if (day < 30) return `${day} 天前`
  return formatTime(iso)
}

export function maskMiddle(value: string, head = 6, tail = 4): string {
  if (!value) return "-"
  if (value.length <= head + tail + 3) return value
  return `${value.slice(0, head)}…${value.slice(-tail)}`
}

export const CAMPAIGN_LABELS: Record<string, string> = {
  "plus-1-month-free": "免费试用 1 个月",
  "plus-2-months-free": "免费试用 2 个月",
  "plus-3-months-free": "免费试用 3 个月",
  "plus-1-month-50-pct-off": "首月 5 折",
  "plus-2-months-50-pct-off": "前 2 个月 5 折",
  "plus-3-months-50-pct-off": "前 3 个月 5 折",
  "plus-1-month-25-pct-off": "首月立减 25%",
  "plus-6-months-50-pct-off": "前 6 个月 5 折",
  "plus-1-month-75-pct-off": "首月立减 75%",
}

export function campaignLabel(id: string): string {
  return CAMPAIGN_LABELS[id] ?? id
}
