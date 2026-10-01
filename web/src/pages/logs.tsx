import { useMemo, useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import { getRouteApi, useNavigate } from "@tanstack/react-router"
import { DownloadIcon, FilterXIcon, RotateCwIcon, ScrollTextIcon, SearchIcon, ShieldCheckIcon } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { LevelBadge, ResultBadge } from "@/components/badges"
import { Flag } from "@/components/country"
import { DataTable } from "@/components/data-table"
import { PageHeader } from "@/components/page-header"
import { EmptyState, ErrorState } from "@/components/states"
import { TimeRangeFilter } from "@/components/time-range"
import { defaultTimeRange, resolveTimeRange, type TimeRangeValue } from "@/lib/time-range"
import { useDebounced } from "@/hooks/use-debounced"
import { useLanes } from "@/hooks/use-lanes"
import { useConnectionLogs, useEvents } from "@/hooks/use-logs"
import { useUsers } from "@/hooks/use-users"
import { api, type ConnLogFilters, type EventFilters } from "@/lib/api"
import { CONN_RESULTS, CONN_RESULT_META } from "@/lib/constants"
import { formatBytes, formatDateTime, formatDurationMs, formatNumber } from "@/lib/format"
import type { AppEvent, ConnLog, Lane } from "@/lib/types"

export type LogsTab = "connections" | "events"

const route = getRouteApi("/app/logs")

export function LogsPage() {
  const { tab = "connections" } = route.useSearch()
  const navigate = useNavigate()
  return (
    <>
      <PageHeader title="Logs" description="Connection history and system events, including the admin audit trail." />
      <Tabs
        value={tab}
        onValueChange={(t) => void navigate({ to: "/logs", search: { tab: t === "events" ? "events" : undefined } })}
        className="gap-4"
      >
        <TabsList>
          <TabsTrigger value="connections">Connections</TabsTrigger>
          <TabsTrigger value="events">Events</TabsTrigger>
        </TabsList>
        <TabsContent value="connections">
          <ConnectionLogs />
        </TabsContent>
        <TabsContent value="events">
          <EventLogs />
        </TabsContent>
      </Tabs>
    </>
  )
}

function useLaneMap() {
  const lanes = useLanes()
  return useMemo(() => new Map<string, Lane>((lanes.data ?? []).map((l) => [l.id, l])), [lanes.data])
}

function LoadMore({
  hasNext,
  fetching,
  onClick,
  count,
}: {
  hasNext: boolean
  fetching: boolean
  onClick: () => void
  count: number
}) {
  return (
    <div className="flex items-center justify-between gap-2 border-t px-3 py-2.5 text-xs text-muted-foreground">
      <span className="tabular">{formatNumber(count)} loaded</span>
      {hasNext ? (
        <Button variant="outline" size="sm" onClick={onClick} disabled={fetching}>
          {fetching ? <Spinner /> : null}
          Load more
        </Button>
      ) : count > 0 ? (
        <span>End of results</span>
      ) : null}
    </div>
  )
}

// ---- Connections --------------------------------------------------------------------------

function ConnectionLogs() {
  const users = useUsers()
  const lanes = useLanes()
  const laneMap = useLaneMap()
  const [range, setRange] = useState<TimeRangeValue>(() => defaultTimeRange("1h"))
  const [user, setUser] = useState("all")
  const [lane, setLane] = useState("all")
  const [result, setResult] = useState("all")
  const [client, setClient] = useState("")
  const [q, setQ] = useState("")
  const debouncedClient = useDebounced(client.trim())
  const debouncedQ = useDebounced(q.trim())

  const filters = useMemo<ConnLogFilters>(() => {
    const r = resolveTimeRange(range)
    return {
      from: r.from,
      to: r.to,
      user: user === "all" ? undefined : user,
      lane: lane === "all" ? undefined : lane,
      result: result === "all" ? undefined : result,
      client: debouncedClient || undefined,
      q: debouncedQ || undefined,
    }
  }, [range, user, lane, result, debouncedClient, debouncedQ])

  const logs = useConnectionLogs(filters)
  const rows = useMemo(() => logs.data?.pages.flatMap((p) => p.items) ?? [], [logs.data])
  const anyFilter = user !== "all" || lane !== "all" || result !== "all" || client !== "" || q !== ""

  const columns = useMemo<ColumnDef<ConnLog>[]>(
    () => [
      {
        id: "time",
        accessorKey: "startedAt",
        header: "Started",
        cell: ({ row }) => (
          <span className="tabular font-mono text-xs whitespace-nowrap">{formatDateTime(row.original.startedAt)}</span>
        ),
      },
      {
        id: "user",
        accessorFn: (c) => c.username ?? "",
        header: "User",
        cell: ({ row }) =>
          row.original.username ? (
            <span className="font-medium">{row.original.username}</span>
          ) : (
            <span className="text-muted-foreground">–</span>
          ),
      },
      {
        id: "client",
        accessorKey: "clientIp",
        header: "Client IP",
        cell: ({ row }) => <span className="font-mono text-xs">{row.original.clientIp}</span>,
      },
      {
        id: "lane",
        accessorFn: (c) => c.laneId ?? "",
        header: "Lane",
        cell: ({ row }) => {
          const l = row.original.laneId ? laneMap.get(row.original.laneId) : undefined
          if (!row.original.laneId) return <span className="text-muted-foreground">–</span>
          return (
            <div className="flex flex-col">
              <span className="inline-flex items-center gap-1.5 text-sm">
                <Flag code={l?.countryCode} />
                {l?.name ?? row.original.laneId}
              </span>
              {row.original.exitIp ? (
                <span className="font-mono text-xs text-muted-foreground">{row.original.exitIp}</span>
              ) : null}
            </div>
          )
        },
      },
      {
        id: "target",
        accessorFn: (c) => c.target ?? "",
        header: "Target",
        cell: ({ row }) =>
          row.original.target ? (
            <span className="block max-w-64 truncate font-mono text-xs" title={row.original.target}>
              {row.original.target}
            </span>
          ) : (
            <span className="text-xs text-muted-foreground">not logged</span>
          ),
      },
      {
        id: "protocol",
        accessorKey: "protocol",
        header: "Proto",
        cell: ({ row }) => (
          <Badge variant="outline" className="font-mono text-[11px] font-normal uppercase">
            {row.original.protocol}
          </Badge>
        ),
      },
      {
        id: "result",
        accessorKey: "result",
        header: "Result",
        cell: ({ row }) =>
          row.original.error ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <span tabIndex={0} className="inline-flex">
                  <ResultBadge result={row.original.result} />
                </span>
              </TooltipTrigger>
              <TooltipContent className="max-w-sm break-words">{row.original.error}</TooltipContent>
            </Tooltip>
          ) : (
            <ResultBadge result={row.original.result} />
          ),
      },
      {
        id: "up",
        accessorKey: "bytesUp",
        header: "Up",
        meta: { align: "right" },
        cell: ({ row }) => formatBytes(row.original.bytesUp),
      },
      {
        id: "down",
        accessorKey: "bytesDown",
        header: "Down",
        meta: { align: "right" },
        cell: ({ row }) => formatBytes(row.original.bytesDown),
      },
      {
        id: "duration",
        accessorKey: "durationMs",
        header: "Duration",
        meta: { align: "right" },
        cell: ({ row }) => formatDurationMs(row.original.durationMs),
      },
    ],
    [laneMap],
  )

  return (
    <Card className="gap-0 overflow-hidden py-0">
      <div className="space-y-2 border-b p-3">
        <div className="flex flex-col gap-2 lg:flex-row lg:flex-wrap lg:items-center">
          <TimeRangeFilter value={range} onChange={setRange} />
          <Select value={user} onValueChange={setUser}>
            <SelectTrigger className="w-full lg:w-40" aria-label="Filter by user">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All users</SelectItem>
              {(users.data ?? []).map((u) => (
                <SelectItem key={u.id} value={String(u.id)}>
                  {u.username}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={lane} onValueChange={setLane}>
            <SelectTrigger className="w-full lg:w-44" aria-label="Filter by lane">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All lanes</SelectItem>
              {(lanes.data ?? []).map((l) => (
                <SelectItem key={l.id} value={l.id}>
                  {l.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={result} onValueChange={setResult}>
            <SelectTrigger className="w-full lg:w-40" aria-label="Filter by result">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All results</SelectItem>
              {CONN_RESULTS.map((r) => (
                <SelectItem key={r} value={r}>
                  {CONN_RESULT_META[r].label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <Input
            placeholder="Client IP"
            value={client}
            onChange={(e) => setClient(e.target.value)}
            aria-label="Filter by client IP"
            className="font-mono sm:w-44"
          />
          <InputGroup className="sm:max-w-xs">
            <InputGroupAddon>
              <SearchIcon />
            </InputGroupAddon>
            <InputGroupInput
              placeholder="Target contains…"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              aria-label="Search target"
            />
          </InputGroup>
          <div className="flex gap-2 sm:ml-auto">
            {anyFilter ? (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setUser("all")
                  setLane("all")
                  setResult("all")
                  setClient("")
                  setQ("")
                }}
              >
                <FilterXIcon /> Clear
              </Button>
            ) : null}
            <Button
              variant="outline"
              size="sm"
              onClick={() => setRange((r) => ({ ...r, anchor: Date.now() }))}
              disabled={logs.isFetching}
              aria-label="Refresh"
            >
              {logs.isFetching && !logs.isFetchingNextPage ? <Spinner /> : <RotateCwIcon />}
              <span className="sm:sr-only lg:not-sr-only">Refresh</span>
            </Button>
            <Button variant="outline" size="sm" asChild>
              <a href={api.logs.connectionsCsvUrl(filters)} download>
                <DownloadIcon /> CSV
              </a>
            </Button>
          </div>
        </div>
      </div>
      {logs.isError && !logs.data ? (
        <ErrorState error={logs.error} onRetry={() => void logs.refetch()} />
      ) : (
        <>
          <DataTable
            columns={columns}
            data={logs.data ? rows : undefined}
            isLoading={logs.isPending}
            getRowId={(c) => c.id}
            empty={
              <EmptyState
                icon={<ScrollTextIcon />}
                title="No connections in this range"
                description="Try a longer time range or fewer filters. Logs are kept for the retention set in Settings → App."
              />
            }
          />
          <LoadMore
            hasNext={Boolean(logs.hasNextPage)}
            fetching={logs.isFetchingNextPage}
            onClick={() => void logs.fetchNextPage()}
            count={rows.length}
          />
        </>
      )}
    </Card>
  )
}

// ---- Events --------------------------------------------------------------------------------

function EventLogs() {
  const lanes = useLanes()
  const [range, setRange] = useState<TimeRangeValue>(() => defaultTimeRange("7d"))
  const [level, setLevel] = useState("all")
  const [type, setType] = useState("")
  const [lane, setLane] = useState("all")
  const [q, setQ] = useState("")
  const [auditOnly, setAuditOnly] = useState(false)
  const debouncedType = useDebounced(type.trim())
  const debouncedQ = useDebounced(q.trim())

  const filters = useMemo<EventFilters>(() => {
    const r = resolveTimeRange(range)
    return {
      from: r.from,
      to: r.to,
      level: level === "all" ? undefined : level,
      // Audit entries are events whose type starts with "admin."; `type` is a prefix filter.
      type: auditOnly ? (debouncedType.startsWith("admin.") ? debouncedType : "admin.") : debouncedType || undefined,
      lane: lane === "all" ? undefined : lane,
      q: debouncedQ || undefined,
    }
  }, [range, level, debouncedType, lane, debouncedQ, auditOnly])

  const events = useEvents(filters)
  const rows = useMemo(() => events.data?.pages.flatMap((p) => p.items) ?? [], [events.data])

  const columns = useMemo<ColumnDef<AppEvent>[]>(
    () => [
      {
        id: "time",
        accessorKey: "time",
        header: "Time",
        cell: ({ row }) => (
          <span className="tabular font-mono text-xs whitespace-nowrap">{formatDateTime(row.original.time)}</span>
        ),
      },
      {
        id: "level",
        accessorKey: "level",
        header: "Level",
        cell: ({ row }) => <LevelBadge level={row.original.level} />,
      },
      {
        id: "type",
        accessorKey: "type",
        header: "Type",
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-1.5 font-mono text-xs">
            {row.original.type.startsWith("admin.") ? (
              <ShieldCheckIcon className="size-3.5 text-muted-foreground" aria-label="Audit entry" />
            ) : null}
            {row.original.type}
          </span>
        ),
      },
      {
        id: "lane",
        accessorFn: (e) => e.laneId ?? "",
        header: "Lane",
        cell: ({ row }) =>
          row.original.laneId ? (
            <span className="font-mono text-xs">{row.original.laneId}</span>
          ) : (
            <span className="text-muted-foreground">–</span>
          ),
      },
      {
        id: "actor",
        accessorFn: (e) => e.actor ?? "",
        header: "Actor",
        cell: ({ row }) => row.original.actor ?? <span className="text-muted-foreground">–</span>,
      },
      {
        id: "message",
        accessorKey: "message",
        header: "Message",
        enableSorting: false,
        cell: ({ row }) => <span className="block max-w-xl min-w-64 text-sm whitespace-normal">{row.original.message}</span>,
      },
    ],
    [],
  )

  return (
    <Card className="gap-0 overflow-hidden py-0">
      <div className="space-y-2 border-b p-3">
        <div className="flex flex-col gap-2 lg:flex-row lg:flex-wrap lg:items-center">
          <TimeRangeFilter value={range} onChange={setRange} />
          <Select value={level} onValueChange={setLevel}>
            <SelectTrigger className="w-full lg:w-32" aria-label="Filter by level">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All levels</SelectItem>
              <SelectItem value="info">Info</SelectItem>
              <SelectItem value="warn">Warn</SelectItem>
              <SelectItem value="error">Error</SelectItem>
            </SelectContent>
          </Select>
          <Select value={lane} onValueChange={setLane}>
            <SelectTrigger className="w-full lg:w-44" aria-label="Filter by lane">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All lanes</SelectItem>
              {(lanes.data ?? []).map((l) => (
                <SelectItem key={l.id} value={l.id}>
                  {l.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="flex items-center gap-2 lg:ml-auto">
            <Switch id="audit-only" checked={auditOnly} onCheckedChange={setAuditOnly} />
            <Label htmlFor="audit-only" className="text-sm">
              Audit only
            </Label>
          </div>
        </div>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <Input
            placeholder={auditOnly ? "admin.…" : "Type prefix, e.g. lane."}
            value={type}
            onChange={(e) => setType(e.target.value)}
            aria-label="Filter by event type prefix"
            className="font-mono sm:w-52"
          />
          <InputGroup className="sm:max-w-xs">
            <InputGroupAddon>
              <SearchIcon />
            </InputGroupAddon>
            <InputGroupInput placeholder="Search messages…" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Search events" />
          </InputGroup>
          <Button
            variant="outline"
            size="sm"
            className="sm:ml-auto"
            onClick={() => setRange((r) => ({ ...r, anchor: Date.now() }))}
            disabled={events.isFetching}
          >
            {events.isFetching && !events.isFetchingNextPage ? <Spinner /> : <RotateCwIcon />}
            Refresh
          </Button>
        </div>
      </div>
      {events.isError && !events.data ? (
        <ErrorState error={events.error} onRetry={() => void events.refetch()} />
      ) : (
        <>
          <DataTable
            columns={columns}
            data={events.data ? rows : undefined}
            isLoading={events.isPending}
            getRowId={(e) => String(e.id)}
            empty={<EmptyState icon={<ScrollTextIcon />} title="No events match" description="Try a longer range or fewer filters." />}
          />
          <LoadMore
            hasNext={Boolean(events.hasNextPage)}
            fetching={events.isFetchingNextPage}
            onClick={() => void events.fetchNextPage()}
            count={rows.length}
          />
        </>
      )}
    </Card>
  )
}
