import { useState, type FormEvent } from "react"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { CopyField } from "@/components/copy-button"
import { FormError, FormField } from "@/components/form-field"
import { ErrorState } from "@/components/states"
import { useCanWrite } from "@/hooks/use-auth"
import { useSaveSettings, useSettings } from "@/hooks/use-settings"
import { errorMessage, fieldErrors } from "@/lib/api"
import { buildProxyUrls } from "@/lib/proxy-urls"
import type { AppSettings } from "@/lib/types"

export function AppSettingsTab() {
  const settings = useSettings()
  if (settings.isPending) return <Skeleton className="h-96 w-full" />
  if (settings.isError) {
    return (
      <Card>
        <ErrorState error={settings.error} onRetry={() => void settings.refetch()} />
      </Card>
    )
  }
  return <AppForm key={JSON.stringify(settings.data.app)} app={settings.data.app} />
}

interface FormState {
  logRetentionDays: string
  eventRetentionDays: string
  publicProxyHost: string
  publicHttpsPort: string
  publicHttpPort: string
  quotaPeriod: AppSettings["quotaPeriod"]
}

function AppForm({ app }: { app: AppSettings }) {
  const canWrite = useCanWrite()
  const save = useSaveSettings()
  const initial: FormState = {
    logRetentionDays: String(app.logRetentionDays),
    eventRetentionDays: String(app.eventRetentionDays),
    publicProxyHost: app.publicProxyHost,
    publicHttpsPort: String(app.publicHttpsPort),
    publicHttpPort: String(app.publicHttpPort),
    quotaPeriod: app.quotaPeriod,
  }
  const [form, setForm] = useState<FormState>(initial)
  const [local, setLocal] = useState<Record<string, string>>({})
  const serverRaw = fieldErrors(save.error)
  const server = Object.fromEntries(Object.entries(serverRaw).map(([k, v]) => [k.replace(/^app\./, ""), v]))
  const errors = { ...server, ...local }
  const general = save.error && Object.keys(serverRaw).length === 0 ? errorMessage(save.error) : null
  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setForm((f) => ({ ...f, [k]: v }))
  const dirty = JSON.stringify(form) !== JSON.stringify(initial)

  const parsed: AppSettings = {
    logRetentionDays: Number(form.logRetentionDays),
    eventRetentionDays: Number(form.eventRetentionDays),
    publicProxyHost: form.publicProxyHost.trim(),
    publicHttpsPort: Number(form.publicHttpsPort || 0),
    publicHttpPort: Number(form.publicHttpPort || 0),
    quotaPeriod: form.quotaPeriod,
  }
  const preview = buildProxyUrls(parsed, "USER", "PASS")

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const errs: Record<string, string> = {}
    const intIn = (k: keyof FormState, min: number, max: number) => {
      const n = Number(form[k])
      if (!Number.isInteger(n) || n < min || n > max) errs[k] = `Enter a whole number from ${min} to ${max}.`
    }
    intIn("logRetentionDays", 1, 3650)
    intIn("eventRetentionDays", 1, 3650)
    intIn("publicHttpsPort", 0, 65535)
    intIn("publicHttpPort", 0, 65535)
    if (parsed.publicProxyHost && !/^[a-z0-9.-]+$|^\[?[0-9a-f:]+\]?$/i.test(parsed.publicProxyHost)) {
      errs.publicProxyHost = "Enter a hostname or IP without scheme or port."
    }
    setLocal(errs)
    if (Object.keys(errs).length) return
    const changed: Partial<AppSettings> = {}
    for (const k of Object.keys(parsed) as (keyof AppSettings)[]) {
      if (parsed[k] !== app[k]) Object.assign(changed, { [k]: parsed[k] })
    }
    save.mutate({ app: changed })
  }

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      <FormError message={general} />
      <fieldset disabled={!canWrite} className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Public proxy address</CardTitle>
            <CardDescription>
              How clients reach the proxy. Used to show ready-made proxy URLs on the Users page; it does not change what
              the server listens on.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-5">
            <FormField label="Public host" htmlFor="app-host" error={errors.publicProxyHost} description="e.g. proxy.example.com">
              <Input
                id="app-host"
                value={form.publicProxyHost}
                onChange={(e) => set("publicProxyHost", e.target.value)}
                placeholder="proxy.example.com"
                className="font-mono"
              />
            </FormField>
            <div className="grid gap-4 sm:grid-cols-2">
              <FormField
                label="HTTPS port"
                htmlFor="app-https"
                error={errors.publicHttpsPort}
                description="443 when TLS is terminated by Traefik. 0 = none."
              >
                <Input
                  id="app-https"
                  type="number"
                  min={0}
                  max={65535}
                  value={form.publicHttpsPort}
                  onChange={(e) => set("publicHttpsPort", e.target.value)}
                  className="tabular"
                />
              </FormField>
              <FormField
                label="HTTP / SOCKS5 port"
                htmlFor="app-http"
                error={errors.publicHttpPort}
                description="Plain port if exposed. 0 = none."
              >
                <Input
                  id="app-http"
                  type="number"
                  min={0}
                  max={65535}
                  value={form.publicHttpPort}
                  onChange={(e) => set("publicHttpPort", e.target.value)}
                  className="tabular"
                />
              </FormField>
            </div>
            <div className="space-y-2">
              <p className="text-xs font-medium text-muted-foreground">Preview</p>
              {preview.length ? (
                preview.map((u) => <CopyField key={u.kind} value={u.url} label={u.label} />)
              ) : (
                <p className="text-xs text-muted-foreground">Set at least one port to generate proxy URLs.</p>
              )}
            </div>
          </CardContent>
        </Card>

        <div className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>Retention</CardTitle>
              <CardDescription>Older records are deleted automatically.</CardDescription>
            </CardHeader>
            <CardContent className="grid gap-4 sm:grid-cols-2">
              <FormField label="Connection logs" htmlFor="app-logret" error={errors.logRetentionDays} description="Also limits “Top domains”.">
                <InputGroup>
                  <InputGroupInput
                    id="app-logret"
                    type="number"
                    min={1}
                    value={form.logRetentionDays}
                    onChange={(e) => set("logRetentionDays", e.target.value)}
                    className="tabular"
                  />
                  <InputGroupAddon align="inline-end">days</InputGroupAddon>
                </InputGroup>
              </FormField>
              <FormField label="Events and audit log" htmlFor="app-evret" error={errors.eventRetentionDays}>
                <InputGroup>
                  <InputGroupInput
                    id="app-evret"
                    type="number"
                    min={1}
                    value={form.eventRetentionDays}
                    onChange={(e) => set("eventRetentionDays", e.target.value)}
                    className="tabular"
                  />
                  <InputGroupAddon align="inline-end">days</InputGroupAddon>
                </InputGroup>
              </FormField>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Quotas</CardTitle>
              <CardDescription>When user traffic quotas reset.</CardDescription>
            </CardHeader>
            <CardContent>
              <FormField label="Quota period" htmlFor="app-quota" error={errors.quotaPeriod}>
                <Select value={form.quotaPeriod} onValueChange={(v) => set("quotaPeriod", v as AppSettings["quotaPeriod"])}>
                  <SelectTrigger id="app-quota" className="w-full sm:w-64">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="monthly">Monthly (resets each month)</SelectItem>
                    <SelectItem value="never">Never (lifetime quota)</SelectItem>
                  </SelectContent>
                </Select>
              </FormField>
            </CardContent>
          </Card>
        </div>
      </fieldset>
      {canWrite ? (
        <div className="flex justify-end gap-2">
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
            Save app settings
          </Button>
        </div>
      ) : null}
    </form>
  )
}
