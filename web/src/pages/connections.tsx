import { useMemo, useState } from "react"
import type { ColumnDef, ColumnFiltersState, FilterFn } from "@tanstack/react-table"
import { CableIcon, MoreHorizontalIcon, PauseIcon, PlayIcon, SearchIcon, UserXIcon, XIcon } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { ControlledConfirm } from "@/components/confirm-dialog"
import { Flag } from "@/components/country"
import { DataTable } from "@/components/data-table"
import { PageHeader } from "@/components/page-header"
import { EmptyState, ErrorState } from "@/components/states"
import { RelativeTime } from "@/components/time"
import { useCanWrite } from "@/hooks/use-auth"
import { useKickConnections, useLiveConnections } from "@/hooks/use-connections"
import { useLanes } from "@/hooks/use-lanes"
import { formatBytes, formatNumber } from "@/lib/format"
import type { Lane, LiveConn } from "@/lib/types"
import { cn } from "@/lib/utils"

const connSearch: FilterFn<LiveConn> = (row, _id, value: string) => {
  const q = value.trim().toLowerCase()
  if (!q) return true
  const c = row.original
  return [c.username, c.clientIp, c.target, c.laneId, c.exitIp].some((v) => v?.toLowerCase().includes(q))
}

type PendingKick = { kind: "one"; conn: LiveConn } | { kind: "user"; userId: number; username: string; count: number }

