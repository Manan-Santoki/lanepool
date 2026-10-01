import { useMemo, useState, type FormEvent } from "react"
import type { ColumnDef, FilterFn } from "@tanstack/react-table"
import { FlameIcon, InfoIcon, PlusIcon, SearchIcon, Trash2Icon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { ToneBadge } from "@/components/badges"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { Flag } from "@/components/country"
import { DataTable } from "@/components/data-table"
import { FormError, FormField } from "@/components/form-field"
import { PageHeader } from "@/components/page-header"
import { EmptyState, ErrorState } from "@/components/states"
import { RelativeTime } from "@/components/time"
import { WriteOnly } from "@/components/write-only"
import { useCanWrite } from "@/hooks/use-auth"
import { useBurned, useCreateBurn, useDeleteBurn } from "@/hooks/use-burned"
import { useLanes } from "@/hooks/use-lanes"
import { useNow } from "@/hooks/use-now"
import { errorMessage, fieldErrors } from "@/lib/api"
import type { ToneName } from "@/lib/constants"
import { formatRelative, parseDate } from "@/lib/format"
import type { BurnedIp, Lane } from "@/lib/types"
import { isIp, normalizeDomain, validateDomain } from "@/lib/validators"

const SOURCE_TONE: Record<BurnedIp["source"], ToneName> = { manual: "blue", auto: "amber", api: "violet" }

const TTL_PRESETS = [
  { value: "60", label: "1 hour" },
  { value: "360", label: "6 hours" },
  { value: "1440", label: "24 hours" },
  { value: "10080", label: "7 days" },
  { value: "43200", label: "30 days" },
  { value: "custom", label: "Custom…" },
]

const burnSearch: FilterFn<BurnedIp> = (row, _id, value: string) => {
  const q = value.trim().toLowerCase()
  if (!q) return true
  const b = row.original
  return [b.domain, b.laneId, b.laneName, b.exitIp, b.note].some((v) => v?.toLowerCase().includes(q))
}

function ExpiresCell({ value }: { value: string }) {
  const now = useNow()
  const d = parseDate(value)
  if (d && d.getTime() <= now.getTime()) return <ToneBadge toneName="gray">Expired</ToneBadge>
  return (
    <span className="tabular whitespace-nowrap" title={value}>
      {formatRelative(value, now)}
    </span>
  )
}

export function BurnedPage() {
  const burned = useBurned()
  const lanes = useLanes()
  const canWrite = useCanWrite()
  const del = useDeleteBurn()
  const [search, setSearch] = useState("")
  const [addOpen, setAddOpen] = useState(false)
  const laneMap = useMemo(() => new Map<string, Lane>((lanes.data ?? []).map((l) => [l.id, l])), [lanes.data])

  const columns = useMemo<ColumnDef<BurnedIp>[]>(() => {
    const cols: ColumnDef<BurnedIp>[] = [
      {
        id: "domain",
        accessorKey: "domain",
        header: "Domain",
        cell: ({ row }) => <span className="font-mono text-sm">{row.original.domain}</span>,
      },
      {
        id: "lane",
        accessorFn: (b) => b.laneName ?? b.laneId,
        header: "Lane",
        cell: ({ row }) => {
          const l = laneMap.get(row.original.laneId)
          return (
            <span className="inline-flex items-center gap-1.5">
              <Flag code={l?.countryCode} />
              {row.original.laneName ?? l?.name ?? row.original.laneId}
            </span>
          )
        },
      },
      {
        id: "exitIp",
        accessorFn: (b) => b.exitIp ?? "",
        header: "Exit IP",
        cell: ({ row }) => <span className="font-mono text-xs">{row.original.exitIp ?? "–"}</span>,
      },
      {
        id: "source",
        accessorKey: "source",
        header: "Source",
        cell: ({ row }) => <ToneBadge toneName={SOURCE_TONE[row.original.source] ?? "gray"}>{row.original.source}</ToneBadge>,
      },
      {
        id: "expires",
        accessorKey: "expiresAt",
        header: "Expires",
        cell: ({ row }) => <ExpiresCell value={row.original.expiresAt} />,
      },
      {
        id: "created",
        accessorKey: "createdAt",
        header: "Added",
        cell: ({ row }) => <RelativeTime value={row.original.createdAt} />,
      },
      {
        id: "note",
        accessorKey: "note",
        header: "Note",
        enableSorting: false,
        cell: ({ row }) =>
          row.original.note ? (
            <span className="block max-w-64 truncate text-sm" title={row.original.note}>
              {row.original.note}
            </span>
          ) : (
            <span className="text-muted-foreground">–</span>
          ),
      },
    ]
    if (canWrite) {
      cols.push({
        id: "actions",
        header: () => <span className="sr-only">Actions</span>,
        enableSorting: false,
        meta: { className: "w-10" },
        cell: ({ row }) => (
          <ConfirmDialog
            trigger={
              <Button variant="ghost" size="icon-sm" aria-label={`Remove burn for ${row.original.domain}`}>
                <Trash2Icon />
              </Button>
            }
            title="Remove this burned IP?"
            description={
              <p>
                The proxy may route <span className="font-mono">{row.original.domain}</span> through this lane again
                right away.
              </p>
            }
            confirmLabel="Remove"
            destructive
            onConfirm={() => del.mutateAsync(row.original)}
          />
        ),
      })
    }
    return cols
  }, [canWrite, laneMap, del])

  return (
    <>
      <PageHeader
        title="Burned IPs"
        description={
          <>
            A burned IP tells the proxy to avoid one lane for one domain until it expires, for example after a site
            blocked or rate-limited that exit IP. Other domains still use the lane. <strong>Auto</strong> entries are
            added after repeated connection failures to a domain; <strong>api</strong> entries come from apps calling{" "}
            <code className="font-mono text-xs">POST /api/v1/burn</code>.
          </>
        }
        actions={
          <WriteOnly>
            <Button size="sm" onClick={() => setAddOpen(true)}>
              <PlusIcon /> Burn IP
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
              placeholder="Search domain, lane, IP…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              aria-label="Search burned IPs"
            />
          </InputGroup>
        </div>
        {burned.isError && !burned.data ? (
          <ErrorState error={burned.error} onRetry={() => void burned.refetch()} />
        ) : (
          <DataTable
            columns={columns}
            data={burned.data}
            isLoading={burned.isPending}
            getRowId={(b) => String(b.id)}
            globalFilter={search}
            globalFilterFn={burnSearch}
            initialSorting={[{ id: "expires", desc: true }]}
            empty={
              <EmptyState
                icon={<FlameIcon />}
                title={search ? "No matches" : "No burned IPs"}
                description={search ? undefined : "Every lane is eligible for every domain."}
              />
            }
          />
        )}
      </Card>

      <BurnDialog open={addOpen} onOpenChange={setAddOpen} lanes={lanes.data ?? []} />
    </>
  )
}

function BurnDialog({ open, onOpenChange, lanes }: { open: boolean; onOpenChange: (o: boolean) => void; lanes: Lane[] }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-lg">{open ? <BurnForm lanes={lanes} onDone={() => onOpenChange(false)} /> : null}</DialogContent>
    </Dialog>
  )
}

function BurnForm({ lanes, onDone }: { lanes: Lane[]; onDone: () => void }) {
  const create = useCreateBurn()
  const [domain, setDomain] = useState("")
  const [mode, setMode] = useState<"lane" | "ip">("lane")
  const [laneId, setLaneId] = useState("")
  const [exitIp, setExitIp] = useState("")
  const [ttl, setTtl] = useState("1440")
  const [customTtl, setCustomTtl] = useState("120")
  const [note, setNote] = useState("")
  const [local, setLocal] = useState<Record<string, string>>({})
  const server = fieldErrors(create.error)
  const errors = { ...server, ...local }
  const general = create.error && Object.keys(server).length === 0 ? errorMessage(create.error) : null

  const sortedLanes = useMemo(() => lanes.slice().sort((a, b) => a.name.localeCompare(b.name)), [lanes])

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const errs: Record<string, string> = {}
    const d = normalizeDomain(domain)
    const dErr = validateDomain(d)
    if (dErr) errs.domain = dErr
    if (mode === "lane" && !laneId) errs.laneId = "Choose a lane."
    if (mode === "ip" && !isIp(exitIp.trim())) errs.exitIp = "Enter a valid IPv4 or IPv6 address."
    const minutes = ttl === "custom" ? Number.parseInt(customTtl, 10) : Number(ttl)
    if (!Number.isFinite(minutes) || minutes <= 0) errs.ttlMinutes = "Enter a positive number of minutes."
    setLocal(errs)
    if (Object.keys(errs).length) return
    create.mutate(
      {
        domain: d,
        laneId: mode === "lane" ? laneId : undefined,
        exitIp: mode === "ip" ? exitIp.trim() : undefined,
        ttlMinutes: minutes,
        note: note.trim() || undefined,
      },
      { onSuccess: onDone },
    )
  }

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      <DialogHeader>
        <DialogTitle>Burn an IP for a domain</DialogTitle>
        <DialogDescription>The proxy stops using this lane for this domain until the entry expires.</DialogDescription>
      </DialogHeader>
      <FormError message={general} />
      <FormField label="Domain" htmlFor="b-domain" error={errors.domain} description="Subdomains are matched too.">
        <Input
          id="b-domain"
          value={domain}
          onChange={(e) => setDomain(e.target.value)}
          placeholder="example.com"
          className="font-mono"
          autoFocus
          aria-invalid={Boolean(errors.domain)}
        />
      </FormField>
      <div className="space-y-2">
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          value={mode}
          onValueChange={(v) => v && setMode(v as "lane" | "ip")}
          aria-label="Identify the exit by"
        >
          <ToggleGroupItem value="lane" className="px-3">
            Lane
          </ToggleGroupItem>
          <ToggleGroupItem value="ip" className="px-3">
            Exit IP
          </ToggleGroupItem>
        </ToggleGroup>
        {mode === "lane" ? (
          <FormField label="Lane" htmlFor="b-lane" error={errors.laneId}>
            <Select value={laneId} onValueChange={setLaneId}>
              <SelectTrigger id="b-lane" className="w-full" aria-invalid={Boolean(errors.laneId)}>
                <SelectValue placeholder="Choose a lane" />
              </SelectTrigger>
              <SelectContent>
                {sortedLanes.map((l) => (
                  <SelectItem key={l.id} value={l.id}>
                    <Flag code={l.countryCode} /> {l.name}
                    {l.exitIp ? <span className="font-mono text-xs text-muted-foreground">{l.exitIp}</span> : null}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </FormField>
        ) : (
          <FormField
            label="Exit IP"
            htmlFor="b-ip"
            error={errors.exitIp}
            description="Whichever lane currently has this exit IP is avoided."
          >
            <Input
              id="b-ip"
              value={exitIp}
              onChange={(e) => setExitIp(e.target.value)}
              placeholder="203.0.113.10"
              className="font-mono"
              aria-invalid={Boolean(errors.exitIp)}
            />
          </FormField>
        )}
      </div>
      <FormField label="Duration" htmlFor="b-ttl" error={errors.ttlMinutes}>
        <div className="flex gap-2">
          <Select value={ttl} onValueChange={setTtl}>
            <SelectTrigger id="b-ttl" className="w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {TTL_PRESETS.map((p) => (
                <SelectItem key={p.value} value={p.value}>
                  {p.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {ttl === "custom" ? (
            <InputGroup className="w-40">
              <InputGroupInput
                type="number"
                min={1}
                value={customTtl}
                onChange={(e) => setCustomTtl(e.target.value)}
                aria-label="Duration in minutes"
                className="tabular"
              />
              <InputGroupAddon align="inline-end">min</InputGroupAddon>
            </InputGroup>
          ) : null}
        </div>
      </FormField>
      <FormField label="Note" htmlFor="b-note" error={errors.note}>
        <Textarea id="b-note" rows={2} value={note} onChange={(e) => setNote(e.target.value)} placeholder="Optional" />
      </FormField>
      <p className="flex items-start gap-2 text-xs text-muted-foreground">
        <InfoIcon className="mt-0.5 size-3.5 shrink-0" />
        If every lane allowed for a user is burned for a domain, the connection fails with “no lane”.
      </p>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={create.isPending}>
          {create.isPending ? <Spinner /> : null}
          Burn
        </Button>
      </DialogFooter>
    </form>
  )
}
