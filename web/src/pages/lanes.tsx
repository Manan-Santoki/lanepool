import { useMemo, useState } from "react"
import type { ColumnDef, ColumnFiltersState, FilterFn } from "@tanstack/react-table"
import { Link } from "@tanstack/react-router"
import {
  MoreHorizontalIcon,
  NetworkIcon,
  PowerIcon,
  PowerOffIcon,
  RotateCwIcon,
  SearchIcon,
  Trash2Icon,
} from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { LaneStatusBadge } from "@/components/badges"
import { AddLanesDialog } from "@/components/add-lanes-dialog"
import { ConfirmDialog, ControlledConfirm } from "@/components/confirm-dialog"
import { CopyButton } from "@/components/copy-button"
import { CountryLabel } from "@/components/country"
import { DataTable } from "@/components/data-table"
import { PageHeader } from "@/components/page-header"
import { EmptyState, ErrorState } from "@/components/states"
import { RelativeTime } from "@/components/time"
import { WriteOnly } from "@/components/write-only"
import { useCanWrite } from "@/hooks/use-auth"
import { useLanes, useRemoveLane, useRestartAllLanes, useRestartLane, useSetLaneEnabled } from "@/hooks/use-lanes"
import { useStreamLive } from "@/hooks/use-stream"
import { LANE_STATUSES, LANE_STATUS_META } from "@/lib/constants"
import { countryName, flagEmoji, formatLatency, formatNumber } from "@/lib/format"
import type { Lane, LaneStatus } from "@/lib/types"
import { cn } from "@/lib/utils"

const STATUS_ORDER: Record<LaneStatus, number> = {
  down: 0,
  backoff: 1,
  connecting: 2,
  queued: 3,
  up: 4,
  disabled: 5,
  standby: 6,
}

/** "active" hides standby servers of a pool; any other value is an exact status. */
const statusFilter: FilterFn<Lane> = (row, _columnId, value: string) =>
  value === "active" ? row.original.status !== "standby" : row.original.status === value

const laneSearch: FilterFn<Lane> = (row, _columnId, filterValue: string) => {
  const q = filterValue.trim().toLowerCase()
  if (!q) return true
  const l = row.original
  return [l.name, l.id, l.exitIp, l.city, l.country, l.countryCode, l.keyLabel, l.lastError]
    .filter(Boolean)
    .some((v) => String(v).toLowerCase().includes(q))
}

