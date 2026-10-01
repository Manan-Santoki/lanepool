import { useMemo } from "react"
import { format } from "date-fns"
import { Area, AreaChart, CartesianGrid, Line, LineChart, XAxis, YAxis, type TooltipContentProps } from "recharts"
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, type ChartConfig } from "@/components/ui/chart"
import { formatBytes, formatNumber, parseDate } from "@/lib/format"
import type { AnalyticsRange, TrafficPoint } from "@/lib/types"
import { cn } from "@/lib/utils"

// Series colours come from the validated categorical palette in index.css (--chart-N), fixed per entity.
const trafficConfig = {
  bytesDown: { label: "Download", color: "var(--chart-1)" },
  bytesUp: { label: "Upload", color: "var(--chart-2)" },
} satisfies ChartConfig

const connConfig = {
  connections: { label: "Connections", color: "var(--chart-1)" },
  failures: { label: "Failures", color: "var(--chart-5)" },
} satisfies ChartConfig

function tickFormat(range: AnalyticsRange) {
  return (value: string) => {
    const d = parseDate(value)
    if (!d) return ""
    return range === "24h" ? format(d, "HH:mm") : format(d, "MMM d")
  }
}

function labelFormat(range: AnalyticsRange, value: unknown) {
  const d = typeof value === "string" ? parseDate(value) : null
  if (!d) return ""
  return range === "24h" || range === "7d" ? format(d, "EEE MMM d, HH:mm") : format(d, "EEE MMM d, yyyy")
}

interface SeriesTooltipProps extends Partial<TooltipContentProps<number, string>> {
  config: ChartConfig
  range: AnalyticsRange
  formatValue: (v: number) => string
  showTotal?: boolean
}

function SeriesTooltip({ active, payload, label, config, range, formatValue, showTotal }: SeriesTooltipProps) {
  if (!active || !payload?.length) return null
  const total = payload.reduce((sum, p) => sum + (typeof p.value === "number" ? p.value : 0), 0)
  return (
    <div className="grid min-w-44 gap-1.5 rounded-lg border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-lg">
      <div className="font-medium">{labelFormat(range, label)}</div>
      {payload.map((p) => {
        const key = String(p.dataKey)
        const cfg = config[key]
        return (
          <div key={key} className="flex items-center gap-2">
            <span aria-hidden className="size-2.5 shrink-0 rounded-[3px]" style={{ background: `var(--color-${key})` }} />
            <span className="text-muted-foreground">{cfg?.label ?? key}</span>
            <span className="tabular ml-auto font-medium">{formatValue(Number(p.value ?? 0))}</span>
          </div>
        )
      })}
      {showTotal && payload.length > 1 ? (
        <div className="flex items-center gap-2 border-t pt-1.5">
          <span className="text-muted-foreground">Total</span>
          <span className="tabular ml-auto font-medium">{formatValue(total)}</span>
        </div>
      ) : null}
    </div>
  )
}

/** Stacked upload/download area chart. */
export function TrafficChart({
  data,
  range,
  className,
}: {
  data: TrafficPoint[]
  range: AnalyticsRange
  className?: string
}) {
  const points = useMemo(() => data.map((p) => ({ t: p.t, bytesDown: p.bytesDown, bytesUp: p.bytesUp })), [data])
  return (
    <ChartContainer config={trafficConfig} className={cn("aspect-auto h-64 w-full", className)}>
      <AreaChart data={points} margin={{ left: 4, right: 8, top: 8 }}>
        <defs>
          {(["bytesDown", "bytesUp"] as const).map((k) => (
            <linearGradient key={k} id={`fill-${k}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor={`var(--color-${k})`} stopOpacity={0.35} />
              <stop offset="95%" stopColor={`var(--color-${k})`} stopOpacity={0.04} />
            </linearGradient>
          ))}
        </defs>
        <CartesianGrid vertical={false} strokeDasharray="3 3" />
        <XAxis
          dataKey="t"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={40}
          tickFormatter={tickFormat(range)}
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          width={64}
          tickFormatter={(v: number) => formatBytes(v, 0)}
        />
        <ChartTooltip
          cursor={{ strokeDasharray: "3 3" }}
          content={<SeriesTooltip config={trafficConfig} range={range} formatValue={(v) => formatBytes(v)} showTotal />}
        />
        <Area
          dataKey="bytesDown"
          type="monotone"
          stackId="traffic"
          stroke="var(--color-bytesDown)"
          strokeWidth={2}
          fill="url(#fill-bytesDown)"
          isAnimationActive={false}
        />
        <Area
          dataKey="bytesUp"
          type="monotone"
          stackId="traffic"
          stroke="var(--color-bytesUp)"
          strokeWidth={2}
          fill="url(#fill-bytesUp)"
          isAnimationActive={false}
        />
        <ChartLegend content={<ChartLegendContent />} />
      </AreaChart>
    </ChartContainer>
  )
}

/** Connections vs failures over time (same unit, one axis). */
export function ConnectionsChart({
  data,
  range,
  className,
}: {
  data: TrafficPoint[]
  range: AnalyticsRange
  className?: string
}) {
  return (
    <ChartContainer config={connConfig} className={cn("aspect-auto h-64 w-full", className)}>
      <LineChart data={data} margin={{ left: 4, right: 8, top: 8 }}>
        <CartesianGrid vertical={false} strokeDasharray="3 3" />
        <XAxis
          dataKey="t"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={40}
          tickFormatter={tickFormat(range)}
        />
        <YAxis tickLine={false} axisLine={false} width={48} allowDecimals={false} tickFormatter={(v: number) => formatNumber(v)} />
        <ChartTooltip
          cursor={{ strokeDasharray: "3 3" }}
          content={<SeriesTooltip config={connConfig} range={range} formatValue={(v) => formatNumber(v)} />}
        />
        <Line dataKey="connections" type="monotone" stroke="var(--color-connections)" strokeWidth={2} dot={false} isAnimationActive={false} />
        <Line dataKey="failures" type="monotone" stroke="var(--color-failures)" strokeWidth={2} dot={false} isAnimationActive={false} />
        <ChartLegend content={<ChartLegendContent />} />
      </LineChart>
    </ChartContainer>
  )
}

/** Horizontal bar list for "top N" rankings. */
export function BarList({
  items,
  formatValue,
  className,
}: {
  items: { key: string; label: React.ReactNode; value: number; secondary?: string; title?: string }[]
  formatValue: (v: number) => string
  className?: string
}) {
  const max = Math.max(1, ...items.map((i) => i.value))
  return (
    <ul className={cn("space-y-1.5", className)}>
      {items.map((item) => (
        <li key={item.key} className="group relative" title={item.title}>
          <div className="relative flex h-8 items-center justify-between gap-3 overflow-hidden rounded-md px-2.5 text-sm">
            <div
              aria-hidden
              className="absolute inset-y-0 left-0 rounded-md bg-chart-1/15 transition-[width] group-hover:bg-chart-1/25"
              style={{ width: `${Math.max(2, (item.value / max) * 100)}%` }}
            />
            <span className="relative min-w-0 truncate">{item.label}</span>
            <span className="tabular relative shrink-0 text-right">
              <span className="font-medium">{formatValue(item.value)}</span>
              {item.secondary ? <span className="ml-2 text-xs text-muted-foreground">{item.secondary}</span> : null}
            </span>
          </div>
        </li>
      ))}
    </ul>
  )
}
