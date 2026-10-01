import { useMemo, useState, type FormEvent } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import { BellIcon, MoreHorizontalIcon, PencilIcon, PlusIcon, SendIcon, Trash2Icon, WebhookIcon } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { ControlledConfirm } from "@/components/confirm-dialog"
import { DataTable } from "@/components/data-table"
import { FormError, FormField } from "@/components/form-field"
import { PageHeader } from "@/components/page-header"
import { EmptyState, ErrorState } from "@/components/states"
import { RelativeTime } from "@/components/time"
import { WriteOnly } from "@/components/write-only"
import {
  useChannels,
  useDeleteChannel,
  useDeleteRule,
  useRules,
  useSaveChannel,
  useSaveRule,
  useTestChannel,
  useToggleChannel,
  useToggleRule,
} from "@/hooks/use-alerts"
import { useCanWrite } from "@/hooks/use-auth"
import { errorMessage, fieldErrors } from "@/lib/api"
import { ALERT_EVENTS, CHANNEL_KINDS } from "@/lib/constants"
import type { AlertChannel, AlertChannelInput, AlertEvent, AlertRule, ChannelKind } from "@/lib/types"

const KIND_LABEL: Record<ChannelKind, string> = Object.fromEntries(CHANNEL_KINDS.map((k) => [k.value, k.label])) as Record<
  ChannelKind,
  string
>

export function AlertsPage() {
  return (
    <>
      <PageHeader title="Alerts" description="Get notified on Telegram, Discord, Slack or any webhook when something needs attention." />
      <ChannelsCard />
      <RulesCard />
    </>
  )
}

// ---- Channels ---------------------------------------------------------------------------------

