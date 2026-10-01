import { format } from "date-fns"

export type RangePreset = "15m" | "1h" | "24h" | "7d" | "custom"

export const RANGE_PRESETS: { value: RangePreset; label: string; ms?: number }[] = [
  { value: "15m", label: "Last 15 minutes", ms: 15 * 60_000 },
  { value: "1h", label: "Last hour", ms: 60 * 60_000 },
  { value: "24h", label: "Last 24 hours", ms: 24 * 60 * 60_000 },
  { value: "7d", label: "Last 7 days", ms: 7 * 24 * 60 * 60_000 },
  { value: "custom", label: "Custom range" },
]

export interface TimeRangeValue {
  preset: RangePreset
  /** datetime-local strings, used when preset = custom */
  customFrom: string
  customTo: string
  /** When the relative preset was anchored (ms). Bumped by "Refresh". */
  anchor: number
}

export function defaultTimeRange(preset: RangePreset = "24h"): TimeRangeValue {
  const now = Date.now()
  return {
    preset,
    customFrom: format(now - 24 * 3600_000, "yyyy-MM-dd'T'HH:mm"),
    customTo: format(now, "yyyy-MM-dd'T'HH:mm"),
    anchor: now,
  }
}

/** Resolves the range to ISO from/to for the API. */
export function resolveTimeRange(v: TimeRangeValue): { from?: string; to?: string } {
  if (v.preset === "custom") {
    const from = v.customFrom ? new Date(v.customFrom) : null
    const to = v.customTo ? new Date(v.customTo) : null
    return {
      from: from && !Number.isNaN(from.getTime()) ? from.toISOString() : undefined,
      to: to && !Number.isNaN(to.getTime()) ? to.toISOString() : undefined,
    }
  }
  const ms = RANGE_PRESETS.find((p) => p.value === v.preset)?.ms ?? 0
  return { from: new Date(v.anchor - ms).toISOString() }
}