function LaneActions({ lane }: { lane: Lane }) {
  const restart = useRestartLane()
  const setEnabled = useSetLaneEnabled()
  const remove = useRemoveLane()
  const [confirmRemove, setConfirmRemove] = useState(false)
  return (
    <>
      <ControlledConfirm
        open={confirmRemove}
        onOpenChange={setConfirmRemove}
        title={`Remove ${lane.name}?`}
        confirmLabel="Remove lane"
        destructive
        onConfirm={() => remove.mutateAsync(lane)}
        description={
          lane.provider === "surfshark" ? (
            <p>
              The lane is closed and this location won't be picked automatically again. You can add it back from{" "}
              <strong>Add lanes</strong> at any time.
            </p>
          ) : (
            <p>This deletes the WireGuard config behind this lane. Open connections on it are closed.</p>
          )
        }
      />
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${lane.name}`}>
            <MoreHorizontalIcon />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuLabel className="truncate">{lane.name}</DropdownMenuLabel>
          <DropdownMenuItem disabled={!lane.enabled || restart.isPending} onSelect={() => restart.mutate(lane)}>
            <RotateCwIcon /> Restart lane
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          {lane.enabled ? (
            <DropdownMenuItem variant="destructive" onSelect={() => setEnabled.mutate({ lane, enabled: false })}>
              <PowerOffIcon /> Disable
            </DropdownMenuItem>
          ) : (
            <DropdownMenuItem onSelect={() => setEnabled.mutate({ lane, enabled: true })}>
              <PowerIcon /> Enable
            </DropdownMenuItem>
          )}
          <DropdownMenuItem variant="destructive" onSelect={() => setConfirmRemove(true)}>
            <Trash2Icon /> Remove lane
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </>
  )
}

export function LanesPage() {
  const lanes = useLanes()
  const live = useStreamLive()
  const canWrite = useCanWrite()
  const restartAll = useRestartAllLanes()
  const [status, setStatus] = useState<string>("active")
  const standbyCount = useMemo(() => (lanes.data ?? []).filter((l) => l.status === "standby").length, [lanes.data])
  const [country, setCountry] = useState<string>("all")
  const [search, setSearch] = useState("")

  const countries = useMemo(() => {
    const set = new Set<string>()
    for (const l of lanes.data ?? []) if (l.countryCode) set.add(l.countryCode.toLowerCase())
    return [...set].sort((a, b) => countryName(a).localeCompare(countryName(b)))
  }, [lanes.data])

  const columnFilters = useMemo<ColumnFiltersState>(() => {
    const f: ColumnFiltersState = []
    if (status !== "all") f.push({ id: "status", value: status })
    if (country !== "all") f.push({ id: "location", value: country })
    return f
  }, [status, country])

  const columns = useMemo<ColumnDef<Lane>[]>(() => {
    const cols: ColumnDef<Lane>[] = [
      {
        id: "status",
        accessorKey: "status",
        header: "Status",
        filterFn: statusFilter,
        sortingFn: (a, b) => STATUS_ORDER[a.original.status] - STATUS_ORDER[b.original.status],
        cell: ({ row }) => <LaneStatusBadge status={row.original.status} />,
      },
      {
        id: "name",
        accessorKey: "name",
        header: "Name",
        cell: ({ row }) => {
          const l = row.original
          return (
            <div className="flex items-center gap-2">
              <span className={cn("font-medium", !l.enabled && "text-muted-foreground")}>{l.name}</span>
              {l.provider === "wireguard" ? (
                <Badge variant="outline" className="font-normal">
                  WireGuard
                </Badge>
              ) : null}
              {l.virtual ? (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Badge variant="outline" className="font-normal">
                      virtual
                    </Badge>
                  </TooltipTrigger>
                  <TooltipContent>Virtual location: the server is physically in another country.</TooltipContent>
                </Tooltip>
              ) : null}
            </div>
          )
        },
      },
      {
        id: "location",
        accessorFn: (l) => l.countryCode?.toLowerCase() ?? "",
        header: "Location",
        filterFn: "equalsString",
        sortingFn: (a, b) =>
          `${a.original.country ?? ""}${a.original.city ?? ""}`.localeCompare(
            `${b.original.country ?? ""}${b.original.city ?? ""}`,
          ),
        cell: ({ row }) => <CountryLabel code={row.original.countryCode} city={row.original.city} />,
      },
      {
        id: "exitIp",
        accessorFn: (l) => l.exitIp ?? "",
        header: "Exit IP",
        cell: ({ row }) =>
          row.original.exitIp ? (
            <span className="inline-flex items-center gap-1">
              <span className="font-mono text-xs">{row.original.exitIp}</span>
              <CopyButton value={row.original.exitIp} label="Copy exit IP" />
            </span>
          ) : (
            <span className="text-muted-foreground">–</span>
          ),
      },
      {
        id: "latency",
        accessorFn: (l) => l.latencyMs ?? Number.POSITIVE_INFINITY,
        header: "Latency",
        meta: { align: "right" },
        cell: ({ row }) => formatLatency(row.original.latencyMs),
      },
      {
        id: "conns",
        accessorKey: "activeConnections",
        header: "Conns",
        meta: { align: "right" },
        cell: ({ row }) => formatNumber(row.original.activeConnections),
      },
      {
        id: "handshake",
        accessorFn: (l) => l.lastHandshake ?? "",
        header: "Last handshake",
        cell: ({ row }) => <RelativeTime value={row.original.lastHandshake} />,
      },
      {
        id: "key",
        accessorFn: (l) => l.keyLabel ?? "",
        header: "Key",
        cell: ({ row }) =>
          row.original.keyLabel ? (
            <span className="text-sm">{row.original.keyLabel}</span>
          ) : (
            <span className="text-muted-foreground">–</span>
          ),
      },
      {
        id: "restarts",
        accessorKey: "restarts",
        header: "Restarts",
        meta: { align: "right" },
        cell: ({ row }) => formatNumber(row.original.restarts),
      },
      {
        id: "lastError",
        accessorFn: (l) => l.lastError ?? "",
        header: "Last error",
        enableSorting: false,
        cell: ({ row }) =>
          row.original.lastError ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="block max-w-56 truncate text-xs text-red-700 dark:text-red-400" tabIndex={0}>
                  {row.original.lastError}
                </span>
              </TooltipTrigger>
              <TooltipContent className="max-w-sm break-words">{row.original.lastError}</TooltipContent>
            </Tooltip>
          ) : (
            <span className="text-muted-foreground">–</span>
          ),
      },
      {
        id: "nextRetry",
        accessorFn: (l) => l.nextRetry ?? "",
        header: "Next retry",
        cell: ({ row }) => <RelativeTime value={row.original.nextRetry} />,
      },
    ]
    if (canWrite) {
      cols.push({
        id: "actions",
        header: () => <span className="sr-only">Actions</span>,
        enableSorting: false,
        meta: { className: "w-10" },
        cell: ({ row }) => <LaneActions lane={row.original} />,
      })
    }
    return cols
  }, [canWrite])

  const filtered = (status !== "all" && status !== "active") || country !== "all" || search.trim() !== ""

  return (
    <>
      <PageHeader
        title="Lanes"
        description="Each lane is one WireGuard tunnel with its own exit IP."
        actions={
          <>
            <span
              className="inline-flex items-center gap-1.5 text-xs text-muted-foreground"
              aria-live="polite"
              title={live ? "Updates stream in every ~2 s" : "Live stream unavailable; refreshing every 5 s"}
            >
              <span aria-hidden className={cn("size-2 rounded-full", live ? "bg-emerald-500" : "bg-amber-500")} />
              {live ? "Live" : "Polling"}
            </span>
            <WriteOnly>
              <AddLanesDialog lanes={lanes.data ?? []} />
            </WriteOnly>
            <WriteOnly>
              <ConfirmDialog
                trigger={
                  <Button variant="outline" size="sm" disabled={!lanes.data?.length || restartAll.isPending}>
                    <RotateCwIcon /> Restart all lanes
                  </Button>
                }
                title="Restart all lanes?"
                confirmLabel="Restart all"
                destructive
                onConfirm={() => restartAll.mutateAsync()}
                description={
                  <>
                    <p>
                      Lanes reconnect using the current connection schedule. Pool restart commands keep their
                      fallback delays and do not start a new set of bursts. Connections on each lane are closed.
                    </p>
                    <p>
                      WireGuard has no disconnect message: every restart leaves the old session on the provider side
                      until it times out. Restarting often can get your exit IPs or account rate-limited, so avoid doing
                      this regularly.
                    </p>
                  </>
                }
              />
            </WriteOnly>
          </>
        }
      />

      <Card className="gap-0 overflow-hidden py-0">
        <div className="flex flex-col gap-2 border-b p-3 sm:flex-row sm:items-center">
          <InputGroup className="sm:max-w-xs">
            <InputGroupAddon>
              <SearchIcon />
            </InputGroupAddon>
            <InputGroupInput
              placeholder="Search name, IP, city, key…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              aria-label="Search lanes"
            />
          </InputGroup>
          <div className="flex gap-2">
            <Select value={status} onValueChange={setStatus}>
              <SelectTrigger className="w-full sm:w-40" aria-label="Filter by status">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="active">{standbyCount ? "Active (hide standby)" : "All statuses"}</SelectItem>
                {standbyCount ? <SelectItem value="all">All, incl. standby</SelectItem> : null}
                {LANE_STATUSES.map((s) => (
                  <SelectItem key={s} value={s}>
                    {LANE_STATUS_META[s].label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={country} onValueChange={setCountry}>
              <SelectTrigger className="w-full sm:w-48" aria-label="Filter by country">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All countries</SelectItem>
                {countries.map((c) => (
                  <SelectItem key={c} value={c}>
                    {flagEmoji(c)} {countryName(c)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {filtered ? (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setStatus("active")
                setCountry("all")
                setSearch("")
              }}
            >
              Reset
            </Button>
          ) : null}
          <span className="tabular text-xs text-muted-foreground sm:ml-auto">
            {lanes.data
              ? standbyCount
                ? `${formatNumber(lanes.data.length - standbyCount)} active · ${formatNumber(standbyCount)} standby`
                : `${formatNumber(lanes.data.length)} lanes`
              : null}
          </span>
        </div>
        {lanes.isError && !lanes.data ? (
          <ErrorState error={lanes.error} onRetry={() => void lanes.refetch()} />
        ) : (
          <DataTable
            columns={columns}
            data={lanes.data}
            isLoading={lanes.isPending}
            getRowId={(l) => l.id}
            globalFilter={search}
            globalFilterFn={laneSearch}
            columnFilters={columnFilters}
            initialSorting={[{ id: "status", desc: false }]}
            rowLimit={200}
            rowClassName={(row) => (row.original.enabled ? undefined : "opacity-60")}
            empty={
              filtered ? (
                <EmptyState icon={<SearchIcon />} title="No lanes match" description="Try a different filter." />
              ) : standbyCount ? (
                <EmptyState
                  icon={<NetworkIcon />}
                  title="No servers connected yet"
                  description={`${formatNumber(standbyCount)} servers are on standby. lanepool connects them a few at a time; servers that fail show here with the reason. New connections may be paused (see the banner).`}
                  action={
                    <Button size="sm" variant="outline" onClick={() => setStatus("all")}>
                      Show standby servers
                    </Button>
                  }
                />
              ) : (
                <EmptyState
                  icon={<NetworkIcon />}
                  title="No lanes yet"
                  description="Lanes are created from your providers: add a Surfshark key or a WireGuard config."
                  action={
                    <Button asChild size="sm">
                      <Link to="/providers">Set up providers</Link>
                    </Button>
                  }
                />
              )
            }
          />
        )}
      </Card>
    </>
  )
}
