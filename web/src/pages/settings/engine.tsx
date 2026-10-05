import { useState, type FormEvent, type ReactNode } from "react"
import { InfoIcon, PowerIcon, RotateCwIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { FormError, FormField } from "@/components/form-field"
import { ErrorState } from "@/components/states"
import { useCanWrite } from "@/hooks/use-auth"
import { useRestartGateway, useSaveSettings, useSetMaintenance, useSettings } from "@/hooks/use-settings"
import { errorMessage, fieldErrors } from "@/lib/api"
import { STRATEGIES } from "@/lib/constants"
import { shortDistance } from "@/lib/format"
import type { EngineSettings, Strategy } from "@/lib/types"
import { cn } from "@/lib/utils"

type NumericKey = Exclude<keyof EngineSettings, "strategy" | "paused" | "ipCheckUrl">

interface NumField {
  key: NumericKey
  label: string
  unit: "seconds" | "count"
  help: ReactNode
  min?: number
}

const PACING: NumField[] = [
  { key: "laneStartDelay", label: "Delay between lane starts", unit: "seconds", help: "Regular lane pacing. Pool fallback waits 60, 90, 120 seconds after failures, or this delay if larger. Startup bursts have no delay between individual lanes." },
  { key: "maxConnecting", label: "Max lanes connecting at once", unit: "count", min: 1, help: "Regular lane handshake limit. Server pools use up to three startup bursts sized to their missing target slots, then one fallback attempt at a time." },
  { key: "connectTimeout", label: "Connect timeout", unit: "seconds", help: "Give up on a lane's handshake after this long and retry later." },
  { key: "retryBackoff", label: "Retry backoff", unit: "seconds", help: "First wait before retrying a regular failed lane; doubles after failures. Server pools instead start fallback at 60 seconds and add 30 after each failure." },
  { key: "retryBackoffMax", label: "Max retry backoff", unit: "seconds", help: "Upper limit for regular lane backoff and increasing pool fallback delays. Pool fallback is always at least 60 seconds and respects a larger lane-start delay." },
  {
    key: "breakerFailures",
    label: "Circuit breaker failures",
    unit: "count",
    help: "Pause regular lane starts after this many consecutive failures. 0 = off. Server pools use their increasing fallback delays instead of this circuit breaker.",
  },
  { key: "breakerPause", label: "Circuit breaker pause", unit: "seconds", help: "How long regular lane starts stay paused once the breaker opens. Does not apply to server pool fallback." },
  {
    key: "handshakeMaxAge",
    label: "Max handshake age",
    unit: "seconds",
    help: "A lane whose last WireGuard handshake is older than this is treated as down. WireGuard re-handshakes about every 2 minutes.",
  },
]

const HEALTH: NumField[] = [
  { key: "ipCheckInterval", label: "IP check interval", unit: "seconds", help: "How often each lane re-checks its exit IP and latency. Server pools look up each tunnel only once, with no repeated probes or lookup retries. 0 = no lookups." },
]

const TIMEOUTS: NumField[] = [
  { key: "dialTimeout", label: "Dial timeout", unit: "seconds", help: "Time allowed to connect to the target through a lane before failing." },
  { key: "idleTimeout", label: "Idle timeout", unit: "seconds", help: "Close proxy connections with no traffic in either direction for this long." },
]

const BURN: NumField[] = [
  {
    key: "autoBurnFailures",
    label: "Auto-burn after failures",
    unit: "count",
    help: "Consecutive failures to the same domain through one lane before that lane is avoided for the domain. 0 = off.",
  },
  { key: "autoBurnTtl", label: "Auto-burn duration", unit: "seconds", help: "How long an automatic burn lasts." },
]

const ALL_NUM = [...PACING, ...HEALTH, ...TIMEOUTS, ...BURN]

type FormState = Record<NumericKey, string> & { strategy: Strategy; ipCheckUrl: string }

function toForm(e: EngineSettings): FormState {
  const f = { strategy: e.strategy, ipCheckUrl: e.ipCheckUrl } as FormState
  for (const field of ALL_NUM) f[field.key] = String(e[field.key])
  return f
}

export function EngineSettingsTab() {
  const settings = useSettings()
  if (settings.isPending) return <Skeleton className="h-96 w-full" />
  if (settings.isError) {
    return (
      <Card>
        <ErrorState error={settings.error} onRetry={() => void settings.refetch()} />
      </Card>
    )
  }
  return (
    <div className="space-y-4">
      <GatewayControls paused={settings.data.engine.paused} />
      <EngineForm key={JSON.stringify(settings.data.engine)} engine={settings.data.engine} />
    </div>
  )
}

function GatewayControls({ paused }: { paused: boolean }) {
  const canWrite = useCanWrite()
  const maintenance = useSetMaintenance()
  const restart = useRestartGateway()
  return (
    <div className="grid gap-4 md:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <PowerIcon className="size-4 text-muted-foreground" /> Maintenance mode
          </CardTitle>
          <CardDescription>
            Rejects new proxy connections. Lanes stay connected, so turning it off again is instant and doesn't
            open new VPN sessions. Useful while you change users or investigate a problem.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <label htmlFor="maintenance" className="flex items-center justify-between gap-4 rounded-md border p-3">
            <span className="text-sm font-medium">{paused ? "Maintenance mode is on" : "Maintenance mode is off"}</span>
            <Switch
              id="maintenance"
              checked={maintenance.isPending ? Boolean(maintenance.variables) : paused}
              disabled={!canWrite || maintenance.isPending}
              onCheckedChange={(v) => maintenance.mutate(v)}
            />
          </label>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <RotateCwIcon className="size-4 text-muted-foreground" /> Proxy server
          </CardTitle>
          <CardDescription>Restart the proxy listener, for example after changing ports or TLS.</CardDescription>
        </CardHeader>
        <CardContent>
          <ConfirmDialog
            trigger={
              <Button variant="outline" disabled={!canWrite || restart.isPending}>
                {restart.isPending ? <Spinner /> : <RotateCwIcon />}
                Restart proxy server
              </Button>
            }
            title="Restart the proxy server?"
            confirmLabel="Restart"
            destructive
            onConfirm={() => restart.mutateAsync()}
            description={
              <>
                <p>All open proxy connections are closed; clients need to reconnect.</p>
                <p>Lanes keep running: WireGuard tunnels are not restarted and exit IPs do not change.</p>
              </>
            }
          />
        </CardContent>
      </Card>
    </div>
  )
}

function NumberInput({
  field,
  value,
  onChange,
  error,
}: {
  field: NumField
  value: string
  onChange: (v: string) => void
  error?: string
}) {
  const n = Number(value)
  const human = field.unit === "seconds" && Number.isFinite(n) && n >= 60 ? `= ${shortDistance(n)}` : null
  return (
    <FormField label={field.label} htmlFor={`eng-${field.key}`} error={error} description={field.help}>
      <InputGroup className="max-w-56">
        <InputGroupInput
          id={`eng-${field.key}`}
          type="number"
          inputMode="numeric"
          min={field.min ?? 0}
          step="any"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="tabular"
          aria-invalid={Boolean(error)}
        />
        {field.unit === "seconds" ? (
          <InputGroupAddon align="inline-end" className="text-xs">
            {human ? <span className="tabular text-muted-foreground/70">{human}</span> : null}
            seconds
          </InputGroupAddon>
        ) : null}
      </InputGroup>
    </FormField>
  )
}

function Group({ title, description, children }: { title: string; description?: ReactNode; children: ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  )
}

function EngineForm({ engine }: { engine: EngineSettings }) {
  const canWrite = useCanWrite()
  const save = useSaveSettings()
  const initial = toForm(engine)
  const [form, setForm] = useState<FormState>(initial)
  const [local, setLocal] = useState<Record<string, string>>({})
  const serverRaw = fieldErrors(save.error)
  // Field errors may be keyed "engine.x" or "x".
  const server = Object.fromEntries(Object.entries(serverRaw).map(([k, v]) => [k.replace(/^engine\./, ""), v]))
  const errors = { ...server, ...local }
  const general = save.error && Object.keys(serverRaw).length === 0 ? errorMessage(save.error) : null

  const set = (key: keyof FormState, value: string) => setForm((f) => ({ ...f, [key]: value }))

  const changed: Partial<EngineSettings> = {}
  for (const field of ALL_NUM) {
    if (form[field.key] !== initial[field.key]) changed[field.key] = Number(form[field.key])
  }
  if (form.strategy !== initial.strategy) changed.strategy = form.strategy
  if (form.ipCheckUrl !== initial.ipCheckUrl) changed.ipCheckUrl = form.ipCheckUrl.trim()
  const dirty = Object.keys(changed).length > 0

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const errs: Record<string, string> = {}
    for (const field of ALL_NUM) {
      const v = form[field.key]
      const n = Number(v)
      if (v.trim() === "" || !Number.isFinite(n) || n < (field.min ?? 0)) errs[field.key] = `Enter a number ≥ ${field.min ?? 0}.`
      else if (field.unit === "count" && !Number.isInteger(n)) errs[field.key] = "Enter a whole number."
    }
    if (Number(form.retryBackoffMax) < Number(form.retryBackoff)) errs.retryBackoffMax = "Must be at least the retry backoff."
    try {
      const u = new URL(form.ipCheckUrl)
      if (u.protocol !== "http:" && u.protocol !== "https:") throw new Error()
    } catch {
      errs.ipCheckUrl = "Enter a valid http(s) URL."
    }
    setLocal(errs)
    if (Object.keys(errs).length) return
    save.mutate({ engine: changed })
  }

  const renderFields = (fields: NumField[]) =>
    fields.map((f) => (
      <NumberInput key={f.key} field={f} value={form[f.key]} onChange={(v) => set(f.key, v)} error={errors[f.key]} />
    ))

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      <FormError message={general} />
      <fieldset disabled={!canWrite} className="space-y-4">
        <Group
          title="Lane pacing"
          description={
            <span className="flex items-start gap-2">
              <InfoIcon className="mt-0.5 size-4 shrink-0" />
              <span>
                Regular lanes follow the pacing and circuit-breaker settings below. Server pools first try up to
                three parallel bursts into missing target slots, keeping successful tunnels connected. Remaining
                slots use single attempts with increasing delays: 60, 90, 120 seconds, and so on.
              </span>
            </span>
          }
        >
          <div className="grid gap-x-6 gap-y-5 md:grid-cols-2">{renderFields(PACING)}</div>
        </Group>

        <Group title="Routing" description="How the proxy picks a lane for each new connection (unless a session keeps it sticky).">
          <RadioGroup
            value={form.strategy}
            onValueChange={(v) => set("strategy", v)}
            className="grid gap-2 md:grid-cols-2"
            aria-label="Routing strategy"
          >
            {STRATEGIES.map((s) => (
              <label
                key={s.value}
                htmlFor={`strategy-${s.value}`}
                className="flex cursor-pointer items-start gap-3 rounded-md border p-3 has-data-[state=checked]:border-primary has-data-[state=checked]:bg-muted/50"
              >
                <RadioGroupItem id={`strategy-${s.value}`} value={s.value} className="mt-0.5" />
                <span className="space-y-0.5">
                  <span className="block text-sm font-medium">{s.label}</span>
                  <span className="block text-xs text-muted-foreground">{s.description}</span>
                </span>
              </label>
            ))}
          </RadioGroup>
          {errors.strategy ? <p className="mt-2 text-sm text-destructive">{errors.strategy}</p> : null}
        </Group>

        <div className="grid gap-4 lg:grid-cols-2">
          <Group title="Exit IP lookups" description="Fetch this URL through a tunnel to learn its exit IP and latency. Server pools use one lookup per tunnel and passive WireGuard handshakes to monitor connectivity.">
            <div className="space-y-5">
              <FormField
                label="IP check URL"
                htmlFor="eng-ipCheckUrl"
                error={errors.ipCheckUrl}
                description="Must return the caller's IP as plain text."
              >
                <Input
                  id="eng-ipCheckUrl"
                  type="url"
                  value={form.ipCheckUrl}
                  onChange={(e) => set("ipCheckUrl", e.target.value)}
                  className="font-mono"
                />
              </FormField>
              {renderFields(HEALTH)}
            </div>
          </Group>
          <Group title="Timeouts" description="Limits for proxy connections.">
            <div className="space-y-5">{renderFields(TIMEOUTS)}</div>
          </Group>
        </div>

        <Group title="Burned IPs" description="Automatically avoid a lane for a domain after it keeps failing.">
          <div className="grid gap-x-6 gap-y-5 md:grid-cols-2">{renderFields(BURN)}</div>
        </Group>
      </fieldset>

      {canWrite ? (
        <Card className={cn("py-3", dirty && "sticky bottom-3 z-10 shadow-lg")}>
          <CardFooter className="flex flex-wrap items-center justify-between gap-2 px-4">
            <span className="text-sm text-muted-foreground">
              {dirty ? `${Object.keys(changed).length} unsaved change${Object.keys(changed).length === 1 ? "" : "s"}` : "No changes"}
            </span>
            <div className="flex gap-2">
              <Button
                type="button"
                variant="ghost"
                disabled={!dirty || save.isPending}
                onClick={() => {
                  setForm(initial)
                  setLocal({})
                }}
              >
                Discard
              </Button>
              <Button type="submit" disabled={!dirty || save.isPending}>
                {save.isPending ? <Spinner /> : null}
                Save engine settings
              </Button>
            </div>
          </CardFooter>
        </Card>
      ) : null}
    </form>
  )
}
