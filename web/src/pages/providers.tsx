import { useMemo, useState, type FormEvent } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import {
  AlertTriangleIcon,
  KeyRoundIcon,
  PlusIcon,
  RefreshCwIcon,
  ServerIcon,
  ShieldIcon,
  Trash2Icon,
} from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { CopyButton } from "@/components/copy-button"
import { CountryLabel, Flag } from "@/components/country"
import { DataTable } from "@/components/data-table"
import { FormError, FormField } from "@/components/form-field"
import { MultiSelect, type MultiSelectOption } from "@/components/multi-select"
import { PageHeader } from "@/components/page-header"
import { SurfsharkAccountCard } from "@/components/surfshark-account"
import { EmptyState, ErrorState } from "@/components/states"
import { RelativeTime } from "@/components/time"
import { WriteOnly } from "@/components/write-only"
import { useCanWrite } from "@/hooks/use-auth"
import {
  useAddSurfsharkKey,
  useAddWireguard,
  useDeleteSurfsharkKey,
  useDeleteWireguard,
  useRotateSurfsharkKey,
  useSaveSurfsharkSelection,
  useSurfshark,
  useSurfsharkAccount,
  useSurfsharkLocations,
  useUpdateSurfsharkKey,
  useUpdateWireguard,
  useWireguard,
} from "@/hooks/use-providers"
import { errorMessage, fieldErrors } from "@/lib/api"
import { allCountries } from "@/lib/countries"
import { countryName, formatNumber, truncateMiddle } from "@/lib/format"
import type { SurfsharkKey, SurfsharkLocation, SurfsharkSelection, WireguardConfig } from "@/lib/types"
import { validateWireguardKey } from "@/lib/validators"

export function ProvidersPage() {
  return (
    <>
      <PageHeader
        title="Providers"
        description="Where lanes come from. Each Surfshark location or WireGuard config becomes one lane."
      />
      <SurfsharkAccountCard />
      <SurfsharkCard />
      <WireguardCard />
    </>
  )
}

// ---- Surfshark ----------------------------------------------------------------------------

