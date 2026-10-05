import { Link } from "@tanstack/react-router"
import { ArrowDownIcon, ArrowUpIcon, CableIcon, GlobeIcon, NetworkIcon, ServerCogIcon, UsersIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { TrafficChart } from "@/components/charts"
import { EventList } from "@/components/event-list"
import { LaneGrid, LaneGridLegend } from "@/components/lane-grid"
import { PageHeader } from "@/components/page-header"
import { StatCard } from "@/components/stat-card"
import { EmptyState, ErrorState } from "@/components/states"
import { StatusBanners } from "@/components/status-banners"
import { RelativeTime } from "@/components/time"
import { useTraffic } from "@/hooks/use-analytics"
import { useLanes } from "@/hooks/use-lanes"
import { useOverview } from "@/hooks/use-overview"
import { useStreamState } from "@/hooks/use-stream"
import { LANE_STATUSES, LANE_STATUS_META, tone } from "@/lib/constants"
import { formatBytes, formatNumber } from "@/lib/format"
import { cn } from "@/lib/utils"

export function OverviewPage() {
  const overview = useOverview()
  const lanes = useLanes()
  const traffic = useTraffic("24h")
  const stream = useStreamState()
  const o = overview.data

  // Prefer live numbers from the stream when available.
  const laneList = lanes.data
  const byStatus = laneList
    ? laneList.reduce<Record<string, number>>((acc, l) => ({ ...acc, [l.status]: (acc[l.status] ?? 0) + 1 }), {})
    : (o?.lanes.byStatus ?? {})
  const laneTotal = laneList?.length ?? o?.lanes.total ?? 0
  const laneTarget = o?.lanes.target ?? laneTotal
  const lanesUp = byStatus.up ?? 0
  const liveConnections = stream?.activeConnections ?? o?.activeConnections ?? 0
  const uniqueIps = laneList
    ? new Set(laneList.filter((l) => l.status === "up" && l.exitIp).map((l) => l.exitIp)).size
    : (o?.uniqueExitIps ?? 0)
  const engine = stream?.engine ?? o?.engine
  const gateway = stream?.gateway ?? o?.gateway

  if (overview.isError && !o) {
    return (
      <>
        <PageHeader title="Overview" />
        <Card>
          <ErrorState error={overview.error} onRetry={() => void overview.refetch()} />
        </Card>
      </>
    )
  }

  const loading = overview.isPending

  return (
    <>
      <PageHeader title="Overview" description="Health of your lanes, traffic and users at a glance." />

      <StatusBanners gateway={gateway} maintenance={o?.maintenance} engine={engine} />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
        <StatCard
          title="Lanes up"
          icon={<NetworkIcon />}
          loading={loading && !laneList}
          value={
            <span>
              {formatNumber(lanesUp)}
              <span className="text-base font-normal text-muted-foreground"> / {formatNumber(laneTarget)}</span>
            </span>
          }
        >
          {laneTarget !== laneTotal ? (
            <p className="text-xs text-muted-foreground">Connected / target · {formatNumber(laneTotal)} candidates</p>
          ) : null}
          <div className="flex flex-wrap gap-x-3 gap-y-0.5 pt-1 text-xs text-muted-foreground">
            {LANE_STATUSES.filter((s) => s !== "up" && byStatus[s]).map((s) => (
              <span key={s} className="inline-flex items-center gap-1">
                <span aria-hidden className={cn("size-2 rounded-full", tone(LANE_STATUS_META[s].tone).dot)} />
                {LANE_STATUS_META[s].label} <span className="tabular text-foreground">{byStatus[s]}</span>
              </span>
            ))}
            {laneTarget > 0 && lanesUp >= laneTarget ? <span>Target reached</span> : null}
          </div>
        </StatCard>
        <StatCard
          title="Unique exit IPs"
          icon={<GlobeIcon />}
          loading={loading && !laneList}
          value={formatNumber(uniqueIps)}
          sub="Distinct public IPs across up lanes"
        />
        <StatCard
          title="Live connections"
          icon={<CableIcon />}
          loading={loading && !stream}
          value={formatNumber(liveConnections)}
          sub={
            <Link to="/connections" className="underline-offset-4 hover:underline">
              View live connections
            </Link>
          }
        />
        <StatCard
          title="Traffic (24h)"
          icon={<ArrowDownIcon />}
          loading={loading}
          value={
            <span className="flex flex-wrap items-baseline gap-x-3">
              <span className="inline-flex items-baseline gap-1" title="Download">
                <ArrowDownIcon className="size-4 self-center text-chart-1" aria-label="Download" />
                {formatBytes(o?.traffic24h.bytesDown)}
              </span>
              <span className="inline-flex items-baseline gap-1 text-lg" title="Upload">
                <ArrowUpIcon className="size-4 self-center text-chart-2" aria-label="Upload" />
                {formatBytes(o?.traffic24h.bytesUp)}
              </span>
            </span>
          }
          sub={
            o ? (
              <span className="tabular">
                {formatNumber(o.traffic24h.connections)} connections ·{" "}
                <span className={cn(o.traffic24h.failures > 0 && "text-red-600 dark:text-red-400")}>
                  {formatNumber(o.traffic24h.failures)} failed
                </span>
              </span>
            ) : null
          }
        />
        <StatCard
          title="Users"
          icon={<UsersIcon />}
          loading={loading}
          value={
            <span>
              {formatNumber(o?.users.enabled)}
              <span className="text-base font-normal text-muted-foreground"> / {formatNumber(o?.users.total)}</span>
            </span>
          }
          sub="Enabled / total proxy users"
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Traffic, last 24 hours</CardTitle>
            <CardDescription>Bytes through the proxy, per hour.</CardDescription>
            <CardAction>
              <Button asChild variant="outline" size="sm">
                <Link to="/analytics">Analytics</Link>
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent>
            {traffic.isPending ? (
              <Skeleton className="h-64 w-full" />
            ) : traffic.isError ? (
              <ErrorState error={traffic.error} onRetry={() => void traffic.refetch()} />
            ) : traffic.data.length === 0 ? (
              <EmptyState title="No traffic yet" description="Traffic appears here once users start using the proxy." />
            ) : (
              <TrafficChart data={traffic.data} range="24h" />
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <ServerCogIcon className="size-4 text-muted-foreground" /> Engine
            </CardTitle>
            <CardDescription>The process that runs WireGuard lanes.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            {!engine ? (
              <Skeleton className="h-20 w-full" />
            ) : (
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2">
                <dt className="text-muted-foreground">Status</dt>
                <dd className="flex items-center gap-2 font-medium">
                  <span
                    aria-hidden
                    className={cn("size-2 rounded-full", engine.connected ? "bg-emerald-500" : "bg-red-500")}
                  />
                  {engine.connected ? "Connected" : "Offline"}
                </dd>
                <dt className="text-muted-foreground">Last report</dt>
                <dd>
                  <RelativeTime value={engine.lastReportAt} fallback="never" />
                </dd>
                <dt className="text-muted-foreground">Started</dt>
                <dd>
                  <RelativeTime value={engine.startedAt} />
                </dd>
                {engine.nodeId ? (
                  <>
                    <dt className="text-muted-foreground">Node</dt>
                    <dd className="truncate font-mono text-xs">{engine.nodeId}</dd>
                  </>
                ) : null}
                <dt className="text-muted-foreground">Proxy listener</dt>
                <dd className="font-medium">{gateway ? (gateway.listening ? "Listening" : "Not listening") : "–"}</dd>
              </dl>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Lanes</CardTitle>
            <CardDescription>Each square is one lane. Hover for details.</CardDescription>
            <CardAction>
              <Button asChild variant="outline" size="sm">
                <Link to="/lanes">All lanes</Link>
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent className="space-y-4">
            {lanes.isPending ? (
              <Skeleton className="h-16 w-full" />
            ) : lanes.isError ? (
              <ErrorState error={lanes.error} onRetry={() => void lanes.refetch()} />
            ) : lanes.data.length === 0 ? (
              <EmptyState
                icon={<NetworkIcon />}
                title="No lanes yet"
                description="Add a Surfshark key or a WireGuard config to create lanes."
                action={
                  <Button asChild size="sm">
                    <Link to="/providers">Set up providers</Link>
                  </Button>
                }
              />
            ) : (
              <>
                <LaneGrid lanes={lanes.data} />
                <LaneGridLegend lanes={lanes.data} />
              </>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Recent events</CardTitle>
            <CardAction>
              <Button asChild variant="ghost" size="sm">
                <Link to="/logs" search={{ tab: "events" }}>
                  View all
                </Link>
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent>
            {loading ? (
              <div className="space-y-3">
                {Array.from({ length: 5 }, (_, i) => (
                  <Skeleton key={i} className="h-10 w-full" />
                ))}
              </div>
            ) : o && o.recentEvents.length > 0 ? (
              <div className="max-h-96 overflow-y-auto pr-1">
                <EventList events={o.recentEvents.slice(0, 15)} />
              </div>
            ) : (
              <EmptyState title="No events yet" className="py-6" />
            )}
          </CardContent>
        </Card>
      </div>
    </>
  )
}