function ChannelsCard() {
  const channels = useChannels()
  const canWrite = useCanWrite()
  const toggle = useToggleChannel()
  const test = useTestChannel()
  const del = useDeleteChannel()
  const [editing, setEditing] = useState<{ channel?: AlertChannel } | null>(null)
  const [deleting, setDeleting] = useState<AlertChannel | null>(null)

  const columns = useMemo<ColumnDef<AlertChannel>[]>(() => {
    const cols: ColumnDef<AlertChannel>[] = [
      { id: "name", accessorKey: "name", header: "Name", cell: ({ row }) => <span className="font-medium">{row.original.name}</span> },
      {
        id: "kind",
        accessorKey: "kind",
        header: "Type",
        cell: ({ row }) => <Badge variant="outline">{KIND_LABEL[row.original.kind] ?? row.original.kind}</Badge>,
      },
      {
        id: "target",
        header: "Destination",
        enableSorting: false,
        cell: ({ row }) => {
          const c = row.original.config
          return (
            <span className="block max-w-72 truncate font-mono text-xs text-muted-foreground">
              {row.original.kind === "telegram" ? `chat ${c.chatId ?? "–"}` : (c.webhookUrl ?? "–")}
            </span>
          )
        },
      },
      {
        id: "enabled",
        accessorKey: "enabled",
        header: "Enabled",
        cell: ({ row }) => (
          <Switch
            checked={row.original.enabled}
            disabled={!canWrite || toggle.isPending}
            onCheckedChange={(enabled) => toggle.mutate({ channel: row.original, enabled })}
            aria-label={`${row.original.enabled ? "Disable" : "Enable"} ${row.original.name}`}
          />
        ),
      },
    ]
    if (canWrite) {
      cols.push({
        id: "actions",
        header: () => <span className="sr-only">Actions</span>,
        enableSorting: false,
        meta: { className: "w-44", align: "right" },
        cell: ({ row }) => (
          <div className="flex justify-end gap-1">
            <Button
              variant="outline"
              size="sm"
              disabled={test.isPending && test.variables?.id === row.original.id}
              onClick={() => test.mutate(row.original)}
            >
              {test.isPending && test.variables?.id === row.original.id ? <Spinner /> : <SendIcon />}
              Send test
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${row.original.name}`}>
                  <MoreHorizontalIcon />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onSelect={() => setEditing({ channel: row.original })}>
                  <PencilIcon /> Edit
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" onSelect={() => setDeleting(row.original)}>
                  <Trash2Icon /> Delete
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        ),
      })
    }
    return cols
  }, [canWrite, toggle, test])

  return (
    <Card>
      <CardHeader>
        <CardTitle>Channels</CardTitle>
        <CardDescription>Where alerts are delivered.</CardDescription>
        <CardAction>
          <WriteOnly>
            <Button size="sm" variant="outline" onClick={() => setEditing({})}>
              <PlusIcon /> Add channel
            </Button>
          </WriteOnly>
        </CardAction>
      </CardHeader>
      <CardContent>
        {channels.isError && !channels.data ? (
          <ErrorState error={channels.error} onRetry={() => void channels.refetch()} />
        ) : (
          <div className="overflow-hidden rounded-md border">
            <DataTable
              columns={columns}
              data={channels.data}
              isLoading={channels.isPending}
              skeletonRows={2}
              getRowId={(c) => String(c.id)}
              empty={<EmptyState icon={<WebhookIcon />} title="No channels" description="Add a channel, then create rules that use it." className="py-8" />}
            />
          </div>
        )}
      </CardContent>
      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-md">
          {editing ? <ChannelForm channel={editing.channel} onDone={() => setEditing(null)} /> : null}
        </DialogContent>
      </Dialog>
      <ControlledConfirm
        open={deleting !== null}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete channel “${deleting?.name ?? ""}”?`}
        description={<p>Rules that use only this channel will stop notifying anyone.</p>}
        confirmLabel="Delete"
        destructive
        onConfirm={() => (deleting ? del.mutateAsync(deleting) : undefined)}
      />
    </Card>
  )
}

function ChannelForm({ channel, onDone }: { channel?: AlertChannel; onDone: () => void }) {
  const save = useSaveChannel()
  const [name, setName] = useState(channel?.name ?? "")
  const [kind, setKind] = useState<ChannelKind>(channel?.kind ?? "telegram")
  const [botToken, setBotToken] = useState(channel?.config.botToken ?? "")
  const [chatId, setChatId] = useState(channel?.config.chatId ?? "")
  const [webhookUrl, setWebhookUrl] = useState(channel?.config.webhookUrl ?? "")
  const [enabled, setEnabled] = useState(channel?.enabled ?? true)
  const [local, setLocal] = useState<Record<string, string>>({})
  const server = fieldErrors(save.error)
  const errors = { ...server, ...local }
  const general = save.error && Object.keys(server).length === 0 ? errorMessage(save.error) : null

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const errs: Record<string, string> = {}
    if (!name.trim()) errs.name = "Name is required."
    if (kind === "telegram") {
      if (!botToken.trim()) errs.botToken = "Bot token is required."
      if (!chatId.trim()) errs.chatId = "Chat ID is required."
    } else {
      try {
        const u = new URL(webhookUrl.trim())
        if (u.protocol !== "https:" && u.protocol !== "http:") throw new Error()
      } catch {
        errs.webhookUrl = "Enter a valid http(s) URL."
      }
    }
    setLocal(errs)
    if (Object.keys(errs).length) return

    // Secrets come back masked ("••••1234"); only send fields the admin actually changed.
    const orig = channel?.config ?? {}
    const config: AlertChannelInput["config"] = {}
    if (kind === "telegram") {
      if (!channel || channel.kind !== kind || botToken !== orig.botToken) config.botToken = botToken.trim()
      if (!channel || channel.kind !== kind || chatId !== orig.chatId) config.chatId = chatId.trim()
    } else if (!channel || channel.kind !== kind || webhookUrl !== orig.webhookUrl) {
      config.webhookUrl = webhookUrl.trim()
    }
    const input = channel
      ? { name: name.trim(), kind, enabled, ...(Object.keys(config).length ? { config } : {}) }
      : { name: name.trim(), kind, enabled, config }
    save.mutate({ id: channel?.id, input }, { onSuccess: onDone })
  }

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      <DialogHeader>
        <DialogTitle>{channel ? "Edit channel" : "Add channel"}</DialogTitle>
        <DialogDescription>Use “Send test” afterwards to check delivery.</DialogDescription>
      </DialogHeader>
      <FormError message={general} />
      <FormField label="Name" htmlFor="ch-name" error={errors.name}>
        <Input id="ch-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Ops on-call" autoFocus />
      </FormField>
      <FormField label="Type" htmlFor="ch-kind" error={errors.kind}>
        <Select value={kind} onValueChange={(v) => setKind(v as ChannelKind)}>
          <SelectTrigger id="ch-kind" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {CHANNEL_KINDS.map((k) => (
              <SelectItem key={k.value} value={k.value}>
                {k.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </FormField>
      {kind === "telegram" ? (
        <>
          <FormField
            label="Bot token"
            htmlFor="ch-bot"
            error={errors.botToken ?? errors["config.botToken"]}
            description={channel ? "Shown masked. Leave as is to keep the current token." : "From @BotFather, like 123456:ABC-DEF…"}
          >
            <Input id="ch-bot" value={botToken} onChange={(e) => setBotToken(e.target.value)} className="font-mono" autoComplete="off" spellCheck={false} />
          </FormField>
          <FormField
            label="Chat ID"
            htmlFor="ch-chat"
            error={errors.chatId ?? errors["config.chatId"]}
            description="User, group or channel ID. Groups usually start with -100."
          >
            <Input id="ch-chat" value={chatId} onChange={(e) => setChatId(e.target.value)} className="font-mono" />
          </FormField>
        </>
      ) : (
        <FormField
          label={kind === "webhook" ? "Webhook URL" : `${KIND_LABEL[kind]} webhook URL`}
          htmlFor="ch-url"
          error={errors.webhookUrl ?? errors["config.webhookUrl"]}
          description={
            kind === "webhook"
              ? "Receives a JSON POST for every alert."
              : channel
                ? "Shown masked. Leave as is to keep the current URL."
                : `Create an incoming webhook in ${KIND_LABEL[kind]} and paste its URL.`
          }
        >
          <Input
            id="ch-url"
            type="url"
            value={webhookUrl}
            onChange={(e) => setWebhookUrl(e.target.value)}
            className="font-mono"
            placeholder="https://"
            autoComplete="off"
          />
        </FormField>
      )}
      <div className="flex items-center gap-2">
        <Switch id="ch-enabled" checked={enabled} onCheckedChange={setEnabled} />
        <Label htmlFor="ch-enabled">Enabled</Label>
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? <Spinner /> : null}
          Save
        </Button>
      </DialogFooter>
    </form>
  )
}

// ---- Rules ------------------------------------------------------------------------------------

function RulesCard() {
  const rules = useRules()
  const channels = useChannels()
  const canWrite = useCanWrite()
  const toggle = useToggleRule()
  const del = useDeleteRule()
  const [editing, setEditing] = useState<{ rule?: AlertRule } | null>(null)
  const [deleting, setDeleting] = useState<AlertRule | null>(null)
  const channelName = useMemo(() => new Map((channels.data ?? []).map((c) => [c.id, c.name])), [channels.data])

  const columns = useMemo<ColumnDef<AlertRule>[]>(() => {
    const cols: ColumnDef<AlertRule>[] = [
      { id: "name", accessorKey: "name", header: "Name", cell: ({ row }) => <span className="font-medium">{row.original.name}</span> },
      {
        id: "event",
        accessorKey: "event",
        header: "When",
        cell: ({ row }) => {
          const ev = ALERT_EVENTS.find((e) => e.value === row.original.event)
          return (
            <div className="text-sm">
              {ev?.label ?? row.original.event}
              {ev?.threshold ? <span className="tabular text-muted-foreground"> · {ev.threshold.toLowerCase()} {row.original.threshold}</span> : null}
            </div>
          )
        },
      },
      {
        id: "cooldown",
        accessorKey: "cooldownMinutes",
        header: "Cooldown",
        meta: { align: "right" },
        cell: ({ row }) => `${row.original.cooldownMinutes} min`,
      },
      {
        id: "channels",
        header: "Channels",
        enableSorting: false,
        cell: ({ row }) =>
          row.original.channelIds.length ? (
            <div className="flex max-w-64 flex-wrap gap-1">
              {row.original.channelIds.map((id) => (
                <Badge key={id} variant="secondary" className="font-normal">
                  {channelName.get(id) ?? `#${id}`}
                </Badge>
              ))}
            </div>
          ) : (
            <span className="text-xs text-amber-700 dark:text-amber-400">No channels</span>
          ),
      },
      {
        id: "lastFired",
        accessorFn: (r) => r.lastFiredAt ?? "",
        header: "Last fired",
        cell: ({ row }) => <RelativeTime value={row.original.lastFiredAt} fallback="Never" />,
      },
      {
        id: "enabled",
        accessorKey: "enabled",
        header: "Enabled",
        cell: ({ row }) => (
          <Switch
            checked={row.original.enabled}
            disabled={!canWrite || toggle.isPending}
            onCheckedChange={(enabled) => toggle.mutate({ rule: row.original, enabled })}
            aria-label={`${row.original.enabled ? "Disable" : "Enable"} ${row.original.name}`}
          />
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
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${row.original.name}`}>
                <MoreHorizontalIcon />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={() => setEditing({ rule: row.original })}>
                <PencilIcon /> Edit
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem variant="destructive" onSelect={() => setDeleting(row.original)}>
                <Trash2Icon /> Delete
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        ),
      })
    }
    return cols
  }, [canWrite, toggle, channelName])

  return (
    <Card>
      <CardHeader>
        <CardTitle>Rules</CardTitle>
        <CardDescription>What triggers an alert, and where it goes.</CardDescription>
        <CardAction>
          <WriteOnly>
            <Button size="sm" variant="outline" onClick={() => setEditing({})}>
              <PlusIcon /> Add rule
            </Button>
          </WriteOnly>
        </CardAction>
      </CardHeader>
      <CardContent>
        {rules.isError && !rules.data ? (
          <ErrorState error={rules.error} onRetry={() => void rules.refetch()} />
        ) : (
          <div className="overflow-hidden rounded-md border">
            <DataTable
              columns={columns}
              data={rules.data}
              isLoading={rules.isPending}
              skeletonRows={3}
              getRowId={(r) => String(r.id)}
              empty={<EmptyState icon={<BellIcon />} title="No rules" description="Add a rule, for example “Healthy lanes below 5”." className="py-8" />}
            />
          </div>
        )}
      </CardContent>
      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-lg">
          {editing ? <RuleForm rule={editing.rule} channels={channels.data ?? []} onDone={() => setEditing(null)} /> : null}
        </DialogContent>
      </Dialog>
      <ControlledConfirm
        open={deleting !== null}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete rule “${deleting?.name ?? ""}”?`}
        description={<p>You will no longer be alerted for this event.</p>}
        confirmLabel="Delete"
        destructive
        onConfirm={() => (deleting ? del.mutateAsync(deleting) : undefined)}
      />
    </Card>
  )
}

function RuleForm({ rule, channels, onDone }: { rule?: AlertRule; channels: AlertChannel[]; onDone: () => void }) {
  const save = useSaveRule()
  const [name, setName] = useState(rule?.name ?? "")
  const [event, setEvent] = useState<AlertEvent>(rule?.event ?? "lanes_below")
  const [threshold, setThreshold] = useState(String(rule?.threshold ?? 5))
  const [cooldown, setCooldown] = useState(String(rule?.cooldownMinutes ?? 30))
  const [channelIds, setChannelIds] = useState<number[]>(rule?.channelIds ?? channels.map((c) => c.id).slice(0, 1))
  const [enabled, setEnabled] = useState(rule?.enabled ?? true)
  const [local, setLocal] = useState<Record<string, string>>({})
  const server = fieldErrors(save.error)
  const errors = { ...server, ...local }
  const general = save.error && Object.keys(server).length === 0 ? errorMessage(save.error) : null
  const ev = ALERT_EVENTS.find((e) => e.value === event)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const errs: Record<string, string> = {}
    const th = Number.parseInt(threshold, 10)
    const cd = Number.parseInt(cooldown, 10)
    if (!name.trim()) errs.name = "Name is required."
    if (ev?.threshold && (!Number.isFinite(th) || th < 0)) errs.threshold = "Enter 0 or more."
    if (!Number.isFinite(cd) || cd < 0) errs.cooldownMinutes = "Enter 0 or more minutes."
    if (channelIds.length === 0) errs.channelIds = "Choose at least one channel."
    setLocal(errs)
    if (Object.keys(errs).length) return
    save.mutate(
      {
        id: rule?.id,
        input: {
          name: name.trim(),
          event,
          threshold: ev?.threshold ? th : 0,
          cooldownMinutes: cd,
          channelIds,
          enabled,
        },
      },
      { onSuccess: onDone },
    )
  }

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      <DialogHeader>
        <DialogTitle>{rule ? "Edit rule" : "Add rule"}</DialogTitle>
        <DialogDescription>Alerts repeat at most once per cooldown period.</DialogDescription>
      </DialogHeader>
      <FormError message={general} />
      <FormField label="Name" htmlFor="r-name" error={errors.name}>
        <Input id="r-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Too few lanes" autoFocus />
      </FormField>
      <FormField label="Event" htmlFor="r-event" error={errors.event} description={ev?.description}>
        <Select value={event} onValueChange={(v) => setEvent(v as AlertEvent)}>
          <SelectTrigger id="r-event" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {ALERT_EVENTS.map((e) => (
              <SelectItem key={e.value} value={e.value}>
                {e.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </FormField>
      <div className="grid gap-4 sm:grid-cols-2">
        {ev?.threshold ? (
          <FormField label={ev.threshold} htmlFor="r-threshold" error={errors.threshold}>
            <Input
              id="r-threshold"
              type="number"
              min={0}
              inputMode="numeric"
              value={threshold}
              onChange={(e) => setThreshold(e.target.value)}
              className="tabular"
            />
          </FormField>
        ) : null}
        <FormField label="Cooldown" unit="minutes" htmlFor="r-cooldown" error={errors.cooldownMinutes}>
          <Input
            id="r-cooldown"
            type="number"
            min={0}
            inputMode="numeric"
            value={cooldown}
            onChange={(e) => setCooldown(e.target.value)}
            className="tabular"
          />
        </FormField>
      </div>
      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">Channels</legend>
        {channels.length === 0 ? (
          <p className="text-sm text-muted-foreground">Add a channel first.</p>
        ) : (
          <div className="grid gap-2 sm:grid-cols-2">
            {channels.map((c) => (
              <label key={c.id} className="flex items-center gap-2 rounded-md border px-3 py-2 text-sm">
                <Checkbox
                  checked={channelIds.includes(c.id)}
                  onCheckedChange={(checked) =>
                    setChannelIds((ids) => (checked === true ? [...ids, c.id] : ids.filter((x) => x !== c.id)))
                  }
                />
                <span className="truncate">{c.name}</span>
                <span className="ml-auto text-xs text-muted-foreground">{KIND_LABEL[c.kind]}</span>
              </label>
            ))}
          </div>
        )}
        {errors.channelIds ? <p className="text-sm text-destructive">{errors.channelIds}</p> : null}
      </fieldset>
      <div className="flex items-center gap-2">
        <Switch id="r-enabled" checked={enabled} onCheckedChange={setEnabled} />
        <Label htmlFor="r-enabled">Enabled</Label>
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? <Spinner /> : null}
          Save
        </Button>
      </DialogFooter>
    </form>
  )
}
