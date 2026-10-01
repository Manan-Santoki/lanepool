import { differenceInSeconds, format as formatDate, formatDistanceStrict, isValid, parseISO } from "date-fns"

const BYTE_UNITS = ["B", "KB", "MB", "GB", "TB", "PB"] as const

/** 1536 -> "1.5 KB". Uses 1024-based units, labelled KB/MB/GB as admins expect. */
export function formatBytes(bytes: number | null | undefined, digits = 1): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes)) return "–"
  if (bytes === 0) return "0 B"
  const sign = bytes < 0 ? "-" : ""
  let value = Math.abs(bytes)
  let unit = 0
  while (value >= 1024 && unit < BYTE_UNITS.length - 1) {
    value /= 1024
    unit++
  }
  const d = unit === 0 ? 0 : value >= 100 ? 0 : digits
  return `${sign}${value.toFixed(d)} ${BYTE_UNITS[unit]}`
}

const numberFmt = new Intl.NumberFormat()
const compactFmt = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 })

export function formatNumber(n: number | null | undefined): string {
  if (n === null || n === undefined || !Number.isFinite(n)) return "–"
  return numberFmt.format(n)
}

export function formatCompact(n: number | null | undefined): string {
  if (n === null || n === undefined || !Number.isFinite(n)) return "–"
  return compactFmt.format(n)
}

export function formatPercent(ratio: number, digits = 0): string {
  if (!Number.isFinite(ratio)) return "–"
  return `${(ratio * 100).toFixed(digits)}%`
}

export function parseDate(value: string | null | undefined): Date | null {
  if (!value) return null
  const d = parseISO(value)
  return isValid(d) ? d : null
}

/** "3m ago" / "in 5m". `now` lets callers re-render on a ticker. */
export function formatRelative(value: string | null | undefined, now: Date = new Date()): string {
  const d = parseDate(value)
  if (!d) return "–"
  const diff = differenceInSeconds(d, now)
  if (Math.abs(diff) < 5) return "just now"
  const dist = shortDistance(Math.abs(diff))
  return diff < 0 ? `${dist} ago` : `in ${dist}`
}

/** Compact distance like "45s", "12m", "3h 5m", "2d". */
export function shortDistance(totalSeconds: number): string {
  const s = Math.max(0, Math.round(totalSeconds))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return m % 60 ? `${h}h ${m % 60}m` : `${h}h`
  const days = Math.floor(h / 24)
  if (days < 30) return h % 24 && days < 3 ? `${days}d ${h % 24}h` : `${days}d`
  return formatDistanceStrict(0, s * 1000)
}

/** Duration in milliseconds -> "850 ms", "12.3 s", "4m 10s". */
export function formatDurationMs(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return "–"
  if (ms < 1000) return `${Math.round(ms)} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)} s`
  const totalS = Math.round(ms / 1000)
  const m = Math.floor(totalS / 60)
  const s = totalS % 60
  if (m < 60) return s ? `${m}m ${s}s` : `${m}m`
  const h = Math.floor(m / 60)
  return `${h}h ${m % 60}m`
}

/** Age of something that started at `value`, e.g. a live connection. */
export function formatAge(value: string | null | undefined, now: Date = new Date()): string {
  const d = parseDate(value)
  if (!d) return "–"
  return shortDistance(differenceInSeconds(now, d))
}

/** Absolute timestamp for tooltips and logs. */
export function formatDateTime(value: string | null | undefined, pattern = "yyyy-MM-dd HH:mm:ss"): string {
  const d = parseDate(value)
  return d ? formatDate(d, pattern) : "–"
}

export function formatDateShort(value: string | null | undefined): string {
  const d = parseDate(value)
  return d ? formatDate(d, "MMM d, yyyy") : "–"
}

/** Seconds -> "90 s (1m 30s)" style helper text. */
export function describeSeconds(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 60) return ""
  return shortDistance(seconds)
}

export function formatLatency(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return "–"
  return `${Math.round(ms)} ms`
}

export function truncateMiddle(value: string, keep = 8): string {
  if (value.length <= keep * 2 + 1) return value
  return `${value.slice(0, keep)}…${value.slice(-keep)}`
}

/** Flag emoji for an ISO 3166-1 alpha-2 code ("us" -> 🇺🇸). Empty string if invalid. */
export function flagEmoji(code: string | null | undefined): string {
  if (!code || !/^[a-z]{2}$/i.test(code)) return ""
  const upper = code.toUpperCase()
  // "UK" is commonly used by VPN providers for the United Kingdom.
  const cc = upper === "UK" ? "GB" : upper
  return String.fromCodePoint(...[...cc].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65))
}

const regionNames = (() => {
  try {
    return new Intl.DisplayNames(undefined, { type: "region" })
  } catch {
    return null
  }
})()

export function countryName(code: string | null | undefined): string {
  if (!code) return ""
  const upper = code.toUpperCase() === "UK" ? "GB" : code.toUpperCase()
  try {
    return regionNames?.of(upper) ?? upper
  } catch {
    return upper
  }
}

/** Quota helpers: users enter GB/MB, the API stores bytes. */
export type QuotaUnit = "MB" | "GB" | "TB"
export const QUOTA_UNITS: Record<QuotaUnit, number> = { MB: 1024 ** 2, GB: 1024 ** 3, TB: 1024 ** 4 }

export function bytesToQuota(bytes: number): { value: string; unit: QuotaUnit } {
  if (!bytes) return { value: "", unit: "GB" }
  for (const unit of ["TB", "GB", "MB"] as const) {
    if (bytes >= QUOTA_UNITS[unit] && bytes % QUOTA_UNITS[unit] === 0) {
      return { value: String(bytes / QUOTA_UNITS[unit]), unit }
    }
  }
  const unit: QuotaUnit = bytes >= QUOTA_UNITS.GB ? "GB" : "MB"
  return { value: String(Math.round((bytes / QUOTA_UNITS[unit]) * 100) / 100), unit }
}

export function quotaToBytes(value: string, unit: QuotaUnit): number {
  const n = Number(value)
  if (!value.trim() || !Number.isFinite(n) || n <= 0) return 0
  return Math.round(n * QUOTA_UNITS[unit])
}
