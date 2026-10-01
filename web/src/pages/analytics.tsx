import { useMemo, useState, type ReactNode } from "react"
import { BarChart3Icon } from "lucide-react"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { BarList, ConnectionsChart, TrafficChart } from "@/components/charts"
import { Flag } from "@/components/country"
import { PageHeader } from "@/components/page-header"
import { EmptyState, ErrorState } from "@/components/states"
import { useTop, useTraffic } from "@/hooks/use-analytics"
import { countryName, formatBytes, formatNumber } from "@/lib/format"
import type { AnalyticsRange, TopBy, TopItem } from "@/lib/types"

const RANGES: { value: AnalyticsRange; label: string }[] = [
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
  { value: "90d", label: "90d" },
]

export function AnalyticsPage() {
  const [range, setRange] = useState<AnalyticsRange>("7d")
  const traffic = useTraffic(range)

  const totals = useMemo(() => {
    const t = { up: 0, down: 0, conns: 0, fails: 0 }
    for (const p of traffic.data ?? []) {
      t.up += p.bytesUp
      t.down += p.bytesDown
      t.conns += p.connections
      t.fails += p.failures
    }
    return t
  }, [traffic.data])

  return (
    <>
      <PageHeader
        title="Analytics"
        description="Traffic and usage over time."
        actions={
          <ToggleGroup
            type="single"
            variant="outline"
            size="sm"
            value={range}
            onValueChange={(v) => v && setRange(v as AnalyticsRange)}
            aria-label="Time range"
          >
            {RANGES.map((r) => (
              <ToggleGroupItem key={r.value} value={r.value} className="tabular px-3">
                {r.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        }
      />

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Traffic</CardTitle>
            <CardDescription className="tabular">
              {traffic.data ? (
                <>
                  ↓ {formatBytes(totals.down)} download · ↑ {formatBytes(totals.up)} upload ·{" "}
                  {range === "24h" || range === "7d" ? "hourly" : "daily"}
                </>
              ) : (
                "Upload and download, stacked"
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <ChartBody query={traffic}>{(data) => <TrafficChart data={data} range={range} />}</ChartBody>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Connections and failures</CardTitle>
            <CardDescription className="tabular">
              {traffic.data ? (
                <>
                  {formatNumber(totals.conns)} connections · {formatNumber(totals.fails)} failed
                  {totals.conns > 0 ? ` (${((totals.fails / totals.conns) * 100).toFixed(1)}%)` : ""}
                </>
              ) : (
                "Connection attempts per period"
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <ChartBody query={traffic}>{(data) => <ConnectionsChart data={data} range={range} />}</ChartBody>
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <TopCard range={range} by="user" title="Top users" description="By traffic" />
        <TopCard range={range} by="lane" title="Top lanes" description="By traffic" />
        <TopCard range={range} by="country" title="Top countries" description="By exit country traffic" />
        <TopCard
          range={range}
          by="domain"
          title="Top domains"
          description="By traffic"
          footnote="Only includes users with destination logging on, and only within the connection log retention period."
        />
      </div>
    </>
  )
}

function ChartBody<T>({
  query,
  children,
}: {
  query: { data: T[] | undefined; isPending: boolean; isError: boolean; error: unknown; refetch: () => unknown }
  children: (data: T[]) => ReactNode
}) {
  if (query.isPending) return <Skeleton className="h-64 w-full" />
  if (query.isError && !query.data) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  if (!query.data || query.data.length === 0) {
    return <EmptyState icon={<BarChart3Icon />} title="No data for this range" className="h-64" />
  }
  return <>{children(query.data)}</>
}

function topLabel(by: TopBy, item: TopItem): ReactNode {
  if (by === "country") {
    return (
      <span className="inline-flex items-center gap-2">
        <Flag code={item.key} />
        {item.label || countryName(item.key)}
      </span>
    )
  }
  if (by === "domain") return <span className="font-mono text-xs">{item.label || item.key}</span>
  return item.label || item.key
}

function TopCard({
  range,
  by,
  title,
  description,
  footnote,
}: {
  range: AnalyticsRange
  by: TopBy
  title: string
  description: string
  footnote?: string
}) {
  const top = useTop(range, by)
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {top.isPending ? (
          <div className="space-y-1.5">
            {Array.from({ length: 6 }, (_, i) => (
              <Skeleton key={i} className="h-8 w-full" />
            ))}
          </div>
        ) : top.isError && !top.data ? (
          <ErrorState error={top.error} onRetry={() => void top.refetch()} />
        ) : top.data.length === 0 ? (
          <EmptyState title="Nothing yet" className="py-6" />
        ) : (
          <BarList
            items={top.data.map((item) => ({
              key: item.key,
              label: topLabel(by, item),
              value: item.bytes,
              secondary: `${formatNumber(item.connections)} conns${item.failures ? ` · ${formatNumber(item.failures)} failed` : ""}`,
              title: `${item.label || item.key}: ${formatBytes(item.bytes)}, ${formatNumber(item.connections)} connections, ${formatNumber(item.failures)} failures`,
            }))}
            formatValue={(v) => formatBytes(v)}
          />
        )}
        {footnote ? <p className="text-xs text-muted-foreground">{footnote}</p> : null}
      </CardContent>
    </Card>
  )
}