function SurfsharkCard() {
  const provider = useSurfshark()
  const canWrite = useCanWrite()
  const updateKey = useUpdateSurfsharkKey()
  const deleteKey = useDeleteSurfsharkKey()
  const rotateKey = useRotateSurfsharkKey()
  const connected = useSurfsharkAccount().data?.connected ?? false
  const [addOpen, setAddOpen] = useState(false)
  const p = provider.data

  const keyColumns = useMemo<ColumnDef<SurfsharkKey>[]>(() => {
    const cols: ColumnDef<SurfsharkKey>[] = [
      {
        id: "label",
        accessorKey: "label",
        header: "Label",
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-1.5">
            <span className="font-medium">{row.original.label || "–"}</span>
            {row.original.managed ? (
              <Badge variant="secondary" title="Created by lanepool through your Surfshark account">
                managed
              </Badge>
            ) : null}
          </span>
        ),
      },
      {
        id: "publicKey",
        accessorKey: "publicKey",
        header: "Public key",
        enableSorting: false,
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-1">
            <code className="font-mono text-xs" title={row.original.publicKey}>
              {truncateMiddle(row.original.publicKey, 6)}
            </code>
            <CopyButton value={row.original.publicKey} label="Copy public key" />
          </span>
        ),
      },
      {
        id: "enabled",
        accessorKey: "enabled",
        header: "Enabled",
        cell: ({ row }) => (
          <Switch
            checked={row.original.enabled}
            disabled={!canWrite || updateKey.isPending}
            onCheckedChange={(enabled) => updateKey.mutate({ key: row.original, enabled })}
            aria-label={`${row.original.enabled ? "Disable" : "Enable"} key ${row.original.label}`}
          />
        ),
      },
      {
        id: "lanes",
        accessorKey: "lanes",
        header: "Lanes",
        meta: { align: "right" },
        cell: ({ row }) => formatNumber(row.original.lanes),
      },
      {
        id: "upLanes",
        accessorKey: "upLanes",
        header: "Up",
        meta: { align: "right" },
        cell: ({ row }) => (
          <span
            className={
              row.original.lanes > 0 && row.original.upLanes === 0 ? "text-red-600 dark:text-red-400" : undefined
            }
          >
            {formatNumber(row.original.upLanes)}
          </span>
        ),
      },
      {
        id: "created",
        accessorKey: "createdAt",
        header: "Added",
        cell: ({ row }) => <RelativeTime value={row.original.createdAt} />,
      },
      {
        id: "expires",
        accessorKey: "expiresAt",
        header: "Expires",
        cell: ({ row }) => <RelativeTime value={row.original.expiresAt} fallback="–" />,
      },
    ]
    if (canWrite) {
      cols.push({
        id: "actions",
        header: () => <span className="sr-only">Actions</span>,
        enableSorting: false,
        meta: { className: connected ? "w-20" : "w-10" },
        cell: ({ row }) => (
          <span className="inline-flex">
            {connected ? (
              <ConfirmDialog
                trigger={
                  <Button variant="ghost" size="icon-sm" aria-label={`Rotate key ${row.original.label}`} title="Rotate">
                    <RefreshCwIcon />
                  </Button>
                }
                title={`Rotate key “${row.original.label}”?`}
                description={
                  <p>
                    A new key is generated and registered at Surfshark, and this one is deleted there, so every session
                    using it ends.{" "}
                    {row.original.lanes > 0
                      ? `Its ${row.original.lanes} lane(s) reconnect on the new key, one at a time.`
                      : null}
                  </p>
                }
                confirmLabel="Rotate key"
                onConfirm={() => rotateKey.mutateAsync(row.original)}
              />
            ) : null}
            <ConfirmDialog
              trigger={
                <Button variant="ghost" size="icon-sm" aria-label={`Delete key ${row.original.label}`}>
                  <Trash2Icon />
                </Button>
              }
              title={`Delete key “${row.original.label}”?`}
              description={
                <p>
                  {row.original.lanes > 0
                    ? `${row.original.lanes} lane${row.original.lanes === 1 ? "" : "s"} using this key will move to other keys or stop. `
                    : null}
                  {connected
                    ? "The key is also deleted in your Surfshark account, which ends every session using it immediately."
                    : "The key is removed from lanepool only; it stays valid in your Surfshark account until you revoke it there. Connect your account above to do both at once."}
                </p>
              }
              confirmLabel="Delete key"
              destructive
              onConfirm={() => deleteKey.mutateAsync(row.original)}
            />
          </span>
        ),
      })
    }
    return cols
  }, [canWrite, updateKey, deleteKey, rotateKey, connected])

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ShieldIcon className="size-4 text-muted-foreground" /> Surfshark
        </CardTitle>
        <CardDescription>
          {p ? (
            <span className="tabular">
              {formatNumber(p.serverCount)} servers available · list fetched{" "}
              <RelativeTime value={p.lastFetchedAt} fallback="never" />
            </span>
          ) : (
            "WireGuard keys and which locations become lanes."
          )}
        </CardDescription>
        <CardAction>
          <WriteOnly>
            <Button size="sm" variant="outline" onClick={() => setAddOpen(true)}>
              <PlusIcon /> Add key
            </Button>
          </WriteOnly>
        </CardAction>
      </CardHeader>
      <CardContent className="space-y-6">
        {provider.isPending ? (
          <Skeleton className="h-40 w-full" />
        ) : provider.isError && !p ? (
          <ErrorState error={provider.error} onRetry={() => void provider.refetch()} />
        ) : p ? (
          <>
            {p.fetchError ? (
              <Alert variant="destructive">
                <AlertTriangleIcon />
                <AlertTitle>Could not refresh the Surfshark server list</AlertTitle>
                <AlertDescription>{p.fetchError}</AlertDescription>
              </Alert>
            ) : null}
            <section className="space-y-2">
              <div>
                <h3 className="text-sm font-semibold">Keys</h3>
                <p className="text-xs text-muted-foreground">
                  Lanes are spread across enabled keys. More keys means fewer sessions per key, which providers are less
                  likely to throttle.
                </p>
              </div>
              <div className="overflow-hidden rounded-md border">
                <DataTable
                  columns={keyColumns}
                  data={p.keys}
                  getRowId={(k) => String(k.id)}
                  empty={
                    <EmptyState
                      icon={<KeyRoundIcon />}
                      title="No keys yet"
                      description="Add a WireGuard private key from your Surfshark account (Manual setup → WireGuard)."
                      action={
                        canWrite ? (
                          <Button size="sm" onClick={() => setAddOpen(true)}>
                            <PlusIcon /> Add key
                          </Button>
                        ) : null
                      }
                    />
                  }
                />
              </div>
            </section>
            <Separator />
            <SelectionForm key={JSON.stringify(p.selection)} selection={p.selection} canWrite={canWrite} />
          </>
        ) : null}
      </CardContent>
      <AddKeyDialog open={addOpen} onOpenChange={setAddOpen} />
    </Card>
  )
}

function AddKeyDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-md">
        {open ? <AddKeyForm onDone={() => onOpenChange(false)} /> : null}
      </DialogContent>
    </Dialog>
  )
}

function AddKeyForm({ onDone }: { onDone: () => void }) {
  const add = useAddSurfsharkKey()
  const [privateKey, setPrivateKey] = useState("")
  const [label, setLabel] = useState("")
  const [localError, setLocalError] = useState<string | null>(null)
  const server = fieldErrors(add.error)
  const general = add.error && Object.keys(server).length === 0 ? errorMessage(add.error) : null

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const err = validateWireguardKey(privateKey)
    setLocalError(err)
    if (err) return
    add.mutate({ privateKey: privateKey.trim(), label: label.trim() || undefined }, { onSuccess: onDone })
  }

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      <DialogHeader>
        <DialogTitle>Add Surfshark key</DialogTitle>
        <DialogDescription>
          In your Surfshark account, open VPN → Manual setup → Desktop or mobile → WireGuard, generate a key pair and
          paste the private key here.
        </DialogDescription>
      </DialogHeader>
      <FormError message={general} />
      <FormField
        label="Private key"
        htmlFor="k-private"
        error={localError ?? server.privateKey}
        description="Stored encrypted; only the public key is shown afterwards."
      >
        <Input
          id="k-private"
          value={privateKey}
          onChange={(e) => {
            setPrivateKey(e.target.value)
            setLocalError(null)
          }}
          placeholder="44 characters ending in ="
          className="font-mono"
          autoComplete="off"
          spellCheck={false}
          autoFocus
          aria-invalid={Boolean(localError ?? server.privateKey)}
        />
      </FormField>
      <FormField label="Label" htmlFor="k-label" error={server.label}>
        <Input id="k-label" value={label} onChange={(e) => setLabel(e.target.value)} placeholder="e.g. account-1" />
      </FormField>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={add.isPending}>
          {add.isPending ? <Spinner /> : null}
          Add key
        </Button>
      </DialogFooter>
    </form>
  )
}

function locationLabel(l: SurfsharkLocation) {
  return `${l.city || countryName(l.countryCode)}, ${l.country || countryName(l.countryCode)}`
}

