import { useMemo, useState, type FormEvent } from "react"
import { format } from "date-fns"
import { DicesIcon, EyeIcon, EyeOffIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { Flag } from "@/components/country"
import { FormError, FormField, FormSection } from "@/components/form-field"
import { MultiSelect, type MultiSelectOption } from "@/components/multi-select"
import { TagInput } from "@/components/tag-input"
import { useLanes } from "@/hooks/use-lanes"
import { useCreateUser, useUpdateUser } from "@/hooks/use-users"
import { errorMessage, fieldErrors } from "@/lib/api"
import { allCountries } from "@/lib/countries"
import { bytesToQuota, parseDate, quotaToBytes, type QuotaUnit } from "@/lib/format"
import type { CreateUserResponse, ProxyUser, ProxyUserInput } from "@/lib/types"
import {
  generatePassword,
  normalizeDomain,
  validateCidr,
  validateCountryCode,
  validateDomain,
  validateUsername,
} from "@/lib/validators"

interface FormState {
  username: string
  password: string
  note: string
  enabled: boolean
  expiresAt: string // yyyy-MM-dd or ""
  allowedCountries: string[]
  allowedLanes: string[]
  stickyMinutes: string
  maxConnections: string
  connPerSecond: string
  quotaValue: string
  quotaUnit: QuotaUnit
  logDestinations: boolean
  allowDomains: string[]
  denyDomains: string[]
  allowedCidrs: string[]
}

function toForm(user?: ProxyUser): FormState {
  const quota = bytesToQuota(user?.quotaBytes ?? 0)
  const exp = parseDate(user?.expiresAt ?? undefined)
  return {
    username: user?.username ?? "",
    password: "",
    note: user?.note ?? "",
    enabled: user?.enabled ?? true,
    expiresAt: exp ? format(exp, "yyyy-MM-dd") : "",
    allowedCountries: user?.allowedCountries ?? [],
    allowedLanes: user?.allowedLanes ?? [],
    stickyMinutes: String(user?.stickyMinutes ?? 0),
    maxConnections: String(user?.maxConnections ?? 0),
    connPerSecond: String(user?.connPerSecond ?? 0),
    quotaValue: quota.value,
    quotaUnit: quota.unit,
    logDestinations: user?.logDestinations ?? false,
    allowDomains: user?.allowDomains ?? [],
    denyDomains: user?.denyDomains ?? [],
    allowedCidrs: user?.allowedCidrs ?? [],
  }
}

const toInt = (v: string) => {
  const n = Number.parseInt(v, 10)
  return Number.isFinite(n) && n > 0 ? n : 0
}

function toInput(f: FormState, isCreate: boolean): ProxyUserInput {
  // Expiry is the end of the chosen day in the admin's local time zone.
  const expiresAt = f.expiresAt ? new Date(`${f.expiresAt}T23:59:59`).toISOString() : null
  const input: ProxyUserInput = {
    username: f.username.trim(),
    note: f.note.trim(),
    enabled: f.enabled,
    expiresAt,
    allowedCountries: f.allowedCountries,
    allowedLanes: f.allowedLanes,
    stickyMinutes: toInt(f.stickyMinutes),
    maxConnections: toInt(f.maxConnections),
    connPerSecond: toInt(f.connPerSecond),
    quotaBytes: quotaToBytes(f.quotaValue, f.quotaUnit),
    logDestinations: f.logDestinations,
    allowDomains: f.allowDomains,
    denyDomains: f.denyDomains,
    allowedCidrs: f.allowedCidrs,
  }
  if (isCreate && f.password) input.password = f.password
  return input
}

export function UserFormSheet({
  open,
  onOpenChange,
  user,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Undefined = create. */
  user?: ProxyUser
  onCreated: (res: CreateUserResponse) => void
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="flex w-full flex-col gap-0 p-0 sm:max-w-xl">
        {open ? (
          <UserForm key={user?.id ?? "new"} user={user} onDone={() => onOpenChange(false)} onCreated={onCreated} />
        ) : null}
      </SheetContent>
    </Sheet>
  )
}

function UserForm({
  user,
  onDone,
  onCreated,
}: {
  user?: ProxyUser
  onDone: () => void
  onCreated: (res: CreateUserResponse) => void
}) {
  const isCreate = !user
  const [form, setForm] = useState<FormState>(() => toForm(user))
  const [showPassword, setShowPassword] = useState(false)
  const [today] = useState(() => format(new Date(), "yyyy-MM-dd"))
  const [localErrors, setLocalErrors] = useState<Record<string, string>>({})
  const create = useCreateUser()
  const update = useUpdateUser()
  const mutation = isCreate ? create : update
  const lanes = useLanes()

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) => setForm((f) => ({ ...f, [key]: value }))

  const serverFields = fieldErrors(mutation.error)
  const errors = { ...serverFields, ...localErrors }
  const generalError =
    mutation.error && Object.keys(serverFields).length === 0 ? errorMessage(mutation.error) : null

  const countryOptions = useMemo<MultiSelectOption[]>(() => {
    const withLanes = new Set((lanes.data ?? []).map((l) => l.countryCode?.toLowerCase()).filter(Boolean))
    return allCountries()
      .map((c) => ({
        value: c.code,
        label: c.name,
        icon: <Flag code={c.code} />,
        keywords: [c.code],
        hint: withLanes.has(c.code) ? "has lanes" : undefined,
      }))
      .sort((a, b) => Number(Boolean(b.hint)) - Number(Boolean(a.hint)))
  }, [lanes.data])

  const laneOptions = useMemo<MultiSelectOption[]>(
    () =>
      (lanes.data ?? [])
        .slice()
        .sort((a, b) => a.name.localeCompare(b.name))
        .map((l) => ({
          value: l.id,
          label: l.name,
          icon: <Flag code={l.countryCode} />,
          hint: l.status,
          keywords: [l.id, l.city ?? "", l.exitIp ?? ""],
        })),
    [lanes.data],
  )

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const errs: Record<string, string> = {}
    const uErr = validateUsername(form.username.trim())
    if (uErr) errs.username = uErr
    if (isCreate && form.password && form.password.length < 8) errs.password = "Use at least 8 characters, or leave empty to generate one."
    if (form.quotaValue && quotaToBytes(form.quotaValue, form.quotaUnit) === 0) errs.quotaBytes = "Enter a positive number, or leave empty for unlimited."
    setLocalErrors(errs)
    if (Object.keys(errs).length) return

    const input = toInput(form, isCreate)
    if (isCreate) {
      create.mutate(input, {
        onSuccess: (res) => {
          onDone()
          onCreated(res)
        },
      })
    } else {
      update.mutate({ id: user.id, input }, { onSuccess: onDone })
    }
  }

  return (
    <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col" noValidate>
      <SheetHeader className="border-b">
        <SheetTitle>{isCreate ? "New proxy user" : `Edit ${user.username}`}</SheetTitle>
        <SheetDescription>
          {isCreate
            ? "Credentials for clients that connect through the proxy."
            : "Changes apply to new connections immediately."}
        </SheetDescription>
      </SheetHeader>

      <div className="min-h-0 flex-1 space-y-6 overflow-y-auto p-4">
        <FormError message={generalError} />

        <FormSection title="Basics">
          <FormField label="Username" htmlFor="u-username" error={errors.username} description="Letters, numbers, dots and underscores.">
            <Input
              id="u-username"
              value={form.username}
              onChange={(e) => set("username", e.target.value)}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={Boolean(errors.username)}
              autoFocus={isCreate}
            />
          </FormField>
          {isCreate ? (
            <FormField
              label="Password"
              htmlFor="u-password"
              error={errors.password}
              description="Leave empty and the server generates one. It is shown once after creating the user."
            >
              <InputGroup>
                <InputGroupInput
                  id="u-password"
                  type={showPassword ? "text" : "password"}
                  value={form.password}
                  onChange={(e) => set("password", e.target.value)}
                  autoComplete="new-password"
                  className="font-mono"
                  aria-invalid={Boolean(errors.password)}
                />
                <InputGroupAddon align="inline-end">
                  <InputGroupButton
                    size="icon-xs"
                    onClick={() => setShowPassword((s) => !s)}
                    aria-label={showPassword ? "Hide password" : "Show password"}
                  >
                    {showPassword ? <EyeOffIcon /> : <EyeIcon />}
                  </InputGroupButton>
                  <InputGroupButton
                    size="xs"
                    onClick={() => {
                      set("password", generatePassword())
                      setShowPassword(true)
                    }}
                  >
                    <DicesIcon /> Generate
                  </InputGroupButton>
                </InputGroupAddon>
              </InputGroup>
            </FormField>
          ) : null}
          <FormField label="Note" htmlFor="u-note" error={errors.note}>
            <Textarea
              id="u-note"
              rows={2}
              value={form.note}
              onChange={(e) => set("note", e.target.value)}
              placeholder="Who or what uses this login"
            />
          </FormField>
          <div className="grid gap-4 sm:grid-cols-2">
            <FormField label="Expires" htmlFor="u-expires" error={errors.expiresAt} description="Empty = never.">
              <div className="flex gap-2">
                <Input
                  id="u-expires"
                  type="date"
                  value={form.expiresAt}
                  min={today}
                  onChange={(e) => set("expiresAt", e.target.value)}
                />
                {form.expiresAt ? (
                  <Button type="button" variant="ghost" size="sm" onClick={() => set("expiresAt", "")}>
                    Clear
                  </Button>
                ) : null}
              </div>
            </FormField>
            <label htmlFor="u-enabled" className="flex items-center justify-between gap-3 rounded-md border p-3 sm:mt-6 sm:h-9 sm:py-0">
              <span className="text-sm font-medium">Enabled</span>
              <Switch id="u-enabled" checked={form.enabled} onCheckedChange={(v) => set("enabled", v)} />
            </label>
          </div>
        </FormSection>

        <Separator />

        <FormSection title="Routing" description="Restrict which lanes this user can exit through. Empty means all.">
          <FormField label="Allowed countries" htmlFor="u-countries" error={errors.allowedCountries}>
            <MultiSelect
              id="u-countries"
              options={countryOptions}
              value={form.allowedCountries}
              onChange={(v) => set("allowedCountries", v)}
              placeholder="All countries"
              searchPlaceholder="Search countries…"
              create={(s) => (validateCountryCode(s.toLowerCase()) ? null : s.toLowerCase())}
            />
          </FormField>
          <FormField label="Allowed lanes" htmlFor="u-lanes" error={errors.allowedLanes}>
            <MultiSelect
              id="u-lanes"
              options={laneOptions}
              value={form.allowedLanes}
              onChange={(v) => set("allowedLanes", v)}
              placeholder="All lanes"
              searchPlaceholder="Search lanes…"
              emptyText={lanes.isPending ? "Loading lanes…" : "No lanes match."}
            />
          </FormField>
          <FormField
            label="Sticky sessions"
            htmlFor="u-sticky"
            unit="minutes"
            error={errors.stickyMinutes}
            description="Keep the same lane for this long. 0 = a new lane for every connection. Clients can override per session with -session-<id>."
          >
            <Input
              id="u-sticky"
              type="number"
              inputMode="numeric"
              min={0}
              value={form.stickyMinutes}
              onChange={(e) => set("stickyMinutes", e.target.value)}
              className="tabular max-w-40"
            />
          </FormField>
        </FormSection>

        <Separator />

        <FormSection title="Limits" description="0 or empty means unlimited.">
          <div className="grid gap-4 sm:grid-cols-2">
            <FormField label="Max connections" htmlFor="u-maxconn" error={errors.maxConnections} description="Concurrent open connections.">
              <Input
                id="u-maxconn"
                type="number"
                inputMode="numeric"
                min={0}
                value={form.maxConnections}
                onChange={(e) => set("maxConnections", e.target.value)}
                className="tabular"
              />
            </FormField>
            <FormField label="Connections per second" htmlFor="u-cps" error={errors.connPerSecond} description="New connections per second.">
              <Input
                id="u-cps"
                type="number"
                inputMode="numeric"
                min={0}
                value={form.connPerSecond}
                onChange={(e) => set("connPerSecond", e.target.value)}
                className="tabular"
              />
            </FormField>
          </div>
          <FormField
            label="Traffic quota"
            htmlFor="u-quota"
            error={errors.quotaBytes}
            description="Upload + download per quota period (see Settings → App). Empty = unlimited."
          >
            <div className="flex max-w-64 gap-2">
              <Input
                id="u-quota"
                type="number"
                inputMode="decimal"
                min={0}
                step="any"
                placeholder="Unlimited"
                value={form.quotaValue}
                onChange={(e) => set("quotaValue", e.target.value)}
                className="tabular"
              />
              <Select value={form.quotaUnit} onValueChange={(v) => set("quotaUnit", v as QuotaUnit)}>
                <SelectTrigger className="w-24" aria-label="Quota unit">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="MB">MB</SelectItem>
                  <SelectItem value="GB">GB</SelectItem>
                  <SelectItem value="TB">TB</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </FormField>
          <label htmlFor="u-logdest" className="flex items-start justify-between gap-4 rounded-md border p-3">
            <span className="space-y-1">
              <span className="block text-sm font-medium">Log destinations</span>
              <span className="block text-xs text-muted-foreground">
                Record the target <span className="font-mono">host:port</span> of each connection in the connection logs.
                Only host and port are stored, never URLs, paths or content. Needed for “Top domains” in Analytics.
              </span>
            </span>
            <Switch id="u-logdest" checked={form.logDestinations} onCheckedChange={(v) => set("logDestinations", v)} />
          </label>
        </FormSection>

        <Separator />

        <FormSection title="Access rules" description="Press Enter, comma or space to add an entry. Paste lists to add many.">
          <FormField label="Allowed domains" htmlFor="u-allow" error={errors.allowDomains} description="Only these domains (and their subdomains with *.) are reachable. Empty = all.">
            <TagInput
              id="u-allow"
              value={form.allowDomains}
              onChange={(v) => set("allowDomains", v)}
              normalize={normalizeDomain}
              validate={validateDomain}
              placeholder="example.com, *.example.org"
            />
          </FormField>
          <FormField label="Denied domains" htmlFor="u-deny" error={errors.denyDomains} description="Always blocked, even if allowed above.">
            <TagInput
              id="u-deny"
              value={form.denyDomains}
              onChange={(v) => set("denyDomains", v)}
              normalize={normalizeDomain}
              validate={validateDomain}
              placeholder="ads.example.com"
            />
          </FormField>
          <FormField label="Allowed client IPs" htmlFor="u-cidrs" error={errors.allowedCidrs} description="IP addresses or CIDR ranges that may use this login. Empty = any.">
            <TagInput
              id="u-cidrs"
              value={form.allowedCidrs}
              onChange={(v) => set("allowedCidrs", v)}
              normalize={(s) => s.trim().toLowerCase()}
              validate={validateCidr}
              placeholder="203.0.113.7, 10.0.0.0/8"
            />
          </FormField>
        </FormSection>
      </div>

      <SheetFooter className="flex-row justify-end gap-2 border-t">
        <Button type="button" variant="outline" onClick={onDone} disabled={mutation.isPending}>
          Cancel
        </Button>
        <Button type="submit" disabled={mutation.isPending}>
          {mutation.isPending ? <Spinner /> : null}
          {isCreate ? "Create user" : "Save changes"}
        </Button>
      </SheetFooter>
    </form>
  )
}
