import { useMemo, useState } from "react"
import type { ColumnDef, FilterFn } from "@tanstack/react-table"
import {
  CableIcon,
  KeyRoundIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlusIcon,
  RotateCcwIcon,
  SearchIcon,
  Trash2Icon,
  UsersIcon,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Progress } from "@/components/ui/progress"
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Chip, ToneBadge } from "@/components/badges"
import { ControlledConfirm } from "@/components/confirm-dialog"
import { flagEmoji } from "@/lib/format"
import { DataTable } from "@/components/data-table"
import { PageHeader } from "@/components/page-header"
import { EmptyState, ErrorState } from "@/components/states"
import { RelativeTime } from "@/components/time"
import { WriteOnly } from "@/components/write-only"
import { useCanWrite } from "@/hooks/use-auth"
import { useNow } from "@/hooks/use-now"
import { useDeleteUser, useResetUsage, useResetUserPassword, useToggleUser, useUsers } from "@/hooks/use-users"
import { formatBytes, formatDateShort, formatNumber, parseDate } from "@/lib/format"
import type { ProxyUser } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ConnectionDetails } from "@/pages/users/connection-details"
import { PasswordRevealDialog } from "@/pages/users/secret-dialog"
import { UserFormSheet } from "@/pages/users/user-form"

const userSearch: FilterFn<ProxyUser> = (row, _id, value: string) => {
  const q = value.trim().toLowerCase()
  if (!q) return true
  const u = row.original
  return u.username.toLowerCase().includes(q) || u.note.toLowerCase().includes(q)
}

function UsageCell({ user }: { user: ProxyUser }) {
  if (!user.quotaBytes) {
    return (
      <div className="w-36 space-y-1">
        <div className="tabular text-xs">{formatBytes(user.usedBytes)}</div>
        <div className="text-[11px] text-muted-foreground">unlimited</div>
      </div>
    )
  }
  const ratio = user.usedBytes / user.quotaBytes
  const pct = Math.min(100, ratio * 100)
  return (
    <div className="w-36 space-y-1">
      <div className="tabular flex justify-between text-xs">
        <span>{formatBytes(user.usedBytes)}</span>
        <span className="text-muted-foreground">/ {formatBytes(user.quotaBytes)}</span>
      </div>
      <Progress
        value={pct}
        aria-label={`${Math.round(pct)}% of quota used`}
        className={cn(
          ratio >= 1 ? "[&>[data-slot=progress-indicator]]:bg-red-500" : ratio >= 0.8 ? "[&>[data-slot=progress-indicator]]:bg-amber-500" : "",
        )}
      />
    </div>
  )
}

function Restrictions({ user }: { user: ProxyUser }) {
  const chips: { key: string; label: string; title: string }[] = []
  if (user.allowedCountries.length) {
    const flags = user.allowedCountries.slice(0, 4).map((c) => flagEmoji(c) || c.toUpperCase()).join(" ")
    chips.push({
      key: "countries",
      label: `${flags}${user.allowedCountries.length > 4 ? ` +${user.allowedCountries.length - 4}` : ""}`,
      title: `Countries: ${user.allowedCountries.join(", ").toUpperCase()}`,
    })
  }
  if (user.allowedLanes.length) {
    chips.push({ key: "lanes", label: `${user.allowedLanes.length} lanes`, title: `Lanes: ${user.allowedLanes.join(", ")}` })
  }
  if (user.stickyMinutes) chips.push({ key: "sticky", label: `sticky ${user.stickyMinutes}m`, title: "Sticky lane duration" })
  if (user.maxConnections) chips.push({ key: "max", label: `≤${user.maxConnections} conns`, title: "Max concurrent connections" })
  if (user.connPerSecond) chips.push({ key: "cps", label: `${user.connPerSecond}/s`, title: "Connections per second" })
  if (user.allowDomains.length) {
    chips.push({ key: "allow", label: `${user.allowDomains.length} allowed`, title: `Allowed domains: ${user.allowDomains.join(", ")}` })
  }
  if (user.denyDomains.length) {
    chips.push({ key: "deny", label: `${user.denyDomains.length} denied`, title: `Denied domains: ${user.denyDomains.join(", ")}` })
  }
  if (user.allowedCidrs.length) {
    chips.push({ key: "cidr", label: `${user.allowedCidrs.length} IPs`, title: `Client IPs: ${user.allowedCidrs.join(", ")}` })
  }
  if (user.logDestinations) chips.push({ key: "log", label: "logs targets", title: "Destination host:port is logged" })
  if (!chips.length) return <span className="text-xs text-muted-foreground">None</span>
  return (
    <div className="flex max-w-80 flex-wrap gap-1">
      {chips.map((c) => (
        <Tooltip key={c.key}>
          <TooltipTrigger asChild>
            <span tabIndex={0}>
              <Chip>{c.label}</Chip>
            </span>
          </TooltipTrigger>
          <TooltipContent className="max-w-sm break-words">{c.title}</TooltipContent>
        </Tooltip>
      ))}
    </div>
  )
}