function SelectionForm({ selection, canWrite }: { selection: SurfsharkSelection; canWrite: boolean }) {
  const [form, setForm] = useState<SurfsharkSelection>(selection)
  const [lanesText, setLanesText] = useState(String(selection.lanes))
  const locations = useSurfsharkLocations()
  const save = useSaveSurfsharkSelection()
  const server = fieldErrors(save.error)
  const general = save.error && Object.keys(server).length === 0 ? errorMessage(save.error) : null

  const countryOptions = useMemo<MultiSelectOption[]>(() => {
    const counts = new Map<string, number>()
    for (const l of locations.data ?? []) {
      const cc = l.countryCode.toLowerCase()
      counts.set(cc, (counts.get(cc) ?? 0) + 1)
    }
    const base = counts.size ? [...counts.keys()].map((code) => ({ code, name: countryName(code) })) : allCountries()
    return base
      .map((c) => ({
        value: c.code,
        label: c.name,
        icon: <Flag code={c.code} />,
        keywords: [c.code],
        hint: counts.get(c.code) ? `${counts.get(c.code)} loc` : undefined,
      }))
      .sort((a, b) => a.label.localeCompare(b.label))
  }, [locations.data])

  const locationOptions = useMemo<MultiSelectOption[]>(
    () =>
      (locations.data ?? [])
        .slice()
        .sort((a, b) => locationLabel(a).localeCompare(locationLabel(b)))
        .map((l) => ({
          value: l.id,
          label: `${locationLabel(l)}${l.virtual ? " (virtual)" : ""}`,
          icon: <Flag code={l.countryCode} />,
          hint: `${Math.round(l.load)}% load`,
          keywords: [l.id, l.countryCode, l.city, l.country],
        })),
    [locations.data],
  )

  const lanes = Number.parseInt(lanesText, 10)
  const lanesError = !Number.isFinite(lanes) || lanes < 0 ? "Enter 0 or more." : null
  const next: SurfsharkSelection = {
    ...form,
    lanes: Number.isFinite(lanes) ? lanes : 0,
  }
  const dirty = JSON.stringify(next) !== JSON.stringify(selection)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (lanesError) return
    save.mutate(next)
  }

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      <div>
        <h3 className="text-sm font-semibold">Lane selection</h3>
        <p className="text-xs text-muted-foreground">
          Which Surfshark locations become lanes. Pinned locations always run; the remaining lanes are picked from the
          included countries, spread across countries. You can also add and remove lanes on the Lanes page.
        </p>
      </div>
      <FormError message={general} />
      <fieldset disabled={!canWrite} className="grid gap-4 md:grid-cols-2">
        <FormField
          label="Number of lanes"
          htmlFor="s-lanes"
          error={lanesError ?? server.lanes}
          description="Total Surfshark lanes. Pinned locations always run, even beyond this number."
        >
          <Input
            id="s-lanes"
            type="number"
            min={0}
            inputMode="numeric"
            value={lanesText}
            onChange={(e) => setLanesText(e.target.value)}
            className="tabular max-w-32"
          />
        </FormField>
        <label
          htmlFor="s-virtual"
          className="flex items-start justify-between gap-4 rounded-md border p-3 md:self-start"
        >
          <span className="space-y-1">
            <span className="block text-sm font-medium">Include virtual locations</span>
            <span className="block text-xs text-muted-foreground">
              Virtual locations show a country’s IP but the server is elsewhere, so latency can be higher.
            </span>
          </span>
          <Switch
            id="s-virtual"
            checked={form.includeVirtual}
            onCheckedChange={(includeVirtual) => setForm((f) => ({ ...f, includeVirtual }))}
          />
        </label>
        <FormField
          label="Include countries"
          htmlFor="s-countries"
          error={server.countries}
          description="Empty = all countries."
        >
          <MultiSelect
            id="s-countries"
            options={countryOptions}
            value={form.countries}
            onChange={(countries) => setForm((f) => ({ ...f, countries }))}
            placeholder="All countries"
            searchPlaceholder="Search countries…"
            disabled={!canWrite}
          />
        </FormField>
        <FormField label="Exclude countries" htmlFor="s-exclude" error={server.excludeCountries}>
          <MultiSelect
            id="s-exclude"
            options={countryOptions}
            value={form.excludeCountries}
            onChange={(excludeCountries) => setForm((f) => ({ ...f, excludeCountries }))}
            placeholder="None"
            searchPlaceholder="Search countries…"
            disabled={!canWrite}
          />
        </FormField>
        <FormField
          label="Pinned locations"
          htmlFor="s-locations"
          error={server.locations}
          description={
            locations.isError
              ? `Could not load locations: ${errorMessage(locations.error)}`
              : "Always run a lane in these locations."
          }
          className="md:col-span-2"
        >
          <MultiSelect
            id="s-locations"
            options={locationOptions}
            value={form.locations}
            onChange={(locs) => setForm((f) => ({ ...f, locations: locs }))}
            placeholder={locations.isPending ? "Loading locations…" : "No pinned locations"}
            searchPlaceholder="Search city, country or ID…"
            emptyText={locations.isPending ? "Loading…" : "No locations match."}
            disabled={!canWrite}
            maxChips={12}
          />
        </FormField>
        <FormField
          label="Removed locations"
          htmlFor="s-exclude-locations"
          error={server.excludeLocations}
          description="Never picked automatically (lanes you removed). Remove an entry to make it available again."
          className="md:col-span-2"
        >
          <MultiSelect
            id="s-exclude-locations"
            options={locationOptions}
            value={form.excludeLocations ?? []}
            onChange={(locs) => setForm((f) => ({ ...f, excludeLocations: locs }))}
            placeholder="None"
            searchPlaceholder="Search city, country or ID…"
            emptyText={locations.isPending ? "Loading…" : "No locations match."}
            disabled={!canWrite}
            maxChips={12}
          />
        </FormField>
      </fieldset>
      {canWrite ? (
        <div className="flex items-center justify-end gap-2">
          {dirty ? (
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setForm(selection)
                setLanesText(String(selection.lanes))
              }}
            >
              Discard
            </Button>
          ) : null}
          <Button type="submit" disabled={!dirty || save.isPending}>
            {save.isPending ? <Spinner /> : null}
            Save selection
          </Button>
        </div>
      ) : null}
    </form>
  )
}