export function ConnectionsPage() {
  const [paused, setPaused] = useState(false)
  const conns = useLiveConnections(paused)
  const lanes = useLanes()
  const canWrite = useCanWrite()
  const kick = useKickConnections()
  const [user, setUser] = useState("all")
  const [search, setSearch] = useState("")
  const [pending, setPending] = useState<PendingKick | null>(null)

  const laneById = useMemo(() => new Map<string, Lane>((lanes.data ?? []).map((l) => [l.id, l])), [lanes.data])

  const users = useMemo(() => {
    const m = new Map<number, { username: string; count: number }>()
    for (const c of conns.data ?? []) {
      const e = m.get(c.userId)
      m.set(c.userId, { username: c.username, count: (e?.count ?? 0) + 1 })
    }
    return [...m.entries()].sort((a, b) => a[1].username.localeCompare(b[1].username))
  }, [conns.data])

  const totals = useMemo(() => {
    let up = 0
    let down = 0
    for (const c of conns.data ?? []) {
      up += c.bytesUp
      down += c.bytesDown
    }
    return { up, down }
  }, [conns.data])

  const columnFilters = useMemo<ColumnFiltersState>(
    () => (user === "all" ? [] : [{ id: "user", value: Number(user) }]),
    [user],
  )

  const columns = useMemo<ColumnDef<LiveConn>[]>(() => {
    const cols: ColumnDef<LiveConn>[] = [
      {
        id: "user",
        accessorKey: "userId",
        header: "User",
        filterFn: "equals",
        sortingFn: (a, b) => a.original.username.localeCompare(b.original.username),
        cell: ({ row }) => <span className="font-medium">{row.original.username}</span>,
      },
      {
        id: "client",
        accessorKey: "clientIp",
        header: "Client IP",
        cell: ({ row }) => <span className="font-mono text-xs">{row.original.clientIp}</span>,
      },
      {
        id: "lane",
        accessorKey: "laneId",
        header: "Exit lane",
        cell: ({ row }) => {
          const lane = laneById.get(row.original.laneId)
          return (
            <div className="flex flex-col">
              <span className="inline-flex items-center gap-1.5 text-sm">
                <Flag code={lane?.countryCode} />
                {lane?.name ?? row.original.laneId}
              </span>
              <span className="font-mono text-xs text-muted-foreground">{row.original.exitIp ?? lane?.exitIp ?? "–"}</span>
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
            <span className="block max-w-72 truncate font-mono text-xs" title={row.original.target}>
              {row.original.target}
            </span>
          ) : (
            <span className="text-xs text-muted-foreground" title="Destination logging is off for this user">
              hidden
            </span>
          ),
      },
      {
        id: "protocol",
        accessorKey: "protocol",
        header: "Protocol",
        cell: ({ row }) => (
          <Badge variant="outline" className="font-mono text-[11px] font-normal uppercase">
            {row.original.protocol}
          </Badge>
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
        id: "age",
        accessorKey: "startedAt",
        header: "Age",
        sortingFn: (a, b) => b.original.startedAt.localeCompare(a.original.startedAt),
        meta: { align: "right" },
        cell: ({ row }) => <RelativeTime value={row.original.startedAt} mode="age" />,
      },
    ]
    if (canWrite) {
      cols.push({
        id: "actions",
        header: () => <span className="sr-only">Actions</span>,
        enableSorting: false,
        meta: { className: "w-10" },
        cell: ({ row }) => {
          const c = row.original
          return (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-sm" aria-label="Connection actions">
                  <MoreHorizontalIcon />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onSelect={() => setPending({ kind: "one", conn: c })}>
                  <XIcon /> Kick connection
                </DropdownMenuItem>
                <DropdownMenuItem
                  variant="destructive"
                  onSelect={() =>
                    setPending({
                      kind: "user",
                      userId: c.userId,
                      username: c.username,
                      count: (conns.data ?? []).filter((x) => x.userId === c.userId).length,
                    })
                  }
                >
                  <UserXIcon /> Kick all of {c.username}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )
        },
      })
    }
    return cols
  }, [canWrite, laneById, conns.data])

  const selectedUser = user === "all" ? null : users.find(([id]) => String(id) === user)

  return (
    <>
      <PageHeader
        title="Live connections"
        description="Open proxy connections right now. Refreshes every 2 seconds."
        actions={
          <>
            <Button variant="outline" size="sm" onClick={() => setPaused((p) => !p)} aria-pressed={paused}>
              {paused ? <PlayIcon /> : <PauseIcon />}
              {paused ? "Resume" : "Pause"}
            </Button>
            {canWrite && selectedUser ? (
              <Button
                variant="destructive"
                size="sm"
                onClick={() =>
                  setPending({
                    kind: "user",
                    userId: selectedUser[0],
                    username: selectedUser[1].username,
                    count: selectedUser[1].count,
                  })
                }
              >
                <UserXIcon /> Kick all of {selectedUser[1].username}
              </Button>
            ) : null}
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
              placeholder="Search client IP, target, lane…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              aria-label="Search connections"
            />
          </InputGroup>
          <Select value={user} onValueChange={setUser}>
            <SelectTrigger className="w-full sm:w-52" aria-label="Filter by user">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All users</SelectItem>
              {users.map(([id, u]) => (
                <SelectItem key={id} value={String(id)}>
                  {u.username} <span className="tabular text-muted-foreground">({u.count})</span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="tabular flex gap-3 text-xs text-muted-foreground sm:ml-auto">
            <span>{formatNumber(conns.data?.length ?? 0)} open</span>
            <span>↑ {formatBytes(totals.up)}</span>
            <span>↓ {formatBytes(totals.down)}</span>
            <span
              aria-hidden
              className={cn(
                "size-2 self-center rounded-full",
                paused ? "bg-muted-foreground/40" : conns.isFetching ? "bg-sky-500" : "bg-emerald-500",
              )}
            />
          </div>
        </div>
        {conns.isError && !conns.data ? (
          <ErrorState error={conns.error} onRetry={() => void conns.refetch()} />
        ) : (
          <DataTable
            columns={columns}
            data={conns.data}
            isLoading={conns.isPending}
            getRowId={(c) => c.id}
            globalFilter={search}
            globalFilterFn={connSearch}
            columnFilters={columnFilters}
            initialSorting={[{ id: "age", desc: false }]}
            empty={
              <EmptyState
                icon={<CableIcon />}
                title={search || user !== "all" ? "No matching connections" : "No open connections"}
                description="Connections appear here while clients are using the proxy."
              />
            }
          />
        )}
      </Card>

      <ControlledConfirm
        open={pending !== null}
        onOpenChange={(o) => !o && setPending(null)}
        title={pending?.kind === "user" ? `Kick all connections of ${pending.username}?` : "Kick this connection?"}
        description={
          pending?.kind === "user" ? (
            <p>
              Closes {pending.count} open connection{pending.count === 1 ? "" : "s"}. The client can reconnect
              immediately unless you also disable the user.
            </p>
          ) : pending?.kind === "one" ? (
            <p>
              Closes the connection from <span className="font-mono">{pending.conn.clientIp}</span>
              {pending.conn.target ? (
                <>
                  {" "}
                  to <span className="font-mono">{pending.conn.target}</span>
                </>
              ) : null}
              . It is logged with result “kicked”.
            </p>
          ) : null
        }
        confirmLabel="Kick"
        destructive
        onConfirm={() =>
          pending?.kind === "user"
            ? kick.mutateAsync({ userId: pending.userId })
            : pending?.kind === "one"
              ? kick.mutateAsync({ ids: [pending.conn.id] })
              : undefined
        }
      />
    </>
  )
}