function ExpiryCell({ value }: { value?: string | null }) {
  const now = useNow()
  const d = parseDate(value ?? undefined)
  if (!d) return <span className="text-muted-foreground">Never</span>
  const expired = d.getTime() < now.getTime()
  return expired ? (
    <ToneBadge toneName="red">Expired {formatDateShort(value)}</ToneBadge>
  ) : (
    <span className="tabular text-sm whitespace-nowrap">{formatDateShort(value)}</span>
  )
}

type Pending =
  | { kind: "delete"; user: ProxyUser }
  | { kind: "password"; user: ProxyUser }
  | { kind: "usage"; user: ProxyUser }

export function UsersPage() {
  const users = useUsers()
  const canWrite = useCanWrite()
  const toggle = useToggleUser()
  const del = useDeleteUser()
  const resetUsage = useResetUsage()
  const resetPassword = useResetUserPassword()
  const [search, setSearch] = useState("")
  const [sheet, setSheet] = useState<{ open: boolean; user?: ProxyUser }>({ open: false })
  const [details, setDetails] = useState<ProxyUser | null>(null)
  const [pending, setPending] = useState<Pending | null>(null)
  const [reveal, setReveal] = useState<{ username: string; password: string; title: string } | null>(null)

  const columns = useMemo<ColumnDef<ProxyUser>[]>(() => {
    const cols: ColumnDef<ProxyUser>[] = [
      {
        id: "username",
        accessorKey: "username",
        header: "Username",
        cell: ({ row }) => (
          <div className="min-w-0">
            <button
              type="button"
              className="font-medium hover:underline focus-visible:underline focus-visible:outline-none"
              onClick={() => setDetails(row.original)}
            >
              {row.original.username}
            </button>
            {row.original.note ? (
              <p className="max-w-56 truncate text-xs text-muted-foreground" title={row.original.note}>
                {row.original.note}
              </p>
            ) : null}
          </div>
        ),
      },
      {
        id: "enabled",
        accessorKey: "enabled",
        header: "Enabled",
        cell: ({ row }) => (
          <Switch
            checked={row.original.enabled}
            disabled={!canWrite || toggle.isPending}
            onCheckedChange={(enabled) => toggle.mutate({ user: row.original, enabled })}
            aria-label={`${row.original.enabled ? "Disable" : "Enable"} ${row.original.username}`}
          />
        ),
      },
      {
        id: "usage",
        accessorFn: (u) => (u.quotaBytes ? u.usedBytes / u.quotaBytes : u.usedBytes / 1e15),
        header: "Usage",
        cell: ({ row }) => <UsageCell user={row.original} />,
      },
      {
        id: "restrictions",
        header: "Restrictions",
        enableSorting: false,
        cell: ({ row }) => <Restrictions user={row.original} />,
      },
      {
        id: "expires",
        accessorFn: (u) => u.expiresAt ?? "9999",
        header: "Expires",
        cell: ({ row }) => <ExpiryCell value={row.original.expiresAt} />,
      },
      {
        id: "conns",
        accessorKey: "activeConnections",
        header: "Conns",
        meta: { align: "right" },
        cell: ({ row }) => formatNumber(row.original.activeConnections),
      },
      {
        id: "lastSeen",
        accessorFn: (u) => u.lastSeenAt ?? "",
        header: "Last seen",
        cell: ({ row }) => <RelativeTime value={row.original.lastSeenAt} fallback="Never" />,
      },
      {
        id: "actions",
        header: () => <span className="sr-only">Actions</span>,
        enableSorting: false,
        meta: { className: "w-10" },
        cell: ({ row }) => {
          const u = row.original
          return (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${u.username}`}>
                  <MoreHorizontalIcon />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onSelect={() => setDetails(u)}>
                  <CableIcon /> Connection details
                </DropdownMenuItem>
                {canWrite ? (
                  <>
                    <DropdownMenuItem onSelect={() => setSheet({ open: true, user: u })}>
                      <PencilIcon /> Edit
                    </DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => setPending({ kind: "password", user: u })}>
                      <KeyRoundIcon /> Reset password
                    </DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => setPending({ kind: "usage", user: u })}>
                      <RotateCcwIcon /> Reset usage
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem variant="destructive" onSelect={() => setPending({ kind: "delete", user: u })}>
                      <Trash2Icon /> Delete
                    </DropdownMenuItem>
                  </>
                ) : null}
              </DropdownMenuContent>
            </DropdownMenu>
          )
        },
      },
    ]
    return cols
  }, [canWrite, toggle])

  const confirm = (() => {
    if (!pending) return null
    const u = pending.user
    switch (pending.kind) {
      case "delete":
        return {
          title: `Delete ${u.username}?`,
          description: (
            <p>
              The user can no longer connect and their open connections are closed. Connection logs keep the username.
              This cannot be undone.
            </p>
          ),
          confirmLabel: "Delete user",
          destructive: true,
          run: () => del.mutateAsync(u),
        }
      case "password":
        return {
          title: `Reset password for ${u.username}?`,
          description: <p>A new random password is generated. Clients using the old one will fail to authenticate.</p>,
          confirmLabel: "Reset password",
          destructive: false,
          run: async () => {
            const res = await resetPassword.mutateAsync({ id: u.id })
            setReveal({ username: u.username, password: res.password, title: `New password for ${u.username}` })
          },
        }
      case "usage":
        return {
          title: `Reset usage for ${u.username}?`,
          description: <p>Sets used traffic back to 0 and starts a new quota period now.</p>,
          confirmLabel: "Reset usage",
          destructive: false,
          run: () => resetUsage.mutateAsync(u),
        }
    }
  })()

  return (
    <>
      <PageHeader
        title="Users"
        description="Proxy logins. Each user has its own credentials, routing rules and limits."
        actions={
          <WriteOnly>
            <Button size="sm" onClick={() => setSheet({ open: true })}>
              <PlusIcon /> New user
            </Button>
          </WriteOnly>
        }
      />

      <Card className="gap-0 overflow-hidden py-0">
        <div className="flex flex-col gap-2 border-b p-3 sm:flex-row sm:items-center">
          <InputGroup className="sm:max-w-xs">
            <InputGroupAddon>
              <SearchIcon />
            </InputGroupAddon>
            <InputGroupInput
              placeholder="Search username or note…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              aria-label="Search users"
            />
          </InputGroup>
          <span className="tabular text-xs text-muted-foreground sm:ml-auto">
            {users.data
              ? `${formatNumber(users.data.filter((u) => u.enabled).length)} enabled of ${formatNumber(users.data.length)}`
              : null}
          </span>
        </div>
        {users.isError && !users.data ? (
          <ErrorState error={users.error} onRetry={() => void users.refetch()} />
        ) : (
          <DataTable
            columns={columns}
            data={users.data}
            isLoading={users.isPending}
            getRowId={(u) => String(u.id)}
            globalFilter={search}
            globalFilterFn={userSearch}
            initialSorting={[{ id: "username", desc: false }]}
            rowClassName={(row) => (row.original.enabled ? undefined : "opacity-60")}
            empty={
              search ? (
                <EmptyState icon={<SearchIcon />} title="No users match" />
              ) : (
                <EmptyState
                  icon={<UsersIcon />}
                  title="No proxy users yet"
                  description="Create a user to get proxy credentials."
                  action={
                    canWrite ? (
                      <Button size="sm" onClick={() => setSheet({ open: true })}>
                        <PlusIcon /> New user
                      </Button>
                    ) : null
                  }
                />
              )
            }
          />
        )}
      </Card>

      <UserFormSheet
        open={sheet.open}
        user={sheet.user}
        onOpenChange={(open) => setSheet((s) => ({ ...s, open }))}
        onCreated={(res) => {
          if (res.password) {
            setReveal({ username: res.user.username, password: res.password, title: `${res.user.username} created` })
          } else {
            setDetails(res.user)
          }
        }}
      />

      <Dialog open={details !== null} onOpenChange={(o) => !o && setDetails(null)}>
        <DialogContent className="max-h-[90svh] grid-cols-[minmax(0,1fr)] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>Connection details: {details?.username}</DialogTitle>
            <DialogDescription>How clients connect as this user.</DialogDescription>
          </DialogHeader>
          {details ? <ConnectionDetails username={details.username} /> : null}
        </DialogContent>
      </Dialog>

      <PasswordRevealDialog data={reveal} onClose={() => setReveal(null)} />

      {confirm ? (
        <ControlledConfirm
          open
          onOpenChange={(o) => !o && setPending(null)}
          title={confirm.title}
          description={confirm.description}
          confirmLabel={confirm.confirmLabel}
          destructive={confirm.destructive}
          onConfirm={confirm.run}
        />
      ) : null}
    </>
  )
}