// ---- Generic WireGuard ----------------------------------------------------------------------

function WireguardCard() {
  const configs = useWireguard()
  const canWrite = useCanWrite()
  const update = useUpdateWireguard()
  const del = useDeleteWireguard()
  const [addOpen, setAddOpen] = useState(false)

  const columns = useMemo<ColumnDef<WireguardConfig>[]>(() => {
    const cols: ColumnDef<WireguardConfig>[] = [
      {
        id: "name",
        accessorKey: "name",
        header: "Name",
        cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
      },
      {
        id: "location",
        accessorFn: (c) => c.countryCode,
        header: "Location",
        cell: ({ row }) => <CountryLabel code={row.original.countryCode} city={row.original.city} />,
      },
      {
        id: "endpoint",
        accessorKey: "endpoint",
        header: "Endpoint",
        cell: ({ row }) => <span className="font-mono text-xs">{row.original.endpoint}</span>,
      },
      {
        id: "enabled",
        accessorKey: "enabled",
        header: "Enabled",
        cell: ({ row }) => (
          <Switch
            checked={row.original.enabled}
            disabled={!canWrite || update.isPending}
            onCheckedChange={(enabled) => update.mutate({ config: row.original, enabled })}
            aria-label={`${row.original.enabled ? "Disable" : "Enable"} ${row.original.name}`}
          />
        ),
      },
      {
        id: "created",
        accessorKey: "createdAt",
        header: "Added",
        cell: ({ row }) => <RelativeTime value={row.original.createdAt} />,
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
              <Button variant="ghost" size="icon-sm" aria-label={`Delete ${row.original.name}`}>
                <Trash2Icon />
              </Button>
            }
            title={`Delete ${row.original.name}?`}
            description={<p>Its lane stops and the stored config (including the private key) is deleted.</p>}
            confirmLabel="Delete"
            destructive
            onConfirm={() => del.mutateAsync(row.original)}
          />
        ),
      })
    }
    return cols
  }, [canWrite, update, del])

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ServerIcon className="size-4 text-muted-foreground" /> Generic WireGuard
        </CardTitle>
        <CardDescription>
          Any WireGuard server (another VPN provider or your own VPS). Each config is one lane.
        </CardDescription>
        <CardAction>
          <WriteOnly>
            <Button size="sm" variant="outline" onClick={() => setAddOpen(true)}>
              <PlusIcon /> Add config
            </Button>
          </WriteOnly>
        </CardAction>
      </CardHeader>
      <CardContent>
        {configs.isError && !configs.data ? (
          <ErrorState error={configs.error} onRetry={() => void configs.refetch()} />
        ) : (
          <div className="overflow-hidden rounded-md border">
            <DataTable
              columns={columns}
              data={configs.data}
              isLoading={configs.isPending}
              skeletonRows={2}
              getRowId={(c) => String(c.id)}
              empty={
                <EmptyState
                  icon={<ServerIcon />}
                  title="No WireGuard configs"
                  description="Paste a wg-quick config to add a lane."
                  className="py-8"
                />
              }
            />
          </div>
        )}
      </CardContent>
      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-lg">
          {addOpen ? <AddWireguardForm onDone={() => setAddOpen(false)} /> : null}
        </DialogContent>
      </Dialog>
    </Card>
  )
}

const WG_PLACEHOLDER = `[Interface]
PrivateKey = …
Address = 10.0.0.2/32
DNS = 1.1.1.1

[Peer]
PublicKey = …
Endpoint = vpn.example.com:51820
AllowedIPs = 0.0.0.0/0`

function AddWireguardForm({ onDone }: { onDone: () => void }) {
  const add = useAddWireguard()
  const [name, setName] = useState("")
  const [config, setConfig] = useState("")
  const [countryCode, setCountryCode] = useState("")
  const [city, setCity] = useState("")
  const [local, setLocal] = useState<Record<string, string>>({})
  const server = fieldErrors(add.error)
  const errors = { ...server, ...local }
  const general = add.error && Object.keys(server).length === 0 ? errorMessage(add.error) : null
  const countries = useMemo(() => allCountries(), [])

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const errs: Record<string, string> = {}
    if (!name.trim()) errs.name = "Name is required."
    if (!/\[Interface\]/i.test(config) || !/\[Peer\]/i.test(config))
      errs.config = "Paste a full wg-quick config with [Interface] and [Peer] sections."
    setLocal(errs)
    if (Object.keys(errs).length) return
    add.mutate(
      {
        name: name.trim(),
        config,
        countryCode: countryCode || undefined,
        city: city.trim() || undefined,
      },
      { onSuccess: onDone },
    )
  }

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      <DialogHeader>
        <DialogTitle>Add WireGuard config</DialogTitle>
        <DialogDescription>The config is stored encrypted and used to bring up one lane.</DialogDescription>
      </DialogHeader>
      <FormError message={general} />
      <FormField label="Name" htmlFor="wg-name" error={errors.name}>
        <Input
          id="wg-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="vps-frankfurt"
          autoFocus
        />
      </FormField>
      <FormField label="wg-quick config" htmlFor="wg-config" error={errors.config}>
        <Textarea
          id="wg-config"
          rows={10}
          value={config}
          onChange={(e) => setConfig(e.target.value)}
          placeholder={WG_PLACEHOLDER}
          className="font-mono text-xs"
          spellCheck={false}
          aria-invalid={Boolean(errors.config)}
        />
      </FormField>
      <div className="grid gap-4 sm:grid-cols-2">
        <FormField
          label="Country"
          htmlFor="wg-country"
          error={errors.countryCode}
          description="Optional; used for country routing."
        >
          <Select value={countryCode || "none"} onValueChange={(v) => setCountryCode(v === "none" ? "" : v)}>
            <SelectTrigger id="wg-country" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent className="max-h-72">
              <SelectItem value="none">Unknown</SelectItem>
              {countries.map((c) => (
                <SelectItem key={c.code} value={c.code}>
                  <Flag code={c.code} /> {c.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </FormField>
        <FormField label="City" htmlFor="wg-city" error={errors.city}>
          <Input id="wg-city" value={city} onChange={(e) => setCity(e.target.value)} placeholder="Optional" />
        </FormField>
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={add.isPending}>
          {add.isPending ? <Spinner /> : null}
          Add config
        </Button>
      </DialogFooter>
    </form>
  )
}
